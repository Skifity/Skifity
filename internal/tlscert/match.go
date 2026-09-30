package tlscert

import (
	"strings"
	"time"
)

// Which certificate a hostname is served with.
//
// Matching is automatic rather than chosen per domain. A certificate names the
// hostnames it is for, and that is already the choice: asking somebody to pick
// it again on every domain is asking them to say the same thing twice, and the
// second time is the one they forget — a new subdomain under a wildcard they
// uploaded would quietly go to Let's Encrypt instead. So a hostname a team
// certificate covers uses it, and each domain says which one it uses and until
// when, where the person looking at the domain will see it.
//
// A wildcard covers exactly one label, as browsers read it (RFC 6125 6.4.3):
// *.example.com covers a.example.com, and neither a.b.example.com nor
// example.com itself. Only a leading "*." is a wildcard; a name with a star
// anywhere else matches nothing, which is also what a browser does with it.

// Normalize is how a hostname and a certificate name are compared: lower case,
// no surrounding space, no trailing dot.
func Normalize(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// Covers reports whether one of a certificate's names covers a hostname.
func Covers(name, hostname string) bool {
	name, hostname = Normalize(name), Normalize(hostname)
	if name == "" || hostname == "" {
		return false
	}
	suffix, wildcard := strings.CutPrefix(name, "*.")
	if !wildcard {
		return !strings.Contains(name, "*") && name == hostname
	}
	if suffix == "" || strings.Contains(suffix, "*") {
		return false
	}
	label, rest, found := strings.Cut(hostname, ".")
	return found && label != "" && rest == suffix
}

// CoversAny reports whether any of a certificate's names covers a hostname.
func CoversAny(names []string, hostname string) bool {
	for _, name := range names {
		if Covers(name, hostname) {
			return true
		}
	}
	return false
}

// Candidate is one certificate a hostname might be served with.
type Candidate struct {
	ID        string
	Hostnames []string
	NotAfter  time.Time
}

// Best picks the certificate a hostname is served with: of those that cover
// it, the one that expires last. It returns -1 when none does.
//
// Expiring last rather than uploaded last: a renewal is uploaded before the
// old one runs out, and the two cover the same names. Picking the newer upload
// would do the same thing there, and the wrong thing when somebody uploads an
// older certificate for one extra name — the whole wildcard would move to the
// one that runs out first. An expired certificate is still chosen when it is
// the only one: the panel does not quietly hand a hostname to a different
// issuer, and the domain says Expired, which is what somebody was warned
// about at 21, 7 and 1 days.
//
// A tie goes to the lower id, so the answer is the same on every sync.
func Best(hostname string, candidates []Candidate) int {
	best := -1
	for i, candidate := range candidates {
		if !CoversAny(candidate.Hostnames, hostname) {
			continue
		}
		if best < 0 || candidate.NotAfter.After(candidates[best].NotAfter) ||
			(candidate.NotAfter.Equal(candidates[best].NotAfter) && candidate.ID < candidates[best].ID) {
			best = i
		}
	}
	return best
}

// Expiry states, as a domain and a certificate show them.
const (
	StateValid    = "valid"
	StateExpiring = "expiring"
	StateExpired  = "expired"
)

// ExpiringWithin is when a certificate starts being called expiring: the first
// of the thresholds below.
const ExpiringWithin = 21 * 24 * time.Hour

// State is what a certificate that runs out at notAfter is, now.
func State(notAfter, now time.Time) string {
	switch {
	case !now.Before(notAfter):
		return StateExpired
	case notAfter.Sub(now) <= ExpiringWithin:
		return StateExpiring
	}
	return StateValid
}

// Thresholds are the days before expiry a team is told, once each.
var Thresholds = []int{21, 7, 1}

// DueThreshold is the warning a certificate is owed now, given the smallest
// threshold already sent for it (0 for none), and whether one is owed at all.
//
// It is the smallest threshold the time left is inside, so a certificate
// uploaded with five days left is warned once, at 7, rather than at 21 and 7
// in the same minute; and one past its date is owed the last warning if it
// never had it.
func DueThreshold(notAfter, now time.Time, notified int) (int, bool) {
	left := notAfter.Sub(now)
	due := 0
	for _, days := range Thresholds {
		if left <= time.Duration(days)*24*time.Hour {
			due = days
		}
	}
	if due == 0 || (notified != 0 && notified <= due) {
		return 0, false
	}
	return due, true
}
