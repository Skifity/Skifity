package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// A software authenticator: what a phone or a security key does, written out
// in the test, so that adding a passkey and signing in with one can run through
// the real handlers with nothing faked on the panel's side.
//
// It is deliberately built from the specification rather than from the
// library the panel verifies with. An authenticator made of the verifier's own
// encoder would agree with the verifier about everything, including its
// mistakes. So the CBOR here is hand-written, the authenticator data is laid
// out byte by byte, and the signature is ECDSA P-256 over SHA-256, DER-encoded,
// the way a real one produces it.

// Authenticator data flags, WebAuthn §6.1.
const (
	flagUserPresent    byte = 0x01
	flagUserVerified   byte = 0x04
	flagBackupEligible byte = 0x08
	flagBackupState    byte = 0x10
	flagAttestedData   byte = 0x40
)

// softAuthenticator holds one credential, as a platform authenticator would.
type softAuthenticator struct {
	key          *ecdsa.PrivateKey
	credentialID []byte
	// userHandle is what the relying party gave at registration, and what the
	// authenticator hands back at every sign-in.
	userHandle []byte
	// counter is the signature counter, and counts says whether it moves: a
	// security key counts, a synced passkey leaves it at zero for ever.
	counter uint32
	counts  bool
	// flags are the flags it sets besides user presence: user verification,
	// and backup eligibility for a synced passkey.
	flags byte
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a key: %v", err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &softAuthenticator{key: key, credentialID: id, flags: flagUserVerified}
}

// ceremony is what a browser would do to an answer on the way out: which
// origin it says the page was on, and which relying party it hashes. A test
// changes one of them to be the browser, or the attacker, that gets it wrong.
type ceremony struct {
	origin string
	rpID   string
	// flags replaces the authenticator's own flags when non-zero.
	flags byte
	// counter replaces the next counter when set.
	counter *uint32
	// signWith signs with another key, as a forged answer would.
	signWith *ecdsa.PrivateKey
	// userHandle replaces the one the authenticator was given.
	userHandle []byte
	// kind replaces the client data type.
	kind string
}

// creationOptions are the parts of the panel's registration options the
// authenticator reads.
type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rp"`
		User struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
		AuthenticatorSelection struct {
			ResidentKey        string `json:"residentKey"`
			RequireResidentKey bool   `json:"requireResidentKey"`
			UserVerification   string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Attestation        string `json:"attestation"`
		ExcludeCredentials []struct {
			ID string `json:"id"`
		} `json:"excludeCredentials"`
		Params []struct {
			Alg int `json:"alg"`
		} `json:"pubKeyCredParams"`
		Timeout int `json:"timeout"`
	} `json:"publicKey"`
}

// requestOptions are the parts of the sign-in options the authenticator reads.
type requestOptions struct {
	PublicKey struct {
		Challenge        string `json:"challenge"`
		RPID             string `json:"rpId"`
		UserVerification string `json:"userVerification"`
		AllowCredentials []any  `json:"allowCredentials"`
		Timeout          int    `json:"timeout"`
	} `json:"publicKey"`
}

var b64 = base64.RawURLEncoding

// create makes the credential and answers the registration options, as
// navigator.credentials.create would, with "none" attestation.
func (a *softAuthenticator) create(t *testing.T, options creationOptions, c ceremony) map[string]any {
	t.Helper()
	handle, err := b64.DecodeString(options.PublicKey.User.ID)
	if err != nil {
		t.Fatalf("the user id is not base64url: %v", err)
	}
	a.userHandle = handle

	rpID := options.PublicKey.RP.ID
	if c.rpID != "" {
		rpID = c.rpID
	}
	flags := flagUserPresent | a.flags
	if c.flags != 0 {
		flags = c.flags
	}
	counter := a.counter
	if c.counter != nil {
		counter = *c.counter
		a.counter = counter
	}

	// Attested credential data: an AAGUID of zeros, which is what a
	// "none" attestation carries, the credential id and the COSE key.
	var attested bytes.Buffer
	attested.Write(make([]byte, 16))
	_ = binary.Write(&attested, binary.BigEndian, uint16(len(a.credentialID)))
	attested.Write(a.credentialID)
	attested.Write(coseKey(t, &a.key.PublicKey))

	authData := authenticatorData(rpID, flags|flagAttestedData, counter)
	authData = append(authData, attested.Bytes()...)

	// {"fmt": "none", "attStmt": {}, "authData": ...}, in CTAP2's canonical
	// order: shorter keys first.
	var object bytes.Buffer
	object.WriteByte(0xa3)
	cborText(&object, "fmt")
	cborText(&object, "none")
	cborText(&object, "attStmt")
	object.WriteByte(0xa0)
	cborText(&object, "authData")
	cborBytes(&object, authData)

	kind := "webauthn.create"
	if c.kind != "" {
		kind = c.kind
	}
	return map[string]any{
		"id":    b64.EncodeToString(a.credentialID),
		"rawId": b64.EncodeToString(a.credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData(t, kind, options.PublicKey.Challenge, c.origin)),
			"attestationObject": b64.EncodeToString(object.Bytes()),
			"transports":        []string{"internal", "hybrid"},
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	}
}

