// Package tlscert reads a certificate somebody brings instead of the one Let's
// Encrypt would issue, and decides which of a team's certificates a hostname is
// served with.
//
// It is pure: PEM in, a description or a documented refusal out. Nothing here
// stores, seals or logs anything, which is what lets every refusal be tested
// with nothing but a certificate made in the test.
//
// What it refuses, and why each one is a refusal rather than a warning:
//
//   - Something that is not a certificate, or a key that is not a key: nothing
//     could be served.
//   - A key that does not belong to the certificate: TLS fails on the first
//     connection, for every visitor.
//   - Certificates that do not form one chain: browsers are sent something
//     they cannot build a path through. The order is repaired when it can be,
//     because "intermediate first" is the most common way a bundle is pasted.
//   - A certificate that has expired, or is not valid yet: every browser
//     refuses it.
//   - A weak key: RSA below 2048 bits is refused by browsers and by the CAs
//     that issue certificates; ECDSA on anything but P-256 or P-384 is not
//     offered by every browser either.
//   - A certificate with no DNS names: browsers have ignored the Common Name
//     for years, so it would match nothing.
package tlscert

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"
	"time"

	"skifity/internal/errdoc"
)

// Parsed is a certificate that can be served, as the panel stores it.
type Parsed struct {
	// ChainPEM is every certificate given, leaf first, each re-encoded from
	// its DER. Nothing else that was pasted survives into it — a private key
	// pasted into the wrong box above all, which would otherwise be stored in
	// the clear.
	ChainPEM string
	// KeyPEM is the private key, re-encoded as PKCS #8. It is the one part
	// that is secret, and the caller seals it before it goes anywhere.
	KeyPEM string

	// Hostnames are the leaf's DNS names, lower case, wildcards as written.
	Hostnames []string
	// Subject is the leaf's Common Name, or its first DNS name when it has
	// none.
	Subject string
	// Issuer is who signed the leaf, by Common Name when it has one.
	Issuer    string
	NotBefore time.Time
	NotAfter  time.Time
	// Fingerprint is the SHA-256 of the leaf, written the way
	// `openssl x509 -noout -fingerprint -sha256` writes it, so the two can be
	// compared by eye.
	Fingerprint string
	// KeyType is "RSA 2048", "ECDSA P-256" or "Ed25519".
	KeyType string
	// SelfSigned is a leaf that signed itself. It is accepted — a company's
	// own machines may trust it — and said, because a browser will not.
	SelfSigned bool
	// ChainLength is how many certificates the chain holds, the leaf included.
	ChainLength int
	// Reordered says the certificates were given in another order and were
	// put leaf first.
	Reordered bool
}

// MaxPEMBytes bounds what is read. A chain of a leaf, two intermediates and a
// root is under 8 KB; this is generous without being a way to fill the
// database.
const MaxPEMBytes = 64 << 10

