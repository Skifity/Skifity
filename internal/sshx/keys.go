package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// KeyPair is an SSH key the panel owns.
type KeyPair struct {
	// PrivateKey is an OpenSSH-format PEM block.
	PrivateKey string
	// PublicKey is the authorized_keys line.
	PublicKey string
	// Fingerprint identifies the key in the UI.
	Fingerprint string
}

// GenerateKeyPair creates an Ed25519 key for the panel to use on one server.
//
// Ed25519 rather than RSA: short, fast, supported by every current OpenSSH, and
// with no key size decision to get wrong. One key per server rather than one
// shared key means a compromised server does not hand over access to the others.
func GenerateKeyPair(comment string) (KeyPair, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate an SSH key: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(private, comment)
	if err != nil {
		return KeyPair{}, fmt.Errorf("encode the private key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return KeyPair{}, fmt.Errorf("build a signer: %w", err)
	}
	_ = public

	authorized := ssh.MarshalAuthorizedKey(signer.PublicKey())
	line := string(authorized[:len(authorized)-1]) // drop the trailing newline
	if comment != "" {
		line += " " + comment
	}

	return KeyPair{
		PrivateKey:  string(pem.EncodeToMemory(block)),
		PublicKey:   line,
		Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}, nil
}

// PublicKeyOf derives the authorized_keys line from a private key.
func PublicKeyOf(privateKey, passphrase string) (string, error) {
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(privateKey))
	}
	if err != nil {
		return "", fmt.Errorf("read the private key: %w", err)
	}
	authorized := ssh.MarshalAuthorizedKey(signer.PublicKey())
	return string(authorized[:len(authorized)-1]), nil
}
