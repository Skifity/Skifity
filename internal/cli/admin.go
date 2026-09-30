package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"skifity/internal/auth"
	"skifity/internal/config"
	"skifity/internal/errdoc"
	"skifity/internal/pluginstore"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Admin commands work on the panel's files directly, not through the API.
//
// They exist for the situation the API cannot help with: nobody can sign in. A
// self-hosted panel has no mail server it can trust, so there is no emailed
// reset link; the way back in is to be root on the server, which is the same
// level of access that could read the database anyway.
func cmdAdmin(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(out, `%s admin - recover access from the server

Usage:
  %s admin reset-password <email>   set a new password for an account
  %s admin list-users               show the accounts on this panel
  %s admin backup-db <path>         write a consistent copy of the database
  %s admin plugin-key               make a signing key for a plugin store
  %s admin plugin-sign <key> <file> sign a plugin store index

These run on the server the panel is installed on and read its database
directly, so they work when nobody can sign in. They need to be run as root.
`, version.Binary, version.Binary, version.Binary, version.Binary, version.Binary, version.Binary)
		return nil
	}

	switch args[0] {
	case "reset-password":
		return adminResetPassword(ctx, args[1:], out)
	case "list-users":
		return adminListUsers(ctx, args[1:], out)
	case "backup-db":
		return adminBackupDatabase(ctx, args[1:], out)
	case "plugin-key":
		return adminPluginKey(args[1:], out)
	case "plugin-sign":
		return adminPluginSign(args[1:], out)
	default:
		return errdoc.BadRequest(fmt.Sprintf("%q is not an admin command. Try `%s admin help`.",
			args[0], version.Binary))
	}
}

// openPanelDatabase opens the panel's own database, wherever it is configured
// to live. An empty override means "wherever the panel would look".
func openPanelDatabase(ctx context.Context, override string) (*store.DB, error) {
	path := override
	if path == "" {
		cfg, err := config.Load("")
		if err != nil {
			return nil, err
		}
		path = cfg.DatabasePath
	}
	if _, err := os.Stat(path); err != nil {
		return nil, errdoc.New("admin.no_database", "The panel's database is not here").
			WithCause("There is no file at %s.", path).
			WithImpact("Nothing was changed.").
			WithFix("Run this on the server the panel is installed on, as root. If the database is somewhere else, pass --database, or set SKIFITY_DATABASE_PATH.")
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func adminResetPassword(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	flags.SetOutput(out)
	databasePath := flags.String("database", "", "the panel database to use")
	password := flags.String("password", "", "the new password; read from the terminal when not given")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errdoc.BadRequest(fmt.Sprintf("Give the email address of the account to reset, for example `%s admin reset-password you@example.com`.", version.Binary))
	}
	email := strings.ToLower(strings.TrimSpace(flags.Arg(0)))

	db, err := openPanelDatabase(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errdoc.New("admin.no_such_user", "No account with that address").
				WithCause("Nobody on this panel uses %s.", email).
				WithImpact("Nothing was changed.").
				WithFix("Run `%s admin list-users` to see the accounts that do exist.", version.Binary)
		}
		return err
	}

	next := *password
	if next == "" {
		next = promptSecret(out, "New password: ")
		again := promptSecret(out, "Again: ")
		fmt.Fprintln(out)
		if next != again {
			return errdoc.BadRequest("Those two passwords are not the same. Nothing was changed.")
		}
	}
	if len(next) < 12 {
		return errdoc.BadRequest("A password needs at least 12 characters. Nothing was changed.")
	}

	hash, err := auth.HashPassword(next)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	if err := db.UpdateUser(ctx, &user); err != nil {
		return err
	}

	// Anyone already signed in as this account is signed out. If the reason for
	// the reset is that somebody else has the old password, leaving their
	// session alive would make the reset pointless.
	sessionsEnded := true
	if err := db.DeleteUserSessions(ctx, user.ID); err != nil {
		sessionsEnded = false
		if !*asJSON {
			fmt.Fprintf(out, "\nThe password was changed, but existing sessions could not be ended: %s\n", err)
			return nil
		}
	}

	if *asJSON {
		return writeJSON(out, map[string]any{
			"email": user.Email, "password_changed": true,
			"sessions_ended": sessionsEnded, "totp_still_enabled": user.TOTPEnabled,
		})
	}

	fmt.Fprintf(out, "\nThe password for %s has been changed, and every signed-in device was signed out.\n", user.Email)
	if user.TOTPEnabled {
		fmt.Fprintln(out, "Two-factor authentication is still on for this account, so you will need your authenticator app.")
	}
	fmt.Fprintln(out)
	return nil
}

func adminListUsers(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("list-users", flag.ContinueOnError)
	flags.SetOutput(out)
	databasePath := flags.String("database", "", "the panel database to use")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	db, err := openPanelDatabase(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	users, err := db.ListUsers(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, users)
	}
	if len(users) == 0 {
		fmt.Fprintln(out, "This panel has no accounts yet.")
		return nil
	}
	for _, user := range users {
		role := "member"
		if user.IsAdmin {
			role = "admin"
		}
		state := ""
		if user.Disabled {
			state = " (disabled)"
		}
		fmt.Fprintf(out, "%s  %s%s\n", user.Email, role, state)
	}
	return nil
}

