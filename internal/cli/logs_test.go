package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// logsPanel answers an app's log with two instances' lines, read and
// followed, and remembers what it was asked for.
func logsPanel(t *testing.T) *[]url.Values {
	t.Helper()
	var mu sync.Mutex
	asked := []url.Values{}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/apps/app_1/logs" {
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
			return
		}
		mu.Lock()
		asked = append(asked, r.URL.Query())
		mu.Unlock()
		if r.URL.Query().Get("follow") == "true" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: instances\ndata: {\"instances\":[\"web-5d8f-aaaaa\",\"web-5d8f-bbbbb\"],\"omitted\":0}\n\n"+
				"id: 1\nevent: entry\ndata: {\"instance\":\"web-5d8f-aaaaa\",\"at\":\"2026-11-09T10:00:01Z\",\"line\":\"listening\"}\n\n"+
				"event: log\ndata: \"listening\"\n\n"+
				"id: 2\nevent: entry\ndata: {\"instance\":\"web-5d8f-bbbbb\",\"at\":\"2026-11-09T10:00:02Z\",\"line\":\"GET /\"}\n\n"+
				"event: log\ndata: \"GET /\"\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"lines":["listening","GET /"],"instances":["web-5d8f-aaaaa","web-5d8f-bbbbb"],
"entries":[{"instance":"web-5d8f-aaaaa","at":"2026-11-09T10:00:01Z","line":"listening"},
{"instance":"web-5d8f-bbbbb","at":"2026-11-09T10:00:02Z","line":"GET /"}]}`)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	return &asked
}

// `skifity logs` reads every instance, says which wrote each line once there
// is more than one, and asks for one instance and a time range by name.
func TestLogsSayWhichInstanceWroteEachLine(t *testing.T) {
	asked := logsPanel(t)
	var out bytes.Buffer
	if err := cmdLogs(t.Context(), []string{"--app", "app_1", "--instance", "aaaaa", "--since", "10m"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := (*asked)[0]; got.Get("instance") != "aaaaa" || got.Get("since") != "10m" || got.Get("tail") != "200" {
		t.Fatalf("the panel was asked for %v", got)
	}
	if want := "[aaaaa] listening\n[bbbbb] GET /\n"; out.String() != want {
		t.Fatalf("printed %q, want %q", out.String(), want)
	}

	out.Reset()
	if err := cmdLogs(t.Context(), []string{"--app", "app_1", "--follow", "--timestamps"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := (*asked)[1]; got.Get("follow") != "true" || got.Has("instance") || got.Has("since") {
		t.Fatalf("following asked for %v", got)
	}
	// Each line once, though the panel sends it twice: as an entry, and as
	// a plain line for a CLI from before entries.
	want := "2026-11-09T10:00:01.000Z [aaaaa] listening\n2026-11-09T10:00:02.000Z [bbbbb] GET /\n"
	if out.String() != want {
		t.Fatalf("followed, printed %q, want %q", out.String(), want)
	}

	out.Reset()
	if err := cmdLogs(t.Context(), []string{"--app", "app_1", "--follow", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 2 ||
		!strings.Contains(lines[1], `"instance":"web-5d8f-bbbbb"`) || !strings.Contains(lines[1], `"line":"GET /"`) {
		t.Fatalf("followed as JSON, printed %q", out.String())
	}
}

// One instance's lines are printed as they are, with nothing in front.
func TestOneInstancesLogIsPrintedPlain(t *testing.T) {
	printer := &logPrinter{}
	var out bytes.Buffer
	printer.out = &out
	printer.reading([]string{"web-5d8f-aaaaa"}, 0)
	printer.print(logLine{Instance: "web-5d8f-aaaaa", Line: "listening"})
	if out.String() != "listening\n" {
		t.Fatalf("printed %q", out.String())
	}
	printer.reading([]string{"web-5d8f-aaaaa"}, 25)
	if !strings.Contains(out.String(), "Reading 1 of 26 instances") {
		t.Fatalf("instances left out were not said: %q", out.String())
	}
}
