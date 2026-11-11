package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/manifests"
	"skifity/internal/runsafe"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Putting the panel on a domain of its own, from Settings → General.
//
// A default install answers on an sslip.io address over plain HTTP, on
// purpose (ADR-0015): the password crosses in clear, and the __Host- cookies
// and passkeys need HTTPS. Running the installer again with --domain was the
// only way out, and nothing in the panel said so. This is the way from inside.
//
// A move is slow and ends by restarting the panel, so it runs beside the
// request and is recorded as it goes (store.PanelDomain):
//
//  1. cert-manager is installed if it is not, with its issuer.
//  2. The panel's route, the Ingress the installer reads back, is written for
//     the domain, with the address the panel answered on kept as a second
//     rule. The panel answers on both from here.
//  3. The certificate is waited for. A move that does not get one stops here,
//     and the panel is where it was.
//  4. The Panel URL setting is written, and the panel's Deployment is told its
//     new address, which restarts it there. The panel that starts finishes the
//     move (SettlePanelDomain).
//
// The address the panel leaves keeps answering, for the webhooks Git hosts
// were given there: a provider does not follow a redirect on a POST. A browser
// that opens it is sent to the domain instead (sendOnFromLeftAddresses).

// PanelMove is a domain the panel is put on, and the hosts its route keeps
// answering on beside it, oldest first.
type PanelMove struct {
	Domain   string
	Previous []string
}

// PanelCertificate is the certificate of the panel's own domain, as
// cert-manager has it.
type PanelCertificate struct {
	// State is none, issuing or ready.
	State string `json:"state"`
	// Message is what cert-manager said last, while it is not ready.
	Message  string     `json:"message,omitempty"`
	NotAfter *time.Time `json:"not_after,omitempty"`
}

// The states of a PanelCertificate.
const (
	PanelCertificateNone    = "none"
	PanelCertificateIssuing = "issuing"
	PanelCertificateReady   = "ready"
)

// PanelRoute is the panel's own Ingress as it stands.
type PanelRoute struct {
	// Hosts are the hosts it answers on, the panel's own first.
	Hosts []string
	// Secrets says, for each host with a certificate, the Secret it is in.
	Secrets map[string]string
	// Certificate is the certificate of the first host, when it asks for one.
	Certificate PanelCertificate
}

// PanelMover is what moving the panel asks of the cluster. The cluster adapter
// has it; a panel with no cluster, or a test's, does not, and the routes say
// the panel cannot move itself.
type PanelMover interface {
	// PanelRoute reads the panel's Ingress and its certificate. A panel
	// whose route is not the installer's answers panel.domain_unsupported.
	PanelRoute(ctx context.Context) (PanelRoute, error)
	// SetPanelDomain does the four steps above. It returns once the panel has
	// been told to restart at the domain, or with why it was not.
	SetPanelDomain(ctx context.Context, move PanelMove) error
	// WritePanelRoute writes the route for one host, with a certificate or
	// over plain HTTP, and the earlier hosts after it. Nothing restarts.
	WritePanelRoute(ctx context.Context, host string, tls bool, previous []string) error
	// ReturnPanel puts the panel back on a host over plain HTTP and restarts
	// it there; current is the route to put back if that cannot be done.
	ReturnPanel(ctx context.Context, host string, current PanelMove) error
	// KeepPanelRoute puts the earlier hosts back on the route when something
	// took them off, and says whether it had to.
	KeepPanelRoute(ctx context.Context, move PanelMove) (bool, error)
}

// panelMoves is what the API keeps about moving the panel between requests.
// The zero value is ready to use.
type panelMoves struct {
	// mu keeps two requests from starting two moves.
	mu sync.Mutex
	// left is the addresses a browser is sent on from, read from the database
	// when first needed and again whenever the move changes.
	left atomic.Pointer[leftAddresses]
}

// leftAddresses are the hosts the panel answered on before its domain, and
// the address it answers on now.
type leftAddresses struct {
	hosts  map[string]bool
	target string
}

// panelMoveTimeout bounds a whole move: cert-manager's own install can take
// five minutes on a small server, and the certificate as long again.
const panelMoveTimeout = 20 * time.Minute

