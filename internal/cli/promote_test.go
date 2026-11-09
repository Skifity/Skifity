package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"skifity/internal/errdoc"
)

// promotePanel answers the routes `promote` reads, and refuses a promotion
// that is not forced, the way the panel does when production would build the
// image differently.
func promotePanel(t *testing.T) *[]map[string]any {
	t.Helper()
	var (
		mu   sync.Mutex
		sent []map[string]any
	)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/apps/app_1/promote":
			_, _ = io.WriteString(w, `{"items":[{"app_id":"app_9","app_name":"web","environment_id":"env_2","environment_name":"Production"}],"total":1}`)
		case "GET /api/apps/app_1/deployments":
			_, _ = io.WriteString(w, `{"items":[{"id":"dep_3","number":3,"status":"failed"},{"id":"dep_2","number":2,"status":"succeeded"}],"total":2}`)
		case "POST /api/apps/app_9/promote":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sent = append(sent, body)
			mu.Unlock()
			if body["force"] != true {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":{"code":"promote.built_differently","title":"This app would build that version differently",`+
					`"cause":"The image was built with other build-time variables than web has.","impact":"Nothing was deployed.","fix":"Promote anyway."}}`)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"dep_7","app_id":"app_9","number":7,"status":"queued","trigger":"promote"}`)
		default:
			t.Errorf("the panel was asked for %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	return &sent
}

func TestPromoteFindsTheEnvironmentByNameAndAsksBeforeForcing(t *testing.T) {
	sent := promotePanel(t)

	// The version running now, to production by its name in any case. The
	// panel refuses: production would build it differently, and that is
	// passed on, with how to go ahead.
	var out bytes.Buffer
	err := cmdPromote(t.Context(), []string{"--app", "app_1", "--to", "production", "--follow=false"}, &out)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "promote.built_differently" || !strings.Contains(out.String(), "--force") {
		t.Fatalf("an unforced promotion answered %v: %s", err, out.String())
	}
	if len(*sent) != 1 || (*sent)[0]["deployment_id"] != "dep_2" || (*sent)[0]["force"] != nil {
		t.Fatalf("the panel was sent %v", *sent)
	}

	// --force says why it is needed, and goes ahead.
	out.Reset()
	if err := cmdPromote(t.Context(), []string{"--app", "app_1", "--to", "Production", "--force", "--follow=false"}, &out); err != nil {
		t.Fatalf("a forced promotion: %v", err)
	}
	if len(*sent) != 3 || (*sent)[2]["force"] != true {
		t.Fatalf("a forced promotion sent %v", *sent)
	}
	if !strings.Contains(out.String(), "build-time variables") || !strings.Contains(out.String(), "Deployment #7 started") {
		t.Errorf("a forced promotion said: %s", out.String())
	}
}

// An environment with no app of the same name is said, with where the version
// can go and how to make the one asked for possible.
func TestPromoteToAnEnvironmentWithNoSuchAppSaysWhere(t *testing.T) {
	sent := promotePanel(t)
	var out bytes.Buffer
	err := cmdPromote(t.Context(), []string{"--app", "app_1", "--to", "staging"}, &out)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "cli.promote_no_target" ||
		!strings.Contains(problem.Cause, "Production") || !strings.Contains(problem.Fix, "clone") {
		t.Fatalf("promoting to staging answered %v", err)
	}
	if len(*sent) != 0 {
		t.Errorf("something was promoted: %v", *sent)
	}
	if err := cmdPromote(t.Context(), []string{"--app", "app_1", "--to", "production", "--deployment", "#3", "--force", "--follow=false"}, &out); err != nil {
		t.Fatalf("promoting #3: %v", err)
	}
	if got := (*sent)[len(*sent)-1]["deployment_id"]; got != "dep_3" {
		t.Errorf("--deployment #3 promoted %v", got)
	}
}
