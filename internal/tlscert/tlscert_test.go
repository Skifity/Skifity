package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/tlscert/certtest"
)

// A company CA, its intermediate, and a leaf for the team's hostnames: the
// shape a certificate somebody brings usually has.
type bundle struct {
	root, intermediate, leaf certtest.Issued
}

func newBundle(t *testing.T, opts certtest.Options) bundle {
	t.Helper()
	root := certtest.Root(t, "Example Root CA")
	intermediate := certtest.Issue(t, root, certtest.Options{CommonName: "Example Issuing CA", IsCA: true})
	if opts.CommonName == "" {
		opts.CommonName = "shop.example.com"
	}
	if opts.DNSNames == nil {
		opts.DNSNames = []string{"shop.example.com", "*.shop.example.com"}
	}
	return bundle{root: root, intermediate: intermediate, leaf: certtest.Issue(t, intermediate, opts)}
}

// refusedWith checks a Parse failed with this code.
func refusedWith(t *testing.T, err error, code string) *errdoc.Problem {
	t.Helper()
	var problem *errdoc.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("want the refusal %s, got %v", code, err)
	}
	if problem.Code != code {
		t.Fatalf("refused with %s (%s), want %s", problem.Code, problem.Cause, code)
	}
	if problem.Cause == "" || problem.Fix == "" || problem.DocsPath == "" {
		t.Errorf("%s does not say why and what to do: %+v", code, problem)
	}
	return problem
}

func TestACertificateInOrderIsReadAndDescribed(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	parsed, err := Parse(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM, time.Now())
	if err != nil {
		t.Fatalf("a good certificate was refused: %v", err)
	}
	if parsed.Reordered {
		t.Error("a chain given leaf first was reported as reordered")
	}
	if strings.Join(parsed.Hostnames, ",") != "shop.example.com,*.shop.example.com" {
		t.Errorf("hostnames are %v", parsed.Hostnames)
	}
	if parsed.Subject != "shop.example.com" || parsed.Issuer != "Example Issuing CA" {
		t.Errorf("subject %q, issuer %q", parsed.Subject, parsed.Issuer)
	}
	if parsed.KeyType != "ECDSA P-256" || parsed.SelfSigned || parsed.ChainLength != 2 {
		t.Errorf("key %q, self-signed %v, chain %d", parsed.KeyType, parsed.SelfSigned, parsed.ChainLength)
	}
	if !parsed.NotAfter.Equal(b.leaf.Cert.NotAfter) || !parsed.NotBefore.Equal(b.leaf.Cert.NotBefore) {
		t.Errorf("dates are %s to %s", parsed.NotBefore, parsed.NotAfter)
	}
	// openssl's spelling: 32 bytes, upper-case hex, colons between.
	if len(parsed.Fingerprint) != 32*3-1 || strings.ToUpper(parsed.Fingerprint) != parsed.Fingerprint ||
		strings.Count(parsed.Fingerprint, ":") != 31 {
		t.Errorf("fingerprint is %q", parsed.Fingerprint)
	}
	if !strings.HasPrefix(parsed.KeyPEM, "-----BEGIN PRIVATE KEY-----") {
		t.Errorf("the key is not PKCS #8: %.40q", parsed.KeyPEM)
	}
}

// The order a bundle is pasted in is not a reason to refuse it: it is put
// right, and the answer says so.
func TestAChainInTheWrongOrderIsPutRight(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	parsed, err := Parse(certtest.Chain(b.root, b.intermediate, b.leaf), b.leaf.KeyPEM, time.Now())
	if err != nil {
		t.Fatalf("a chain in the wrong order was refused rather than reordered: %v", err)
	}
	if !parsed.Reordered || parsed.ChainLength != 3 {
		t.Fatalf("reordered %v, chain %d", parsed.Reordered, parsed.ChainLength)
	}
	var order []string
	for rest := []byte(parsed.ChainPEM); ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, cert.Subject.CommonName)
	}
	if strings.Join(order, " < ") != "shop.example.com < Example Issuing CA < Example Root CA" {
		t.Fatalf("the chain is %v", order)
	}
}

