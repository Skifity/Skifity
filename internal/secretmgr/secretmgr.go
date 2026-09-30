// Package secretmgr reads variables' values from the secret managers a team
// already runs: HashiCorp Vault or OpenBao, Infisical, Doppler and AWS Secrets
// Manager.
//
// Some teams may not keep a production secret anywhere but their company's
// manager, and a panel that seals every value in its own database is a panel
// those teams cannot use. A variable can therefore be a reference — a
// connection, a path, and a key inside the secret — instead of a value. The
// reference is stored; the value never is. It is read when the app is
// deployed or its configuration is applied, handed to the cluster in the
// app's own Secret, and forgotten.
//
// Every request goes through internal/netguard, with a timeout and a limit on
// how much of an answer is read. What the panel signs in with is sealed like
// every other secret, and nothing this package returns in an error — which
// ends up in problems, logs and the audit log — carries a value it read.
package secretmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// The kinds of secret manager a connection can be.
const (
	// KindVault is HashiCorp Vault, or OpenBao, which answers the same API:
	// a KV version 2 mount, read with a token or an AppRole.
	KindVault = "vault"
	// KindInfisical is Infisical, cloud or self-hosted, read by a machine
	// identity with universal auth.
	KindInfisical = "infisical"
	// KindDoppler is Doppler, read with a service token, which names the
	// project and the config itself.
	KindDoppler = "doppler"
	// KindAWS is AWS Secrets Manager, read with an access key.
	KindAWS = "aws"
)

// Kinds are the kinds a connection can be, in the order a form offers them.
var Kinds = []string{KindVault, KindInfisical, KindDoppler, KindAWS}

// KindLabel is a kind's name as a person writes it.
func KindLabel(kind string) string {
	switch kind {
	case KindVault:
		return "Vault"
	case KindInfisical:
		return "Infisical"
	case KindDoppler:
		return "Doppler"
	case KindAWS:
		return "AWS Secrets Manager"
	}
	return kind
}

const (
	// requestTimeout bounds one request to a secret manager. A deploy waits
	// for it, and a manager that has not answered in this long is down.
	requestTimeout = 15 * time.Second
	// maxAnswer is the most of an answer that is read. A secret is a few
	// hundred bytes; the largest Secrets Manager allows is 64 KiB. Anything
	// much bigger is not an answer to the question that was asked.
	maxAnswer = 1 << 20
	// maxMessage bounds a manager's own error message, which is quoted.
	maxMessage = 200
)

// Reason is what kind of failure an Error is, which decides the problem a
// person is shown and what it tells them to do.
type Reason string

// The reasons a secret manager could not be read.
const (
	// Unreachable is no answer at all: a refused connection, a timeout, an
	// address the panel will not dial.
	Unreachable Reason = "unreachable"
	// Denied is an answer that the credentials are wrong, or do not allow
	// what was asked.
	Denied Reason = "denied"
	// NotFound is no secret at that path, or no key of that name in it.
	NotFound Reason = "not_found"
	// BadAnswer is an answer that is not what that manager sends.
	BadAnswer Reason = "bad_answer"
)

// Error is a secret manager that could not be read, and why, in a sentence
// that holds no value it read.
type Error struct {
	Reason Reason
	Detail string
}

func (e *Error) Error() string { return e.Detail }

