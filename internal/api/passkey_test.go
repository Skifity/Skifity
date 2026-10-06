package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/crypto"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Passkeys, end to end through the handlers the panel serves, with the
// software authenticator in passkey_authenticator_test.go standing in for the
// person's phone. No browser and no real authenticator are involved; what is
// tested is everything the panel does with what they send.

const (
	passkeyAddress  = "https://panel.example.test"
	passkeyRPID     = "panel.example.test"
	passkeyPassword = "correct horse battery staple"
)

// passkeyPanel is a panel at an https address, which is what passkeys need.
func passkeyPanel(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.setPanelURL(t, passkeyAddress)
	return h
}

func (h *harness) setPanelURL(t *testing.T, address string) {
	t.Helper()
	if err := h.db.SetSetting(t.Context(), settings.KeyPanelURL, address, false, "test"); err != nil {
		t.Fatalf("set the panel URL: %v", err)
	}
}

// exchange is one request with whatever cookies a browser holds, answering
// with the cookies the panel set as well.
func (h *harness) exchange(t *testing.T, cookies []*http.Cookie, method, path, csrf string, body any) (int, string, []*http.Cookie) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
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
	return resp.StatusCode, string(out), resp.Cookies()
}

// withDefaults is a ceremony run by an honest browser on the panel's page.
func (c ceremony) withDefaults() ceremony {
	if c.origin == "" {
		c.origin = passkeyAddress
	}
	return c
}

// beginRegistration asks the panel for registration options.
func (h *harness) beginRegistration(t *testing.T, b browser) (int, string, creationOptions) {
	t.Helper()
	status, body := h.send(t, b, http.MethodPost, "/api/me/passkeys/register", b.csrf, nil)
	var options creationOptions
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &options); err != nil {
			t.Fatalf("decode the registration options: %v\n%s", err, body)
		}
	}
	return status, body, options
}

// addPasskey is the account page adding a passkey: options, the
// authenticator, and the answer.
func (h *harness) addPasskey(t *testing.T, b browser, a *softAuthenticator, name string, c ceremony) (int, string) {
	t.Helper()
	status, body, options := h.beginRegistration(t, b)
	if status != http.StatusOK {
		return status, body
	}
	answer := a.create(t, options, c.withDefaults())
	return h.send(t, b, http.MethodPost, "/api/me/passkeys", b.csrf,
		map[string]any{"name": name, "credential": answer})
}

// mustAddPasskey adds a passkey and returns it as the panel answered.
func (h *harness) mustAddPasskey(t *testing.T, b browser, a *softAuthenticator, name string) passkeyView {
	t.Helper()
	status, body := h.addPasskey(t, b, a, name, ceremony{})
	if status != http.StatusCreated {
		t.Fatalf("adding a passkey answered %d: %s", status, body)
	}
	var view passkeyView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode the passkey: %v", err)
	}
	return view
}

// passkeyAttempt is a sign-in page: it begins, hands the options to the
// authenticator, and finishes with the cookie the panel gave it.
type passkeyAttempt struct {
	cookies []*http.Cookie
	options requestOptions
}

func (h *harness) beginPasskeySignIn(t *testing.T, cookies []*http.Cookie) (int, string, passkeyAttempt) {
	t.Helper()
	status, body, set := h.exchange(t, cookies, http.MethodPost, "/api/auth/passkey/begin", "", map[string]any{})
	attempt := passkeyAttempt{cookies: append(append([]*http.Cookie{}, cookies...), set...)}
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &attempt.options); err != nil {
			t.Fatalf("decode the sign-in options: %v\n%s", err, body)
		}
	}
	return status, body, attempt
}

func (h *harness) finishPasskeySignIn(t *testing.T, attempt passkeyAttempt, answer map[string]any) (int, string, browser) {
	t.Helper()
	status, body, set := h.exchange(t, attempt.cookies, http.MethodPost, "/api/auth/passkey/finish", "", answer)
	var signedIn struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal([]byte(body), &signedIn)
	return status, body, browser{cookies: set, csrf: signedIn.CSRFToken}
}

// passkeySignIn is the whole of signing in with a passkey.
func (h *harness) passkeySignIn(t *testing.T, a *softAuthenticator, c ceremony) (int, string, browser) {
	t.Helper()
	status, body, attempt := h.beginPasskeySignIn(t, nil)
	if status != http.StatusOK {
		t.Fatalf("starting a passkey sign-in answered %d: %s", status, body)
	}
	return h.finishPasskeySignIn(t, attempt, a.get(t, attempt.options, c.withDefaults()))
}

