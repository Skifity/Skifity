package secretmgr

import (
	"fmt"
	"slices"
	"strings"

	"skifity/internal/store"
)

// What a connection may be used for.
//
// A connection is the team's, and its credentials read whatever the manager's
// own policy lets them. Without a limit, anybody who may set a variable in any
// of the team's projects can point one at any of that, deploy, and read the
// value from inside their own app. An administrator can therefore limit a
// connection to some path prefixes and to some projects; empty is no limit.
//
// Both are checked when a reference is set and again every time one is read,
// so a connection narrowed after a variable was pointed at it stops that
// variable at the next deploy, sync or refresh.
//
// The limit is the panel's, and it only binds what goes through the panel.
// The manager's own policy is the real boundary: the credentials read what
// that policy allows whoever holds them. One connection per project, each
// with a policy that reads only that project's secrets, is the setup to aim
// for; these limits are what keep a shared one from being a way around it.

// maxAllowedPaths bounds a connection's path limits. A limit is a handful of
// prefixes; hundreds is a policy that belongs in the manager.
const maxAllowedPaths = 100

// separator is what a path of this kind divides at, and so where a limit's
// prefix may end: "app" allows "app/db", and never "app-b".
func separator(kind string) string {
	if kind == KindDoppler {
		// A Doppler secret is named by its name alone — the service token
		// already names the project and the config — and names divide at the
		// underscore: BILLING allows BILLING_STRIPE_KEY and not BILLINGS.
		return "_"
	}
	return "/"
}

// limitForm is a path the way a limit compares it: spelled the way the
// manager is asked for it, so two spellings of one secret compare alike.
//
// ok is false for a path that cannot be compared: one with a . segment, which
// a server might resolve to somewhere else, is under no limit.
func limitForm(kind, path string) (string, bool) {
	path = strings.TrimSpace(path)
	switch kind {
	case KindVault:
		// What vault.fetch asks for: the path under the mount, without a
		// slash at either end.
		path = strings.Trim(path, "/")
	case KindInfisical:
		// What splitInfisicalPath makes of it: a path from the root folder.
		path = "/" + strings.Trim(path, "/")
	case KindDoppler:
		// Doppler's names are upper case. One written in lower case is
		// compared as the name it is, rather than let past a limit.
		path = strings.ToUpper(path)
	case KindAWS:
		// A name or an ARN, compared as written: a name is allowed by a
		// prefix that is a name, and an ARN by one that is an ARN. Reading
		// the name out of an ARN would let a secret of the same name in
		// another account or region past a limit meant for this one.
	default:
		return "", false
	}
	if path == "" || path == "/" {
		return "", false
	}
	if separator(kind) == "/" {
		for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
			if segment == "." || segment == ".." {
				return "", false
			}
		}
	}
	return path, true
}

// PathAllowed reports whether a connection limited to prefixes may read path:
// the path is one of them, or under one at the kind's separator. A prefix is
// never a string prefix. No prefixes is every path.
func PathAllowed(kind string, prefixes []string, path string) bool {
	if len(prefixes) == 0 {
		return true
	}
	form, ok := limitForm(kind, path)
	if !ok {
		return false
	}
	sep := separator(kind)
	for _, prefix := range prefixes {
		if form == prefix || strings.HasPrefix(form, prefix+sep) {
			return true
		}
	}
	return false
}

// ProjectAllowed reports whether a connection limited to some projects may be
// read by a variable of this one. No projects is every project; a variable
// whose project is not known is allowed only then.
func ProjectAllowed(allowed []string, projectID string) bool {
	return len(allowed) == 0 || (projectID != "" && slices.Contains(allowed, projectID))
}

// Limit names which of a connection's limits refused a reference.
type Limit string

// The limits a connection can have.
const (
	LimitProject Limit = "project"
	LimitPath    Limit = "path"
)

// Refuses says which of a connection's limits refuses a variable of a project
// reading a path through it, or "" when neither does.
func Refuses(c store.SecretConnection, projectID, path string) Limit {
	if !ProjectAllowed(c.AllowedProjectIDs, projectID) {
		return LimitProject
	}
	if !PathAllowed(c.Kind, c.AllowedPaths, path) {
		return LimitPath
	}
	return ""
}

// NormalizeAllowedPaths checks a connection's path limits and answers them
// the way PathAllowed compares them, in the order given, without blanks or
// repeats.
//
// A limit is written the way a reference's path is, with two allowances for
// what a policy says: a trailing /* (_* for Doppler) is the prefix itself,
// which already covers everything under it; and a Vault prefix may start with
// the mount's own <mount>/data/, the way a Vault policy writes it. Any other
// * is refused rather than read as a string prefix, which would let app* past
// to app-b. A prefix that is every path is refused: an empty list says that.
//
// The error is a sentence for a person.
func NormalizeAllowedPaths(kind string, settings map[string]string, prefixes []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range prefixes {
		written := strings.TrimSpace(raw)
		if written == "" {
			continue
		}
		prefix, err := normalizePrefix(kind, settings, written)
		if err != nil {
			return nil, err
		}
		if !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	if len(out) > maxAllowedPaths {
		return nil, fmt.Errorf("a connection is limited to at most %d paths; a longer list belongs in the secret manager's own policy", maxAllowedPaths)
	}
	return out, nil
}

func normalizePrefix(kind string, settings map[string]string, written string) (string, error) {
	sep := separator(kind)
	prefix := strings.TrimSuffix(written, sep+"*")
	if prefix == "*" {
		return "", fmt.Errorf("%q is every path; leave the list empty for that", written)
	}
	if strings.Contains(prefix, "*") {
		return "", fmt.Errorf("%q has a * in it: a limit is a path, and it already covers everything under it at a %s", written, sep)
	}
	switch kind {
	case KindVault:
		prefix = strings.Trim(prefix, "/")
		if mount := strings.Trim(settings["mount"], "/"); mount != "" {
			if prefix == mount+"/data" {
				return "", fmt.Errorf("%q is the whole %s mount; leave the list empty for every path", written, mount)
			}
			prefix = strings.TrimPrefix(prefix, mount+"/data/")
		}
	case KindInfisical:
		prefix = "/" + strings.Trim(prefix, "/")
	case KindDoppler:
		prefix = strings.ToUpper(strings.TrimRight(prefix, "_"))
	case KindAWS:
		prefix = strings.TrimRight(prefix, "/")
	default:
		return "", fmt.Errorf("%q is not a kind of secret manager", kind)
	}
	if prefix == "" || prefix == "/" {
		return "", fmt.Errorf("%q is every path; leave the list empty for that", written)
	}
	// A prefix no reference could ever be under is a typo, and is found now
	// rather than as a variable nobody can set.
	if err := CheckReference(kind, prefix, ""); err != nil {
		return "", fmt.Errorf("%q is not a path a secret can be under in %s", written, KindLabel(kind))
	}
	if form, ok := limitForm(kind, prefix); !ok || form != prefix {
		return "", fmt.Errorf("%q is not a path a secret can be under in %s", written, KindLabel(kind))
	}
	return prefix, nil
}

// DescribePaths is a connection's path limit in words, for a sentence that
// says what it reads.
func DescribePaths(prefixes []string) string {
	if len(prefixes) == 0 {
		return "any path"
	}
	shown := prefixes
	more := ""
	if len(shown) > 10 {
		shown, more = shown[:10], fmt.Sprintf(" and %d more", len(prefixes)-10)
	}
	return strings.Join(shown, ", ") + more
}
