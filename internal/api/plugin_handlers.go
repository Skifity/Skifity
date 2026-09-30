package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/netguard"
	"skifity/internal/plugins"
	"skifity/internal/pluginstore"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Installing a plugin.
//
// Two steps on purpose. The first reads a manifest and answers what it would
// do: what it may touch, what it can stop, what it will cost. The second
// installs it. A single step would mean the permissions were shown by the same
// request that granted them, which is a confirmation dialog nobody reads
// because it is already too late.
//
// Everything here is owner-only. A plugin receives a token and runs a container
// somebody else wrote, and an admin who manages servers is not the same person
// as the one who decides what code runs in the cluster.

// pluginView is an installed plugin as the panel shows it.
type pluginView struct {
	store.Plugin
	// Manifest is the decoded document, so the page does not parse JSON twice.
	Decoded plugins.Manifest `json:"decoded"`
	// Settings are the values as stored, with a secret's value left out.
	Settings []pluginSettingView `json:"settings"`
}

type pluginSettingView struct {
	Key string `json:"key"`
	// Value is empty for a secret, always. Once stored, a secret's value is
	// never shown again — by the panel, the CLI, the API or anything else.
	Value      string `json:"value,omitempty"`
	Configured bool   `json:"configured"`
	Secret     bool   `json:"secret"`
}

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwnerSomewhere(r); err != nil {
		writeError(w, r, err)
		return
	}
	installed, err := s.db.ListPlugins(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	views := make([]pluginView, 0, len(installed))
	for _, record := range installed {
		view, err := s.pluginView(r, record)
		if err != nil {
			writeError(w, r, err)
			return
		}
		views = append(views, view)
	}
	writeList(w, views)
}

type inspectPluginRequest struct {
	// URL is where the manifest is published, or Manifest is its text pasted
	// straight in — which is how a plugin is installed on a cluster with no way
	// out to the internet, and how an author tries their own before publishing.
	URL      string `json:"url,omitempty"`
	Manifest string `json:"manifest,omitempty"`
}

// inspectPluginResponse is what an administrator is shown before deciding.
type inspectPluginResponse struct {
	Manifest plugins.Manifest `json:"manifest"`
	// Permissions is the same list in words a person can weigh.
	Permissions []string `json:"permissions"`
	// BlocksDeploys is called out separately because it is the one power that
	// can stop everybody's work rather than merely read something.
	BlocksDeploys bool `json:"blocks_deploys"`
	// AlreadyInstalled names the version that is there, when there is one.
	AlreadyInstalled string `json:"already_installed,omitempty"`
}

// handleInspectPlugin reads a manifest and says what installing it would mean.
func (s *Server) handleInspectPlugin(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwnerSomewhere(r); err != nil {
		writeError(w, r, err)
		return
	}
	var req inspectPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	manifest, _, err := s.fetchManifest(r, req)
	if err != nil {
		writeError(w, r, err)
		return
	}

	response := inspectPluginResponse{
		Manifest:      manifest,
		Permissions:   manifest.Permissions,
		BlocksDeploys: manifest.BlocksDeploys(),
	}
	if existing, err := s.db.GetPlugin(r.Context(), manifest.ID); err == nil {
		response.AlreadyInstalled = existing.Version
	}
	writeJSON(w, http.StatusOK, response)
}

