package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// A domain that redirects to another of its app's: www.example.com to
// example.com, the bare domain to www, an old name to the new one. Coolify
// has the www and bare-domain pair; this is any hostname to any other, with
// the certificate shared. See kube/hostredirect.go.

type domainRedirectRequest struct {
	// RedirectTo is the hostname to send visitors to, or empty to serve the
	// app on this one again.
	RedirectTo string `json:"redirect_to"`
}

func (s *Server) handleSetDomainRedirect(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req domainRedirectRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	domainID := chi.URLParam(r, "domainID")
	var domain *store.Domain
	for i := range domains {
		if domains[i].ID == domainID {
			domain = &domains[i]
		}
	}
	// Only this app's own domains: an id from another app is not found here.
	if domain == nil {
		writeError(w, r, errdoc.NotFound("domain", domainID))
		return
	}
	target := kube.CleanHostname(req.RedirectTo)
	if err := checkRedirect(domains, *domain, target); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.SetDomainRedirect(r.Context(), domain.ID, target); err != nil {
		writeError(w, r, err)
		return
	}
	domain.RedirectTo = target
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not apply the domain's redirect", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	detail := domain.Hostname + " serves the app"
	if target != "" {
		detail = domain.Hostname + " → " + target
	}
	s.audit(r, teamID, "domain.redirect_changed", "app", app.ID, detail)
	writeJSON(w, http.StatusOK, domain)
}

// checkRedirect says whether a domain may redirect to target: another of
// the same app's hostnames, which serves the app itself. A chain — a
// redirect to a redirect, or redirecting a hostname others redirect to — is
// refused rather than rendered as two hops or a loop.
func checkRedirect(domains []store.Domain, domain store.Domain, target string) error {
	if target == "" {
		return nil
	}
	if target == domain.Hostname {
		return errdoc.BadRequest("A domain cannot redirect to itself.")
	}
	found := false
	for _, other := range domains {
		if other.ID == domain.ID {
			continue
		}
		if other.Hostname == target {
			found = true
			if other.RedirectTo != "" {
				return errdoc.BadRequest(target + " redirects to " + other.RedirectTo +
					" itself. Redirect to " + other.RedirectTo + " instead, so visitors get there in one step.")
			}
		}
		if other.RedirectTo == domain.Hostname {
			return errdoc.BadRequest(other.Hostname + " redirects to " + domain.Hostname +
				", so it cannot redirect anywhere itself. Change " + other.Hostname + " first.")
		}
	}
	if !found {
		return errdoc.BadRequest(target + " is not one of this app's domains. " +
			"Add it first; a redirect goes to a hostname the app answers on.")
	}
	return nil
}
