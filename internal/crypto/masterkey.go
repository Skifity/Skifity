package crypto

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KeyFile is the on-disk representation of the keyring.
//
// It is a tiny line-oriented format rather than JSON so that an operator can read
// it, and so that a partially written file fails to parse instead of silently
// losing a retired key.
//
//	# comments are ignored
//	active = k2
//	k2 = <base64 32 bytes>
//	k1 = <base64 32 bytes>
type KeyFile struct {
	Active string
	Keys   map[string]string // id -> base64 key
}

// LoadKeyring reads a key file and returns a usable keyring.
func LoadKeyring(path string) (*Keyring, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("master key file %s does not exist: run the installer, or restore it from your recovery key", path)
		}
		return nil, fmt.Errorf("read master key file %s: %w", path, err)
	}
	if err := checkPermissions(path); err != nil {
		return nil, err
	}
	kf, err := parseKeyFile(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse master key file %s: %w", path, err)
	}
	return kf.Keyring()
}

// Keyring turns a parsed key file into a keyring.
func (kf KeyFile) Keyring() (*Keyring, error) {
	activeRaw, ok := kf.Keys[kf.Active]
	if !ok {
		return nil, fmt.Errorf("master key file names active key %q but does not contain it", kf.Active)
	}
	activeKey, err := decodeKey(activeRaw)
	if err != nil {
		return nil, fmt.Errorf("active key %q: %w", kf.Active, err)
	}
	ring, err := NewKeyring(kf.Active, activeKey)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(kf.Keys))
	for id := range kf.Keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if id == kf.Active {
			continue
		}
		key, err := decodeKey(kf.Keys[id])
		if err != nil {
			return nil, fmt.Errorf("retired key %q: %w", id, err)
		}
		if err := ring.AddRetired(id, key); err != nil {
			return nil, err
		}
	}
	return ring, nil
}

// SaveKeyring writes a keyring back to disk atomically with 0600 permissions.
//
// Writing to a temporary file in the same directory and renaming means a crash
// mid-write cannot leave a truncated key file, which would be unrecoverable.
func SaveKeyring(path string, ring *Keyring) error {
	var b strings.Builder
	b.WriteString("# Skifity master keys. Anyone holding this file can read every stored secret.\n")
	b.WriteString("# Losing it means losing every stored secret. Keep the recovery key somewhere safe.\n")

	// One read lock for the whole file: a rotation halfway through would
	// otherwise write an active line naming a key the loop had not reached.
	ring.mu.RLock()
	fmt.Fprintf(&b, "active = %s\n", ring.activeID)
	ids := make([]string, 0, len(ring.keys))
	for id := range ring.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "%s = %s\n", id, base64.StdEncoding.EncodeToString(ring.keys[id]))
	}
	ring.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".master-key-*")
	if err != nil {
		return fmt.Errorf("create temporary key file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set key file permissions: %w", err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("write key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close key file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install key file: %w", err)
	}
	// The rename itself has to reach the disk. Without this a crash seconds
	// later can leave the directory entry unwritten, and the file this whole
	// design exists to protect is the one file whose loss cannot be undone.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open key directory to sync it: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync key directory: %w", err)
	}
	return nil
}

// InitKeyring creates a key file with a single fresh key when none exists, and
// returns the keyring either way. It is safe to call on every start.
func InitKeyring(path string) (*Keyring, error) {
	if _, err := os.Stat(path); err == nil {
		return LoadKeyring(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat master key file %s: %w", path, err)
	}
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	ring, err := NewKeyring("k1", key)
	if err != nil {
		return nil, err
	}
	if err := SaveKeyring(path, ring); err != nil {
		return nil, err
	}
	return ring, nil
}

// NextKeyID returns an unused key id of the form k<n>, used when rotating.
func (k *Keyring) NextKeyID() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.nextKeyIDLocked()
}

func (k *Keyring) nextKeyIDLocked() string {
	for n := len(k.keys) + 1; ; n++ {
		id := fmt.Sprintf("k%d", n)
		if _, taken := k.keys[id]; !taken {
			return id
		}
	}
}

