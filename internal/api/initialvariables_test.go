package api

import (
	"net/http"
	"strings"
	"testing"
)

// A variable name the cluster cannot carry is refused before the app exists.
// It used to be found while storing the variables, after the insert: the
// request said 400, the app stayed, and pressing Create again was refused
// because its name was taken.
func TestABadVariableNameCreatesNoApp(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	path := "/api/environments/" + acme.env.ID + "/apps"

	for name, key := range map[string]string{
		"a Compose setting with a dot":    "discovery.type",
		"the GPU plugin's own variable":   "NVIDIA_VISIBLE_DEVICES",
		"a name that starts with a digit": "1PASSWORD",
		"a name with a dash in it":        "MY-VAR",
	} {
		t.Run(name, func(t *testing.T) {
			status, body := h.do(acme, http.MethodPost, path, map[string]any{
				"name": "search", "source_type": "image", "image": "elasticsearch:8.15.0",
				"variables": map[string]string{"ES_JAVA_OPTS": "-Xms512m", key: "x"},
			})
			if status != http.StatusBadRequest {
				t.Fatalf("got %d, want 400: %s", status, truncate(body, 200))
			}
			if !strings.Contains(body, key) {
				t.Errorf("the refusal does not name %s: %s", key, truncate(body, 300))
			}
			apps, err := h.db.ListApps(t.Context(), acme.env.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(apps) != 0 {
				t.Fatalf("a refused request left %d apps behind", len(apps))
			}
		})
	}

	// And the retry, with the name fixed, is not refused for the first try.
	status, body := h.do(acme, http.MethodPost, path, map[string]any{
		"name": "search", "source_type": "image", "image": "elasticsearch:8.15.0",
		"variables": map[string]string{"ES_JAVA_OPTS": "-Xms512m", "DISCOVERY_TYPE": "single-node"},
	})
	if status != http.StatusCreated {
		t.Fatalf("the retry answered %d: %s", status, truncate(body, 300))
	}
}
