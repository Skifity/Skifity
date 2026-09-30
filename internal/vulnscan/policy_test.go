package vulnscan

import (
	"strings"
	"testing"
	"time"

	"skifity/internal/store"
)

func finding(id, pkg, severity, fixed string) store.ScanFinding {
	return store.ScanFinding{ID: id, Package: pkg, Installed: "1.0", Severity: severity, FixedIn: fixed}
}

// A deploy is stopped by a critical with a fix, and by nothing else: not a
// critical nobody can fix yet, and not a high with a fix.
func TestOnlyACriticalWithAFixBlocks(t *testing.T) {
	findings := []store.ScanFinding{
		finding("CVE-1", "next", SeverityCritical, "14.2.25"),
		finding("CVE-2", "zlib1g", SeverityCritical, ""),
		finding("CVE-3", "braces", SeverityHigh, "3.0.3"),
		finding("CVE-4", "log4j-core", SeverityCritical, "2.15.0"),
	}
	blocking := Blocking(findings)
	if len(blocking) != 2 || blocking[0].Package != "next" || blocking[1].Package != "log4j-core" {
		t.Errorf("blocked by %+v", blocking)
	}
	if Blocking(findings[1:3]) != nil {
		t.Error("an unfixable critical and a fixable high stop a deploy")
	}
	if got := Describe(blocking, 10); got != "next 1.0 (fixed in 14.2.25, CVE-1); log4j-core 1.0 (fixed in 2.15.0, CVE-4)" {
		t.Errorf("described as %q", got)
	}
	if got := Describe(findings, 2); !strings.HasSuffix(got, "; and 2 more") {
		t.Errorf("a long list is not cut short: %q", got)
	}
}

// A critical is news once: the scan that first finds it, and not the daily
// rescan after it. One that goes away and comes back is news again.
func TestACriticalIsNewsOnce(t *testing.T) {
	first := []store.ScanFinding{
		finding("CVE-1", "next", SeverityCritical, "14.2.25"),
		finding("CVE-9", "braces", SeverityHigh, "3.0.3"),
	}
	if fresh := NewCriticals(nil, first); len(fresh) != 1 || fresh[0].ID != "CVE-1" {
		t.Fatalf("the first scan's news is %+v; want the one critical, and not the high", fresh)
	}
	if fresh := NewCriticals(first, first); len(fresh) != 0 {
		t.Errorf("the same critical tomorrow is news again: %+v", fresh)
	}

	second := append([]store.ScanFinding{
		finding("CVE-2", "openssl", SeverityCritical, ""),
		// The same advisory in another package is another thing to fix.
		finding("CVE-1", "next-auth", SeverityCritical, "4.24.8"),
	}, first...)
	fresh := NewCriticals(first, second)
	if len(fresh) != 2 || fresh[0].ID != "CVE-2" || fresh[1].Package != "next-auth" {
		t.Errorf("the second scan's news is %+v", fresh)
	}

	// Fixed, then back after a rollback.
	fixed := []store.ScanFinding{finding("CVE-9", "braces", SeverityHigh, "3.0.3")}
	if fresh := NewCriticals(fixed, first); len(fresh) != 1 {
		t.Errorf("a critical that came back is not news: %+v", fresh)
	}
	// A severity that rose to critical is news.
	raised := []store.ScanFinding{finding("CVE-9", "braces", SeverityCritical, "3.0.3")}
	if fresh := NewCriticals(first, raised); len(fresh) != 1 {
		t.Errorf("a finding raised to critical is not news: %+v", fresh)
	}
}

// The daily rescan takes what is not already being scanned and was not
// scanned lately, the never-scanned first and then the longest since.
func TestTheRescanPicksWhatIsDueOldestFirst(t *testing.T) {
	now := time.Date(2026, 9, 30, 4, 23, 0, 0, time.UTC)
	candidates := []store.ScanCandidate{
		{AppID: "app_recent", Image: "r:1", LastScanned: now.Add(-2 * time.Hour)},
		{AppID: "app_old", Image: "o:1", LastScanned: now.Add(-49 * time.Hour)},
		{AppID: "app_busy", Image: "b:1", Pending: true},
		{AppID: "app_yesterday", Image: "y:1", LastScanned: now.Add(-25 * time.Hour)},
		{AppID: "app_never_b", Image: "n:1"},
		{AppID: "app_never_a", Image: "n:2"},
		{AppID: "app_no_image"},
		{AppID: "app_edge", Image: "e:1", LastScanned: now.Add(-12 * time.Hour)},
	}
	due := Due(candidates, now, 12*time.Hour)
	var order []string
	for _, c := range due {
		order = append(order, c.AppID)
	}
	want := "app_never_a, app_never_b, app_old, app_yesterday, app_edge"
	if strings.Join(order, ", ") != want {
		t.Errorf("rescans %s, want %s", strings.Join(order, ", "), want)
	}
}

// What the scanner printed on its way out decides which explanation a failed
// scan gets, and a cluster with no way out is told so first.
func TestAFailedScanIsExplainedFromWhatItPrinted(t *testing.T) {
	cases := []struct {
		output, code string
	}{
		{"2026-09-30T04:23:41Z\tFATAL\tFatal error\trun error: init error: DB error: failed to download vulnerability DB: " +
			"OCI artifact error: failed to download artifact from any source", "scan.database_unavailable"},
		{"FATAL\tFatal error\trun error: java DB error: failed to download Java DB: no such host", "scan.database_unavailable"},
		{"FATAL\tFatal error\trun error: image scan error: unable to find the specified image \"ghcr.io/acme/app:1\" in " +
			"[\"remote\"]: remote error: GET https://ghcr.io/v2/acme/app/manifests/1: UNAUTHORIZED: authentication required",
			"scan.pull_failed"},
		{"FATAL\tFatal error\tanalyze error: context deadline exceeded", "scan.timeout"},
		{"", "scan.failed"},
		{"panic: runtime error: index out of range", "scan.failed"},
	}
	for _, c := range cases {
		problem := Failure("ghcr.io/acme/app:1", c.output)
		if problem.Code != c.code {
			t.Errorf("%q was explained as %s, want %s", c.output, problem.Code, c.code)
		}
		if problem.Cause == "" || problem.Fix == "" {
			t.Errorf("%s has no cause or fix", problem.Code)
		}
	}

	long := strings.Repeat("a line Trivy printed\n", 500) + "the last line"
	problem := Failure("nginx", long)
	if !strings.Contains(problem.Cause, "the last line") || len(problem.Cause) > 1500 {
		t.Errorf("a long output is not cut to its end: %d characters", len(problem.Cause))
	}
}
