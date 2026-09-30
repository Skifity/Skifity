package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/crypto"
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
	// A member limited to projects creates apps in them, and picking the
	// repository to create one from is where that starts. The list carries
	// names, never tokens.
	if _, _, err := s.authorizeTeamMember(r, teamID, store.RoleViewer); err != nil {
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
	// The webhook secret is the connection's own, made here. It was the token:
	// registered as the secret on every repository the panel hooked, and sent
	// verbatim by GitLab on every delivery, so the account's token went with
	// each push, and the secret could not change without the token changing.
	secret, err := crypto.RandomToken(32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	stored := map[string]string{"webhook_secret": secret}
	if req.Token != "" {
		stored["token"] = req.Token
	}
	config, err := json.Marshal(stored)
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
	if err := s.db.CreateGitSource(r.Context(), &source); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "git_source.created", "git_source", source.ID, source.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"source":         source,
		"webhook_url":    s.webhookURL(r, source.ID),
		"webhook_secret": secret,
	})
}

// handleGetGitSourceWebhook answers where a repository delivers pushes and the
// secret it signs them with, for a host the panel could not hook by itself.
//
// A connection made before each had its own secret signs with its token; that
// is said, and the token is not shown — it is a credential for the whole
// account, not a webhook's.
func (s *Server) handleGetGitSourceWebhook(w http.ResponseWriter, r *http.Request) {
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
	answer := map[string]any{"url": s.webhookURL(r, source.ID), "secret": "", "secret_is_token": false}
	if source.ConfigEnc != "" {
		raw, err := s.keyring.Open(source.ConfigEnc, "git_source:"+source.TeamID+":"+source.Name)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var config map[string]string
		if err := json.Unmarshal(raw, &config); err != nil {
			writeError(w, r, err)
			return
		}
		answer["secret"] = config["webhook_secret"]
		answer["secret_is_token"] = config["webhook_secret"] == "" && config["token"] != ""
	}
	s.audit(r, teamID, "git_source.webhook_viewed", "git_source", source.ID, source.Name)
	writeJSON(w, http.StatusOK, answer)
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
	if _, err := s.authorizeTeam(r, teamID, store.RoleViewer); err != nil {
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
// picked by a person. A built-in kind's fields come without words, because the
// panel has them translated; a provided kind carries its own form, in the
// plugin author's English.
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
	// Projects limits the channel to some of the team's projects. Absent is
	// every project.
	Projects []string `json:"projects,omitempty"`
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
	scoped, projects, err := s.channelLimits(r, teamID, req.Projects)
	if err != nil {
		writeError(w, r, err)
		return
	}
	channel := store.NotificationChannel{
		TeamID:   teamID,
		Kind:     req.Kind,
		Name:     defaultString(strings.TrimSpace(req.Name), req.Kind),
		Events:   strings.Join(req.Events, ","),
		Enabled:  true,
		Scoped:   scoped,
		Projects: projects,
	}
	if channel.ConfigEnc, err = s.sealChannelConfig(teamID, channel.Name, req.Config); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.CreateNotificationChannel(r.Context(), &channel); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "notification.created", "channel", channel.ID, channel.Name)
	writeJSON(w, http.StatusCreated, channel)
}

// channelView is one channel as the form that edits it reads it.
type channelView struct {
	store.NotificationChannel
	// Config is every setting the form may show again: the ones that are not
	// secret.
	Config map[string]string `json:"config"`
	// Secrets names the secret settings that are stored. The form says they
	// are there; their values never leave the panel.
	Secrets []string `json:"secrets"`
}

// handleGetNotificationChannel is a channel with its settings, for the form
// that changes it.
//
// Administrators only, as adding one is. A viewer sees the list — where the
// team's alerts go and which events — and not a chat id or a list of
// recipients, which are the administrator's to manage.
func (s *Server) handleGetNotificationChannel(w http.ResponseWriter, r *http.Request) {
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
	config, err := s.openChannelConfig(teamID, channel)
	if err != nil {
		writeError(w, r, err)
		return
	}
	fields, known := s.channelForm(r, channel.Kind)
	values, secrets := notify.Revealable(fields, known, config)
	writeJSON(w, http.StatusOK, channelView{NotificationChannel: channel, Config: values, Secrets: secrets})
}

// updateChannelRequest is the whole channel as the form shows it. The kind is
// not in it: a Telegram channel does not become a Discord one, and its stored
// settings would mean nothing to the other.
type updateChannelRequest struct {
	// Name is kept when empty.
	Name   string   `json:"name"`
	Events []string `json:"events"`
	// Enabled is kept when absent, so a request that does not mention it does
	// not switch a channel off.
	Enabled *bool `json:"enabled,omitempty"`
	// Projects limits the channel to some of the team's projects. Absent is
	// every project, as it is when a channel is created.
	Projects []string `json:"projects,omitempty"`
	// Config changes the channel's settings. A key it leaves out is kept, and
	// so is a secret sent empty — which is what the form sends for a password
	// box nobody touched. Absent keeps every setting as it is.
	Config map[string]string `json:"config,omitempty"`
}

