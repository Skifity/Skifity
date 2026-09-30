package vulnscan

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// What a report means: for a deploy, for the people told about it, and for
// which app is looked at next.

// Blocking is what stops a deploy when the panel is set to stop them: the
// critical findings that have a fixed version. A critical with no fix is not
// in it — there is nothing to upgrade to, and refusing the deploy would only
// refuse the next fix of something else.
func Blocking(findings []store.ScanFinding) []store.ScanFinding {
	var out []store.ScanFinding
	for _, f := range findings {
		if f.Severity == SeverityCritical && f.FixedIn != "" {
			out = append(out, f)
		}
	}
	return out
}

// NewCriticals is the critical findings in current that previous did not have,
// compared by advisory and package, so a critical that is still there
// tomorrow is not news tomorrow — and one that comes back after a rollback is.
//
// previous is the app's scan before this one; nil when there was none, and
// then every critical is new.
func NewCriticals(previous, current []store.ScanFinding) []store.ScanFinding {
	known := map[string]bool{}
	for _, f := range previous {
		if f.Severity == SeverityCritical {
			known[f.ID+"\x00"+f.Package] = true
		}
	}
	var out []store.ScanFinding
	for _, f := range current {
		if f.Severity == SeverityCritical && !known[f.ID+"\x00"+f.Package] {
			out = append(out, f)
		}
	}
	return out
}

// Due picks the apps whose image is to be scanned again: not already queued
// or running, and not scanned within minAge — an app deployed at midnight was
// looked at then, and the 4 a.m. rescan has nothing to add. Never-scanned
// first, then the longest since.
//
// The rescan runs them one at a time, in this order: spreading the work is
// the queue's job, and this only says what goes in it.
func Due(candidates []store.ScanCandidate, now time.Time, minAge time.Duration) []store.ScanCandidate {
	var out []store.ScanCandidate
	for _, c := range candidates {
		if c.Pending || c.Image == "" {
			continue
		}
		if !c.LastScanned.IsZero() && now.Sub(c.LastScanned) < minAge {
			continue
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b store.ScanCandidate) int {
		if c := a.LastScanned.Compare(b.LastScanned); c != 0 {
			return c
		}
		return strings.Compare(a.AppID, b.AppID)
	})
	return out
}

// Describe lists findings for a sentence: "openssl 3.0.2 (fixed in 3.0.13,
// CVE-2024-0001)", at most limit of them, and how many more.
func Describe(findings []store.ScanFinding, limit int) string {
	parts := make([]string, 0, min(len(findings), limit)+1)
	for i, f := range findings {
		if i == limit {
			parts = append(parts, fmt.Sprintf("and %d more", len(findings)-limit))
			break
		}
		part := f.Package + " " + f.Installed
		if f.FixedIn != "" {
			part += " (fixed in " + f.FixedIn + ", " + f.ID + ")"
		} else {
			part += " (" + f.ID + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// Failure explains a scan that ended without a report, from what the scanner
// printed on its way out.
//
// The database comes first: a cluster with no way out to the internet cannot
// download it, and says so in a sentence that also mentions the image, so the
// order of these checks is the order that reads the message right.
func Failure(image, output string) *errdoc.Problem {
	detail := lastLines(output, 12, 1200)
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "vulnerability db") || strings.Contains(lower, "java db") ||
		strings.Contains(lower, "javadb") || strings.Contains(lower, "trivy-db"):
		return errdoc.ScanDatabaseUnavailable(detail)
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout"):
		return errdoc.ScanTimedOut(image)
	case strings.Contains(lower, "unable to find the specified image") ||
		strings.Contains(lower, "unauthorized") || strings.Contains(lower, "denied") ||
		strings.Contains(lower, "manifest unknown") || strings.Contains(lower, "manifest_unknown") ||
		strings.Contains(lower, "not found"):
		return errdoc.ScanPullFailed(image, detail)
	case strings.TrimSpace(output) == "":
		return errdoc.ScanFailed(image, "the scanner stopped without saying why")
	default:
		return errdoc.ScanFailed(image, detail)
	}
}

// lastLines is the end of some output, as much of it as a message can hold.
func lastLines(output string, lines, maxBytes int) string {
	all := strings.Split(strings.TrimSpace(output), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	text := strings.TrimSpace(strings.Join(all, "\n"))
	if len(text) > maxBytes {
		text = "…" + strings.ToValidUTF8(text[len(text)-maxBytes:], "")
	}
	return text
}
