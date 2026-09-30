package pluginstore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// storeServer serves an index, its signature and the manifests it names.
type storeServer struct {
	*httptest.Server
	index     []byte
	signature string
	manifests map[string][]byte
}

func newStore(t *testing.T, index Index, private ed25519.PrivateKey) *storeServer {
	t.Helper()
	body, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	s := &storeServer{index: body, manifests: map[string][]byte{}}
	if private != nil {
		s.signature = Sign(private, body)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(s.index)
		case "/index.json.sig":
			if s.signature == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(s.signature))
		default:
			manifest, ok := s.manifests[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(manifest)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func sampleIndex(manifestURL, digest string) Index {
	return Index{
		Version: IndexVersion,
		Plugins: []Entry{{
			ID: "com.example.backups", Name: "Backups", Description: "Copies backups away.",
			Version: "1.0.0", License: "MIT", Author: "Example Ltd",
			ManifestURL: manifestURL, ManifestSHA256: digest,
		}},
	}
}

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

// A signed index from the key the panel trusts is verified, and the entries
// come through.
func TestASignedIndexIsVerified(t *testing.T) {
	public, private := keys(t)
	server := newStore(t, Index{Version: IndexVersion}, private)
	server.index, _ = json.Marshal(sampleIndex(server.URL+"/m.yaml", digestOf([]byte("x"))))
	server.signature = Sign(private, server.index)

	catalogue, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", EncodePublicKey(public))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !catalogue.Verified {
		t.Error("a correctly signed index came back unverified")
	}
	if catalogue.Unsigned {
		t.Error("a signed index was reported as unsigned")
	}
	if len(catalogue.Index.Plugins) != 1 || catalogue.Index.Plugins[0].ID != "com.example.backups" {
		t.Fatalf("entries = %+v", catalogue.Index.Plugins)
	}
}

// An index signed by somebody else is refused outright. This is the case the
// whole file exists for.
func TestAnIndexSignedByAnotherKeyIsRefused(t *testing.T) {
	trusted, _ := keys(t)
	_, impostor := keys(t)
	server := newStore(t, sampleIndex("http://example.test/m.yaml", digestOf([]byte("x"))), impostor)

	_, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", EncodePublicKey(trusted))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want a signature failure", err)
	}
}

// Changing one byte of a signed index invalidates it.
func TestATamperedIndexIsRefused(t *testing.T) {
	public, private := keys(t)
	server := newStore(t, sampleIndex("http://example.test/m.yaml", digestOf([]byte("x"))), private)
	// The signature stays; the body changes.
	server.index = append(server.index, ' ')

	_, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", EncodePublicKey(public))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want a signature failure", err)
	}
}

// A key configured and no signature published is a failure, not a shrug: it is
// exactly what an attacker who can serve the index but not sign it would do.
func TestAMissingSignatureIsAFailureWhenAKeyIsConfigured(t *testing.T) {
	public, _ := keys(t)
	server := newStore(t, sampleIndex("http://example.test/m.yaml", digestOf([]byte("x"))), nil)

	_, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", EncodePublicKey(public))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want a signature failure", err)
	}
}

// With no key configured the index is readable and every entry is marked as
// vouched for by nobody. Refusing would make the store unusable until somebody
// pastes a key; accepting quietly would make the signature decoration.
func TestWithNoKeyTheCatalogueIsMarkedUnsigned(t *testing.T) {
	server := newStore(t, sampleIndex("http://example.test/m.yaml", digestOf([]byte("x"))), nil)

	catalogue, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if catalogue.Verified {
		t.Error("an index nobody signed was reported as verified")
	}
	if !catalogue.Unsigned {
		t.Error("an index read with no key was not marked unsigned")
	}
}

// A signed list of addresses would still let whoever serves those addresses put
// anything behind them. The hash is what closes that.
func TestAManifestMustHashToWhatTheIndexListed(t *testing.T) {
	manifest := []byte("apiVersion: plugin.skifity.com/v1\n")
	server := newStore(t, Index{Version: IndexVersion}, nil)
	server.manifests["/m.yaml"] = manifest

	client := Client{HTTP: server.Client()}
	entry := Entry{ManifestURL: server.URL + "/m.yaml", ManifestSHA256: digestOf(manifest)}

	got, err := client.FetchManifest(context.Background(), entry)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if string(got) != string(manifest) {
		t.Error("the manifest came back changed")
	}

	// Now the address serves something else.
	server.manifests["/m.yaml"] = []byte("apiVersion: plugin.skifity.com/v1 # and something extra\n")
	if _, err := client.FetchManifest(context.Background(), entry); !errors.Is(err, ErrManifestDigest) {
		t.Fatalf("err = %v, want a digest failure", err)
	}
}

// An index in a format this build does not read is refused rather than
// half-understood.
func TestAnotherIndexVersionIsRefused(t *testing.T) {
	server := newStore(t, Index{Version: 99}, nil)
	_, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", "")
	if !errors.Is(err, ErrBadIndex) {
		t.Fatalf("err = %v, want a format failure", err)
	}
}

// A store is somebody else's file, and one bad row must not empty the page.
func TestUnusableEntriesAreDroppedRatherThanFailing(t *testing.T) {
	index := Index{Version: IndexVersion, Plugins: []Entry{
		{ID: "good", ManifestURL: "http://example.test/a", ManifestSHA256: digestOf([]byte("a"))},
		{ID: "no manifest address"},
		{ID: "no hash", ManifestURL: "http://example.test/b"},
		{ID: "a hash that is not one", ManifestURL: "http://example.test/c", ManifestSHA256: "zzzz"},
	}}
	server := newStore(t, index, nil)

	catalogue, err := Client{HTTP: server.Client()}.
		Fetch(context.Background(), server.URL+"/index.json", "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(catalogue.Index.Plugins) != 1 || catalogue.Index.Plugins[0].ID != "good" {
		t.Errorf("entries = %+v, want only the usable one", catalogue.Index.Plugins)
	}
}

// A key that is not one is refused where somebody can see it, not at the moment
// a catalogue is being read.
func TestParsePublicKey(t *testing.T) {
	public, _ := keys(t)
	if _, err := ParsePublicKey(EncodePublicKey(public)); err != nil {
		t.Errorf("a real key was refused: %v", err)
	}
	for _, bad := range []string{"not base64!", "c2hvcnQ=", ""} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("%q was accepted as a key", bad)
		}
	}
}
