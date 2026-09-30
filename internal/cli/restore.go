package cli

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
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
	"skifity/internal/sealed"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// adminRestoreDatabase puts a backup of the panel's own database back.
//
// The other half of the scheduled panel backup: a copy in a bucket is only a
// backup once it can be put back. It takes the file as it comes out of the
// bucket — compressed or not — checks that it is a panel's database and that
// it is whole, and only then moves the current one aside and puts it in its
// place. Nothing is deleted: the database it replaces is kept beside it.
func adminRestoreDatabase(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("restore-db", flag.ContinueOnError)
	flags.SetOutput(out)
	databasePath := flags.String("database", "", "the panel database to replace")
	yes := flags.Bool("yes", false, "replace it; without this nothing is changed")
	masterKey := flags.String("master-key", "", "the master key the panel will start with, to check the backup's secrets open with it")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	// Flags may come after the file too: the file is what gets typed first.
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errdoc.BadRequest(fmt.Sprintf(
			"Give the backup to restore, for example `%s admin restore-db ./panel.db.gz`.",
			version.Binary))
	}
	source := positional[0]

	target := *databasePath
	if target == "" {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		target = cfg.DatabasePath
	}

	// The candidate is written into the database's own directory, so the
	// last step is a rename on one filesystem and never a half-copied file.
	candidate := filepath.Join(filepath.Dir(target),
		fmt.Sprintf(".restore-%s.db", time.Now().UTC().Format("20060102-150405")))
	if err := unpackBackup(source, candidate); err != nil {
		removeCandidate(candidate)
		return errdoc.New("admin.restore_unreadable", "That backup could not be read").
			WithCause("%s", err.Error()).
			WithImpact("Nothing was changed.").
			WithFix("Give the file exactly as it came out of the bucket: a .db.gz, or a .db from `%s admin backup-db`.", version.Binary)
	}
	inspection, err := checkRestoreCandidate(ctx, candidate)
	if err != nil {
		removeCandidate(candidate)
		return errdoc.New("admin.restore_not_a_panel", "That is not a whole panel database").
			WithCause("%s", err.Error()).
			WithImpact("Nothing was changed.").
			WithFix("Take a different backup. The newest one in the bucket's skifity/panel/ folder is usually the one to use.")
	}
	users := inspection.Users
	keyPath := *masterKey
	if keyPath == "" {
		keyPath = masterKeyPathFor(*databasePath)
	}
	keyNote := masterKeyNote(inspection, keyPath)
	if inspection.SchemaVersion > inspection.Known {
		keyNote = strings.TrimSpace(keyNote + "\n" + fmt.Sprintf("It was taken by a newer version of %s "+
			"(schema %d; this one knows %d). The panel will only open it once it runs that version again.",
			version.Name, inspection.SchemaVersion, inspection.Known))
	}

	if !*yes {
		removeCandidate(candidate)
		if *asJSON {
			return writeJSON(out, map[string]any{
				"restored": false, "database": target, "from": source, "accounts": users, "master_key_note": keyNote,
			})
		}
		again := "--yes " + source
		if *databasePath != "" {
			again = "--database " + *databasePath + " " + again
		}
		fmt.Fprintf(out, "\n%s is a panel database with %d accounts, and it is whole.\n", source, users)
		if keyNote != "" {
			fmt.Fprintf(out, "%s\n", keyNote)
		}
		fmt.Fprintf(out, "\nRestoring replaces %s with it. The current database is kept beside it,\n"+
			"not deleted. Stop the panel first, restore, then start it again:\n\n"+
			"  kubectl -n %[2]s-system scale deployment/%[2]s-panel --replicas=0\n"+
			"  %[2]s admin restore-db %[3]s\n"+
			"  kubectl -n %[2]s-system scale deployment/%[2]s-panel --replicas=1\n\n",
			target, version.Binary, again)
		return nil
	}

	kept := target + ".before-restore-" + time.Now().UTC().Format("20060102-150405")
	moved := []string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(target + suffix); err != nil {
			continue
		}
		if err := os.Rename(target+suffix, kept+suffix); err != nil {
			// Put back whatever was already moved, so a failure halfway
			// leaves the panel exactly as it was.
			for _, done := range moved {
				_ = os.Rename(kept+done, target+done)
			}
			removeCandidate(candidate)
			return fmt.Errorf("move the current database aside: %w", err)
		}
		moved = append(moved, suffix)
	}
	// Reading the candidate can leave a -wal and a -shm beside it with
	// nothing in them; they are not the database, and must not follow it.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(candidate + suffix)
	}
	if err := os.Rename(candidate, target); err != nil {
		for _, done := range moved {
			_ = os.Rename(kept+done, target+done)
		}
		removeCandidate(candidate)
		return fmt.Errorf("put the backup in place: %w", err)
	}

	if *asJSON {
		return writeJSON(out, map[string]any{
			"restored": true, "database": target, "from": source, "previous": kept, "accounts": users,
			"master_key_note": keyNote,
		})
	}
	fmt.Fprintf(out, "\nRestored %s from %s.\nThe database it replaced is at %s.\n", target, source, kept)
	if keyNote != "" {
		fmt.Fprintf(out, "%s\n", keyNote)
	}
	fmt.Fprintf(out, "Start the panel again.\n\n")
	return nil
}

