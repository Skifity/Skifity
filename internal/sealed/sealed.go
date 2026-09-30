// Package sealed is the format a backup is encrypted in: a passphrase, a key
// per file, and chunks that cannot be moved, dropped or cut short unnoticed.
//
// It is a package of its own, with nothing but the standard library and
// Argon2 beneath it, because three things need it that cannot all import the
// backup package: the backup jobs, the panel verifying a backup, and the
// command that restores the panel's own database.
package sealed

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/argon2"
)

// A sealed backup: encrypted and authenticated before it leaves the cluster.
//
// A backup is every row of a database, and it sits in a bucket somebody else
// runs. Encrypting it with the storage provider's own keys protects it from
// a stolen disk and from nobody else; this is encrypted with a passphrase the
// provider never sees. Cloudron, Dokploy and Dokku offer the same.
//
// The format is a header and a stream of chunks:
//
//	magic     8 bytes   "SKFSEAL1"
//	salt     16 bytes   for Argon2id, so each file has its own key
//	check    16 bytes   HMAC of a fixed string under the key, which is what
//	                    tells a wrong passphrase apart from a damaged file
//	prefix    7 bytes   the random part of every chunk's nonce
//	chunks              4-byte plaintext length, then the AES-256-GCM
//	                    ciphertext of at most 64 KiB and its tag
//
// Each nonce is the prefix, the chunk's number and a byte that is 1 for the
// last chunk only, and the header is authenticated with every chunk. So a
// chunk cannot be moved, dropped, repeated or appended, and a file cut short —
// the likeliest damage of all — is refused rather than restored as a shorter
// database: there is always a last chunk, empty if it has to be.

const (
	magic      = "SKFSEAL1"
	saltSize   = 16
	checkSize  = 16
	prefixSize = 7
	chunkSize  = 64 << 10
	HeaderSize = len(magic) + saltSize + checkSize + prefixSize

	// The Argon2id cost: once per file, so it can afford to be slow.
	kdfTime    = 3
	kdfMemory  = 64 * 1024
	kdfThreads = 2
)

// ErrWrongPassphrase is a sealed backup opened with another passphrase.
var ErrWrongPassphrase = errors.New("this backup was sealed with a different passphrase")

// ErrDamaged is a sealed backup that has been changed or cut short.
var ErrDamaged = errors.New("this backup is damaged: part of it is missing or was changed")

// IsSealed reports whether data starts like a sealed backup.
func IsSealed(start []byte) bool {
	return bytes.HasPrefix(start, []byte(magic))
}

func deriveKey(passphrase string, salt []byte) ([]byte, []byte) {
	key := argon2.IDKey([]byte(passphrase), salt, kdfTime, kdfMemory, kdfThreads, 32)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("skifity backup key check"))
	return key, mac.Sum(nil)[:checkSize]
}

func nonceFor(prefix []byte, counter uint32, last bool) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[prefixSize:], counter)
	if last {
		nonce[11] = 1
	}
	return nonce
}

// Seal encrypts src into dst under passphrase.
func Seal(dst io.Writer, src io.Reader, passphrase string) error {
	if passphrase == "" {
		return errors.New("there is no passphrase to seal the backup with")
	}
	header := make([]byte, HeaderSize)
	copy(header, magic)
	salt := header[len(magic) : len(magic)+saltSize]
	prefix := header[HeaderSize-prefixSize:]
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	key, check := deriveKey(passphrase, salt)
	copy(header[len(magic)+saltSize:], check)
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	if _, err := dst.Write(header); err != nil {
		return err
	}

	// One chunk is read ahead, because whether a chunk is the last is only
	// known once the next read comes back empty.
	current := make([]byte, chunkSize)
	next := make([]byte, chunkSize)
	n, err := io.ReadFull(src, current)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	var counter uint32
	for {
		m := 0
		if n == chunkSize {
			m, err = io.ReadFull(src, next)
			if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				return err
			}
		}
		last := m == 0
		if err := writeChunk(dst, aead, header, prefix, counter, last, current[:n]); err != nil {
			return err
		}
		if last {
			return nil
		}
		if counter == ^uint32(0) {
			return errors.New("the backup is too large to seal")
		}
		counter++
		current, next = next, current
		n = m
	}
}

