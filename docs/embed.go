// Package docs holds the documentation the panel serves.
//
// The Go file lives here rather than somewhere tidier because //go:embed can
// only reach files beside it, and copying the pages into a package directory at
// build time is one more thing to forget. The documentation someone reads on
// their own server is therefore exactly the file in this directory.
//
// It is embedded on purpose: an operator whose cluster is broken is often on a
// machine with no browser and a panel with no outbound network, and that is
// precisely when the troubleshooting page matters.
package docs

import (
	"embed"
	"io/fs"
)

// Pages are the user-facing documents. The research notes, the decision record
// and the progress log are for people working on Skifity, not people running
// it, so they are not shipped.
//
//go:embed quick-start.md concepts.md templates.md tour.md adding-servers.md cli.md backups.md
//go:embed configuration.md troubleshooting.md faq.md performance.md firewall.md
//go:embed plugins.md
//go:embed images/*.png images/*.svg
var Pages embed.FS

// FS returns the embedded documentation.
func FS() fs.FS { return Pages }
