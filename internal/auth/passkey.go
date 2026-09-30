package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"skifity/internal/store"
)

// Passkeys: WebAuthn credentials that sign an account in without a password.
//
// A passkey is a key pair the person's device or security key makes for this
// panel's hostname. The private half never leaves the device, and using it
// takes the person's fingerprint, face or device PIN — user verification, which
// the panel requires on every ceremony. So a passkey is two factors on its own,
// something held and something known or been, and it cannot be phished: the
// browser only offers it to the hostname it was made for, and signs that
// origin into every answer.
//
// What the panel checks, on every sign-in, through github.com/go-webauthn: the
// signature over the challenge; that the challenge is one this panel issued, to
// this browser, less than five minutes ago, and has not been answered before;
// the origin the browser signed; the hash of the relying party id; the user
// verification flag; and the signature counter, which on an authenticator that
// counts may only go up. A counter that goes backwards means two copies of the
// key exist, and the sign-in is refused and audited.

// MethodPasskey is a session signed into with a passkey. It counts as strong
// authentication for a team that requires a second factor or single sign-on:
// user verification is required, so a passkey is already two factors.
const MethodPasskey = "passkey"

// PasskeyChallengeTTL is how long a passkey ceremony may take between its two
// halves.
const PasskeyChallengeTTL = 5 * time.Minute

// PasskeyCookieName ties a passkey sign-in to the browser that started it.
//
// The challenge is held in the panel's database; the browser holds this, and
// the challenge can only be answered alongside it. Without it, a page on a
// sibling subdomain could make a victim's browser finish a sign-in with the
// attacker's own passkey — and the victim, signed in as the attacker, would
// type their secrets into somebody else's account. On HTTPS, which passkeys
// need, it carries the __Host- prefix like every other cookie here, so no such
// page can write it.
const PasskeyCookieName = "skifity_passkey"

// maxOpenPasskeySignIns bounds how many unanswered sign-in challenges one
// address may hold at once, so an anonymous caller cannot fill the table.
// Every visit to the sign-in page starts one for the browser's autofill.
const maxOpenPasskeySignIns = 30

var (
	// ErrPasskeyChallengeGone is an answer to a challenge that has expired,
	// was answered already, was never issued, or was issued to another
	// browser or session. One answer for all of them.
	ErrPasskeyChallengeGone = errors.New("the passkey request has expired or was already used")

	// ErrPasskeyRefused is a passkey sign-in that did not check out. Which
	// check failed is for the log, not for the caller: it is anonymous.
	ErrPasskeyRefused = errors.New("the passkey was not accepted")

	// ErrPasskeyCloned is a signature counter that did not go up.
	ErrPasskeyCloned = errors.New("the passkey's signature counter did not go up, so it may have been copied")

	// ErrPasskeyNotAdded is a new passkey whose answer did not check out.
	ErrPasskeyNotAdded = errors.New("the new passkey could not be verified")

	// ErrPasskeyTaken is a credential that is already stored.
	ErrPasskeyTaken = errors.New("that passkey is already on an account")

	// ErrPasskeyLastWayIn is removing the passkey an account with no password
	// and no single sign-on cannot do without.
	ErrPasskeyLastWayIn = errors.New("that passkey is the account's only way in")

	// ErrTooManyPasskeySignIns is an address with too many unanswered
	// sign-in challenges.
	ErrTooManyPasskeySignIns = errors.New("too many passkey sign-ins started from this address")
)

// RelyingParty is who a passkey belongs to: the panel's hostname, and the one
// origin a browser may answer from.
type RelyingParty struct {
	// ID is the hostname. A passkey made for it is offered by the browser to
	// that hostname and nowhere else.
	ID string
	// Origin is scheme, host and port, exactly as the browser reports it.
	Origin string
	// Name is what the browser shows beside the passkey.
	Name string
}