// forgetFailures clears the sign-in limit, so one refusal in a table of them
// is not answered by the pause the ones before it caused.
func (h *harness) forgetFailures(t *testing.T) {
	t.Helper()
	if _, err := h.db.Exec(t.Context(), `DELETE FROM login_attempts`); err != nil {
		t.Fatal(err)
	}
}

func problemCode(body string) string {
	var answer struct {
		Error struct {
			Code     string `json:"code"`
			DocsPath string `json:"docs_path"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &answer)
	return answer.Error.Code
}

func sessionCookieIn(cookies []*http.Cookie) bool {
	for _, cookie := range cookies {
		if strings.HasSuffix(cookie.Name, auth.SessionCookieName) && cookie.Value != "" {
			return true
		}
	}
	return false
}

// TestAPasskeyIsAddedAndSignsIn is the whole of the feature once, the way it
// is meant to go: a signed-in person adds a passkey, and later signs in with it
// and nothing else — no address, no password.
func TestAPasskeyIsAddedAndSignsIn(t *testing.T) {
	h := passkeyPanel(t)
	user := h.person(t, "ana@example.test", passkeyPassword)
	b := h.signIn(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)

	// What the panel asks the browser for: a discoverable credential with user
	// verification, for its own hostname, with no attestation.
	status, body, options := h.beginRegistration(t, b)
	if status != http.StatusOK {
		t.Fatalf("starting to add a passkey answered %d: %s", status, body)
	}
	selection := options.PublicKey.AuthenticatorSelection
	if selection.ResidentKey != "required" || !selection.RequireResidentKey || selection.UserVerification != "required" {
		t.Errorf("the options ask for %+v, want a discoverable credential with user verification required", selection)
	}
	if options.PublicKey.RP.ID != passkeyRPID || options.PublicKey.Attestation != "none" {
		t.Errorf("the relying party is %q with attestation %q", options.PublicKey.RP.ID, options.PublicKey.Attestation)
	}
	if handle, _ := b64.DecodeString(options.PublicKey.User.ID); string(handle) != user.ID {
		t.Errorf("the user handle is %q, want the account id", handle)
	}
	if options.PublicKey.Timeout != int((5 * time.Minute).Milliseconds()) {
		t.Errorf("the browser is given %dms, want five minutes", options.PublicKey.Timeout)
	}
	answer := phone.create(t, options, ceremony{}.withDefaults())
	status, body = h.send(t, b, http.MethodPost, "/api/me/passkeys", b.csrf,
		map[string]any{"name": "  Ana's phone  ", "credential": answer})
	if status != http.StatusCreated {
		t.Fatalf("finishing adding a passkey answered %d: %s", status, body)
	}

	// Stored with its public key sealed, and listed under the name given.
	stored, err := h.db.ListPasskeys(t.Context(), user.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored passkeys: %v %+v", err, stored)
	}
	if !crypto.IsEnvelope(stored[0].PublicKeyEnc) || stored[0].Name != "Ana's phone" || stored[0].RPID != passkeyRPID {
		t.Errorf("the passkey was stored as %+v", stored[0])
	}
	if strings.Contains(body, stored[0].PublicKeyEnc) || strings.Contains(body, stored[0].CredentialID) {
		t.Error("the answer carries the key or the credential id, which the page has no use for")
	}

	// A second registration excludes the passkey that is already there, so
	// the same phone is not added twice.
	_, _, again := h.beginRegistration(t, b)
	if len(again.PublicKey.ExcludeCredentials) != 1 || again.PublicKey.ExcludeCredentials[0].ID != b64.EncodeToString(phone.credentialID) {
		t.Errorf("the second registration excludes %+v", again.PublicKey.ExcludeCredentials)
	}

	// Signing in: nothing typed, the passkey says whose it is.
	status, body, signedIn := h.passkeySignIn(t, phone, ceremony{})
	if status != http.StatusOK || !sessionCookieIn(signedIn.cookies) {
		t.Fatalf("signing in with the passkey answered %d: %s", status, body)
	}
	status, body = h.send(t, signedIn, http.MethodGet, "/api/me", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "ana@example.test") {
		t.Fatalf("the passkey session reads /me as %d: %s", status, body)
	}
	// The same session as a password makes, with the CSRF token that goes
	// with it, and marked as a passkey sign-in.
	if status, body := h.send(t, signedIn, http.MethodPatch, "/api/me", signedIn.csrf, map[string]string{"name": "Ana"}); status != http.StatusOK {
		t.Fatalf("the passkey session could not change anything: %d %s", status, body)
	}
	var method string
	if err := h.db.QueryRowContext(t.Context(), `SELECT method FROM sessions WHERE user_id = ? ORDER BY created_at DESC LIMIT 1`,
		user.ID).Scan(&method); err != nil || method != auth.MethodPasskey {
		t.Errorf("the session says it was signed into with %q (%v)", method, err)
	}

	status, body = h.send(t, signedIn, http.MethodGet, "/api/me/passkeys", "", nil)
	if status != http.StatusOK || !strings.Contains(body, `"last_used_at"`) || !strings.Contains(body, `"name":"Ana's phone"`) {
		t.Errorf("the list answered %d: %s", status, body)
	}
	for _, action := range []string{"auth.passkey_added", "auth.login_passkey"} {
		var n int
		if err := h.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE action = ? AND actor_id = ?`,
			action, user.ID).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s was recorded %d times (%v)", action, n, err)
		}
	}
}

