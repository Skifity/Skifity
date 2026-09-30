package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"skifity/internal/builder"
	"skifity/internal/gitsrc"
)

// packed is a folder ready to send.
type packed struct {
	// Archive is a gzipped tar in a temporary file. The caller removes it.
	Archive string
	// Size is the archive's size; Unpacked is what its files add up to.
	Size, Unpacked int64
	Files          []string
	// Skipped are links and special files, which are not sent, named so the
	// person is not surprised later by one that mattered.
	Skipped []string
	// Largest are the biggest top-level entries, for explaining an upload that
	// is too large.
	Largest []sizedEntry
	// Tree is what the detector reads: every path, and the contents of the
	// few files that decide what the app is.
	Tree builder.Tree
}

type sizedEntry struct {
	Name string
	Size int64
}

// readLimit bounds a file read for detection. The files it reads are
// manifests, which are small; one that is not is not one worth reading.
const readLimit = 256 << 10

// fixedTime is every entry's modification time. With a real one, touching a
// file without changing it would change the archive, the archive's hash, and
// so the build fingerprint — and a deploy that could have reused the image
// would build again for nothing.
var fixedTime = time.Unix(0, 0)

// pack walks a folder and writes what should be sent into a gzipped tar.
func pack(root string) (*packed, error) {
	result := &packed{Tree: builder.Tree{Contents: map[string]string{}}}
	ig := newIgnorer()

	type file struct {
		rel  string
		mode fs.FileMode
		size int64
	}
	var files []file
	topLevel := map[string]int64{}

	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			ig.loadDir(root, "")
			return nil
		}
		isDir := entry.IsDir()
		if ig.ignored(rel, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isDir {
			ig.loadDir(root, rel)
			return nil
		}
		if !entry.Type().IsRegular() {
			// A link can point anywhere, including outside the folder, and
			// the panel refuses one anyway; a socket or a device is not code.
			result.Skipped = append(result.Skipped, rel)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, file{rel: rel, mode: info.Mode(), size: info.Size()})
		topLevel[strings.SplitN(rel, "/", 2)[0]] += info.Size()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	// Sorted, so the same folder always makes the same archive.
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	for name, size := range topLevel {
		result.Largest = append(result.Largest, sizedEntry{name, size})
	}
	sort.Slice(result.Largest, func(i, j int) bool { return result.Largest[i].Size > result.Largest[j].Size })
	if len(result.Largest) > 3 {
		result.Largest = result.Largest[:3]
	}

	readable := map[string]bool{}
	for _, name := range gitsrc.ReadableFiles() {
		readable[name] = true
	}

	temp, err := os.CreateTemp("", "skifity-up-*.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("make a temporary file: %w", err)
	}
	result.Archive = temp.Name()
	fail := func(err error) (*packed, error) {
		temp.Close()
		os.Remove(result.Archive)
		return nil, err
	}

	gz := gzip.NewWriter(temp)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		mode := int64(0o644)
		if f.mode&0o111 != 0 {
			// A script the build runs, gradlew or bin/start, has to stay
			// runnable.
			mode = 0o755
		}
		header := &tar.Header{
			Name: f.rel, Mode: mode, Size: f.size, Typeflag: tar.TypeReg,
			ModTime: fixedTime, Format: tar.FormatPAX,
		}
		if err := tw.WriteHeader(header); err != nil {
			return fail(fmt.Errorf("pack %s: %w", f.rel, err))
		}
		source, err := os.Open(filepath.Join(root, filepath.FromSlash(f.rel)))
		if err != nil {
			return fail(fmt.Errorf("read %s: %w", f.rel, err))
		}
		// Exactly the size the header promised: a file that grew while being
		// read would otherwise write past its entry and corrupt the archive.
		var detect strings.Builder
		var sink io.Writer = tw
		if readable[f.rel] && f.size <= readLimit {
			sink = io.MultiWriter(tw, &detect)
		}
		copied, err := io.Copy(sink, io.LimitReader(source, f.size))
		source.Close()
		if err != nil {
			return fail(fmt.Errorf("pack %s: %w", f.rel, err))
		}
		if copied != f.size {
			return fail(fmt.Errorf("%s changed while it was being packed; run it again", f.rel))
		}
		if detect.Len() > 0 {
			result.Tree.Contents[f.rel] = detect.String()
		}
		result.Files = append(result.Files, f.rel)
		result.Unpacked += f.size
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	info, err := temp.Stat()
	if err != nil {
		return fail(err)
	}
	if err := temp.Close(); err != nil {
		return fail(err)
	}
	result.Size = info.Size()
	result.Tree.Files = result.Files
	return result, nil
}

// humanSize prints a byte count the way a person reads one.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// displayPath is a folder the way the person would type it.
func displayPath(dir string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "this folder"
			}
			return "./" + filepath.ToSlash(rel)
		}
	}
	return path.Clean(filepath.ToSlash(dir))
}