// PanelRelyingParty works out the relying party from the address people reach
// the panel on, and reports false when a browser would not offer a passkey
// there.
//
// A browser offers passkeys only in a secure context — a page reached over
// HTTPS, or one on localhost — and only for a domain name: an IP address is
// never a relying party. The default install is plain HTTP on an sslip.io
// address (ADR-0015), and on it passkeys are unavailable until a domain is put
// on the panel.
func PanelRelyingParty(address, name string) (RelyingParty, bool) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" {
		return RelyingParty{}, false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil || protocol.ValidateRPID(host) != nil {
		return RelyingParty{}, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "https":
	case "http":
		// Plain http is a secure context on localhost and nowhere else.
		if host != "localhost" && !strings.HasSuffix(host, ".localhost") {
			return RelyingParty{}, false
		}
	default:
		return RelyingParty{}, false
	}
	origin := scheme + "://" + host
	if port := parsed.Port(); port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		origin += ":" + port
	}
	return RelyingParty{ID: host, Origin: origin, Name: name}, true
}

// webAuthn configures the library for one relying party. The settings that
// matter are here and nowhere else: a discoverable credential, so signing in
// needs no address typed first; user verification required on every ceremony;
// no attestation asked for, because which brand of authenticator somebody uses
// is not this panel's business; and the five-minute limit enforced by the
// library as well as by the table the challenge waits in.
func (rp RelyingParty) webAuthn() (*webauthn.WebAuthn, error) {
	requireResident := true
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: PasskeyChallengeTTL, TimeoutUVD: PasskeyChallengeTTL}
	return webauthn.New(&webauthn.Config{
		RPID:          rp.ID,
		RPDisplayName: rp.Name,
		RPOrigins:     []string{rp.Origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: &requireResident,
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts:              webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

// passkeyUser is an account as the library sees it. The user handle is the
// account id: it is stored on the authenticator and comes back with every
// sign-in, so it must never change, and it says nothing about the person.
type passkeyUser struct {
	user        store.User
	credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte { return []byte(u.user.ID) }

func (u passkeyUser) WebAuthnName() string { return u.user.Email }

func (u passkeyUser) WebAuthnDisplayName() string {
	if name := strings.TrimSpace(u.user.Name); name != "" {
		return name
	}
	return u.user.Email
}

func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// passkeyContext binds a passkey's public key to its account and credential,
// so a sealed key moved onto another row does not open.
func passkeyContext(userID, credentialID string) string {
	return "passkey:" + userID + ":" + credentialID
}

// credentialOf rebuilds the library's record of a stored passkey.
func credentialOf(p store.Passkey, publicKey []byte) (webauthn.Credential, error) {
	id, err := base64.RawURLEncoding.DecodeString(p.CredentialID)
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("read the credential id: %w", err)
	}
	// Every stored passkey was made with the user present, which is what the
	// raw flags said then; the rest are as stored.
	flags := protocol.FlagUserPresent
	if p.UserVerified {
		flags |= protocol.FlagUserVerified
	}
	if p.BackupEligible {
		flags |= protocol.FlagBackupEligible
	}
	if p.BackupState {
		flags |= protocol.FlagBackupState
	}
	transports := make([]protocol.AuthenticatorTransport, 0, len(p.Transports))
	for _, t := range p.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:                id,
		PublicKey:         publicKey,
		AttestationFormat: p.AttestationFormat,
		Transport:         transports,
		Flags:             webauthn.NewCredentialFlags(flags),
		Authenticator:     webauthn.Authenticator{AAGUID: aaguidBytes(p.AAGUID), SignCount: p.SignCount},
	}, nil
}

// aaguidString and aaguidBytes move an authenticator's model id between its
// sixteen bytes and the dashed form it is usually written in.
func aaguidString(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func aaguidBytes(s string) []byte {
	out := make([]byte, 16)
	if raw, err := hex.DecodeString(strings.ReplaceAll(s, "-", "")); err == nil && len(raw) == 16 {
		copy(out, raw)
	}
	return out
}

// Passkeys lists an account's passkeys.
func (s *Service) Passkeys(ctx context.Context, userID string) ([]store.Passkey, error) {
	return s.db.ListPasskeys(ctx, userID)
}

// BeginPasskeyRegistration starts adding a passkey to a signed-in account. The
// challenge is held in the database, bound to the session asking, and works
// once, for five minutes. The account's existing passkeys are listed for the
// browser to exclude, so the same authenticator is not added twice.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, rp RelyingParty, user store.User, sessionID string) (*protocol.CredentialCreation, error) {
	if sessionID == "" {
		return nil, errors.New("adding a passkey needs a signed-in session")
	}
	wa, err := rp.webAuthn()
	if err != nil {
		return nil, fmt.Errorf("configure passkeys: %w", err)
	}
	existing, err := s.db.ListPasskeys(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(existing))
	for _, p := range existing {
		if id, err := base64.RawURLEncoding.DecodeString(p.CredentialID); err == nil {
			exclude = append(exclude, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: id})
		}
	}
	creation, session, err := wa.BeginRegistration(passkeyUser{user: user},
		webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
	if err != nil {
		return nil, fmt.Errorf("start adding a passkey: %w", err)
	}
	if err := s.holdChallenge(ctx, session, store.PasskeyRegister, user.ID, sessionID, ""); err != nil {
		return nil, err
	}
	return creation, nil
}

// FinishPasskeyRegistration checks the browser's answer and stores the new
// passkey under the name the person gave it.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, rp RelyingParty, user store.User, sessionID, name string, answer []byte) (store.Passkey, error) {
	wa, err := rp.webAuthn()
	if err != nil {
		return store.Passkey{}, fmt.Errorf("configure passkeys: %w", err)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(answer)
	if err != nil {
		return store.Passkey{}, fmt.Errorf("%w: %s", ErrPasskeyNotAdded, reason(err))
	}
	pending, err := s.takeChallenge(ctx, parsed.Response.CollectedClientData.Challenge, store.PasskeyRegister)
	if err != nil {
		return store.Passkey{}, err
	}
	// Issued to this account, in this session, and nobody else's. A session
	// id is not a secret; the session cookie that proves it is.
	if pending.UserID != user.ID || sessionID == "" ||
		subtle.ConstantTimeCompare([]byte(pending.Binding), []byte(sessionID)) != 1 {
		return store.Passkey{}, ErrPasskeyChallengeGone
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(pending.Data), &session); err != nil {
		return store.Passkey{}, fmt.Errorf("read the passkey challenge: %w", err)
	}
	credential, err := wa.CreateCredential(passkeyUser{user: user}, session, parsed)
	if err != nil {
		return store.Passkey{}, fmt.Errorf("%w: %s", ErrPasskeyNotAdded, reason(err))
	}
	// The library refuses an answer without it, since the ceremony required
	// it. Checked again because it is what makes a passkey two factors.
	if !credential.Flags.UserVerified {
		return store.Passkey{}, fmt.Errorf("%w: the authenticator did not verify the person", ErrPasskeyNotAdded)
	}

	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	sealed, err := s.keyring.Seal(credential.PublicKey, passkeyContext(user.ID, credentialID))
	if err != nil {
		return store.Passkey{}, fmt.Errorf("seal the passkey: %w", err)
	}
	transports := make([]string, 0, len(credential.Transport))
	for _, t := range credential.Transport {
		transports = append(transports, string(t))
	}
	rpID := session.RelyingPartyID
	if rpID == "" {
		rpID = rp.ID
	}
	passkey := store.Passkey{
		ID:                store.NewID("pky"),
		UserID:            user.ID,
		CredentialID:      credentialID,
		PublicKeyEnc:      sealed,
		RPID:              rpID,
		SignCount:         credential.Authenticator.SignCount,
		AAGUID:            aaguidString(credential.Authenticator.AAGUID),
		Transports:        transports,
		AttestationFormat: credential.AttestationFormat,
		UserVerified:      credential.Flags.UserVerified,
		BackupEligible:    credential.Flags.BackupEligible,
		BackupState:       credential.Flags.BackupState,
		Name:              name,
	}
	if err := s.db.CreatePasskey(ctx, &passkey); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.Passkey{}, ErrPasskeyTaken
		}
		return store.Passkey{}, err
	}
	return passkey, nil
}

