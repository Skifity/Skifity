package api

import (
	"errors"
	"net/http"

	"skifity/internal/store"
)

// What changing an app's variables does to the app.
//
// A runtime variable is rolled out. A build-time one is baked into the image,
// so the version the app runs is built again with it — the same code, as a
// secret manager's refresh does — and the panel's "the app is being rebuilt"
// is true. It used to say so and queue nothing: the handler skipped the
// rollout because a rebuild would cover it, and no rebuild ever started.

// Why a build-time change did not rebuild the app, as a token the interface
// translates.
const (
	// notRebuiltLocked is an app whose deploys are locked: the change waits
	// for the next deploy after they are unlocked.
	notRebuiltLocked = "locked"
	// notRebuiltNotDeployed is an app that runs nothing yet: its first deploy
	// builds with the change.
	notRebuiltNotDeployed = "not_deployed"
	// notRebuiltFailed is a rebuild that could not start; the panel's log
	// says why, and the next deploy builds with the change.
	notRebuiltFailed = "failed"
)

// variableEffect is what a change of variables did: a rebuild, with the
// deployment doing it, or why there was none.
type variableEffect struct {
	// RequiresRebuild is a build-time change that is being built now.
	RequiresRebuild bool              `json:"requires_rebuild"`
	Deployment      *store.Deployment `json:"deployment,omitempty"`
	// NotRebuilt is set when a build-time change was not built now: locked,
	// not_deployed or failed. It takes effect at the next deploy.
	NotRebuilt string `json:"not_rebuilt,omitempty"`
}

// applyVariableChanges gives the app what changed: a rollout of what it reads
// at runtime, and a rebuild of the version it runs when the build reads
// anything that changed. The rollout does not wait for the build: a value
// somebody needs now arrives now, and a build that fails does not take it
// with it.
func (s *Server) applyVariableChanges(r *http.Request, app store.App, actorID string, rollout, rebuild bool) variableEffect {
	ctx := r.Context()
	if rollout && s.deployer != nil {
		if err := s.deployer.Sync(ctx, app.ID); err != nil {
			s.log.Warn("could not apply variable changes", "app", app.ID, "error", err)
		}
	}
	if !rebuild {
		return variableEffect{}
	}
	if reason := s.rebuildRefusal(r, app); reason != "" {
		return variableEffect{NotRebuilt: reason}
	}
	live, err := s.db.LatestSuccessfulDeployment(ctx, app.ID)
	if err == nil {
		// The commit the app runs. A version deployed from the button
		// recorded none, and is built from its branch again, as the button
		// would.
		var deployment store.Deployment
		deployment, err = s.deployer.Deploy(ctx, DeployRequest{
			AppID: app.ID, Trigger: "variables", CommitSHA: live.CommitSHA, CreatedBy: actorID,
		})
		if err == nil {
			return variableEffect{RequiresRebuild: true, Deployment: &deployment}
		}
	}
	s.log.Warn("could not rebuild an app for a build-time variable", "app", app.ID, "error", err)
	return variableEffect{NotRebuilt: notRebuiltFailed}
}

// rebuildRefusal says why the app cannot be rebuilt now, or nothing when it
// can.
func (s *Server) rebuildRefusal(r *http.Request, app store.App) string {
	if s.deployer == nil {
		return notRebuiltFailed
	}
	if _, err := s.db.GetDeployLock(r.Context(), app.ID); err == nil {
		return notRebuiltLocked
	}
	if _, err := s.db.LatestSuccessfulDeployment(r.Context(), app.ID); errors.Is(err, store.ErrNotFound) {
		return notRebuiltNotDeployed
	}
	return ""
}

// buildReads reports whether a change to a build-time variable changes what
// the app runs. An image app builds nothing, so its build-time variables are
// only ever read at runtime.
func buildReads(app store.App) bool { return app.SourceType != "image" }
