// Package shellsafe quotes a value for a shell script the panel generates.
//
// It exists because of a trap that is very easy to fall into and very hard to
// see afterwards: Go's %q looks like shell quoting and is not. It produces a Go
// double-quoted literal, escaping backslashes and double quotes — and a shell
// reading a double-quoted string still expands $, `, and \. So
//
//	fmt.Sprintf("TARGET=%q", untrusted)
//
// with untrusted = `1.2.3.4$(id)` produces TARGET="1.2.3.4$(id)", and the shell
// runs id. The code reads as though it is defended and is not, and every
// generated script in this repository that interpolates a value used %q.
//
// Single quotes are the only quoting a POSIX shell does not look inside. Quote
// wraps a value in them and escapes the one character that can end them, which
// makes the result inert whatever it contains.
package shellsafe

import "strings"

// Quote renders a value as a single-quoted shell word.
//
// A single-quoted string in POSIX sh has no escape sequences at all and ends at
// the next single quote, so the only thing to handle is a single quote in the
// value: close the string, add an escaped quote, open it again.
//
//	don't  ->  'don'\''t'
//
// The result is always quoted, including for an empty value, because an
// unquoted empty word disappears and changes how many arguments a command gets.
func Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// Join quotes each value and joins them with spaces, for a command line.
func Join(values ...string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, Quote(value))
	}
	return strings.Join(quoted, " ")
}