// BeginPasskeySignIn starts a sign-in with a passkey, for somebody nobody knows
// yet: the browser lets the person pick one of the passkeys it has for this
// hostname, and that passkey says whose it is. The challenge is bound to the
// browser through binding, the value of its passkey cookie.
//
// An address that sign-in has already paused for is refused here too, and so
// is one holding too many unanswered challenges.
func (s *Service) BeginPasskeySignIn(ctx context.Context, rp RelyingParty, binding, ip string) (*protocol.CredentialAssertion, error) {
	if binding == "" {
		return nil, errors.New("a passkey sign-in needs a browser to bind it to")
	}
	if err := s.checkLockout(ctx, "", ip); err != nil {
		return nil, err
	}
	open, err := s.db.CountOpenPasskeySignIns(ctx, ip, time.Now())
	if err != nil {
		return nil, err
	}
	if open >= maxOpenPasskeySignIns {
		return nil, ErrTooManyPasskeySignIns
	}
	wa, err := rp.webAuthn()
	if err != nil {
		return nil, fmt.Errorf("configure passkeys: %w", err)
	}
	assertion, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, fmt.Errorf("start a passkey sign-in: %w", err)
	}
	if err := s.holdChallenge(ctx, session, store.PasskeySignIn, "", HashToken(binding), ip); err != nil {
		return nil, err
	}
	return assertion, nil
}