// TestAPasskeyAnswerThatDoesNotCheckOutIsRefused: every check the panel makes,
// broken one at a time, each answered the same way to an anonymous caller.
func TestAPasskeyAnswerThatDoesNotCheckOutIsRefused(t *testing.T) {
	h := passkeyPanel(t)
	h.person(t, "ana@example.test", passkeyPassword)
	other := h.person(t, "ben@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), phone, "phone")

	stranger := newSoftAuthenticator(t)
	stranger.userHandle = phone.userHandle
	forger, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// The caller hears the same thing every time. The panel's log says which
	// check it was, and that is what pins each case to its own cause rather
	// than to whichever check happens to run first.
	var log lockedBuffer
	h.api.log = slog.New(slog.NewTextHandler(&log, nil))

	for _, refusal := range []struct {
		name  string
		as    *softAuthenticator
		fault ceremony
		why   string
	}{
		{"an origin that is not the panel's", phone, ceremony{origin: "https://panel.example.test.evil.example"}, "origin"},
		{"the panel's hostname over plain http", phone, ceremony{origin: "http://panel.example.test"}, "origin"},
		{"the hash of another relying party", phone, ceremony{rpID: "evil.example"}, "RP Hash mismatch"},
		{"no user verification", phone, ceremony{flags: flagUserPresent}, "User verification required"},
		{"no user presence", phone, ceremony{flags: flagUserVerified}, "User presence required"},
		{"a signature by another key", phone, ceremony{signWith: forger}, "assertion signature"},
		{"somebody else's user handle", phone, ceremony{userHandle: []byte(other.ID)}, "User handle"},
		{"a registration's client data", phone, ceremony{kind: "webauthn.create"}, "ceremony type"},
		{"a passkey this panel does not know", stranger, ceremony{}, "no passkey here has that id"},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			h.forgetFailures(t)
			log.Reset()
			status, body, signedIn := h.passkeySignIn(t, refusal.as, refusal.fault)
			if status != http.StatusUnauthorized || problemCode(body) != "auth.passkey_refused" {
				t.Fatalf("answered %d: %s", status, body)
			}
			if sessionCookieIn(signedIn.cookies) {
				t.Fatal("a refused sign-in set a session cookie")
			}
			if !strings.Contains(log.String(), refusal.why) {
				t.Fatalf("refused for another reason than %q: %s", refusal.why, log.String())
			}
		})
	}

	// And after all of that, the real thing still works, or the table above
	// passed by refusing everything.
	h.forgetFailures(t)
	if status, body, _ := h.passkeySignIn(t, phone, ceremony{}); status != http.StatusOK {
		t.Fatalf("the honest passkey answered %d: %s", status, body)
	}
}

