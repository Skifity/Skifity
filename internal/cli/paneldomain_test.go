package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// `skifity domains panel NAME` moves the panel and follows the move to the
// end: through the certificate, through the restart, when the panel is away
// for a moment, to the panel answering at its domain. --check changes nothing,
// and --remove goes back.
func TestThePanelIsMovedFromTheCommandLine(t *testing.T) {
	was, wasWait := panelMovePoll, panelMoveWait
	panelMovePoll, panelMoveWait = time.Millisecond, 5*time.Second
	t.Cleanup(func() { panelMovePoll, panelMoveWait = was, wasWait })

	const (
		plain   = `{"url":"http://203-0-113-10.sslip.io","host":"203-0-113-10.sslip.io","scheme":"http","previous":[],"dns_target":"203.0.113.10","movable":true}`
		moving  = `{"url":"http://203-0-113-10.sslip.io","host":"203-0-113-10.sslip.io","scheme":"http","domain":"panel.example.test","state":"moving","previous":[],"dns_target":"203.0.113.10","movable":true}`
		restart = `{"url":"http://203-0-113-10.sslip.io","host":"203-0-113-10.sslip.io","scheme":"http","domain":"panel.example.test","state":"restarting","previous":[],"dns_target":"203.0.113.10","movable":true}`
		moved   = `{"url":"https://panel.example.test","host":"panel.example.test","scheme":"https","domain":"panel.example.test","state":"live","previous":["203-0-113-10.sslip.io"],"dns_target":"203.0.113.10","movable":true}`
	)
	var mu sync.Mutex
	var sent map[string]any
	answers := []string{moving, moving, restart, "", moved}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/panel/domain":
			if len(answers) == 0 {
				_, _ = io.WriteString(w, plain)
				return
			}
			answer := answers[0]
			answers = answers[1:]
			if answer == "" {
				// The panel restarting: the ingress has nothing to send it to.
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
				return
			}
			_, _ = io.WriteString(w, answer)
		case "POST /api/panel/domain":
			sent = nil
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, moving)
		case "POST /api/panel/domain/check":
			_, _ = io.WriteString(w, `{"hostname":"panel.example.test","status":"missing","found":[],"expected":["203.0.113.10"]}`)
		case "DELETE /api/panel/domain":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"url":"http://203-0-113-10.sslip.io","restarting":true}`)
		default:
			http.Error(w, `{"error":{"code":"resource.not_found","title":"no"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	var out bytes.Buffer
	if err := cmdDomains(t.Context(), []string{"panel", "panel.example.test", "--check"}, &out); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(out.String(), "Create an A record for it pointing at 203.0.113.10") {
		t.Errorf("the check said: %s", out.String())
	}
	if sent != nil {
		t.Errorf("checking moved the panel: %v", sent)
	}

	out.Reset()
	if err := cmdDomains(t.Context(), []string{"panel", "panel.example.test"}, &out); err != nil {
		t.Fatalf("move: %v\n%s", err, out.String())
	}
	if sent["domain"] != "panel.example.test" || sent["ignore_dns"] != nil {
		t.Errorf("the panel was sent %v", sent)
	}
	for _, want := range []string{
		"Asking for a certificate for panel.example.test",
		"The certificate is issued. The panel is restarting at https://panel.example.test.",
		"The panel answers at https://panel.example.test now.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the move did not say %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := cmdDomains(t.Context(), []string{"panel"}, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "plain HTTP") || !strings.Contains(out.String(), "domains panel panel.example.com --check") {
		t.Errorf("the status said: %s", out.String())
	}

	out.Reset()
	if err := cmdDomains(t.Context(), []string{"panel", "--remove"}, &out); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(out.String(), "restarting at http://203-0-113-10.sslip.io") {
		t.Errorf("taking the domain off said: %s", out.String())
	}
}

// A move that stops ends the command with why, as the panel said it.
func TestAPanelMoveThatStopsSaysWhy(t *testing.T) {
	was := panelMovePoll
	panelMovePoll = time.Millisecond
	t.Cleanup(func() { panelMovePoll = was })

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state := `"state":"failed","problem":{"code":"panel.domain_certificate_pending","title":"The certificate for the panel's domain has not arrived","severity":"error","retryable":true,"at":"2026-11-09T00:00:00Z"}`
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			state = `"state":"moving"`
		}
		_, _ = io.WriteString(w, `{"url":"http://203-0-113-10.sslip.io","host":"203-0-113-10.sslip.io","scheme":"http","domain":"panel.example.test",`+
			state+`,"previous":[],"dns_target":"203.0.113.10","movable":true}`)
	}))
	t.Cleanup(panel.Close)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("SKIFITY_URL", panel.URL)
	t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
	t.Setenv("SKIFITY_TEAM", "")

	var out bytes.Buffer
	err := cmdDomains(t.Context(), []string{"panel", "panel.example.test", "--ignore-dns"}, &out)
	if err == nil || !strings.Contains(err.Error(), "has not arrived") {
		t.Errorf("a move that stopped answered %v", err)
	}
}
