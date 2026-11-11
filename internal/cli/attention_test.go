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

// attentionPanel answers the team's attention list with the items given.
func attentionPanel(t *testing.T, items string) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/teams/team_1/attention" {
			t.Errorf("the CLI asked for %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[`+items+`],"total":0}`)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "team_1")
}

const (
	failedDeploy = `{"kind":"deploy_failed","severity":"error","code":"deploy_failed","params":{"app":"web","number":7},` +
		`"message":"Deployment #7 of web failed.","target_type":"app","target_id":"app_1","target_label":"web",` +
		`"project_name":"shop","environment_name":"production","since":"2026-09-30T03:00:00Z",` +
		`"path":"/apps/app_1?tab=deployments&deployment=dep_7"}`
	noChannel = `{"kind":"no_channel","severity":"warning","code":"no_channel","params":{},` +
		`"message":"Nobody is told when something breaks: the team has no notification channel.",` +
		`"target_type":"team","target_id":"team_1","target_label":"","path":"/settings?tab=notifications"}`
)

// `status --all` is the overview's list, for a script: --json is the items,
// and the exit status says whether anything is broken, with nothing printed
// on top of the list to say so.
func TestStatusAllExitsOneWhenSomethingIsBroken(t *testing.T) {
	attentionPanel(t, failedDeploy+","+noChannel)
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"status", "--all", "--json"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit status %d with a failed deploy, want 1\n%s", code, stderr.String())
	}
	var items []attentionItem
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("--json is not the list: %v\n%s", err, stdout.String())
	}
	if len(items) != 2 || items[0].Kind != "deploy_failed" || items[0].Path != "/apps/app_1?tab=deployments&deployment=dep_7" {
		t.Errorf("items = %+v", items)
	}
	if stderr.Len() != 0 {
		t.Errorf("an error was printed beside the list: %q", stderr.String())
	}

	stdout.Reset()
	if code := Run(t.Context(), []string{"attention"}, &stdout, &stderr); code != 1 {
		t.Fatalf("`attention` exited %d, want the same as status --all", code)
	}
	text := stdout.String()
	for _, want := range []string{
		"1 error, 1 warning", "ERROR    Deployment #7 of web failed.", "shop / production",
		"/apps/app_1?tab=deployments&deployment=dep_7", "WARNING  Nobody is told",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the list does not say %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "http://127.0.0.1") {
		t.Errorf("the page is not a link into the panel:\n%s", text)
	}
}

// Warnings are worth reading and not worth failing a script over.
func TestStatusAllExitsZeroForWarningsAndForNothing(t *testing.T) {
	attentionPanel(t, noChannel)
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"status", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit status %d with only a warning, want 0\n%s", code, stderr.String())
	}

	attentionPanel(t, "")
	stdout.Reset()
	if code := Run(t.Context(), []string{"status", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit status %d with nothing wrong", code)
	}
	if strings.TrimSpace(stdout.String()) != "Nothing needs attention." {
		t.Errorf("an empty list printed %q", stdout.String())
	}
	stdout.Reset()
	if code := Run(t.Context(), []string{"status", "--all", "--json"}, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != "[]" {
		t.Errorf("an empty list as JSON is %q (exit %d), want []", stdout.String(), code)
	}
}