// TestAPasskeyChallengeWorksOnceForFiveMinutesInOneBrowser: an answer cannot
// be sent twice, cannot be sent late, and cannot be sent from a browser the
// challenge was not given to.
func TestAPasskeyChallengeWorksOnceForFiveMinutesInOneBrowser(t *testing.T) {
	h := passkeyPanel(t)
	h.person(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), phone, "phone")

	// Replayed: the same answer, which worked once.
	_, _, attempt := h.beginPasskeySignIn(t, nil)
	answer := phone.get(t, attempt.options, ceremony{}.withDefaults())
	if status, body, _ := h.finishPasskeySignIn(t, attempt, answer); status != http.StatusOK {
		t.Fatalf("the first answer was refused: %d %s", status, body)
	}
	if status, body, _ := h.finishPasskeySignIn(t, attempt, answer); status != http.StatusBadRequest || problemCode(body) != "auth.passkey_expired" {
		t.Fatalf("the same answer again answered %d: %s", status, body)
	}

	// Expired: answered after the five minutes.
	_, _, attempt = h.beginPasskeySignIn(t, nil)
	if _, err := h.db.Exec(t.Context(), `UPDATE passkey_challenges SET expires_at = ?`,
		store.FormatTime(time.Now().Add(-time.Second))); err != nil {
		t.Fatal(err)
	}
	status, body, _ := h.finishPasskeySignIn(t, attempt, phone.get(t, attempt.options, ceremony{}.withDefaults()))
	if status != http.StatusBadRequest || problemCode(body) != "auth.passkey_expired" {
		t.Fatalf("a late answer answered %d: %s", status, body)
	}

	// Another browser: the challenge was given to one, and the answer comes
	// from one that holds a different cookie, or none.
	_, _, attempt = h.beginPasskeySignIn(t, nil)
	_, _, elsewhere := h.beginPasskeySignIn(t, nil)
	stolen := phone.get(t, attempt.options, ceremony{}.withDefaults())
	if status, body, _ := h.finishPasskeySignIn(t, passkeyAttempt{cookies: elsewhere.cookies}, stolen); status != http.StatusBadRequest {
		t.Fatalf("an answer from another browser answered %d: %s", status, body)
	}
	_, _, attempt = h.beginPasskeySignIn(t, nil)
	if status, body, _ := h.finishPasskeySignIn(t, passkeyAttempt{}, phone.get(t, attempt.options, ceremony{}.withDefaults())); status != http.StatusBadRequest {
		t.Fatalf("an answer with no cookie answered %d: %s", status, body)
	}

	// A challenge for adding a passkey is not one for signing in.
	b := h.signIn(t, "ana@example.test", passkeyPassword)
	_, _, registration := h.beginRegistration(t, b)
	_, _, attempt = h.beginPasskeySignIn(t, nil)
	var signIn requestOptions
	signIn.PublicKey.Challenge, signIn.PublicKey.RPID = registration.PublicKey.Challenge, passkeyRPID
	crossed := phone.get(t, signIn, ceremony{}.withDefaults())
	if status, body, _ := h.finishPasskeySignIn(t, attempt, crossed); status != http.StatusBadRequest {
		t.Fatalf("a registration challenge used to sign in answered %d: %s", status, body)
	}
}

// TestARegistrationChallengeBelongsToItsSession: a challenge to add a passkey
// is for the session that asked, and works once.
func TestARegistrationChallengeBelongsToItsSession(t *testing.T) {
	h := passkeyPanel(t)
	h.person(t, "ana@example.test", passkeyPassword)
	here := h.signIn(t, "ana@example.test", passkeyPassword)
	there := h.signIn(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)

	_, _, options := h.beginRegistration(t, here)
	answer := phone.create(t, options, ceremony{}.withDefaults())
	status, body := h.send(t, there, http.MethodPost, "/api/me/passkeys", there.csrf,
		map[string]any{"name": "phone", "credential": answer})
	if status != http.StatusBadRequest || problemCode(body) != "auth.passkey_expired" {
		t.Fatalf("another session finishing it answered %d: %s", status, body)
	}
	// Taken by that attempt, so not usable afterwards by anybody.
	status, body = h.send(t, here, http.MethodPost, "/api/me/passkeys", here.csrf,
		map[string]any{"name": "phone", "credential": answer})
	if status != http.StatusBadRequest {
		t.Fatalf("a spent registration challenge answered %d: %s", status, body)
	}

	// A registration answer that does not check out is refused and adds
	// nothing: here, a key made for another relying party.
	status, body = h.addPasskey(t, here, newSoftAuthenticator(t), "evil", ceremony{rpID: "evil.example"})
	if status != http.StatusBadRequest || problemCode(body) != "auth.passkey_not_added" {
		t.Fatalf("a registration for another relying party answered %d: %s", status, body)
	}
	status, body = h.addPasskey(t, here, newSoftAuthenticator(t), "no uv", ceremony{flags: flagUserPresent})
	if status != http.StatusBadRequest || problemCode(body) != "auth.passkey_not_added" {
		t.Fatalf("a registration without user verification answered %d: %s", status, body)
	}
	status, body = h.addPasskey(t, here, newSoftAuthenticator(t), "", ceremony{})
	if status != http.StatusBadRequest {
		t.Fatalf("a passkey with no name answered %d: %s", status, body)
	}
	if n, _ := h.db.CountPasskeys(t.Context(), mustUser(t, h, "ana@example.test").ID); n != 0 {
		t.Fatalf("%d passkeys were stored from refused answers", n)
	}
}

