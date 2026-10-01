package dnsprov

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Each provider, against a server that speaks its API's shapes — the paths,
// the pagination, the envelopes, the way a name is written inside a zone —
// as the provider's own documentation gives them (cited at the top of each
// provider's file). The same conversation is held with all four: list the
// zones, look at an empty name, create a record, find it, point it elsewhere,
// delete it, delete it again. A provider that only half-speaks its API fails
// one of those.

// fakeAPI is what a test needs from a fake provider.
type fakeAPI interface {
	URL() string
	// Records is everything at a name, as the fake holds it.
	Count() int
}

func conform(t *testing.T, kind string, creds Credentials, fake fakeAPI, wantZones []string) {
	t.Helper()
	ctx := t.Context()
	provider, err := open(kind, creds, fake.URL(), &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	zones, err := provider.Zones(ctx)
	if err != nil {
		t.Fatalf("list zones: %v", err)
	}
	var names []string
	for _, z := range zones {
		names = append(names, z.Name)
	}
	if strings.Join(names, ",") != strings.Join(wantZones, ",") {
		t.Fatalf("zones are %v, want %v", names, wantZones)
	}
	zone := zones[MatchZone("blog.example.com", names)]

	before, err := provider.Records(ctx, zone, "blog.example.com")
	if err != nil {
		t.Fatalf("list an empty name: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("an empty name has %v", before)
	}
	// A record at another name must not be answered as one at this one.
	others, err := provider.Records(ctx, zone, "www.example.com")
	if err != nil || len(others) != 1 {
		t.Fatalf("the record already at www is %v (%v)", others, err)
	}

	made, err := provider.Create(ctx, zone, Record{Type: "A", Name: "blog.example.com", Content: "203.0.113.10", Note: marker})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if made.ID == "" || made.Content != "203.0.113.10" || made.Name != "blog.example.com" {
		t.Fatalf("created %+v", made)
	}
	found, err := provider.Records(ctx, zone, "blog.example.com")
	if err != nil || len(found) != 1 || found[0].ID != made.ID || found[0].Content != "203.0.113.10" {
		t.Fatalf("after creating, the name has %+v (%v)", found, err)
	}
	if KeepsNotes(kind) && found[0].Note != marker {
		t.Errorf("the record's note is %q, want the panel's", found[0].Note)
	}
	if !KeepsNotes(kind) && found[0].Note != "" {
		t.Errorf("a provider without notes answered one: %q", found[0].Note)
	}

	moved, err := provider.SetContent(ctx, zone, found[0], "198.51.100.20")
	if err != nil || moved.Content != "198.51.100.20" {
		t.Fatalf("set content: %+v (%v)", moved, err)
	}
	found, _ = provider.Records(ctx, zone, "blog.example.com")
	if len(found) != 1 || found[0].Content != "198.51.100.20" {
		t.Fatalf("after moving, the name has %+v", found)
	}
	if KeepsNotes(kind) && found[0].Note != marker {
		t.Errorf("moving the record lost its note: %q", found[0].Note)
	}

	// A CNAME, whose value each provider spells its own way.
	cname, err := provider.Create(ctx, zone, Record{Type: "CNAME", Name: "shop.example.com", Content: "lb.example.net", Note: marker})
	if err != nil || cname.Content != "lb.example.net" {
		t.Fatalf("create a CNAME: %+v (%v)", cname, err)
	}
	found, _ = provider.Records(ctx, zone, "shop.example.com")
	if len(found) != 1 || found[0].Type != "CNAME" || found[0].Content != "lb.example.net" {
		t.Fatalf("the CNAME reads back as %+v", found)
	}

	count := fake.Count()
	if err := provider.Delete(ctx, zone, moved); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if fake.Count() != count-1 {
		t.Errorf("deleting left %d records, want %d", fake.Count(), count-1)
	}
	if err := provider.Delete(ctx, zone, moved); !errors.Is(err, ErrGone) {
		t.Errorf("deleting a record that is gone answered %v, want ErrGone", err)
	}

	wrong := creds
	wrong.Token, wrong.SecretAccessKey = "not-the-token-0000000000000000000000000", "not-the-secret"
	refused, _ := open(kind, wrong, fake.URL(), &http.Client{Timeout: 5 * time.Second})
	if _, err := refused.Zones(ctx); !IsRefused(err) {
		t.Errorf("a wrong credential answered %v, want a refusal", err)
	}
}

func TestCloudflare(t *testing.T) {
	fake := newFakeCloudflare(t)
	conform(t, Cloudflare, Credentials{Token: cfToken}, fake, []string{"example.com", "example.org"})
	if !fake.sawExactFilter {
		t.Error("records were listed without asking for the exact name")
	}
	if fake.proxied {
		t.Error("a record was created behind Cloudflare's proxy; HTTP-01 needs the origin")
	}
}

func TestHetzner(t *testing.T) {
	fake := newFakeHetzner(t)
	conform(t, Hetzner, Credentials{Token: hzToken}, fake, []string{"example.com", "example.net"})
	if !fake.labelled {
		t.Error("a record set was created without the managed-by label")
	}
}

func TestDigitalOcean(t *testing.T) {
	fake := newFakeDigitalOcean(t)
	conform(t, DigitalOcean, Credentials{Token: doToken}, fake, []string{"example.com", "example.io"})
}

func TestRoute53(t *testing.T) {
	fake := newFakeRoute53(t)
	conform(t, Route53, Credentials{AccessKeyID: r53Key, SecretAccessKey: r53Secret}, fake, []string{"example.com", "example.dev"})
	if fake.signed == 0 {
		t.Error("no request was signed")
	}
}

// Route 53 refuses to delete a set that is not as the panel left it, and
// the panel reads that as a change rather than a failure.
func TestRoute53RefusesAChangedSet(t *testing.T) {
	fake := newFakeRoute53(t)
	provider, _ := open(Route53, Credentials{AccessKeyID: r53Key, SecretAccessKey: r53Secret}, fake.URL(), &http.Client{Timeout: 5 * time.Second})
	zone := Zone{ID: "Z1EXAMPLE", Name: "example.com"}
	made, err := provider.Create(t.Context(), zone, Record{Type: "A", Name: "blog.example.com", Content: "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	fake.edit("blog.example.com.", "A", "198.51.100.7")
	if err := provider.Delete(t.Context(), zone, made); !errors.Is(err, ErrChanged) {
		t.Errorf("deleting a set somebody edited answered %v, want ErrChanged", err)
	}
	if _, err := provider.SetContent(t.Context(), zone, made, "198.51.100.20"); !errors.Is(err, ErrChanged) {
		t.Errorf("moving a set somebody edited answered %v, want ErrChanged", err)
	}
}

// Every provider goes through the guard outside tests, and the guard
// refuses the machine the panel runs on — which is where a test's fake is.
// So an address somebody managed to put in front of a provider could not
// reach the panel's own network either: the base address is not settable
// from the API at all, and this is the second lock.
func TestEveryProviderGoesThroughTheGuard(t *testing.T) {
	fakes := map[string]fakeAPI{
		Cloudflare:   newFakeCloudflare(t),
		Hetzner:      newFakeHetzner(t),
		DigitalOcean: newFakeDigitalOcean(t),
		Route53:      newFakeRoute53(t),
	}
	creds := Credentials{Token: cfToken, AccessKeyID: r53Key, SecretAccessKey: r53Secret}
	for kind, fake := range fakes {
		creds := creds
		switch kind {
		case Hetzner:
			creds.Token = hzToken
		case DigitalOcean:
			creds.Token = doToken
		}
		provider, err := open(kind, creds, fake.URL(), guarded)
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.Zones(t.Context())
		if err == nil || !strings.Contains(err.Error(), "refusing to connect") {
			t.Errorf("%s reached %s through the guard: %v", kind, fake.URL(), err)
		}
	}
	for kind := range fakes {
		p, err := New(kind, Credentials{Token: cfToken, AccessKeyID: r53Key, SecretAccessKey: r53Secret})
		if err != nil {
			t.Fatal(err)
		}
		if base := baseOf(p); !strings.HasPrefix(base, "https://") || strings.Contains(base, "127.0.0.1") {
			t.Errorf("%s talks to %s by default", kind, base)
		}
	}
}

func baseOf(p Provider) string {
	switch p := p.(type) {
	case *cloudflare:
		return p.base
	case *hetzner:
		return p.base
	case *digitalocean:
		return p.base
	case *route53:
		return p.base
	}
	return ""
}

func TestCredentialsAreCheckedBeforeTheyAreSent(t *testing.T) {
	// A Global API Key is 37 hex characters, and can do anything.
	if err := CheckCredentials(Cloudflare, Credentials{Token: "1234567890abcdef1234567890abcdef12345"}); !errors.Is(err, ErrGlobalKey) {
		t.Errorf("a Global API Key was taken for a token: %v", err)
	}
	if err := CheckCredentials(Cloudflare, Credentials{Token: cfToken}); err != nil {
		t.Errorf("a token was refused: %v", err)
	}
	for _, bad := range []struct {
		kind  string
		creds Credentials
	}{
		{Hetzner, Credentials{}},
		{DigitalOcean, Credentials{Token: "two words"}},
		{Route53, Credentials{AccessKeyID: r53Key}},
		{"godaddy", Credentials{Token: "x"}},
	} {
		if err := CheckCredentials(bad.kind, bad.creds); err == nil {
			t.Errorf("%s accepted %+v", bad.kind, bad.creds)
		}
	}
}