// get answers sign-in options with this credential, as
// navigator.credentials.get would once the person picked it.
func (a *softAuthenticator) get(t *testing.T, options requestOptions, c ceremony) map[string]any {
	t.Helper()
	rpID := options.PublicKey.RPID
	if c.rpID != "" {
		rpID = c.rpID
	}
	flags := flagUserPresent | a.flags
	if c.flags != 0 {
		flags = c.flags
	}
	// A counting authenticator moves its counter on every signature.
	if a.counts {
		a.counter++
	}
	if c.counter != nil {
		a.counter = *c.counter
	}
	authData := authenticatorData(rpID, flags, a.counter)

	kind := "webauthn.get"
	if c.kind != "" {
		kind = c.kind
	}
	data := clientData(t, kind, options.PublicKey.Challenge, c.origin)
	hash := sha256.Sum256(data)
	signed := sha256.Sum256(append(append([]byte{}, authData...), hash[:]...))
	key := a.key
	if c.signWith != nil {
		key = c.signWith
	}
	signature, err := ecdsa.SignASN1(rand.Reader, key, signed[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	handle := a.userHandle
	if c.userHandle != nil {
		handle = c.userHandle
	}
	return map[string]any{
		"id":    b64.EncodeToString(a.credentialID),
		"rawId": b64.EncodeToString(a.credentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(data),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(signature),
			"userHandle":        b64.EncodeToString(handle),
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	}
}

// authenticatorData lays out §6.1: the hash of the relying party id, the
// flags, and the counter, big-endian.
func authenticatorData(rpID string, flags byte, counter uint32) []byte {
	hash := sha256.Sum256([]byte(rpID))
	out := append([]byte{}, hash[:]...)
	out = append(out, flags)
	return binary.BigEndian.AppendUint32(out, counter)
}

// clientData is the JSON the browser signs over: the ceremony, the challenge
// exactly as the relying party sent it, and the origin of the page.
func clientData(t *testing.T, kind, challenge, origin string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// coseKey is an EC2 public key in COSE (RFC 9052): kty 2, alg -7 (ES256),
// crv 1 (P-256), and the coordinates, in CTAP2's canonical key order.
func coseKey(t *testing.T, key *ecdsa.PublicKey) []byte {
	t.Helper()
	// The uncompressed point, SEC 1: 0x04, then x and y, 32 bytes each.
	point, err := key.Bytes()
	if err != nil || len(point) != 65 || point[0] != 0x04 {
		t.Fatalf("encode the public key: %v", err)
	}
	var out bytes.Buffer
	out.WriteByte(0xa5)
	cborInt(&out, 1)
	cborInt(&out, 2)
	cborInt(&out, 3)
	cborInt(&out, -7)
	cborInt(&out, -1)
	cborInt(&out, 1)
	cborInt(&out, -2)
	cborBytes(&out, point[1:33])
	cborInt(&out, -3)
	cborBytes(&out, point[33:65])
	return out.Bytes()
}

// The three CBOR items this needs, RFC 8949 §3.

func cborHead(out *bytes.Buffer, major byte, n uint64) {
	switch {
	case n < 24:
		out.WriteByte(major<<5 | byte(n))
	case n < 1<<8:
		out.WriteByte(major<<5 | 24)
		out.WriteByte(byte(n))
	case n < 1<<16:
		out.WriteByte(major<<5 | 25)
		_ = binary.Write(out, binary.BigEndian, uint16(n))
	default:
		out.WriteByte(major<<5 | 26)
		_ = binary.Write(out, binary.BigEndian, uint32(n))
	}
}

func cborInt(out *bytes.Buffer, n int) {
	if n >= 0 {
		cborHead(out, 0, uint64(n))
		return
	}
	cborHead(out, 1, uint64(-1-n))
}

func cborBytes(out *bytes.Buffer, b []byte) {
	cborHead(out, 2, uint64(len(b)))
	out.Write(b)
}

func cborText(out *bytes.Buffer, s string) {
	cborHead(out, 3, uint64(len(s)))
	out.WriteString(s)
}