// removeCandidate removes an unpacked backup and whatever reading it left
// beside it.
func removeCandidate(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

// unpackBackup writes a backup to path, decompressing it if it is gzipped.
func unpackBackup(source, path string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	buffered := bufio.NewReader(in)
	// A sealed backup is opened into a file of its own first, and used only
	// once all of it has been authenticated: a file cut short must not become
	// a database with half its rows.
	if start, err := buffered.Peek(sealed.HeaderSize); err == nil && sealed.IsSealed(start) {
		passphrase := os.Getenv("SKIFITY_BACKUP_PASSPHRASE")
		if passphrase == "" {
			return errors.New("it is sealed with the backup passphrase: set SKIFITY_BACKUP_PASSPHRASE to it and run this again")
		}
		opened, err := os.CreateTemp(filepath.Dir(path), ".restore-open-*")
		if err != nil {
			return err
		}
		defer os.Remove(opened.Name())
		defer opened.Close()
		if err := sealed.Open(opened, buffered, passphrase); err != nil {
			return fmt.Errorf("open it: %w", err)
		}
		if _, err := opened.Seek(0, io.SeekStart); err != nil {
			return err
		}
		buffered = bufio.NewReader(opened)
	}
	var reader io.Reader = buffered
	if magic, err := buffered.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(buffered)
		if err != nil {
			return fmt.Errorf("decompress it: %w", err)
		}
		defer func() { _ = zr.Close() }()
		reader = zr
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		return fmt.Errorf("write it out: %w", err)
	}
	return out.Close()
}

// checkRestoreCandidate reads a candidate without changing it — a backup is
// put back exactly as it was taken, and the panel that opens it brings it up
// to date, or refuses it if it is from a later version — and checks it is
// whole and has somebody who can sign in.
func checkRestoreCandidate(ctx context.Context, path string) (store.Inspection, error) {
	inspection, err := store.Inspect(ctx, path)
	if err != nil {
		return inspection, err
	}
	if inspection.Integrity != "ok" {
		return inspection, fmt.Errorf("SQLite says it is damaged: %s", inspection.Integrity)
	}
	if inspection.Users == 0 {
		return inspection, fmt.Errorf("it has no accounts, so nobody could sign in to it")
	}
	return inspection, nil
}

// masterKeyNote says whether the backup's secrets open with the master key on
// this server. A backup never carries the key, and a panel started with the
// wrong one comes up with every secret unreadable, which looks like a much
// stranger failure than it is. Empty when they open or there is nothing sealed.
func masterKeyNote(inspection store.Inspection, keyPath string) string {
	keyring, err := crypto.LoadKeyring(keyPath)
	if err != nil {
		return fmt.Sprintf("There is no master key at %s. The panel needs the one this backup was taken with.", keyPath)
	}
	if inspection.SealedValue == "" {
		return ""
	}
	if _, err := keyring.Open(inspection.SealedValue, settings.Context(inspection.SealedKey)); err != nil {
		return fmt.Sprintf("Its secrets do not open with the master key at %s. Put the key this backup "+
			"was taken with there before starting the panel, or every secret will be unreadable.", keyPath)
	}
	return ""
}
