package remote

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/netguard"
)

// The download is the panel's own request to an address a team administrator
// typed. netguard refuses loopback, which is where every httptest server
// listens, so the tests that need to reach one say so through Allowed — the
// same seam netguard's own tests describe — and one test leaves it alone to
// show that the panel's real client does refuse it.

func allowLoopback(ip net.IP) bool { return ip.IsLoopback() || netguard.Allowed(ip) }

// testFetcher reaches the test servers, trusting their certificate.
func testFetcher(server *httptest.Server) *Fetcher {
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	return &Fetcher{Allowed: allowLoopback, RootCAs: pool, Timeout: 5 * time.Second}
}

func TestTheRealClientRefusesWhatNetguardRefuses(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("templates: []"))
	}))
	defer server.Close()

	for _, address := range []string{
		server.URL + "/catalogue.yaml",
		"https://169.254.169.254/latest/meta-data/",
		"https://127.0.0.2:1/catalogue.yaml",
	} {
		_, err := (&Fetcher{Timeout: 5 * time.Second}).Fetch(context.Background(), address, Header{}, MaxDownloadBytes)
		var blocked *netguard.Blocked
		if !errors.As(err, &blocked) {
			t.Errorf("%s answered %v, not netguard refusing it", address, err)
		}
	}
}

func TestADownloadIsLimitedInSize(t *testing.T) {
	big := strings.Repeat("x", 4096)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/streamed" {
			// No Content-Length: the limit has to be read, not believed.
			w.Header().Set("Content-Type", "application/yaml")
			flusher := w.(http.Flusher)
			for i := 0; i < 4; i++ {
				_, _ = w.Write([]byte(big))
				flusher.Flush()
			}
			return
		}
		_, _ = w.Write([]byte(big + big))
	}))
	defer server.Close()
	fetcher := testFetcher(server)

	for _, path := range []string{"/declared", "/streamed"} {
		_, err := fetcher.Fetch(context.Background(), server.URL+path, Header{}, 4096)
		var tooLarge *TooLargeError
		if !errors.As(err, &tooLarge) {
			t.Errorf("%s over the limit answered %v", path, err)
		}
	}
	body, err := fetcher.Fetch(context.Background(), server.URL+"/declared", Header{}, 8192)
	if err != nil || len(body) != 8192 {
		t.Errorf("a download at the limit answered %d bytes, %v", len(body), err)
	}
}

func TestASlowServerIsCutOff(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("templates:"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer close(release)

	fetcher := testFetcher(server)
	fetcher.Timeout = 300 * time.Millisecond
	started := time.Now()
	_, err := fetcher.Fetch(context.Background(), server.URL, Header{}, MaxDownloadBytes)
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("a server that never finishes answered %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Errorf("the download took %s to give up", time.Since(started))
	}
}

func TestAnAnswerOtherThanOKIsAFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such file", http.StatusNotFound)
	}))
	defer server.Close()
	_, err := testFetcher(server).Fetch(context.Background(), server.URL, Header{}, MaxDownloadBytes)
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusNotFound {
		t.Fatalf("a 404 answered %v", err)
	}
}

// The team's header goes to the catalogue's host and nowhere else. net/http
// drops Authorization on the way to another domain on its own, and keeps
// PRIVATE-TOKEN — GitLab's — which is why this is not left to it.
func TestTheHeaderIsNotSentOnToAnotherHost(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen["other"] = r.Header.Get("PRIVATE-TOKEN")
		mu.Unlock()
		_, _ = w.Write([]byte("templates: []"))
	}))
	defer other.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("PRIVATE-TOKEN")
		mu.Unlock()
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/away":
			http.Redirect(w, r, other.URL+"/catalogue.yaml", http.StatusFound)
		default:
			_, _ = w.Write([]byte("templates: []"))
		}
	}))
	defer server.Close()

	fetcher := testFetcher(server)
	header := Header{Name: "PRIVATE-TOKEN", Value: "glpat-not-a-real-token"}
	for _, path := range []string{"/same", "/away"} {
		if _, err := fetcher.Fetch(context.Background(), server.URL+path, header, MaxDownloadBytes); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["/same"] != header.Value || seen["/final"] != header.Value {
		t.Errorf("the header was not sent to the catalogue's own host: %+v", seen)
	}
	if seen["other"] != "" {
		t.Errorf("the team's token was sent on to another host: %q", seen["other"])
	}
}

func TestARedirectMustStayHTTPSAndStopSomewhere(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			http.Redirect(w, r, "http://example.org/catalogue.yaml?token=planted-redirect-secret", http.StatusFound)
		default:
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer server.Close()
	fetcher := testFetcher(server)

	for path, says := range map[string]string{"/plain": "not https", "/loop": "more than"} {
		_, err := fetcher.Fetch(context.Background(), server.URL+path, Header{}, MaxDownloadBytes)
		var refused *RedirectError
		if !errors.As(err, &refused) || !strings.Contains(err.Error(), says) {
			t.Errorf("%s answered %v", path, err)
		}
		// The sentence is stored and shown to the team, and a redirect's
		// address can carry somebody's signature.
		if err != nil && strings.Contains(err.Error(), "planted-redirect-secret") {
			t.Errorf("%s repeated the redirect's query: %v", path, err)
		}
	}
}

