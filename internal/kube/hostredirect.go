package kube

import (
	"regexp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/version"
)

// One hostname sending its visitors to another of the same app: www to the
// bare domain, the bare domain to www, or an old name to the new one.
//
// Traefik's redirectRegex middleware does it, one per hostname that
// redirects. A middleware applies to the whole Ingress, not to one host, so
// each regex is anchored to its own hostname: on the app's other hostnames it
// matches nothing and does nothing. That is what lets the redirecting host
// share the app's Ingress and its certificate rather than needing an Ingress
// and a certificate of its own.
//
// The redirect is permanent (308, which keeps the method and body), and it is
// straight to the target's scheme: a visitor to http://www.example.com lands
// on https://example.com in one hop, not two.

// hostRedirectLabel marks a Middleware as one of an app's redirects, so the
// ones nobody wants any more can be found and removed.
var hostRedirectLabel = version.LabelKey("redirect-of")

// HostRedirectMiddlewareName is the Middleware that redirects one hostname.
func HostRedirectMiddlewareName(app, hostname string) string {
	return ResourceName(app, "redirect-"+shortHash(hostname, 8))
}

// hostRedirects are the app's domains that redirect, each with where to.
func hostRedirects(s AppSpec) []DomainSpec {
	targets := map[string]DomainSpec{}
	for _, d := range s.Domains {
		targets[d.Hostname] = d
	}
	var out []DomainSpec
	for _, d := range s.Domains {
		target, ok := targets[d.RedirectTo]
		// A redirect to a hostname the app does not have, or to one that
		// redirects itself, is not rendered: a loop or a dead end is worse
		// than serving the app on both names.
		if d.RedirectTo == "" || !ok || target.RedirectTo != "" || d.RedirectTo == d.Hostname {
			continue
		}
		out = append(out, d)
	}
	return out
}

// RedirectTarget is the URL a redirecting hostname sends its visitors to.
func RedirectTarget(s AppSpec, d DomainSpec) string {
	for _, target := range s.Domains {
		if target.Hostname == d.RedirectTo {
			scheme := "http"
			if target.TLS {
				scheme = "https"
			}
			return scheme + "://" + target.Hostname
		}
	}
	return ""
}

// BuildHostRedirects renders the app's redirect middlewares, one per
// hostname that redirects. None when nothing does.
func BuildHostRedirects(s AppSpec) []*unstructured.Unstructured {
	var out []*unstructured.Unstructured
	for _, d := range hostRedirects(s) {
		labels := map[string]any{hostRedirectLabel: s.Name}
		for key, value := range s.Labels() {
			labels[key] = value
		}
		out = append(out, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]any{
				"name":      HostRedirectMiddlewareName(s.Name, d.Hostname),
				"namespace": s.Namespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"redirectRegex": map[string]any{
					// The whole URL, on this hostname only, any port; the
					// path and query go along.
					"regex":       `^https?://` + regexp.QuoteMeta(d.Hostname) + `(?::[0-9]+)?(/.*)?$`,
					"replacement": RedirectTarget(s, d) + "${1}",
					"permanent":   true,
				},
			},
		}})
	}
	return out
}

// IsHostRedirectOf reports whether a Middleware is one of an app's
// redirects, for removing the ones its domains no longer ask for.
func IsHostRedirectOf(object unstructured.Unstructured, app string) bool {
	return object.GetLabels()[hostRedirectLabel] == app
}
