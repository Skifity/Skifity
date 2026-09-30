// Package auth handles passwords, two-factor codes, sessions, API tokens and
// sign-in rate limiting.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters.
//
// These follow the OWASP recommendation of 19 MiB of memory with two passes,
// which resists GPU cracking while staying usable on a 1 GB VPS: a login costs
// about 40 ms of one core.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrPasswordMismatch is returned when a password does not match its hash. It is
// deliberately the only error a caller can distinguish, so failure reasons do not
// leak whether an account exists.
var ErrPasswordMismatch = errors.New("password does not match")

// HashPassword returns a PHC-formatted Argon2id hash.
//
//	$argon2id$v=19$m=19456,t=2,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	parallelism := argonParallelism()
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, parallelism, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

// VerifyPassword checks a password against a stored hash.
//
// The parameters are read back from the hash rather than assumed, so hashes
// created with older settings keep working after the cost is raised.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return fmt.Errorf("stored password hash is not in the expected argon2id format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return fmt.Errorf("stored password hash has an unreadable version: %w", err)
	}
	if version != argon2.Version {
		return fmt.Errorf("stored password hash uses argon2 version %d, this build supports %d", version, argon2.Version)
	}
	var memory uint32
	var time uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &parallelism); err != nil {
		return fmt.Errorf("stored password hash has unreadable parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return fmt.Errorf("stored password hash has an unreadable salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return fmt.Errorf("stored password hash is unreadable: %w", err)
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash was made with weaker parameters than
// the current ones, so it can be upgraded on the user's next successful sign-in.
func NeedsRehash(encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return true
	}
	var memory, time uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &parallelism); err != nil {
		return true
	}
	return memory < argonMemory || time < argonTime
}

// argonParallelism keeps hashing responsive on small VPSes without letting it
// use every core on a large one.
func argonParallelism() uint8 {
	n := runtime.NumCPU()
	switch {
	case n <= 1:
		return 1
	case n >= 4:
		return 4
	default:
		return uint8(n)
	}
}

// PasswordPolicy is the minimum a password must meet.
//
// Length does far more for security than character-class rules, so the policy is
// a long minimum plus a check against the handful of passwords everyone tries.
type PasswordPolicy struct {
	MinLength int
}

// DefaultPasswordPolicy is what the panel enforces.
func DefaultPasswordPolicy() PasswordPolicy { return PasswordPolicy{MinLength: 12} }

// Check validates a password and explains what is wrong in a way the UI can show.
func (p PasswordPolicy) Check(password string) error {
	if len([]rune(password)) < p.MinLength {
		return fmt.Errorf("password must be at least %d characters long", p.MinLength)
	}
	if len(password) > 1024 {
		// Argon2 will happily hash a megabyte; refusing keeps a login cheap.
		return errors.New("password must be shorter than 1024 characters")
	}
	if isCommonPassword(password) {
		return errors.New("this password appears on lists of the most commonly used passwords; pick something else")
	}
	if isSingleRepeatedRune(password) {
		return errors.New("a password made of one repeated character is not accepted")
	}
	return nil
}

// commonPasswords covers what automated attacks try first. A full breach-corpus
// check needs a network service, which a self-hosted panel cannot assume.
var commonPasswords = map[string]bool{
	"password":      true,
	"password1":     true,
	"password123":   true,
	"passw0rd":      true,
	"123456":        true,
	"123456789":     true,
	"1234567890":    true,
	"12345678":      true,
	"qwerty":        true,
	"qwertyuiop":    true,
	"iloveyou":      true,
	"admin":         true,
	"administrator": true,
	"letmein":       true,
	"welcome":       true,
	"monkey":        true,
	"dragon":        true,
	"football":      true,
	"baseball":      true,
	"sunshine":      true,
	"princess":      true,
	"changeme":      true,
	"secret":        true,
	"abc123":        true,
	"111111":        true,
	"000000":        true,
	"qwerty123":     true,
	"1q2w3e4r":      true,
	"zaq12wsx":      true,
	"trustno1":      true,
	"superman":      true,
	"starwars":      true,
	"whatever":      true,
	"password1234":  true,
	"kubernetes":    true,
	"docker":        true,
	"skifity":       true,
}

func isCommonPassword(password string) bool {
	return commonPasswords[strings.ToLower(strings.TrimSpace(password))]
}

func isSingleRepeatedRune(password string) bool {
	runes := []rune(password)
	if len(runes) == 0 {
		return true
	}
	first := runes[0]
	for _, r := range runes[1:] {
		if r != first {
			return false
		}
	}
	return !unicode.IsControl(first)
}

// HashToken returns the stored form of a session or API token. Tokens have full
// entropy already, so a plain SHA-256 is right: it is fast enough to run on
// every request and there is nothing to brute force.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
