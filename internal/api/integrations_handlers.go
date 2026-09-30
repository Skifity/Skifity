package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/notify"
	"skifity/internal/store"
	"skifity/internal/templates"
	"skifity/internal/version"
)

// --- git sources ---

func (s *Server) handleListGitSources(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	sources, err := s.db.ListGitSources(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, sources)
}

type createGitSourceRequest struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
	Account string `json:"account,omitempty"`
	// Token is a personal access token for the GitLab, Gitea and GitHub PAT kinds.
	Token string `json:"token,omitempty"`
}

func (s *Server) handleCreateGitSource(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req createGitSourceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	// github_app was in this list and is not any more. A GitHub App needs a
	// private key signed into a JWT and exchanged for an installation token,
	// and none of that was ever written — so the kind was accepted, could not
	// clone a private repository, and had five settings behind it that nothing
	// read. A personal access token is the path that works.
	switch req.Kind {
	case "github_pat", "gitlab", "gitea", "generic":
	default:
		writeError(w, r, errdoc.BadRequest("Kind must be github_pat, gitlab, gitea or generic."))
		return
	}
	if req.Kind != "generic" && strings.TrimSpace(req.Token) == "" {
		writeError(w, r, errdoc.BadRequest("A token is needed so Skifity can read the repository and register a webhook."))
		return
	}

	// The panel's own process makes requests to this address, so it is checked
	// here rather than trusted later.
	baseURL, err := gitsrc.ValidateBaseURL(req.BaseURL)
	if err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}

	source := store.GitSource{
		TeamID:  teamID,
		Kind:    req.Kind,
		Name:    defaultString(req.Name, req.Kind),
		BaseURL: baseURL,
		Account: strings.TrimSpace(req.Account),
	}
	if req.Token != "" {
		// A webhook secret is generated per source so one leaked secret cannot
		// be replayed against another.
		config, err := json.Marshal(map[string]string{"token": req.Token})
		if err != nil {
			writeError(w, r, err)
			return
		}
		sealed, err := s.keyring.Seal(config, "git_source:"+teamID+":"+source.Name)
		if err != nil {
			writeError(w, r, err)
			return
		}
		source.ConfigEnc = sealed
	}
	if err := s.db.CreateGitSource(r.Context(), &source); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "git_source.created", "git_source", source.ID, source.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"source":      source,
		"webhook_url": s.webhookURL(r, source.ID),
	})
}

func (s *Server) handleDeleteGitSource(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	sourceID := chi.URLParam(r, "sourceID")
	source, err := s.db.GetGitSource(r.Context(), sourceID)
	if err != nil || source.TeamID != teamID {
		writeError(w, r, errdoc.NotFound("git source", sourceID))
		return
	}
	if err := s.db.DeleteGitSource(r.Context(), sourceID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "git_source.deleted", "git_source", sourceID, source.Name)
	writeOK(w)
}