// mover is the cluster's PanelMover, or a problem saying why there is none.
func (s *Server) mover() (PanelMover, error) {
	if s.cluster == nil {
		return nil, errdoc.PanelDomainUnsupported("The panel is not connected to a cluster, so it has no route of its own to change.")
	}
	mover, ok := s.cluster.(PanelMover)
	if !ok {
		return nil, errdoc.PanelDomainUnsupported("The panel is not running inside a cluster it can change, which is normal in development.")
	}
	return mover, nil
}

// panelDomainView is what Settings → General → Panel address shows.
type panelDomainView struct {
	// URL is where this panel answers: the address it was started with.
	URL    string `json:"url"`
	Host   string `json:"host"`
	Scheme string `json:"scheme"`
	// Domain is the domain set here: being moved to, answered on, or a move
	// to it that stopped. Empty for a panel never moved from Settings, which
	// includes one the installer put on a domain.
	Domain string `json:"domain,omitempty"`
	// State is moving, restarting, live or failed, with Domain.
	State string `json:"state,omitempty"`
	// Problem is why a move stopped.
	Problem   json.RawMessage `json:"problem,omitempty"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	// Previous are the addresses the panel keeps answering on for the
	// webhooks registered there; a browser that opens one is sent here.
	Previous []string `json:"previous"`
	// Certificate is the certificate of the first host of the panel's
	// route, when the panel could read it.
	Certificate *PanelCertificate `json:"certificate,omitempty"`
	// DNSTarget is the address a record for the domain points at, and
	// DNSTargetIPv6 the IPv6 one beside it when the cluster has one.
	DNSTarget     string `json:"dns_target"`
	DNSTargetIPv6 string `json:"dns_target_ipv6,omitempty"`
	// FreeAddress is the sslip.io address taking the domain off would put
	// the panel back on, when there is one.
	FreeAddress string `json:"free_address,omitempty"`
	// Movable is whether the panel can change its own address: it runs in
	// the cluster it manages, behind the route the installer made.
	Movable bool `json:"movable"`
	// Reason is why not, when it cannot.
	Reason *errdoc.Problem `json:"reason,omitempty"`
}

// handleGetPanelDomain says where the panel answers and where moving it
// stands. The panel administrator's, like every panel-wide setting.
func (s *Server) handleGetPanelDomain(w http.ResponseWriter, r *http.Request) {
	view, err := s.panelDomainView(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) panelDomainView(ctx context.Context) (panelDomainView, error) {
	url := s.runningURL()
	host, scheme := hostOf(url), "http"
	if strings.HasPrefix(url, "https://") {
		scheme = "https"
	}
	view := panelDomainView{URL: url, Host: host, Scheme: scheme, Previous: []string{}}

	row, err := s.db.GetPanelDomain(ctx)
	switch {
	case err == nil:
		view.Domain, view.State, view.Problem = row.Domain, row.State, row.Problem
		started := row.StartedAt
		view.StartedAt = &started
		for _, previous := range row.Previous {
			if previous != host {
				view.Previous = append(view.Previous, previous)
			}
		}
	case !errors.Is(err, store.ErrNotFound):
		return view, err
	}

	if s.cluster != nil {
		target, ipv6 := s.publicAddress(ctx, ""), s.clusterIPv6(ctx)
		view.DNSTarget = target
		if target != "" && ipv6 != target {
			view.DNSTargetIPv6 = ipv6
		}
	}
	mover, err := s.mover()
	if err != nil {
		view.Reason = errdoc.From(err)
		return view, nil
	}
	route, err := mover.PanelRoute(ctx)
	if err != nil {
		view.Reason = errdoc.From(err)
		return view, nil
	}
	view.Movable = true
	if len(route.Hosts) > 0 && route.Secrets[route.Hosts[0]] != "" {
		certificate := route.Certificate
		view.Certificate = &certificate
	}
	view.FreeAddress = s.freeAddress(ctx, row.Previous, route.Hosts)
	return view, nil
}

// runningURL is the address this panel was started with. A move ends by
// restarting the panel with a new one, so it is the address the panel is at,
// where the Panel URL setting is what links say.
func (s *Server) runningURL() string {
	return strings.TrimSuffix(strings.TrimSpace(s.cfg.PublicURL), "/")
}

type panelDomainRequest struct {
	Domain string `json:"domain"`
	// IgnoreDNS moves the panel although its DNS check says the domain does
	// not point here: it reaches the server some way the panel cannot see.
	IgnoreDNS bool `json:"ignore_dns,omitempty"`
}

// cleanPanelDomain is a domain the panel can be put on, or why not.
func cleanPanelDomain(raw string) (string, error) {
	domain := kube.CleanHostname(raw)
	switch {
	case domain == "":
		return "", errdoc.BadRequest("Enter the domain the panel should answer on, such as panel.example.com.")
	case strings.Contains(domain, "*"):
		return "", errdoc.BadRequest("A wildcard cannot be the panel's address. Name one host, such as panel.example.com.")
	case net.ParseIP(strings.Trim(domain, "[]")) != nil:
		return "", errdoc.BadRequest("An IP address cannot be given a certificate by Let's Encrypt. " +
			"Use a domain name that points at it, such as panel.example.com.")
	case !kube.ValidHostname(domain):
		return "", errdoc.BadRequest("That does not look like a domain name. Enter something like panel.example.com.")
	case manifests.FreeAddress(domain):
		return "", errdoc.BadRequest("An sslip.io name is shared by every Skifity install, and so is Let's Encrypt's limit " +
			"on certificates for it (ADR-0015). Use a domain of your own.")
	}
	return domain, nil
}

// checkPanelDomainFree refuses a domain an app answers on.
func (s *Server) checkPanelDomainFree(ctx context.Context, domain string) error {
	taken, err := s.db.HostnameIsAnAppsDomain(ctx, domain)
	if err != nil {
		return err
	}
	if taken {
		return errdoc.PanelDomainTaken(domain)
	}
	return nil
}

// panelDNSTargets is every address the panel's domain can point at: the one
// the panel asks for first, then each ready server's own. Traefik answers on
// every server, so a record naming any of them reaches the panel.
func (s *Server) panelDNSTargets(ctx context.Context) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	add(s.publicAddress(ctx, ""))
	add(s.clusterIPv6(ctx))
	if servers, err := s.db.ListAllServers(ctx); err == nil {
		for _, server := range servers {
			if server.Status == store.ServerReady {
				add(server.ExternalIP)
			}
		}
	}
	return out
}

// handleCheckPanelDomain answers whether a domain's DNS points at this server,
// before the panel is moved to it.
func (s *Server) handleCheckPanelDomain(w http.ResponseWriter, r *http.Request) {
	var req panelDomainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	domain, err := cleanPanelDomain(req.Domain)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dnsCheckTimeout)
	defer cancel()
	check, err := checkDNS(ctx, s.resolver, domain, s.panelDNSTargets(ctx))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, check)
}

// handleSetPanelDomain starts moving the panel to a domain, and answers with
// where that stands; the rest happens beside the request.
//
// Not behind requireRecentAuth: ADR-0020 keeps that for what turns a borrowed
// session into access kept, and moving the panel gives nobody anything. It
// signs everybody out, the person who asked included.
func (s *Server) handleSetPanelDomain(w http.ResponseWriter, r *http.Request) {
	var req panelDomainRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	domain, err := cleanPanelDomain(req.Domain)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.checkPanelDomainFree(r.Context(), domain); err != nil {
		writeError(w, r, err)
		return
	}
	mover, err := s.mover()
	if err != nil {
		writeError(w, r, err)
		return
	}

	s.moves.mu.Lock()
	defer s.moves.mu.Unlock()
	row, err := s.db.GetPanelDomain(r.Context())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, r, err)
		return
	}
	if row.State == store.PanelDomainMoving || row.State == store.PanelDomainRestarting {
		writeError(w, r, errdoc.PanelDomainBusy(row.Domain))
		return
	}
	route, err := mover.PanelRoute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	current := hostOf(s.runningURL())
	if current == "" && len(route.Hosts) > 0 {
		current = route.Hosts[0]
	}
	if current == domain {
		writeError(w, r, errdoc.BadRequest("The panel answers at "+domain+" already."))
		return
	}

	// Asked of DNS first, because Let's Encrypt asks the same question, and
	// a move to a name that does not point here only waits five minutes for
	// a certificate that cannot come. A check that cannot get an answer is
	// not a reason to refuse: the certificate's wait says the rest.
	if !req.IgnoreDNS {
		ctx, cancel := context.WithTimeout(r.Context(), dnsCheckTimeout)
		targets := s.panelDNSTargets(ctx)
		check, err := checkDNS(ctx, s.resolver, domain, targets)
		cancel()
		if err == nil && (check.Status == DNSMissing || check.Status == DNSElsewhere || check.Status == DNSPartly) {
			target := "the server's public address"
			if len(targets) > 0 {
				target = targets[0]
			}
			writeError(w, r, errdoc.PanelDomainDNSMismatch(domain, foundAddresses(check), target))
			return
		}
	}

	// What the panel answered on keeps answering: the hosts kept by an
	// earlier move, and the one it is at now.
	previous := []string{}
	for _, host := range append(slices.Clone(row.Previous), current) {
		if host != "" && host != domain && !slices.Contains(previous, host) {
			previous = append(previous, host)
		}
	}
	user, _ := UserFrom(r.Context())
	move := store.PanelDomain{Domain: domain, Previous: previous, State: store.PanelDomainMoving, StartedBy: user.Email}
	if err := s.db.StartPanelDomain(r.Context(), move); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "panel.domain_set", "panel", "domain", domain)
	s.forgetLeftAddresses()

	runsafe.Go(s.log, "moving the panel to its domain", func() {
		ctx, cancel := context.WithTimeout(context.Background(), panelMoveTimeout)
		defer cancel()
		err := mover.SetPanelDomain(ctx, PanelMove{Domain: domain, Previous: previous})
		if err == nil {
			// The panel is restarting at the domain; the one that starts
			// there finishes the move.
			return
		}
		var problem *errdoc.Problem
		if !errors.As(err, &problem) {
			problem = errdoc.PanelDomainMoveFailed(domain, err)
		}
		s.log.Warn("the panel was not moved to its domain", "domain", domain, "code", problem.Code, "error", err)
		recorded, _ := problem.JSON()
		if err := s.db.SetPanelDomainState(context.WithoutCancel(ctx), store.PanelDomainFailed, problemOf(recorded)); err != nil {
			s.log.Warn("could not record that moving the panel stopped", "error", err)
		}
	})

	view, err := s.panelDomainView(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// problemOf is the Problem inside the {"error": ...} a problem's JSON is,
// which is what a move records and the view hands back.
func problemOf(body []byte) json.RawMessage {
	var wrapped struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) != nil || len(wrapped.Error) == 0 {
		return nil
	}
	return wrapped.Error
}

// foundAddresses says what a DNS check found, for a sentence.
func foundAddresses(check DNSCheck) string {
	values := []string{}
	for _, record := range check.Found {
		if record.Type != "CNAME" {
			values = append(values, record.Value)
		}
	}
	if len(values) == 0 {
		return "nothing"
	}
	return strings.Join(values, ", ")
}

// handleRemovePanelDomain takes the panel's domain off.
//
// A move that stopped is cancelled: the route goes back to what the panel
// answers on, and nothing restarts. A panel at its domain goes back to its
// sslip.io address over plain HTTP, and restarts there.
func (s *Server) handleRemovePanelDomain(w http.ResponseWriter, r *http.Request) {
	mover, err := s.mover()
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.moves.mu.Lock()
	defer s.moves.mu.Unlock()
	row, err := s.db.GetPanelDomain(r.Context())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, r, err)
		return
	}
	if row.State == store.PanelDomainMoving || row.State == store.PanelDomainRestarting {
		writeError(w, r, errdoc.PanelDomainBusy(row.Domain))
		return
	}
	route, err := mover.PanelRoute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	url := s.runningURL()
	current := hostOf(url)
	secure := strings.HasPrefix(url, "https://")
	if current == "" {
		writeError(w, r, errdoc.PanelDomainUnsupported("The panel was started without SKIFITY_PUBLIC_URL, so it does not know the address it answers on."))
		return
	}
	kept := []string{}
	for _, host := range row.Previous {
		if host != current {
			kept = append(kept, host)
		}
	}

	if row.State == store.PanelDomainFailed {
		// The panel never left: the route goes back to what it answers on.
		if err := mover.WritePanelRoute(r.Context(), current, secure, kept); err != nil {
			writeError(w, r, err)
			return
		}
		if secure && len(kept) > 0 {
			err = s.db.StartPanelDomain(r.Context(), store.PanelDomain{
				Domain: current, Previous: kept, State: store.PanelDomainLive, StartedBy: row.StartedBy,
			})
		} else {
			err = s.db.DeletePanelDomain(r.Context())
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.audit(r, "", "panel.domain_cancelled", "panel", "domain", row.Domain)
		s.forgetLeftAddresses()
		view, err := s.panelDomainView(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}

	if !secure {
		writeError(w, r, errdoc.BadRequest("The panel has no domain of its own to take off: it answers at "+url+"."))
		return
	}
	free := s.freeAddress(r.Context(), row.Previous, route.Hosts)
	if free == "" {
		writeError(w, r, errdoc.PanelDomainNoFreeAddress())
		return
	}
	if err := mover.ReturnPanel(r.Context(), free, PanelMove{Domain: current, Previous: kept}); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.DeletePanelDomain(r.Context()); err != nil {
		s.log.Warn("could not forget the panel's domain", "error", err)
	}
	s.audit(r, "", "panel.domain_removed", "panel", "domain", current)
	s.forgetLeftAddresses()
	writeJSON(w, http.StatusAccepted, panelReturned{URL: "http://" + free, Restarting: true})
}

// panelReturned is what taking the domain off answers: the panel is
// restarting at the address it goes back to, which the browser has to open.
type panelReturned struct {
	URL        string `json:"url"`
	Restarting bool   `json:"restarting"`
}

// freeAddress is the sslip.io address the panel goes back to without its
// domain: the newest one it answered on, else the one its route still has,
// else one made from the cluster's public IP the way the installer makes it.
func (s *Server) freeAddress(ctx context.Context, previous, route []string) string {
	for i := len(previous) - 1; i >= 0; i-- {
		if manifests.FreeAddress(previous[i]) && net.ParseIP(previous[i]) == nil {
			return previous[i]
		}
	}
	for _, host := range route {
		if manifests.FreeAddress(host) && net.ParseIP(host) == nil {
			return host
		}
	}
	ip := net.ParseIP(s.publicAddress(ctx, ""))
	if ip == nil {
		return ""
	}
	// install.sh: tr '.:' '--'.
	return strings.NewReplacer(".", "-", ":", "-").Replace(ip.String()) + ".sslip.io"
}

// SettlePanelDomain finishes a move the restart interrupted, at start-up.
//
// A move ends by restarting the panel at its domain, so the panel that
// started it never sees it through: the one that starts next does, here. One
// that started at the domain has moved; one that started anywhere else has
// not, and says so. A move the restart caught before that point stopped, and
// is recorded as stopped rather than left looking busy for ever. And a panel
// the installer put somewhere else since forgets the domain set here.
func (s *Server) SettlePanelDomain(ctx context.Context) {
	defer s.forgetLeftAddresses()
	row, err := s.db.GetPanelDomain(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		s.log.Warn("could not read the panel's domain", "error", err)
		return
	}
	url := s.runningURL()
	current := hostOf(url)
	if current == "" {
		return
	}
	record := func(state string, problem *errdoc.Problem) {
		var recorded json.RawMessage
		if problem != nil {
			body, _ := problem.JSON()
			recorded = problemOf(body)
		}
		if err := s.db.SetPanelDomainState(ctx, state, recorded); err != nil {
			s.log.Warn("could not record where moving the panel stands", "state", state, "error", err)
		}
	}

	switch row.State {
	case store.PanelDomainMoving:
		s.log.Warn("the panel restarted while it was being moved, so the move stopped", "domain", row.Domain)
		record(store.PanelDomainFailed, errdoc.PanelDomainInterrupted(row.Domain))
	case store.PanelDomainRestarting:
		if current != row.Domain || !strings.HasPrefix(url, "https://") {
			s.log.Warn("the panel was told to restart at its domain and started elsewhere", "domain", row.Domain, "address", url)
			record(store.PanelDomainFailed, errdoc.PanelDomainNotRestarted(row.Domain, url))
			return
		}
		// Written before the restart too; again here, in case that was the
		// write that did not happen.
		if err := s.db.SetSetting(ctx, settings.KeyPanelURL, url, false, "system"); err != nil {
			s.log.Warn("could not record the panel's new address", "error", err)
		}
		record(store.PanelDomainLive, nil)
		s.log.Info("the panel answers at its domain now", "url", url)
	case store.PanelDomainLive:
		if current != row.Domain {
			// Run again with --domain, or moved by hand: the domain set here
			// is not where the panel is any more.
			s.log.Info("the panel was put on another address since its domain was set in Settings, so that domain is forgotten",
				"domain", row.Domain, "address", url)
			if err := s.db.DeletePanelDomain(ctx); err != nil {
				s.log.Warn("could not forget the panel's domain", "error", err)
			}
		}
	}
}

// KeepPanelRoute puts the addresses the panel left back on its route, when
// something took them off.
//
// Running the installer again applies its own route, with the domain alone in
// it, and the webhooks registered at the address before stop arriving. The
// panel looks at start-up and every few minutes, which costs one read.
func (s *Server) KeepPanelRoute(ctx context.Context) {
	row, err := s.db.GetPanelDomain(ctx)
	if err != nil || row.State != store.PanelDomainLive || len(row.Previous) == 0 || hostOf(s.runningURL()) != row.Domain {
		return
	}
	mover, err := s.mover()
	if err != nil {
		return
	}
	restored, err := mover.KeepPanelRoute(ctx, PanelMove{Domain: row.Domain, Previous: row.Previous})
	switch {
	case err != nil:
		s.log.Warn("could not check the panel's route for the addresses it keeps", "error", err)
	case restored:
		s.log.Info("put the addresses the panel answered on before back on its route", "domain", row.Domain)
	}
}

// forgetLeftAddresses makes the next request read the addresses the panel
// left again.
func (s *Server) forgetLeftAddresses() { s.moves.left.Store(nil) }

// leftAddresses are the hosts the panel answered on before its domain, which
// a browser is sent on from, and where to.
func (s *Server) leftAddresses(ctx context.Context) *leftAddresses {
	if left := s.moves.left.Load(); left != nil {
		return left
	}
	left := &leftAddresses{hosts: map[string]bool{}}
	if s.db != nil {
		if row, err := s.db.GetPanelDomain(ctx); err == nil {
			current := hostOf(s.runningURL())
			for _, host := range row.Previous {
				if host != current {
					left.hosts[host] = true
				}
			}
			left.target = s.runningURL()
		}
	}
	if left.target == "" {
		left.hosts = map[string]bool{}
	}
	s.moves.left.Store(left)
	return left
}

// samePanelPath is the path and query a browser asked for, to be put after
// the panel's own address. Always a path: RequestURI gives back the opaque
// part of a request line such as "x:.example.net/", which a client can send
// and a browser never does, and joined onto the address that would have named
// another host.
func samePanelPath(u *url.URL) string {
	path := u.EscapedPath()
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}

// sendOnFromLeftAddresses answers a browser at an address the panel moved
// away from with the address it is at now.
//
// A page is sent on with a 308, path and all. A request to the API from a
// browser, which carries a cookie or says where it came from, is answered
// with panel.domain_moved and the address, and the panel's own client opens
// it: a session cookie set at the old address is not the new address's, and
// a fetch that followed a redirect there would be refused as another origin.
//
// Everything else is served where it is: a Git host's webhook, which does
// not follow a redirect on a POST, and a request with an API token — the CLI,
// an assistant, a script — which was given this address and works at it.
func (s *Server) sendOnFromLeftAddresses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		left := s.leftAddresses(r.Context())
		if len(left.hosts) == 0 || !left.hosts[hostOf(r.Host)] ||
			strings.HasPrefix(r.URL.Path, "/api/webhooks/") || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, left.target+samePanelPath(r.URL), http.StatusPermanentRedirect)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Origin") != "" || len(r.Cookies()) > 0 {
			writeError(w, r, errdoc.PanelMoved(left.target))
			return
		}
		next.ServeHTTP(w, r)
	})
}
