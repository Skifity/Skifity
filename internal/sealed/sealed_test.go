package sealed

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func sealed(t *testing.T, plain []byte, passphrase string) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := Seal(&out, bytes.NewReader(plain), passphrase); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Every size that meets a chunk boundary differently, round trip.
func TestASealedBackupOpensToWhatWentIn(t *testing.T) {
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		plain := make([]byte, size)
		_, _ = rand.Read(plain)
		file := sealed(t, plain, "correct horse battery staple")
		if !IsSealed(file) || bytes.Contains(file, plain[:min(size, 64)]) && size >= 64 {
			t.Fatalf("%d bytes: the file is not sealed", size)
		}
		var out bytes.Buffer
		if err := Open(&out, bytes.NewReader(file), "correct horse battery staple"); err != nil {
			t.Fatalf("%d bytes: %v", size, err)
		}
		if !bytes.Equal(out.Bytes(), plain) {
			t.Fatalf("%d bytes: came back different", size)
		}
	}
	// The same backup sealed twice is two different files.
	if bytes.Equal(sealed(t, []byte("x"), "p"), sealed(t, []byte("x"), "p")) {
		t.Fatal("sealing is deterministic")
	}
}

func TestAWrongPassphraseIsToldApartFromDamage(t *testing.T) {
	file := sealed(t, bytes.Repeat([]byte("row"), 50000), "the right one")
	if err := Open(&bytes.Buffer{}, bytes.NewReader(file), "the wrong one"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("a wrong passphrase answered %v", err)
	}
}

// What a file in a bucket can suffer, each refused rather than restored.
func TestADamagedBackupIsRefused(t *testing.T) {
	plain := make([]byte, 3*chunkSize+100)
	_, _ = rand.Read(plain)
	file := sealed(t, plain, "p")
	chunk := 4 + chunkSize + 16 // length, then ciphertext and tag

	cases := map[string][]byte{
		"cut short in the middle of a chunk": file[:len(file)-50],
		"cut at a chunk boundary":            file[:HeaderSize+2*chunk],
		"a byte changed":                     flip(file, HeaderSize+chunk+100),
		"the header changed":                 flip(file, len(magic)+saltSize+checkSize+1),
		"something appended":                 append(append([]byte{}, file...), 0, 0, 0, 0),
		"two chunks swapped": append(append(append(append([]byte{}, file[:HeaderSize]...),
			file[HeaderSize+chunk:HeaderSize+2*chunk]...), file[HeaderSize:HeaderSize+chunk]...),
			file[HeaderSize+2*chunk:]...),
		"only the header": file[:HeaderSize],
	}
	for name, damaged := range cases {
		if err := Open(&bytes.Buffer{}, bytes.NewReader(damaged), "p"); !errors.Is(err, ErrDamaged) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func flip(data []byte, at int) []byte {
	out := append([]byte{}, data...)
	out[at] ^= 0x01
	return out
}
