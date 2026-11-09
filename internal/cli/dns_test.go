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

// `skifity dns providers` connects, lists, tests and removes a team's DNS
// providers through the panel, takes the credentials from a file or standard
// input and never from an argument, and prints JSON when asked. `skifity
// domains add --manage-dns` says so to the panel.
func TestDNSProvidersAreManagedWithCredentialsFromAFile(t *testing.T) {
	const token = "cf-test-token-not-real-000000000000000000"
	var mu sync.Mutex
	var asked []string
	var connected, domain map[string]any
	provider := `{"id":"dnsp_1","kind":"cloudflare","name":"Cloudflare","title":"Cloudflare",
		"zones":[{"id":"z1","name":"example.com"}],"records":2}`
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/me":
			_, _ = io.WriteString(w, `{"teams":[{"id":"team_1","name":"acme"}]}`)
		case "GET /api/teams/team_1/dns-providers":
			_, _ = io.WriteString(w, `{"items":[`+provider+`],"total":1}`)
		case "POST /api/teams/team_1/dns-providers":
			connected = nil
			_ = json.NewDecoder(r.Body).Decode(&connected)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, provider)
		case "POST /api/teams/team_1/dns-providers/dnsp_1/test":
			_, _ = io.WriteString(w, provider)
		case "GET /api/teams/team_1/dns-providers/dnsp_1/zones":
			_, _ = io.WriteString(w, `{"items":[{"id":"z1","name":"example.com"}],"total":1}`)
		case "DELETE /api/teams/team_1/dns-providers/dnsp_1":
			_, _ = io.WriteString(w, `{"ok":true,"left":2}`)
		case "POST /api/apps/app_1/domains":
			domain = nil
			_ = json.NewDecoder(r.Body).Decode(&domain)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"dom_1","hostname":"shop.example.com","tls":true,"status":"pending",
				"managed_dns":{"provider_name":"Cloudflare","zone":"example.com","manage":true,"state":"created",
				"records":[{"type":"A","content":"203.0.113.10"}]}}`)
		default:
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	file := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cmdDNS(t.Context(), []string{"providers", "add", "--kind", "cloudflare", "--credentials", file}, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	if connected["token"] != token || connected["kind"] != "cloudflare" {
		t.Errorf("the panel was sent %v", connected)
	}
	if strings.Contains(out.String(), token) {
		t.Errorf("the token was printed back: %s", out.String())
	}

	// The token itself where a file name goes is refused, and says why.
	err := cmdDNS(t.Context(), []string{"providers", "add", "--kind", "cloudflare", "--credentials", "cfTestTokenNotReal000000000000"}, &out)
	if err == nil || !strings.Contains(err.Error(), "history") {
		t.Errorf("a token on the command line answered %v", err)
	}

	// Route 53, from standard input, in the shape ~/.aws/credentials has.
	dnsStdin = strings.NewReader("[default]\naws_access_key_id = AKIAEXAMPLENOTREAL00\naws_secret_access_key = not/a/real/secret\n[other]\naws_access_key_id = AKIAOTHER\n")
	t.Cleanup(func() { dnsStdin = os.Stdin })
	if err := cmdDNS(t.Context(), []string{"providers", "add", "--kind", "route53", "--credentials", "-"}, &out); err != nil {
		t.Fatalf("add route53: %v", err)
	}
	if connected["access_key_id"] != "AKIAEXAMPLENOTREAL00" || connected["secret_access_key"] != "not/a/real/secret" {
		t.Errorf("the panel was sent %v", connected)
	}

	for _, args := range [][]string{
		{"providers", "--json"},
		{"providers", "test", "cloudflare", "--json"},
		{"providers", "zones", "Cloudflare", "--json"},
		{"providers", "remove", "Cloudflare", "--json"},
	} {
		out.Reset()
		if err := cmdDNS(t.Context(), args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !json.Valid(out.Bytes()) {
			t.Errorf("%v printed %s", args, out.String())
		}
	}
	out.Reset()
	if err := cmdDNS(t.Context(), []string{"providers", "remove", "Cloudflare"}, &out); err != nil || !strings.Contains(out.String(), "2 record(s)") {
		t.Errorf("remove said %q (%v)", out.String(), err)
	}

	out.Reset()
	if err := cmdDomains(t.Context(), []string{"add", "shop.example.com", "--manage-dns", "--app", "app_1"}, &out); err != nil {
		t.Fatalf("domains add: %v", err)
	}
	if domain["manage_dns"] != true || domain["hostname"] != "shop.example.com" {
		t.Errorf("the panel was sent %v", domain)
	}
	if !strings.Contains(out.String(), "created at Cloudflare (A 203.0.113.10)") {
		t.Errorf("domains add said %q", out.String())
	}
	// Left out, the panel decides: the field is not sent.
	if err := cmdDomains(t.Context(), []string{"add", "shop.example.com", "--json", "--app", "app_1"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, sent := domain["manage_dns"]; sent {
		t.Errorf("manage_dns was sent without being asked for: %v", domain)
	}
	if err := cmdDomains(t.Context(), []string{"add", "shop.example.com", "--manage-dns=false", "--app", "app_1"}, &out); err != nil {
		t.Fatal(err)
	}
	if domain["manage_dns"] != false {
		t.Errorf("--manage-dns=false sent %v", domain)
	}
}

func TestAWSKeysAreReadFromEitherShape(t *testing.T) {
	for _, text := range []string{
		"aws_access_key_id=AKIAX\naws_secret_access_key=s3cr3t\n",
		"export AWS_ACCESS_KEY_ID=\"AKIAX\"\nexport AWS_SECRET_ACCESS_KEY='s3cr3t'\n",
		"# a comment\n[default]\naws_access_key_id = AKIAX\naws_secret_access_key = s3cr3t\n",
	} {
		if id, secret := awsKeys([]byte(text)); id != "AKIAX" || secret != "s3cr3t" {
			t.Errorf("%q read as %q, %q", text, id, secret)
		}
	}
}

// `dns providers update` replaces a connection's credentials, from a file or
// standard input and never an argument, in the shape its kind takes.
func TestDNSProviderCredentialsAreReplacedFromAFile(t *testing.T) {
	const token = "cf-test-token-not-real-111111111111111111"
	var mu sync.Mutex
	var sent []map[string]any
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/me":
			_, _ = io.WriteString(w, `{"teams":[{"id":"team_1","name":"acme"}]}`)
		case "GET /api/teams/team_1/dns-providers":
			_, _ = io.WriteString(w, `{"items":[{"id":"dnsp_1","kind":"cloudflare","name":"Cloudflare","title":"Cloudflare",
				"zones":[{"id":"z1","name":"example.com"}],"records":2},{"id":"dnsp_2","kind":"route53","name":"AWS",
				"title":"Amazon Route 53","zones":[{"id":"z2","name":"example.org"}],"records":0}],"total":2}`)
		case "PATCH /api/teams/team_1/dns-providers/dnsp_1", "PATCH /api/teams/team_1/dns-providers/dnsp_2":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = append(sent, body)
			_, _ = io.WriteString(w, `{"id":"dnsp_1","kind":"cloudflare","name":"Cloudflare","title":"Cloudflare",
				"zones":[{"id":"z1","name":"example.com"}],"records":2}`)
		default:
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	file := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cmdDNS(t.Context(), []string{"providers", "update", "Cloudflare", "--credentials", file}, &out); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(out.String(), "Cloudflare has its new credentials") || strings.Contains(out.String(), token) {
		t.Errorf("update printed %q", out.String())
	}

	// Route 53 takes its key pair, here from standard input.
	dnsStdin = strings.NewReader("AWS_ACCESS_KEY_ID=AKIAEXAMPLENOTREAL01\nAWS_SECRET_ACCESS_KEY=not/a/real/secret/either\n")
	t.Cleanup(func() { dnsStdin = os.Stdin })
	out.Reset()
	if err := cmdDNS(t.Context(), []string{"providers", "update", "aws", "--credentials", "-", "--json"}, &out); err != nil {
		t.Fatalf("update route53: %v", err)
	}
	if !json.Valid(out.Bytes()) {
		t.Errorf("--json printed %q", out.String())
	}

	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"an empty file":    {"providers", "update", "Cloudflare", "--credentials", empty},
		"no credentials":   {"providers", "update", "Cloudflare"},
		"the token itself": {"providers", "update", "Cloudflare", "--credentials", "cfTestTokenNotReal000000000000"},
		"a --token flag":   {"providers", "update", "Cloudflare", "--token", token},
		"an unknown one":   {"providers", "update", "Hetzner", "--credentials", file},
	} {
		if err := cmdDNS(t.Context(), args, &out); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("%d replacements reached the panel: %v", len(sent), sent)
	}
	if len(sent[0]) != 1 || sent[0]["token"] != token {
		t.Errorf("the panel was sent %v for Cloudflare", sent[0])
	}
	if sent[1]["access_key_id"] != "AKIAEXAMPLENOTREAL01" || sent[1]["secret_access_key"] != "not/a/real/secret/either" || sent[1]["kind"] != nil {
		t.Errorf("the panel was sent %v for Route 53", sent[1])
	}
}
