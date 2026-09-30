package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// settingView hides the value of an encrypted setting while still telling the UI
// that it has been configured.
type settingView struct {
	Key        string `json:"key"`
	Value      string `json:"value,omitempty"`
	Secret     bool   `json:"secret"`
	Configured bool   `json:"configured"`
	Label      string `json:"label"`
	Group      string `json:"group"`
	Help       string `json:"help"`
	// Kind tells the panel which control to draw. Without it a yes/no setting
	// is a text box where the user has to guess which word this one accepts.
	Kind        string   `json:"kind"`
	Options     []string `json:"options,omitempty"`
	Multiline   bool     `json:"multiline,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
}

func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	stored, err := s.db.ListSettings(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	views := make([]settingView, 0, len(settings.Definitions))
	for _, def := range settings.Definitions {
		row, exists := stored[def.Key]
		view := settingView{
			Key:         def.Key,
			Secret:      def.Secret,
			Label:       def.Label,
			Group:       def.Group,
			Help:        def.Help,
			Kind:        string(def.ResolvedKind()),
			Options:     def.Options,
			Multiline:   def.Multiline,
			Placeholder: def.Placeholder,
			Configured:  exists && row.Value != "",
		}
		if !def.Secret && exists {
			view.Value = row.Value
		}
		views = append(views, view)
	}
	writeList(w, views)
}

type updateSettingsRequest struct {
	Values map[string]string `json:"values"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	var req updateSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	for key, value := range req.Values {
		def, ok := settings.Lookup(key)
		if !ok {
			writeError(w, r, errdoc.BadRequest(fmt.Sprintf("%q is not a setting this panel has.", key)))
			return
		}
		if err := def.Validate(value); err != nil {
			writeError(w, r, errdoc.BadRequest(fmt.Sprintf("%s: %s", def.Label, err.Error())))
			return
		}

		// An empty value clears a setting; for a secret that means the operator
		// is disconnecting the integration, not blanking the password by accident.
		if strings.TrimSpace(value) == "" {
			if err := s.db.DeleteSetting(r.Context(), key); err != nil {
				writeError(w, r, err)
				return
			}
			continue
		}

		stored := value
		if def.Secret {
			sealed, err := s.keyring.Seal([]byte(value), settings.Context(key))
			if err != nil {
				writeError(w, r, err)
				return
			}
			stored = sealed
		}
		if err := s.db.SetSetting(r.Context(), key, stored, def.Secret, user.ID); err != nil {
			writeError(w, r, err)
			return
		}
		// The key is audited; the value never is.
		s.audit(r, "", "settings.updated", "setting", key, def.Label)
	}

	// A setting that something in the cluster is already running on has to
	// reach the cluster too, or saving it does nothing a user can see.
	if _, changed := req.Values[settings.KeyCloudflareTunnelToken]; changed && s.cluster != nil {
		// The save has happened; this is the cluster catching up, and it must
		// not be abandoned because the browser navigated away.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
		defer cancel()
		if err := s.cluster.RefreshCloudflareTunnel(ctx); err != nil {
			writeError(w, r, errdoc.New("tunnel.not_applied", "The token was saved, but the tunnel was not updated").
				WithCause("%s", err).
				WithImpact("The connectors are still running on the previous token, so traffic keeps flowing until they are restarted.").
				WithFix("Fix what the cause says and save the token again, or install Cloudflare tunnel under Components.").
				WithStatus(http.StatusBadGateway))
			return
		}
	}
	writeOK(w)
}