type installPluginRequest struct {
	inspectPluginRequest
	// Permissions is what the administrator saw and agreed to. The install is
	// refused when it does not match the manifest's, which is what stops a
	// manifest changing between the screen and the button.
	Permissions []string `json:"permissions"`
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	user, err := s.requireOwnerSomewhere(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req installPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	manifest, raw, err := s.fetchManifest(r, req.inspectPluginRequest)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !samePermissions(req.Permissions, manifest.Permissions) {
		writeError(w, r, errdoc.New("plugin.permissions_changed", "This plugin is asking for something else now").
			WithCause("What you agreed to was %s; the manifest now asks for %s.",
				listOrNothing(req.Permissions), listOrNothing(manifest.Permissions)).
			WithImpact("Nothing was installed.").
			WithFix("Read what it is asking for again, and install it if that is still acceptable.").
			WithStatus(http.StatusConflict))
		return
	}
	if _, err := s.db.GetPlugin(r.Context(), manifest.ID); err == nil {
		writeError(w, r, errdoc.Conflict(
			manifest.ID+" is already installed.",
			"Remove it first, or upgrade it from the plugin's own page."))
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}

	// The token first. A pod that starts without one is a plugin whose first
	// request fails for a reason nobody can see.
	token, plaintext, err := s.auth.CreateAPIToken(r.Context(), user.ID, "",
		"plugin: "+manifest.Name, manifest.Scopes(), 0)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sealed, err := s.cluster.NewPluginSigningSecret(manifest.ID)
	if err != nil {
		_ = s.db.DeleteAPIToken(r.Context(), token.ID, user.ID)
		writeError(w, r, err)
		return
	}

	record := store.Plugin{
		ID: manifest.ID, Manifest: string(raw), Version: manifest.Version,
		SourceURL: req.URL, Status: "installing", Enabled: true,
		TokenID: token.ID, HMACSealed: sealed, InstalledBy: user.ID,
	}
	if err := s.db.CreatePlugin(r.Context(), &record); err != nil {
		_ = s.db.DeleteAPIToken(r.Context(), token.ID, user.ID)
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "plugin.installed", "plugin", manifest.ID,
		manifest.Name+" "+manifest.Version+" ("+listOrNothing(manifest.Permissions)+")")

	if err := s.cluster.InstallPlugin(r.Context(), record, manifest, plaintext); err != nil {
		_ = s.db.SetPluginStatus(r.Context(), manifest.ID, "failed", err.Error())
		writeError(w, r, err)
		return
	}
	if err := s.db.SetPluginStatus(r.Context(), manifest.ID, "running", ""); err != nil {
		writeError(w, r, err)
		return
	}

	record.Status = "running"
	view, err := s.pluginView(r, record)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwnerSomewhere(r); err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "pluginID")
	record, err := s.db.GetPlugin(r.Context(), id)
	if err != nil {
		writeError(w, r, errdoc.NotFound("plugin", id))
		return
	}

	// The token goes first and goes even when the cluster cannot be reached: a
	// credential nobody can trace to anything is worse than a namespace left
	// behind, and the namespace is visible while the token is not.
	if record.TokenID != "" {
		if err := s.db.DeleteAPIToken(r.Context(), record.TokenID, record.InstalledBy); err != nil {
			s.log.Warn("could not revoke a plugin's token", "plugin", id, "error", err)
		}
	}
	if s.cluster != nil {
		if err := s.cluster.RemovePlugin(r.Context(), id); err != nil {
			s.log.Warn("could not remove a plugin's namespace", "plugin", id, "error", err)
		}
	}
	if err := s.db.DeletePlugin(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "plugin.uninstalled", "plugin", id, record.Version)
	writeOK(w)
}

type updatePluginRequest struct {
	// Enabled switches a plugin off without losing its settings or its token.
	Enabled *bool `json:"enabled,omitempty"`
	// Settings are the values it declared. A secret left out keeps what is
	// stored; a secret set to an empty string is cleared, which is how an
	// integration inside a plugin is disconnected.
	Settings map[string]string `json:"settings,omitempty"`
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwnerSomewhere(r); err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "pluginID")
	record, err := s.db.GetPlugin(r.Context(), id)
	if err != nil {
		writeError(w, r, errdoc.NotFound("plugin", id))
		return
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(record.Manifest), &manifest); err != nil {
		writeError(w, r, fmt.Errorf("this plugin's manifest could not be read: %w", err))
		return
	}

	var req updatePluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	declared := map[string]plugins.Setting{}
	for _, setting := range manifest.Settings {
		declared[setting.Key] = setting
	}
	for key, value := range req.Settings {
		setting, ok := declared[key]
		if !ok {
			writeError(w, r, errdoc.BadRequest(
				fmt.Sprintf("%q is not a setting this plugin declared.", key)))
			return
		}
		if strings.TrimSpace(value) == "" {
			if err := s.db.DeletePluginSetting(r.Context(), id, key); err != nil {
				writeError(w, r, err)
				return
			}
			continue
		}
		stored := store.PluginSetting{Key: key, Value: value, Encrypted: setting.Secret}
		if setting.Secret {
			sealed, err := s.keyring.Seal([]byte(value), pluginSettingContext(id, key))
			if err != nil {
				writeError(w, r, err)
				return
			}
			stored.Value = sealed
		}
		if err := s.db.SetPluginSetting(r.Context(), id, stored); err != nil {
			writeError(w, r, err)
			return
		}
	}

	if req.Enabled != nil && *req.Enabled != record.Enabled {
		if err := s.db.SetPluginEnabled(r.Context(), id, *req.Enabled); err != nil {
			writeError(w, r, err)
			return
		}
		record.Enabled = *req.Enabled
		s.audit(r, "", "plugin.switched", "plugin", id, enabledWord(*req.Enabled))
		if s.cluster != nil {
			var err error
			if *req.Enabled {
				err = s.cluster.StartPlugin(r.Context(), id)
			} else {
				err = s.cluster.StopPlugin(r.Context(), id)
			}
			if err != nil {
				writeError(w, r, err)
				return
			}
		}
	}

	// A Secret read into the environment is read once when the process starts,
	// so new settings that do not restart the pod change nothing at all.
	if len(req.Settings) > 0 && record.Enabled && s.cluster != nil {
		if err := s.cluster.RestartPlugin(r.Context(), id); err != nil {
			s.log.Warn("the plugin's settings were saved but it was not restarted",
				"plugin", id, "error", err)
		}
		s.audit(r, "", "plugin.configured", "plugin", id, settingNames(req.Settings))
	}

	view, err := s.pluginView(r, record)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// fetchManifest reads a manifest from a URL or from the text that was pasted.
