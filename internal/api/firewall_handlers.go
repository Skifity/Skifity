package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/edgerules"
	"skifity/internal/errdoc"
	"skifity/internal/geoip"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// firewallView is an app's rules and everything the page needs to draw the form
// honestly.
type firewallView struct {
	Enabled bool              `json:"enabled"`
	Rules   edgerules.RuleSet `json:"rules"`
	// Installed is whether the component that enforces these is running. Rules
	// saved without it are rules that protect nothing, and the page says so
	// rather than looking like a firewall.
	Installed bool `json:"installed"`
	// Country and Network report which geo databases are configured, which is
	// what decides whether a rule about either can be saved at all.
	Country bool `json:"country_available"`
	Network bool `json:"network_available"`
	// Attribution is what the free databases' licence asks for.
	Attribution    string `json:"attribution,omitempty"`
	AttributionURL string `json:"attribution_url,omitempty"`
	// Hostnames are the addresses these rules will apply to. An app with no
	// domain has no hostname, and a firewall on it guards nothing — which is
	// the sort of thing a page should say out loud.
	Hostnames []string `json:"hostnames"`
}

func (s *Server) handleGetFirewall(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.firewallView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type setFirewallRequest struct {
	Enabled bool              `json:"enabled"`
	Rules   edgerules.RuleSet `json:"rules"`
}

// handleSetFirewall replaces an app's rules.
//
// Admin rather than member: this decides who may reach a running site, and a
// member who can deploy should not be able to lock a team out of its own app or
// quietly open one up.
func (s *Server) handleSetFirewall(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req setFirewallRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := req.Rules.Validate(); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}

	// A rule about a country with no country database is a rule that will be
	// skipped on every request for ever. Refusing it here, while somebody is
	// looking at the form, is the difference between a message and a firewall
	// that looks configured and does nothing.
	if req.Enabled {
		needCountry, needASN := req.Rules.Needs()
		if (needCountry || needASN) && !s.geoAvailable(r) {
			field, which := "ip.country", "country"
			if needASN {
				field, which = "ip.asn", "network"
			}
			writeError(w, r, missingGeoDatabase(which, field))
			return
		}
	}

	encoded, err := json.Marshal(req.Rules)
	if err != nil {
		writeError(w, r, err)
		return
	}
	user, _ := UserFrom(r.Context())
	if err := s.db.SetAppFirewall(r.Context(), store.AppFirewall{
		AppID: app.ID, Enabled: req.Enabled, Rules: string(encoded),
	}, user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "firewall.updated", "app", app.ID, summariseRules(req))

	// Saved is not enforced. The rules have to reach the cluster, and the
	// app's own Ingress has to gain or lose the middleware, or turning the
	// firewall on changes a row in SQLite and nothing else.
	if s.cluster != nil {
		if err := s.cluster.RefreshFirewall(r.Context()); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("the firewall was saved but the app was not re-applied",
				"app", app.ID, "error", err)
		}
	}

	view, err := s.firewallView(r, app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) firewallView(r *http.Request, app store.App) (firewallView, error) {
	stored, err := s.db.GetAppFirewall(r.Context(), app.ID)
	if err != nil {
		return firewallView{}, err
	}
	view := firewallView{Enabled: stored.Enabled, Hostnames: []string{}}
	if err := json.Unmarshal([]byte(stored.Rules), &view.Rules); err != nil {
		// Rules that will not parse are reported as none rather than as an
		// error: the page has to open so that somebody can fix them.
		s.log.Error("an app's firewall rules could not be read", "app", app.ID, "error", err)
		view.Rules = edgerules.RuleSet{}
	}

	domains, err := s.db.ListDomains(r.Context(), app.ID)
	if err != nil {
		return view, err
	}
	for _, domain := range domains {
		view.Hostnames = append(view.Hostnames, domain.Hostname)
	}

	if s.cluster != nil {
		if component, err := s.cluster.ComponentStatus(r.Context(), "firewall"); err == nil {
			view.Installed = component.Status == "installed"
		}
	}
	view.Country = s.geoAvailable(r)
	view.Network = view.Country
	if view.Country {
		view.Attribution = geoip.Attribution
		view.AttributionURL = geoip.AttributionURL
	}
	return view, nil
}

// geoAvailable reports whether the firewall can be told a visitor's country and
// network at all.
//
// The panel cannot see the guard's disk, so this is whether the lookup is
// switched on rather than whether a file finished downloading. That is the
// honest thing to check while somebody is saving a rule: switched off is a
// question that will certainly never be answered, and a download that is
// failing shows up as the rule being skipped, by name, in the guard's log.
//
// Default on. The free databases need no account, so the ordinary install can
// write a rule about a country without configuring anything first.
func (s *Server) geoAvailable(r *http.Request) bool {
	value, _, err := s.db.GetSetting(r.Context(), settings.KeyGeoEnabled)
	if err != nil || value == "" {
		return true
	}
	return value != "false"
}

func missingGeoDatabase(which, field string) error {
	return errdoc.Newf("firewall.no_geo_database", "This panel does not look up a visitor's %s", which).
		WithCause("A rule tests %s, and looking up a visitor's country and network is switched off.", field).
		WithImpact("Nothing was saved. A rule that cannot be evaluated is skipped on every request, " +
			"so it would have looked configured and protected nothing.").
		WithFix("Turn on \"Look up a visitor's country and network\" under Settings, then Domains and HTTPS. " +
			"It uses DB-IP's free monthly databases, which need no account.").
		WithStatus(http.StatusBadRequest)
}

// summariseRules names the rules and never their values.
//
// A list of addresses in a rule is somebody's home connection, and an audit log
// is read by more people than the rules are.
func summariseRules(req setFirewallRequest) string {
	if !req.Enabled {
		return "off"
	}
	if len(req.Rules.Rules) == 0 {
		return "on, no rules"
	}
	names := make([]string, 0, len(req.Rules.Rules))
	for _, rule := range req.Rules.Rules {
		names = append(names, rule.Name)
	}
	return "on: " + strings.Join(names, ", ")
}
