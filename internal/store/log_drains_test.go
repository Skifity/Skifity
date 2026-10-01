package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"skifity/internal/crypto"
)

// A drain's secrets are sealed to the team, the drain and the address, are on
// the list a master key rotation walks, and open again afterwards; its
// projects narrow it to the team's own; and the collector reads only the
// enabled ones.
func TestALogDrainIsSealedRotatedAndLimited(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, project, _ := seedTeam(t, db)
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}

	// Another team's project, which a drain of this team cannot be limited to.
	other := Team{Name: "Globex", Slug: "globex"}
	if err := db.CreateTeam(ctx, &other); err != nil {
		t.Fatal(err)
	}
	theirs := Project{TeamID: other.ID, Name: "Theirs", Slug: "theirs"}
	if err := db.CreateProject(ctx, &theirs); err != nil {
		t.Fatal(err)
	}

	const secret = `{"password":"not-a-real-loki-token"}`
	const destination = "https://logs.example.com/loki/api/v1/push"
	drain := LogDrainRow{LogDrain: LogDrain{
		ID: NewID("ldr"), TeamID: team.ID, Name: "Grafana", Kind: "loki",
		Settings: map[string]string{"url": "https://logs.example.com", "username": "123"},
		Enabled:  true, Scoped: true, Projects: []string{project.ID, theirs.ID}, TestedAt: time.Now(),
	}}
	drain.SealedSecrets, err = keyring.Seal([]byte(secret), LogDrainContext(team.ID, drain.ID, destination))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateLogDrain(ctx, &drain); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := db.GetLogDrain(ctx, team.ID, drain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Projects, []string{project.ID}) {
		t.Errorf("the drain is limited to %v; another team's project was stored", got.Projects)
	}
	if got.Settings["username"] != "123" || got.TestedAt.IsZero() {
		t.Errorf("the drain came back as %+v", got)
	}
	if _, err := db.GetLogDrain(ctx, other.ID, drain.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team read the drain: %v", err)
	}
	// A second drain of the same name in the same team is a conflict.
	twin := LogDrainRow{LogDrain: LogDrain{ID: NewID("ldr"), TeamID: team.ID, Name: "Grafana", Kind: "http",
		Settings: map[string]string{}}}
	if err := db.CreateLogDrain(ctx, &twin); !errors.Is(err, ErrConflict) {
		t.Errorf("a second drain called Grafana answered %v", err)
	}

	// Rotation reaches it, and it opens afterwards under the same context —
	// and not under another team's or another address's.
	if _, err := keyring.BeginRotation(); err != nil {
		t.Fatal(err)
	}
	refs, err := db.ListSealedSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		if ref.Table != "log_drains" || ref.ID != drain.ID {
			continue
		}
		found = true
		next, changed, err := keyring.Rewrap(ref.Sealed)
		if err != nil || !changed {
			t.Fatalf("rewrap: %v, %v", changed, err)
		}
		if written, err := db.UpdateSealed(ctx, ref, next); err != nil || !written {
			t.Fatalf("the rewrapped secrets were not written back: %v, %v", written, err)
		}
	}
	if !found {
		t.Fatal("a master key rotation would leave the drain's secrets behind")
	}
	rotated, _ := db.GetLogDrain(ctx, team.ID, drain.ID)
	if rotated.SealedSecrets == drain.SealedSecrets {
		t.Error("the secrets are still wrapped in the old key")
	}
	opened, err := keyring.Open(rotated.SealedSecrets, LogDrainContext(team.ID, drain.ID, destination))
	if err != nil || string(opened) != secret {
		t.Errorf("after rotation the secrets open as %q (%v)", opened, err)
	}
	if _, err := keyring.Open(rotated.SealedSecrets, LogDrainContext(team.ID, drain.ID, "https://evil.example.com")); err == nil {
		t.Error("the secrets open for another address")
	}
	if _, err := keyring.Open(rotated.SealedSecrets, LogDrainContext(other.ID, drain.ID, destination)); err == nil {
		t.Error("the secrets open for another team")
	}

	// Paused, it is not the collector's.
	rotated.Enabled = false
	if err := db.UpdateLogDrain(ctx, &rotated); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := db.ListEnabledLogDrains(ctx); len(enabled) != 0 {
		t.Errorf("a paused drain is given to the collector: %d", len(enabled))
	}

	// The project going leaves the drain limited to nothing, not to
	// everything.
	if err := db.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetLogDrain(ctx, team.ID, drain.ID)
	if !after.Scoped || len(after.Projects) != 0 {
		t.Errorf("after its project went the drain is scoped %v to %v", after.Scoped, after.Projects)
	}

	// The names the collector puts beside the ids are this team's and no
	// other's.
	names, err := db.LogDrainNames(ctx, []string{team.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.ID == other.ID || name.ID == theirs.ID {
			t.Errorf("another team's %s is in the names", name.Kind)
		}
	}
	if !slices.ContainsFunc(names, func(n LogDrainName) bool { return n.Kind == "team" && n.Name == "Acme" }) {
		t.Errorf("the names are %v", names)
	}

	if err := db.DeleteLogDrain(ctx, other.ID, drain.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team deleted the drain: %v", err)
	}
	if err := db.DeleteLogDrain(ctx, team.ID, drain.ID); err != nil {
		t.Fatal(err)
	}
}
