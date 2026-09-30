package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
	"skifity/internal/store"
	"skifity/internal/tlscert"
)

// A team's own certificates: a company CA's, an EV or OV certificate, a
// wildcard bought elsewhere, or one for a hostname Let's Encrypt cannot
// reach. A hostname one of them covers is served with it instead of one from
// Let's Encrypt; see tlscert for which one and kube/owncert.go for how.
//
// Reading is a viewer's, like the team's registries: a certificate is public —
// every visitor to the site is sent it — and the list says which domains use
// each one, which is what somebody looking at a domain wants to know. The
// private key is never in any answer: it is sealed as it arrives, and no query
// that lists certificates reads it. Uploading and removing are an
// administrator's, because every app in the team is served with what they
// choose.
//
// There is no MCP tool for any of this, on purpose: see internal/mcpserver.

// certificateView is a certificate as the API answers with it.
type certificateView struct {
	store.Certificate
	// State is valid, expiring (within 21 days) or expired.
	State string `json:"state"`
	// Domains are the team's domains served with this certificate: the ones
	// it covers, and for which no other certificate of the team's runs out
	// later.
	Domains []certificateDomain `json:"domains"`
}

type certificateDomain struct {
	ID       string `json:"id"`
	AppID    string `json:"app_id"`
	AppName  string `json:"app_name"`
	Hostname string `json:"hostname"`
}

func (s *Server) handleListCertificates(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	views, err := s.certificateViews(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, views)
}

// certificateViews are a team's certificates with the domains each serves.
func (s *Server) certificateViews(ctx context.Context, teamID string) ([]certificateView, error) {
	certificates, err := s.db.ListCertificates(ctx, teamID)
	if err != nil {
		return nil, err
	}
	domains, err := s.db.ListTeamDomains(ctx, teamID)
	if err != nil {
		return nil, err
	}
	using := map[string][]certificateDomain{}
	for _, domain := range domains {
		// Only what is actually served with one: an internal app has no
		// Ingress, and a domain without HTTPS has no certificate at all.
		if !domain.TLS || domain.Internal {
			continue
		}
		if certificate, ok := store.CertificateFor(certificates, domain.Hostname); ok {
			using[certificate.ID] = append(using[certificate.ID], certificateDomain{
				ID: domain.ID, AppID: domain.AppID, AppName: domain.AppName, Hostname: domain.Hostname,
			})
		}
	}
	now := time.Now()
	views := make([]certificateView, 0, len(certificates))
	for _, certificate := range certificates {
		views = append(views, certificateView{
			Certificate: certificate,
			State:       tlscert.State(certificate.NotAfter, now),
			Domains:     append([]certificateDomain{}, using[certificate.ID]...),
		})
	}
	return views, nil
}

type saveCertificateRequest struct {
	// Name is what the team calls it. Uploading under a name that exists
	// replaces that certificate, which is how a renewal is put in place.
	Name string `json:"name"`
	// Certificate is the chain, PEM: the certificate and its intermediates,
	// in any order.
	Certificate string `json:"certificate"`
	// PrivateKey is the certificate's key, PEM and unencrypted. It is sealed
	// before anything else happens to it and is never sent back.
	PrivateKey string `json:"private_key"`
}

type savedCertificate struct {
	Certificate certificateView `json:"certificate"`
	// Replaced is true when a certificate of the same name was replaced.
	Replaced bool `json:"replaced"`
	// Reordered is true when the chain was pasted in another order and was
	// put leaf first.
	Reordered bool `json:"reordered"`
	// Updating is how many apps are being re-applied to use it, in the
	// background.
	Updating int `json:"updating"`
}

// maxCertificateName bounds a certificate's name, which is shown in a list.
const maxCertificateName = 100

