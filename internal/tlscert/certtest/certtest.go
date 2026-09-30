// Package certtest makes certificates for tests: a root, intermediates and
// leaves with whatever names, dates and keys a test needs, so every refusal in
// tlscert can be tested against a real certificate rather than a fixture file
// that expires.
//
// Everything here is made fresh and thrown away. Nothing is trusted by
// anything outside the test that made it.
package certtest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// Issued is a certificate and the key it was issued for.
type Issued struct {
	Cert *x509.Certificate
	Key  crypto.Signer
	// CertPEM and KeyPEM are both PEM; the key is PKCS #8.
	CertPEM []byte
	KeyPEM  []byte
}

// Options describe a certificate to make. Zero values are a leaf valid from an
// hour ago for 90 days, on a new ECDSA P-256 key.
type Options struct {
	CommonName string
	DNSNames   []string
	NotBefore  time.Time
	NotAfter   time.Time
	Key        crypto.Signer
	IsCA       bool
}

// Root makes a self-signed certificate authority.
func Root(t testing.TB, name string) Issued {
	t.Helper()
	return issue(t, nil, Options{CommonName: name, IsCA: true})
}

// Issue makes a certificate signed by parent.
func Issue(t testing.TB, parent Issued, opts Options) Issued {
	t.Helper()
	return issue(t, &parent, opts)
}

// SelfSigned makes a leaf that signed itself, the way openssl req -x509 does.
func SelfSigned(t testing.TB, opts Options) Issued {
	t.Helper()
	return issue(t, nil, opts)
}

func issue(t testing.TB, parent *Issued, opts Options) Issued {
	t.Helper()
	key := opts.Key
	if key == nil {
		key = ECDSA(t, elliptic.P256())
	}
	if opts.NotBefore.IsZero() {
		opts.NotBefore = time.Now().Add(-time.Hour)
	}
	if opts.NotAfter.IsZero() {
		opts.NotAfter = opts.NotBefore.Add(90 * 24 * time.Hour)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: opts.CommonName, Organization: []string{"Example Test"}},
		DNSNames:              opts.DNSNames,
		NotBefore:             opts.NotBefore,
		NotAfter:              opts.NotAfter,
		BasicConstraintsValid: true,
		IsCA:                  opts.IsCA,
	}
	if opts.IsCA {
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		template.KeyUsage = x509.KeyUsageDigitalSignature
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	signerCert, signerKey := template, key
	if parent != nil {
		signerCert, signerKey = parent.Cert, parent.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signerCert, key.Public(), signerKey)
	if err != nil {
		t.Fatalf("create certificate %q: %v", opts.CommonName, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}
	return Issued{
		Cert:    cert,
		Key:     key,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}),
	}
}

// ECDSA makes an ECDSA key on a curve.
func ECDSA(t testing.TB, curve elliptic.Curve) crypto.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	return key
}

// RSA makes an RSA key of a size.
func RSA(t testing.TB, bits int) crypto.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	return key
}

// Ed25519 makes an Ed25519 key.
func Ed25519(t testing.TB) crypto.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 key: %v", err)
	}
	return key
}

// Chain joins certificates' PEM in the order given.
func Chain(certs ...Issued) []byte {
	var out []byte
	for _, cert := range certs {
		out = append(out, cert.CertPEM...)
	}
	return out
}
