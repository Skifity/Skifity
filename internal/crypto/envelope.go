// Package crypto implements envelope encryption for every secret the panel stores.
//
// Each secret gets its own random 256-bit data encryption key (DEK). The secret is
// sealed with the DEK, and the DEK is sealed with a master key from the keyring.
// Both operations use AES-256-GCM.
//
// Two properties matter:
//
//   - A stolen database is useless on its own, because no master key is in it.
//   - Rotating the master key only rewraps DEKs. Plaintext is never decrypted, so
//     rotation is fast and cannot corrupt a secret it fails to parse.
//
// Every ciphertext is bound to a context string (additional authenticated data).
// Moving a ciphertext from one row to another therefore fails to open, which stops
// an attacker with write access to the database from swapping one app's database
// password into another app.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	// KeySize is the size of both the master key and every data key.
	KeySize = 32
	// nonceSize is the AES-GCM standard nonce size.
	nonceSize = 12
	// magic identifies the envelope format and its version.
	magic   = "SKF1"
	magicV1 = 1
)

// Errors returned by this package. They are deliberately coarse: callers must not
// be able to tell "wrong key" from "corrupt ciphertext" from timing or message.
var (
	ErrUnknownKey    = errors.New("envelope was sealed with a master key that is not in the keyring")
	ErrMalformed     = errors.New("envelope is malformed")
	ErrOpenFailed    = errors.New("envelope could not be opened: wrong key, wrong context, or tampered data")
	ErrKeySize       = fmt.Errorf("master key must be exactly %d bytes", KeySize)
	ErrEmptyKeyring  = errors.New("keyring has no keys")
	ErrDuplicateKeyI = errors.New("keyring already contains a key with that id")
)

// Envelope is a sealed secret. It is stored as a single opaque string.
type Envelope struct {
	// KeyID identifies the master key that wrapped the data key.
	KeyID string
	// WrappedDEK is the data key sealed with the master key.
	WrappedDEK []byte
	// Ciphertext is the secret sealed with the data key.
	Ciphertext []byte
}

// Keyring holds the master keys. It has exactly one active key, which is used for
// new and rewrapped envelopes, plus any number of retired keys kept so existing
// envelopes can still be opened during a rotation.
//
// One Keyring is shared by everything in the panel — the API, the deployer, the
// backup manager, the cluster adapter — and a rotation writes to it while those
// are reading. A Go map read during a map write is not a race the program
// survives: the runtime throws, and a throw is not a panic, so nothing recovers
// it. Hence the lock, held for every read of the map as well as every write.
type Keyring struct {
	mu       sync.RWMutex
	keys     map[string][]byte
	activeID string
}

// NewKeyring creates a keyring whose active key is the given one.
func NewKeyring(activeID string, activeKey []byte) (*Keyring, error) {
	if len(activeKey) != KeySize {
		return nil, ErrKeySize
	}
	if activeID == "" {
		return nil, errors.New("master key id must not be empty")
	}
	if err := checkKeyID(activeID); err != nil {
		return nil, err
	}
	k := &Keyring{keys: map[string][]byte{}, activeID: activeID}
	k.keys[activeID] = append([]byte(nil), activeKey...)
	return k, nil
}

// maxKeyIDLen is what the envelope header can carry: one byte of length.
//
// The id comes out of the master key file, which an operator edits by hand.
// Without this, a longer one would be written into that byte truncated, every
// secret sealed afterwards would be unopenable, and nothing would say so until
// something tried to read one back — long after the rows were written.
const maxKeyIDLen = 255

func checkKeyID(id string) error {
	switch {
	case id == "":
		return errors.New("master key id must not be empty")
	case strings.Contains(id, ":"):
		return errors.New("master key id must not contain a colon")
	case len(id) > maxKeyIDLen:
		return fmt.Errorf("master key id is %d bytes; an envelope header holds at most %d",
			len(id), maxKeyIDLen)
	}
	return nil
}

