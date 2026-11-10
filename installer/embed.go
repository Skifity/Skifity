// Package installer holds install.sh and uninstall.sh, which are POSIX shell.
//
// The Go file is here so the panel can read the one line it shares with the
// installer, the k3s version a release was tested with, rather than keep a
// copy of it that a release could forget to change. //go:embed can only reach
// files beside it, like deploy/embed.go.
package installer

import (
	_ "embed"
	"regexp"
)

//go:embed install.sh
var script string

// k3sVersionLine is the assignment in install.sh: a plain value in double
// quotes, nothing the shell would expand, so what Go reads is what sh runs.
var k3sVersionLine = regexp.MustCompile(`(?m)^K3S_VERSION="([A-Za-z0-9.+-]*)"$`)

// K3sVersion is the k3s release a fresh install gets, such as v1.37.2+k3s1:
// K3S_VERSION in install.sh, the version the Cluster and Two servers workflows
// ran on before the release was tagged. A test fails if it cannot be read.
var K3sVersion = k3sVersion(script)

func k3sVersion(script string) string {
	match := k3sVersionLine.FindStringSubmatch(script)
	if match == nil {
		return ""
	}
	return match[1]
}
