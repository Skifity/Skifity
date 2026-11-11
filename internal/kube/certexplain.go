package kube

import "strings"

// Saying what to do about a certificate that is not being issued.
//
// cert-manager says why in ACME's words — orders, authorizations, a self
// check — and the fix is nearly always somewhere those words do not name: a
// DNS record, port 80, a limit only time lifts. The certificate.failed
// notification passed its words on alone, so the person told was left to
// work out from "Issuing certificate as Secret does not exist" that their DNS
// record pointed at their old host.

// ExplainCertificate is the fix for a certificate cert-manager has not issued,
// from the reason it gives. Anything it does not recognise gets the fix for
// the commonest cause, a hostname that does not point here yet: a certificate
// still saying it is being issued after a quarter of an hour, which is when
// the watcher calls it failed, is almost always waiting on exactly that.
func ExplainCertificate(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	// First: a name that is rate limited fails every other way too, and
	// changing DNS or a firewall will not get it a certificate this week.
	case containsAny(lower, "ratelimited", "rate limit", "too many certificates", "too many failed authorizations",
		"too many new orders", "too many registrations"):
		return certificateFixes["rate_limited"]
	case strings.Contains(lower, "caa"):
		return certificateFixes["caa"]
	case containsAny(lower, "no such host", "nxdomain", "servfail", "dns problem", "no valid ip addresses",
		"no a records", "no aaaa records"):
		return certificateFixes["dns"]
	case containsAny(lower, "connection refused", "i/o timeout", "timeout during connect", "deadline exceeded",
		"connection reset", "wrong status code", "invalid response", "self check", "unauthorized"):
		return certificateFixes["http01"]
	default:
		return certificateFixes["dns"]
	}
}

// certificateFixes are the sentences above, by the cause they are for.
var certificateFixes = map[string]string{
	"rate_limited": "Let's Encrypt has stopped issuing for this name for a while, because too many " +
		"certificates or failed attempts were asked for recently. Once DNS is right nothing else needs " +
		"changing: the panel keeps asking, and the limit lifts by itself within a week at most. Removing " +
		"and adding the domain again asks again, and keeps the limit in place.",
	"caa": "A CAA record on the domain does not allow Let's Encrypt to issue certificates for it. " +
		"Add letsencrypt.org to the domain's CAA records at your DNS provider, or remove them.",
	"dns": "The hostname does not point at this server yet. Create the DNS record the app's Domains tab " +
		"shows, use Check DNS there to see that it has taken effect, and wait: the panel asks for the " +
		"certificate again by itself.",
	"http01": "Let's Encrypt could not reach this server on port 80 to check that the name is yours. " +
		"Open port 80 to the internet in the server's firewall and the cloud provider's, and if the name " +
		"is behind a proxy such as Cloudflare, let /.well-known/acme-challenge/ through to the server.",
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}
