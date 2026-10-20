package kube

import (
	"regexp"
	"strings"
	"testing"
)

func redirectSpec() AppSpec {
	return AppSpec{
		Name: "shop", Namespace: "acme-shop-prod", AppID: "app_1", Port: 3000,
		ClusterIssuer: "letsencrypt",
		Domains: []DomainSpec{
			{Hostname: "example.com", Path: "/", TLS: true},
			{Hostname: "www.example.com", Path: "/", TLS: true, RedirectTo: "example.com"},
			{Hostname: "old.example.net", Path: "/", TLS: true, RedirectTo: "example.com"},
		},
	}
}

// Each redirect answers for its own hostname and nothing else: a middleware
// applies to the whole Ingress, so a regex that matched the target too would
// send example.com to example.com forever.
func TestARedirectMatchesOnlyItsOwnHostname(t *testing.T) {
	redirects := BuildHostRedirects(redirectSpec())
	if len(redirects) != 2 {
		t.Fatalf("got %d redirects, want 2", len(redirects))
	}
	www := redirects[0].Object["spec"].(map[string]any)["redirectRegex"].(map[string]any)
	pattern := regexp.MustCompile(www["regex"].(string))
	replacement := www["replacement"].(string)

	for url, want := range map[string]string{
		"https://www.example.com/cart?id=7": "https://example.com/cart?id=7",
		"http://www.example.com":            "https://example.com",
		"http://www.example.com:80/a/b":     "https://example.com/a/b",
	} {
		if !pattern.MatchString(url) {
			t.Errorf("%s is not redirected", url)
			continue
		}
		if got := pattern.ReplaceAllString(url, replacement); got != want {
			t.Errorf("%s goes to %s, want %s", url, got, want)
		}
	}
	for _, url := range []string{
		"https://example.com/", "https://wwwxexample.com/", "https://www.example.com.evil.test/",
		"https://old.example.net/",
	} {
		if pattern.MatchString(url) {
			t.Errorf("the www redirect also catches %s", url)
		}
	}
	if www["permanent"] != true {
		t.Error("the redirect is not permanent, so search engines keep both names")
	}
}

// The redirect runs before the one to HTTPS, so http://www is one hop from
// https://example.com, and after the firewall, which answers first.
func TestTheIngressRunsRedirectsAfterTheFirewallAndBeforeHTTPS(t *testing.T) {
	spec := redirectSpec()
	spec.Protected = true
	ingress := BuildIngress(spec)
	chain := strings.Split(ingress.Annotations["traefik.ingress.kubernetes.io/router.middlewares"], ",")
	position := map[string]int{}
	for i, name := range chain {
		position[name] = i
	}
	guard := position[spec.Namespace+"-"+GuardMiddleware+"@kubernetescrd"]
	https := position[spec.Namespace+"-"+HTTPSRedirectMiddlewareName("shop")+"@kubernetescrd"]
	www, ok := position[spec.Namespace+"-"+HostRedirectMiddlewareName("shop", "www.example.com")+"@kubernetescrd"]
	if !ok || !(guard < www && www < https) {
		t.Errorf("the middlewares run in the order %v", chain)
	}
	// The redirecting hostnames are routed and on the certificate.
	hosts := strings.Join(ingress.Spec.TLS[0].Hosts, ",")
	if !strings.Contains(hosts, "www.example.com") || !strings.Contains(hosts, "old.example.net") {
		t.Errorf("the certificate covers %s", hosts)
	}
}

// A redirect to a hostname the app does not have, or to one that redirects
// itself, is not rendered: better to serve on both names than to loop.
func TestABrokenRedirectIsNotRendered(t *testing.T) {
	spec := redirectSpec()
	spec.Domains = append(spec.Domains,
		DomainSpec{Hostname: "a.example.com", RedirectTo: "gone.example.com"},
		DomainSpec{Hostname: "b.example.com", RedirectTo: "www.example.com"},
	)
	for _, r := range BuildHostRedirects(spec) {
		name := r.GetName()
		if name == HostRedirectMiddlewareName("shop", "a.example.com") || name == HostRedirectMiddlewareName("shop", "b.example.com") {
			t.Errorf("%s was rendered", name)
		}
		if !IsHostRedirectOf(*r, "shop") || IsHostRedirectOf(*r, "shop-api") {
			t.Errorf("%s cannot be told apart as the app's redirect", name)
		}
	}
}

// Plain HTTP goes to HTTPS on the hostnames that have a certificate, and only
// on those. One middleware for the whole namespace sent every hostname of the
// Ingress, so the free sslip.io address, plain HTTP on purpose, began sending
// visitors to an HTTPS address with no certificate the moment the app was
// given a domain of its own.
func TestOnlyTheHostnamesWithACertificateGoToHTTPS(t *testing.T) {
	spec := AppSpec{
		Name: "shop", Namespace: "acme-shop-prod", AppID: "app_1", Port: 3000,
		ClusterIssuer: "letsencrypt",
		Domains: []DomainSpec{
			{Hostname: "shop-prod.203-0-113-10.sslip.io", Path: "/"},
			{Hostname: "shop.example.com", Path: "/", TLS: true},
			{Hostname: "own.example.net", Path: "/", TLS: true, Certificate: "crt_1"},
		},
	}
	middleware := BuildHTTPSRedirect(spec)
	if middleware == nil {
		t.Fatal("no redirect to HTTPS for an app with hostnames that have a certificate")
	}
	if !IsHostRedirectOf(*middleware, "shop") {
		t.Error("the redirect cannot be told apart as the app's, so it is never removed")
	}
	redirect := middleware.Object["spec"].(map[string]any)["redirectRegex"].(map[string]any)
	pattern := regexp.MustCompile(redirect["regex"].(string))
	replacement := redirect["replacement"].(string)

	for url, want := range map[string]string{
		"http://shop.example.com/cart?id=7": "https://shop.example.com/cart?id=7",
		"http://shop.example.com":           "https://shop.example.com",
		"http://shop.example.com:80/a":      "https://shop.example.com/a",
		"http://own.example.net/":           "https://own.example.net/",
	} {
		if !pattern.MatchString(url) {
			t.Errorf("%s is not sent to HTTPS", url)
			continue
		}
		if got := pattern.ReplaceAllString(url, replacement); got != want {
			t.Errorf("%s goes to %s, want %s", url, got, want)
		}
	}
	for _, url := range []string{
		"http://shop-prod.203-0-113-10.sslip.io/",
		"https://shop.example.com/",
		"http://shop.example.com.evil.test/",
		"http://xshop.example.com/",
	} {
		if pattern.MatchString(url) {
			t.Errorf("%s is sent to HTTPS", url)
		}
	}
	if redirect["permanent"] != true {
		t.Error("the redirect to HTTPS is not permanent")
	}

	// With no certificate coming for any hostname there is nothing to
	// redirect to, and the Ingress names no redirect.
	spec.ClusterIssuer = ""
	spec.Domains = spec.Domains[:2]
	if BuildHTTPSRedirect(spec) != nil {
		t.Error("a redirect to HTTPS was rendered with no certificate to serve it")
	}
	if got := BuildIngress(spec).Annotations["traefik.ingress.kubernetes.io/router.middlewares"]; got != "" {
		t.Errorf("an app with no certificate names %q", got)
	}
}
