package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/store"
)

// Signing in, and what a session is allowed to do once it exists.
//
// Every test here is a property somebody could remove without any other test
// noticing: a cookie name, a CSRF token that is checked against the right
// thing, a ceiling on how long a session lives, and the three actions that ask
// for the password again. They run against the router the panel serves.

// person creates an account with a real password and returns it.
func (h *harness) person(t *testing.T, email, password string) store.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := store.User{Email: email, Name: email, PasswordHash: hash}
	if err := h.db.CreateUser(t.Context(), &user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

// browser is a signed-in session: the cookies the panel set, and the CSRF token
// it handed back.
type browser struct {
	cookies []*http.Cookie
	csrf    string
}

// signIn posts to the sign-in endpoint the way the panel's own page does.
func (h *harness) signIn(t *testing.T, email, password string) browser {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		h.server.URL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign in answered %d: %s", resp.StatusCode, payload)
	}
	var answer struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(payload, &answer); err != nil {
		t.Fatalf("decode sign-in: %v", err)
	}
	return browser{cookies: resp.Cookies(), csrf: answer.CSRFToken}
}

// send makes a request as a browser, with whatever CSRF token is given.
func (h *harness) send(t *testing.T, b browser, method, path, csrf string, body any) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// TestTheCSRFTokenIsCheckedAgainstTheSessionNotACookie is the one that matters
// most here.
//
// Double-submit compares a cookie with a header and is only as strong as the
// assumption that nothing else can write this panel's cookies. This panel hosts
// other people's applications, and an operator who puts the panel and a
// wildcard app domain on the same domain has given every app a way to write a
// cookie the panel would read. So the check is against the session.
func TestTheCSRFTokenIsCheckedAgainstTheSessionNotACookie(t *testing.T) {
	h := newHarness(t)
	h.person(t, "csrf@example.test", "correct horse battery staple")
	b := h.signIn(t, "csrf@example.test", "correct horse battery staple")

	// What an attacker who can write cookies on this domain would do: put a
	// value of their choosing in the CSRF cookie and echo the same value in the
	// header, so the two agree with each other.
	forged := browser{csrf: "forged-token"}
	for _, cookie := range b.cookies {
		if strings.Contains(cookie.Name, auth.CSRFCookieName) {
			forged.cookies = append(forged.cookies,
				&http.Cookie{Name: cookie.Name, Value: "forged-token"})
			continue
		}
		forged.cookies = append(forged.cookies, cookie)
	}
	status, body := h.send(t, forged, http.MethodPatch, "/api/me", "forged-token",
		map[string]string{"name": "taken over"})
	if status != http.StatusForbidden {
		t.Fatalf("a CSRF token that only agrees with its own cookie answered %d: %s", status, body)
	}

	// And the real one still works, or this test would pass by refusing
	// everything.
	status, body = h.send(t, b, http.MethodPatch, "/api/me", b.csrf, map[string]string{"name": "me"})
	if status != http.StatusOK {
		t.Fatalf("the session's own CSRF token answered %d: %s", status, body)
	}
}

// TestNoCSRFTokenIsRefused covers the plain case: a cross-site form post
// carries cookies and cannot set a header.
func TestNoCSRFTokenIsRefused(t *testing.T) {
	h := newHarness(t)
	h.person(t, "nocsrf@example.test", "correct horse battery staple")
	b := h.signIn(t, "nocsrf@example.test", "correct horse battery staple")

	status, _ := h.send(t, b, http.MethodPatch, "/api/me", "", map[string]string{"name": "x"})
	if status != http.StatusForbidden {
		t.Fatalf("a request with no CSRF header answered %d, want 403", status)
	}
}