// A certificate that is not part of the leaf's chain cannot be put in order,
// and is named.
func TestACertificateThatIsNotPartOfTheChainIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	stranger := certtest.Root(t, "Somebody Else's CA")
	err := func() error {
		_, err := Parse(certtest.Chain(b.leaf, b.intermediate, stranger), b.leaf.KeyPEM, time.Now())
		return err
	}()
	problem := refusedWith(t, err, "certificate.chain_broken")
	if !strings.Contains(problem.Cause, "Somebody Else's CA") {
		t.Errorf("the refusal does not name the stray certificate: %s", problem.Cause)
	}
}

func TestAKeyThatBelongsToAnotherCertificateIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	other := certtest.SelfSigned(t, certtest.Options{CommonName: "other.example.com", DNSNames: []string{"other.example.com"}})
	_, err := Parse(certtest.Chain(b.leaf, b.intermediate), other.KeyPEM, time.Now())
	refusedWith(t, err, "certificate.key_mismatch")
}

func TestAnExpiredCertificateIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{
		NotBefore: time.Now().Add(-100 * 24 * time.Hour), NotAfter: time.Now().Add(-24 * time.Hour),
	})
	_, err := Parse(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM, time.Now())
	refusedWith(t, err, "certificate.expired")
}

func TestACertificateThatIsNotValidYetIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{
		NotBefore: time.Now().Add(48 * time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
	})
	_, err := Parse(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM, time.Now())
	refusedWith(t, err, "certificate.not_yet_valid")
}

func TestKeysBrowsersDoNotAcceptAreRefused(t *testing.T) {
	for name, key := range map[string]func(*testing.T) certtest.Options{
		"RSA 1024":    func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.RSA(t, 1024)} },
		"ECDSA P-521": func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.ECDSA(t, elliptic.P521())} },
		"ECDSA P-224": func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.ECDSA(t, elliptic.P224())} },
	} {
		t.Run(name, func(t *testing.T) {
			b := newBundle(t, key(t))
			_, err := Parse(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM, time.Now())
			problem := refusedWith(t, err, "certificate.weak_key")
			if !strings.Contains(problem.Cause, name) {
				t.Errorf("the refusal does not say which key it is: %s", problem.Cause)
			}
		})
	}
}

func TestKeysBrowsersAcceptAreAccepted(t *testing.T) {
	for name, key := range map[string]func(*testing.T) certtest.Options{
		"RSA 2048":    func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.RSA(t, 2048)} },
		"ECDSA P-256": func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.ECDSA(t, elliptic.P256())} },
		"ECDSA P-384": func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.ECDSA(t, elliptic.P384())} },
		"Ed25519":     func(t *testing.T) certtest.Options { return certtest.Options{Key: certtest.Ed25519(t)} },
	} {
		t.Run(name, func(t *testing.T) {
			b := newBundle(t, key(t))
			parsed, err := Parse(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM, time.Now())
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if parsed.KeyType != name {
				t.Errorf("the key is described as %q", parsed.KeyType)
			}
		})
	}
}

// PKCS #1 and SEC 1, which openssl writes by default for RSA and EC keys, are
// read like PKCS #8, and an EC key with its parameters block ahead of it too.
func TestTheKeyEncodingsOpenSSLWritesAreRead(t *testing.T) {
	rsaBundle := newBundle(t, certtest.Options{Key: certtest.RSA(t, 2048)})
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(mustRSA(t, rsaBundle.leaf))})
	if _, err := Parse(rsaBundle.leaf.CertPEM, pkcs1, time.Now()); err != nil {
		t.Errorf("a PKCS #1 key was refused: %v", err)
	}

	ecBundle := newBundle(t, certtest.Options{})
	sec1, err := x509.MarshalECPrivateKey(mustEC(t, ecBundle.leaf))
	if err != nil {
		t.Fatal(err)
	}
	withParameters := append(pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{6, 8, 42, 134, 72, 206, 61, 3, 1, 7}}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})...)
	if _, err := Parse(ecBundle.leaf.CertPEM, withParameters, time.Now()); err != nil {
		t.Errorf("a SEC 1 key with its parameters was refused: %v", err)
	}
}

func TestSomethingThatIsNotACertificateIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	for name, chain := range map[string][]byte{
		"empty":         nil,
		"text":          []byte("this is my certificate"),
		"a key instead": b.leaf.KeyPEM,
		"garbage in a certificate block": pem.EncodeToMemory(&pem.Block{
			Type: "CERTIFICATE", Bytes: []byte("not DER at all")}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(chain, b.leaf.KeyPEM, time.Now())
			refusedWith(t, err, "certificate.unreadable")
		})
	}
}

func TestAKeyThatCannotBeReadIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	encrypted := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1, 2, 3}})
	legacyEncrypted := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00"}, Bytes: []byte{1, 2, 3}})
	for name, key := range map[string][]byte{
		"missing":                nil,
		"a certificate":          b.leaf.CertPEM,
		"encrypted, PKCS #8":     encrypted,
		"encrypted, the old way": legacyEncrypted,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(certtest.Chain(b.leaf, b.intermediate), key, time.Now())
			refusedWith(t, err, "certificate.key_unreadable")
		})
	}
}

func TestACertificateWithNoDNSNamesIsRefused(t *testing.T) {
	b := newBundle(t, certtest.Options{DNSNames: []string{}})
	_, err := Parse(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM, time.Now())
	refusedWith(t, err, "certificate.no_hostnames")
}

// A combined PEM file — certificate and key in one — pasted into the
// certificate box: the key is used when its own box is empty, and never kept
// in the chain, which is stored in the clear.
func TestAKeyPastedWithTheCertificateIsUsedAndNeverKeptInTheChain(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	combined := append(certtest.Chain(b.leaf, b.intermediate), b.leaf.KeyPEM...)
	parsed, err := Parse(combined, nil, time.Now())
	if err != nil {
		t.Fatalf("a combined file was refused: %v", err)
	}
	if strings.Contains(parsed.ChainPEM, "PRIVATE KEY") {
		t.Fatal("the private key is in the chain, which is stored in the clear")
	}
	if parsed.KeyPEM == "" {
		t.Fatal("the key pasted with the certificate was not used")
	}

	// With the key in its own box as well, the one in the chain is still
	// not kept.
	parsed, err = Parse(combined, b.leaf.KeyPEM, time.Now())
	if err != nil || strings.Contains(parsed.ChainPEM, "PRIVATE KEY") {
		t.Fatalf("the chain keeps a key (%v)", err)
	}
}

// A self-signed certificate is accepted — a company's machines may trust it —
// and said to be one.
func TestASelfSignedCertificateIsAcceptedAndSaidToBeOne(t *testing.T) {
	cert := certtest.SelfSigned(t, certtest.Options{CommonName: "intranet.example.com", DNSNames: []string{"intranet.example.com"}})
	parsed, err := Parse(cert.CertPEM, cert.KeyPEM, time.Now())
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if !parsed.SelfSigned || parsed.ChainLength != 1 {
		t.Errorf("self-signed %v, chain %d", parsed.SelfSigned, parsed.ChainLength)
	}
}

func TestTheSameCertificateTwiceIsOneLink(t *testing.T) {
	b := newBundle(t, certtest.Options{})
	parsed, err := Parse(certtest.Chain(b.leaf, b.intermediate, b.intermediate), b.leaf.KeyPEM, time.Now())
	if err != nil || parsed.ChainLength != 2 {
		t.Fatalf("chain %d (%v)", parsed.ChainLength, err)
	}
}

func mustRSA(t *testing.T, issued certtest.Issued) *rsa.PrivateKey {
	t.Helper()
	key, ok := issued.Key.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("not an RSA key: %T", issued.Key)
	}
	return key
}

func mustEC(t *testing.T, issued certtest.Issued) *ecdsa.PrivateKey {
	t.Helper()
	key, ok := issued.Key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("not an EC key: %T", issued.Key)
	}
	return key
}