func (s *Server) handleListComponents(w http.ResponseWriter, r *http.Request) {
	components, err := s.db.ListComponents(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	known := map[string]store.ClusterComponent{}
	for _, c := range components {
		known[c.Name] = c
	}
	type componentView struct {
		store.ClusterComponent
		Title       string `json:"title"`
		Description string `json:"description"`
		Optional    bool   `json:"optional"`
		Beta        bool   `json:"beta"`
		MemoryMB    int    `json:"approximate_memory_mb"`
		External    bool   `json:"external"`
		Docs        string `json:"docs,omitempty"`
	}
	views := make([]componentView, 0, len(settings.Components))
	for _, def := range settings.Components {
		component, ok := known[def.Name]
		if !ok {
			component = store.ClusterComponent{Name: def.Name, Status: "absent"}
		}
		views = append(views, componentView{
			ClusterComponent: component,
			Title:            def.Title,
			Description:      def.Description,
			Optional:         def.Optional,
			Beta:             def.Beta,
			MemoryMB:         def.MemoryMB,
			External:         def.External,
			Docs:             def.Docs,
		})
	}
	writeList(w, views)
}

func (s *Server) handleInstallComponent(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	def, ok := settings.LookupComponent(name)
	if !ok {
		writeError(w, r, errdoc.NotFound("component", name))
		return
	}
	if def.External {
		writeError(w, r, errdoc.Newf("component.external", "%s is not installed by the panel", def.Title).
			WithCause("It is installed with Helm, because it is several dozen objects with their own release cadence and the panel would be a worse installer than the one its authors wrote.").
			WithImpact("Nothing was changed.").
			WithFix("The documentation has the command.").
			WithDocs(def.Docs).
			WithStatus(http.StatusBadRequest))
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	if err := s.cluster.InstallComponent(r.Context(), name); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "component.installed", "component", name, name)
	writeOK(w)
}

// --- master key rotation and recovery ---

type rotateResponse struct {
	NewKeyID    string `json:"new_key_id"`
	Rewrapped   int    `json:"rewrapped"`
	Failed      int    `json:"failed"`
	RecoveryKey string `json:"recovery_key"`
}

// handleRotateMasterKey rotates the master key and rewraps every secret.
//
// The new key is written to disk before anything is rewrapped, so a crash
// mid-rotation leaves a keyring that can still open both old and new envelopes.
func (s *Server) handleRotateMasterKey(w http.ResponseWriter, r *http.Request) {
	newKeyID, err := s.keyring.BeginRotation()
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.saveKeyring(); err != nil {
		writeError(w, r, err)
		return
	}

	// The rewrap runs on a context of its own. On the request's, a browser tab
	// closed halfway through cancels it: the secrets already rewrapped are
	// fine, the rest keep the old key, and nobody is told which is which. The
	// old key is only dropped when every one succeeded, so finishing the pass
	// is what makes the difference between a clean rotation and a half one.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()

	refs, err := s.db.ListSealedSecrets(ctx)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rewrapped, failed := 0, 0
	for _, ref := range refs {
		next, changed, err := s.keyring.Rewrap(ref.Sealed)
		if err != nil {
			// One unreadable secret must not stop the rest being rotated.
			s.log.Error("could not rewrap a secret",
				"table", ref.Table, "column", ref.Column, "id", ref.ID, "error", err)
			failed++
			continue
		}
		if !changed {
			continue
		}
		if err := s.db.UpdateSealed(ctx, ref, next); err != nil {
			s.log.Error("could not store a rewrapped secret",
				"table", ref.Table, "column", ref.Column, "id", ref.ID, "error", err)
			failed++
			continue
		}
		rewrapped++
	}

	recovery, err := s.keyring.RecoveryKey()
	if err != nil {
		s.log.Warn("could not render the recovery key", "error", err)
	}
	s.audit(r, "", "security.key_rotated", "keyring", newKeyID,
		fmt.Sprintf("%d rewrapped, %d failed", rewrapped, failed))

	// The old key stays in the keyring on purpose when anything failed, so the
	// secrets that did not rewrap can still be read.
	if failed == 0 {
		for _, id := range s.keyring.IDs() {
			if id != newKeyID {
				if err := s.keyring.DropKey(id); err != nil {
					s.log.Warn("could not drop a retired key", "key_id", id, "error", err)
				}
			}
		}
		if err := s.saveKeyring(); err != nil {
			s.log.Warn("could not save the keyring after dropping retired keys", "error", err)
		}
	}

	writeJSON(w, http.StatusOK, rotateResponse{
		NewKeyID: newKeyID, Rewrapped: rewrapped, Failed: failed, RecoveryKey: recovery,
	})
}

func (s *Server) handleRecoveryKey(w http.ResponseWriter, r *http.Request) {
	key, err := s.keyring.RecoveryKey()
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, "", "security.recovery_key_viewed", "keyring", s.keyring.ActiveID(), "")
	writeJSON(w, http.StatusOK, map[string]string{
		"recovery_key": key,
		"warning":      "Anyone with this key can decrypt every secret this panel stores. Keep it somewhere safe and offline.",
	})
}

func (s *Server) handleRecoveryKeySaved(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	user.RecoverySaved = true
	if err := s.db.UpdateUser(r.Context(), &user); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w)
}

// saveKeyring persists the keyring to the configured path.
func (s *Server) saveKeyring() error {
	return saveKeyringTo(s.cfg.MasterKeyPath, s.keyring)
}