// PasskeySignIn is a finished passkey sign-in. User and Passkey are filled in
// as soon as the answer names a passkey that exists, so a refusal can still be
// audited against the account it was for.
type PasskeySignIn struct {
	Result  LoginResult
	User    store.User
	Passkey store.Passkey
}

// FinishPasskeySignIn checks a browser's answer and, when it is right, issues
// a session exactly like a password sign-in does.
//
// It is limited the way a password is: an address that has failed too often is
// refused before anything is read, and an account that has, once the passkey
// has said whose it is. A failure counts against both. A disabled account is
// refused with the same answer as a wrong passkey.
func (s *Service) FinishPasskeySignIn(ctx context.Context, rp RelyingParty, binding string, answer []byte, ip, userAgent string) (PasskeySignIn, error) {
	var out PasskeySignIn
	if err := s.checkLockout(ctx, "", ip); err != nil {
		return out, err
	}
	wa, err := rp.webAuthn()
	if err != nil {
		return out, fmt.Errorf("configure passkeys: %w", err)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(answer)
	if err != nil {
		s.recordFailure(ctx, "", ip)
		return out, fmt.Errorf("%w: %s", ErrPasskeyRefused, reason(err))
	}
	pending, err := s.takeChallenge(ctx, parsed.Response.CollectedClientData.Challenge, store.PasskeySignIn)
	if err != nil {
		return out, err
	}
	if binding == "" || subtle.ConstantTimeCompare([]byte(pending.Binding), []byte(HashToken(binding))) != 1 {
		return out, ErrPasskeyChallengeGone
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(pending.Data), &session); err != nil {
		return out, fmt.Errorf("read the passkey challenge: %w", err)
	}

	// The library asks this for the account once it has read which passkey
	// answered. Everything it learns is kept for the caller, and a failure
	// that is the panel's own is kept apart from a refusal.
	var lockedOut, broken error
	find := func(rawID, _ []byte) (webauthn.User, error) {
		passkey, err := s.db.PasskeyByCredentialID(ctx, base64.RawURLEncoding.EncodeToString(rawID))
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New("no passkey here has that id")
		}
		if err != nil {
			broken = err
			return nil, err
		}
		user, err := s.db.GetUser(ctx, passkey.UserID)
		if err != nil {
			broken = err
			return nil, err
		}
		out.User, out.Passkey = user, passkey
		if err := s.checkLockout(ctx, user.Email, ip); err != nil {
			lockedOut = err
			return nil, err
		}
		publicKey, err := s.keyring.Open(passkey.PublicKeyEnc, passkeyContext(passkey.UserID, passkey.CredentialID))
		if err != nil {
			// A key that does not open was written by something other than
			// this panel, or moved from another account.
			return nil, fmt.Errorf("the passkey's public key does not open: %w", err)
		}
		credential, err := credentialOf(passkey, publicKey)
		if err != nil {
			return nil, err
		}
		return passkeyUser{user: user, credentials: []webauthn.Credential{credential}}, nil
	}

	_, credential, err := wa.ValidatePasskeyLogin(find, session, parsed)
	switch {
	case lockedOut != nil:
		return out, lockedOut
	case broken != nil:
		return out, broken
	case err != nil:
		s.recordFailure(ctx, out.User.Email, ip)
		return out, fmt.Errorf("%w: %s", ErrPasskeyRefused, reason(err))
	}

	flags := parsed.Response.AuthenticatorData.Flags
	if !flags.HasUserVerified() {
		s.recordFailure(ctx, out.User.Email, ip)
		return out, fmt.Errorf("%w: the authenticator did not verify the person", ErrPasskeyRefused)
	}
	if credential.Authenticator.CloneWarning {
		s.recordFailure(ctx, out.User.Email, ip)
		return out, ErrPasskeyCloned
	}
	if out.User.Disabled {
		s.recordFailure(ctx, out.User.Email, ip)
		return out, fmt.Errorf("%w: the account is disabled", ErrPasskeyRefused)
	}
	// Checked again as it is written, so two sign-ins racing with one copied
	// key cannot both get past a counter each read before the other wrote.
	advanced, err := s.db.RecordPasskeyUse(ctx, out.Passkey.ID,
		parsed.Response.AuthenticatorData.Counter, flags.HasBackupState(), time.Now())
	if err != nil {
		return out, err
	}
	if !advanced {
		s.recordFailure(ctx, out.User.Email, ip)
		return out, ErrPasskeyCloned
	}

	result, err := s.issueSession(ctx, out.User, ip, userAgent, MethodPasskey)
	if err != nil {
		return out, err
	}
	_ = s.db.RecordLoginAttempt(ctx, out.User.Email, ip, true)
	_ = s.db.ClearLoginAttempts(ctx, out.User.Email)
	_ = s.db.TouchUserLogin(ctx, out.User.ID)
	out.Result = result
	return out, nil
}

