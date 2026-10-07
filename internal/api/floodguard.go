package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"skifity/internal/errdoc"
)

// A flood from one address should cost the panel one address's worth of work.
//
// Sign-in has its own lockouts, per account and per address, and they are about
// guessing a password. Everything else answered anybody who asked, as fast as
// they asked, from a SQLite file on one disk: a script looping on any endpoint
// that reads the database — a bad session cookie is enough — kept the panel busy
// at the expense of the people using it. This is a token bucket per address in
// front of the API, generous enough that no person and no sensible script
// notices it: two hundred requests a second sustained, with room for six
// hundred at once, which is more than a page of the interface asks for when it
// opens.
//
// It limits an address, not a person. Behind a proxy the panel is told how many
// of them there are (SKIFITY_TRUSTED_PROXY_COUNT); with the wrong number every
// visitor shares the proxy's address and one bucket, which is why the numbers
// are high and why nothing here locks anything: a caller over the line is told
// to wait a second, and is let in again at once when the bucket has refilled.
const (
	floodRate  = 200.0
	floodBurst = 600.0
	// A bucket nobody has touched for this long is full again, so it is
	// forgotten rather than kept for ever.
	floodForget = 5 * time.Minute
	// The most addresses remembered at once. A flood from many addresses must not
	// be able to grow this without bound; when it is full, the oldest are dropped.
	floodMaxBuckets = 50000
)

type floodBucket struct {
	tokens float64
	last   time.Time
}

type floodGuard struct {
	mu      sync.Mutex
	buckets map[string]*floodBucket
	rate    float64
	burst   float64
	now     func() time.Time
}

func newFloodGuard() *floodGuard {
	return &floodGuard{buckets: map[string]*floodBucket{}, rate: floodRate, burst: floodBurst, now: time.Now}
}

// allow takes a token for the address, or says how many seconds to wait.
func (g *floodGuard) allow(address string) (ok bool, retryAfter int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	bucket, found := g.buckets[address]
	if !found {
		if len(g.buckets) >= floodMaxBuckets {
			g.pruneLocked(now, true)
		}
		bucket = &floodBucket{tokens: g.burst, last: now}
		g.buckets[address] = bucket
	}
	bucket.tokens = min(g.burst, bucket.tokens+now.Sub(bucket.last).Seconds()*g.rate)
	bucket.last = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	// Time until one token has come back, rounded up: a client told "0" would
	// retry at once and be refused again.
	wait := int((1-bucket.tokens)/g.rate) + 1
	return false, wait
}

// pruneLocked forgets buckets that have been idle long enough to be full again,
// and, when asked to make room, the quarter that has been idle longest.
func (g *floodGuard) pruneLocked(now time.Time, makeRoom bool) {
	for address, bucket := range g.buckets {
		if now.Sub(bucket.last) > floodForget {
			delete(g.buckets, address)
		}
	}
	if !makeRoom || len(g.buckets) < floodMaxBuckets {
		return
	}
	// Still full of recent addresses: drop whichever are oldest. Not exact, and
	// it need not be: what matters is that the map stops at its bound.
	cutoff := floodForget / 5
	for len(g.buckets) >= floodMaxBuckets*3/4 && cutoff > time.Second {
		for address, bucket := range g.buckets {
			if now.Sub(bucket.last) > cutoff {
				delete(g.buckets, address)
			}
		}
		cutoff /= 2
	}
	// Last resort, for a flood that is all one second old.
	for address := range g.buckets {
		if len(g.buckets) < floodMaxBuckets*3/4 {
			break
		}
		delete(g.buckets, address)
	}
}

// sweep is the periodic version of pruneLocked, so a quiet panel does not hold
// the addresses of last week's visitors.
func (g *floodGuard) sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneLocked(g.now(), false)
}

// floodLimit is the middleware: it counts every API request against the caller's
// address, before anything reads the database for it.
func (s *Server) floodLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, wait := s.flood.allow(clientIPFrom(r.Context()))
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		writeError(w, r, errdoc.TooManyRequests(wait))
	})
}
