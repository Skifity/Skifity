package vulnscan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/store"
)

func readFixture(t *testing.T) Report {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "trivy-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	report, err := ParseReport(file)
	if err != nil {
		t.Fatalf("the fixture did not parse: %v", err)
	}
	return report
}

// A report as `trivy image --format json` writes it, with the fields the
// panel does not read left in, comes out as counts per severity and the
// findings in the order somebody would act on them.
func TestAReportIsCountedAndOrdered(t *testing.T) {
	result := Summarize(readFixture(t), MaxFindings)

	want := store.ScanCounts{Critical: 3, High: 3, Medium: 1, Low: 2, Unknown: 1}
	if result.Counts != want {
		t.Errorf("counted %+v, want %+v", result.Counts, want)
	}
	if result.Fixable != 6 || result.FixableCritical != 2 {
		t.Errorf("fixable %d, fixable critical %d; want 6 and 2", result.Fixable, result.FixableCritical)
	}
	if result.ScannerVersion != "0.74.0" || result.OS != "debian 12.7" {
		t.Errorf("scanner %q on %q", result.ScannerVersion, result.OS)
	}
	if result.Digest != "sha256:5b0bcabd1ed22e9fb1310cf6c2dec7cdef19f0ad69efa1f392e94a4333501270" {
		t.Errorf("the digest is %q", result.Digest)
	}

	var order []string
	for _, f := range result.Findings {
		order = append(order, f.Severity+" "+f.Package)
	}
	wantOrder := []string{
		// Critical with a fix, then critical without: the first two are
		// something to do today, the third is something to know.
		"CRITICAL next", "CRITICAL org.apache.logging.log4j:log4j-core", "CRITICAL zlib1g",
		"HIGH braces", "HIGH libssl3", "HIGH openssl",
		"MEDIUM openssl",
		"LOW apt", "LOW bash",
		"UNKNOWN left-pad",
	}
	if strings.Join(order, ", ") != strings.Join(wantOrder, ", ") {
		t.Errorf("the findings are in the order\n  %s\nwant\n  %s", strings.Join(order, ", "), strings.Join(wantOrder, ", "))
	}

	next := result.Findings[0]
	if next.ID != "CVE-2025-29927" || next.Installed != "14.2.3" || next.FixedIn != "14.2.25, 15.2.3" ||
		next.URL != "https://avd.aquasec.com/nvd/cve-2025-29927" || next.Target != "app/package-lock.json" ||
		next.Title != "nextjs: Authorization Bypass in Next.js Middleware" {
		t.Errorf("the next.js finding lost something on the way: %+v", next)
	}
	if result.Omitted != 0 {
		t.Errorf("%d omitted from a report of ten", result.Omitted)
	}
}

// The same library at the same version, found in two jars, is one thing to
// upgrade: counted once.
func TestOnePackageFoundTwiceIsCountedOnce(t *testing.T) {
	result := Summarize(readFixture(t), MaxFindings)
	seen := 0
	for _, f := range result.Findings {
		if f.ID == "CVE-2021-44228" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("log4j-core 2.14.1 in two jars is listed %d times", seen)
	}
}

// The list is capped and says how many it left out; the counts are not.
func TestTheFindingsAreCappedAndTheCountsAreNot(t *testing.T) {
	result := Summarize(readFixture(t), 4)
	if len(result.Findings) != 4 || result.Omitted != 6 {
		t.Fatalf("kept %d and omitted %d, want 4 and 6", len(result.Findings), result.Omitted)
	}
	if result.Counts.Total() != 10 || result.Counts.Critical != 3 {
		t.Errorf("capping the list changed the counts: %+v", result.Counts)
	}
	if result.Findings[3].Severity != SeverityHigh {
		t.Errorf("the cap kept %s before a high", result.Findings[3].Severity)
	}
}

// The report is read out of a container's log, so what comes before it and
// after it is not the report's.
func TestAReportIsFoundInTheOutputAroundIt(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "trivy-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	noisy := "2026-09-30T04:23:40Z\tWARN\tsomething the scanner wanted to say\n" + string(body) + "\nand a line after it\n"
	report, err := ParseReport(strings.NewReader(noisy))
	if err != nil {
		t.Fatalf("a report with a line before it did not parse: %v", err)
	}
	if got := Summarize(report, MaxFindings).Counts.Total(); got != 10 {
		t.Errorf("read %d findings", got)
	}

	if _, err := ParseReport(strings.NewReader("FATAL\tinit error: DB error: failed to download vulnerability DB\n")); !errors.Is(err, ErrNoReport) {
		t.Errorf("output with no report in it answered %v", err)
	}
	if _, err := ParseReport(strings.NewReader(`{"SchemaVersion": 1, "Results": []}`)); err == nil {
		t.Error("a schema this does not read was read")
	}
	if _, err := ParseReport(strings.NewReader(`{"SchemaVersion": 2, "Results": [`)); err == nil {
		t.Error("a report cut off half way was read")
	}
}

// What the panel renders from a report is only what it can safely render.
func TestAFindingKeepsOnlyWhatIsSafeToShow(t *testing.T) {
	report := Report{SchemaVersion: 2, Results: []Result{{
		Target: "app/package-lock.json",
		Vulnerabilities: []Vulnerability{
			{VulnerabilityID: "CVE-2026-0001", PkgName: "a", InstalledVersion: "1", Severity: "critical",
				PrimaryURL: "javascript:alert(1)", Title: "  spread\n over\tlines  "},
			{VulnerabilityID: "CVE-2026-0002", PkgName: "b", InstalledVersion: "1", Severity: "NEGLIGIBLE",
				PrimaryURL: "http://example.test/plain"},
			{VulnerabilityID: "", PkgName: "c", InstalledVersion: "1", Severity: "HIGH"},
			{VulnerabilityID: "CVE-2026-0003", PkgName: strings.Repeat("x", 500), InstalledVersion: "1", Severity: "LOW"},
		},
	}}}
	result := Summarize(report, MaxFindings)
	if len(result.Findings) != 3 {
		t.Fatalf("kept %d findings; one without an id is not a finding", len(result.Findings))
	}
	first := result.Findings[0]
	if first.Severity != SeverityCritical || first.URL != "" || first.Title != "spread over lines" {
		t.Errorf("the first finding is %+v", first)
	}
	// Most severe first, so the one rated in words nobody knows is last.
	unknown := result.Findings[2]
	if unknown.Package != "b" || unknown.Severity != SeverityUnknown {
		t.Errorf("a severity nobody knows became %+v", unknown)
	}
	if unknown.URL != "" {
		t.Errorf("a plain-http advisory address was kept: %q", unknown.URL)
	}
	for _, f := range result.Findings {
		if n := len([]rune(f.Package)); n > 200 {
			t.Errorf("a package name of %d characters was kept whole", n)
		}
	}
	if result.Counts.Critical != 1 || result.Counts.Unknown != 1 || result.Counts.Low != 1 {
		t.Errorf("counted %+v", result.Counts)
	}
}