// AddRetired registers a key that can still open old envelopes but is never used
// to seal new ones.
func (k *Keyring) AddRetired(id string, key []byte) error {
	if len(key) != KeySize {
		return ErrKeySize
	}
	if err := checkKeyID(id); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.addLocked(id, key)
}

// addLocked is AddRetired with the lock already held, for the callers that hold
// it while doing something else in the same breath.
func (k *Keyring) addLocked(id string, key []byte) error {
	if _, exists := k.keys[id]; exists {
		return ErrDuplicateKeyI
	}
	k.keys[id] = append([]byte(nil), key...)
	return nil
}

// ActiveID is the id of the key that seals new envelopes.
func (k *Keyring) ActiveID() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.activeID
}

// IDs lists every key id in the keyring, active and retired.
func (k *Keyring) IDs() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]string, 0, len(k.keys))
	for id := range k.keys {
		out = append(out, id)
	}
	return out
}

// Promote makes an already-registered key the active one. Used by rotation once
// the new key has been persisted.
func (k *Keyring) Promote(id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.keys[id]; !ok {
		return ErrUnknownKey
	}
	k.activeID = id
	return nil
}

// GenerateKey returns a fresh random master or data key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return key, nil
}

// Seal encrypts plaintext and returns the envelope as a storable string.
//
// context binds the ciphertext to where it lives, for example
// "variable:app_7f3a:DATABASE_URL". The same context must be passed to Open.
func (k *Keyring) Seal(plaintext []byte, context string) (string, error) {
	// Held across the seal rather than only across the map read: DropKey zeroes
	// a key's bytes, and a slice read after that is a key of zeros.
	k.mu.RLock()
	defer k.mu.RUnlock()

	if len(k.keys) == 0 {
		return "", ErrEmptyKeyring
	}
	activeID := k.activeID
	master, ok := k.keys[activeID]
	if !ok {
		return "", ErrUnknownKey
	}

	dek, err := GenerateKey()
	if err != nil {
		return "", err
	}
	defer zero(dek)

	ciphertext, err := sealWith(dek, plaintext, []byte(context))
	if err != nil {
		return "", fmt.Errorf("seal secret: %w", err)
	}
	// The wrapped DEK is bound to the key id, so a wrapped DEK cannot be
	// replayed under a different master key entry.
	wrapped, err := sealWith(master, dek, []byte(activeID))
	if err != nil {
		return "", fmt.Errorf("wrap data key: %w", err)
	}

	return encode(Envelope{KeyID: activeID, WrappedDEK: wrapped, Ciphertext: ciphertext})
}

// Open decrypts an envelope produced by Seal. context must match exactly.
func (k *Keyring) Open(stored, context string) ([]byte, error) {
	env, err := decode(stored)
	if err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	master, ok := k.keys[env.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w: key id %q", ErrUnknownKey, env.KeyID)
	}
	dek, err := openWith(master, env.WrappedDEK, []byte(env.KeyID))
	if err != nil {
		return nil, ErrOpenFailed
	}
	defer zero(dek)

	plaintext, err := openWith(dek, env.Ciphertext, []byte(context))
	if err != nil {
		return nil, ErrOpenFailed
	}
	return plaintext, nil
}