// adminBackupDatabase writes a copy of the panel's own database.
//
// The instruction used to be "copy panel.db", and that is wrong: the database
// runs in WAL mode, so a committed transaction can be in panel.db-wal and not
// yet in panel.db. The copy would come back missing whatever had not been
// checkpointed, and nothing would say so — which is the worst way for a backup
// to fail, because it is discovered during a restore.
func adminBackupDatabase(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backup-db", flag.ContinueOnError)
	flags.SetOutput(out)
	databasePath := flags.String("database", "", "the panel database to copy")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errdoc.BadRequest(fmt.Sprintf(
			"Give the file to write, for example `%s admin backup-db /root/panel-backup.db`.",
			version.Binary))
	}
	target := flags.Arg(0)

	db, err := openPanelDatabase(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Snapshot(ctx, target); err != nil {
		return errdoc.New("admin.backup_failed", "The database could not be copied").
			WithCause("%s", err.Error()).
			WithImpact("No copy was written.").
			WithFix("Check that the directory exists, that the file does not, and that there is room on the disk.")
	}

	info, statErr := os.Stat(target)
	size := ""
	var bytes int64
	if statErr == nil {
		bytes = info.Size()
		size = fmt.Sprintf(" (%d bytes)", bytes)
	}
	if *asJSON {
		return writeJSON(out, map[string]any{
			"path": target, "bytes": bytes,
			"master_key_path": masterKeyPathFor(*databasePath),
		})
	}
	fmt.Fprintf(out, "\nWrote %s%s.\n", target, size)
	fmt.Fprintf(out, "This is the whole panel except its master key. Copy %s too, and keep\n"+
		"the two in different places: together they are everything, apart neither is enough.\n\n",
		masterKeyPathFor(*databasePath))
	return nil
}

// masterKeyPathFor names the key file that goes with a database, for the note
// above. It is the configured one unless the caller pointed somewhere else.
func masterKeyPathFor(databaseOverride string) string {
	if databaseOverride != "" {
		return version.ConfigDir + "/master.key"
	}
	cfg, err := config.Load("")
	if err != nil || cfg.MasterKeyPath == "" {
		return version.ConfigDir + "/master.key"
	}
	return cfg.MasterKeyPath
}

// Running a plugin store.
//
// Two commands, and they are here rather than in a separate tool because the
// thing that signs and the thing that verifies have to agree byte for byte.
// Two implementations of that is one implementation and one bug waiting.

// adminPluginKey makes the keypair a store is signed with.
func adminPluginKey(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("plugin-key", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return errdoc.BadRequest("plugin-key takes no arguments.")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate a signing key: %w", err)
	}

	if *asJSON {
		// The private key is in the JSON because a script that generates a pair
		// has nothing to do with a pair it cannot read. It is still never
		// written to disk by this command.
		return writeJSON(out, map[string]any{
			"public_key":  pluginstore.EncodePublicKey(public),
			"private_key": base64.StdEncoding.EncodeToString(private),
			"stored":      false,
		})
	}

	// The private key is printed and never written: a key this command wrote to
	// a file would be a key somebody left in /root, and the one thing an
	// operator has to do with it is put it somewhere they already trust.
	fmt.Fprintf(out, `
A signing key for a plugin store.

Public key — paste this into Settings, then Plugins, on every panel that should
trust this store:

  %s

Private key — keep this secret. It signs your index and nothing else needs it:

  %s

Sign an index with:

  %s admin plugin-sign <private key> index.json > index.json.sig

It is not stored anywhere by this command. If you lose it, make a new pair and
change the public key on every panel; nothing else breaks.

`, pluginstore.EncodePublicKey(public),
		base64.StdEncoding.EncodeToString(private),
		version.Binary)
	return nil
}

// adminPluginSign signs an index so a panel will trust it.
func adminPluginSign(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("plugin-sign", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return errdoc.BadRequest(fmt.Sprintf(
			"Usage: %s admin plugin-sign <private key> <index.json>", version.Binary))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(flags.Arg(0)))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return errdoc.BadRequest(fmt.Sprintf(
			"That is not a private key from `%s admin plugin-key`.", version.Binary))
	}
	path := flags.Arg(1)
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// The signature covers the file's exact bytes, so it has to be published
	// exactly as it was signed — reformatting the JSON afterwards invalidates
	// it, which is the point.
	signature := pluginstore.Sign(ed25519.PrivateKey(raw), body)

	if *asJSON {
		return writeJSON(out, map[string]any{
			"file": path, "signature": signature,
		})
	}
	// Plain output is the signature and nothing else, because the usual thing
	// to do with it is redirect it into index.json.sig.
	fmt.Fprintln(out, signature)
	return nil
}