func mustUser(t *testing.T, h *harness, email string) store.User {
	t.Helper()
	user, err := h.db.GetUserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

// TestACounterThatGoesBackwardsIsRefusedAndAudited: on an authenticator that
// counts, a counter that does not go up means two copies of the key exist.
func TestACounterThatGoesBackwardsIsRefusedAndAudited(t *testing.T) {
	h := passkeyPanel(t)
	user := h.person(t, "ana@example.test", passkeyPassword)
	key := newSoftAuthenticator(t)
	key.counts = true
	key.counter = 1
	h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), key, "security key")

	for range 2 {
		if status, body, _ := h.passkeySignIn(t, key, ceremony{}); status != http.StatusOK {
			t.Fatalf("a counter going up answered %d: %s", status, body)
		}
	}
	// The key is at 3 now. A copy made when it was at 1 answers with 2.
	for _, counter := range []uint32{2, 3} {
		copied := counter
		status, body, _ := h.passkeySignIn(t, key, ceremony{counter: &copied})
		if status != http.StatusUnauthorized || problemCode(body) != "auth.passkey_refused" {
			t.Fatalf("a counter of %d after 3 answered %d: %s", counter, status, body)
		}
	}
	var n int
	if err := h.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE action = ? AND actor_id = ?`,
		"auth.passkey_clone_suspected", user.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("a possible copy was audited %d times (%v)", n, err)
	}
	stored, _ := h.db.ListPasskeys(t.Context(), user.ID)
	if stored[0].SignCount != 3 {
		t.Fatalf("the stored counter is %d; a refused answer moved it", stored[0].SignCount)
	}

	// A synced passkey does not count at all, and is not a copy for it.
	h.forgetFailures(t)
	synced := newSoftAuthenticator(t)
	synced.flags = flagUserVerified | flagBackupEligible | flagBackupState
	view := h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), synced, "password manager")
	if !view.BackupEligible || !view.BackupState {
		t.Errorf("a synced passkey is listed as %+v", view.Passkey)
	}
	for range 2 {
		if status, body, _ := h.passkeySignIn(t, synced, ceremony{}); status != http.StatusOK {
			t.Fatalf("a passkey that does not count answered %d: %s", status, body)
		}
	}
}

// TestADisabledAccountCannotSignInWithAPasskey.
func TestADisabledAccountCannotSignInWithAPasskey(t *testing.T) {
	h := passkeyPanel(t)
	user := h.person(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), phone, "phone")

	user.Disabled = true
	if err := h.db.UpdateUser(t.Context(), &user); err != nil {
		t.Fatal(err)
	}
	status, body, signedIn := h.passkeySignIn(t, phone, ceremony{})
	if status != http.StatusUnauthorized || problemCode(body) != "auth.passkey_refused" || sessionCookieIn(signedIn.cookies) {
		t.Fatalf("a disabled account answered %d: %s", status, body)
	}
}

// TestAPasskeyMovedToAnotherAccountDoesNotOpen: somebody who can write to the
// database but does not have the master key cannot give themselves a way into
// somebody else's account by moving their own passkey onto it.
func TestAPasskeyMovedToAnotherAccountDoesNotOpen(t *testing.T) {
	h := passkeyPanel(t)
	victim := h.person(t, "owner@example.test", passkeyPassword)
	h.person(t, "mallory@example.test", passkeyPassword)
	theirs := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "mallory@example.test", passkeyPassword), theirs, "mine")

	if _, err := h.db.Exec(t.Context(), `UPDATE passkeys SET user_id = ?`, victim.ID); err != nil {
		t.Fatal(err)
	}
	theirs.userHandle = []byte(victim.ID)
	status, body, signedIn := h.passkeySignIn(t, theirs, ceremony{})
	if status != http.StatusUnauthorized || sessionCookieIn(signedIn.cookies) {
		t.Fatalf("a passkey moved onto the owner's account answered %d: %s", status, body)
	}
}

// TestPasskeysNeedTheirOwnDomainOverHTTPS: on the default install, plain HTTP
// on an sslip.io address, a browser would not offer a passkey, and the panel
// says so rather than handing out options nothing can use.
func TestPasskeysNeedTheirOwnDomainOverHTTPS(t *testing.T) {
	h := newHarness(t)
	h.person(t, "ana@example.test", passkeyPassword)
	b := h.signIn(t, "ana@example.test", passkeyPassword)

	for _, address := range []string{"", "http://203-0-113-10.sslip.io", "https://203.0.113.10", "http://127.0.0.1:8080"} {
		h.setPanelURL(t, address)
		_, meta := h.send(t, browser{}, http.MethodGet, "/api/meta", "", nil)
		if !strings.Contains(meta, `"passkeys":{"available":false}`) {
			t.Errorf("at %q the meta says %s", address, meta)
		}
		status, body, _ := h.beginPasskeySignIn(t, nil)
		if status != http.StatusConflict || problemCode(body) != "auth.passkeys_unavailable" ||
			!strings.Contains(body, "/docs/configuration#put-a-domain-on-the-panel") {
			t.Errorf("at %q a passkey sign-in answered %d: %s", address, status, body)
		}
		if status, body, _ := h.beginRegistration(t, b); status != http.StatusConflict {
			t.Errorf("at %q adding a passkey answered %d: %s", address, status, body)
		}
		// The list still answers, so a passkey from before can be removed.
		if status, _ := h.send(t, b, http.MethodGet, "/api/me/passkeys", "", nil); status != http.StatusOK {
			t.Errorf("at %q the list answered %d", address, status)
		}
	}

	// Localhost is a secure context over plain http, and a domain is fine.
	for _, address := range []string{"http://localhost:8080", passkeyAddress} {
		h.setPanelURL(t, address)
		if _, meta := h.send(t, browser{}, http.MethodGet, "/api/meta", "", nil); !strings.Contains(meta, `"passkeys":{"available":true}`) {
			t.Errorf("at %q the meta says %s", address, meta)
		}
	}
}

// TestAddingAndRemovingAPasskeyAskForThePasswordAgain: a passkey is a new
// way into the account, so a session somebody borrowed cannot add one and keep
// it — nor remove the owner's.
func TestAddingAndRemovingAPasskeyAskForThePasswordAgain(t *testing.T) {
	h := passkeyPanel(t)
	user := h.person(t, "ana@example.test", passkeyPassword)
	b := h.signIn(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)
	view := h.mustAddPasskey(t, b, phone, "phone")

	h.spendReauth(t, "ana@example.test")
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/me/passkeys/register"},
		{http.MethodPost, "/api/me/passkeys"},
		{http.MethodDelete, "/api/me/passkeys/" + view.ID},
	} {
		status, body := h.send(t, b, route.method, route.path, b.csrf, map[string]any{"name": "x"})
		if status != http.StatusForbidden || problemCode(body) != "auth.reauth_required" {
			t.Errorf("%s %s answered %d without a step-up: %s", route.method, route.path, status, body)
		}
	}
	// A name opens nothing, so renaming does not ask.
	if status, body := h.send(t, b, http.MethodPatch, "/api/me/passkeys/"+view.ID, b.csrf,
		map[string]string{"name": "work phone"}); status != http.StatusOK || !strings.Contains(body, "work phone") {
		t.Errorf("renaming answered %d: %s", status, body)
	}

	// An API token has nobody at the keyboard to ask.
	team := store.Team{Name: "acme", Slug: "acme"}
	if err := h.db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatal(err)
	}
	_, token, err := h.auth.CreateAPIToken(t.Context(), user.ID, team.ID, "test", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/me/passkeys/register"},
		{http.MethodDelete, "/api/me/passkeys/" + view.ID},
	} {
		status, body := h.do(tenant{token: token}, route.method, route.path, map[string]any{})
		if status != http.StatusForbidden || problemCode(body) != "auth.needs_person" {
			t.Errorf("%s %s with a token answered %d: %s", route.method, route.path, status, body)
		}
	}

	// With the password given again, both go through.
	if status, body := h.send(t, b, http.MethodPost, "/api/me/reauth", b.csrf,
		map[string]string{"password": passkeyPassword}); status != http.StatusOK {
		t.Fatalf("stepping up answered %d: %s", status, body)
	}
	h.mustAddPasskey(t, b, newSoftAuthenticator(t), "laptop")
	if status, body := h.send(t, b, http.MethodDelete, "/api/me/passkeys/"+view.ID, b.csrf, nil); status != http.StatusOK {
		t.Fatalf("removing a passkey answered %d: %s", status, body)
	}
	if status, body, _ := h.passkeySignIn(t, phone, ceremony{}); status != http.StatusUnauthorized {
		t.Fatalf("a removed passkey still signed in: %d %s", status, body)
	}
	var n int
	if err := h.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE action = 'auth.passkey_removed' AND target_id = ?`,
		view.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("removing it was audited %d times (%v)", n, err)
	}

	// Somebody else's passkey is not there to rename or remove.
	h.person(t, "ben@example.test", passkeyPassword)
	ben := h.signIn(t, "ben@example.test", passkeyPassword)
	stored, _ := h.db.ListPasskeys(t.Context(), user.ID)
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		if status, _ := h.send(t, ben, method, "/api/me/passkeys/"+stored[0].ID, ben.csrf,
			map[string]string{"name": "mine now"}); status != http.StatusNotFound {
			t.Errorf("%s on somebody else's passkey answered %d", method, status)
		}
	}
}

