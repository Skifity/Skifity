package store

import (
	"errors"
	"testing"
	"time"
)

// A certificate is replaced in place by its name, keeping its id — the id is
// part of what its key is sealed under — and a replacement starts its expiry
// warnings again.
func TestACertificateIsReplacedByNameAndKeepsItsID(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, _ := seedTeam(t, db)

	first := Certificate{
		ID: NewID("crt"), TeamID: team.ID, Name: "wildcard", Hostnames: []string{"*.example.com"},
		Subject: "*.example.com", Issuer: "Example CA", NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(10 * 24 * time.Hour), Fingerprint: "AA:BB", KeyType: "ECDSA P-256",
		ChainLength: 2, ChainPEM: "-----BEGIN CERTIFICATE-----\nfirst\n",
	}
	replaced, err := db.SaveCertificate(ctx, &first, "SKF1.sealed-first")
	if err != nil || replaced {
		t.Fatalf("save: replaced %v, %v", replaced, err)
	}
	if err := db.SetCertificateExpiryNotified(ctx, first.ID, first.NotAfter, 21); err != nil {
		t.Fatal(err)
	}

	renewal := first
	renewal.NotAfter = time.Now().Add(400 * 24 * time.Hour)
	renewal.Hostnames = []string{"*.example.com", "example.com"}
	renewal.ChainPEM = "-----BEGIN CERTIFICATE-----\nrenewal\n"
	replaced, err = db.SaveCertificate(ctx, &renewal, "SKF1.sealed-renewal")
	if err != nil || !replaced {
		t.Fatalf("replace: replaced %v, %v", replaced, err)
	}

	list, err := db.ListCertificates(ctx, team.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d, %v", len(list), err)
	}
	got := list[0]
	if got.ID != first.ID || len(got.Hostnames) != 2 || got.ExpiryNotified != 0 {
		t.Errorf("after the renewal: id %s, hostnames %v, warnings sent %d", got.ID, got.Hostnames, got.ExpiryNotified)
	}
	if got.ChainPEM != "" {
		t.Error("a list carries the chain, which nothing that lists needs")
	}
	chain, sealed, err := db.CertificateMaterial(ctx, team.ID, first.ID)
	if err != nil || chain != renewal.ChainPEM || sealed != "SKF1.sealed-renewal" {
		t.Errorf("material is %q, %q (%v)", chain, sealed, err)
	}
	// A master key rotation reaches the key: it is on the list rotation walks,
	// and written back where it came from.
	refs, err := db.ListSealedSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		if ref.Table == "certificates" && ref.ID == first.ID && ref.Sealed == "SKF1.sealed-renewal" {
			found = true
			if written, err := db.UpdateSealed(ctx, ref, "SKF1.rewrapped"); err != nil || !written {
				t.Errorf("the rewrapped key was not written back: %v, %v", written, err)
			}
		}
	}
	if !found {
		t.Fatal("a master key rotation would leave the certificate's key behind")
	}
	if _, sealed, _ := db.CertificateMaterial(ctx, team.ID, first.ID); sealed != "SKF1.rewrapped" {
		t.Errorf("after rotation the key is %q", sealed)
	}

	// A warning recorded against the old date changes nothing on the new one.
	if err := db.SetCertificateExpiryNotified(ctx, first.ID, first.NotAfter, 7); err != nil {
		t.Fatal(err)
	}
	if again, _ := db.GetCertificate(ctx, team.ID, first.ID); again.ExpiryNotified != 0 {
		t.Errorf("a warning about the replaced version was recorded on the new one: %d", again.ExpiryNotified)
	}

	// Another upload of the same name that raced this one, with an id of its
	// own, is refused rather than stored under a context its row will not
	// have.
	racer := renewal
	racer.ID = NewID("crt")
	if _, err := db.SaveCertificate(ctx, &racer, "SKF1.sealed-racer"); !errors.Is(err, ErrConflict) {
		t.Errorf("a racing upload was %v, want ErrConflict", err)
	}

	// Another team's certificates are not this team's.
	other := Team{Name: "Other", Slug: "other"}
	if err := db.CreateTeam(ctx, &other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetCertificate(ctx, other.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team read the certificate: %v", err)
	}
	if err := db.DeleteCertificate(ctx, other.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another team deleted the certificate: %v", err)
	}
	if others, _ := db.OtherTeamsCertificates(ctx, other.ID); len(others) != 1 {
		t.Errorf("the other team sees %d certificates of other teams, want 1", len(others))
	}
	if err := db.DeleteCertificate(ctx, team.ID, first.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateForPicksAmongATeamsCertificates(t *testing.T) {
	now := time.Now()
	certificates := []Certificate{
		{ID: "crt_old", Name: "old", Hostnames: []string{"*.example.com"}, NotAfter: now.Add(24 * time.Hour)},
		{ID: "crt_new", Name: "new", Hostnames: []string{"*.example.com"}, NotAfter: now.Add(300 * 24 * time.Hour)},
	}
	if got, ok := CertificateFor(certificates, "shop.example.com"); !ok || got.ID != "crt_new" {
		t.Errorf("chose %q (%v)", got.ID, ok)
	}
	if _, ok := CertificateFor(certificates, "example.com"); ok {
		t.Error("a wildcard covered its own apex")
	}
}
