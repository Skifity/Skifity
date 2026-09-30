package secretmgr

import (
	"errors"
	"strings"
	"testing"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// connect stores a connection for a team, the way the API does.
func connect(t *testing.T, db *store.DB, keyring *crypto.Keyring, teamID, name, kind string, settings, credentials map[string]string) store.SecretConnection {
	t.Helper()
	settings, credentials, err := Normalize(kind, settings, credentials)
	if err != nil {
		t.Fatal(err)
	}
	c := store.SecretConnection{ID: store.NewID("sm"), TeamID: teamID, Name: name, Kind: kind, Settings: settings}
	sealed, err := SealCredentials(keyring, c.ID, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSecretConnection(t.Context(), &c, sealed); err != nil {
		t.Fatal(err)
	}
	return c
}

func resolverFixture(t *testing.T) (*Resolver, *store.DB, *crypto.Keyring, string, string) {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	acme := store.Team{Name: "Acme", Slug: "acme"}
	other := store.Team{Name: "Other", Slug: "other"}
	for _, team := range []*store.Team{&acme, &other} {
		if err := db.CreateTeam(t.Context(), team); err != nil {
			t.Fatal(err)
		}
	}
	r := New(db, keyring)
	r.Client = testClient()
	return r, db, keyring, acme.ID, other.ID
}

func TestResolveReadsEachSecretOnceAndNamesWhatFailed(t *testing.T) {
	r, db, keyring, acme, other := resolverFixture(t)
	f := newFakeVault(t)
	vault := connect(t, db, keyring, acme, "company-vault", KindVault, f.settings(), f.credentials())

	values, err := r.Resolve(t.Context(), acme, []Wanted{
		{Variable: "STRIPE_KEY", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "stripe_key"}},
		{Variable: "PORT", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "port"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if values["STRIPE_KEY"] != stripeKey || values["PORT"] != "8080" {
		t.Errorf("resolved %v", values)
	}
	if n := f.reads.Load(); n != 1 {
		t.Errorf("two keys of one secret were %d reads, want 1", n)
	}

	// A key that is not there fails the lot, and says which variable, which
	// connection and why.
	_, err = r.Resolve(t.Context(), acme, []Wanted{
		{Variable: "STRIPE_KEY", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "stripe_key"}},
		{Variable: "MAIL_PASSWORD", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "mail"}},
	})
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "secrets.reference_unresolved" {
		t.Fatalf("a missing key answered %v", err)
	}
	for _, want := range []string{"MAIL_PASSWORD", "company-vault", "shop#mail", "Vault"} {
		if !strings.Contains(problem.Cause, want) {
			t.Errorf("the cause does not name %s: %s", want, problem.Cause)
		}
	}
	if strings.Contains(problem.Text(), stripeKey) {
		t.Errorf("the problem carries a value: %s", problem.Text())
	}

	// Another team's connection is not found, even with its id.
	_, err = r.Resolve(t.Context(), other, []Wanted{
		{Variable: "STOLEN", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "stripe_key"}},
	})
	if !errors.As(err, &problem) || problem.Code != "secrets.reference_unresolved" || problem.Context["reason"] != string(NotFound) {
		t.Errorf("another team's connection answered %v", err)
	}
}

func TestACacheIsSharedAcrossResolutions(t *testing.T) {
	r, db, keyring, acme, _ := resolverFixture(t)
	f := newFakeVault(t)
	vault := connect(t, db, keyring, acme, "company-vault", KindVault, f.settings(), f.credentials())
	ctx := WithCache(t.Context())
	for range 3 {
		if _, err := r.Resolve(ctx, acme, []Wanted{
			{Variable: "STRIPE_KEY", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "stripe_key"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Errorf("three resolutions under one cache made %d reads, want 1", n)
	}
}

// A master key rotation walks the sealed columns and rewraps each: a
// connection's credentials and an app's reference digests are among them, and
// both open under their own context afterwards, and only with the new key.
func TestRotatingTheMasterKeyKeepsAConnectionReadable(t *testing.T) {
	r, db, keyring, acme, _ := resolverFixture(t)
	f := newFakeVault(t)
	vault := connect(t, db, keyring, acme, "company-vault", KindVault, f.settings(), f.credentials())

	refs, err := db.ListSealedSecrets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := crypto.GenerateKey()
	if err := keyring.AddRetired("k2", fresh); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Promote("k2"); err != nil {
		t.Fatal(err)
	}
	rotated := 0
	for _, ref := range refs {
		if ref.Table != "secret_connections" {
			continue
		}
		rewrapped, changed, err := keyring.Rewrap(ref.Sealed)
		if err != nil || !changed {
			t.Fatalf("rewrap %s: %v %v", ref.ID, changed, err)
		}
		if ok, err := db.UpdateSealed(t.Context(), ref, rewrapped); err != nil || !ok {
			t.Fatalf("write back %s: %v %v", ref.ID, ok, err)
		}
		rotated++
	}
	if rotated != 1 {
		t.Fatalf("%d connections were rotated, want 1", rotated)
	}
	row, err := db.GetSecretConnection(t.Context(), vault.ID)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := crypto.KeyIDOf(row.SealedCredentials); id != "k2" {
		t.Errorf("the credentials are still wrapped in %s", id)
	}
	if err := r.TestConnection(t.Context(), row); err != nil {
		t.Errorf("the connection does not sign in after a rotation: %v", err)
	}
}

// Two secrets from one Infisical are one login, not two.
func TestOneResolutionSignsInOnce(t *testing.T) {
	r, db, keyring, acme, _ := resolverFixture(t)
	f := newFakeInfisical(t)
	infisical := connect(t, db, keyring, acme, "infisical", KindInfisical, f.settings(), f.credentials())
	values, err := r.Resolve(t.Context(), acme, []Wanted{
		{Variable: "STRIPE_KEY", Reference: store.SecretReference{ConnectionID: infisical.ID, Path: "STRIPE_KEY"}},
		{Variable: "FROM_CONFIG", Reference: store.SecretReference{ConnectionID: infisical.ID, Path: "/backend/CONFIG", Key: "stripe_key"}},
	})
	if err != nil || values["STRIPE_KEY"] != stripeKey || values["FROM_CONFIG"] != stripeKey {
		t.Fatalf("resolved %v, %v", values, err)
	}
	if n := f.logins.Load(); n != 1 {
		t.Errorf("two secrets were %d logins, want 1", n)
	}
}

func TestAStoredConnectionIsTestedWithItsSealedCredentials(t *testing.T) {
	r, db, keyring, acme, _ := resolverFixture(t)
	f := newFakeInfisical(t)
	stored := connect(t, db, keyring, acme, "infisical", KindInfisical, f.settings(), f.credentials())
	row, err := db.GetSecretConnection(t.Context(), stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.SealedCredentials, fakeInfisicalKey) {
		t.Fatal("the client secret is stored as it was typed")
	}
	if err := r.TestConnection(t.Context(), row); err != nil {
		t.Errorf("the stored connection failed its test: %v", err)
	}
	// Sealed under its own id: moved to another row, it does not open.
	row.ID = "sm_elsewhere"
	if err := r.TestConnection(t.Context(), row); err == nil {
		t.Error("credentials sealed for one connection opened as another's")
	}
}
