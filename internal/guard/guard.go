// Package guard is the process that actually stops a request.
//
// # Why this is not the panel
//
// Traefik can ask an external service whether to let a request through, which
// is how Authelia and oauth2-proxy attach to it, and the obvious place to put
// that service is the panel: it holds the rules already. It is the wrong place.
// The panel's own Deployment uses the Recreate strategy, because its database
// is a file on one node's disk — so every upgrade stops the panel, and every
// protected app would stop with it. A firewall that takes the sites down when
// the panel is upgraded is a firewall people turn off.
//
// So the guard is its own Deployment, with two replicas spread across servers,
// and it is the same binary: `skifity edge-guard`. It has no database, no
// Kubernetes API access and no credentials. Its whole state is a file the panel
// writes into a ConfigMap and the kubelet drops into its filesystem.
//
// # Failing
//
// Traefik has no way to ignore an authorizer that does not answer: an
// unreachable one is a 500 and the request does not reach the app. So a guard
// that is entirely down takes the protected sites down with it, and there is no
// per-site knob that could change that — the decision is Traefik's, before this
// process is reached. An earlier draft of this file had one, which would have
// been a switch in the panel that controlled nothing.
//
// What can be done is done: two replicas spread across servers, and a readiness
// probe that reports a replica with no rules loaded as not ready, so a rollout
// never puts one in front of traffic it cannot judge. And the guard never fails
// open for a reason it could have avoided — a rules file it cannot parse leaves
// the previous one in place rather than becoming an empty rule set that allows
// everybody.
package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"skifity/internal/edgerules"
	"skifity/internal/geoip"
	"skifity/internal/runsafe"
)

// Config is what the panel writes and the guard reads.
type Config struct {
	// Sets is keyed by hostname, because a hostname is what Traefik forwards
	// and what a rule set is attached to in the end. An app with three domains
	// appears three times, which costs nothing and means the guard never has to
	// know what an app is.
	Sets map[string]Protected `json:"sets"`
	// TrustedProxies are the ranges that may speak for somebody else, on top of
	// the pod and service networks.
	TrustedProxies []string `json:"trusted_proxies,omitempty"`
	// Cloudflare says the tunnel is installed, so the CF headers may be
	// believed when the peer handing them over is one of ours.
	Cloudflare bool `json:"cloudflare,omitempty"`
	// CountryURL and ASNURL are where the geo databases come from. Empty means
	// that question cannot be answered, which the rules engine turns into
	// "unknown" rather than into a decision.
	CountryURL string `json:"country_url,omitempty"`
	ASNURL     string `json:"asn_url,omitempty"`
}

// Protected is one hostname's rules.
type Protected struct {
	// AppID is carried only so a blocked request can be logged against
	// something the panel can look up.
	AppID   string            `json:"app_id"`
	RuleSet edgerules.RuleSet `json:"rules"`
}

// Options configure a guard.
type Options struct {
	// ConfigPath is the file the panel's ConfigMap is mounted at.
	ConfigPath string
	// DataDir is where the geo databases are kept. An emptyDir is right: they
	// are downloaded again in a few seconds and are not worth a volume.
	DataDir string
	// PodCIDR and ServiceCIDR are the cluster's own networks, which are the
	// proxies that are true on every install.
	PodCIDR     string
	ServiceCIDR string
	Log         *slog.Logger
}

// Guard holds the loaded rules and answers Traefik.
type Guard struct {
	opts   Options
	log    *slog.Logger
	lookup *geoip.Lookup

	mu      sync.RWMutex
	config  Config
	trust   edgerules.Trust
	loaded  bool
	modTime time.Time
}

// New builds a guard. It has loaded nothing yet; Reload does that.
func New(opts Options) *Guard {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Guard{opts: opts, log: log, lookup: geoip.New()}
}

// Close releases the geo databases.
func (g *Guard) Close() { g.lookup.Close() }

