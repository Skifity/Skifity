package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// The admin commands that change an account, offline, as root on the server.
//
// They are the way back when the panel cannot help: the phone and the
// recovery codes are both gone, or the only administrator has left. Each one
// writes what it did to the activity log, because a change nobody can see is
// a change nobody can question afterwards.

// adminActor is who the activity log says acted. It is the command rather
// than a description of one, so it reads the same in every language the panel
// is shown in.
var adminActor = version.Binary + " admin"

// noSuchUser is an address nobody on this panel uses. One constructor, so
// every admin command says it in the same words.
func noSuchUser(email string) *errdoc.Problem {
	return errdoc.New("admin.no_such_user", "No account with that address").
		WithCause("Nobody on this panel uses %s.", email).
		WithImpact("Nothing was changed.").
		WithFix("Run `%s admin list-users` to see the accounts that do exist.", version.Binary)
}

// accountByEmail reads the account an admin command names.
func accountByEmail(ctx context.Context, db *store.DB, email string) (store.User, error) {
	user, err := db.GetUserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, noSuchUser(email)
	}
	return user, err
}

// oneAddress parses an admin command that takes one account's address and
// its flags, in either order, the way reset-password always has. Each command
// defines its own flags, so that what it takes is written where it runs.
func oneAddress(command string, flags *flag.FlagSet, args []string) (string, error) {
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return "", err
	}
	if len(positional) != 1 {
		return "", errdoc.BadRequest(fmt.Sprintf(
			"Give the email address of the account, for example `%s admin %s you@example.com`.",
			version.Binary, command))
	}
	return strings.ToLower(strings.TrimSpace(positional[0])), nil
}

// recordAccountChange writes an admin command's change into the activity log
// of every team the account is in.
//
// Once per team rather than once for the panel: a panel-wide entry with nobody
// behind it is shown to every team's admins, and an account's address is not
// theirs to see unless the account is one of their own. An account in no team
// has nobody else to show it to, so it gets the one panel-wide entry.
func recordAccountChange(ctx context.Context, db *store.DB, action string, user store.User) error {
	teams, err := db.ListTeamsForUser(ctx, user.ID)
	if err != nil {
		return err
	}
	ids := []string{""}
	if len(teams) > 0 {
		ids = ids[:0]
		for _, team := range teams {
			ids = append(ids, team.ID)
		}
	}
	for _, teamID := range ids {
		if err := db.RecordAudit(ctx, &store.AuditEvent{
			TeamID: teamID, ActorLabel: adminActor, Action: action,
			TargetType: "user", TargetID: user.ID, TargetLabel: user.Email,
		}); err != nil {
			return err
		}
	}
	return nil
}

// strongAuthTeams names the teams of an account that refuse a sign-in with a
// password alone.
func strongAuthTeams(ctx context.Context, db *store.DB, userID string) ([]string, error) {
	teams, err := db.ListTeamsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, team := range teams {
		if team.RequireStrongAuth {
			names = append(names, team.Name)
		}
	}
	return names, nil
}

// adminResetTwoFactor turns an account's two-factor authentication off, for
// somebody who has lost the phone and the recovery codes with it.
//
// The account page cannot do it — turning two-factor off there needs a code —
// and for the panel's only administrator nobody else can either.
func adminResetTwoFactor(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("reset-2fa", flag.ContinueOnError)
	flags.SetOutput(out)
	databasePath := flags.String("database", "", "the panel database to use")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	email, err := oneAddress("reset-2fa", flags, args)
	if err != nil {
		return err
	}
	db, err := openPanelDatabase(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	user, err := accountByEmail(ctx, db, email)
	if err != nil {
		return err
	}
	if err := db.ResetSecondFactor(ctx, user.ID); err != nil {
		return err
	}
	// Whoever has the phone may have a session too. Signing everything out
	// makes the reset the end of their access, not just the start of ours.
	if err := db.DeleteUserSessions(ctx, user.ID); err != nil {
		return fmt.Errorf("two-factor authentication was reset, but the account's sessions could not be ended: %w", err)
	}
	if err := recordAccountChange(ctx, db, "account.two_factor_reset", user); err != nil {
		return fmt.Errorf("two-factor authentication was reset, but the activity log could not record it: %w", err)
	}
	strict, err := strongAuthTeams(ctx, db, user.ID)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(out, map[string]any{
			"email": user.Email, "two_factor_was_on": user.TOTPEnabled,
			"sessions_ended": true, "strong_auth_teams": strict,
		})
	}
	if user.TOTPEnabled {
		fmt.Fprintf(out, "\nTwo-factor authentication is off for %s, its recovery codes are gone, and every\n"+
			"signed-in device was signed out. The account signs in without a code now, and can\n"+
			"turn two-factor on again under Account, which gives it new recovery codes.\n", user.Email)
	} else {
		fmt.Fprintf(out, "\nTwo-factor authentication was already off for %s. Every signed-in device was\n"+
			"signed out.\n", user.Email)
	}
	if len(strict) > 0 {
		// Said, not refused: the person may be about to turn two-factor on
		// again, or sign in with a passkey, and either passes.
		fmt.Fprintf(out, "Teams that ask for a second factor, a passkey or single sign-on refuse this account\n"+
			"signed in with a password alone until two-factor is on again: %s.\n", strings.Join(strict, ", "))
	}
	fmt.Fprintln(out)
	return nil
}