// RenamePasskey changes the name of one of an account's passkeys.
func (s *Service) RenamePasskey(ctx context.Context, userID, id, name string) (store.Passkey, error) {
	if err := s.db.RenamePasskey(ctx, id, userID, name); err != nil {
		return store.Passkey{}, err
	}
	return s.db.GetPasskey(ctx, id, userID)
}

// RemovePasskey removes one of an account's passkeys and returns it.
//
// An account with no password and no single sign-on keeps its last one: it
// would have nothing left to sign in with. Such an account exists when it was
// made through the identity provider before identities were recorded.
func (s *Service) RemovePasskey(ctx context.Context, user store.User, id string) (store.Passkey, error) {
	passkey, err := s.db.GetPasskey(ctx, id, user.ID)
	if err != nil {
		return store.Passkey{}, err
	}
	keepOne := false
	if user.PasswordHash == "" {
		identities, err := s.db.IdentitiesForUser(ctx, user.ID)
		if err != nil {
			return store.Passkey{}, err
		}
		keepOne = len(identities) == 0
	}
	if err := s.db.DeletePasskey(ctx, id, user.ID, keepOne); err != nil {
		if errors.Is(err, store.ErrLastPasskey) {
			return store.Passkey{}, ErrPasskeyLastWayIn
		}
		return store.Passkey{}, err
	}
	return passkey, nil
}

// reason says why the library refused an answer, for the operator's log: the
// check that failed, and what it found. Nothing in either is a secret — an
// origin, a hash of a hostname, a flag, a challenge that is spent.
func reason(err error) string {
	var refusal *protocol.Error
	if errors.As(err, &refusal) && refusal.DevInfo != "" {
		return refusal.Details + " (" + refusal.DevInfo + ")"
	}
	return err.Error()
}

// PasskeyCookie ties a passkey sign-in to this browser. Strict, because both
// halves are requests the panel's own page makes; HttpOnly, because no script
// has any business with it.
func (s *Service) PasskeyCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     s.CookieName(PasskeyCookieName),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(PasskeyChallengeTTL.Seconds()),
	}
}

// holdChallenge keeps a ceremony's challenge until it is answered.
func (s *Service) holdChallenge(ctx context.Context, session *webauthn.SessionData, kind, userID, binding, ip string) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("record the passkey challenge: %w", err)
	}
	return s.db.SavePasskeyChallenge(ctx, store.PasskeyChallenge{
		ChallengeHash: HashToken(session.Challenge),
		Kind:          kind,
		UserID:        userID,
		Binding:       binding,
		IP:            ip,
		Data:          string(data),
		ExpiresAt:     time.Now().Add(PasskeyChallengeTTL),
	})
}

// takeChallenge spends the challenge an answer names. It is gone afterwards
// whatever the answer turns out to be, so an answer cannot be sent twice.
func (s *Service) takeChallenge(ctx context.Context, challenge, kind string) (store.PasskeyChallenge, error) {
	if challenge == "" {
		return store.PasskeyChallenge{}, ErrPasskeyChallengeGone
	}
	pending, err := s.db.TakePasskeyChallenge(ctx, HashToken(challenge), kind, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return store.PasskeyChallenge{}, ErrPasskeyChallengeGone
	}
	return pending, err
}
