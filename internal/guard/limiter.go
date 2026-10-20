package guard

import (
	"net/netip"
	"sync"
	"time"

	"skifity/internal/edgerules"
)

// Counting for the firewall's rate limits.
//
// A fixed window for each rule and visitor: the first request starts a window
// as long as the rule's period, every request in it is counted, and the next
// window starts from nothing. A sliding window is smoother at its edges and
// costs a list of timestamps per visitor; for a limit somebody writes as "five
// a minute", the difference is a visitor who times their attempts to the
// second getting ten in two seconds once, and then waiting like everybody else.
//
// Memory is the risk, and it is bounded. Every window ends, ended windows are
// swept, and past maxTracked visitors a new one is not counted at all rather
// than counted by throwing away somebody else's count. That is the limiter
// failing open, which is the right way for it to fail: a flood from more
// addresses than this is a volumetric attack, which a count per address does
// not stop, and a limiter that refused everybody once it was full would turn
// that attack into an outage of its own making.
//
// An IPv6 visitor is counted by their /64. A machine is usually given a whole
// one, and a count per address is a limit anybody can step around by changing
// the last few bits of theirs.

// maxTracked is how many rule-and-visitor windows the guard keeps at once.
const maxTracked = 200_000

type limitKey struct {
	rule    string
	visitor netip.Prefix
}

type limitWindow struct {
	ends  time.Time
	count int
}

// limiter is the guard's edgerules.Counter.
type limiter struct {
	now func() time.Time
	// refused is told the first time a visitor goes over a limit in a window,
	// so the guard can log that once rather than once per refused request.
	refused func(ruleID string, visitor netip.Prefix)

	mu      sync.Mutex
	windows map[limitKey]*limitWindow
}

func newLimiter(now func() time.Time, refused func(string, netip.Prefix)) *limiter {
	if now == nil {
		now = time.Now
	}
	return &limiter{now: now, refused: refused, windows: map[limitKey]*limitWindow{}}
}

// visitor is the address a count is kept against.
func visitor(ip netip.Addr) netip.Prefix {
	ip = ip.Unmap()
	if ip.Is4() {
		return netip.PrefixFrom(ip, 32)
	}
	prefix, err := ip.Prefix(64)
	if err != nil {
		return netip.PrefixFrom(ip, ip.BitLen())
	}
	return prefix
}

// Over counts one request and reports whether the visitor has gone over the
// rule's limit in the current window.
func (l *limiter) Over(ruleID string, ip netip.Addr, limit edgerules.Limit) bool {
	key := limitKey{rule: ruleID, visitor: visitor(ip)}
	period := time.Duration(limit.PeriodSeconds) * time.Second

	l.mu.Lock()
	now := l.now()
	window, found := l.windows[key]
	if !found || !now.Before(window.ends) {
		if !found && len(l.windows) >= maxTracked {
			l.sweepLocked(now)
			if len(l.windows) >= maxTracked {
				l.mu.Unlock()
				return false
			}
		}
		window = &limitWindow{ends: now.Add(period)}
		l.windows[key] = window
	}
	window.count++
	count := window.count
	l.mu.Unlock()

	if count == limit.Requests+1 && l.refused != nil {
		l.refused(ruleID, key.visitor)
	}
	return count > limit.Requests
}

// sweep forgets every window that has ended.
func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(l.now())
}

func (l *limiter) sweepLocked(now time.Time) {
	for key, window := range l.windows {
		if !now.Before(window.ends) {
			delete(l.windows, key)
		}
	}
}

// tracked is how many windows are open, for the tests.
func (l *limiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.windows)
}