// adminChangeAccount is disable, enable, make-admin and revoke-admin: the way
// back when the panel's only administrator has gone, or somebody who left can
// still sign in and nobody left can stop them. The panel's last enabled
// administrator is kept here as it is in the panel.
func adminChangeAccount(ctx context.Context, command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	databasePath := flags.String("database", "", "the panel database to use")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	email, err := oneAddress(command, flags, args)
	if err != nil {
		return err
	}
	db, err := openPanelDatabase(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	user, err := accountByEmail(ctx, db, email)
	if err != nil {
		return err
	}
	var changed bool
	var action string
	switch command {
	case "disable":
		changed, action = !user.Disabled, "account.disabled"
		err = db.SetUserDisabled(ctx, user.ID, true)
	case "enable":
		changed, action = user.Disabled, "account.enabled"
		err = db.SetUserDisabled(ctx, user.ID, false)
	case "make-admin":
		changed, action = !user.IsAdmin, "account.admin_granted"
		err = db.SetUserAdmin(ctx, user.ID, true)
	case "revoke-admin":
		changed, action = user.IsAdmin, "account.admin_revoked"
		err = db.SetUserAdmin(ctx, user.ID, false)
	default:
		return errdoc.BadRequest(fmt.Sprintf("%q is not an admin command. Try `%s admin help`.", command, version.Binary))
	}
	if errors.Is(err, store.ErrLastAdmin) {
		return errdoc.AccountLastAdmin()
	}
	if err != nil {
		return err
	}
	if command == "disable" {
		// Every way in refuses a disabled account; ending the sessions now
		// means somebody in the middle of using one is out at once.
		if err := db.DeleteUserSessions(ctx, user.ID); err != nil {
			return fmt.Errorf("the account was disabled, but its sessions could not be ended: %w", err)
		}
	}
	if changed {
		if err := recordAccountChange(ctx, db, action, user); err != nil {
			return fmt.Errorf("the account was changed, but the activity log could not record it: %w", err)
		}
	}
	after, err := db.GetUser(ctx, user.ID)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(out, map[string]any{
			"email": after.Email, "changed": changed,
			"is_admin": after.IsAdmin, "disabled": after.Disabled,
		})
	}
	fmt.Fprintln(out)
	switch {
	case !changed:
		already := map[string]string{
			"disable": "is already disabled", "enable": "is not disabled",
			"make-admin": "is already a panel administrator", "revoke-admin": "is not a panel administrator",
		}[command]
		fmt.Fprintf(out, "Nothing was changed: %s %s.\n", after.Email, already)
	case command == "disable":
		fmt.Fprintf(out, "%s is disabled, and every signed-in device was signed out. Its password,\n"+
			"passkeys, single sign-on and API tokens stop working until `%s admin enable %s`.\n",
			after.Email, version.Binary, after.Email)
	case command == "enable":
		fmt.Fprintf(out, "%s can sign in again.\n", after.Email)
	case command == "make-admin":
		fmt.Fprintf(out, "%s is a panel administrator: it can change panel-wide settings, upgrade the\n"+
			"panel, install plugins and read the recovery key.\n", after.Email)
	case command == "revoke-admin":
		fmt.Fprintf(out, "%s is no longer a panel administrator. It keeps its teams.\n", after.Email)
	}
	if after.IsAdmin && after.Disabled {
		fmt.Fprintf(out, "It is disabled, so it cannot sign in until `%s admin enable %s`.\n",
			version.Binary, after.Email)
	}
	fmt.Fprintln(out)
	return nil
}