func (s *Server) handleSaveCertificate(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req saveCertificateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxCertificateName || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		writeError(w, r, errdoc.BadRequest("A certificate needs a name of up to 100 characters, such as \"Company wildcard\". "+
			"Uploading under a name that exists replaces that certificate."))
		return
	}
	parsed, err := tlscert.Parse([]byte(req.Certificate), []byte(req.PrivateKey), time.Now())
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.checkCertificateHostnames(r.Context(), teamID, parsed.Hostnames); err != nil {
		writeError(w, r, err)
		return
	}

	// The id is known before the key is sealed, because the id is part of
	// what it is sealed under: a replacement keeps the one it replaces.
	previous, err := s.db.CertificateByName(r.Context(), teamID, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		previous = store.Certificate{ID: store.NewID("crt")}
	case err != nil:
		writeError(w, r, err)
		return
	}
	sealed, err := s.keyring.Seal([]byte(parsed.KeyPEM), store.CertificateContext(teamID, previous.ID))
	if err != nil {
		writeError(w, r, err)
		return
	}
	certificate := store.Certificate{
		ID: previous.ID, TeamID: teamID, Name: name,
		Hostnames: parsed.Hostnames, Subject: parsed.Subject, Issuer: parsed.Issuer,
		NotBefore: parsed.NotBefore, NotAfter: parsed.NotAfter, Fingerprint: parsed.Fingerprint,
		KeyType: parsed.KeyType, SelfSigned: parsed.SelfSigned, ChainLength: parsed.ChainLength,
		ChainPEM: parsed.ChainPEM,
	}
	replaced, err := s.db.SaveCertificate(r.Context(), &certificate, sealed)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, errdoc.Conflict(
				"Another upload of the certificate \""+name+"\" was saved while this one was being read.",
				"Look at the list, and upload again if the one there is not the one you meant."))
			return
		}
		writeError(w, r, err)
		return
	}
	if replaced {
		s.audit(r, teamID, "certificate.replaced", "team", teamID, name)
	} else {
		s.audit(r, teamID, "certificate.saved", "team", teamID, name)
	}

	// Every app whose hostnames the old version or the new one covers: the
	// new one may cover fewer names, and those go back to Let's Encrypt.
	updating := s.syncAppsCoveredBy(r.Context(), teamID, previous.Hostnames, certificate.Hostnames)

	views, err := s.certificateViews(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	answer := savedCertificate{Replaced: replaced, Reordered: parsed.Reordered, Updating: updating}
	for _, view := range views {
		if view.ID == certificate.ID {
			answer.Certificate = view
		}
	}
	status := http.StatusCreated
	if replaced {
		status = http.StatusOK
	}
	writeJSON(w, status, answer)
}

// checkCertificateHostnames refuses a certificate naming a hostname that is
// not this team's to name.
//
// The ingress controller loads every certificate in the cluster into one
// store and picks one by the name a browser asks for. Two certificates with
// the same name are picked between in whatever order it read them, so a
// certificate naming another team's hostname — self-signed, even — would be
// sent to that team's visitors some of the time. An exact name loses to
// nothing but another exact name; a wildcard loses to any exact name, so a
// wildcard is only refused when another team's certificate has the same one.
func (s *Server) checkCertificateHostnames(ctx context.Context, teamID string, hostnames []string) error {
	var exact []string
	for _, hostname := range hostnames {
		if !strings.HasPrefix(hostname, "*.") {
			exact = append(exact, hostname)
		}
	}
	for _, hostname := range exact {
		if s.isPanelHostname(ctx, hostname) {
			return errdoc.CertificateHostnameTaken(hostname)
		}
	}
	taken, err := s.db.HostnamesOfOtherTeams(ctx, teamID, exact)
	if err != nil {
		return err
	}
	if len(taken) > 0 {
		return errdoc.CertificateHostnameTaken(taken[0])
	}
	others, err := s.db.OtherTeamsCertificates(ctx, teamID)
	if err != nil {
		return err
	}
	for _, other := range others {
		for _, name := range other.Hostnames {
			for _, hostname := range hostnames {
				if tlscert.Normalize(name) == hostname {
					return errdoc.CertificateHostnameTaken(hostname)
				}
			}
		}
	}
	return nil
}

// checkNotNamedByAnotherTeam refuses a domain another team's certificate
// names exactly, for the same reason: see checkCertificateHostnames.
func (s *Server) checkNotNamedByAnotherTeam(ctx context.Context, teamID, hostname string) error {
	others, err := s.db.OtherTeamsCertificates(ctx, teamID)
	if err != nil {
		return err
	}
	for _, other := range others {
		for _, name := range other.Hostnames {
			if !strings.HasPrefix(name, "*.") && tlscert.Normalize(name) == hostname {
				return errdoc.DomainNamedByCertificate(hostname)
			}
		}
	}
	return nil
}