func (s *Server) fetchManifest(r *http.Request, req inspectPluginRequest) (plugins.Manifest, []byte, error) {
	raw := []byte(req.Manifest)
	if strings.TrimSpace(req.Manifest) == "" {
		if strings.TrimSpace(req.URL) == "" {
			return plugins.Manifest{}, nil, errdoc.BadRequest(
				"Give the address of a plugin's manifest, or paste the manifest itself.")
		}
		fetched, err := s.fetchPluginURL(r, req.URL)
		if err != nil {
			return plugins.Manifest{}, nil, err
		}
		raw = fetched
	}
	manifest, err := plugins.Parse(raw)
	if err != nil {
		return plugins.Manifest{}, nil, errdoc.New("plugin.bad_manifest", "That is not a plugin manifest").
			WithCause("%s", err).
			WithImpact("Nothing was installed.").
			WithFix("Check it against the plugin standard, which is documented at /docs/plugins.").
			WithDocs("/docs/plugins").
			WithStatus(http.StatusBadRequest)
	}
	// Stored as JSON rather than as the YAML it may have arrived as, so that
	// everything reading it back later reads one format.
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return plugins.Manifest{}, nil, err
	}
	return manifest, encoded, nil
}

// fetchPluginURL downloads a manifest through the guarded client.
//
// Guarded because the URL is somebody's input and the panel is the last process
// on the box that should make arbitrary requests: netguard refuses the cloud
// metadata address, loopback and the unspecified address, on the resolved
// address at the moment of connecting.
func (s *Server) fetchPluginURL(r *http.Request, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, errdoc.BadRequest("That is not an address this panel can fetch.")
	}
	resp, err := pluginManifestClient.Do(req)
	if err != nil {
		return nil, errdoc.New("plugin.unreachable", "That manifest could not be fetched").
			WithCause("%s", err).
			WithImpact("Nothing was installed.").
			WithFix("Check the address, and that this cluster can reach it.").
			WithStatus(http.StatusBadGateway)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errdoc.New("plugin.unreachable", "That manifest could not be fetched").
			WithCause("The server answered %s.", resp.Status).
			WithImpact("Nothing was installed.").
			WithFix("Check the address.").
			WithStatus(http.StatusBadGateway)
	}
	return io.ReadAll(io.LimitReader(resp.Body, plugins.MaxManifestBytes+1))
}

func (s *Server) pluginView(r *http.Request, record store.Plugin) (pluginView, error) {
	view := pluginView{Plugin: record, Settings: []pluginSettingView{}}
	if err := json.Unmarshal([]byte(record.Manifest), &view.Decoded); err != nil {
		s.log.Error("an installed plugin's manifest could not be read",
			"plugin", record.ID, "error", err)
	}
	stored, err := s.db.ListPluginSettings(r.Context(), record.ID)
	if err != nil {
		return view, err
	}
	values := map[string]store.PluginSetting{}
	for _, setting := range stored {
		values[setting.Key] = setting
	}
	for _, declared := range view.Decoded.Settings {
		saved, configured := values[declared.Key]
		item := pluginSettingView{
			Key: declared.Key, Configured: configured && saved.Value != "", Secret: declared.Secret,
		}
		if !declared.Secret {
			item.Value = saved.Value
		}
		view.Settings = append(view.Settings, item)
	}
	return view, nil
}

// requireOwnerSomewhere allows a request from somebody who owns a team.
//
// Installing a plugin is not a team's decision: the container runs beside the
// panel and its token reaches the whole install. There is no "plugins team", so
// the nearest honest rule is that you have to own something here.
func (s *Server) requireOwnerSomewhere(r *http.Request) (store.User, error) {
	user, ok := UserFrom(r.Context())
	if !ok {
		return store.User{}, errdoc.Unauthorized()
	}
	teams, err := s.db.ListTeamsForUser(r.Context(), user.ID)
	if err != nil {
		return store.User{}, err
	}
	for _, team := range teams {
		if team.Role == store.RoleOwner {
			return user, nil
		}
	}
	return store.User{}, errdoc.Forbidden("installing and removing plugins")
}

