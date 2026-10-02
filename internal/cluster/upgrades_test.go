package cluster

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

func TestAComponentsVersionIsTheOneThisPanelInstalls(t *testing.T) {
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := New(nil, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for name, want := range map[string]string{
		"cert-manager":   "1.21.2",
		"cloudnative-pg": "1.29.0", // not the 1.29 of its release branch
		"keda":           "2.20.0",
		"longhorn":       "1.10.0",
		"registry":       "3.1.2",
		"buildkit":       builder.BuildKitVersion,
		"system-upgrade": "0.20.2",
		GuardComponent:   "",
	} {
		if got := c.ComponentVersion(t.Context(), name); got != want {
			t.Errorf("%s is version %q, want %q", name, got, want)
		}
	}

	// A manifest somebody pointed a setting at is the version they chose.
	if err := db.SetSetting(t.Context(), "components.keda_url",
		"https://mirror.internal/keda/keda-2.21.1.yaml", false, "test"); err != nil {
		t.Fatal(err)
	}
	if got := c.ComponentVersion(t.Context(), "keda"); got != "2.21.1" {
		t.Fatalf("keda from a setting is %q", got)
	}
	if tag := imageTag("registry.internal:5000/cloudflared"); tag != "" {
		t.Fatalf("a registry's port was read as a tag: %q", tag)
	}
	if tag := imageTag("registry.internal:5000/registry:3.1.2@sha256:ddf754342cfc"); tag != "3.1.2" {
		t.Fatalf("an image pinned by digest is read as version %q", tag)
	}
}

func TestK3sReleasesAreReadFromItsChannels(t *testing.T) {
	releases, err := parseK3sChannels(strings.NewReader(`{"type":"collection","data":[
		{"id":"stable","latest":"v1.36.4+k3s1"},
		{"id":"latest","latest":"v1.37.0+k3s1"},
		{"id":"testing","latest":"v1.18.2-rc3+k3s1"},
		{"id":"v1.35","latest":"v1.35.9+k3s1"},
		{"id":"v1.37","latest":"v1.37.0+k3s1"},
		{"id":"v1.36","latest":"v1.36.4+k3s1"},
		{"id":"v1.36-testing","latest":"v1.36.5-rc1+k3s1"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, release := range releases {
		label := release.Channel + "=" + release.Version
		if release.Stable {
			label += "*"
		}
		got = append(got, label)
	}
	if strings.Join(got, " ") != "v1.37=v1.37.0+k3s1 v1.36=v1.36.4+k3s1* v1.35=v1.35.9+k3s1" {
		t.Fatalf("releases %v", got)
	}
	if _, err := parseK3sChannels(strings.NewReader(`{"data":[]}`)); err == nil {
		t.Fatal("an empty channel list was taken as no releases")
	}
}

func TestAComponentIsNotDowngradedOrMovedSeveralMinorsAtOnce(t *testing.T) {
	for _, c := range []struct {
		name, installed, wanted, refused string
	}{
		{"longhorn", "1.7.2", "1.8.1", ""},
		{"longhorn", "1.7.2", "1.9.0", "latest 1.8 release in the components.longhorn_url setting"},
		{"cert-manager", "1.22.0", "1.21.2", "which is older"},
		// A component whose version is an image tag is not upgraded from a
		// manifest, and has no minor rule to keep.
		{"registry", "2.8.3", "3.0.0", ""},
		{"longhorn", "", "1.9.0", ""},
	} {
		err := checkComponentUpgrade(c.name, c.name, c.installed, c.wanted)
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%s %s → %s was refused: %v", c.name, c.installed, c.wanted, err)
		case c.refused != "" && (err == nil || !strings.Contains(errdoc.From(err).Cause+err.Error(), c.refused)):
			t.Errorf("%s %s → %s: %v, want it refused saying %q", c.name, c.installed, c.wanted, err, c.refused)
		}
	}
}