func (s *Server) handleDeleteCertificate(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "certificateID")
	certificate, err := s.db.GetCertificate(r.Context(), teamID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, errdoc.NotFound("certificate", id))
			return
		}
		writeError(w, r, err)
		return
	}
	if err := s.db.DeleteCertificate(r.Context(), teamID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, errdoc.NotFound("certificate", id))
			return
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "certificate.deleted", "team", teamID, certificate.Name)
	// Its hostnames go back to Let's Encrypt, or to another of the team's
	// certificates that covers them, and its Secret leaves each namespace.
	updating := s.syncAppsCoveredBy(r.Context(), teamID, certificate.Hostnames)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updating": updating})
}

// Re-applying the apps a certificate change affects.
//
// In the background, because a wildcard can cover every app in a team and each
// re-apply talks to the cluster; the answer says how many are being updated.
// Bounded both ways: a few at a time, so a team of a hundred apps does not
// send a hundred applies at the API server at once, and each for a few
// minutes at most, so a cluster that stopped answering does not hold a
// goroutine for ever.
const (
	certificateSyncWorkers = 3
	certificateSyncTimeout = 3 * time.Minute
)

// syncAppsCoveredBy re-applies, in the background, every app in the team with
// an HTTPS domain one of these sets of names covers, and says how many.
func (s *Server) syncAppsCoveredBy(ctx context.Context, teamID string, names ...[]string) int {
	if s.deployer == nil {
		return 0
	}
	domains, err := s.db.ListTeamDomains(ctx, teamID)
	if err != nil {
		s.log.Warn("could not find the apps a certificate change affects", "team", teamID, "error", err)
		return 0
	}
	var apps []string
	seen := map[string]bool{}
	for _, domain := range domains {
		if !domain.TLS || domain.Internal || seen[domain.AppID] {
			continue
		}
		for _, set := range names {
			if tlscert.CoversAny(set, domain.Hostname) {
				seen[domain.AppID] = true
				apps = append(apps, domain.AppID)
				break
			}
		}
	}
	if len(apps) == 0 {
		return 0
	}

	deployer, log := s.deployer, s.log
	runsafe.Go(log, "re-apply the apps a certificate change affects", func() {
		slots := make(chan struct{}, certificateSyncWorkers)
		var wg sync.WaitGroup
		for _, appID := range apps {
			slots <- struct{}{}
			wg.Add(1)
			go func() {
				defer runsafe.Recover(log, "re-apply an app after a certificate change", nil)
				defer wg.Done()
				defer func() { <-slots }()
				ctx, cancel := context.WithTimeout(context.Background(), certificateSyncTimeout)
				defer cancel()
				if err := deployer.Sync(ctx, appID); err != nil {
					log.Warn("could not apply a certificate change to an app", "app", appID, "error", err)
				}
			}()
		}
		wg.Wait()
	})
	return len(apps)
}

// describeCertificates says, on each HTTPS domain one of the team's own
// certificates covers, which one and until when, and gives the domain the
// certificate's state in place of cert-manager's: there is nothing for
// cert-manager to issue, so "waiting" would never end.
func (s *Server) describeCertificates(ctx context.Context, teamID string, domains []store.Domain) {
	certificates, err := s.db.ListCertificates(ctx, teamID)
	if err != nil || len(certificates) == 0 {
		return
	}
	now := time.Now()
	for i := range domains {
		if !domains[i].TLS {
			continue
		}
		certificate, ok := store.CertificateFor(certificates, domains[i].Hostname)
		if !ok {
			continue
		}
		state := tlscert.State(certificate.NotAfter, now)
		domains[i].Certificate = &store.DomainCertificate{
			ID: certificate.ID, Name: certificate.Name, NotAfter: certificate.NotAfter, State: state,
		}
		domains[i].StatusDetail = ""
		switch state {
		case tlscert.StateValid:
			domains[i].Status = "active"
		default:
			domains[i].Status = state
		}
	}
}
