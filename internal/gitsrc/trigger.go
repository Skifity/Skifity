package gitsrc

import (
	"errors"
	"fmt"
	slashpath "path"
	"strings"
)

// What a push has to be to deploy an app.
//
// An app deploys every push to its branch, or — for somebody who releases by
// tagging — only the tags whose name matches a pattern, and then not the
// pushes to the branch as well: building every commit and then the tag on one
// of them is two deploys of the same code, the first of which nobody asked
// for. And a push whose head commit says "[skip ci]" deploys nothing, because
// that is how every CI system is told a commit is not worth building, and
// Coolify and Dokploy honour it too.

const (
	// DeployOnBranch deploys every push to the app's branch. It is what every
	// app did before there was a choice.
	DeployOnBranch = "branch"
	// DeployOnTag deploys a pushed tag whose name matches the app's pattern,
	// and nothing pushed to a branch.
	DeployOnTag = "tag"
	// DefaultTagPattern is the pattern an app is given when it has none: the
	// way most projects spell a release.
	DefaultTagPattern = "v*"
)

// skipMarkers ask a push not to deploy. The first two are what GitHub
// Actions, GitLab CI and most others read; the last two say what is meant for
// somebody whose CI should still run.
var skipMarkers = []string{"[skip ci]", "[ci skip]", "[skip deploy]", "[no deploy]"}

// SkipMarker returns the marker in a commit message that asks for it not to be
// deployed, in the spelling it is documented in, or "" when there is none.
// Case does not matter: "[Skip CI]" is somebody asking the same thing.
func SkipMarker(message string) string {
	lower := strings.ToLower(message)
	for _, marker := range skipMarkers {
		if strings.Contains(lower, marker) {
			return marker
		}
	}
	return ""
}

// maxTagPatternLength bounds a pattern. A tag name is a short thing, and a
// pattern that is not is a mistake rather than a need.
const maxTagPatternLength = 100

// ErrBadTagPattern is a tag pattern that cannot be matched against anything.
var ErrBadTagPattern = errors.New("that is not a tag pattern")

// ValidTagPattern checks a pattern a tag's name is matched against. The syntax
// is path.Match's: * is any run of characters but a slash, ? one character,
// and [0-9] one of a set.
func ValidTagPattern(pattern string) error {
	if pattern == "" || len(pattern) > maxTagPatternLength {
		return fmt.Errorf("%w: a pattern is between 1 and %d characters", ErrBadTagPattern, maxTagPatternLength)
	}
	for _, r := range pattern {
		// Git refuses these in a ref name, so a pattern holding one matches
		// nothing and was not meant.
		if r <= ' ' || r == 0x7f || strings.ContainsRune("~^:", r) {
			return fmt.Errorf("%w: a tag name cannot contain spaces, control characters, a tilde, a caret or a colon",
				ErrBadTagPattern)
		}
	}
	// path.Match reports a malformed pattern only when it gets as far as the
	// bad part, so it is matched against something that reaches it.
	if _, err := slashpath.Match(pattern, pattern); err != nil {
		return fmt.Errorf("%w: %w", ErrBadTagPattern, err)
	}
	return nil
}

// TagMatches reports whether a tag's name matches an app's pattern. A pattern
// that is not one matches nothing: it was checked when it was saved, and a
// deploy nobody asked for is worse than one that did not happen.
func TagMatches(pattern, tag string) bool {
	if tag == "" {
		return false
	}
	ok, err := slashpath.Match(pattern, tag)
	return err == nil && ok
}
