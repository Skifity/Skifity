package cli

import (
	"bufio"
	"os"
	"path"
	"regexp"
	"strings"

	"skifity/internal/upload"
)

// What `skifity up` leaves out of the folder it sends.
//
// The folder on somebody's laptop is not the same thing as their app. It has
// node_modules in it, a .next cache, a virtualenv, the .env with their real
// keys. A repository already draws that line in .gitignore, and a folder an
// assistant generated almost always has one — so the same file draws it here,
// read the way git reads it. A .skifityignore beside it adds to it, for what is
// in git but should not be deployed.

// alwaysLeftOut is never sent, whatever an ignore file says: version control
// that is not source, dependencies the build installs itself, and the file this
// command writes for itself.
var alwaysLeftOut = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true,
	".DS_Store":    true, "Thumbs.db": true,
	ProjectFileName: true,
}

// leftOutUnlessKept is left out as if a .gitignore said so, which means an
// ignore file can bring one back with a "!" line. Caches and environments that
// a build recreates, and editor folders.
var leftOutUnlessKept = []string{
	"__pycache__/", "*.pyc", ".venv/", "venv/", ".tox/", ".pytest_cache/", ".mypy_cache/",
	".next/", ".nuxt/", ".svelte-kit/", ".turbo/", ".cache/", ".parcel-cache/",
	".vercel/", ".netlify/", ".idea/", ".vscode/", "coverage/", "*.log",
}

// ignoreFiles are read in every directory, in this order.
var ignoreFiles = []string{".gitignore", ".skifityignore"}

type ignoreRule struct {
	// base is the directory the rule's file sits in, relative to the root. A
	// rule only applies beneath it.
	base    string
	pattern *regexp.Regexp
	negate  bool
	dirOnly bool
}

// ignorer decides, path by path, what stays behind.
type ignorer struct {
	rules []ignoreRule
}

func newIgnorer() *ignorer {
	ig := &ignorer{}
	for _, line := range leftOutUnlessKept {
		ig.add("", line)
	}
	return ig
}

// loadDir reads the ignore files in one directory. rel is the directory's path
// relative to the root, "" for the root itself.
func (ig *ignorer) loadDir(root, rel string) {
	for _, name := range ignoreFiles {
		file, err := os.Open(path.Join(root, rel, name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			ig.add(rel, scanner.Text())
		}
		file.Close()
	}
}

// add parses one line of an ignore file.
func (ig *ignorer) add(base, line string) {
	line = strings.TrimRight(line, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	rule := ignoreRule{base: base}
	if strings.HasPrefix(line, "!") {
		rule.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return
	}
	// A slash anywhere but the end ties the pattern to the directory of the
	// file it is in. Without one it matches a name at any depth.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	expr := globToRegexp(line)
	if anchored {
		expr = "^" + expr + "$"
	} else {
		expr = "^(?:.*/)?" + expr + "$"
	}
	compiled, err := regexp.Compile(expr)
	if err != nil {
		// A line git would not understand either; skipping it is what git does.
		return
	}
	rule.pattern = compiled
	ig.rules = append(ig.rules, rule)
}

// ignored reports whether a path, relative to the root and with forward
// slashes, stays behind. The last rule that matches decides, as in git.
func (ig *ignorer) ignored(rel string, isDir bool) bool {
	name := path.Base(rel)
	if alwaysLeftOut[name] || (!isDir && upload.IsSecretsFile(name)) {
		return true
	}
	result := false
	for _, rule := range ig.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		sub := rel
		if rule.base != "" {
			if !strings.HasPrefix(rel, rule.base+"/") {
				continue
			}
			sub = strings.TrimPrefix(rel, rule.base+"/")
		}
		if rule.pattern.MatchString(sub) {
			result = !rule.negate
		}
	}
	return result
}

// globToRegexp turns a gitignore glob into a regular expression body.
func globToRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "/**") && i+3 == len(glob):
			b.WriteString("(?:/.*)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(glob):
			i++
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
