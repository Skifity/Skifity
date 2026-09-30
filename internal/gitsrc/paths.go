package gitsrc

import (
	"fmt"
	slashpath "path"
	"strings"
)

// Watch paths say which pushes an app cares about.
//
// In a monorepo every push to the branch used to rebuild every app built from
// it, including the ones whose code the push never touched. Railway, Render and
// Coolify let an app name the paths it watches; Vercel skips a project whose
// directory did not change. This is the first of those, with one line per
// pattern:
//
//	apps/web/**      everything under apps/web
//	packages/ui      the same: a plain path covers what is under it
//	!**/*.md         except Markdown, wherever it is
//
// Every pattern starts at the repository's root, whatever the app's root
// directory is, because what an app depends on is often outside it. "*" and
// "?" stay inside one directory, "**" crosses any number of them, and a later
// line wins over an earlier one — the rules of a .gitignore, without its
// "a name with no slash matches at any depth", which surprises more often than
// it helps.

// MaxWatchPaths bounds how many patterns an app can have. The matcher is
// cheap, but a webhook runs it for every file in the push against every app
// built from the repository.
const MaxWatchPaths = 50

const maxWatchPathLength = 200

// BadWatchPath is a line that is not a pattern an app can watch.
type BadWatchPath struct {
	Line   string
	Reason string
}

func (e *BadWatchPath) Error() string {
	return fmt.Sprintf("%q %s", truncatePattern(e.Line), e.Reason)
}

// ErrTooManyWatchPaths is more patterns than MaxWatchPaths.
var ErrTooManyWatchPaths = fmt.Errorf("an app can watch at most %d patterns", MaxWatchPaths)

type watchPattern struct {
	segments []string
	negated  bool
}

// ParseWatchPaths reads the patterns an app was given, one per line. Blank
// lines and lines starting with # are ignored. The error is a *BadWatchPath
// naming the first line that is not a pattern, or ErrTooManyWatchPaths.
func ParseWatchPaths(text string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := compileWatchPath(line); err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	if len(out) > MaxWatchPaths {
		return nil, ErrTooManyWatchPaths
	}
	return out, nil
}

func compileWatchPath(line string) (watchPattern, error) {
	if len(line) > maxWatchPathLength {
		return watchPattern{}, &BadWatchPath{Line: line, Reason: fmt.Sprintf("is longer than %d characters", maxWatchPathLength)}
	}
	p := watchPattern{}
	rest, negated := strings.CutPrefix(line, "!")
	p.negated = negated
	rest = strings.TrimPrefix(strings.TrimSpace(rest), "./")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return watchPattern{}, &BadWatchPath{Line: line, Reason: "names no path; ** is everything"}
	}
	for _, segment := range strings.Split(rest, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			return watchPattern{}, &BadWatchPath{Line: line, Reason: "leaves the repository"}
		}
		// path.Match reports a malformed pattern only when it gets as far as
		// the bad part, so it is matched against something that reaches it.
		if _, err := slashpath.Match(segment, segment); err != nil {
			return watchPattern{}, &BadWatchPath{Line: line, Reason: "is not a pattern: " + err.Error()}
		}
		p.segments = append(p.segments, segment)
	}
	return p, nil
}

func truncatePattern(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// Watches reports whether a change to file matters to an app with these
// patterns. No patterns means everything matters, and so does a list of only
// exclusions: "!docs/**" reads as "everything except the docs".
func Watches(patterns []string, file string) bool {
	return watches(compileWatchPaths(patterns), file)
}

// compileWatchPaths compiles stored patterns. They were checked when they were
// saved; one that no longer compiles is ignored rather than failing the push.
func compileWatchPaths(patterns []string) []watchPattern {
	compiled := make([]watchPattern, 0, len(patterns))
	for _, line := range patterns {
		if p, err := compileWatchPath(line); err == nil {
			compiled = append(compiled, p)
		}
	}
	return compiled
}

func watches(patterns []watchPattern, file string) bool {
	watched := true
	for _, p := range patterns {
		if !p.negated {
			watched = false
			break
		}
	}
	name := strings.Split(strings.Trim(file, "/"), "/")
	for _, p := range patterns {
		if matchSegments(p.segments, name) {
			watched = !p.negated
		}
	}
	return watched
}

// matchSegments matches a pattern against a path, one directory at a time. A
// pattern that runs out before the path does has matched a directory the file
// is inside, which counts: "apps/web" covers "apps/web/src/main.ts".
func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			rest := pattern[1:]
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := slashpath.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return true
}

// Touches reports whether the push changed anything an app with these patterns
// watches. A push whose files are not known touches everything: a deploy that
// was not needed costs a build, and one that was needed and skipped leaves the
// old code running with nothing to say so.
func (e PushEvent) Touches(patterns []string) bool {
	if len(patterns) == 0 || !e.FilesKnown {
		return true
	}
	compiled := compileWatchPaths(patterns)
	for _, file := range e.ChangedFiles {
		if watches(compiled, file) {
			return true
		}
	}
	return false
}