func failure(reason Reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// quote shortens a manager's own message for a sentence of ours.
func quote(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > maxMessage {
		message = message[:maxMessage] + "…"
	}
	return message
}

// --- settings and credentials ---

// field is one setting or credential a kind of connection takes.
type field struct {
	name     string
	required bool
	fallback string
	check    func(string) error
}

// fieldsFor are the settings a kind takes, and the credentials, which can
// depend on the settings: a Vault connection signs in with a token or with an
// AppRole.
func fieldsFor(kind string, settings map[string]string) (settingFields, credentialFields []field, err error) {
	switch kind {
	case KindVault:
		settingFields = []field{
			{name: "address", required: true, check: checkAddress},
			{name: "mount", fallback: "secret", check: checkMount},
			{name: "namespace", check: checkMount},
			{name: "auth", fallback: "token", check: oneOf("token", "approle")},
			{name: "approle_mount", fallback: "approle", check: checkMount},
		}
		if strings.TrimSpace(settings["auth"]) == "approle" {
			credentialFields = []field{{name: "role_id", required: true}, {name: "secret_id", required: true}}
		} else {
			credentialFields = []field{{name: "token", required: true}}
		}
	case KindInfisical:
		settingFields = []field{
			{name: "site_url", fallback: "https://app.infisical.com", check: checkAddress},
			{name: "project_id", required: true, check: checkIdentifier},
			{name: "environment", required: true, check: checkIdentifier},
		}
		credentialFields = []field{{name: "client_id", required: true}, {name: "client_secret", required: true}}
	case KindDoppler:
		credentialFields = []field{{name: "token", required: true}}
	case KindAWS:
		settingFields = []field{
			{name: "region", required: true, check: checkRegion},
			{name: "endpoint", check: checkAddress},
		}
		credentialFields = []field{
			{name: "access_key_id", required: true},
			{name: "secret_access_key", required: true},
			{name: "session_token"},
		}
	default:
		return nil, nil, fmt.Errorf("%q is not a kind of secret manager; it is one of %s", kind, strings.Join(Kinds, ", "))
	}
	return settingFields, credentialFields, nil
}

// Normalize checks a connection's settings and credentials, fills in the
// defaults, and answers them cleaned. Credentials may be nil when only the
// settings are being checked — a connection whose credentials are kept.
//
// The error is a sentence for a person: which field, and what is wrong.
func Normalize(kind string, settings, credentials map[string]string) (map[string]string, map[string]string, error) {
	settingFields, credentialFields, err := fieldsFor(kind, settings)
	if err != nil {
		return nil, nil, err
	}
	cleanSettings, err := clean("setting", settingFields, settings, true)
	if err != nil {
		return nil, nil, err
	}
	if credentials == nil {
		return cleanSettings, nil, nil
	}
	cleanCredentials, err := clean("credential", credentialFields, credentials, false)
	if err != nil {
		return nil, nil, err
	}
	return cleanSettings, cleanCredentials, nil
}

func clean(what string, fields []field, given map[string]string, trim bool) (map[string]string, error) {
	known := map[string]bool{}
	out := map[string]string{}
	for _, f := range fields {
		known[f.name] = true
		value := given[f.name]
		if trim {
			value = strings.TrimSpace(value)
		} else {
			// A credential pasted with its trailing newline is the same
			// credential; one with a space in the middle is not, so only the
			// ends go.
			value = strings.Trim(value, " \t\r\n")
		}
		if value == "" {
			value = f.fallback
		}
		if value == "" {
			if f.required {
				return nil, fmt.Errorf("the %s %s is required", what, f.name)
			}
			continue
		}
		if len(value) > 4096 {
			return nil, fmt.Errorf("the %s %s is longer than 4096 characters", what, f.name)
		}
		if f.check != nil {
			if err := f.check(value); err != nil {
				return nil, fmt.Errorf("the %s %s: %w", what, f.name, err)
			}
		}
		out[f.name] = value
	}
	var unknown []string
	for name, value := range given {
		if !known[name] && strings.TrimSpace(value) != "" {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		names := make([]string, 0, len(fields))
		for _, f := range fields {
			names = append(names, f.name)
		}
		return nil, fmt.Errorf("this kind of secret manager takes no %s called %s; it takes %s",
			what, strings.Join(unknown, ", "), orNothing(names))
	}
	return out, nil
}

func orNothing(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func checkAddress(value string) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an http:// or https:// address", value)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q has a user, a query or a fragment in it; give the address alone", u.Redacted())
	}
	return nil
}

var (
	mountPattern      = regexp.MustCompile(`^[A-Za-z0-9_.\-]+(/[A-Za-z0-9_.\-]+)*$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	regionPattern     = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d+$`)
)

func checkMount(value string) error {
	if !mountPattern.MatchString(value) || strings.Contains(value, "..") {
		return fmt.Errorf("%q is not a path: letters, digits, _ . - and / between them", value)
	}
	return nil
}

func checkIdentifier(value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%q has characters an identifier does not", value)
	}
	return nil
}

func checkRegion(value string) error {
	if !regionPattern.MatchString(value) {
		return fmt.Errorf("%q is not an AWS region, such as eu-central-1", value)
	}
	return nil
}

func oneOf(values ...string) func(string) error {
	return func(value string) error {
		if !slices.Contains(values, value) {
			return fmt.Errorf("%q is not one of %s", value, strings.Join(values, ", "))
		}
		return nil
	}
}

// CredentialNames are the credentials a connection of this kind with these
// settings signs in with, for a form that asks for them.
func CredentialNames(kind string, settings map[string]string) []string {
	_, fields, err := fieldsFor(kind, settings)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.name)
	}
	return out
}

// --- references ---

