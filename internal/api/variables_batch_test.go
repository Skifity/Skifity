package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"skifity/internal/store"
)

// Several variables at once: a pasted .env, `skifity env import`.

// countingDeployer counts rollouts.
type countingDeployer struct {
	fakeDeployer
	syncs atomic.Int32
}

func (c *countingDeployer) Sync(context.Context, string) error {
	c.syncs.Add(1)
	return nil
}

func withCountingDeployer(t *testing.T) (*harness, *countingDeployer) {
	t.Helper()
	h := newHarness(t)
	deployer := &countingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.api.deployer = deployer
	return h, deployer
}

func variableKeys(t *testing.T, h *harness, appID string) map[string]store.Variable {
	t.Helper()
	rows, err := h.db.ListVariables(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Variable{}
	for _, row := range rows {
		out[row.Key] = row.Variable
	}
	return out
}

// Thirty lines were thirty rollouts, twenty-nine of them with half a
// configuration. Now they are one.
func TestAPastedEnvIsOneRollout(t *testing.T) {
	h, deployer := withCountingDeployer(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	variable := store.Variable{AppID: app.ID, Key: "OLD"}
	if err := h.db.SetVariable(t.Context(), &variable, "sealed"); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/variables/batch", map[string]any{
		"set": []map[string]any{
			{"key": "APP_ENV", "value": "production"},
			{"key": "STRIPE_SECRET_KEY", "value": "sk_live_not_a_real_key_0123456789"},
			{"key": "LOG_LEVEL", "value": "info"},
		},
		"unset": []string{"OLD"},
	})
	if status != http.StatusOK {
		t.Fatalf("the batch answered %d: %s", status, body)
	}
	if n := deployer.syncs.Load(); n != 1 {
		t.Fatalf("the batch rolled the app out %d times, want once", n)
	}
	got := variableKeys(t, h, app.ID)
	if len(got) != 3 || got["OLD"].Key != "" {
		t.Fatalf("the variables are %v", got)
	}
	// The same rule as one at a time: a key that looks like a secret is kept
	// as one when nobody said otherwise.
	if !got["STRIPE_SECRET_KEY"].IsSecret || got["APP_ENV"].IsSecret {
		t.Fatalf("secretness was not decided: %+v", got)
	}
	var answer struct {
		Set []store.Variable `json:"set"`
	}
	_ = json.Unmarshal([]byte(body), &answer)
	for _, v := range answer.Set {
		if v.Value != "" {
			t.Fatalf("the answer hands a value back: %s", body)
		}
	}

	events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "variables.changed", "", 10)
	if len(events) != 1 || events[0].TargetLabel != "APP_ENV, STRIPE_SECRET_KEY, LOG_LEVEL, -OLD" {
		t.Fatalf("the audit log says %+v", events)
	}
}

// All or none: one bad line means nothing is written and nothing rolls out.
func TestABatchWithOneBadLineChangesNothing(t *testing.T) {
	h, deployer := withCountingDeployer(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")

	for name, body := range map[string]map[string]any{
		"a key that is not one": {"set": []map[string]any{
			{"key": "GOOD", "value": "1"}, {"key": "not a key!", "value": "2"},
		}},
		"the same key twice": {"set": []map[string]any{
			{"key": "GOOD", "value": "1"}, {"key": "GOOD", "value": "2"},
		}},
		"set and removed": {"set": []map[string]any{{"key": "GOOD", "value": "1"}}, "unset": []string{"GOOD"}},
		"nothing at all":  {},
	} {
		if status, answer := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/variables/batch", body); status != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s", name, status, answer)
		}
	}
	if got := variableKeys(t, h, app.ID); len(got) != 0 {
		t.Fatalf("a refused batch wrote %v", got)
	}
	if n := deployer.syncs.Load(); n != 0 {
		t.Fatalf("a refused batch rolled out %d times", n)
	}
}

// Build-time values change the image, so they wait for the next deploy
// rather than rolling out the old one.
func TestABatchOfBuildValuesAsksForARebuild(t *testing.T) {
	h, deployer := withCountingDeployer(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")

	status, body := h.do(acme, http.MethodPost, "/api/apps/"+app.ID+"/variables/batch", map[string]any{
		"set": []map[string]any{{"key": "NEXT_PUBLIC_API", "value": "https://api.example", "build_time": true}},
	})
	var answer struct {
		RequiresRebuild bool `json:"requires_rebuild"`
	}
	_ = json.Unmarshal([]byte(body), &answer)
	if status != http.StatusOK || !answer.RequiresRebuild || deployer.syncs.Load() != 0 {
		t.Fatalf("answered %d %s after %d rollouts", status, body, deployer.syncs.Load())
	}
}

// A shared variable reaches every app in the project, so one line at a time
// was every app rolled out per line. A batch is every app once.
func TestAPastedEnvOfSharedVariablesRollsEachAppOutOnce(t *testing.T) {
	h, deployer := withCountingDeployer(t)
	acme := h.newTenant("acme")
	h.app(acme, "web")
	h.app(acme, "worker")

	status, body := h.do(acme, http.MethodPost, "/api/projects/"+acme.project.ID+"/variables/batch", map[string]any{
		"set": []map[string]any{
			{"key": "REGION", "value": "eu"}, {"key": "SENTRY_DSN", "value": "https://key@sentry.example/1"},
			{"key": "FEATURE_X", "value": "on"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("the batch answered %d: %s", status, body)
	}
	if n := deployer.syncs.Load(); n != 2 {
		t.Fatalf("two apps were rolled out %d times between them, want 2", n)
	}
	shared, _ := h.db.ListSharedVariables(t.Context(), acme.project.ID)
	if len(shared) != 3 {
		t.Fatalf("%d shared variables were stored, want 3", len(shared))
	}

	status, _ = h.do(acme, http.MethodPost, "/api/projects/"+acme.project.ID+"/variables/batch", map[string]any{
		"set": []map[string]any{{"key": "NEXT_PUBLIC_X", "value": "1", "build_time": true}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a build-time shared variable answered %d", status)
	}
}