// TestAPasskeySignInIsLimitedLikeAPassword: the same pause, counted the same
// way, whichever of the two is being guessed at.
func TestAPasskeySignInIsLimitedLikeAPassword(t *testing.T) {
	h := passkeyPanel(t)
	h.person(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), phone, "phone")
	limit := auth.DefaultLockout().MaxPerAccount

	// Wrong passwords pause the account, and a passkey does not get round it.
	for range limit {
		status, _, _ := h.exchange(t, nil, http.MethodPost, "/api/auth/login", "",
			map[string]string{"email": "ana@example.test", "password": "wrong"})
		if status != http.StatusUnauthorized {
			t.Fatalf("a wrong password answered %d", status)
		}
	}
	if status, body, _ := h.passkeySignIn(t, phone, ceremony{}); status != http.StatusTooManyRequests {
		t.Fatalf("a passkey on a paused account answered %d: %s", status, body)
	}

	// And answers that do not check out count against the account like wrong
	// passwords do: the password is paused after them.
	h.forgetFailures(t)
	forger, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for i := range limit {
		status, body, _ := h.passkeySignIn(t, phone, ceremony{signWith: forger})
		if status != http.StatusUnauthorized {
			t.Fatalf("forged answer %d answered %d: %s", i+1, status, body)
		}
	}
	status, _, _ := h.exchange(t, nil, http.MethodPost, "/api/auth/login", "",
		map[string]string{"email": "ana@example.test", "password": passkeyPassword})
	if status != http.StatusTooManyRequests {
		t.Fatalf("after %d forged passkey answers the right password answered %d", limit, status)
	}
	if status, _, _ := h.passkeySignIn(t, phone, ceremony{}); status != http.StatusTooManyRequests {
		t.Fatalf("after %d forged passkey answers the right passkey answered %d", limit, status)
	}

	// An address that has failed too often cannot even start one.
	h.forgetFailures(t)
	for range auth.DefaultLockout().MaxPerIPAddress {
		if err := h.db.RecordLoginAttempt(t.Context(), "someone@example.test", "127.0.0.1", false); err != nil {
			t.Fatal(err)
		}
	}
	if status, body, _ := h.beginPasskeySignIn(t, nil); status != http.StatusTooManyRequests {
		t.Fatalf("a paused address started a passkey sign-in: %d %s", status, body)
	}
}