// Reload reads the rules file if it has changed since the last read.
//
// A file it cannot parse leaves the previous rules in place. The alternative —
// an empty rule set — is a firewall that turns itself off when somebody writes
// a bad byte, which is the one behaviour a firewall must never have.
func (g *Guard) Reload() error {
	info, err := os.Stat(g.opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read the rules: %w", err)
	}
	g.mu.RLock()
	unchanged := g.loaded && info.ModTime().Equal(g.modTime)
	g.mu.RUnlock()
	if unchanged {
		return nil
	}

	raw, err := os.ReadFile(g.opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read the rules: %w", err)
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("the rules file is not readable, so the previous rules are still in force: %w", err)
	}

	trust := edgerules.Trust{
		Proxies:    edgerules.DefaultProxies(g.opts.PodCIDR, g.opts.ServiceCIDR),
		Cloudflare: config.Cloudflare,
	}
	trust.Proxies = append(trust.Proxies,
		edgerules.ParseProxies(strings.Join(config.TrustedProxies, ","))...)

	g.mu.Lock()
	g.config = config
	g.trust = trust
	g.loaded = true
	g.modTime = info.ModTime()
	g.mu.Unlock()

	g.log.Info("rules loaded", "hostnames", len(config.Sets))
	return nil
}

// RefreshGeo downloads the geo databases named in the config and loads them.
func (g *Guard) RefreshGeo(ctx context.Context, client *http.Client, now time.Time) {
	g.mu.RLock()
	countryURL, asnURL := g.config.CountryURL, g.config.ASNURL
	g.mu.RUnlock()

	fetcher := geoip.Fetcher{Client: client, Dir: g.opts.DataDir}
	if path, err := fetcher.Fetch(ctx, "country", countryURL, now); err != nil {
		g.log.Warn("no country database", "error", err)
	} else if err := g.lookup.LoadCountry(path); err != nil {
		g.log.Warn("the country database would not open", "error", err)
	}
	if path, err := fetcher.Fetch(ctx, "asn", asnURL, now); err != nil {
		g.log.Warn("no network database", "error", err)
	} else if err := g.lookup.LoadASN(path); err != nil {
		g.log.Warn("the network database would not open", "error", err)
	}
}

// Decide answers one forwarded request.
//
// Traefik sends the original request's method, host and path in X-Forwarded-*
// headers and the original headers as themselves, which is what makes a rule on
// a header or a user agent work at all.
// It returns the request as it judged it, so that what is logged is what the
// rules actually saw. Logging the raw header instead said one thing while the
// rules used another, which is the worst possible state for a record somebody
// consults after being blocked.
func (g *Guard) Decide(r *http.Request) (edgerules.Decision, edgerules.Request, bool) {
	g.mu.RLock()
	config, trust, loaded := g.config, g.trust, g.loaded
	g.mu.RUnlock()

	// The peer first, because everything below is decided by whether the hop
	// that sent these headers is one of ours.
	peer := peerAddr(r)

	// X-Forwarded-Host chooses which rule set applies, and a host with no set
	// is allowed through. Believing it from anybody was therefore a way past
	// the whole firewall with one header: name a hostname nobody protects and
	// there are no rules to fail. ClientIP has always asked whether the hop is
	// trusted before believing what it says; every other forwarded header now
	// asks the same question.
	host := hostname(firstNonEmpty(forwardedByAProxy(r, "X-Forwarded-Host", peer, trust), r.Host))
	protected, found := config.Sets[host]
	if !loaded {
		// Nothing has ever been loaded, so nothing can be said about this
		// request. Refusing is the only honest answer for a process whose whole
		// purpose is to refuse.
		return edgerules.Decision{Action: edgerules.ActionBlock}, edgerules.Request{}, false
	}
	if !found {
		// A hostname with no rules is a hostname nobody asked to protect. It
		// should not be reaching the guard at all, and letting it through is
		// better than breaking a site because a middleware outlived its rules.
		return edgerules.Decision{Action: edgerules.ActionAllow}, edgerules.Request{Host: host}, true
	}

	ip := edgerules.ClientIP(peer, r.Header, trust)

	request := edgerules.Request{
		IP:        ip,
		Country:   edgerules.ClientCountry(peer, r.Header, trust),
		Host:      host,
		Path:      pathOf(r, peer, trust),
		Method:    firstNonEmpty(forwardedByAProxy(r, "X-Forwarded-Method", peer, trust), r.Method),
		UserAgent: r.Header.Get("User-Agent"),
		Header:    r.Header.Get,
	}
	// The database is only consulted for what the headers did not answer, so a
	// cluster behind the tunnel pays nothing for a country rule.
	needCountry, needASN := protected.RuleSet.Needs()
	if (needCountry && request.Country == "") || needASN {
		result := g.lookup.Lookup(ip)
		if request.Country == "" {
			request.Country = result.Country
		}
		request.ASN = result.ASN
	}

	return protected.RuleSet.Evaluate(request), request, true
}

