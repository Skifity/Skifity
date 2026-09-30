// Package logging sets up structured logging and keeps secrets out of it.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// New builds a slog.Logger. format is "json" or "text"; level is one of
// debug, info, warn, error.
func New(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(&redactor{inner: h})
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Redacted is the placeholder written in place of a sensitive value.
const Redacted = "[redacted]"

// Public wraps a value that must survive redaction.
//
// Redacting by key name is the right default, but a few values have
// secret-shaped names and still have to be readable: the one-time setup token
// is the only way to claim a fresh panel, and a redacted one leaves an operator
// with a panel nobody can ever sign in to. Wrapping is deliberate and greppable,
// which a carefully chosen key name would not be.
type Public struct{ Value any }

// String makes a wrapped value print as itself.
func (p Public) String() string { return fmt.Sprint(p.Value) }

// LogValue keeps the wrapper invisible in the output.
func (p Public) LogValue() slog.Value { return slog.AnyValue(p.Value) }

// sensitiveKey matches attribute names whose values must never be logged.
var sensitiveKey = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|authorization|cookie|private|otp|seed|signature)`)

// valuePattern matches secret-shaped substrings inside otherwise ordinary
// messages, such as a PEM block or a bearer token pasted into an error.
var valuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{16,}`),
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
}

// redactor wraps a slog.Handler and rewrites sensitive attributes and messages.
// Redacting in the handler rather than at each call site means a new log line
// cannot leak a secret by forgetting to redact.
type redactor struct{ inner slog.Handler }

func (r *redactor) Enabled(ctx context.Context, l slog.Level) bool { return r.inner.Enabled(ctx, l) }

func (r *redactor) Handle(ctx context.Context, rec slog.Record) error {
	clean := slog.NewRecord(rec.Time, rec.Level, Scrub(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactAttr(a))
		return true
	})
	return r.inner.Handle(ctx, clean)
}

func (r *redactor) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &redactor{inner: r.inner.WithAttrs(out)}
}

func (r *redactor) WithGroup(name string) slog.Handler {
	return &redactor{inner: r.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	// An explicitly public value is never redacted, whatever it is called.
	if public, ok := a.Value.Any().(Public); ok {
		return slog.Any(a.Key, public.Value)
	}
	if sensitiveKey.MatchString(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	if a.Value.Kind() == slog.KindGroup {
		sub := a.Value.Group()
		out := make([]any, 0, len(sub))
		for _, s := range sub {
			out = append(out, redactAttr(s))
		}
		return slog.Group(a.Key, out...)
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, Scrub(a.Value.String()))
	}
	// An error is the usual carrier of a secret into a log: net/http's
	// *url.Error quotes the whole address, with a Telegram bot's token in its
	// path or a webhook's secret in its query. Only strings were scrubbed, and
	// every `"error", err` is not one.
	if a.Value.Kind() == slog.KindAny {
		switch value := a.Value.Any().(type) {
		case error:
			return slog.String(a.Key, Scrub(value.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Scrub(value.String()))
		}
	}
	return a
}

// LooksSecret reports whether a variable should be stored as a secret when
// nobody said either way.
//
// It is the same definition the log redaction uses, on purpose: a key that the
// logger would never write out is a key whose value should not come back from
// the API either, and two lists of what counts as sensitive is one list that
// is wrong. One rule is added that the logger has no need of. A connection
// string carries its password inside the value — `DATABASE_URL=postgres://
// shop:hunter2@db/shop` — under a key that matches nothing above.
//
// The direction of a mistake matters. Marking something secret that was not
// costs somebody the ability to read it back; they can still see the name and
// overwrite it. Missing a secret hands it to every member of the team, to the
// browser, and — through the MCP server — to a language model.
func LooksSecret(key, value string) bool {
	if sensitiveKey.MatchString(key) {
		return true
	}
	for _, re := range valuePatterns {
		if re.MatchString(value) {
			return true
		}
	}
	return credentialInURL.MatchString(value)
}

// credentialInURL matches scheme://user:password@host, the shape of every
// database and cache connection string. The user part may be empty, which is
// how Redis writes a password-only URL.
var credentialInURL = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://[^/\s:@]*:[^/\s@]+@`)

// addressSecrets are the parts of an address that are a secret, with what is
// left of it: enough to tell which service it was, and not enough to use it.
var addressSecrets = []struct {
	re   *regexp.Regexp
	with string
}{
	// scheme://user:password@host — a connection string, or a URL with
	// credentials in it.
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^/\s:@]*:)[^/\s@]+@`), "${1}" + Redacted + "@"},
	// A Telegram bot's token is part of every address it is called at.
	{regexp.MustCompile(`/bot\d+:[A-Za-z0-9_\-]{20,}`), "/bot" + Redacted},
	// A Slack or Discord incoming webhook is its own secret.
	{regexp.MustCompile(`(hooks\.slack\.com/(?:services|workflows|triggers)/)[A-Za-z0-9/_\-]+`), "${1}" + Redacted},
	{regexp.MustCompile(`(discord(?:app)?\.com/api/webhooks/\d+/)[A-Za-z0-9_\-]+`), "${1}" + Redacted},
	// A query parameter named like a secret: a presigned URL's signature and
	// credential, an ?access_token=, a ?key=.
	{regexp.MustCompile(`(?i)([?&][^=&\s"']*(?:token|key|secret|signature|password|credential|sig)[^=&\s"']*=)[^&\s"']+`), "${1}" + Redacted},
}

// minimumSecretLength is how long a known secret has to be before it is
// looked for in text. Replacing every "1" or "true" in a sentence because a
// variable happens to hold it would redact the sentence, not the secret.
const minimumSecretLength = 6

// RedactValues scrubs text and then replaces every occurrence of a value known
// to be secret — an app's secret variables — with the placeholder.
//
// Scrub finds what looks like a secret; this finds what is one. Text written by
// the cluster, such as an event's message or a field somebody edited with
// kubectl, can repeat a secret the panel holds under a name that gives nothing
// away, and the shape of the value is no help: a password is just a word.
func RedactValues(text string, secrets []string) string {
	text = Scrub(text)
	for _, secret := range secrets {
		if len(secret) >= minimumSecretLength {
			text = strings.ReplaceAll(text, secret, Redacted)
		}
	}
	return text
}

// Scrub removes secret-shaped substrings from free text. It is exported because
// command output and build logs pass through it before reaching the UI.
func Scrub(s string) string {
	for _, re := range valuePatterns {
		s = re.ReplaceAllString(s, Redacted)
	}
	for _, secret := range addressSecrets {
		s = secret.re.ReplaceAllString(s, secret.with)
	}
	return s
}