func (s *Server) webhookURL(r *http.Request, sourceID string) string {
	base := s.cfg.PublicURL
	if base == "" {
		scheme := "https"
		if r.TLS == nil && s.cfg.DevMode {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	return strings.TrimSuffix(base, "/") + "/api/webhooks/git/" + sourceID
}

// --- notification channels ---

func (s *Server) handleListNotificationChannels(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	channels, err := s.db.ListNotificationChannels(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, channels)
}

// handleListNotificationKinds is what the form is built from.
//
// It exists because a plugin that provides a channel and is never offered is a
// plugin that does nothing: the panel used to have the four kinds written into
// the frontend, so a provided one could be stored through the API and never
// picked by a person. The built-in kinds are returned with no fields, because
// the panel has their forms already and has translated them; a provided kind
// carries its own form, in the plugin author's English.
func (s *Server) handleListNotificationKinds(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	kinds := notify.BuiltInKinds()
	if s.channels != nil {
		provided, err := s.channels.Kinds(r.Context())
		if err != nil {
			// The built-in kinds still work, and a person opening this form
			// should not be stopped by a plugin the panel could not read.
			s.log.Warn("the channels plugins provide could not be listed", "error", err)
		}
		kinds = append(kinds, provided...)
	}
	writeList(w, kinds)
}

type createChannelRequest struct {
	Kind   string            `json:"kind"`
	Name   string            `json:"name"`
	Config map[string]string `json:"config"`
	Events []string          `json:"events"`
}

func (s *Server) handleCreateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	var req createChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := notify.ValidateConfig(r.Context(), req.Kind, req.Config, s.channels); err != nil {
		writeError(w, r, errdoc.BadRequest(err.Error()))
		return
	}
	raw, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, r, err)
		return
	}
	channel := store.NotificationChannel{
		TeamID:  teamID,
		Kind:    req.Kind,
		Name:    defaultString(req.Name, req.Kind),
		Events:  strings.Join(req.Events, ","),
		Enabled: true,
	}
	sealed, err := s.keyring.Seal(raw, "notification_channel:"+teamID+":"+channel.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	channel.ConfigEnc = sealed
	if err := s.db.CreateNotificationChannel(r.Context(), &channel); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "notification.created", "channel", channel.ID, channel.Name)
	writeJSON(w, http.StatusCreated, channel)
}

func (s *Server) handleDeleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	channelID := chi.URLParam(r, "channelID")
	channel, err := s.db.GetNotificationChannel(r.Context(), channelID)
	if err != nil || channel.TeamID != teamID {
		writeError(w, r, errdoc.NotFound("notification channel", channelID))
		return
	}
	if err := s.db.DeleteNotificationChannel(r.Context(), channelID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "notification.deleted", "channel", channelID, channel.Name)
	writeOK(w)
}

func (s *Server) handleTestNotificationChannel(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	channelID := chi.URLParam(r, "channelID")
	channel, err := s.db.GetNotificationChannel(r.Context(), channelID)
	if err != nil || channel.TeamID != teamID {
		writeError(w, r, errdoc.NotFound("notification channel", channelID))
		return
	}
	raw, err := s.keyring.Open(channel.ConfigEnc, "notification_channel:"+teamID+":"+channel.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		writeError(w, r, err)
		return
	}

	message := notify.Message{
		Title: version.Name + " test notification",
		Body:  "If you are reading this, notifications from " + version.Name + " are working.",
		Level: "info",
	}
	if err := notify.Send(r.Context(), channel.Kind, config, message, s.channels); err != nil {
		writeError(w, r, errdoc.New("notification.test_failed", "The test message could not be sent").
			WithCause("%s", err.Error()).
			WithImpact("This channel will not deliver notifications until it works.").
			WithFix("Check the webhook URL or token, and that this server can reach the service.").
			WithStatus(http.StatusBadGateway).Retry())
		return
	}
	writeOK(w)
}

// --- templates ---

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	writeList(w, templates.All())
}

// handleTemplateIcon serves a template's logo out of the binary.
//
// Out of the binary rather than from a CDN: the panel's own policy is
// `img-src 'self'`, and a catalogue that loads its pictures from somebody
// else's server tells that server which self-hosted apps each user is
// browsing. It also means an install with no outbound network still has a
// catalogue worth looking at.
func (s *Server) handleTemplateIcon(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateID")
	body, contentType, ok := templates.ReadIcon(id)
	if !ok {
		// Not an errdoc: the caller is an <img> tag, which cannot read one.
		// The page draws the letter it drew before logos existed.
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	// A logo changes when somebody runs hack/fetch_icons.py and commits the
	// result, which is a new build. A day is short enough that it is never a
	// mystery and long enough that a catalogue of three hundred cards is not
	// three hundred requests every time it is opened.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	// It is an SVG from a third party, so it is served as a picture and never
	// as a document: no script in it can run against this origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// ServeContent rather than Write: it answers a Range request and sets the
	// length. The name is only what it would guess a type from, and the type
	// is already set above.
	http.ServeContent(w, r, id, time.Time{}, bytes.NewReader(body))
}

type installTemplateRequest struct {
	EnvironmentID string            `json:"environment_id"`
	Name          string            `json:"name,omitempty"`
	Values        map[string]string `json:"values,omitempty"`
}

func (s *Server) handleInstallTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := chi.URLParam(r, "templateID")
	tpl, ok := templates.Lookup(templateID)
	if !ok {
		writeError(w, r, errdoc.NotFound("template", templateID))
		return
	}
	var req installTemplateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	env, user, err := s.authorizeEnvironment(r, req.EnvironmentID, store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}

	created, err := s.installTemplate(r, tpl, env, user, req.Name, req.Values)
	if err != nil {
		writeError(w, r, err)
		return
	}
	teamID, _ := s.db.TeamIDForEnvironment(r.Context(), env.ID)
	s.audit(r, teamID, "template.installed", "environment", env.ID, tpl.Name)
	writeJSON(w, http.StatusCreated, created)
}

