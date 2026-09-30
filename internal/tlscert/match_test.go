package tlscert

import (
	"testing"
	"time"
)

// Which names cover which hostnames, as browsers read them: a wildcard is one
// label, never two and never none.
func TestWhichNamesCoverWhichHostnames(t *testing.T) {
	for _, c := range []struct {
		name, hostname string
		covers         bool
	}{
		{"shop.example.com", "shop.example.com", true},
		{"Shop.Example.com", "shop.example.COM", true},
		{"shop.example.com.", "shop.example.com", true},
		{"shop.example.com", "www.shop.example.com", false},
		{"shop.example.com", "example.com", false},

		{"*.example.com", "a.example.com", true},
		{"*.example.com", "shop.example.com", true},
		{"*.EXAMPLE.com", "a.example.com", true},
		{"*.example.com", "a.b.example.com", false},
		{"*.example.com", "example.com", false},
		{"*.example.com", ".example.com", false},
		{"*.example.com", "a.example.org", false},
		{"*.example.com", "aexample.com", false},
		{"*.shop.example.com", "a.shop.example.com", true},
		{"*.shop.example.com", "shop.example.com", false},

		// A star anywhere but the whole first label is not a wildcard, and a
		// browser matches nothing with it.
		{"a*.example.com", "ab.example.com", false},
		{"a*.example.com", "a*.example.com", false},
		{"*.*.example.com", "a.b.example.com", false},
		{"*", "localhost", false},
		{"*.", "a.", false},
		{"", "", false},
	} {
		if got := Covers(c.name, c.hostname); got != c.covers {
			t.Errorf("Covers(%q, %q) = %v, want %v", c.name, c.hostname, got, c.covers)
		}
	}
}

// Of several certificates that cover a hostname, the one that runs out last.
func TestTheCertificateThatExpiresLastIsChosen(t *testing.T) {
	now := time.Now()
	wildcard := Candidate{ID: "crt_wild", Hostnames: []string{"*.example.com"}, NotAfter: now.Add(30 * 24 * time.Hour)}
	renewal := Candidate{ID: "crt_renew", Hostnames: []string{"*.example.com", "example.com"}, NotAfter: now.Add(400 * 24 * time.Hour)}
	exact := Candidate{ID: "crt_exact", Hostnames: []string{"shop.example.com"}, NotAfter: now.Add(60 * 24 * time.Hour)}
	expired := Candidate{ID: "crt_old", Hostnames: []string{"legacy.example.org"}, NotAfter: now.Add(-24 * time.Hour)}
	candidates := []Candidate{wildcard, exact, renewal, expired}

	for hostname, want := range map[string]string{
		"shop.example.com": "crt_renew",
		"blog.example.com": "crt_renew",
		"example.com":      "crt_renew",
		// Chosen though expired: it is the only one, and the panel does not
		// hand the hostname to another issuer behind anybody's back.
		"legacy.example.org": "crt_old",
		"a.b.example.com":    "",
		"example.org":        "",
	} {
		got := ""
		if i := Best(hostname, candidates); i >= 0 {
			got = candidates[i].ID
		}
		if got != want {
			t.Errorf("%s is served with %q, want %q", hostname, got, want)
		}
	}

	// Without the renewal, the exact one lasts longer than the wildcard.
	if i := Best("shop.example.com", []Candidate{wildcard, exact}); i != 1 {
		t.Errorf("chose %d, want the exact certificate that lasts longer", i)
	}
	// A tie is decided the same way every time.
	twin := Candidate{ID: "crt_a", Hostnames: []string{"*.example.com"}, NotAfter: wildcard.NotAfter}
	for range 5 {
		if i := Best("a.example.com", []Candidate{wildcard, twin}); i != 1 {
			t.Fatalf("a tie chose %d, want the lower id", i)
		}
	}
}

func TestACertificatesStateFollowsItsDate(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		left time.Duration
		want string
	}{
		{90 * 24 * time.Hour, StateValid},
		{21*24*time.Hour + time.Minute, StateValid},
		{21 * 24 * time.Hour, StateExpiring},
		{time.Hour, StateExpiring},
		{0, StateExpired},
		{-time.Hour, StateExpired},
	} {
		if got := State(now.Add(c.left), now); got != c.want {
			t.Errorf("%s left is %s, want %s", c.left, got, c.want)
		}
	}
}

// Each threshold once, the smallest one the time left is inside, and nothing
// again once it has been sent.
func TestEachExpiryWarningIsDueOnce(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, c := range []struct {
		left     time.Duration
		notified int
		due      int
	}{
		{30 * day, 0, 0},
		{21*day + time.Hour, 0, 0},
		{21 * day, 0, 21},
		{20 * day, 21, 0},
		{8 * day, 21, 0},
		{7 * day, 21, 7},
		{6 * day, 7, 0},
		{day, 7, 1},
		{time.Hour, 1, 0},
		// Uploaded with five days left: warned once, at 7, not at 21 as well.
		{5 * day, 0, 7},
		// Expired without ever having been warned: the last warning.
		{-day, 0, 1},
		{-day, 1, 0},
	} {
		days, ok := DueThreshold(now.Add(c.left), now, c.notified)
		if c.due == 0 && ok {
			t.Errorf("%s left, %d sent: %d is due, want none", c.left, c.notified, days)
		}
		if c.due != 0 && (!ok || days != c.due) {
			t.Errorf("%s left, %d sent: due %d (%v), want %d", c.left, c.notified, days, ok, c.due)
		}
	}
}
