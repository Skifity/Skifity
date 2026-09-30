package kube

import (
	"strings"
	"testing"
)

// A password in front of an app: Traefik's basicAuth, reading one account from
// a Secret in the app's own namespace.

func passwordSpec() AppSpec {
	spec := baseSpec()
	spec.ClusterIssuer = "skifity"
	spec.Domains = []DomainSpec{{Hostname: "staging.example.com", Path: "/", TLS: true}}
	spec.PasswordUsers = "client:$2y$06$abcdefghijklmnopqrstuuv0123456789abcdefghijklmnopqrs"
	return spec
}

// After the firewall, and after the redirect to HTTPS. A browser answers a
// password prompt with the password, so asking before the redirect would send
// it over plain HTTP one request before it needed to.
func TestThePasswordIsAskedForAfterTheRedirectToHTTPS(t *testing.T) {
	spec := passwordSpec()
	spec.Protected = true
	got := BuildIngress(spec).Annotations["traefik.ingress.kubernetes.io/router.middlewares"]
	want := "acme-shop-production-firewall@kubernetescrd," +
		"acme-shop-production-redirect-https@kubernetescrd," +
		"acme-shop-production-web-password@kubernetescrd"
	if got != want {
		t.Errorf("middlewares = %q\nwant        %q", got, want)
	}

	spec.PasswordUsers = ""
	got = BuildIngress(spec).Annotations["traefik.ingress.kubernetes.io/router.middlewares"]
	if strings.Contains(got, "password") {
		t.Errorf("an app with no password names the middleware: %q", got)
	}
}

// The middleware the Ingress names is the one that is rendered, in the same
// namespace. A name that does not match is a route Traefik refuses to serve.
func TestTheIngressNamesThePasswordMiddlewareThatExists(t *testing.T) {
	spec := passwordSpec()
	middleware := BuildPasswordMiddleware(spec)
	if middleware == nil {
		t.Fatal("no middleware was rendered for an app with a password")
	}
	annotation := BuildIngress(spec).Annotations["traefik.ingress.kubernetes.io/router.middlewares"]
	reference := middleware.GetNamespace() + "-" + middleware.GetName() + "@kubernetescrd"
	if !strings.Contains(annotation, reference) {
		t.Errorf("the Ingress names %q and the middleware is %q", annotation, reference)
	}
	if middleware.GetAPIVersion() != "traefik.io/v1alpha1" || middleware.GetKind() != "Middleware" {
		t.Errorf("the middleware is a %s %s", middleware.GetAPIVersion(), middleware.GetKind())
	}
}

func TestThePasswordMiddlewareReadsItsSecretAndHidesTheHeader(t *testing.T) {
	spec := passwordSpec()
	middleware := BuildPasswordMiddleware(spec)
	secret := BuildPasswordSecret(spec)
	if secret == nil {
		t.Fatal("no secret was rendered for an app with a password")
	}

	basicAuth, _ := middleware.Object["spec"].(map[string]any)["basicAuth"].(map[string]any)
	if basicAuth["secret"] != secret.Name {
		t.Errorf("the middleware reads %v, the secret is %s", basicAuth["secret"], secret.Name)
	}
	if secret.Namespace != middleware.GetNamespace() {
		t.Errorf("the secret is in %s and the middleware in %s", secret.Namespace, middleware.GetNamespace())
	}
	// The app did not ask for the password; an app that logs its headers
	// would otherwise write it into every line.
	if basicAuth["removeHeader"] != true {
		t.Error("the Authorization header is passed on to the app")
	}
	if secret.StringData["users"] != spec.PasswordUsers {
		t.Errorf("the secret holds %q", secret.StringData["users"])
	}
}

func TestNoPasswordMeansNoPasswordObjects(t *testing.T) {
	spec := baseSpec()
	if BuildPasswordSecret(spec) != nil || BuildPasswordMiddleware(spec) != nil {
		t.Fatal("an app with no password got password objects")
	}
}

// Two apps in one environment can have different passwords, so the objects are
// named after the app, not the namespace.
func TestEachAppHasItsOwnPasswordObjects(t *testing.T) {
	web, api := passwordSpec(), passwordSpec()
	api.Name = "api"
	if BuildPasswordMiddleware(web).GetName() == BuildPasswordMiddleware(api).GetName() {
		t.Error("two apps share one password middleware")
	}
	if BuildPasswordSecret(web).Name == BuildPasswordSecret(api).Name {
		t.Error("two apps share one password secret")
	}
}
