// Package upload keeps the source code people send the panel without git.
//
// Somebody who has never deployed anything often has no repository either: the
// app is a folder that an assistant wrote, on a laptop, or in an online editor
// that exports a zip. Every other way into the panel starts with a repository
// URL, so for them it did not start at all. `skifity up` packs the folder and
// sends it here; the build then works from that instead of a clone.
//
// # What is refused, and why here
//
// An archive is somebody else's bytes, and unpacking one is a classic way to
// write outside the directory you meant to. Everything is checked in Go, at
// the moment it arrives, before a single file is written anywhere: no absolute
// paths, no `..`, no links, no device files, and bounds on how much there is.
// The build later unpacks it with an ordinary tar, which is safe precisely
// because nothing unsafe is ever stored for it to unpack.
//
// A `.env` file is refused too. It is where the real values of an app's
// settings live, and a build context ends up inside the image, where anyone
// who can pull the image can read it. The CLI leaves it out; this is the line
// for anything else that sends an archive.
package upload

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Limits bound one upload.
type Limits struct {
	// Compressed is the most the archive itself may be.
	Compressed int64
	// Unpacked is the most its contents may add up to, which is what stops a
	// small archive that expands into a full disk.
	Unpacked int64
	// Entries is the most files and directories it may hold.
	Entries int
}

// DefaultLimits are generous for source code and far too small for the things
// that should never be in an upload: node_modules, a virtualenv, a build
// directory, a database dump. An app's own code is almost always a few
// megabytes; hitting these means something is in the folder that should not be.
var DefaultLimits = Limits{
	Compressed: 100 << 20,
	Unpacked:   1 << 30,
	Entries:    50_000,
}

// Summary describes an upload that was accepted.
type Summary struct {
	// SHA256 is the archive's hash, which is also its name and what a
	// deployment records as its "commit": the same code sent twice is the same
	// upload, and different code is a different build.
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Files    int    `json:"files"`
	Unpacked int64  `json:"unpacked"`
}

// ErrTooLarge and the rest are the ways an archive is refused. Each is in words
// for the person whose upload it was.
var (
	ErrTooLarge = errors.New("the upload is larger than the panel accepts")
	ErrNotFound = errors.New("there is no such upload")
)

// Refusal is an archive that was read and turned down, with the entry that
// caused it.
type Refusal struct {
	// Kind says which rule it broke, so the panel can say so in the reader's
	// own language. Reason is the same in English, for logs and the CLI.
	Kind   RefusalKind
	Entry  string
	Reason string
}

// RefusalKind is why an archive was turned down.
type RefusalKind string

// The ways an archive is refused.
const (
	// NotAnArchive is anything that is not a whole gzipped tar.
	NotAnArchive RefusalKind = "not_an_archive"
	// UnsafeEntry is a path, a link or a file type that could reach outside
	// the folder it is unpacked into.
	UnsafeEntry RefusalKind = "unsafe_entry"
	// SecretsFile is a .env with an app's real settings in it.
	SecretsFile RefusalKind = "secrets_file"
	// TooManyFiles and Unpacks are the limits on what is inside.
	TooManyFiles RefusalKind = "too_many_files"
	UnpacksLarge RefusalKind = "unpacks_too_large"
	// Empty is an archive with no files in it.
	Empty RefusalKind = "empty"
)

func (r *Refusal) Error() string {
	if r.Entry == "" {
		return r.Reason
	}
	return fmt.Sprintf("%s: %s", r.Entry, r.Reason)
}

// Inspect reads a gzipped tar from start to end and refuses anything that would
// not be safe to unpack. Nothing is written.
func Inspect(r io.Reader, limits Limits) (files int, unpacked int64, err error) {
	return walk(r, limits, nil)
}

