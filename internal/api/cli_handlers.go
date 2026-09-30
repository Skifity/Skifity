package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// The panel hands out the command line tool it is itself running.
//
// One binary is the panel, the CLI and the MCP server, so the file serving this
// request *is* the file somebody wants on their PATH. That turns out to matter
// twice over.
//
// The installer used to fetch the CLI from a releases page, and there is no
// release: no releases page existed, so every install ended
// with "could not download the command line tool" and a link to nothing. It now
// asks the panel it has just started, which always answers and always answers
// with the matching version — a CLI one release behind its panel is a class of
// bug report nobody enjoys.
//
// Open, like the frontend beside it. There is nothing in the binary that is not
// in the source, and requiring a token to download the thing you obtain tokens
// with is a circle.

// handleDownloadCLI streams the command line tool for the platform asked for.
//
// With no platform named, or with the panel's own, it is the running binary —
// always there, always the matching version. Anything else is a cross-built
// copy from the CLI directory, which the image fills: most people deploying
// from a laptop are on macOS or Windows, and the panel runs on Linux, so
// "the file serving this request" is the one file that would not run for them.
func (s *Server) handleDownloadCLI(w http.ResponseWriter, r *http.Request) {
	goos := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("os")))
	arch := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("arch")))
	if goos == "" {
		goos = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	// A closed list, checked before either becomes part of a file name.
	if !cliOS[goos] || !cliArch[arch] {
		writeError(w, r, errdoc.CLIPlatformUnknown(goos+"/"+arch))
		return
	}
	if goos == runtime.GOOS && arch == runtime.GOARCH {
		s.serveOwnBinary(w, r)
		return
	}

	name := cliFileName(goos, arch)
	file, err := os.Open(filepath.Join(s.cfg.CLIDir, name+".gz"))
	if err != nil || s.cfg.CLIDir == "" {
		writeError(w, r, errdoc.CLIPlatformUnavailable(goos+"/"+arch, strings.Join(s.cliPlatforms(), ", ")))
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	// Kept compressed on disk, because five binaries are most of the image.
	// A client that takes gzip gets the file as it is; curl without
	// --compressed does not ask, and gets it unpacked, so the one-line install
	// works as written.
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = io.Copy(w, file)
		return
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		writeError(w, r, cliUnavailable(err))
		return
	}
	defer func() { _ = gz.Close() }()
	_, _ = io.Copy(w, gz)
}

// serveOwnBinary streams the running executable.
func (s *Server) serveOwnBinary(w http.ResponseWriter, r *http.Request) {
	path, err := os.Executable()
	if err != nil {
		writeError(w, r, cliUnavailable(err))
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, r, cliUnavailable(err))
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		writeError(w, r, cliUnavailable(err))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+cliFileName(runtime.GOOS, runtime.GOARCH)+`"`)
	// Not cached: the panel upgrades in place, and a stale copy of a CLI is
	// exactly the version mismatch this endpoint exists to prevent.
	w.Header().Set("Cache-Control", "no-store")
	// ServeContent rather than io.Copy: it answers a Range request, which is
	// what makes an interrupted download resumable over a bad connection.
	http.ServeContent(w, r, version.Binary, info.ModTime(), file)
}

// The platforms a CLI is built for, which are the ones `make release` and the
// image build: anything else is refused before it can be part of a path.
var (
	cliOS   = map[string]bool{"linux": true, "darwin": true, "windows": true}
	cliArch = map[string]bool{"amd64": true, "arm64": true}
)

// cliFileName is what a platform's CLI is called: skifity-darwin-arm64, or
// skifity-windows-amd64.exe, which Windows needs to run it at all.
func cliFileName(goos, arch string) string {
	name := version.Binary + "-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// cliPlatforms lists what this panel can hand out: its own platform, and every
// cross-built copy in the CLI directory.
func (s *Server) cliPlatforms() []string {
	platforms := []string{cliPlatform()}
	for goos := range cliOS {
		for arch := range cliArch {
			platform := goos + "/" + arch
			if platform == cliPlatform() || s.cfg.CLIDir == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(s.cfg.CLIDir, cliFileName(goos, arch)+".gz")); err == nil {
				platforms = append(platforms, platform)
			}
		}
	}
	sort.Strings(platforms[1:])
	return platforms
}

// cliPlatform is what the binary this panel is running was built for. It is in
// /api/meta so that a page or a script can say "this is the linux/amd64 build"
// rather than handing somebody a file that will not run.
func cliPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func cliUnavailable(err error) error {
	return errdoc.New("cli.unavailable", "This panel cannot hand out its own binary").
		WithCause("The running executable could not be read: %s", err.Error()).
		WithImpact("The command line tool was not downloaded. The panel itself is unaffected.").
		WithFix("Build it from the repository with `make build`, or copy the binary from another machine. The panel and the CLI are the same file.").
		WithStatus(http.StatusInternalServerError)
}
