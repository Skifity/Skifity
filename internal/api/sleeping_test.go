package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stoppedCluster reports an app with no instances, which is all the cluster
// can see when the last one has gone.
type stoppedCluster struct {
	Cluster
}

func (stoppedCluster) AppStatus(context.Context, string, string) (AppRuntimeStatus, error) {
	return AppRuntimeStatus{
		Phase:           "stopped",
		Detail:          "This app is scaled to zero instances.",
		DesiredReplicas: 0,
	}, nil
}

// An app asleep under scale to zero is not an app somebody stopped.
//
// The cluster only knows the last instance is gone and says "stopped", and the
// panel used to repeat it: an app working exactly as configured showed the same
// word, the same grey badge and the same pause icon as one a person had taken
// down. The next request wakes it, and nothing said so.
func TestAnAppAsleepDoesNotReadAsAnAppSomebodyStopped(t *testing.T) {
	h := newHarness(t)
	h.withCluster(stoppedCluster{})
	owner := h.newTenant("acme")

	for _, tc := range []struct {
		name        string
		scaleToZero bool
		wantPhase   string
	}{
		{"asleep", true, "sleeping"},
		{"stopped by a person", false, "stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := h.app(owner, "web-"+strings.ReplaceAll(tc.name, " ", "-"))
			// CreateApp leaves the source unset, and UpdateApp will not write
			// a row the schema refuses.
			app.SourceType = "image"
			app.Image = "nginx:1"
			app.ScaleToZero = tc.scaleToZero
			if err := h.db.UpdateApp(t.Context(), &app); err != nil {
				t.Fatalf("UpdateApp: %v", err)
			}

			code, body := h.do(owner, "GET", "/api/apps/"+app.ID+"/status", nil)
			if code != 200 {
				t.Fatalf("status %d: %s", code, body)
			}
			var got AppRuntimeStatus
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Phase != tc.wantPhase {
				t.Errorf("phase is %q, want %q", got.Phase, tc.wantPhase)
			}
		})
	}
}

// Every phase the panel can send has a word for it in every language.
//
// The badge falls back to the raw phase when the key is missing, so a phase
// added in Go arrives at the user as the token a Go file happens to use —
// "not_deployed", in five languages. Two files decide what a phase can be: the
// cluster client summarises a Deployment into one, and the status handler
// rewrites it where the panel knows better than the cluster does.
func TestEveryPhaseTheBackendCanSendHasAWordForIt(t *testing.T) {
	sources := []string{
		filepath.Join("..", "kube", "client.go"),
		filepath.Join("apps_handlers.go"),
	}
	// `return "starting", "…"` and `Phase: "unknown"` are the only two shapes
	// either file uses, and a third would be worth a line here.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`return "(\w+)", `),
		regexp.MustCompile(`Phase:\s*"(\w+)"`),
		regexp.MustCompile(`status\.Phase = "(\w+)"`),
	}

	found := map[string]bool{}
	for _, name := range sources {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, pattern := range patterns {
			for _, match := range pattern.FindAllStringSubmatch(string(body), -1) {
				found[match[1]] = true
			}
		}
	}
	if len(found) < 5 {
		t.Fatalf("only found %d phases in %v, so this test has stopped reading them", len(found), sources)
	}

	locales, err := filepath.Glob(filepath.Join("..", "..", "web", "src", "locales", "*.json"))
	if err != nil || len(locales) == 0 {
		t.Fatalf("find the locales: %v", err)
	}
	for _, path := range locales {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var locale struct {
			Apps struct {
				Phase map[string]string `json:"phase"`
			} `json:"apps"`
		}
		if err := json.Unmarshal(body, &locale); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for phase := range found {
			if locale.Apps.Phase[phase] == "" {
				t.Errorf("%s has no word for the phase %q: add apps.phase.%s",
					filepath.Base(path), phase, phase)
			}
		}
	}
}