// TestOneAddressCannotHoardPasskeyChallenges: every visit to the sign-in page
// starts one, and nobody has to finish it, so an address may only hold so many.
func TestOneAddressCannotHoardPasskeyChallenges(t *testing.T) {
	h := passkeyPanel(t)
	for i := 0; ; i++ {
		status, body, _ := h.beginPasskeySignIn(t, nil)
		if status == http.StatusTooManyRequests {
			if i < 10 {
				t.Fatalf("refused after only %d", i)
			}
			return
		}
		if status != http.StatusOK || i > 100 {
			t.Fatalf("challenge %d answered %d: %s", i+1, status, body)
		}
	}
}

// TestAPasskeySessionPassesATeamsStrongAuthRequirement: a passkey with user
// verification is two factors, so a team that requires more than a password
// lets it in, and still refuses the same person's password alone.
func TestAPasskeySessionPassesATeamsStrongAuthRequirement(t *testing.T) {
	h := passkeyPanel(t)
	acme := h.newTenant("acme")
	person := h.person(t, "member@example.test", passkeyPassword)
	if err := h.db.AddMember(t.Context(), acme.team.ID, person.ID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	phone := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "member@example.test", passkeyPassword), phone, "phone")
	requireStrongAuth(t, h, acme.team.ID)
	projects := "/api/teams/" + acme.team.ID + "/projects"

	password := h.signIn(t, "member@example.test", passkeyPassword)
	if status, body := h.send(t, password, http.MethodGet, projects, "", nil); status != http.StatusForbidden ||
		problemCode(body) != "auth.strong_auth_required" {
		t.Fatalf("a password alone answered %d: %s", status, body)
	}

	_, _, passkey := h.passkeySignIn(t, phone, ceremony{})
	if status, body := h.send(t, passkey, http.MethodGet, projects, "", nil); status != http.StatusOK {
		t.Fatalf("a passkey session answered %d: %s", status, body)
	}
	_, body := h.send(t, passkey, http.MethodGet, "/api/me", "", nil)
	if !strings.Contains(body, `"strong_auth":true`) {
		t.Fatalf("/me does not say the passkey session is strong: %s", body)
	}
}

