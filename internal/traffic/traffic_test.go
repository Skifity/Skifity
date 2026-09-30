package traffic

import (
	"strconv"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	"skifity/internal/kube"
)

// traefikNameFor is the rule Traefik's Kubernetes Ingress provider applies to
// an Ingress backend, written out again from the rendered object rather than
// from what ServiceNames assumes about it (traefik v3.6,
// pkg/provider/kubernetes/ingress/kubernetes.go: the port's name when it has
// one, its number otherwise, the whole normalised, then the provider).
func traefikNameFor(namespace string, backend *networkingv1.IngressServiceBackend) string {
	port := backend.Port.Name
	if port == "" {
		port = strconv.Itoa(int(backend.Port.Number))
	}
	return normalize(namespace+"-"+backend.Name+"-"+port) + "@kubernetes"
}

// The name an app's requests are counted under is the one Traefik derives from
// the Ingress the panel actually renders — for an ordinary app, one behind the
// firewall, and one that scales to zero through KEDA's interceptor — so the
// day BuildIngress points somewhere else, this fails rather than every app's
// chart going quietly flat.
func TestAnAppIsFoundUnderTheNameTraefikGivesItsIngress(t *testing.T) {
	long := strings.Repeat("a", 60)
	cases := []struct {
		name    string
		spec    kube.AppSpec
		through string
	}{
		{"an ordinary app", kube.AppSpec{}, "acme-shop-production-web-80@kubernetes"},
		{"behind the firewall", kube.AppSpec{Protected: true}, "acme-shop-production-web-80@kubernetes"},
		{"with a password too", kube.AppSpec{Protected: true, PasswordUsers: "x:y"}, "acme-shop-production-web-80@kubernetes"},
		{"scaling to zero", kube.AppSpec{ScaleToZero: true}, "acme-shop-production-web-wake-8080@kubernetes"},
		{"scaling to zero behind the firewall", kube.AppSpec{ScaleToZero: true, Protected: true},
			"acme-shop-production-web-wake-8080@kubernetes"},
		// A slug long enough that the wake Service's name is shortened with a
		// hash: whatever ResourceName makes, the index makes too.
		{"with a long name, scaling to zero", kube.AppSpec{Name: long, ScaleToZero: true}, ""},
	}
	for _, tc := range cases {
		spec := tc.spec
		if spec.Name == "" {
			spec.Name = "web"
		}
		spec.Namespace = "acme-shop-production"
		spec.Port = 3000
		spec.Domains = []kube.DomainSpec{{Hostname: "shop.example.com", TLS: true}}
		spec.ClusterIssuer = "letsencrypt"

		ingress := kube.BuildIngress(spec)
		if ingress == nil {
			t.Fatalf("%s: no Ingress", tc.name)
		}
		if spec.Protected && !strings.Contains(ingress.Annotations["traefik.ingress.kubernetes.io/router.middlewares"],
			kube.GuardMiddleware) {
			t.Fatalf("%s: the firewall is not on the router, so this case tests nothing", tc.name)
		}
		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				label := traefikNameFor(ingress.Namespace, path.Backend.Service)
				if tc.through != "" && label != tc.through {
					t.Errorf("%s: Traefik calls the backend %s, want %s", tc.name, label, tc.through)
				}
				index, _ := NewIndex([]App{{ID: "app_1", Namespace: spec.Namespace, Slug: spec.Name}})
				if id, ok := index.AppFor(label); !ok || id != "app_1" {
					t.Errorf("%s: %s is not found as the app; it knows %v", tc.name, label,
						ServiceNames(spec.Namespace, spec.Name))
				}
			}
		}
	}
}

// Traefik folds what is not a letter or a digit into one dash, and so must the
// name looked for.
func TestTheNameIsNormalisedTheWayTraefikDoes(t *testing.T) {
	names := ServiceNames("team--x-prod", "api")
	if names[0] != "team-x-prod-api-80@kubernetes" || names[1] != "team-x-prod-api-wake-8080@kubernetes" {
		t.Fatalf("names are %v", names)
	}
}

// Two apps Traefik cannot tell apart are counted for neither: one tenant's
// requests on another tenant's page is worse than no numbers.
func TestTwoAppsWithOneNameAreCountedForNeither(t *testing.T) {
	index, shared := NewIndex([]App{
		{ID: "app_a", Namespace: "acme-shop", Slug: "prod-web"},
		{ID: "app_b", Namespace: "acme", Slug: "shop-prod-web"},
		{ID: "app_c", Namespace: "other-team-prod", Slug: "web"},
	})
	if !shared["app_a"] || !shared["app_b"] || shared["app_c"] {
		t.Fatalf("shared is %v", shared)
	}
	for _, name := range []string{"acme-shop-prod-web-80@kubernetes", "acme-shop-prod-web-wake-8080@kubernetes"} {
		if id, ok := index.AppFor(name); ok {
			t.Errorf("%s was given to %s", name, id)
		}
	}
	if id, ok := index.AppFor("other-team-prod-web-80@kubernetes"); !ok || id != "app_c" {
		t.Fatalf("the app nobody shares with is %q", id)
	}
	// Somebody else's service — the panel's own Ingress, an IngressRoute —
	// belongs to no app.
	if _, ok := index.AppFor("whoami1@docker"); ok {
		t.Fatal("a service that is no app's was found")
	}
}
