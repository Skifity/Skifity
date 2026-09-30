// Package manifests renders the panel's own Kubernetes objects.
//
// The files in deploy/ carry __PLACEHOLDER__ values that the installer fills
// in. Rendering them goes through here so that one rule decides what a
// placeholder looks like, and so that a manifest applied with a placeholder
// still in it is an error rather than a Deployment that pulls an image called
// "__IMAGE__".
package manifests

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// placeholderPattern matches __NAME__, which is the only substitution the
// manifests use. Anything else in the files is literal YAML.
var placeholderPattern = regexp.MustCompile(`__[A-Z0-9_]+__`)

// Placeholders lists the distinct placeholder names a manifest uses, sorted.
//
// The names are returned without their underscores, so __IMAGE__ comes back as
// "IMAGE".
func Placeholders(source string) []string {
	seen := map[string]bool{}
	for _, match := range placeholderPattern.FindAllString(source, -1) {
		seen[strings.Trim(match, "_")] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Render substitutes every placeholder and reports any that had no value.
//
// An empty value is allowed: an ACME account without an email address is a
// legitimate choice. A missing key is not.
func Render(source string, values map[string]string) (string, error) {
	var missing []string
	out := placeholderPattern.ReplaceAllStringFunc(source, func(match string) string {
		name := strings.Trim(match, "_")
		value, ok := values[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return value
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("no value for %s", strings.Join(unique(missing), ", "))
	}
	return out, nil
}

func unique(values []string) []string {
	out := values[:0:0]
	var last string
	for i, value := range values {
		if i == 0 || value != last {
			out = append(out, value)
		}
		last = value
	}
	return out
}
