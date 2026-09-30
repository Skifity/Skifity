package guard

import (
	"fmt"
	"html"
	"net/http"
	"net/netip"
	"strings"

	"skifity/internal/edgerules"
)

// Maintenance is an app answering visitors with a page instead of itself.
//
// The guard is already Traefik's forward-auth for a protected app, and
// whatever it answers other than a 2xx is what the visitor gets, headers and
// body. So maintenance is one more answer here rather than a second proxy, and
// nothing about the app's own Deployment changes: turning it off is instant,
// and the app is warm when it is.
type Maintenance struct {
	// Message is shown as written, in the team's own words and language.
	Message string `json:"message"`
	// Allow are addresses and ranges that still reach the app, so whoever is
	// doing the work can check it before anybody else sees it.
	Allow []string `json:"allow,omitempty"`
}

// MaintenanceRetryAfter is what the page tells browsers and crawlers: come
// back in five minutes. A crawler that sees a 503 with it keeps the page
// indexed rather than dropping it.
const MaintenanceRetryAfter = "300"

// ParseAllowed reads an allow list entry: an address, or a range.
func ParseAllowed(entry string) (netip.Prefix, error) {
	entry = strings.TrimSpace(entry)
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a range", entry)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// maintenanceFor returns the page to answer a request with, or nil when the
// app is not in maintenance or the request comes from an allowed address.
func (g *Guard) maintenanceFor(judged edgerules.Request) *Maintenance {
	g.mu.RLock()
	protected, found := g.config.Sets[judged.Host]
	g.mu.RUnlock()
	if !found || protected.Maintenance == nil {
		return nil
	}
	ip := judged.IP.Unmap()
	for _, entry := range protected.Maintenance.Allow {
		// An entry that does not parse allows nobody: it was checked when it
		// was saved, and failing closed here only shows the page.
		if prefix, err := ParseAllowed(entry); err == nil && ip.IsValid() && prefix.Contains(ip) {
			return nil
		}
	}
	return protected.Maintenance
}

// serveMaintenance writes the page. It holds nothing but the message: the
// visitor's language is not known here, and the team wrote the message in the
// one their visitors read.
func serveMaintenance(w http.ResponseWriter, m *Maintenance) {
	message := strings.TrimSpace(m.Message)
	title, _, _ := strings.Cut(message, "\n")
	var body strings.Builder
	for _, paragraph := range strings.Split(message, "\n") {
		if paragraph = strings.TrimSpace(paragraph); paragraph != "" {
			body.WriteString("<p>" + html.EscapeString(paragraph) + "</p>")
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", MaintenanceRetryAfter)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintf(w, maintenancePage, html.EscapeString(title), body.String())
}

const maintenancePage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>%s</title>
<style>
:root { color-scheme: light dark; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center;
  font: 16px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif;
  background: #fafafa; color: #18181b; }
main { max-width: 32rem; padding: 2rem 1rem; text-align: center; }
main p:first-child { font-size: 1.25rem; font-weight: 600; }
@media (prefers-color-scheme: dark) { body { background: #09090b; color: #fafafa; } }
</style>
</head>
<body><main>%s</main></body>
</html>
`
