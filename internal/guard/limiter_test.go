package guard

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"skifity/internal/edgerules"
)

// clock is a time the test moves by hand.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func TestAVisitorGetsTheLimitInEachWindow(t *testing.T) {
	at := &clock{now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	limits := newLimiter(at.Now, nil)
	limit := edgerules.Limit{Requests: 3, PeriodSeconds: 60}
	visitor := netip.MustParseAddr("198.51.100.7")

	for i := 1; i <= 3; i++ {
		if limits.Over("login", visitor, limit) {
			t.Fatalf("request %d of 3 was over the limit", i)
		}
	}
	if !limits.Over("login", visitor, limit) {
		t.Fatal("the fourth request in a minute was not over a limit of three")
	}

	// Somebody else, and the same visitor under a different rule, count apart.
	if limits.Over("login", netip.MustParseAddr("198.51.100.8"), limit) {
		t.Error("one visitor's requests counted against another")
	}
	if limits.Over("search", visitor, limit) {
		t.Error("one rule's count was used for another")
	}

	// A minute later the window is a new one.
	at.now = at.now.Add(time.Minute)
	if limits.Over("login", visitor, limit) {
		t.Error("the visitor was still limited after the window ended")
	}
}

func TestAnIPv6VisitorIsCountedByTheirNetwork(t *testing.T) {
	limits := newLimiter(nil, nil)
	limit := edgerules.Limit{Requests: 2, PeriodSeconds: 60}
	// Three addresses in one /64: one machine changing the last bits of its
	// address is still one visitor.
	for _, address := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2"} {
		if limits.Over("login", netip.MustParseAddr(address), limit) {
			t.Fatalf("%s was over a limit of two too early", address)
		}
	}
	if !limits.Over("login", netip.MustParseAddr("2001:db8:1:2:ffff::9"), limit) {
		t.Error("a third address in the same /64 was not counted with the first two")
	}
	if limits.Over("login", netip.MustParseAddr("2001:db8:1:3::1"), limit) {
		t.Error("a different /64 was counted with the first")
	}
}

func TestEndedWindowsAreForgotten(t *testing.T) {
	at := &clock{now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	limits := newLimiter(at.Now, nil)
	for i := range 50 {
		limits.Over("login", netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}), edgerules.Limit{Requests: 5, PeriodSeconds: 10})
	}
	if limits.tracked() != 50 {
		t.Fatalf("%d windows are open, want 50", limits.tracked())
	}
	at.now = at.now.Add(11 * time.Second)
	limits.sweep()
	if limits.tracked() != 0 {
		t.Errorf("%d windows are still held after every one of them ended", limits.tracked())
	}
}

// Past maxTracked visitors a new one is not counted, rather than counted by
// forgetting somebody whose window is still open — and not refused, because a
// limiter that refuses everybody once it is full is an outage.
func TestAFullLimiterFailsOpenForNewVisitors(t *testing.T) {
	limits := newLimiter(nil, nil)
	limit := edgerules.Limit{Requests: 1, PeriodSeconds: 3600}
	for i := range maxTracked {
		limits.Over("r", netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}), limit)
	}
	stranger := netip.MustParseAddr("198.51.100.200")
	for range 3 {
		if limits.Over("r", stranger, limit) {
			t.Fatal("a visitor the full limiter could not count was refused")
		}
	}
	// Those already counted still are.
	if !limits.Over("r", netip.AddrFrom4([4]byte{10, 0, 0, 0}), limit) {
		t.Error("a visitor already over their limit was let through once the limiter filled")
	}
}

func TestARefusalIsReportedOncePerWindow(t *testing.T) {
	var reported []string
	limits := newLimiter(nil, func(ruleID string, visitor netip.Prefix) {
		reported = append(reported, ruleID+" "+visitor.String())
	})
	for range 10 {
		limits.Over("login", netip.MustParseAddr("198.51.100.7"), edgerules.Limit{Requests: 2, PeriodSeconds: 60})
	}
	if len(reported) != 1 || reported[0] != "login 198.51.100.7/32" {
		t.Errorf("the refusals were reported as %v, want once", reported)
	}
}

// End to end, the way Traefik asks: the limit applies to the visitor the
// forwarded headers name, and an over-the-limit answer is a 429 that says when
// to come back.
func TestTheGuardAnswersTooManyRequestsOverALimit(t *testing.T) {
	g := newGuard(t, Config{Sets: map[string]Protected{
		"shop.example.com": {
			AppID: "app_1",
			RuleSet: edgerules.RuleSet{
				Default: edgerules.ActionAllow,
				Rules: []edgerules.Rule{{
					ID: "login", Name: "the login page", Action: edgerules.ActionLimit, Enabled: true,
					Limit: &edgerules.Limit{Requests: 2, PeriodSeconds: 30},
					Expr: edgerules.Expr{Test: &edgerules.Test{
						Field: edgerules.FieldPath, Op: edgerules.OpStartsWith, Values: []string{"/login"},
					}},
				}},
			},
		},
	}})

	ask := func(path, client string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, forwarded("shop.example.com", path, client))
		return w
	}
	for i := 1; i <= 2; i++ {
		if w := ask("/login", "203.0.113.9"); w.Code != http.StatusOK {
			t.Fatalf("attempt %d answered %d, want 200", i, w.Code)
		}
	}
	w := ask("/login", "203.0.113.9")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the third attempt answered %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After is %q, want the period, 30", got)
	}

	// The rest of the site, and another visitor, are untouched.
	if w := ask("/products", "203.0.113.9"); w.Code != http.StatusOK {
		t.Errorf("a page the rule does not cover answered %d", w.Code)
	}
	if w := ask("/login", "203.0.113.10"); w.Code != http.StatusOK {
		t.Errorf("another visitor's first attempt answered %d", w.Code)
	}
}
