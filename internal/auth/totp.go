package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP, RFC 6238, implemented here rather than pulled in as a dependency: it is
// sixty lines, every authenticator app expects exactly SHA-1 / 6 digits / 30
// seconds, and a dependency for this is a dependency to audit.

const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew is how many periods either side of now are accepted, which covers
	// a phone clock that is up to 30 seconds off.
	totpSkew = 1
	// totpSecretLen is 20 bytes, the RFC 4226 recommendation for SHA-1.
	totpSecretLen = 20
)

// ErrInvalidTOTP is returned when a code does not match.
var ErrInvalidTOTP = errors.New("that code is not correct")

// GenerateTOTPSecret returns a new base32 secret to show as a QR code.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, totpSecretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// TOTPURI builds the otpauth:// URI an authenticator app scans.
func TOTPURI(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(int(totpPeriod.Seconds())))
	// The label is "Issuer:account" and both halves must be escaped.
	label := url.PathEscape(issuer + ":" + account)
	return "otpauth://totp/" + label + "?" + v.Encode()
}

// TOTPCode computes the code for a secret at a point in time.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return totpAt(key, uint64(at.Unix())/uint64(totpPeriod.Seconds())), nil
}

// VerifyTOTP checks a user-supplied code, allowing for a little clock drift,
// and returns the counter the code belongs to.
//
// The counter is what makes a code single-use. RFC 6238 says a one-time
// password must be accepted once, and the window here is three steps wide, so
// without recording which step was spent a code read off somebody's screen
// stays valid for up to ninety seconds. The caller stores it; this function
// stays pure.
func VerifyTOTP(secret, code string, at time.Time) (uint64, error) {
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	if len(code) != totpDigits {
		return 0, ErrInvalidTOTP
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0, err
	}
	counter := uint64(at.Unix()) / uint64(totpPeriod.Seconds())
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		candidate := uint64(int64(counter) + int64(delta))
		// Constant time so a timing side channel cannot reveal digits.
		if subtle.ConstantTimeCompare([]byte(totpAt(key, candidate)), []byte(code)) == 1 {
			return candidate, nil
		}
	}
	return 0, ErrInvalidTOTP
}

func totpAt(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 section 5.3.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for range totpDigits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod)
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	// Authenticator apps show secrets in groups and uppercase; accept both.
	normalised := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(normalised, "="))
	if err != nil {
		return nil, fmt.Errorf("two-factor secret is not valid base32: %w", err)
	}
	if len(key) == 0 {
		return nil, errors.New("two-factor secret is empty")
	}
	return key, nil
}

// GenerateRecoveryCodes returns single-use codes for when a phone is lost.
//
// Twelve base32 characters is sixty bits. That is far more than an online
// guess needs to be hopeless, and it is chosen for the other case: a stolen
// database, where the only thing between an attacker and somebody's second
// factor is how long the code takes to find.
func GenerateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for range n {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("read random bytes: %w", err)
		}
		// Twelve characters in threes, because a code that has to be typed off
		// a piece of paper is typed wrong when it is one long run.
		s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
		codes = append(codes, s[0:4]+"-"+s[4:8]+"-"+s[8:12])
	}
	return codes, nil
}

// NormaliseRecoveryCode puts a typed code into the one form that is hashed.
//
// People type these off paper, so they arrive with the dashes left out, with
// spaces in, and in whichever case their keyboard was in.
func NormaliseRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HashRecoveryCode is what is stored. A recovery code has full entropy of its
// own, so it is hashed like a token rather than like a password.
func HashRecoveryCode(code string) string {
	return HashToken("recovery:" + NormaliseRecoveryCode(code))
}