// Parse reads a certificate chain and its private key.
//
// A private key pasted into the chain rather than into its own box is used
// when the key box is empty — a combined PEM file is how some tools write
// one — and is never kept in the chain either way.
func Parse(chainPEM, keyPEM []byte, now time.Time) (Parsed, error) {
	if len(chainPEM) > MaxPEMBytes || len(keyPEM) > MaxPEMBytes {
		return Parsed{}, errdoc.CertificateUnreadable(fmt.Sprintf("more than %d KB was pasted", MaxPEMBytes>>10))
	}
	certs, strayKey, err := readCertificates(chainPEM)
	if err != nil {
		return Parsed{}, err
	}
	if len(bytes.TrimSpace(keyPEM)) == 0 {
		keyPEM = strayKey
	}
	key, err := readKey(keyPEM)
	if err != nil {
		return Parsed{}, err
	}

	leaf, ok := leafFor(certs, key)
	if !ok {
		return Parsed{}, errdoc.CertificateKeyMismatch(nameOf(certs[0]))
	}
	chain, stray := order(certs, leaf)
	if stray != nil {
		return Parsed{}, errdoc.CertificateChainBroken(nameOf(stray), nameOf(leaf))
	}

	switch {
	case now.After(leaf.NotAfter):
		return Parsed{}, errdoc.CertificateExpired(nameOf(leaf), leaf.NotAfter.UTC().Format(time.DateOnly))
	case now.Before(leaf.NotBefore):
		return Parsed{}, errdoc.CertificateNotYetValid(nameOf(leaf), leaf.NotBefore.UTC().Format(time.DateTime)+" UTC")
	}
	keyType, strong := describeKey(key)
	if !strong {
		return Parsed{}, errdoc.CertificateWeakKey(keyType)
	}
	hostnames := dnsNames(leaf)
	if len(hostnames) == 0 {
		return Parsed{}, errdoc.CertificateNoHostnames(nameOf(leaf))
	}

	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Parsed{}, errdoc.CertificateKeyUnreadable()
	}
	var encodedChain strings.Builder
	for _, cert := range chain {
		encodedChain.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	}

	sum := sha256.Sum256(leaf.Raw)
	return Parsed{
		ChainPEM:    encodedChain.String(),
		KeyPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})),
		Hostnames:   hostnames,
		Subject:     nameOf(leaf),
		Issuer:      issuerOf(leaf),
		NotBefore:   leaf.NotBefore.UTC(),
		NotAfter:    leaf.NotAfter.UTC(),
		Fingerprint: fingerprint(sum[:]),
		KeyType:     keyType,
		SelfSigned:  selfSigned(leaf),
		ChainLength: len(chain),
		Reordered:   !sameOrder(certs, chain),
	}, nil
}

// readCertificates returns every certificate in the input, each once, and a
// private key block if one was pasted among them.
func readCertificates(input []byte) ([]*x509.Certificate, []byte, error) {
	var certs []*x509.Certificate
	var strayKey []byte
	seen := map[string]bool{}
	rest := input
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, nil, errdoc.CertificateUnreadable(err.Error())
			}
			// The same certificate twice is a bundle concatenated with
			// itself, not a second link in the chain.
			if seen[string(cert.Raw)] {
				continue
			}
			seen[string(cert.Raw)] = true
			certs = append(certs, cert)
		case strings.HasSuffix(block.Type, "PRIVATE KEY") && strayKey == nil:
			strayKey = pem.EncodeToMemory(block)
		}
	}
	if len(certs) == 0 {
		return nil, nil, errdoc.CertificateUnreadable("no block beginning -----BEGIN CERTIFICATE----- was found")
	}
	return certs, strayKey, nil
}

// readKey reads one private key in any of the three encodings OpenSSL writes.
// An encrypted one is refused: the panel would need its passphrase every time
// it wrote the key into the cluster.
func readKey(input []byte) (crypto.Signer, error) {
	rest := input
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errdoc.CertificateKeyUnreadable()
		}
		if !strings.HasSuffix(block.Type, "PRIVATE KEY") {
			// "EC PARAMETERS" ahead of an EC key is what `openssl ecparam
			// -genkey` writes, and is not the key.
			continue
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || block.Headers["Proc-Type"] != "" {
			return nil, errdoc.CertificateKeyUnreadable()
		}
		var key any
		var err error
		switch block.Type {
		case "RSA PRIVATE KEY":
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			key, err = x509.ParseECPrivateKey(block.Bytes)
		default:
			key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		}
		if err != nil {
			return nil, errdoc.CertificateKeyUnreadable()
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, errdoc.CertificateKeyUnreadable()
		}
		return signer, nil
	}
}