func samePermissions(agreed, asked []string) bool {
	if len(agreed) != len(asked) {
		return false
	}
	seen := map[string]int{}
	for _, permission := range agreed {
		seen[strings.TrimSpace(permission)]++
	}
	for _, permission := range asked {
		seen[strings.TrimSpace(permission)]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func listOrNothing(values []string) string {
	if len(values) == 0 {
		return "nothing"
	}
	return strings.Join(values, ", ")
}

func settingNames(values map[string]string) string {
	names := make([]string, 0, len(values))
	for key := range values {
		names = append(names, key)
	}
	return strings.Join(names, ", ")
}

func enabledWord(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// pluginSettingContext binds a sealed plugin setting to the plugin and the key
// it belongs to, so a row copied elsewhere will not open.
func pluginSettingContext(id, key string) string { return "plugin-setting:" + id + ":" + key }

// pluginManifestClient fetches a manifest from an address somebody typed.
//
// Guarded, because the panel holds the master key and can reach the Kubernetes
// API: it is the last process on the box that should make an arbitrary request.
// netguard refuses the cloud metadata address, loopback and the unspecified
// address, checked on the resolved address at the moment of connecting rather
// than on the hostname — a name that resolves to a public address when it is
// validated and a link-local one when it is dialled is the oldest way around a
// check written the other way round.
var pluginManifestClient = netguard.Client(20 * time.Second)

// storeCatalogueResponse is the store as the page shows it.
type storeCatalogueResponse struct {
	pluginstore.Catalogue
	// Installed maps a plugin id to the version that is here, so the page can
	// show "installed" and "upgrade" without a second request.
	Installed map[string]string `json:"installed"`
}

// handleStoreCatalogue reads the store's index.
//
// A failure is reported rather than swallowed. A store that is down, a store
// whose index is signed by somebody else, and a store with nothing in it are
// three different things, and a page that showed an empty list for all three
// would send somebody looking in the wrong place.
func (s *Server) handleStoreCatalogue(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireOwnerSomewhere(r); err != nil {
		writeError(w, r, err)
		return
	}
	indexURL, _, err := s.db.GetSetting(r.Context(), settings.KeyPluginStoreURL)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(indexURL) == "" {
		indexURL = pluginstore.DefaultIndexURL
	}
	key, _, _ := s.db.GetSetting(r.Context(), settings.KeyPluginStoreKey)

	catalogue, err := pluginstore.Client{HTTP: pluginManifestClient}.
		Fetch(r.Context(), indexURL, key)
	if err != nil {
		writeError(w, r, storeProblem(indexURL, err))
		return
	}

	response := storeCatalogueResponse{Catalogue: catalogue, Installed: map[string]string{}}
	installed, err := s.db.ListPlugins(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, record := range installed {
		response.Installed[record.ID] = record.Version
	}
	writeJSON(w, http.StatusOK, response)
}

// storeProblem turns a store failure into something that names what to do.
func storeProblem(indexURL string, err error) error {
	switch {
	case errors.Is(err, pluginstore.ErrBadSignature):
		return errdoc.New("store.bad_signature", "The plugin store's index is not signed by the key this panel trusts").
			WithCause("%s", err).
			WithImpact("Nothing was read from it, and nothing was installed.").
			WithFix("Check the signing key under Settings, then Plugins. If it is right, the index at %s "+
				"is not the one that key signs, and that is worth finding out about before installing anything from it.",
				indexURL).
			WithStatus(http.StatusBadGateway)
	case errors.Is(err, pluginstore.ErrBadIndex):
		return errdoc.New("store.bad_index", "The plugin store's index could not be read").
			WithCause("%s", err).
			WithImpact("Nothing was read from it.").
			WithFix("If this is a store of your own, check that the index is the format this panel reads. " +
				"A plugin can still be installed from a manifest address or by pasting one.").
			WithStatus(http.StatusBadGateway)
	default:
		return errdoc.New("store.unreachable", "The plugin store could not be reached").
			WithCause("%s", err).
			WithImpact("Nothing was read from it.").
			WithFix("Check that this cluster can reach %s. "+
				"A plugin can still be installed from a manifest address or by pasting one, "+
				"which is what a cluster with no way out to the internet uses.", indexURL).
			WithStatus(http.StatusBadGateway)
	}
}
