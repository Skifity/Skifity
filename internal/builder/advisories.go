package builder

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Advisories are the critical vulnerabilities platforms stopped deploys over.
//
// When the React Server Components hole (CVE-2025-55182, a remote code
// execution anyone could reach) was published, Vercel and Netlify refused to
// deploy the affected Next.js versions within a day, and anybody hosting
// their own found out from the news. This is the same check, kept short on
// purpose: a vulnerability that turns a public app into somebody else's
// server, in a framework a large share of apps here are built with. Everything
// else is what `npm audit` and Dependabot are for.
//
// A version is read from the lockfile, which says what is installed; from
// package.json only when it names one exact version, because a range says
// nothing about what a lockfile pinned. Pre-release versions, canaries among
// them, are not checked. A warning never stops a deploy.

// Advisory is one of those vulnerabilities in a version the source uses.
type Advisory struct {
	Package string `json:"package"`
	Version string `json:"version"`
	// ID is the CVE.
	ID string `json:"id"`
	// FixedIn is the first version on the same line that is not affected.
	FixedIn string `json:"fixed_in"`
	URL     string `json:"url"`
}

type knownAdvisory struct {
	packages []string
	id, url  string
	// affected are [introduced, fixed) pairs.
	affected [][2]string
}

var knownAdvisories = []knownAdvisory{
	{
		packages: []string{"next"},
		id:       "CVE-2025-29927",
		url:      "https://github.com/vercel/next.js/security/advisories/GHSA-f82v-jwr5-mffw",
		affected: [][2]string{{"11.1.4", "12.3.5"}, {"13.0.0", "13.5.9"}, {"14.0.0", "14.2.25"}, {"15.0.0", "15.2.3"}},
	},
	{
		packages: []string{"next"},
		id:       "CVE-2025-55182",
		url:      "https://github.com/vercel/next.js/security/advisories/GHSA-9qr9-h5gf-34mp",
		affected: [][2]string{
			{"15.0.0", "15.0.5"}, {"15.1.0", "15.1.9"}, {"15.2.0", "15.2.6"}, {"15.3.0", "15.3.6"},
			{"15.4.0", "15.4.8"}, {"15.5.0", "15.5.7"}, {"16.0.0", "16.0.7"},
		},
	},
	{
		packages: []string{"react-server-dom-webpack", "react-server-dom-parcel", "react-server-dom-turbopack"},
		id:       "CVE-2025-55182",
		url:      "https://github.com/facebook/react/security/advisories/GHSA-fv66-9v8q-g76r",
		affected: [][2]string{{"19.0.0", "19.0.1"}, {"19.1.0", "19.1.2"}, {"19.2.0", "19.2.1"}},
	},
}

// LockFiles are where the installed versions are read from.
var LockFiles = []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock"}

// FindAdvisories lists the known critical advisories that apply to the
// versions the source uses.
func FindAdvisories(tree Tree) []Advisory {
	var out []Advisory
	for _, known := range knownAdvisories {
		for _, pkg := range known.packages {
			for _, version := range installedVersions(tree, pkg) {
				if fixed, hit := known.affects(version); hit {
					out = append(out, Advisory{
						Package: pkg, Version: version, ID: known.id, FixedIn: fixed, URL: known.url,
					})
				}
			}
		}
	}
	return out
}

func (k knownAdvisory) affects(version string) (string, bool) {
	v, ok := parseRelease(version)
	if !ok {
		return "", false
	}
	for _, span := range k.affected {
		from, _ := parseRelease(span[0])
		to, _ := parseRelease(span[1])
		if !v.less(from) && v.less(to) {
			return span[1], true
		}
	}
	return "", false
}

type release [3]int

func (a release) less(b release) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// parseRelease reads X.Y.Z, and refuses anything with a pre-release or build
// suffix.
func parseRelease(version string) (release, bool) {
	var r release
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) != 3 {
		return r, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return r, false
		}
		r[i] = n
	}
	return r, true
}

// installedVersions are the versions of a package the source installs: every
// one a lockfile names, since a workspace can hold several, or else the one
// package.json pins.
func installedVersions(tree Tree, pkg string) []string {
	seen := map[string]bool{}
	for _, lock := range LockFiles {
		for _, version := range lockedVersions(lock, tree.Read(lock), pkg) {
			seen[version] = true
		}
	}
	if len(seen) == 0 {
		if version := pinnedVersion(tree.Read("package.json"), pkg); version != "" {
			seen[version] = true
		}
	}
	out := make([]string, 0, len(seen))
	for version := range seen {
		out = append(out, version)
	}
	sort.Strings(out)
	return out
}

// lockedVersions reads a lockfile as text rather than parsing it: a lockfile
// read through a Git host's API may be cut short, and what a pattern finds in
// the part that arrived is still true.
func lockedVersions(lock, content, pkg string) []string {
	if content == "" {
		return nil
	}
	name := regexp.QuoteMeta(pkg)
	var patterns []*regexp.Regexp
	switch lock {
	case "package-lock.json", "npm-shrinkwrap.json":
		// "node_modules/next": { "version": "15.1.0", — lockfile versions 2
		// and 3, nested under another package too — and version 1's
		// "next": { "version": "15.1.0".
		patterns = []*regexp.Regexp{
			regexp.MustCompile(`"(?:[^"]*node_modules/)?` + name + `"\s*:\s*\{\s*"version"\s*:\s*"([^"]+)"`),
		}
	case "pnpm-lock.yaml":
		// /next@15.1.0: (version 6), next@15.1.0: (version 9), /next/15.1.0:
		// (version 5), each possibly quoted and followed by peers in brackets.
		patterns = []*regexp.Regexp{
			regexp.MustCompile(`(?m)^\s+['"]?/?` + name + `[@/]([0-9][^:('"\s]*)`),
		}
	case "yarn.lock":
		// next@^15.1.0: followed by version "15.1.0" (Yarn 1), or
		// "next@npm:^15.1.0": followed by version: 15.1.0 (Yarn 2 and later).
		patterns = []*regexp.Regexp{
			regexp.MustCompile(`(?m)^"?` + name + `@[^\n]*:\n(?:[ \t]+[^\n]*\n)*?[ \t]+version:?[ \t]+"?([0-9][^"\s]*)`),
		}
	case "bun.lock":
		// "next": ["next@15.1.0", ...
		patterns = []*regexp.Regexp{
			regexp.MustCompile(`"` + name + `"\s*:\s*\[\s*"` + name + `@([0-9][^"]*)"`),
		}
	}
	var out []string
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(content, -1) {
			out = append(out, match[1])
		}
	}
	return out
}

// pinnedVersion is the version package.json names for a package, when it
// names exactly one.
func pinnedVersion(packageJSON, pkg string) string {
	if packageJSON == "" {
		return ""
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(packageJSON), &manifest) != nil {
		return ""
	}
	spec := manifest.Dependencies[pkg]
	if spec == "" {
		spec = manifest.DevDependencies[pkg]
	}
	spec = strings.TrimPrefix(strings.TrimSpace(spec), "=")
	if _, ok := parseRelease(spec); !ok {
		return ""
	}
	return spec
}