// TestSessionCookiesCarryTheHostPrefixOnHTTPS: the prefix is what stops a page
// on a sibling subdomain writing the panel's cookies, because a browser will
// not store a __Host- cookie that names a Domain at all.
func TestSessionCookiesCarryTheHostPrefixOnHTTPS(t *testing.T) {
	secure := auth.NewService(nil, nil, time.Hour, true)
	for _, cookie := range []*http.Cookie{
		secure.SessionCookie("t", time.Now().Add(time.Hour)),
		secure.CSRFCookie("t", time.Now().Add(time.Hour)),
		secure.SSOStateCookie("t"),
	} {
		if !strings.HasPrefix(cookie.Name, auth.HostPrefix) {
			t.Errorf("%s does not carry the __Host- prefix", cookie.Name)
		}
		// The prefix means nothing unless these hold; a browser rejects the
		// cookie outright when they do not, which would sign everybody out.
		if !cookie.Secure {
			t.Errorf("%s is not Secure, so a browser will not store it", cookie.Name)
		}
		if cookie.Path != "/" {
			t.Errorf("%s has path %q, and __Host- requires /", cookie.Name, cookie.Path)
		}
		if cookie.Domain != "" {
			t.Errorf("%s names a domain, which is exactly what __Host- forbids", cookie.Name)
		}
	}

	// Over plain http the prefix cannot be used, because it requires Secure.
	plain := auth.NewService(nil, nil, time.Hour, false)
	if name := plain.SessionCookie("t", time.Now()).Name; name != auth.SessionCookieName {
		t.Errorf("without https the cookie is %q, want the bare name", name)
	}
}

// TestOnlyTheNameThisPanelSetsIsRead: accepting the unprefixed name as a
// fallback on an https panel would hand back everything the prefix bought —
// the attacker would write that one instead.
func TestOnlyTheNameThisPanelSetsIsRead(t *testing.T) {
	secure := auth.NewService(nil, nil, time.Hour, true)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://panel.example.test/", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	// What a sibling subdomain can still write: the bare name, scoped to the
	// parent domain.
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "tossed-by-an-app"})
	if got := secure.ReadCookie(req, auth.SessionCookieName); got != "" {
		t.Fatalf("an https panel read a cookie under the bare name: %q", got)
	}
	req.AddCookie(&http.Cookie{Name: auth.HostPrefix + auth.SessionCookieName, Value: "ours"})
	if got := secure.ReadCookie(req, auth.SessionCookieName); got != "ours" {
		t.Fatalf("the panel's own cookie read as %q", got)
	}
}

// TestASessionStopsAtItsCeiling: the sliding expiry answers "has this person
// been away", never "how long has this cookie been valid". Without a ceiling a
// session that is used once a day renews forever.
func TestASessionStopsAtItsCeiling(t *testing.T) {
	h := newHarness(t)
	user := h.person(t, "old@example.test", "correct horse battery staple")

	result, err := h.auth.IssueSession(t.Context(), user, "10.0.0.1", "test")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	session, err := h.db.GetSessionByHash(t.Context(), auth.HashToken(result.Token))
	if err != nil {
		t.Fatalf("read session: %v", err)
	}

	// Still inside its sliding expiry — it was touched a moment ago — and past
	// the ceiling.
	born := time.Now().Add(-auth.MaxSessionLife - time.Hour)
	if _, err := h.db.Exec(t.Context(),
		`UPDATE sessions SET created_at = ? WHERE id = ?`, store.FormatTime(born), session.ID); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	if _, _, err := h.auth.Authenticate(t.Context(), result.Token); err == nil {
		t.Fatal("a session older than the ceiling still authenticated")
	}
	if _, err := h.db.GetSessionByHash(t.Context(), auth.HashToken(result.Token)); err == nil {
		t.Error("the expired session was left in the database")
	}
}

// TestTurningTwoFactorOffAsksForThePasswordAgain: a session somebody borrowed
// from an unlocked laptop must not be able to remove the second factor, mint a
// token that outlives itself, or read the recovery codes.
func TestTurningTwoFactorOffAsksForThePasswordAgain(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	h.person(t, "stepup@example.test", password)
	b := h.signIn(t, "stepup@example.test", password)

	// Signing in counts, so the window has to be spent before this means
	// anything. That is what a borrowed session looks like: the sign-in was
	// hours ago.
	h.spendReauth(t, "stepup@example.test")

	for _, route := range []struct {
		method, path string
	}{
		{http.MethodDelete, "/api/me/totp"},
		{http.MethodPost, "/api/me/totp"},
		{http.MethodPost, "/api/me/tokens"},
	} {
		status, body := h.send(t, b, route.method, route.path, b.csrf, map[string]string{"name": "x"})
		if status != http.StatusForbidden {
			t.Errorf("%s %s answered %d without a step-up, want 403: %s",
				route.method, route.path, status, body)
		}
		if !strings.Contains(body, "auth.reauth_required") {
			t.Errorf("%s %s did not say why: %s", route.method, route.path, body)
		}
	}

	// The wrong password does not open it.
	status, _ := h.send(t, b, http.MethodPost, "/api/me/reauth", b.csrf,
		map[string]string{"password": "not the password"})
	if status != http.StatusUnauthorized {
		t.Fatalf("stepping up with the wrong password answered %d, want 401", status)
	}
	status, body := h.send(t, b, http.MethodDelete, "/api/me/totp", b.csrf, nil)
	if status != http.StatusForbidden {
		t.Fatalf("a failed step-up opened the door: %d %s", status, body)
	}

	// The right one does, and the action goes through.
	status, body = h.send(t, b, http.MethodPost, "/api/me/reauth", b.csrf,
		map[string]string{"password": password})
	if status != http.StatusOK {
		t.Fatalf("stepping up with the right password answered %d: %s", status, body)
	}
	status, body = h.send(t, b, http.MethodDelete, "/api/me/totp", b.csrf, nil)
	if status != http.StatusOK {
		t.Fatalf("after a step-up, turning two-factor off answered %d: %s", status, body)
	}
}