// Rewrap moves an envelope onto the active master key without touching the
// plaintext. It returns the new envelope and whether anything changed, so a
// rotation pass can skip envelopes that are already current.
func (k *Keyring) Rewrap(stored string) (string, bool, error) {
	env, err := decode(stored)
	if err != nil {
		return "", false, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()

	activeID := k.activeID
	if env.KeyID == activeID {
		return stored, false, nil
	}
	old, ok := k.keys[env.KeyID]
	if !ok {
		return "", false, fmt.Errorf("%w: key id %q", ErrUnknownKey, env.KeyID)
	}
	active, ok := k.keys[activeID]
	if !ok {
		return "", false, ErrUnknownKey
	}

	dek, err := openWith(old, env.WrappedDEK, []byte(env.KeyID))
	if err != nil {
		return "", false, ErrOpenFailed
	}
	defer zero(dek)

	wrapped, err := sealWith(active, dek, []byte(activeID))
	if err != nil {
		return "", false, fmt.Errorf("wrap data key: %w", err)
	}
	sealed, err := encode(Envelope{KeyID: activeID, WrappedDEK: wrapped, Ciphertext: env.Ciphertext})
	if err != nil {
		return "", false, err
	}
	return sealed, true, nil
}

// KeyIDOf reports which master key sealed an envelope, without opening it.
func KeyIDOf(stored string) (string, error) {
	env, err := decode(stored)
	if err != nil {
		return "", err
	}
	return env.KeyID, nil
}

// IsEnvelope reports whether a stored string looks like one of our envelopes.
// Used by migrations to tell encrypted columns from legacy plaintext.
func IsEnvelope(stored string) bool {
	_, err := decode(stored)
	return err == nil
}

func sealWith(key, plaintext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func openWith(key, blob, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < nonceSize {
		return nil, ErrMalformed
	}
	return gcm.Open(nil, blob[:nonceSize], blob[nonceSize:], aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// encode serialises an envelope as "SKF1.<base64url>". The binary body is
//
//	version(1) | keyIDLen(1) | keyID | wrappedLen(2 BE) | wrappedDEK | ciphertext
func encode(e Envelope) (string, error) {
	// Both lengths are bounded by construction — the id is checked when a key
	// joins the keyring, and a wrapped 32-byte DEK is 60 bytes — so this can
	// only fire if one of those changes. Truncating a length field produces an
	// envelope that looks fine and can never be opened, which is worse than any
	// error, so it is refused here rather than caught later.
	if len(e.KeyID) > maxKeyIDLen {
		return "", fmt.Errorf("key id is %d bytes; an envelope header holds at most %d",
			len(e.KeyID), maxKeyIDLen)
	}
	if len(e.WrappedDEK) > 0xFFFF {
		return "", fmt.Errorf("wrapped data key is %d bytes; an envelope header holds at most %d",
			len(e.WrappedDEK), 0xFFFF)
	}
	body := make([]byte, 0, 4+len(e.KeyID)+len(e.WrappedDEK)+len(e.Ciphertext))
	body = append(body, magicV1, byte(len(e.KeyID)))
	body = append(body, e.KeyID...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(e.WrappedDEK)))
	body = append(body, e.WrappedDEK...)
	body = append(body, e.Ciphertext...)
	return magic + "." + base64.RawURLEncoding.EncodeToString(body), nil
}

func decode(stored string) (Envelope, error) {
	rest, ok := strings.CutPrefix(stored, magic+".")
	if !ok {
		return Envelope{}, ErrMalformed
	}
	// Strict rejects a non-canonical encoding, so an envelope has exactly one
	// valid textual form and cannot be mutated in the unused trailing bits.
	body, err := base64.RawURLEncoding.Strict().DecodeString(rest)
	if err != nil {
		return Envelope{}, ErrMalformed
	}
	if len(body) < 2 || body[0] != magicV1 {
		return Envelope{}, ErrMalformed
	}
	idLen := int(body[1])
	pos := 2
	if len(body) < pos+idLen+2 {
		return Envelope{}, ErrMalformed
	}
	keyID := string(body[pos : pos+idLen])
	pos += idLen
	wrappedLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body) < pos+wrappedLen+nonceSize {
		return Envelope{}, ErrMalformed
	}
	return Envelope{
		KeyID:      keyID,
		WrappedDEK: body[pos : pos+wrappedLen],
		Ciphertext: body[pos+wrappedLen:],
	}, nil
}

// zero overwrites key material once it is no longer needed. Go's garbage
// collector may still have copied it, so this is defence in depth rather than a
// guarantee.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ConstantTimeEqual compares two secrets without leaking their contents through
// timing. Used for session tokens, API tokens and the setup token.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