// BeginRotation adds a freshly generated key and makes it active. Existing
// envelopes stay readable through the retired keys, and a rotation pass rewraps
// them in the background.
func (k *Keyring) BeginRotation() (string, error) {
	key, err := GenerateKey()
	if err != nil {
		return "", err
	}
	// One lock around choosing the id, adding the key and promoting it: two
	// rotations started at the same moment would otherwise pick the same id,
	// and the second would fail after the first had already moved the active
	// key — a rotation half-done for a reason nobody could see.
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.nextKeyIDLocked()
	if err := k.addLocked(id, key); err != nil {
		return "", err
	}
	k.activeID = id
	return id, nil
}

// DropKey removes a retired key once nothing references it any more. Refusing to
// drop the active key stops an operator from locking themselves out with one call.
func (k *Keyring) DropKey(id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if id == k.activeID {
		return errors.New("refusing to drop the active master key")
	}
	if _, ok := k.keys[id]; !ok {
		return ErrUnknownKey
	}
	zero(k.keys[id])
	delete(k.keys, id)
	return nil
}

// RecoveryKey renders the active master key as grouped Base32, which is far
// easier to copy by hand or read over the phone than Base64.
//
//	SKIFITY-RECOVERY-v1-k1-ABCDEFGH-IJKLMNOP-...
func (k *Keyring) RecoveryKey() (string, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[k.activeID]
	if !ok {
		return "", ErrUnknownKey
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)
	groups := make([]string, 0, (len(enc)+7)/8)
	for i := 0; i < len(enc); i += 8 {
		groups = append(groups, enc[i:min(i+8, len(enc))])
	}
	return "SKIFITY-RECOVERY-v1-" + k.activeID + "-" + strings.Join(groups, "-"), nil
}

// ParseRecoveryKey rebuilds a keyring from a recovery key string. Whitespace and
// lowercase input are accepted because people retype these from paper.
func ParseRecoveryKey(s string) (*Keyring, error) {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	rest, ok := strings.CutPrefix(s, "SKIFITY-RECOVERY-V1-")
	if !ok {
		return nil, errors.New("this does not look like a Skifity recovery key: it should start with SKIFITY-RECOVERY-v1-")
	}
	keyID, body, ok := strings.Cut(rest, "-")
	if !ok || keyID == "" {
		return nil, errors.New("recovery key is missing its key id")
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ReplaceAll(body, "-", ""))
	if err != nil {
		return nil, fmt.Errorf("recovery key contains characters that are not valid: %w", err)
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("recovery key decodes to %d bytes, expected %d: it looks truncated", len(raw), KeySize)
	}
	return NewKeyring(strings.ToLower(keyID), raw)
}

func parseKeyFile(src string) (KeyFile, error) {
	kf := KeyFile{Keys: map[string]string{}}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return kf, fmt.Errorf("line %d: expected `id = value`", i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "active" {
			kf.Active = value
			continue
		}
		kf.Keys[key] = value
	}
	if kf.Active == "" {
		return kf, errors.New("no `active` line")
	}
	if len(kf.Keys) == 0 {
		return kf, errors.New("no keys")
	}
	return kf, nil
}

func decodeKey(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("value is not valid base64: %w", err)
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("decodes to %d bytes, expected %d", len(raw), KeySize)
	}
	return raw, nil
}

// checkPermissions refuses a world-readable key file. A key anyone on the host
// can read is not a key.
func checkPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat master key file: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("master key file %s is mode %#o: it must not be readable by other users. Fix it with: chmod 600 %s", path, mode, path)
	}
	return nil
}

// RandomToken returns a URL-safe random token with n bytes of entropy. Used for
// session ids, API tokens, the setup token and webhook secrets.
func RandomToken(n int) (string, error) {
	if n < 16 {
		return "", fmt.Errorf("token needs at least 16 bytes of entropy, got %d", n)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// RandomName returns n random lowercase base32 characters.
//
// Not RandomToken: that is base64url, which contains "_" and uppercase, and a
// Kubernetes object name may hold neither. A name built from one is refused by
// the API server, which is a failure a long way from its cause.
func RandomName(n int) (string, error) {
	if n < 4 || n > 32 {
		return "", fmt.Errorf("a random name of %d characters is not useful", n)
	}
	// Five base32 characters per four bytes, rounded up.
	buf := make([]byte, (n*5+7)/8+1)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	return encoded[:n], nil
}
