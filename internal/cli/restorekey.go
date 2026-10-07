package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"skifity/internal/config"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// adminRestoreKey writes the master key back from a recovery key.
//
// The recovery key is shown once, at setup, and the panel keeps asking until it
// has been downloaded. Everything said about it was that it "opens the encrypted
// settings if the master key is lost", and nothing could be done with it: no
// command read one. A key that cannot be redeemed is a sheet of paper, and this
// is the other half.
//
// The key arrives on standard input, never as an argument, so it is not in the
// process list or the shell's history:
//
//	read -rs KEY; printf %s "$KEY" | sudo skifity admin restore-key
//
// It is checked against the panel's database before anything is written: a key
// that opens none of the database's secrets is the wrong one, and a wrong key
// put in place is exactly the mistake the panel now refuses to start on.
func adminRestoreKey(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("restore-key", flag.ContinueOnError)
	flags.SetOutput(out)
	keyPath := flags.String("master-key", "", "where to write the master key; the panel's own path by default")
	databasePath := flags.String("database", "", "the panel database to check the key against")
	yes := flags.Bool("yes", false, "replace a master key that is already there; it is kept beside the new one")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	line, _ := bufio.NewReader(in).ReadString('\n')
	recovery := strings.TrimSpace(line)
	if recovery == "" {
		return errdoc.BadRequest(fmt.Sprintf(
			"Give the recovery key on standard input, so it stays out of your shell's history: "+
				"`read -rs KEY; printf %%s \"$KEY\" | sudo %s admin restore-key`.", version.Binary))
	}
	ring, err := crypto.ParseRecoveryKey(recovery)
	if err != nil {
		return errdoc.New("admin.recovery_key_unreadable", "That is not a recovery key this panel can read").
			WithCause("%s", err.Error()).
			WithImpact("Nothing was changed.").
			WithFix("Copy it again from the file you downloaded. It starts with SKIFITY-RECOVERY-v1-; spaces, dashes and line breaks are ignored.")
	}

	target := *keyPath
	if target == "" {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		target = cfg.MasterKeyPath
	}
	if target == "" {
		target = filepath.Join(version.ConfigDir, "master.key")
	}

	database := *databasePath
	if database == "" {
		if cfg, err := config.Load(""); err == nil {
			database = cfg.DatabasePath
		}
	}
	checked, err := checkKeyAgainstDatabase(ctx, ring, database)
	if err != nil {
		return err
	}

	exists, err := crypto.KeyFileExists(target)
	if err != nil {
		return err
	}
	if exists && !*yes {
		return errdoc.New("admin.master_key_exists", "There is already a master key here").
			WithCause("%s exists.", target).
			WithImpact("Nothing was changed.").
			WithFix("If it is the wrong key, run this again with --yes: the key that is there is kept beside the new one, not deleted.")
	}
	kept := ""
	if exists {
		kept = target + ".before-restore-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(target, kept); err != nil {
			return fmt.Errorf("move the current master key aside: %w", err)
		}
	}
	if err := crypto.SaveKeyring(target, ring); err != nil {
		return fmt.Errorf("write the master key: %w", err)
	}

	if *asJSON {
		return writeJSON(out, map[string]any{
			"restored": true, "master_key_path": target, "key_id": ring.ActiveID(),
			"checked_against_database": checked, "previous_key_kept_at": kept,
		})
	}
	if kept != "" {
		fmt.Fprintf(out, "The master key that was there is kept at %s.\n", kept)
	}
	fmt.Fprintf(out, "\nThe master key is back at %s (key %s).\n", target, ring.ActiveID())
	if checked {
		fmt.Fprintf(out, "It opens the panel's secrets: this is the key the database was sealed with.\n")
	} else {
		fmt.Fprintf(out, "There was nothing sealed in the database to try it on, so it could not be checked.\n")
	}
	fmt.Fprintf(out, "\nA recovery key holds the key that was active when it was shown. Anything sealed by an\n"+
		"older key, from before a rotation, needs that key too.\n\n"+
		"Start the panel again:\n\n  kubectl -n %[1]s-system rollout restart deployment/%[1]s-panel\n\n", version.Binary)
	return nil
}

// checkKeyAgainstDatabase tries a key on one secret the database holds. It says
// whether there was one to try, and fails when there was and the key did not open it.
func checkKeyAgainstDatabase(ctx context.Context, ring *crypto.Keyring, database string) (bool, error) {
	if database == "" {
		return false, nil
	}
	if _, err := os.Stat(database); err != nil {
		return false, nil
	}
	inspection, err := store.Inspect(ctx, database)
	if err != nil {
		return false, errdoc.New("admin.restore_key_database", "The panel's database could not be read to check the key against").
			WithCause("%s", err.Error()).
			WithImpact("Nothing was changed.").
			WithFix("Stop the panel and run this again, or give the database with --database.")
	}
	if inspection.SealedValue == "" {
		return false, nil
	}
	if _, err := ring.Open(inspection.SealedValue, settings.Context(inspection.SealedKey)); err != nil {
		return false, errdoc.New("admin.recovery_key_wrong", "That recovery key does not open this panel's database").
			WithCause("The secrets in %s were sealed with a different key.", database).
			WithImpact("Nothing was changed. Putting it in place would have left every stored secret unreadable.").
			WithFix("Find the recovery key that was shown when this panel was set up, or the master key file itself. A recovery key from another install, or from before the key was rotated, will not open it.")
	}
	return true, nil
}
