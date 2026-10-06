package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// upgradePanel answers the two upgrade routes and remembers every request.
func upgradePanel(t *testing.T, state, latest string) *[]string {
	t.Helper()
	seen := &[]string{}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		entry := r.Method + " " + r.URL.Path
		if r.Method == http.MethodPost && r.URL.Path == "/api/upgrade" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			entry += " " + body["version"]
		}
		*seen = append(*seen, entry)
		switch r.URL.Path {
		case "/api/upgrade/check":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"current_version": "v0.1.0", "latest_version": latest, "state": state,
				"release_url": "https://github.com/acme/skifity/releases/tag/" + latest,
				"repository":  "acme/skifity",
			})
		case "/api/upgrade":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"status":"started","previous_image":"ghcr.io/acme/skifity:v0.1.0",
"snapshot":"/var/lib/skifity/panel.db.before-upgrade-1-to-v0.2.0",
"rollback":["kubectl -n skifity-system scale deploy/skifity-panel --replicas=0","skifity admin restore-db --yes /var/lib/skifity/panel.db.before-upgrade-1-to-v0.2.0"]}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	return seen
}

func TestUpgradeWithNoFlagsOnlyAsks(t *testing.T) {
	seen := upgradePanel(t, "available", "v0.2.0")
	var out bytes.Buffer
	if err := cmdUpgrade(t.Context(), nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 || (*seen)[0] != "POST /api/upgrade/check" {
		t.Fatalf("with no flags it sent %v; it should only ask", *seen)
	}
	for _, want := range []string{"Running  v0.1.0", "Newest   v0.2.0", "skifity upgrade --latest"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printed:\n%s\nmissing %q", out.String(), want)
		}
	}
}

func TestUpgradeLatestUpgradesOnlyToAnewerRelease(t *testing.T) {
	seen := upgradePanel(t, "available", "v0.2.0")
	var out bytes.Buffer
	if err := cmdUpgrade(t.Context(), []string{"--latest"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*seen, "|"); got != "POST /api/upgrade/check|POST /api/upgrade v0.2.0" {
		t.Fatalf("it sent %s", got)
	}
	for _, want := range []string{
		"upgrade to v0.2.0 has started",
		"panel.db.before-upgrade-1-to-v0.2.0",
		"If the new version does not come up",
		"scale deploy/skifity-panel --replicas=0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printed:\n%s\nmissing %q", out.String(), want)
		}
	}

	// Nothing newer: nothing is changed, and it says so.
	for _, state := range []string{"up_to_date", "ahead", "development", "unknown", "no_release"} {
		seen := upgradePanel(t, state, "v0.1.0")
		out.Reset()
		if err := cmdUpgrade(t.Context(), []string{"--latest"}, &out); err != nil {
			t.Fatal(err)
		}
		if len(*seen) != 1 || strings.Contains(strings.Join(*seen, "|"), "POST /api/upgrade ") {
			t.Errorf("state %s: it sent %v, and should only have asked", state, *seen)
		}
		if !strings.Contains(out.String(), "Nothing was changed.") {
			t.Errorf("state %s printed:\n%s", state, out.String())
		}
	}
}

func TestUpgradeToANamedReleaseAsksNobody(t *testing.T) {
	seen := upgradePanel(t, "available", "v0.2.0")
	var out bytes.Buffer
	if err := cmdUpgrade(t.Context(), []string{"--to", "v0.3.0"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*seen, "|"); got != "POST /api/upgrade v0.3.0" {
		t.Fatalf("it sent %s; naming the release should not ask about releases", got)
	}

	if err := cmdUpgrade(t.Context(), []string{"--to", "v0.3.0", "--latest"}, &out); err == nil {
		t.Error("--to and --latest together were accepted")
	}

	out.Reset()
	if err := cmdUpgrade(t.Context(), []string{"--to", "v0.3.0", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var answer map[string]any
	if err := json.Unmarshal(out.Bytes(), &answer); err != nil || answer["snapshot"] == nil {
		t.Errorf("--json printed %q: %v", out.String(), err)
	}
}