// Handler is what Traefik talks to.
func (g *Guard) Handler() http.Handler {
	mux := http.NewServeMux()

	// Liveness and readiness. A guard that has never loaded its rules is not
	// ready, so a rollout does not put it in front of traffic it cannot judge.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		loaded := g.loaded
		g.mu.RUnlock()
		if !loaded {
			http.Error(w, "no rules loaded", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		decision, judged, decided := g.Decide(r)
		if !decided {
			// No rules have ever been loaded, so nothing can be said about this
			// request. The readiness probe reports the same thing, so this
			// should be unreachable in a running cluster — it is the answer for
			// the moment between a pod starting and the kubelet noticing.
			http.Error(w, "This site is not accepting requests right now.", http.StatusServiceUnavailable)
			return
		}
		if decision.Action == edgerules.ActionBlock {
			g.log.Info("blocked",
				"host", judged.Host,
				"rule", decision.RuleName,
				"path", judged.Path)
			// No detail in the body. Telling somebody which rule stopped them
			// is telling them what to change.
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if len(decision.Skipped) > 0 {
			// Loud, because a rule that cannot be evaluated is a rule that is
			// not protecting anything and the operator has to find out.
			g.log.Warn("rules were skipped because something they test is not known",
				"host", judged.Host, "rules", strings.Join(decision.Skipped, ", "))
		}
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

// Run serves until the context is cancelled, reloading the rules as they change
// and refreshing the geo databases daily.
func (g *Guard) Run(ctx context.Context, address string) error {
	if err := g.Reload(); err != nil {
		// Not fatal: the ConfigMap may not be mounted yet on the first start,
		// and the readiness probe keeps this replica out of the way until it is.
		g.log.Warn("no rules yet", "error", err)
	}
	g.RefreshGeo(ctx, nil, time.Now())

	go g.watch(ctx)

	server := &http.Server{
		Addr:              address,
		Handler:           g.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		// The guard is the only thing standing in front of an app. A panic in
		// it is that app unreachable, so even the shutdown watcher recovers.
		defer runsafe.Recover(g.log, "shutting the guard down", nil)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	g.log.Info("guard listening", "address", address)
	if err := server.ListenAndServe(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// watch reloads the rules as the kubelet updates the mounted ConfigMap, and
// refreshes the geo databases once a day.
//
// Polling rather than inotify: a ConfigMap arrives as a swapped symlink, which
// inotify reports in a way that differs between kubelet versions, and five
// seconds of lag on a rule change is not worth being clever about.
func (g *Guard) watch(ctx context.Context) {
	rules := time.NewTicker(5 * time.Second)
	defer rules.Stop()
	geo := time.NewTicker(24 * time.Hour)
	defer geo.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rules.C:
			if err := g.Reload(); err != nil {
				g.log.Warn("could not reload the rules", "error", err)
			}
		case <-geo.C:
			g.RefreshGeo(ctx, nil, time.Now())
		}
	}
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// hostname strips a port and lowercases, because a Host header carries one and
// a rule is written without.
func hostname(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.TrimSuffix(value, ".")
}

// pathOf is the original path, without the query string.
//
// Traefik sends it in X-Forwarded-Uri, which includes the query; a rule is
// written about the path, and a rule on "/admin" that a "?next=/admin" could
// satisfy would be a rule that means nothing.
func pathOf(r *http.Request, peer netip.Addr, trust edgerules.Trust) string {
	uri := firstNonEmpty(forwardedByAProxy(r, "X-Forwarded-Uri", peer, trust), r.URL.Path)
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	if uri == "" {
		return "/"
	}
	return uri
}

// forwardedByAProxy reads an X-Forwarded-* header, and only from a hop we
// trust to have written it.
//
// Two rules in one place. A header that says what the original request was is
// only worth anything when something of ours put it there, or a client can
// describe its own request however it likes and be judged on the description.
// And when the header arrived more than once it is the last line that the
// nearest hop added; the first is whatever the client sent.
func forwardedByAProxy(r *http.Request, name string, peer netip.Addr, trust edgerules.Trust) string {
	if !trust.Contains(peer) {
		return ""
	}
	return edgerules.LastHeaderValue(r.Header, name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
