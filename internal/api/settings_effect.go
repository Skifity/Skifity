package api

import (
	"context"
	"encoding/json"
	"reflect"

	"skifity/internal/kube"
	"skifity/internal/store"
)

// When a saved setting takes effect.
//
// Saving an app's settings gives the running instances what they read at
// runtime, by a rollout there and then, and leaves what goes into the image
// for the next deploy to build: the promise in ADR-0007. The answer used to be
// the app alone, so a new build command was saved, "Saved" was all anybody
// heard, and the app went on running what the old one built. Now it says which
// was which, the panel, the CLI and an assistant alike.

// rolloutSettings are the settings an apply of the app gives its running
// instances, in the order a person reads them.
var rolloutSettings = []string{
	"port", "start_command",
	"health_check", "health_path", "health_start_seconds", "health_timeout_seconds",
	"cpu_request_m", "cpu_limit_m", "mem_request_mb", "mem_limit_mb",
	"run_as_user", "internal",
}

// appUpdate is what saving an app's settings answers: the app as it is now,
// beside when each setting that changed takes effect. The app's own fields
// stay at the top level, so whatever read the bare app before still reads it.
type appUpdate struct {
	store.App
	// RolledOut are the settings the running instances were given now.
	RolledOut []string `json:"rolled_out"`
	// NextDeploy are those that wait for the next deploy: what goes into the
	// image, which the deploy builds with, and the release command, which
	// runs only in a deploy. On an app that runs nothing yet, every change.
	NextDeploy []string `json:"next_deploy"`
}

// settingsTakingEffect sorts the settings that differ between before and
// after into those rolled out and those waiting for the next deploy. Nothing
// is rolled out unless rolledOut says the apply happened: an app that has
// never been deployed, or whose apply failed, gets them at its next deploy.
// A setting that changes neither — a name, deploying on push — is in neither
// list.
func settingsTakingEffect(before, after store.App, rolledOut bool) ([]string, []string) {
	was, now := settingValues(before), settingValues(after)
	changed := func(setting string) bool { return !reflect.DeepEqual(was[setting], now[setting]) }

	applied, waiting := []string{}, []string{}
	for _, input := range kube.BuildInputs {
		if changed(input.Setting) {
			waiting = append(waiting, input.Setting)
		}
	}
	if changed("release_command") {
		waiting = append(waiting, "release_command")
	}
	for _, setting := range rolloutSettings {
		switch {
		case !changed(setting):
		case rolledOut:
			applied = append(applied, setting)
		default:
			waiting = append(waiting, setting)
		}
	}
	return applied, waiting
}

// settingValues is an app's settings by the names the API gives them, which
// are the names the two lists above use.
func settingValues(app store.App) map[string]any {
	encoded, err := json.Marshal(app)
	if err != nil {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil
	}
	return values
}

// appRunning reports whether an app has a version an apply reaches: one that
// deployed, or one being rolled out now.
func (s *Server) appRunning(ctx context.Context, appID string) bool {
	if _, err := s.db.LatestSuccessfulDeployment(ctx, appID); err == nil {
		return true
	}
	_, err := s.db.DeploymentRollingOut(ctx, appID)
	return err == nil
}
