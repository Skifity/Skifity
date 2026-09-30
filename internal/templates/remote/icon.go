package remote

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Logos from a private catalogue.
//
// The panel's own policy is `img-src 'self'`, and the built-in logos are served
// out of the binary for the reason that policy exists: a page that loads its
// pictures from somebody else's server tells that server who is looking at it
// and when. A private catalogue's `icon:` is an address on somebody else's
// server, so the browser is never sent there. The panel fetches the logo
// itself, once, when it refreshes the catalogue — not when a page is opened, so
// the host learns nothing about who browses what or when — through the same
// guarded client as the catalogue, and only from the catalogue's own host: a
// catalogue cannot use its logos to point the panel, or anybody's browser, at a
// third party. What comes back is kept only if its bytes are a picture, whatever
// it said it was, and is served from the panel with the same sandbox as the
// built-in logos.

// Sniff says what kind of picture a logo is, from its bytes.
//
// A server's Content-Type is not trusted — raw.githubusercontent.com calls
// every SVG text/plain, and anything else could call an HTML page image/png —
// so the answer comes from what is there, and anything that is not one of
// these three is not a logo.
func Sniff(body []byte) (string, bool) {
	switch {
	case len(body) == 0:
		return "", false
	case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case len(body) >= 12 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP")):
		return "image/webp", true
	case isSVG(body):
		return "image/svg+xml", true
	}
	return "", false
}

// isSVG says whether a document is an SVG: text whose first element is <svg,
// after the declaration, comments and a doctype that may come before it.
func isSVG(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	for {
		text = strings.TrimLeft(text, " \t\r\n")
		switch {
		case strings.HasPrefix(text, "<?"):
			end := strings.Index(text, "?>")
			if end < 0 {
				return false
			}
			text = text[end+2:]
		case strings.HasPrefix(text, "<!--"):
			end := strings.Index(text, "-->")
			if end < 0 {
				return false
			}
			text = text[end+3:]
		case strings.HasPrefix(text, "<!DOCTYPE") || strings.HasPrefix(text, "<!doctype"):
			end := strings.Index(text, ">")
			if end < 0 {
				return false
			}
			text = text[end+1:]
		default:
			return (strings.HasPrefix(text, "<svg ") || strings.HasPrefix(text, "<svg>") ||
				strings.HasPrefix(text, "<svg\n") || strings.HasPrefix(text, "<svg\t") ||
				strings.HasPrefix(text, "<svg\r")) && strings.Contains(text, "</svg>")
		}
	}
}

// ResolveIconURL turns a template's `icon:` into the address the panel may
// fetch it from, or says why it will not.
//
// Relative to the catalogue, or absolute and on the catalogue's own host and
// port, over https. Anywhere else is refused: the host a team chose to trust
// with its catalogue is the only one the panel fetches for it.
func ResolveIconURL(catalogue *url.URL, ref string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return nil, fmt.Errorf("the icon %q is not an address", ref)
	}
	resolved := catalogue.ResolveReference(parsed)
	switch {
	case resolved.Scheme != "https":
		return nil, fmt.Errorf("the icon %q is not https", ref)
	case !SameHost(resolved, catalogue):
		return nil, fmt.Errorf("the icon %q is on %s, and a catalogue's icons come from its own host, %s",
			ref, resolved.Host, catalogue.Host)
	case resolved.User != nil:
		return nil, fmt.Errorf("the icon %q carries a username or a password", ref)
	}
	resolved.Fragment = ""
	return resolved, nil
}

// SameHost says whether two https addresses are the same server: the same
// name, whatever its case, and the same port, 443 when none is written.
func SameHost(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		return "443"
	}
	return strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