// spendReauth puts a session's step-up window in the past, which is what every
// session looks like a few minutes after signing in.
func (h *harness) spendReauth(t *testing.T, email string) {
	t.Helper()
	stale := time.Now().Add(-auth.ReauthWindow - time.Minute)
	if _, err := h.db.Exec(t.Context(), `UPDATE sessions SET reauth_at = ?
		WHERE user_id = (SELECT id FROM users WHERE email = ?)`,
		store.FormatTime(stale), email); err != nil {
		t.Fatalf("age the step-up: %v", err)
	}
}

// TestAnAPITokenCannotTurnTwoFactorOff: a token cannot prove there is anybody
// at the keyboard, and letting one disable two-factor would make the token a
// way around the thing two-factor protects.
func TestAnAPITokenCannotTurnTwoFactorOff(t *testing.T) {
	h := newHarness(t)
	owner := h.newTenant("tokenholder")

	for _, route := range []struct{ method, path string }{
		{http.MethodDelete, "/api/me/totp"},
		{http.MethodPost, "/api/me/tokens"},
		{http.MethodPost, "/api/me/reauth"},
	} {
		status, body := h.do(owner, route.method, route.path, map[string]string{"name": "x"})
		if status != http.StatusForbidden {
			t.Errorf("%s %s with a bearer token answered %d, want 403: %s",
				route.method, route.path, status, body)
		}
		if !strings.Contains(body, "auth.needs_person") {
			t.Errorf("%s %s did not say why: %s", route.method, route.path, body)
		}
	}
}

// TestSigningOutEverywhereKeepsThisDevice: the button exists for the laptop
// left on a train, and it would be useless if it signed this browser out too.
func TestSigningOutEverywhereKeepsThisDevice(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	user := h.person(t, "many@example.test", password)
	here := h.signIn(t, "many@example.test", password)
	elsewhere := h.signIn(t, "many@example.test", password)

	status, body := h.send(t, here, http.MethodDelete, "/api/me/sessions", here.csrf, nil)
	if status != http.StatusOK {
		t.Fatalf("signing out everywhere answered %d: %s", status, body)
	}

	sessions, err := h.db.ListSessions(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("%d sessions left, want only this one", len(sessions))
	}
	if status, _ := h.send(t, elsewhere, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("the other device is still signed in: %d", status)
	}
	if status, _ := h.send(t, here, http.MethodGet, "/api/me", "", nil); status != http.StatusOK {
		t.Errorf("this device was signed out too: %d", status)
	}
}

// TestSteppingUpIsRateLimitedLikeSigningIn: somebody who already holds a
// session and is guessing the password is guessing against the same limit as
// somebody at the sign-in page. Without this, the step-up dialog is an
// unmetered password oracle for an account whose session has been taken —
// which is the exact case it exists for.
func TestSteppingUpIsRateLimitedLikeSigningIn(t *testing.T) {
	const password = "correct horse battery staple"
	h := newHarness(t)
	h.person(t, "guessed@example.test", password)
	b := h.signIn(t, "guessed@example.test", password)
	h.spendReauth(t, "guessed@example.test")

	limit := auth.DefaultLockout().MaxPerAccount
	for i := range limit + 2 {
		status, body := h.send(t, b, http.MethodPost, "/api/me/reauth", b.csrf,
			map[string]string{"password": "wrong"})
		if status == http.StatusTooManyRequests {
			return
		}
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d answered %d: %s", i+1, status, body)
		}
	}
	t.Fatalf("%d wrong passwords in a row and the step-up never paused", limit+2)
}
