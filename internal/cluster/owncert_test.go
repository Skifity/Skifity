package cluster

import (
	"strings"
	"testing"
	"time"

	"skifity/internal/kube"
	"skifity/internal/store"
)

// The spec says which hostnames are on the team's own certificates, chosen
// the way tlscert.Best chooses, and the objects rendered from it put those in
// the second Ingress — which is what the Advanced view shows.
func TestTheSpecPutsHostnamesOnTheTeamsOwnCertificates(t *testing.T) {
	c, db, app, env, teamID := autoDomainFixture(t)
	ctx := t.Context()
	for _, domain := range []store.Domain{
		{AppID: app.ID, Hostname: "shop.example.com", Path: "/", TLS: true},
		{AppID: app.ID, Hostname: "example.com", Path: "/", TLS: true},
		{AppID: app.ID, Hostname: "plain.example.com", Path: "/", TLS: false},
		{AppID: app.ID, Hostname: "shop.example.org", Path: "/", TLS: true},
	} {
		if err := db.CreateDomain(ctx, &domain); err != nil {
			t.Fatal(err)
		}
	}
	save := func(name string, hostnames []string, lasts time.Duration) store.Certificate {
		certificate := store.Certificate{
			ID: store.NewID("crt"), TeamID: teamID, Name: name, Hostnames: hostnames,
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(lasts),
			ChainPEM: "-----BEGIN CERTIFICATE-----\n" + name + "\n",
		}
		if _, err := db.SaveCertificate(ctx, &certificate, "SKF1.sealed-"+name); err != nil {
			t.Fatal(err)
		}
		return certificate
	}
	short := save("short", []string{"*.example.com"}, 20*24*time.Hour)
	long := save("long", []string{"*.example.com"}, 300*24*time.Hour)

	spec, err := c.SpecFor(ctx, app, env, "registry.example.test/web:1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, domain := range spec.Domains {
		got[domain.Hostname] = domain.Certificate
	}
	for hostname, want := range map[string]string{
		// Of two certificates that cover it, the one that lasts longer.
		"shop.example.com": long.ID,
		// A wildcard does not cover its apex, and plain HTTP has no
		// certificate at all.
		"example.com":       "",
		"plain.example.com": "",
		"shop.example.org":  "",
	} {
		if got[hostname] != want {
			t.Errorf("%s is served with %q, want %q", hostname, got[hostname], want)
		}
	}
	if got["shop.example.com"] == short.ID {
		t.Error("the certificate that runs out first was chosen")
	}

	manifests, err := c.Manifests(ctx, app, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifests, "name: "+kube.OwnCertIngressName("web")) ||
		!strings.Contains(manifests, "# Secret/"+kube.CertificateSecretName("web", long.ID)) {
		t.Errorf("the Advanced view does not show the second Ingress and its Secret:\n%s", manifests)
	}
	if strings.Contains(manifests, "SKF1.sealed") {
		t.Error("the Advanced view shows the sealed key")
	}

	// An internal app serves none of its domains, with any certificate.
	if err := db.UpdateApp(ctx, withInternal(app)); err != nil {
		t.Fatal(err)
	}
	internal, err := db.GetApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = c.SpecFor(ctx, internal, env, "registry.example.test/web:1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Domains) != 0 || kube.BuildOwnCertIngress(spec) != nil {
		t.Errorf("an internal app has %d domains", len(spec.Domains))
	}
}

func withInternal(app store.App) *store.App {
	app.Internal = true
	return &app
}