// ParseReference reads a reference as the CLI writes it:
// connection:path, or connection:path#key.
//
// The connection is everything before the first colon, so an AWS secret's
// ARN, which has colons of its own, is a path like any other.
func ParseReference(text string) (connection, path, key string, err error) {
	connection, rest, found := strings.Cut(strings.TrimSpace(text), ":")
	if !found || connection == "" || rest == "" {
		return "", "", "", fmt.Errorf("%q is not a reference; write it as connection:path, or connection:path#key", text)
	}
	path, key = rest, ""
	if i := strings.LastIndex(rest, "#"); i >= 0 {
		path, key = rest[:i], rest[i+1:]
		if key == "" {
			return "", "", "", fmt.Errorf("%q ends in # with no key after it", text)
		}
	}
	return connection, path, key, nil
}

var (
	vaultPath     = regexp.MustCompile(`^[A-Za-z0-9_.\- @]+(/[A-Za-z0-9_.\- @]+)*$`)
	infisicalPath = regexp.MustCompile(`^/?([A-Za-z0-9_.\-]+/)*[A-Za-z0-9_.\-]+$`)
	dopplerName   = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	awsSecretID   = regexp.MustCompile(`^[A-Za-z0-9/_+=.@:\-]+$`)
)

// CheckReference says whether a path and a key are ones a kind of manager
// can have. It does not ask the manager; Resolve does that.
func CheckReference(kind, path, key string) error {
	if path == "" {
		return errors.New("give the path of the secret")
	}
	if len(path) > 512 || len(key) > 256 {
		return errors.New("the path is at most 512 characters, and the key at most 256")
	}
	if strings.ContainsAny(key, "\x00\r\n\t") || strings.Contains(path, "..") {
		return fmt.Errorf("%q is not a path a secret can be at", path)
	}
	var ok bool
	switch kind {
	case KindVault:
		ok = vaultPath.MatchString(path)
	case KindInfisical:
		ok = infisicalPath.MatchString(path)
	case KindDoppler:
		ok = dopplerName.MatchString(path)
	case KindAWS:
		ok = awsSecretID.MatchString(path)
	default:
		return fmt.Errorf("%q is not a kind of secret manager", kind)
	}
	if !ok {
		return fmt.Errorf("%q is not a path a secret can be at in %s", path, KindLabel(kind))
	}
	return nil
}

// --- what a manager answers ---

// secret is one secret as a manager answered it: fields, for a Vault secret,
// which is a set of keys and values; or one value, for the others.
type secret struct {
	fields map[string]string
	value  string
}

// pick is the value a reference names from a secret: a field of a Vault
// secret, the whole value of another, or a key of one whose value is a JSON
// object — the way AWS keeps several values in one secret, and the way
// anybody can in Infisical or Doppler.
func pick(s secret, path, key string) (string, error) {
	var value string
	switch {
	case s.fields != nil && key != "":
		v, ok := s.fields[key]
		if !ok {
			return "", failure(NotFound, "the secret at %s has no key %s; it has %s", path, key, fieldNames(s.fields))
		}
		value = v
	case s.fields != nil:
		if len(s.fields) != 1 {
			return "", failure(NotFound, "the secret at %s has %d keys (%s), so the reference has to say which: %s#key",
				path, len(s.fields), fieldNames(s.fields), path)
		}
		for _, v := range s.fields {
			value = v
		}
	case key != "":
		var object map[string]any
		if err := json.Unmarshal([]byte(s.value), &object); err != nil || object == nil {
			return "", failure(NotFound, "the secret at %s is not a JSON object, so it has no key %s", path, key)
		}
		raw, ok := object[key]
		if !ok {
			return "", failure(NotFound, "the secret at %s has no key %s; it has %s", path, key, fieldNames(object))
		}
		value = stringOf(raw)
	default:
		value = s.value
	}
	if value == "" {
		// An empty value is almost always the wrong environment or a
		// placeholder somebody meant to fill in, and an app started with it is
		// an app that fails later and further from the cause.
		where := path
		if key != "" {
			where += "#" + key
		}
		return "", failure(NotFound, "the secret at %s is empty", where)
	}
	return value, nil
}

// fieldNames lists a secret's keys, which are names and not values.
func fieldNames[V any](fields map[string]V) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 20 {
		names = append(names[:20], "…")
	}
	if len(names) == 0 {
		return "no keys"
	}
	return strings.Join(names, ", ")
}

// stringOf is a JSON value as an environment variable holds it: a string as
// itself, anything else as its JSON.
func stringOf(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}