// ReadTree reads an archive the way Inspect does, refusing the same things,
// and returns every file's path and the contents of the ones named in
// readable — which is what the build detector reads from a repository, so an
// uploaded folder is detected exactly as the same code in a repository would
// be. A file larger than maxRead is listed and not read.
func ReadTree(r io.Reader, limits Limits, readable []string, maxRead int64) ([]string, map[string]string, error) {
	wanted := map[string]bool{}
	for _, name := range readable {
		wanted[name] = true
	}
	var paths []string
	contents := map[string]string{}
	_, _, err := walk(r, limits, func(name string, size int64, body io.Reader) error {
		name = strings.TrimPrefix(name, "./")
		paths = append(paths, name)
		if !wanted[name] || size > maxRead {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(body, maxRead))
		if err != nil {
			return err
		}
		contents[name] = string(data)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	return paths, contents, nil
}

// walk reads an archive from start to end, refusing anything unsafe, and
// hands each regular file to visit when there is one.
func walk(r io.Reader, limits Limits, visit func(name string, size int64, body io.Reader) error) (files int, unpacked int64, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, 0, &Refusal{Kind: NotAnArchive, Reason: "this is not a gzipped tar archive"}
	}
	// Nothing is written, so closing the reader has nothing to report.
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)

	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, 0, &Refusal{Kind: NotAnArchive, Reason: "the archive is damaged: " + err.Error()}
		}
		entries++
		if entries > limits.Entries {
			return 0, 0, &Refusal{Kind: TooManyFiles, Reason: fmt.Sprintf(
				"it holds more than %d files; a dependency folder such as node_modules is probably in it", limits.Entries)}
		}

		name := header.Name
		if kind, err := checkName(name); err != nil {
			return 0, 0, &Refusal{Kind: kind, Entry: name, Reason: err.Error()}
		}

		switch header.Typeflag {
		case tar.TypeReg:
			files++
			if header.Size < 0 {
				return 0, 0, &Refusal{Kind: NotAnArchive, Entry: name, Reason: "it says it has a negative size"}
			}
			unpacked += header.Size
			if unpacked > limits.Unpacked {
				return 0, 0, &Refusal{Kind: UnpacksLarge, Reason: fmt.Sprintf(
					"it unpacks to more than %d MB; a build output or a dependency folder is probably in it",
					limits.Unpacked>>20)}
			}
			// Next reads past whatever of the body visit left, so an archive
			// cut off halfway fails there, as damaged, rather than
			// half-unpacked by the build.
			if visit != nil {
				if err := visit(name, header.Size, reader); err != nil {
					return 0, 0, &Refusal{Kind: NotAnArchive, Entry: name, Reason: "the archive is damaged: " + err.Error()}
				}
			}
		case tar.TypeDir:
			// A directory is fine; its name was checked above.
		case tar.TypeSymlink, tar.TypeLink:
			return 0, 0, &Refusal{Kind: UnsafeEntry, Entry: name, Reason: "links are not accepted, because one can point outside the folder it is unpacked into"}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// Metadata written by some tar implementations, with no file of its
			// own. archive/tar folds it into the next header.
		default:
			return 0, 0, &Refusal{Kind: UnsafeEntry, Entry: name, Reason: "only ordinary files and folders are accepted"}
		}
	}
	return files, unpacked, nil
}

// checkName refuses a path that could land outside the directory it is
// unpacked into, or that is the file with an app's real secrets.
func checkName(name string) (RefusalKind, error) {
	if name == "" {
		return UnsafeEntry, errors.New("an entry has no name")
	}
	if strings.ContainsRune(name, 0) {
		return UnsafeEntry, errors.New("a name contains a NUL character")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || hasDriveLetter(name) {
		return UnsafeEntry, errors.New("absolute paths are not accepted")
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return UnsafeEntry, errors.New("a path may not climb out of the folder with \"..\"")
		}
	}
	if base := path.Base(strings.TrimSuffix(name, "/")); isSecretsFile(base) {
		return SecretsFile, errors.New("this is the file with the app's real settings. It would be built into the image, " +
			"where anyone who can pull the image can read it. Set its values under Variables instead")
	}
	return "", nil
}

var driveLetter = regexp.MustCompile(`^[A-Za-z]:`)

func hasDriveLetter(name string) bool { return driveLetter.MatchString(name) }

// isSecretsFile reports whether a file name is a .env with real values, as
// opposed to the template of one.
func isSecretsFile(base string) bool {
	if base != ".env" && !strings.HasPrefix(base, ".env.") {
		return false
	}
	switch base {
	case ".env.example", ".env.sample", ".env.template", ".env.dist":
		return false
	}
	return true
}

// IsSecretsFile is the same rule, for the CLI to leave these out before it
// ever sends them.
func IsSecretsFile(base string) bool { return isSecretsFile(base) }

// Store keeps accepted uploads on the panel's own disk, beside its database.
type Store struct {
	Dir    string
	Limits Limits
	// Keep is how many uploads an app keeps. Older ones are only needed to
	// rebuild an old version, and a rollback reuses the image instead.
	Keep int
}

var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ValidSHA reports whether a string could name an upload. It is checked before
// it is ever part of a path, because it arrives in a request.
func ValidSHA(sha string) bool { return shaPattern.MatchString(sha) }

