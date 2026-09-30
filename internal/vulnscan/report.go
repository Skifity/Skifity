// Package vulnscan looks for known vulnerabilities in the images apps run.
//
// The scanner is Trivy, run as a Kubernetes Job in the build namespace (job.go)
// against the image a deployment runs. This package renders that Job, reads
// the JSON report it prints (this file), and decides what a report means for a
// deploy and for the people told about it (policy.go). It runs nothing and
// stores nothing: internal/deploy does both.
package vulnscan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"skifity/internal/store"
)

// MaxFindings is how many findings a scan keeps. The counts cover every one;
// the list is the most severe first, and a report on an old image with a full
// distribution in it runs to thousands of rows.
const MaxFindings = 200

// MaxReportBytes bounds how much of a report is read. A large image's report
// with every advisory's description in it is a few megabytes; anything past
// this is not a report the panel should be holding in memory.
const MaxReportBytes = 64 << 20

// Severities, in Trivy's words and in the order they matter.
const (
	SeverityCritical = "CRITICAL"
	SeverityHigh     = "HIGH"
	SeverityMedium   = "MEDIUM"
	SeverityLow      = "LOW"
	SeverityUnknown  = "UNKNOWN"
)

// Severities is every severity the scan asks for, most severe first.
var Severities = []string{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityUnknown}

// Report is the part of Trivy's JSON report the panel reads: schema version 2,
// as `trivy image --format json` writes it. Every field Trivy adds and this
// does not name is ignored.
type Report struct {
	SchemaVersion int `json:"SchemaVersion"`
	Trivy         struct {
		Version string `json:"Version"`
	} `json:"Trivy"`
	ArtifactName string `json:"ArtifactName"`
	ArtifactType string `json:"ArtifactType"`
	Metadata     struct {
		OS *struct {
			Family string `json:"Family"`
			Name   string `json:"Name"`
			EOSL   bool   `json:"EOSL"`
		} `json:"OS"`
		ImageID     string   `json:"ImageID"`
		RepoDigests []string `json:"RepoDigests"`
	} `json:"Metadata"`
	Results []Result `json:"Results"`
}

// Result is one place in the image Trivy looked: the operating system's
// packages, or one lockfile or directory of a language's.
type Result struct {
	Target          string          `json:"Target"`
	Class           string          `json:"Class"`
	Type            string          `json:"Type"`
	Vulnerabilities []Vulnerability `json:"Vulnerabilities"`
}

// Vulnerability is one advisory against one installed package.
type Vulnerability struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	PkgPath          string `json:"PkgPath"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Status           string `json:"Status"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	PrimaryURL       string `json:"PrimaryURL"`
}

// ErrNoReport is output with no report in it: a scanner that printed an error
// instead, or nothing at all.
var ErrNoReport = errors.New("the scanner printed no report")

// ParseReport reads a Trivy JSON report.
//
// The report is read out of a container's log, so it is found rather than
// assumed to start the output: anything before the first line that opens a
// JSON object is skipped, and anything after the object closes is ignored.
func ParseReport(r io.Reader) (Report, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxReportBytes+1))
	if err != nil {
		return Report{}, fmt.Errorf("read the report: %w", err)
	}
	if len(raw) > MaxReportBytes {
		return Report{}, fmt.Errorf("the report is larger than %d MiB", MaxReportBytes>>20)
	}
	start := reportStart(raw)
	if start < 0 {
		return Report{}, ErrNoReport
	}
	var report Report
	if err := json.NewDecoder(bytes.NewReader(raw[start:])).Decode(&report); err != nil {
		return Report{}, fmt.Errorf("read the report: %w", err)
	}
	if report.SchemaVersion < 2 {
		return Report{}, fmt.Errorf("the report is schema version %d, and only version 2 is read", report.SchemaVersion)
	}
	return report, nil
}

// reportStart is where the JSON object starts: the first "{" at the start of
// a line, or -1.
func reportStart(raw []byte) int {
	for offset := 0; offset < len(raw); {
		line := raw[offset:]
		if end := bytes.IndexByte(line, '\n'); end >= 0 {
			line = line[:end+1]
		}
		if trimmed := bytes.TrimLeft(line, " \t\r"); len(trimmed) > 0 && trimmed[0] == '{' {
			return offset + len(line) - len(trimmed)
		}
		offset += len(line)
	}
	return -1
}

