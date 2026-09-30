package crypto

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestKeyring(t *testing.T) *Keyring {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ring, err := NewKeyring("k1", key)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return ring
}

func TestSealOpenRoundTrip(t *testing.T) {
	ring := newTestKeyring(t)
	cases := []struct{ name, plaintext string }{
		{"short", "hunter2"},
		{"empty", ""},
		{"unicode", "пароль-密码-पासवर्ड"},
		{"long", strings.Repeat("a", 64*1024)},
		{"pem", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sealed, err := ring.Seal([]byte(tc.plaintext), "variable:app_1:SECRET")
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if strings.Contains(sealed, tc.plaintext) && tc.plaintext != "" {
				t.Fatal("ciphertext contains the plaintext")
			}
			got, err := ring.Open(sealed, "variable:app_1:SECRET")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if string(got) != tc.plaintext {
				t.Fatalf("round trip mismatch: got %q want %q", got, tc.plaintext)
			}
		})
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	ring := newTestKeyring(t)
	a, err := ring.Seal([]byte("same"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := ring.Seal([]byte("same"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if a == b {
		t.Fatal("sealing the same plaintext twice produced identical ciphertext; the nonce is not random")
	}
}

func TestOpenRejectsWrongContext(t *testing.T) {
	ring := newTestKeyring(t)
	sealed, err := ring.Seal([]byte("db-password"), "variable:app_1:DATABASE_URL")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// This is the attack the context binding exists to stop: copying one app's
	// ciphertext into another app's row.
	if _, err := ring.Open(sealed, "variable:app_2:DATABASE_URL"); err == nil {
		t.Fatal("opened an envelope under a different context")
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	ring := newTestKeyring(t)
	sealed, err := ring.Seal([]byte("secret"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	body, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(sealed, magic+"."))
	if err != nil {
		t.Fatalf("decode own envelope: %v", err)
	}
	// Flip one bit in every byte position in turn: the wrapped data key, the
	// nonces and the ciphertext must all be covered by an authentication tag.
	for i := range body {
		mutated := append([]byte(nil), body...)
		mutated[i] ^= 0x01
		candidate := magic + "." + base64.RawURLEncoding.EncodeToString(mutated)
		if candidate == sealed {
			continue
		}
		if _, err := ring.Open(candidate, "ctx"); err == nil {
			t.Fatalf("opened an envelope with byte %d of %d flipped", i, len(body))
		}
	}
}

func TestOpenRejectsNonCanonicalBase64(t *testing.T) {
	ring := newTestKeyring(t)
	sealed, err := ring.Seal([]byte("secret"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// RawURLEncoding leaves unused bits in the final character. Setting one of
	// them must be rejected rather than silently decoding to the same bytes.
	runes := []byte(sealed)
	last := runes[len(runes)-1]
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i := range alphabet {
		c := alphabet[i]
		if c == last {
			continue
		}
		runes[len(runes)-1] = c
		if _, err := ring.Open(string(runes), "ctx"); err == nil {
			t.Fatalf("opened an envelope whose last base64 character was changed to %q", c)
		}
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	a := newTestKeyring(t)
	b := newTestKeyring(t)
	sealed, err := a.Seal([]byte("secret"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Same key id, different key material.
	if _, err := b.Open(sealed, "ctx"); err == nil {
		t.Fatal("opened an envelope with an unrelated master key")
	}
}

func TestOpenRejectsMalformed(t *testing.T) {
	ring := newTestKeyring(t)
	for _, bad := range []string{"", "plaintext", "SKF1", "SKF1.", "SKF1.!!!!", "SKF2.AAAA", "SKF1." + strings.Repeat("A", 8)} {
		if _, err := ring.Open(bad, "ctx"); err == nil {
			t.Fatalf("Open(%q) succeeded, want an error", bad)
		}
	}
}

func TestRotationKeepsSecretsReadable(t *testing.T) {
	ring := newTestKeyring(t)
	sealed, err := ring.Seal([]byte("survives-rotation"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	newID, err := ring.BeginRotation()
	if err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if newID == "k1" {
		t.Fatal("rotation reused the old key id")
	}
	if ring.ActiveID() != newID {
		t.Fatalf("active key is %q, want %q", ring.ActiveID(), newID)
	}

	// Old envelopes must still open before the rewrap pass runs.
	if got, err := ring.Open(sealed, "ctx"); err != nil || string(got) != "survives-rotation" {
		t.Fatalf("Open before rewrap: %q, %v", got, err)
	}

	rewrapped, changed, err := ring.Rewrap(sealed)
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	if !changed {
		t.Fatal("Rewrap reported no change for an envelope on the old key")
	}
	if id, _ := KeyIDOf(rewrapped); id != newID {
		t.Fatalf("rewrapped envelope has key id %q, want %q", id, newID)
	}
	if got, err := ring.Open(rewrapped, "ctx"); err != nil || string(got) != "survives-rotation" {
		t.Fatalf("Open after rewrap: %q, %v", got, err)
	}

	// Rewrapping twice is a no-op, so a rotation pass can be resumed safely.
	if _, changed, err := ring.Rewrap(rewrapped); err != nil || changed {
		t.Fatalf("second Rewrap: changed=%v err=%v, want false/nil", changed, err)
	}

	// Dropping the retired key must not break the rewrapped envelope.
	if err := ring.DropKey("k1"); err != nil {
		t.Fatalf("DropKey: %v", err)
	}
	if _, err := ring.Open(rewrapped, "ctx"); err != nil {
		t.Fatalf("Open after dropping the retired key: %v", err)
	}
	if _, err := ring.Open(sealed, "ctx"); err == nil {
		t.Fatal("an envelope on a dropped key still opened")
	}
}

func TestDropActiveKeyRefused(t *testing.T) {
	ring := newTestKeyring(t)
	if err := ring.DropKey("k1"); err == nil {
		t.Fatal("dropping the active key was allowed")
	}
}

func TestNewKeyringRejectsBadInput(t *testing.T) {
	short := make([]byte, 16)
	if _, err := NewKeyring("k1", short); err == nil {
		t.Fatal("accepted a 16-byte master key")
	}
	full := make([]byte, KeySize)
	if _, err := NewKeyring("", full); err == nil {
		t.Fatal("accepted an empty key id")
	}
	if _, err := NewKeyring("a:b", full); err == nil {
		t.Fatal("accepted a key id containing a colon")
	}
}

func TestKeyFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	ring, err := InitKeyring(path)
	if err != nil {
		t.Fatalf("InitKeyring: %v", err)
	}
	sealed, err := ring.Seal([]byte("value"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Calling InitKeyring again must reuse the file, not generate a new key.
	again, err := InitKeyring(path)
	if err != nil {
		t.Fatalf("InitKeyring (second call): %v", err)
	}
	if got, err := again.Open(sealed, "ctx"); err != nil || string(got) != "value" {
		t.Fatalf("second keyring could not open the first keyring's envelope: %q, %v", got, err)
	}

	// A rotated keyring must survive a save/load cycle with both keys intact.
	if _, err := ring.BeginRotation(); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if err := SaveKeyring(path, ring); err != nil {
		t.Fatalf("SaveKeyring: %v", err)
	}
	reloaded, err := LoadKeyring(path)
	if err != nil {
		t.Fatalf("LoadKeyring: %v", err)
	}
	if reloaded.ActiveID() != ring.ActiveID() {
		t.Fatalf("active key id %q survived as %q", ring.ActiveID(), reloaded.ActiveID())
	}
	if len(reloaded.IDs()) != 2 {
		t.Fatalf("reloaded keyring has %d keys, want 2", len(reloaded.IDs()))
	}
	if got, err := reloaded.Open(sealed, "ctx"); err != nil || string(got) != "value" {
		t.Fatalf("reloaded keyring lost the retired key: %q, %v", got, err)
	}
}

func TestLoadKeyringRejectsLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	if _, err := InitKeyring(path); err != nil {
		t.Fatalf("InitKeyring: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	_, err := LoadKeyring(path)
	if err == nil {
		t.Fatal("loaded a world-readable master key file")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("error should tell the operator how to fix it, got: %v", err)
	}
}

func TestRecoveryKeyRoundTrip(t *testing.T) {
	ring := newTestKeyring(t)
	sealed, err := ring.Seal([]byte("recover-me"), "ctx")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	recovery, err := ring.RecoveryKey()
	if err != nil {
		t.Fatalf("RecoveryKey: %v", err)
	}
	if !strings.HasPrefix(recovery, "SKIFITY-RECOVERY-v1-k1-") {
		t.Fatalf("unexpected recovery key shape: %q", recovery)
	}

	// People retype these from paper, so lowercase and stray spaces must work.
	messy := " " + strings.ToLower(recovery) + "\n"
	restored, err := ParseRecoveryKey(messy)
	if err != nil {
		t.Fatalf("ParseRecoveryKey: %v", err)
	}
	got, err := restored.Open(sealed, "ctx")
	if err != nil {
		t.Fatalf("Open with the restored keyring: %v", err)
	}
	if string(got) != "recover-me" {
		t.Fatalf("recovered plaintext %q, want %q", got, "recover-me")
	}
}

func TestParseRecoveryKeyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"hello",
		"SKIFITY-RECOVERY-v1-",
		"SKIFITY-RECOVERY-v1-k1-AAAA",         // truncated
		"SKIFITY-RECOVERY-v1-k1-" + "1111111", // invalid base32 characters
	} {
		if _, err := ParseRecoveryKey(bad); err == nil {
			t.Fatalf("ParseRecoveryKey(%q) succeeded, want an error", bad)
		}
	}
}

func TestRandomTokenIsUniqueAndSized(t *testing.T) {
	if _, err := RandomToken(8); err == nil {
		t.Fatal("accepted an 8-byte token")
	}
	seen := map[string]bool{}
	for range 100 {
		tok, err := RandomToken(32)
		if err != nil {
			t.Fatalf("RandomToken: %v", err)
		}
		if seen[tok] {
			t.Fatal("RandomToken repeated a value")
		}
		seen[tok] = true
	}
}

func TestIsEnvelope(t *testing.T) {
	ring := newTestKeyring(t)
	sealed, _ := ring.Seal([]byte("x"), "ctx")
	if !IsEnvelope(sealed) {
		t.Fatal("IsEnvelope said no to a real envelope")
	}
	if IsEnvelope("plain text value") {
		t.Fatal("IsEnvelope said yes to plaintext")
	}
}

// TestAKeyIDTooLongForTheHeaderIsRefused: the envelope header carries the key
// id's length in one byte. A longer id would be written truncated, every secret
// sealed with it would be unopenable, and the first sign of it would be a
// decryption failure on data that was written correctly weeks earlier.
func TestAKeyIDTooLongForTheHeaderIsRefused(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	if _, err := NewKeyring(strings.Repeat("k", 256), key); err == nil {
		t.Fatal("a 256-byte key id was accepted, and its length does not fit the header")
	}
	// 255 is the boundary and has to keep working, or the check is off by one
	// in the direction that refuses something valid.
	ring, err := NewKeyring(strings.Repeat("k", 255), key)
	if err != nil {
		t.Fatalf("a 255-byte key id was refused: %v", err)
	}
	sealed, err := ring.Seal([]byte("secret"), "app:1:VAR")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	opened, err := ring.Open(sealed, "app:1:VAR")
	if err != nil {
		t.Fatalf("open what was just sealed: %v", err)
	}
	if string(opened) != "secret" {
		t.Fatalf("opened %q, want %q", opened, "secret")
	}

	retired, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if err := ring.AddRetired(strings.Repeat("r", 256), retired); err == nil {
		t.Fatal("a 256-byte retired key id was accepted")
	}
}

// One keyring is shared by everything in the panel, and a rotation writes to it
// while deploys, backups and the API read it. A Go map read during a map write
// is a runtime throw, which nothing recovers: the panel is simply gone. This is
// the test that has to be run with -race, and `make check` does.
func TestAKeyringSurvivesARotationWhileItIsBeingUsed(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	ring, err := NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := ring.Seal([]byte("a secret"), "variable:app_1:TOKEN")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := ring.Seal([]byte("another"), "variable:app_2:TOKEN"); err != nil {
					t.Error(err)
					return
				}
				if _, err := ring.Open(sealed, "variable:app_1:TOKEN"); err != nil {
					t.Error(err)
					return
				}
				_ = ring.ActiveID()
				_ = ring.IDs()
			}
		}()
	}

	for range 5 {
		if _, err := ring.BeginRotation(); err != nil {
			t.Error(err)
			break
		}
		if _, _, err := ring.Rewrap(sealed); err != nil {
			t.Error(err)
			break
		}
	}
	close(stop)
	wg.Wait()

	// The secret sealed before any of it still opens, which is the promise
	// rotation makes: old envelopes stay readable through the retired keys.
	plaintext, err := ring.Open(sealed, "variable:app_1:TOKEN")
	if err != nil {
		t.Fatalf("the secret sealed before the rotation cannot be opened: %v", err)
	}
	if string(plaintext) != "a secret" {
		t.Fatalf("opened %q", plaintext)
	}
}
