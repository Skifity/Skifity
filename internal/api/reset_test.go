package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/settings"
)

// A forgotten password, reset through a link by email.

// mailbox stands in for the mail server: it keeps what was sent.
type mailbox struct {
	mu   sync.Mutex
	sent map[string][]string
}

func (m *mailbox) link(t *testing.T, to string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		bodies := m.sent[to]
		m.mu.Unlock()
		if len(bodies) > 0 {
			body := bodies[len(bodies)-1]
			i := strings.Index(body, "#token=")
			if i < 0 {
				t.Fatalf("the mail has no link: %s", body)
			}
			return strings.Fields(body[i+len("#token="):])[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no mail reached %s", to)
	return ""
}

func (m *mailbox) count(to string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent[to])
}

func withMail(t *testing.T) (*harness, *mailbox) {
	t.Helper()
	h := newHarness(t)
	box := &mailbox{sent: map[string][]string{}}
	previous := sendResetEmail
	sendResetEmail = func(_ *Server, _ context.Context, to, _, body string) error {
		box.mu.Lock()
		defer box.mu.Unlock()
		box.sent[to] = append(box.sent[to], body)
		return nil
	}
	t.Cleanup(func() { sendResetEmail = previous })
	for key, value := range map[string]string{
		settings.KeySMTPHost: "mail.example.test", settings.KeyPanelURL: "https://panel.example.test",
	} {
		if err := h.db.SetSetting(t.Context(), key, value, false, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return h, box
}

func TestAForgottenPasswordIsResetOnceThroughTheLinkByEmail(t *testing.T) {
	h, box := withMail(t)
	h.person(t, "ana@example.test", "the-old-password-1")
	signedIn := h.signIn(t, "ana@example.test", "the-old-password-1")

	status, body := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset", "", map[string]string{"email": "ana@example.test"})
	if status != http.StatusAccepted {
		t.Fatalf("asking answered %d: %s", status, body)
	}
	token := box.link(t, "ana@example.test")

	// Too short spends nothing: the link still works for a good password.
	if status, _ := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset/confirm", "",
		map[string]string{"token": token, "password": "short"}); status != http.StatusBadRequest {
		t.Fatalf("a short password answered %d", status)
	}
	if status, body := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset/confirm", "",
		map[string]string{"token": token, "password": "the-new-password-2"}); status != http.StatusOK {
		t.Fatalf("resetting answered %d: %s", status, body)
	}
	// Once.
	status, body = h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset/confirm", "",
		map[string]string{"token": token, "password": "a-third-password-3"})
	if status != http.StatusBadRequest || !strings.Contains(body, "auth.reset_invalid") {
		t.Fatalf("the link worked twice: %d %s", status, body)
	}

	h.signIn(t, "ana@example.test", "the-new-password-2")
	// Whoever was signed in with the old password is not any more.
	if status, _ := h.send(t, signedIn, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("a session from before the reset still works: %d", status)
	}
}

// The answer is the same for an address with no account, a disabled one and
// a real one, so the page cannot be used to find out who has an account.
func TestAskingForALinkSaysNothingAboutWhoHasAnAccount(t *testing.T) {
	h, box := withMail(t)
	h.person(t, "real@example.test", "a-long-password-1")
	disabled := h.person(t, "gone@example.test", "a-long-password-2")
	disabled.Disabled = true
	if err := h.db.UpdateUser(t.Context(), &disabled); err != nil {
		t.Fatal(err)
	}

	answers := map[string]string{}
	for _, email := range []string{"real@example.test", "nobody@example.test", "gone@example.test"} {
		status, body := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset", "", map[string]string{"email": email})
		answers[email] = http.StatusText(status) + body
	}
	if answers["real@example.test"] != answers["nobody@example.test"] || answers["real@example.test"] != answers["gone@example.test"] {
		t.Errorf("the answers differ: %v", answers)
	}
	box.link(t, "real@example.test")
	if box.count("nobody@example.test") != 0 || box.count("gone@example.test") != 0 {
		t.Error("mail went to an address with no usable account")
	}
}

func TestALinkIsBuiltFromThePanelsAddressNeverTheRequests(t *testing.T) {
	h, box := withMail(t)
	h.person(t, "ana@example.test", "a-long-password-1")
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+"/api/auth/password-reset",
		strings.NewReader(`{"email":"ana@example.test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "attacker.example.test"
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	box.link(t, "ana@example.test")
	box.mu.Lock()
	body := box.sent["ana@example.test"][0]
	box.mu.Unlock()
	if !strings.Contains(body, "https://panel.example.test/reset-password#token=") || strings.Contains(body, "attacker") {
		t.Errorf("the link is not the panel's own: %s", body)
	}
}

func TestAnAccountIsNotFloodedWithLinks(t *testing.T) {
	h, box := withMail(t)
	h.person(t, "ana@example.test", "a-long-password-1")
	for range 5 {
		h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset", "", map[string]string{"email": "ana@example.test"})
	}
	box.link(t, "ana@example.test")
	time.Sleep(50 * time.Millisecond)
	if n := box.count("ana@example.test"); n != 3 {
		t.Errorf("%d links were sent in a row, want 3", n)
	}
	// And only the newest works.
	newest := box.link(t, "ana@example.test")
	var first string
	box.mu.Lock()
	body := box.sent["ana@example.test"][0]
	box.mu.Unlock()
	first = strings.Fields(body[strings.Index(body, "#token=")+len("#token="):])[0]
	if status, _ := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset/confirm", "",
		map[string]string{"token": first, "password": "the-new-password-2"}); status != http.StatusBadRequest {
		t.Errorf("an older link worked: %d", status)
	}
	if status, _ := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset/confirm", "",
		map[string]string{"token": newest, "password": "the-new-password-2"}); status != http.StatusOK {
		t.Errorf("the newest link did not work: %d", status)
	}
}

func TestWithoutAMailServerThereIsNoResetToOffer(t *testing.T) {
	h := newHarness(t)
	status, body := h.send(t, browser{}, http.MethodPost, "/api/auth/password-reset", "", map[string]string{"email": "ana@example.test"})
	if status != http.StatusConflict || !strings.Contains(body, "auth.reset_unavailable") {
		t.Errorf("answered %d: %s", status, body)
	}
	_, meta := h.send(t, browser{}, http.MethodGet, "/api/meta", "", nil)
	if !strings.Contains(meta, `"password_reset":false`) {
		t.Errorf("the sign-in page is told to offer a reset: %s", meta)
	}
}