// NormalizeSeverity is a severity as one of Severities. Anything the scanner
// could not rate, or rated in words this does not know, is UNKNOWN.
func NormalizeSeverity(severity string) string {
	severity = strings.ToUpper(strings.TrimSpace(severity))
	if slices.Contains(Severities, severity) {
		return severity
	}
	return SeverityUnknown
}

// rank orders severities, the most severe lowest.
func rank(severity string) int {
	if i := slices.Index(Severities, severity); i >= 0 {
		return i
	}
	return len(Severities)
}

// Summarize turns a report into what a scan keeps: a count per severity over
// everything found, and the most severe findings, at most limit of them.
//
// The same advisory against the same package at the same version is counted
// once, wherever in the image it was found: two copies of one library in two
// directories are one thing to upgrade, not two.
func Summarize(report Report, limit int) store.ScanResult {
	if limit <= 0 {
		limit = MaxFindings
	}
	result := store.ScanResult{
		ScannerVersion: clip(report.Trivy.Version, 40),
		Digest:         digestOf(report.Metadata.RepoDigests),
	}
	if system := report.Metadata.OS; system != nil {
		result.OS = clip(strings.TrimSpace(system.Family+" "+system.Name), 80)
	}

	seen := map[string]bool{}
	var findings []store.ScanFinding
	for _, target := range report.Results {
		for _, v := range target.Vulnerabilities {
			id := strings.TrimSpace(v.VulnerabilityID)
			if id == "" {
				continue
			}
			key := id + "\x00" + v.PkgName + "\x00" + v.InstalledVersion
			if seen[key] {
				continue
			}
			seen[key] = true

			finding := store.ScanFinding{
				ID:        clip(id, 80),
				Package:   clip(v.PkgName, 200),
				Installed: clip(v.InstalledVersion, 120),
				FixedIn:   clip(strings.TrimSpace(v.FixedVersion), 200),
				Severity:  NormalizeSeverity(v.Severity),
				Title:     clip(strings.Join(strings.Fields(v.Title), " "), 240),
				URL:       advisoryURL(v.PrimaryURL),
				Target:    clip(target.Target, 200),
			}
			count(&result, finding)
			findings = append(findings, finding)
		}
	}

	slices.SortStableFunc(findings, compareFindings)
	if len(findings) > limit {
		result.Omitted = len(findings) - limit
		findings = findings[:limit]
	}
	result.Findings = findings
	if result.Findings == nil {
		result.Findings = []store.ScanFinding{}
	}
	return result
}

// count adds a finding to the totals.
func count(result *store.ScanResult, finding store.ScanFinding) {
	switch finding.Severity {
	case SeverityCritical:
		result.Counts.Critical++
	case SeverityHigh:
		result.Counts.High++
	case SeverityMedium:
		result.Counts.Medium++
	case SeverityLow:
		result.Counts.Low++
	default:
		result.Counts.Unknown++
	}
	if finding.FixedIn != "" {
		result.Fixable++
		if finding.Severity == SeverityCritical {
			result.FixableCritical++
		}
	}
}

// compareFindings puts the most severe first, and among equals the ones with
// a fix first: those are the rows somebody can act on today.
func compareFindings(a, b store.ScanFinding) int {
	if d := rank(a.Severity) - rank(b.Severity); d != 0 {
		return d
	}
	if fa, fb := a.FixedIn != "", b.FixedIn != ""; fa != fb {
		if fa {
			return -1
		}
		return 1
	}
	if c := strings.Compare(a.Package, b.Package); c != 0 {
		return c
	}
	if c := strings.Compare(a.ID, b.ID); c != 0 {
		return c
	}
	return strings.Compare(a.Installed, b.Installed)
}

// digestOf is the manifest digest out of a report's repository digests,
// "registry/name@sha256:...", or empty.
func digestOf(repoDigests []string) string {
	for _, reference := range repoDigests {
		if _, digest, ok := strings.Cut(reference, "@"); ok && strings.HasPrefix(digest, "sha256:") && len(digest) == 71 {
			return digest
		}
	}
	return ""
}

// advisoryURL keeps an advisory's address when it is one a browser can safely
// be sent to. The report is read from a container, and a link the panel
// renders is not a place for a javascript: address.
func advisoryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "https://") || len(raw) > 500 || strings.ContainsAny(raw, " \t\n\"'<>") {
		return ""
	}
	return raw
}

// clip shortens a string to at most n characters, on a character boundary.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