// leafFor finds the certificate the key belongs to. Two that share it — a
// renewal kept on the same key, pasted beside the old one — resolve to the one
// that is not a CA and lasts longer, and the other is then a stray.
func leafFor(certs []*x509.Certificate, key crypto.Signer) (*x509.Certificate, bool) {
	var best *x509.Certificate
	for _, cert := range certs {
		if !samePublicKey(cert.PublicKey, key.Public()) {
			continue
		}
		if best == nil || (best.IsCA && !cert.IsCA) ||
			(best.IsCA == cert.IsCA && cert.NotAfter.After(best.NotAfter)) {
			best = cert
		}
	}
	return best, best != nil
}

func samePublicKey(a, b crypto.PublicKey) bool {
	type equaler interface{ Equal(crypto.PublicKey) bool }
	key, ok := a.(equaler)
	return ok && key.Equal(b)
}

// order puts the chain leaf first, each certificate followed by the one that
// signed it. What is left over is not part of this chain, and the first such
// certificate is returned so the refusal can name it.
func order(certs []*x509.Certificate, leaf *x509.Certificate) ([]*x509.Certificate, *x509.Certificate) {
	remaining := slices.DeleteFunc(slices.Clone(certs), func(c *x509.Certificate) bool { return c == leaf })
	chain := []*x509.Certificate{leaf}
	for current := leaf; !selfSigned(current); {
		i := slices.IndexFunc(remaining, func(candidate *x509.Certificate) bool {
			return signedBy(current, candidate)
		})
		if i < 0 {
			break
		}
		current = remaining[i]
		chain = append(chain, current)
		remaining = slices.Delete(remaining, i, i+1)
	}
	if len(remaining) > 0 {
		return nil, remaining[0]
	}
	return chain, nil
}

func sameOrder(given, chain []*x509.Certificate) bool {
	return slices.EqualFunc(given, chain, func(a, b *x509.Certificate) bool { return a == b })
}

// selfSigned is a certificate that names itself as its issuer and whose
// signature its own key verifies: a root, or a certificate somebody made
// with openssl req -x509.
func selfSigned(cert *x509.Certificate) bool {
	return signedBy(cert, cert)
}

// signedBy reports whether parent's key made cert's signature, and parent is
// the issuer cert names.
//
// The signature alone, not x509's CheckSignatureFrom: that also asks the
// parent to be marked as a certificate authority, which a self-signed leaf is
// not, and which is the browser's to judge of an intermediate. A link that is
// there is put in its place, whatever else is true of it.
func signedBy(cert, parent *x509.Certificate) bool {
	return bytes.Equal(cert.RawIssuer, parent.RawSubject) &&
		parent.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}

// describeKey names a key and says whether it is one the panel serves.
func describeKey(key crypto.Signer) (string, bool) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		bits := k.N.BitLen()
		return fmt.Sprintf("RSA %d", bits), bits >= 2048
	case *ecdsa.PrivateKey:
		name := k.Curve.Params().Name
		return "ECDSA " + name, k.Curve == elliptic.P256() || k.Curve == elliptic.P384()
	case ed25519.PrivateKey:
		return "Ed25519", true
	}
	return fmt.Sprintf("%T", key), false
}

// dnsNames are the leaf's DNS names, lower case, without a trailing dot, each
// once. IP addresses are left out: a domain in the panel is a hostname.
func dnsNames(cert *x509.Certificate) []string {
	var out []string
	for _, name := range cert.DNSNames {
		name = Normalize(name)
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func nameOf(cert *x509.Certificate) string {
	if cert.Subject.CommonName != "" {
		return cert.Subject.CommonName
	}
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames[0]
	}
	return cert.Subject.String()
}

func issuerOf(cert *x509.Certificate) string {
	if cert.Issuer.CommonName != "" {
		return cert.Issuer.CommonName
	}
	return cert.Issuer.String()
}

func fingerprint(sum []byte) string {
	encoded := strings.ToUpper(hex.EncodeToString(sum))
	parts := make([]string, 0, len(encoded)/2)
	for i := 0; i < len(encoded); i += 2 {
		parts = append(parts, encoded[i:i+2])
	}
	return strings.Join(parts, ":")
}
