// Package pluginstore reads the catalogue a plugin can be installed from.
//
// # What the store is
//
// Two files at a URL: `index.json`, which lists what is available, and
// `index.json.sig`, a detached Ed25519 signature over its exact bytes. Two
// files rather than one envelope so that the index stays readable and
// diffable — a catalogue somebody can open in a browser and check by eye is
// worth more than one that is only machine-readable.
//
// # What the signature is for, and what it is not for
//
// An index entry says "this manifest, at this address, with this hash". The
// signature covers the whole list, so the store operator vouches for the list;
// the hash covers each manifest, so a manifest cannot be swapped after the list
// was signed. That is the same shape as an apt release file, and it is chosen
// because it needs no key registry: a publisher does not need a key, because
// the store is what vouches for them.
//
// It is not proof the plugin is safe. It says this entry is the one the store
// published. What a plugin may do is its manifest's permission list, which an
// administrator reads before installing, and that is the part that actually
// protects anybody.
//
// # When there is no key
//
// A panel with no public key configured still reads the index, and every entry
// comes back unverified and is shown as such. Refusing outright would mean the
// store cannot be used until somebody pastes a key, and quietly accepting would
// make the signature decoration. Saying "nobody vouched for this" on the page
// is the third answer, and it is the honest one.
package pluginstore

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultIndexURL is where the Skifity store publishes its catalogue.
const DefaultIndexURL = "https://plugins.skifity.com/index.json"

// SignatureSuffix is appended to the index URL to find its signature.
const SignatureSuffix = ".sig"

// Limits, so that a URL an operator pasted cannot be a way to fill a disk.
const (
	MaxIndexBytes     = 4 << 20
	MaxSignatureBytes = 1 << 10
)

// Index is the catalogue as published.
type Index struct {
	// Version is the index format, so a later store can change shape without
	// every older panel reading it wrongly.
	Version int `json:"version"`
	// GeneratedAt is when it was built, shown so that a catalogue nobody has
	// updated for a year is visibly one.
	GeneratedAt time.Time `json:"generated_at,omitzero"`
	Plugins     []Entry   `json:"plugins"`
}

// IndexVersion is the format this build reads.
const IndexVersion = 1

// Entry is one plugin in the catalogue.
//
// It is a summary and not a manifest: what a plugin may do is in its manifest,
// which is fetched and shown before anything is installed. An index that
// carried permissions would be a second place for them to be written and a
// second place for them to disagree.
type Entry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	License     string `json:"license"`
	Author      string `json:"author"`
	Homepage    string `json:"homepage,omitempty"`
	Category    string `json:"category,omitempty"`
	// ManifestURL is where the manifest is, and ManifestSHA256 is what it
	// should hash to. The hash is what stops a manifest being swapped after
	// the index was signed.
	ManifestURL    string `json:"manifest_url"`
	ManifestSHA256 string `json:"manifest_sha256"`
	// Paid marks a plugin that is sold. Skifity does not process payments:
	// PurchaseURL is the publisher's own page, and the plugin validates its
	// own licence key through its own server.
	Paid        bool   `json:"paid,omitempty"`
	PurchaseURL string `json:"purchase_url,omitempty"`
}

// Catalogue is an index and what is known about where it came from.
type Catalogue struct {
	Index Index `json:"index"`
	// Verified is true when a public key was configured and the signature
	// checked out. False means nobody vouched for this list, and the panel says
	// so rather than showing it as though somebody had.
	Verified bool `json:"verified"`
	// Unsigned is true when no key is configured at all, which is a different
	// thing from a signature that failed — and a failed signature is an error
	// rather than a Catalogue.
	Unsigned bool `json:"unsigned"`
	// URL is where it was read from.
	URL string `json:"url"`
}

// Errors this package returns, so a caller can tell a store that is down from
// one that is lying.
var (
	ErrUnreachable    = errors.New("the plugin store could not be reached")
	ErrBadIndex       = errors.New("the plugin store's index could not be read")
	ErrBadSignature   = errors.New("the plugin store's index is not signed by the key this panel trusts")
	ErrManifestDigest = errors.New("that manifest is not the one the store listed")
)

