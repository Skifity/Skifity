package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"skifity/internal/crypto"
	"skifity/internal/store"
)

// A forgotten password, reset through a link sent to the account's address.
//
// The only way back in used to be `skifity admin reset-password` on the
// server itself — fine for the person who installed the panel, no help at all
// to anybody else on it. Coolify resets by email; this does too, with the
// usual care: the answer is the same whether or not the address has an
// account, the link is good once and for half an hour, only its hash is
// stored, the account's sessions end when it is used, and asking is limited.

// ResetLinkTTL is how long a reset link works.
const ResetLinkTTL = 30 * time.Minute

// Limits on asking. Per account so an address cannot be flooded with mail;
// per address so one caller cannot walk a list of addresses.
const (
	resetsPerAccount = 3
	resetsPerAddress = 10
	resetWindow      = time.Hour
)

// ErrResetInvalid is a link that was used, has expired, or never existed —
// one answer for all three, because which one it was is nobody's business.
var ErrResetInvalid = errors.New("this reset link cannot be used")

// ErrResetLimited is a caller who has asked for too many links.
var ErrResetLimited = errors.New("too many reset links asked for")

// RequestPasswordReset makes a reset link for the account at an address.
//
// It returns the user and the token to send, or ok false when there is
// nothing to send: no such account, a disabled one, or one that has asked too
// often. The caller answers the same way in every case. Only a caller asking
// from one address too often is told, which says nothing about any account.
func (s *Service) RequestPasswordReset(ctx context.Context, email, ip string) (user store.User, token string, ok bool, err error) {
	since := time.Now().Add(-resetWindow)
	byAddress, _, err := s.db.CountFailedLogins(ctx, "reset-from:"+ip, "", since)
	if err != nil {
		return store.User{}, "", false, err
	}
	if byAddress >= resetsPerAddress {
		return store.User{}, "", false, ErrResetLimited
	}
	// Counted as attempts that are not sign-ins, without an address, so a
	// few forgotten passwords never lock anybody out of signing in.
	_ = s.db.RecordLoginAttempt(ctx, "reset-from:"+ip, "", false)

	email = strings.TrimSpace(email)
	byAccount, _, err := s.db.CountFailedLogins(ctx, "reset:"+email, "", since)
	if err != nil {
		return store.User{}, "", false, err
	}
	if byAccount >= resetsPerAccount {
		return store.User{}, "", false, nil
	}
	_ = s.db.RecordLoginAttempt(ctx, "reset:"+email, "", false)

	user, err = s.db.GetUserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) || (err == nil && user.Disabled) {
		return store.User{}, "", false, nil
	}
	if err != nil {
		return store.User{}, "", false, err
	}
	token, err = crypto.RandomToken(32)
	if err != nil {
		return store.User{}, "", false, err
	}
	if err := s.db.CreatePasswordReset(ctx, user.ID, HashToken(token), time.Now().Add(ResetLinkTTL)); err != nil {
		return store.User{}, "", false, err
	}
	return user, token, true, nil
}

// ResetPassword sets a new password through a reset link, and ends every
// session the account had: whoever else was signed in with the old password
// is not any more.
//
// A second factor is not skipped: an account with two-factor still needs its
// code to sign in afterwards. A reset link proves the mailbox, not the phone.
func (s *Service) ResetPassword(ctx context.Context, token, next string) (store.User, error) {
	// The policy first, so a password that is too short does not spend the
	// link and leave its owner asking for another.
	if err := DefaultPasswordPolicy().Check(next); err != nil {
		return store.User{}, err
	}
	if token == "" {
		return store.User{}, ErrResetInvalid
	}
	userID, err := s.db.UsePasswordReset(ctx, HashToken(token), time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrResetInvalid
	}
	if err != nil {
		return store.User{}, err
	}
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return store.User{}, err
	}
	if user.Disabled {
		return store.User{}, ErrResetInvalid
	}
	hash, err := s.hashPassword(ctx, next)
	if err != nil {
		return store.User{}, err
	}
	user.PasswordHash = hash
	if err := s.db.UpdateUser(ctx, &user); err != nil {
		return store.User{}, err
	}
	if err := s.db.DeleteUserSessions(ctx, user.ID); err != nil {
		return store.User{}, err
	}
	// The lockout a forgotten password probably caused goes with it.
	_ = s.db.ClearLoginAttempts(ctx, user.Email)
	return user, nil
}
