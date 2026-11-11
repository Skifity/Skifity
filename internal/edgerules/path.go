package edgerules

import (
	"fmt"
	"strings"
)

// What the path is.
//
// A rule about /admin is worth exactly as much as the number of ways of
// writing /admin it catches. An app does not route the bytes a client sent: Go's
// net/http, a WSGI server, Tomcat and nearly everything else decode
// percent-escapes, drop a ;jsessionid=… parameter, collapse // and resolve .
// and .. before they choose a handler, and several treat a backslash as a
// slash. A firewall comparing the raw bytes is one that /%61dmin, //admin,
// /x/../admin, /admin;x=1 and \admin each walk around, while the app behind it
// serves every one of them as /admin.
//
// So a rule is asked about the path an app would route, written one way. What
// cannot be read one way is refused rather than guessed at: an escape that is
// not one, a control character, and a slash, backslash or dot encoded twice.
// The last because an app that decodes once and one that decodes twice
// disagree about where %252F leads, and the firewall cannot know which of the
// two is behind it.
//
// Case is kept. A comparison lowercases both sides already, and a pattern
// should see the path as it was written.

// CanonicalPath is the path a request asked for, as an app would route it.
//
// In order: the query and fragment are cut off; an invalid escape, an encoded
// or raw control character and a doubly encoded '/', '\' or '.' are refused;
// the rest is decoded exactly once; a backslash becomes a slash; a ;parameter
// is removed from each segment; repeated slashes become one; and . and .. are
// resolved, never above the root. A trailing slash is kept, and the result
// always starts with one.
func CanonicalPath(raw string) (string, error) {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	// Most paths are already what this would make of them, and the guard asks
	// on every request, for the path and for every value a rule compares it
	// with. Those cost one look at each byte and nothing else.
	if alreadyCanonical(raw) {
		return raw, nil
	}
	decoded, err := decodeOnce(raw)
	if err != nil {
		return "", err
	}
	segments := strings.Split(strings.ReplaceAll(decoded, `\`, "/"), "/")

	kept := make([]string, 0, len(segments))
	trailing := false
	for i, segment := range segments {
		segment = withoutParameter(segment)
		last := i == len(segments)-1
		switch segment {
		case "", ".":
			// An empty segment is a doubled slash, and "." is the segment
			// itself; at the end, either one means the path ends with a slash.
			trailing = last
		case "..":
			if len(kept) > 0 {
				kept = kept[:len(kept)-1]
			}
			trailing = last
		default:
			kept = append(kept, segment)
			trailing = false
		}
	}
	if len(kept) == 0 {
		return "/", nil
	}
	path := "/" + strings.Join(kept, "/")
	if trailing {
		path += "/"
	}
	return path, nil
}

// canonicalFragment reads a piece of a path the way CanonicalPath reads a
// whole one, for a rule that compares with the middle or the end of it: decoded
// once, a backslash as a slash, a ;parameter dropped from each segment and
// repeated slashes as one. A leading slash is not added, because ".php" is a
// thing a path ends with and "/.php" is not, and . and .. are left as they are,
// because without the segments to their left there is nothing to resolve them
// against.
func canonicalFragment(raw string) (string, error) {
	decoded, err := decodeOnce(raw)
	if err != nil {
		return "", err
	}
	segments := strings.Split(strings.ReplaceAll(decoded, `\`, "/"), "/")
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment = withoutParameter(segment); segment != "" {
			kept = append(kept, segment)
		}
	}
	fragment := strings.Join(kept, "/")
	if len(kept) == 0 {
		// Nothing but slashes and parameters: a slash, or nothing at all.
		if strings.Contains(decoded, "/") || strings.Contains(decoded, `\`) {
			return "/", nil
		}
		return "", nil
	}
	if strings.HasPrefix(segments[0], ";") || segments[0] == "" {
		fragment = "/" + fragment
	}
	if last := segments[len(segments)-1]; last == "" || strings.HasPrefix(last, ";") {
		fragment += "/"
	}
	return fragment, nil
}

// canonicalValue is what a rule's value is compared as: a whole path for an
// operator that compares with the whole path or its start, a fragment for one
// that compares with its middle or its end.
func canonicalValue(op Operator, value string) (string, error) {
	switch op {
	case OpContains, OpEndsWith:
		return canonicalFragment(value)
	default:
		return CanonicalPath(value)
	}
}

// decodeOnce turns every percent-escape into its byte, refusing what cannot
// be read one way.
func decodeOnce(raw string) (string, error) {
	if !strings.ContainsAny(raw, "%") {
		if i := strings.IndexFunc(raw, isControl); i >= 0 {
			return "", fmt.Errorf("the path holds a control character (byte %#02x)", raw[i])
		}
		return raw, nil
	}
	var out strings.Builder
	out.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if isControl(rune(c)) {
			return "", fmt.Errorf("the path holds a control character (byte %#02x)", c)
		}
		if c != '%' {
			out.WriteByte(c)
			continue
		}
		if i+2 >= len(raw) || !isHex(raw[i+1]) || !isHex(raw[i+2]) {
			end := min(i+3, len(raw))
			return "", fmt.Errorf("%q in the path is not a percent-escape", raw[i:end])
		}
		value := unhex(raw[i+1])<<4 | unhex(raw[i+2])
		if isControl(rune(value)) {
			return "", fmt.Errorf("%q in the path is an encoded control character", raw[i:i+3])
		}
		out.WriteByte(value)
		i += 2
	}
	decoded := out.String()
	// What one decoding leaves as an escape of a slash, a backslash or a dot is
	// one that was encoded twice, however it was spelled the first time.
	for i := 0; i+2 < len(decoded); i++ {
		if decoded[i] != '%' || !isHex(decoded[i+1]) || !isHex(decoded[i+2]) {
			continue
		}
		switch unhex(decoded[i+1])<<4 | unhex(decoded[i+2]) {
		case '/', '\\', '.':
			return "", fmt.Errorf("the path holds %q encoded twice, which an app that decodes once and one that decodes twice read differently",
				decoded[i:i+3])
		}
	}
	return decoded, nil
}

// alreadyCanonical reports whether CanonicalPath would return raw unchanged:
// it starts with a slash and holds no escape, backslash, parameter, control
// character, doubled slash or dot segment.
func alreadyCanonical(raw string) bool {
	if raw == "" || raw[0] != '/' {
		return false
	}
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '%' || c == '\\' || c == ';' || isControl(rune(c)):
			return false
		case c == '/':
			rest := raw[i+1:]
			if strings.HasPrefix(rest, "/") || rest == "." || rest == ".." ||
				strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, "../") {
				return false
			}
		}
	}
	return true
}

// withoutParameter drops a ;parameter from a path segment, which is how
// Tomcat and every servlet container carry a session id and what none of them
// routes on.
func withoutParameter(segment string) string {
	if i := strings.IndexByte(segment, ';'); i >= 0 {
		return segment[:i]
	}
	return segment
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
