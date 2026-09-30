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
	"time"

	"skifity/internal/errdoc"
)

// scanPanel answers the two scan routes: a report, and a scan that takes
// two looks to finish, or fails.
func scanPanel(t *testing.T, fail bool) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	looks := 0
	report := func(latest string) string {
		return `{"enabled":true,"blocking":true,"image":"registry.example.test/acme/web:2","current":true,` +
			`"scan":{"id":"scan_old","app_id":"app_1","image":"registry.example.test/acme/web:2","trigger":"schedule",` +
			`"status":"succeeded","counts":{"critical":1,"high":0,"medium":0,"low":0,"unknown":0},"fixable":1,` +
			`"fixable_critical":1,"omitted":0,"scanner_version":"0.74.0","created_at":"2026-09-30T04:23:00Z",` +
			`"finished_at":"2026-09-30T04:24:00Z","findings":[{"id":"CVE-2025-29927","package":"next",` +
			`"installed":"14.2.3","fixed_in":"14.2.25","severity":"CRITICAL"}]}` + latest + `}`
	}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/apps/app_1/vulnerabilities/scan":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"scan_new","app_id":"app_1","image":"registry.example.test/acme/web:2",`+
				`"trigger":"manual","status":"queued","counts":{},"findings":[],"created_at":"2026-09-30T10:00:00Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/apps/app_1/vulnerabilities":
			looks++
			switch {
			case looks == 1:
				_, _ = io.WriteString(w, report(`,"latest":{"id":"scan_new","app_id":"app_1","image":"x","trigger":"manual","status":"running","counts":{},"findings":[],"created_at":"2026-09-30T10:00:00Z"}`))
			case fail:
				_, _ = io.WriteString(w, report(`,"latest":{"id":"scan_new","app_id":"app_1","image":"x","trigger":"manual","status":"failed","counts":{},"findings":[],`+
					`"error_code":"scan.database_unavailable","error_message":"The scanner could not fetch its database: no such host",`+
					`"error_hint":"The cluster needs to reach mirror.gcr.io.","created_at":"2026-09-30T10:00:00Z"}`))
			default:
				_, _ = io.WriteString(w, strings.Replace(report(""), `"id":"scan_old"`, `"id":"scan_new"`, 1))
			}
		default:
			t.Errorf("the CLI asked for %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	return panel, &seen
}

// `scan` reads the report, and says what a person needs from it.
func TestScanPrintsTheReport(t *testing.T) {
	scanPanel(t, false)
	var out bytes.Buffer
	if err := cmdScan(t.Context(), []string{"--app", "app_1"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1 critical, 0 high", "Trivy 0.74.0", "CRITICAL", "next", "14.2.25", "CVE-2025-29927",
		"--accept-vulnerabilities",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report does not say %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := cmdScan(t.Context(), []string{"--app", "app_1", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var report vulnerabilityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Scan == nil || report.Scan.Counts.Critical != 1 {
		t.Errorf("--json printed something that is not the report (%v):\n%s", err, out.String())
	}
}

// `scan --now` starts a scan and waits for its own report, not the one
// before it.
func TestScanNowWaitsForItsOwnReport(t *testing.T) {
	_, seen := scanPanel(t, false)
	previous := scanPoll
	scanPoll = time.Millisecond
	t.Cleanup(func() { scanPoll = previous })

	var out bytes.Buffer
	if err := cmdScan(t.Context(), []string{"--app", "app_1", "--now", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var report vulnerabilityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Scan == nil || report.Scan.ID != "scan_new" {
		t.Errorf("--now --json printed (%v):\n%s", err, out.String())
	}
	if got := strings.Join(*seen, ", "); !strings.HasPrefix(got, "POST /api/apps/app_1/vulnerabilities/scan, GET") {
		t.Errorf("the CLI asked %s", got)
	}
}

// A scan that fails is an error with the panel's explanation, not a report.
func TestScanNowSaysWhyItFailed(t *testing.T) {
	scanPanel(t, true)
	previous := scanPoll
	scanPoll = time.Millisecond
	t.Cleanup(func() { scanPoll = previous })

	err := cmdScan(t.Context(), []string{"--app", "app_1", "--now"}, &bytes.Buffer{})
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "scan.database_unavailable" || !strings.Contains(problem.Cause, "no such host") {
		t.Fatalf("a failed scan answered %v", err)
	}
}