// --- project canvas ---

// canvasNode is one box on the Railway-style service canvas.
type canvasNode struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Detail string   `json:"detail,omitempty"`
	URLs   []string `json:"urls,omitempty"`
	Env    string   `json:"environment"`
}

// canvasEdge is a connection between two boxes.
type canvasEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

func (s *Server) handleProjectCanvas(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	envs, err := s.db.ListEnvironments(r.Context(), project.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	nodes := []canvasNode{}
	edges := []canvasEdge{}
	for _, env := range envs {
		apps, err := s.db.ListApps(r.Context(), env.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, app := range apps {
			node := canvasNode{ID: app.ID, Kind: "app", Name: app.Name, Status: app.Status, Env: env.Slug}
			if domains, err := s.db.ListDomains(r.Context(), app.ID); err == nil {
				for _, d := range domains {
					scheme := "http://"
					if d.TLS {
						scheme = "https://"
					}
					node.URLs = append(node.URLs, scheme+d.Hostname)
				}
			}
			nodes = append(nodes, node)
		}
		databases, err := s.db.ListDatabases(r.Context(), env.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, record := range databases {
			nodes = append(nodes, canvasNode{
				ID: record.ID, Kind: "database", Name: record.Name,
				Status: record.Status, Detail: record.Engine, Env: env.Slug,
			})
			links, err := s.db.ListLinksForDatabase(r.Context(), record.ID)
			if err != nil {
				continue
			}
			for _, link := range links {
				// The arrow points from the app to the database it uses, which
				// is the direction people read a dependency.
				edges = append(edges, canvasEdge{From: link.AppID, To: record.ID, Label: link.VarName})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
}

// --- upgrade ---

func (s *Server) handleUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"current_version": version.Version,
		"commit":          version.Commit,
		"built":           version.Date,
		// Skifity does not phone home, so the panel cannot know what the latest
		// release is. Saying so is better than a field that is always empty.
		"update_check": "disabled",
		"note":         "Skifity does not contact any server to check for updates. Follow the releases page to learn about new versions.",
	})
}

type upgradeRequest struct {
	Version string `json:"version"`
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	var req upgradeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	// Upgrading the panel means changing the image of the Deployment the panel
	// itself runs in, which Kubernetes then rolls out. The panel is restarted
	// by that rollout, which is why the response is sent first.
	previous, err := s.upgradePanel(r, strings.TrimSpace(req.Version))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "panel.upgrade_started", "panel", version.Version, req.Version)

	// The undo command goes out with the response, before the panel stops. If
	// the new version does not start there is nothing left here to ask.
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":           "started",
		"previous_image":   previous,
		"note":             "The panel will restart. Your apps keep running while it does.",
		"if_it_goes_wrong": "The panel stops before the new version starts, and nothing rolls it back automatically. On the server: kubectl -n " + s.cfg.Namespace + " rollout undo deploy/skifity-panel",
	})
}