// TestTheLastPasskeyOfAnAccountWithNoPasswordStays: an account made through
// single sign-on has no password. Without a provider identity either, its
// passkeys are all it has, and the last one cannot be removed.
func TestTheLastPasskeyOfAnAccountWithNoPasswordStays(t *testing.T) {
	h := passkeyPanel(t)
	user := store.User{Email: "sso@example.test", Name: "sso"}
	if err := h.db.CreateUser(t.Context(), &user); err != nil {
		t.Fatal(err)
	}
	// Signing in counts as proving who you are, so this session may add and
	// remove passkeys without being asked for the password it does not have.
	session, err := h.auth.IssueSession(t.Context(), user, "10.0.0.1", "test", auth.MethodSSO)
	if err != nil {
		t.Fatal(err)
	}
	b := browser{cookies: []*http.Cookie{h.auth.SessionCookie(session.Token, session.ExpiresAt)}, csrf: session.CSRFToken}
	first := h.mustAddPasskey(t, b, newSoftAuthenticator(t), "phone")
	second := h.mustAddPasskey(t, b, newSoftAuthenticator(t), "laptop")

	if status, body := h.send(t, b, http.MethodDelete, "/api/me/passkeys/"+first.ID, b.csrf, nil); status != http.StatusOK {
		t.Fatalf("removing one of two answered %d: %s", status, body)
	}
	status, body := h.send(t, b, http.MethodDelete, "/api/me/passkeys/"+second.ID, b.csrf, nil)
	if status != http.StatusConflict || problemCode(body) != "auth.passkey_last_way_in" {
		t.Fatalf("removing the last one answered %d: %s", status, body)
	}

	// With the identity provider linked there is another way in.
	if err := h.db.LinkIdentity(t.Context(), user.ID, "https://idp.example.test", "sub-1", user.Email); err != nil {
		t.Fatal(err)
	}
	if status, body := h.send(t, b, http.MethodDelete, "/api/me/passkeys/"+second.ID, b.csrf, nil); status != http.StatusOK {
		t.Fatalf("with single sign-on linked, removing the last one answered %d: %s", status, body)
	}
}

// TestAPasswordResetLeavesPasskeysAlone: a reset replaces a forgotten
// password and ends every session; it does not quietly take the account's
// passkeys with it. `skifity admin reset-password --remove-passkeys` does
// that, on purpose; see internal/cli.
func TestAPasswordResetLeavesPasskeysAlone(t *testing.T) {
	h := passkeyPanel(t)
	user := h.person(t, "ana@example.test", passkeyPassword)
	phone := newSoftAuthenticator(t)
	h.mustAddPasskey(t, h.signIn(t, "ana@example.test", passkeyPassword), phone, "phone")

	_, token, ok, err := h.auth.RequestPasswordReset(t.Context(), user.Email, "10.0.0.1")
	if err != nil || !ok {
		t.Fatalf("ask for a reset: %v %v", ok, err)
	}
	if _, err := h.auth.ResetPassword(t.Context(), token, "a brand new passphrase"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if n, _ := h.db.CountPasskeys(t.Context(), user.ID); n != 1 {
		t.Fatalf("the reset left %d passkeys", n)
	}
	if status, body, _ := h.passkeySignIn(t, phone, ceremony{}); status != http.StatusOK {
		t.Fatalf("the passkey after a reset answered %d: %s", status, body)
	}
}