// A self-hosted Gitea on 10.0.0.5 is an address somebody typed and can see. A
// public host answering "go to 10.0.0.5 instead" is not the same thing.
func TestARedirectCannotLeadToAPrivateAddressTheFirstRequestDidNotReach(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("this machine cannot listen on a second loopback address: %v", err)
	}
	inside := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the inside of somebody's network"))
	}))
	inside.Listener = listener
	inside.StartTLS()
	defer inside.Close()

	outside := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/inside":
			http.Redirect(w, r, inside.URL+"/admin", http.StatusFound)
		case "/here":
			http.Redirect(w, r, "/catalogue.yaml", http.StatusFound)
		default:
			_, _ = w.Write([]byte("templates: []"))
		}
	}))
	defer outside.Close()

	fetcher := testFetcher(outside)
	// Loopback stands in for the private ranges here, both addresses of it.
	fetcher.Private = func(ip net.IP) bool { return ip.IsLoopback() }

	_, err = fetcher.Fetch(context.Background(), outside.URL+"/inside", Header{}, MaxDownloadBytes)
	var refused *RedirectError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "127.0.0.2") {
		t.Fatalf("a redirect to a private address nobody typed answered %v", err)
	}
	// A redirect that stays where the first request already was is fine: it
	// is the address the administrator typed.
	if _, err := fetcher.Fetch(context.Background(), outside.URL+"/here", Header{}, MaxDownloadBytes); err != nil {
		t.Errorf("a redirect on the same private server was refused: %v", err)
	}
}

func TestWhatAnAddressMayBe(t *testing.T) {
	for address, ok := range map[string]bool{
		"https://raw.githubusercontent.com/acme/catalogue/v1.2.0/index.yaml":                          true,
		"https://git.acme.internal/api/v4/projects/7/repository/archive.tar.gz?sha=v1.2.0":            true,
		"https://codeberg.org/acme/catalogue/archive/main.zip#readme":                                 true,
		"http://raw.githubusercontent.com/acme/catalogue/main/index.yaml":                             false,
		"raw.githubusercontent.com/acme/catalogue/main/index.yaml":                                    false,
		"https://bot:glpat-not-real@git.acme.internal/catalogue.yaml":                                 false,
		"https://git.acme.internal/api/v4/projects/7/repository/files/index.yaml/raw?private_token=x": false,
		"https://bucket.s3.amazonaws.com/index.yaml?X-Amz-Signature=abc&X-Amz-Credential=def":         false,
		"https://example.org/index.yaml?api_key=x":                                                    false,
		"file:///etc/passwd": false,
		"":                   false,
	} {
		parsed, err := CheckURL(address)
		if (err == nil) != ok {
			t.Errorf("CheckURL(%q) = %v; want accepted %v", address, err, ok)
		}
		if err == nil && parsed.Fragment != "" {
			t.Errorf("CheckURL(%q) kept the fragment", address)
		}
	}
}

func TestWhatAHeaderMayBe(t *testing.T) {
	for _, c := range []struct {
		name, value string
		ok          bool
	}{
		{"", "", true},
		{"Authorization", "Bearer not-a-real-token", true},
		{"PRIVATE-TOKEN", "glpat-not-a-real-token", true},
		{"Authorization", "", false},
		{"", "orphan-value", false},
		{"Host", "elsewhere.example", false},
		{"Content-Length", "0", false},
		{"Bad Header", "x", false},
		{"Authorization", "Bearer x\r\nX-Injected: yes", false},
	} {
		if err := CheckHeader(c.name, c.value); (err == nil) != c.ok {
			t.Errorf("CheckHeader(%q, %q) = %v; want accepted %v", c.name, c.value, err, c.ok)
		}
	}
}

func TestAnIconComesOnlyFromTheCataloguesOwnHost(t *testing.T) {
	catalogue, _ := url.Parse("https://raw.githubusercontent.com/acme/catalogue/main/index.yaml")
	for ref, want := range map[string]string{
		"icons/wiki.svg": "https://raw.githubusercontent.com/acme/catalogue/main/icons/wiki.svg",
		"/acme/logo.png": "https://raw.githubusercontent.com/acme/logo.png",
		"https://RAW.githubusercontent.com:443/acme/x.svg":  "https://RAW.githubusercontent.com:443/acme/x.svg",
		"https://tracker.example/pixel.png":                 "",
		"http://raw.githubusercontent.com/acme/x.svg":       "",
		"//cdn.example/x.svg":                               "",
		"https://raw.githubusercontent.com:8443/acme/x.svg": "",
	} {
		resolved, err := ResolveIconURL(catalogue, ref)
		switch {
		case want == "" && err == nil:
			t.Errorf("%s was accepted as %s", ref, resolved)
		case want != "" && err != nil:
			t.Errorf("%s was refused: %v", ref, err)
		case want != "" && resolved.String() != want:
			t.Errorf("%s resolved to %s, want %s", ref, resolved, want)
		}
	}
}

func TestAnIconIsWhatItsBytesSay(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		want string
	}{
		"an svg":                   {svgLogo, "image/svg+xml"},
		"an svg after a comment":   {"<!-- logo -->\n<svg viewBox=\"0 0 1 1\"></svg>", "image/svg+xml"},
		"a png":                    {"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png"},
		"a webp":                   {"RIFF\x10\x00\x00\x00WEBPVP8 ", "image/webp"},
		"an html page":             {"<!doctype html><html><svg></svg></html>", ""},
		"a script":                 {"<script>alert(1)</script>", ""},
		"an svg that never closes": {"<svg viewBox=\"0 0 1 1\">", ""},
		"nothing":                  {"", ""},
	} {
		got, ok := Sniff([]byte(c.body))
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s sniffed as %q, %v; want %q", name, got, ok, c.want)
		}
	}
}
