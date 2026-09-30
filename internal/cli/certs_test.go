package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// certsPanel answers the certificate routes for one team, and remembers what
// was uploaded.
type certsPanel struct {
	mu       sync.Mutex
	uploaded []map[string]string
	deleted  []string
}

func (p *certsPanel) serve(t *testing.T) {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/me":
			_, _ = io.WriteString(w, `{"user":{"id":"usr_1"},"teams":[{"id":"team_1","name":"Acme"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/teams/team_1/certificates":
			_, _ = io.WriteString(w, `{"items":[{"id":"crt_1","name":"wildcard","hostnames":["*.example.com"],
				"issuer":"Example CA","not_after":"2027-01-31T00:00:00Z","state":"valid","key_type":"ECDSA P-256",
				"domains":[{"hostname":"shop.example.com","app_name":"shop"}]}],"total":1}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/teams/team_1/certificates":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.mu.Lock()
			p.uploaded = append(p.uploaded, body)
			p.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"certificate":{"id":"crt_2","name":"`+body["name"]+`",
				"hostnames":["intranet.example.org"],"not_after":"2027-01-31T00:00:00Z","state":"valid"},
				"replaced":false,"reordered":true,"updating":2}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/teams/team_1/certificates/crt_1":
			p.mu.Lock()
			p.deleted = append(p.deleted, "crt_1")
			p.mu.Unlock()
			_, _ = io.WriteString(w, `{"ok":true,"updating":1}`)
		default:
			http.Error(w, `{"error":{"code":"not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")
}

const fakeChain = "-----BEGIN CERTIFICATE-----\nnot-a-real-certificate\n-----END CERTIFICATE-----\n"
const fakeKey = "-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n"

// The key comes from a file or from standard input, never from the command
// line, and every answer is JSON when asked for.
func TestACertificateIsUploadedFromFilesAndStandardInput(t *testing.T) {
	panel := &certsPanel{}
	panel.serve(t)
	dir := t.TempDir()
	chainFile, keyFile := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	if err := os.WriteFile(chainFile, []byte(fakeChain), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte(fakeKey), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := cmdCerts(t.Context(), []string{"add", "intranet", "--cert", chainFile, "--key", keyFile}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "intranet is uploaded") || !strings.Contains(out.String(), "put in order") ||
		!strings.Contains(out.String(), "2 app(s)") {
		t.Errorf("printed %q", out.String())
	}

	certsStdin = strings.NewReader(fakeKey)
	t.Cleanup(func() { certsStdin = os.Stdin })
	out.Reset()
	if err := cmdCerts(t.Context(), []string{"add", "intranet", "--cert", chainFile, "--key", "-", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(out.Bytes(), &saved); err != nil || saved["updating"] != float64(2) {
		t.Errorf("--json printed %q (%v)", out.String(), err)
	}

	panel.mu.Lock()
	defer panel.mu.Unlock()
	if len(panel.uploaded) != 2 {
		t.Fatalf("%d uploads reached the panel", len(panel.uploaded))
	}
	for _, body := range panel.uploaded {
		if body["name"] != "intranet" || body["certificate"] != fakeChain || body["private_key"] != fakeKey {
			t.Errorf("the panel was sent %v", body)
		}
	}
}

func TestAKeyOnTheCommandLineIsRefused(t *testing.T) {
	panel := &certsPanel{}
	panel.serve(t)
	chainFile := filepath.Join(t.TempDir(), "fullchain.pem")
	if err := os.WriteFile(chainFile, []byte(fakeChain), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"the key itself": {"add", "x", "--cert", chainFile, "--key", fakeKey},
		"no key":         {"add", "x", "--cert", chainFile},
		"both on stdin":  {"add", "x", "--cert", "-", "--key", "-"},
		"no name":        {"add", "--cert", chainFile, "--key", chainFile},
	} {
		var out bytes.Buffer
		if err := cmdCerts(t.Context(), args, &out); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(panel.uploaded) != 0 {
		t.Fatalf("something refused still reached the panel: %v", panel.uploaded)
	}
}

func TestCertificatesAreListedAndRemovedByName(t *testing.T) {
	panel := &certsPanel{}
	panel.serve(t)

	var out bytes.Buffer
	if err := cmdCerts(t.Context(), nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wildcard", "*.example.com", "Example CA", "2027-01-31", "shop.example.com"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the list does not show %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := cmdCerts(t.Context(), []string{"ls", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var listed []map[string]any
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Errorf("--json printed %q (%v)", out.String(), err)
	}

	out.Reset()
	if err := cmdCerts(t.Context(), []string{"remove", "wildcard", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(panel.deleted) != 1 || !strings.Contains(out.String(), `"removed": "wildcard"`) {
		t.Errorf("removed %v, printed %q", panel.deleted, out.String())
	}
	if err := cmdCerts(t.Context(), []string{"remove", "nothing-by-that-name"}, &out); err == nil {
		t.Error("removing a certificate that does not exist succeeded")
	}
}
