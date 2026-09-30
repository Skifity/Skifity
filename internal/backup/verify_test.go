package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"skifity/internal/sealed"
)

func gz(t *testing.T, plain []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	return out.Bytes()
}

func seal(t *testing.T, data []byte, passphrase string) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := sealed.Seal(&out, bytes.NewReader(data), passphrase); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// What verifying a backup has to tell apart: a whole one, sealed or not, and
// each way one can be useless on the day it is needed.
func TestVerifyingReadsABackupThroughAndSaysWhatIsWrong(t *testing.T) {
	dump := make([]byte, 300_000)
	_, _ = rand.Read(dump)
	archive := gz(t, dump)
	sealedArchive := seal(t, archive, "correct horse battery staple")

	if n, err := ReadThrough(bytes.NewReader(archive), false, ""); err != nil || n != int64(len(dump)) {
		t.Fatalf("a whole archive: %d, %v", n, err)
	}
	if n, err := ReadThrough(bytes.NewReader(sealedArchive), true, "correct horse battery staple"); err != nil || n != int64(len(dump)) {
		t.Fatalf("a whole sealed archive: %d, %v", n, err)
	}

	cases := []struct {
		name       string
		data       []byte
		isSealed   bool
		passphrase string
		want       string
	}{
		{"cut short", archive[:len(archive)/2], false, "", "damaged"},
		{"a changed byte", flipped(archive, len(archive)/2), false, "", "damaged"},
		{"not an archive", []byte("<html>Access Denied</html>"), false, "", "gzip"},
		{"empty", gz(t, nil), false, "", "empty"},
		{"sealed, the wrong passphrase", sealedArchive, true, "another passphrase", "different passphrase"},
		{"sealed, cut short", sealedArchive[:len(sealedArchive)-100], true, "correct horse battery staple", "damaged"},
	}
	for _, c := range cases {
		_, err := ReadThrough(bytes.NewReader(c.data), c.isSealed, c.passphrase)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := ReadThrough(bytes.NewReader(sealedArchive), true, "x"); !errors.Is(err, sealed.ErrWrongPassphrase) {
		t.Errorf("the wrong passphrase is not recognisable: %v", err)
	}
}

func flipped(data []byte, at int) []byte {
	out := append([]byte{}, data...)
	out[at] ^= 0xff
	return out
}

func TestABackupIsVerifiedOnceAtATime(t *testing.T) {
	// Each verification of a sealed backup is 64 MiB of Argon2id. Asked for
	// again while it runs, it is the same verification, not a second one.
	m := &Manager{}
	if !m.claimVerification("bak_1") {
		t.Fatal("the first verification was refused")
	}
	if m.claimVerification("bak_1") {
		t.Fatal("a backup being verified was verified again alongside")
	}
	if !m.claimVerification("bak_2") {
		t.Fatal("another backup's verification was refused; it waits its turn instead")
	}
	m.releaseVerification("bak_1")
	if !m.claimVerification("bak_1") {
		t.Fatal("a backup verified before cannot be verified again")
	}
}