func (s *Server) handleUpdateNotificationChannel(w http.ResponseWriter, r *http.Request) {
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
	var req updateChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	scoped, projects, err := s.channelLimits(r, teamID, req.Projects)
	if err != nil {
		writeError(w, r, err)
		return
	}
	stored, err := s.openChannelConfig(teamID, channel)
	if err != nil {
		writeError(w, r, err)
		return
	}
	config := stored
	if req.Config != nil {
		fields, known := s.channelForm(r, channel.Kind)
		config = notify.MergeConfig(fields, known, stored, req.Config)
		// Only what changed is checked. A channel whose plugin has gone can
		// still be renamed, paused or removed from a project; its settings
		// cannot be checked by anything, and nobody asked to change them.
		if !maps.Equal(config, stored) {
			if err := notify.ValidateConfig(r.Context(), channel.Kind, config, s.channels); err != nil {
				writeError(w, r, errdoc.BadRequest(err.Error()))
				return
			}
		}
	}

	channel.Name = defaultString(strings.TrimSpace(req.Name), channel.Name)
	channel.Events = strings.Join(req.Events, ",")
	if req.Enabled != nil {
		channel.Enabled = *req.Enabled
	}
	channel.Scoped, channel.Projects = scoped, projects
	// Sealed again whatever changed: the name is part of what the settings are
	// sealed with, so a renamed channel whose settings kept the old seal
	// would never open again.
	if channel.ConfigEnc, err = s.sealChannelConfig(teamID, channel.Name, config); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.UpdateNotificationChannel(r.Context(), &channel); err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.db.GetNotificationChannel(r.Context(), channel.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "notification.updated", "channel", channel.ID, channel.Name)
	writeJSON(w, http.StatusOK, updated)
}

// channelLimits checks the projects a channel is to be limited to: nil for
// every project, otherwise at least one, and every one a project of this team.
func (s *Server) channelLimits(r *http.Request, teamID string, projects []string) (scoped bool, out []string, err error) {
	if projects == nil {
		return false, nil, nil
	}
	if len(projects) == 0 {
		return false, nil, errdoc.BadRequest("Choose at least one project, or leave the limit off for every project.")
	}
	out = make([]string, 0, len(projects))
	for _, id := range projects {
		if slices.Contains(out, id) {
			continue
		}
		project, err := s.db.GetProject(r.Context(), id)
		if err != nil || project.TeamID != teamID {
			return false, nil, errdoc.NotFound("project", id)
		}
		out = append(out, id)
	}
	return true, out, nil
}

// channelForm is the form of a channel's kind, asking the plugins when a
// plugin provides it. known is false when nothing can say what the form is,
// and the settings are then all treated as secrets.
func (s *Server) channelForm(r *http.Request, kind string) (fields []notify.Field, known bool) {
	var provided []notify.ChannelKind
	if notify.IsProvided(kind) && s.channels != nil {
		kinds, err := s.channels.Kinds(r.Context())
		if err != nil {
			s.log.Warn("the channels plugins provide could not be listed", "error", err)
		}
		provided = kinds
	}
	return notify.FormOf(kind, provided)
}

// channelSealContext is what a channel's settings are sealed with. The name
// is part of it, so a renamed channel's settings are sealed again.
func channelSealContext(teamID, name string) string {
	return "notification_channel:" + teamID + ":" + name
}

func (s *Server) sealChannelConfig(teamID, name string, config map[string]string) (string, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return s.keyring.Seal(raw, channelSealContext(teamID, name))
}

func (s *Server) openChannelConfig(teamID string, channel store.NotificationChannel) (map[string]string, error) {
	raw, err := s.keyring.Open(channel.ConfigEnc, channelSealContext(teamID, channel.Name))
	if err != nil {
		return nil, err
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if config == nil {
		config = map[string]string{}
	}
	return config, nil
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
	config, err := s.openChannelConfig(teamID, channel)
	if err != nil {
		writeError(w, r, err)
		return
	}

	message := notify.Message{
		Title: version.Name + " test notification",
		Body:  "If you are reading this, notifications from " + version.Name + " are working.",
		Level: "info",
	}
	// The same settings a real delivery is sent with: an email channel that
	// names only its recipients failed every test for want of a server it
	// would have been given.
	ctx := r.Context()
	if channel.Kind == "email" {
		ctx = notify.PrepareEmail(ctx, s.db, s.keyring, s.log, config)
	}
	if err := notify.Send(ctx, channel.Kind, config, message, s.channels); err != nil {
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
	project, _, err := s.authorizeProject(r, chi.URLParam(r, "projectID"), store.RoleViewer)
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
	started, err := s.upgradePanel(r, strings.TrimSpace(req.Version))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "panel.upgrade_started", "panel", version.Version, req.Version)

	// The undo goes out with the response, before the panel stops. If the new
	// version does not start there is nothing left here to ask. It is two
	// steps, not one: the new version migrates the database when it starts,
	// and the old one refuses a database a later version has migrated, so
	// going back is the old image and the copy taken before.
	deployment := "deploy/" + version.Binary + "-panel"
	undo := fmt.Sprintf("The panel stops before the new version starts, and nothing rolls it back automatically. "+
		"On the server: kubectl -n %[1]s scale %[2]s --replicas=0; "+
		"%[3]s admin restore-db --yes %[4]s; "+
		"kubectl -n %[1]s rollout undo %[2]s; kubectl -n %[1]s scale %[2]s --replicas=1",
		s.cfg.Namespace, deployment, version.Binary, started.Snapshot)
	answer := map[string]string{
		"status":           "started",
		"previous_image":   started.Previous,
		"snapshot":         started.Snapshot,
		"note":             "The panel will restart. Your apps keep running while it does.",
		"if_it_goes_wrong": undo,
	}
	if started.OffSite != "" {
		answer["off_site_copy"] = started.OffSite
	}
	writeJSON(w, http.StatusAccepted, answer)
}