func (s *Store) limits() Limits {
	if s.Limits == (Limits{}) {
		return DefaultLimits
	}
	return s.Limits
}

func (s *Store) appDir(appID string) (string, error) {
	// An app id is the panel's own, but it becomes a directory name, so it is
	// held to a shape that cannot be anything else.
	if !appIDPattern.MatchString(appID) {
		return "", fmt.Errorf("%q is not an app id", appID)
	}
	return filepath.Join(s.Dir, appID), nil
}

var appIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Save stores an archive for an app and returns what it holds.
//
// It is written to a temporary file while it is hashed, inspected from there,
// and only then given its name. A refused upload leaves nothing behind.
func (s *Store) Save(appID string, r io.Reader) (Summary, error) {
	dir, err := s.appDir(appID)
	if err != nil {
		return Summary{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Summary{}, fmt.Errorf("make the upload folder: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return Summary{}, fmt.Errorf("start the upload: %w", err)
	}
	keep := false
	defer func() {
		temp.Close()
		if !keep {
			os.Remove(temp.Name())
		}
	}()

	limits := s.limits()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(r, limits.Compressed+1))
	if err != nil {
		return Summary{}, fmt.Errorf("receive the upload: %w", err)
	}
	if written > limits.Compressed {
		return Summary{}, ErrTooLarge
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return Summary{}, err
	}
	files, unpacked, err := Inspect(temp, limits)
	if err != nil {
		return Summary{}, err
	}
	if files == 0 {
		return Summary{}, &Refusal{Kind: Empty, Reason: "the archive has no files in it"}
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	final := filepath.Join(dir, sum+".tar.gz")
	if err := temp.Sync(); err != nil {
		return Summary{}, err
	}
	if err := temp.Close(); err != nil {
		return Summary{}, err
	}
	if err := os.Rename(temp.Name(), final); err != nil {
		return Summary{}, fmt.Errorf("keep the upload: %w", err)
	}
	keep = true
	// The rename replaces an upload that was already there with the file just
	// written, so the same code sent again is the newest for pruning, not the
	// first thing thrown away.
	s.prune(dir)

	return Summary{SHA256: sum, Size: written, Files: files, Unpacked: unpacked}, nil
}

// Open returns an app's upload for reading.
func (s *Store) Open(appID, sha string) (*os.File, error) {
	if !ValidSHA(sha) {
		return nil, ErrNotFound
	}
	dir, err := s.appDir(appID)
	if err != nil {
		return nil, ErrNotFound
	}
	file, err := os.Open(filepath.Join(dir, sha+".tar.gz"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return file, err
}

// Has reports whether an app has an upload with this hash.
func (s *Store) Has(appID, sha string) bool {
	file, err := s.Open(appID, sha)
	if err != nil {
		return false
	}
	file.Close()
	return true
}

// Latest returns the hash of the newest upload an app has, which is what a
// deploy with no upload named means: the code this person sent last.
func (s *Store) Latest(appID string) (string, error) {
	dir, err := s.appDir(appID)
	if err != nil {
		return "", ErrNotFound
	}
	uploads := listUploads(dir)
	if len(uploads) == 0 {
		return "", ErrNotFound
	}
	return strings.TrimSuffix(uploads[0].name, ".tar.gz"), nil
}

// Remove deletes everything an app uploaded, when the app is deleted.
func (s *Store) Remove(appID string) error {
	dir, err := s.appDir(appID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// AppIDs lists the apps that have uploads, so code left behind by an app that
// went with its project or environment can be found and removed.
func (s *Store) AppIDs() []string {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() && appIDPattern.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	return ids
}

// prune keeps the newest uploads and removes the rest, so an app deployed from
// a laptop every day does not fill the panel's disk over a year.
func (s *Store) prune(dir string) {
	keep := s.Keep
	if keep <= 0 {
		keep = 10
	}
	uploads := listUploads(dir)
	if len(uploads) <= keep {
		return
	}
	for _, old := range uploads[keep:] {
		os.Remove(filepath.Join(dir, old.name))
	}
}

type storedUpload struct {
	name string
	mod  time.Time
}

// listUploads returns an app's uploads, newest first.
func listUploads(dir string) []storedUpload {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var uploads []storedUpload
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".tar.gz") || !ValidSHA(strings.TrimSuffix(name, ".tar.gz")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		uploads = append(uploads, storedUpload{name, info.ModTime()})
	}
	sort.Slice(uploads, func(i, j int) bool { return uploads[i].mod.After(uploads[j].mod) })
	return uploads
}