func writeChunk(dst io.Writer, aead cipher.AEAD, header, prefix []byte, counter uint32, last bool, plain []byte) error {
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(plain)))
	sealed := aead.Seal(nil, nonceFor(prefix, counter, last), plain, header)
	if _, err := dst.Write(length); err != nil {
		return err
	}
	_, err := dst.Write(sealed)
	return err
}

// Open decrypts a sealed backup from src into dst. It returns
// ErrWrongPassphrase or ErrDamaged rather than writing anything it could not
// authenticate — but what it wrote before finding damage stays written, so a
// caller restoring from it should write to a file and use it only on success.
func Open(dst io.Writer, src io.Reader, passphrase string) error {
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(src, header); err != nil {
		return ErrDamaged
	}
	if !IsSealed(header) {
		return errors.New("this is not a sealed backup")
	}
	salt := header[len(magic) : len(magic)+saltSize]
	check := header[len(magic)+saltSize : len(magic)+saltSize+checkSize]
	prefix := header[HeaderSize-prefixSize:]
	key, want := deriveKey(passphrase, salt)
	if !hmac.Equal(check, want) {
		return ErrWrongPassphrase
	}
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}

	length := make([]byte, 4)
	buf := make([]byte, chunkSize+aead.Overhead())
	for counter := uint32(0); ; counter++ {
		if _, err := io.ReadFull(src, length); err != nil {
			// The stream ended without a last chunk: cut short.
			return ErrDamaged
		}
		size := binary.BigEndian.Uint32(length)
		if size > chunkSize {
			return ErrDamaged
		}
		sealed := buf[:int(size)+aead.Overhead()]
		if _, err := io.ReadFull(src, sealed); err != nil {
			return ErrDamaged
		}
		if plain, err := aead.Open(nil, nonceFor(prefix, counter, false), sealed, header); err == nil {
			if _, err := dst.Write(plain); err != nil {
				return err
			}
			continue
		}
		plain, err := aead.Open(nil, nonceFor(prefix, counter, true), sealed, header)
		if err != nil {
			return ErrDamaged
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
		// Nothing may follow the last chunk.
		if n, _ := io.ReadFull(src, make([]byte, 1)); n != 0 {
			return ErrDamaged
		}
		return nil
	}
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("prepare the cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// CheckPassphrase reports whether passphrase opens a sealed backup, from its
// header alone: ErrWrongPassphrase when it does not, ErrDamaged when the
// header is not one.
func CheckPassphrase(header []byte, passphrase string) error {
	if len(header) < HeaderSize || !IsSealed(header) {
		return ErrDamaged
	}
	salt := header[len(magic) : len(magic)+saltSize]
	check := header[len(magic)+saltSize : len(magic)+saltSize+checkSize]
	if _, want := deriveKey(passphrase, salt); !hmac.Equal(check, want) {
		return ErrWrongPassphrase
	}
	return nil
}

// SealFile seals a file in place, for a backup job's seal step. The result is
// readable by the job's other containers, which share the pod's group.
func SealFile(path, passphrase string) error {
	return transformFile(path, func(dst io.Writer, src io.Reader) error { return Seal(dst, src, passphrase) })
}

// OpenFile opens a sealed file in place, for a restore job's open step.
func OpenFile(path, passphrase string) error {
	return transformFile(path, func(dst io.Writer, src io.Reader) error { return Open(dst, src, passphrase) })
}

func transformFile(path string, transform func(io.Writer, io.Reader) error) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	partial := path + ".partial"
	dst, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	// Explicit, because the process's umask decides what OpenFile's mode
	// becomes, and the next container runs as another user in the same group.
	if err := dst.Chmod(0o640); err != nil {
		dst.Close()
		return err
	}
	if err := transform(dst, src); err != nil {
		dst.Close()
		os.Remove(partial)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(partial)
		return err
	}
	return os.Rename(partial, path)
}
