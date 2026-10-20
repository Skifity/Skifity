// Package registryguard stands in front of the in-cluster registry and decides
// who may write to it.
//
// The registry holds every team's images and takes no password: a registry
// with authentication would need every server's container runtime to have a
// password before it could pull an app, and a cluster that cannot pull cannot
// restart anything. So pulling stays open — only the machines and the build
// namespace can reach the registry at all — and writing is what is guarded. A
// build was able to push over any other team's image, which is the one thing a
// hostile Dockerfile could do to somebody else's running app.
//
// Each build is given a token for its own app's repository and nothing else,
// signed by the panel with a key only the panel and this guard hold. The panel
// gives itself one for every repository, to sweep old images. The token says
// what it is for and until when; there is nothing to look up.
//
// The guard runs in the registry's own pod, in front of a registry that listens
// on the pod's loopback only, so there is no way round it.
package registryguard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"skifity/internal/runsafe"
)

// AnyRepository is the scope of the panel's own token: every repository.
const AnyRepository = "*"

// User is the user name a token is sent with. A registry client sends a
// password with a user name; the guard reads only the password.
const User = "skifity"

const tokenPrefix = "v1."

type claims struct {
	Repository string `json:"r"`
	Expires    int64  `json:"e"`
}

// Sign makes a token for one repository, or AnyRepository, valid until
// expires.
func Sign(key []byte, repository string, expires time.Time) string {
	payload, _ := json.Marshal(claims{Repository: repository, Expires: expires.Unix()})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return tokenPrefix + encoded + "." + base64.RawURLEncoding.EncodeToString(mac(key, encoded))
}

// Verify checks a token and returns the repository it is for.
func Verify(key []byte, token string, now time.Time) (string, error) {
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return "", errors.New("not a registry token")
	}
	encoded, signature, ok := strings.Cut(rest, ".")
	if !ok {
		return "", errors.New("not a registry token")
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(given, mac(key, encoded)) {
		return "", errors.New("the token was not signed by this panel")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("not a registry token")
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil || c.Repository == "" {
		return "", errors.New("not a registry token")
	}
	if now.Unix() >= c.Expires {
		return "", errors.New("the token has expired")
	}
	return c.Repository, nil
}

func mac(key []byte, payload string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(tokenPrefix + payload))
	return h.Sum(nil)
}

// Guard is the HTTP handler in front of the registry.
type Guard struct {
	key   []byte
	proxy *httputil.ReverseProxy
	now   func() time.Time
	log   *slog.Logger
}

// New builds a guard that forwards what it allows to upstream.
func New(key []byte, upstream *url.URL, log *slog.Logger) (*Guard, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("the registry's key is %d bytes, and it has to be at least 32", len(key))
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			// The registry answers an upload with where to send the rest of
			// it. It is told to make that address relative, and the host is
			// left as the client asked, so the rest comes back through here.
			r.Out.Host = r.In.Host
			// The registry has no use for the token, and nothing behind the
			// guard should ever see one.
			r.Out.Header.Del("Authorization")
		},
	}
	return &Guard{key: key, proxy: proxy, now: time.Now, log: log}, nil
}

// Run serves the guard on address until ctx ends.
//
// The header has to arrive within seconds, and an idle connection is closed
// after two minutes. Nothing else is timed: a layer is gigabytes, and pushing
// or pulling one over a slow line is minutes of one request. A body is read
// only from a request already allowed, so a stranger cannot hold a connection
// open by sending one slowly.
func (g *Guard) Run(ctx context.Context, address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           g,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		// Every pull of every app passes through here, so even the shutdown
		// watcher recovers.
		defer runsafe.Recover(g.log, "shutting the registry's guard down", nil)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	g.log.Info("registry guard listening", "address", address)
	if err := server.ListenAndServe(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// ServeHTTP answers one request to the registry.
func (g *Guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if decision, reason := g.decide(r); decision != allowed {
		g.refuse(w, r, decision, reason)
		return
	}
	g.proxy.ServeHTTP(w, r)
}

type decision int

const (
	allowed decision = iota
	unauthenticated
	forbidden
)

// decide is the whole policy.
//
// Reading is open, except the list of every repository, which only the panel
// needs and which names every team's apps. Everything else is a write and
// needs a token for the repository written to; deleting needs the panel's own.
func (g *Guard) decide(r *http.Request) (decision, string) {
	path := r.URL.Path
	reading := r.Method == http.MethodGet || r.Method == http.MethodHead
	if path == "/v2/" || path == "/v2" {
		return allowed, ""
	}
	if reading && path != "/v2/_catalog" {
		return allowed, ""
	}

	repository, err := g.tokenFrom(r)
	if err != nil {
		return unauthenticated, err.Error()
	}
	if repository == AnyRepository {
		return allowed, ""
	}
	if r.Method == http.MethodDelete || path == "/v2/_catalog" {
		return forbidden, "only the panel deletes images and lists the repositories"
	}
	if !within(path, repository) {
		return forbidden, "this token is for " + repository
	}
	return allowed, ""
}

// within reports whether a registry API path is about one repository: its
// manifests, blobs, uploads, tags or referrers. Matched against the
// repository the token names, rather than by parsing the path, because a
// repository's name may itself contain a segment called "blobs".
func within(path, repository string) bool {
	rest, ok := strings.CutPrefix(path, "/v2/"+repository+"/")
	if !ok {
		return false
	}
	for _, kind := range []string{"manifests/", "blobs/", "tags/", "referrers/"} {
		if strings.HasPrefix(rest, kind) {
			return true
		}
	}
	return false
}

// tokenFrom reads a token from Basic authentication, as a registry client
// sends it, or a Bearer header, as the panel does.
func (g *Guard) tokenFrom(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", errors.New("writing to the registry needs a token")
	}
	var token string
	if bearer, ok := strings.CutPrefix(header, "Bearer "); ok {
		token = strings.TrimSpace(bearer)
	} else if _, password, ok := r.BasicAuth(); ok {
		token = password
	} else {
		return "", errors.New("writing to the registry needs a token")
	}
	return Verify(g.key, token, g.now())
}

// refuse answers the way a registry does, so that a client says something a
// person can read rather than failing to parse the answer.
func (g *Guard) refuse(w http.ResponseWriter, r *http.Request, d decision, reason string) {
	code, status := "UNAUTHORIZED", http.StatusUnauthorized
	if d == forbidden {
		code, status = "DENIED", http.StatusForbidden
	} else {
		// The challenge is what makes a client send its credentials at all:
		// without it a push is tried once, anonymously, and fails.
		w.Header().Set("WWW-Authenticate", `Basic realm="Skifity registry"`)
	}
	g.log.Info("registry write refused", "method", r.Method, "path", r.URL.Path, "reason", reason)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": code, "message": reason}},
	})
}
