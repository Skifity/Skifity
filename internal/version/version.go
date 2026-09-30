// Package version holds the product identity and build metadata.
//
// Every user-visible mention of the product name must come from here, so the
// product can be renamed by editing this one file.
package version

import (
	"fmt"
	"runtime"
	"strings"
)

// Product identity. Change these to rebrand the whole product.
const (
	// Name is the display name used in the UI, the docs and CLI output.
	Name = "Skifity"
	// Binary is the name of the executable and of the CLI the docs refer to.
	Binary = "skifity"
	// Domain is the vendor domain used for Kubernetes label and annotation keys.
	Domain = "skifity.com"
	// Tagline is shown on the login screen and in `skifity --help`.
	Tagline = "Self-hosted apps, powered by Kubernetes."
	// ConfigDir is where the installer keeps configuration on a host.
	ConfigDir = "/etc/skifity"
	// DataDir is where the panel keeps its database inside its container.
	DataDir = "/var/lib/skifity"
)

// Build metadata, injected with -ldflags at release time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// LabelKey returns a Kubernetes label or annotation key in the product's namespace,
// for example "skifity.com/app-id".
func LabelKey(suffix string) string {
	return Domain + "/" + suffix
}

// Short returns just the version, for `--version` style output.
func Short() string { return Version }

// Full returns a human-readable one-line description of this build.
func Full() string {
	return fmt.Sprintf("%s %s (commit %s, built %s, %s/%s, %s)",
		Name, Version, shortCommit(), Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// UserAgent returns the HTTP User-Agent this build sends.
func UserAgent() string { return Binary + "/" + Version }

func shortCommit() string {
	if len(Commit) > 8 {
		return Commit[:8]
	}
	return Commit
}

// IsRelease reports whether this build came from the release pipeline.
func IsRelease() bool { return Version != "dev" && !strings.HasSuffix(Version, "-dirty") }