// Client reads a store.
type Client struct {
	// HTTP is the client to fetch with. A caller that has a guarded one —
	// refusing loopback and the cloud metadata address — passes it here,
	// because the index URL is configurable.
	HTTP *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Fetch reads the catalogue at indexURL and checks its signature.
//
// publicKey is base64, as a store operator publishes it. An empty one means
// nothing is checked and the catalogue comes back marked unsigned.
func (c Client) Fetch(ctx context.Context, indexURL, publicKey string) (Catalogue, error) {
	body, err := c.get(ctx, indexURL, MaxIndexBytes)
	if err != nil {
		return Catalogue{}, err
	}

	catalogue := Catalogue{URL: indexURL}
	if strings.TrimSpace(publicKey) == "" {
		catalogue.Unsigned = true
	} else {
		key, err := ParsePublicKey(publicKey)
		if err != nil {
			return Catalogue{}, err
		}
		signature, err := c.get(ctx, indexURL+SignatureSuffix, MaxSignatureBytes)
		if err != nil {
			return Catalogue{}, fmt.Errorf("%w: the index is there and its signature is not: %w",
				ErrBadSignature, err)
		}
		if !VerifySignature(key, body, string(signature)) {
			return Catalogue{}, ErrBadSignature
		}
		catalogue.Verified = true
	}

	// Verified before parsed, always. Parsing first would mean the panel had
	// already acted on bytes nobody vouched for.
	if err := json.Unmarshal(body, &catalogue.Index); err != nil {
		return Catalogue{}, fmt.Errorf("%w: %w", ErrBadIndex, err)
	}
	if catalogue.Index.Version != IndexVersion {
		return Catalogue{}, fmt.Errorf(
			"%w: it is version %d and this panel reads version %d",
			ErrBadIndex, catalogue.Index.Version, IndexVersion)
	}
	catalogue.Index.Plugins = keepUsable(catalogue.Index.Plugins)
	return catalogue, nil
}

// keepUsable drops entries nothing could install.
//
// A store is somebody else's file and one bad row must not empty the page, so
// this filters rather than failing. An entry with no manifest address or no
// hash is not a thing the panel could install even if it wanted to.
func keepUsable(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.ID == "" || entry.ManifestURL == "" || entry.ManifestSHA256 == "" {
			continue
		}
		if _, err := hex.DecodeString(entry.ManifestSHA256); err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// FetchManifest downloads an entry's manifest and checks it against the hash
// the index carried.
//
// The hash is the whole reason the index is worth signing: without it, a signed
// list of addresses would still let whoever serves those addresses put anything
// behind them.
func (c Client) FetchManifest(ctx context.Context, entry Entry) ([]byte, error) {
	body, err := c.get(ctx, entry.ManifestURL, MaxIndexBytes)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), entry.ManifestSHA256) {
		return nil, fmt.Errorf("%w: the store listed %s and the file at %s hashes to %s",
			ErrManifestDigest, entry.ManifestSHA256, entry.ManifestURL, hex.EncodeToString(sum[:]))
	}
	return body, nil
}

func (c Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not an address: %w", ErrUnreachable, url, err)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnreachable, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", ErrUnreachable, url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnreachable, url, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d KB", ErrUnreachable, url, limit>>10)
	}
	return body, nil
}

// ParsePublicKey reads a base64 Ed25519 public key as a store publishes it.
func ParsePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("that is not a base64 public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("an Ed25519 public key is %d bytes and that is %d",
			ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// VerifySignature checks a detached base64 signature over body.
func VerifySignature(key ed25519.PublicKey, body []byte, signature string) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(key, body, raw)
}

// Sign produces the signature a store publishes beside its index.
//
// Here rather than in a separate tool because the thing that verifies and the
// thing that signs must agree byte for byte, and two implementations of that
// is one implementation and one bug waiting.
func Sign(private ed25519.PrivateKey, body []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(private, body))
}

// EncodePublicKey renders a key the way a store operator publishes it.
func EncodePublicKey(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}
