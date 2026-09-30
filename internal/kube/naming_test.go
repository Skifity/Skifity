package kube

import (
	"strings"
	"testing"
)

func TestSlugifyProducesValidNames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"web", "web"},
		{"My App", "my-app"},
		{"Tegar's Shop!", "tegar-s-shop"},
		{"  spaced  out  ", "spaced-out"},
		{"UPPER_CASE_NAME", "upper-case-name"},
		{"api.v2", "api-v2"},
		{"--leading-and-trailing--", "leading-and-trailing"},
		{"a/b/c", "a-b-c"},
	}
	for _, tc := range cases {
		got := Slugify(tc.in)
		if got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !ValidLabel(got) {
			t.Errorf("Slugify(%q) = %q, which Kubernetes would reject", tc.in, got)
		}
	}
}

func TestSlugifyHandlesNonLatinNames(t *testing.T) {
	// Five languages ship in the UI, so project names will not be ASCII.
	for _, name := range []string{"магазин", "दुकान", "商店", "トコ", "!!!", "…"} {
		got := Slugify(name)
		if !ValidLabel(got) {
			t.Fatalf("Slugify(%q) = %q, which Kubernetes would reject", name, got)
		}
		if got != Slugify(name) {
			t.Fatalf("Slugify(%q) is not deterministic", name)
		}
	}
	// Different non-Latin names must not collapse onto the same slug.
	if Slugify("магазин") == Slugify("商店") {
		t.Fatal("two different names produced the same slug")
	}
}

func TestSlugifyTruncatesLongNamesUniquely(t *testing.T) {
	long := strings.Repeat("very-long-project-name-", 10)
	a := Slugify(long + "a")
	b := Slugify(long + "b")
	if len(a) > 63 {
		t.Fatalf("slug is %d characters, Kubernetes allows 63", len(a))
	}
	if !ValidLabel(a) {
		t.Fatalf("truncated slug %q is not a valid label", a)
	}
	if a == b {
		t.Fatal("two long names truncated to the same slug, which would collide in the cluster")
	}
}

func TestNamespaceFor(t *testing.T) {
	got := NamespaceFor("acme", "shop", "production")
	if got != "acme-shop-production" {
		t.Fatalf("NamespaceFor = %q, want acme-shop-production", got)
	}
	if !ValidLabel(got) {
		t.Fatalf("namespace %q is not valid", got)
	}

	long := NamespaceFor(strings.Repeat("team", 10), strings.Repeat("project", 10), "staging")
	if len(long) > 63 || !ValidLabel(long) {
		t.Fatalf("long namespace %q (%d chars) is not valid", long, len(long))
	}
	if !strings.Contains(long, "staging") {
		t.Fatalf("namespace %q lost the environment name, which is what tells two namespaces apart", long)
	}
	// Two environments of the same long project must not share a namespace.
	other := NamespaceFor(strings.Repeat("team", 10), strings.Repeat("project", 10), "production")
	if long == other {
		t.Fatal("two environments produced the same namespace")
	}
}

func TestResourceName(t *testing.T) {
	if got := ResourceName("web", "tls"); got != "web-tls" {
		t.Fatalf("ResourceName = %q, want web-tls", got)
	}
	if got := ResourceName("web", ""); got != "web" {
		t.Fatalf("ResourceName with no suffix = %q, want web", got)
	}
	long := ResourceName(strings.Repeat("a", 60), "hpa")
	if len(long) > 63 || !ValidLabel(long) {
		t.Fatalf("ResourceName produced %q (%d chars)", long, len(long))
	}
	if !strings.HasSuffix(long, "-hpa") {
		t.Fatalf("ResourceName dropped the suffix: %q", long)
	}
}

func TestAutoHostname(t *testing.T) {
	// With a wildcard domain, production gets the bare app name.
	if got := AutoHostname("web", "production", "apps.example.com", ""); got != "web.apps.example.com" {
		t.Fatalf("AutoHostname = %q, want web.apps.example.com", got)
	}
	// Other environments are distinguished.
	if got := AutoHostname("web", "staging", "apps.example.com", ""); got != "web-staging.apps.example.com" {
		t.Fatalf("AutoHostname = %q, want web-staging.apps.example.com", got)
	}
	// A wildcard written with the asterisk still works.
	if got := AutoHostname("web", "production", "*.apps.example.com", ""); got != "web.apps.example.com" {
		t.Fatalf("AutoHostname with a *. prefix = %q", got)
	}
	// With no domain at all, a fresh install still gets a working URL.
	got := AutoHostname("web", "production", "", "203.0.113.10")
	if got != "web.203-0-113-10.sslip.io" {
		t.Fatalf("AutoHostname fallback = %q, want web.203-0-113-10.sslip.io", got)
	}
	if !ValidHostname(got) {
		t.Fatalf("fallback hostname %q is not a valid hostname", got)
	}
	// With neither, there is nothing to offer.
	if got := AutoHostname("web", "production", "", ""); got != "" {
		t.Fatalf("AutoHostname with no domain and no IP = %q, want empty", got)
	}
}

func TestValidHostname(t *testing.T) {
	for _, good := range []string{"example.com", "app.example.com", "a-b.example.co.uk", "web.203-0-113-10.sslip.io"} {
		if !ValidHostname(good) {
			t.Errorf("ValidHostname(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "localhost", "-example.com", "example-.com", "exa mple.com", "example..com", strings.Repeat("a", 70) + ".com"} {
		if ValidHostname(bad) {
			t.Errorf("ValidHostname(%q) = true", bad)
		}
	}
	// A trailing dot is how DNS spells a fully qualified name, and people paste it.
	if !ValidHostname("example.com.") {
		t.Error("a trailing dot was rejected")
	}
}

func TestSanitiseEnvKey(t *testing.T) {
	for _, good := range []string{"PORT", "DATABASE_URL", "_private", "a1"} {
		if _, err := SanitiseEnvKey(good); err != nil {
			t.Errorf("SanitiseEnvKey(%q): %v", good, err)
		}
	}
	for _, bad := range []string{"", "MY-VAR", "1START", "with space", "ПЕРЕМЕННАЯ", strings.Repeat("A", 300)} {
		if _, err := SanitiseEnvKey(bad); err == nil {
			t.Errorf("SanitiseEnvKey(%q) was accepted, but a shell cannot export it", bad)
		}
	}
}
