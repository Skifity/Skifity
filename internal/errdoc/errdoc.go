// Package errdoc turns failures into explanations.
//
// Every error a user can see carries four things: what happened, why it matters,
// how to fix it, and enough context to paste into a search box or an AI
// assistant. A bare "exit status 1" is never shown.
//
// The same Problem renders three ways:
//
//   - as JSON for the panel, where the UI shows cause, impact and fix;
//   - as text for the CLI;
//   - as a Markdown block for the "Copy error for AI" button.
package errdoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Severity tells the UI how loudly to present a Problem.
type Severity string

const (
	// SeverityError means the operation did not happen.
	SeverityError Severity = "error"
	// SeverityWarning means it happened, but something needs attention.
	SeverityWarning Severity = "warning"
	// SeverityInfo is used for blocked-but-expected situations, such as a
	// preflight check that found a supported but unusual setup.
	SeverityInfo Severity = "info"
)

// Problem is a failure with an explanation attached.
type Problem struct {
	// Code is a stable machine-readable identifier such as "ssh.auth_failed".
	// The UI uses it to pick a translated message; it never changes once shipped.
	Code string `json:"code"`
	// Title is a short summary in English, used as a fallback when the UI has
	// no translation for Code.
	Title string `json:"title"`
	// Cause explains what actually went wrong, in the user's terms.
	Cause string `json:"cause,omitempty"`
	// Impact explains what is and is not working as a result.
	Impact string `json:"impact,omitempty"`
	// Fix is what to do about it, with exact commands where they exist.
	Fix string `json:"fix,omitempty"`
	// Args are the values interpolated into Cause, Impact and Fix.
	//
	// The panel shows a sentence from its own locale rather than the English
	// one above, and a translated sentence needs the same values in it. They
	// are carried separately, each rendered by the verb that produced it, so
	// the interface fills "%s is where this panel answers." in Russian with the
	// hostname the English one carries.
	Args Args `json:"args,omitzero"`
	// DocsPath links to the relevant documentation page.
	DocsPath string `json:"docs_path,omitempty"`
	// Severity drives presentation.
	Severity Severity `json:"severity"`
	// Retryable tells the UI whether to offer a Retry button.
	Retryable bool `json:"retryable"`
	// Status is the HTTP status to answer with.
	Status int `json:"-"`
	// Context is structured detail: the host, the exit code, the command.
	// Values here are shown to the user and copied for AI, so they must never
	// contain secrets.
	Context map[string]string `json:"context,omitempty"`
	// Err is the underlying error, kept for logs and errors.Is, never shown.
	Err error `json:"-"`
	// At is when the problem occurred.
	At time.Time `json:"at"`
}

// Error implements error.
func (p *Problem) Error() string {
	if p.Cause != "" {
		return p.Title + ": " + p.Cause
	}
	return p.Title
}

// Unwrap exposes the underlying error to errors.Is and errors.As.
func (p *Problem) Unwrap() error { return p.Err }

// New starts a Problem from a code and title.
func New(code, title string) *Problem {
	return &Problem{
		Code:     code,
		Title:    title,
		Severity: SeverityError,
		Status:   http.StatusInternalServerError,
		At:       time.Now().UTC(),
	}
}

// Args are the values that went into a Problem's sentences.
//
// Positional, because that is what the Go call sites already are: a hundred
// and seventeen of them pass their values to a format string, and naming every
// one of them would be a hundred and seventeen edits for no gain. The locale
// writes {{0}} and {{1}} where the English writes %s and %d, in the same order.
type Args struct {
	Title  []string `json:"title,omitempty"`
	Cause  []string `json:"cause,omitempty"`
	Impact []string `json:"impact,omitempty"`
	Fix    []string `json:"fix,omitempty"`
}

// Newf starts a Problem whose title carries a value.
//
// Four errors named the thing they were about — "That deployment does not
// exist", "Cloudflare tunnel is not installed by the panel" — by concatenating
// a variable into the title. That reads well and cannot be translated: the
// locale has no way to say where the value goes, and the test that checks every
// error has words could not read the English either, so all four were missing
// from the catalogue and from the list of what was missing.
func Newf(code, format string, args ...any) *Problem {
	p := New(code, fmt.Sprintf(format, args...))
	p.Args.Title = renderArgs(format, args)
	return p
}

// WithCause sets the explanation of what went wrong.
func (p *Problem) WithCause(format string, args ...any) *Problem {
	p.Cause = fmt.Sprintf(format, args...)
	p.Args.Cause = renderArgs(format, args)
	return p
}

// WithImpact sets what this means for the user.
func (p *Problem) WithImpact(format string, args ...any) *Problem {
	p.Impact = fmt.Sprintf(format, args...)
	p.Args.Impact = renderArgs(format, args)
	return p
}

// WithFix sets the suggested remedy.
func (p *Problem) WithFix(format string, args ...any) *Problem {
	p.Fix = fmt.Sprintf(format, args...)
	p.Args.Fix = renderArgs(format, args)
	return p
}

// Sprintf formats a sentence and hands back the values that went into it.
//
// The same trade as a Problem's own sentences, for the three other places the
// server writes English a person reads: the scaling findings, the preflight
// report and the messages under each step of adding a server. The English is
// the fallback and what the CLI prints; the values let the panel fill the same
// sentence in another language.
func Sprintf(format string, args ...any) (string, []string) {
	return fmt.Sprintf(format, args...), renderArgs(format, args)
}

// renderArgs formats each argument on its own, with the verb that was going to
// print it.
//
// Not "%v" for everything: %q puts the quotes on, %d would print a rune as a
// number, and the value the panel shows has to be the value the English
// sentence shows. An argument with no verb left to match — which means the
// format string and the call disagree, and fmt has already written %!s(MISSING)
// into the sentence — falls back to %v rather than being dropped.
func renderArgs(format string, args []any) []string {
	if len(args) == 0 {
		return nil
	}
	verbs := verbsIn(format)
	out := make([]string, len(args))
	for i, arg := range args {
		verb := "%v"
		if i < len(verbs) {
			verb = verbs[i]
		}
		out[i] = fmt.Sprintf(verb, arg)
	}
	return out
}

// verbsIn returns the formatting verbs a format string uses, in order.
func verbsIn(format string) []string {
	var verbs []string
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		// "%%" is a literal per cent and consumes no argument.
		if i+1 < len(format) && format[i+1] == '%' {
			i++
			continue
		}
		end := i + 1
		for end < len(format) && strings.ContainsRune("+-# 0123456789.", rune(format[end])) {
			end++
		}
		if end < len(format) {
			verbs = append(verbs, format[i:end+1])
			i = end
		}
	}
	return verbs
}

// WithDocs links a documentation page.
func (p *Problem) WithDocs(path string) *Problem {
	p.DocsPath = path
	return p
}

// WithStatus sets the HTTP status.
func (p *Problem) WithStatus(status int) *Problem {
	p.Status = status
	return p
}

// WithSeverity overrides the default severity.
func (p *Problem) WithSeverity(s Severity) *Problem {
	p.Severity = s
	return p
}

// Retry marks the problem as worth retrying, which shows a Retry button.
func (p *Problem) Retry() *Problem {
	p.Retryable = true
	return p
}

// With adds one piece of context. Keys should be stable, lowercase and
// snake_case so they read well in the copied block.
func (p *Problem) With(key, value string) *Problem {
	if value == "" {
		return p
	}
	if p.Context == nil {
		p.Context = map[string]string{}
	}
	p.Context[key] = value
	return p
}

// Wrap attaches the underlying error.
func (p *Problem) Wrap(err error) *Problem {
	p.Err = err
	if err != nil {
		return p.With("underlying_error", err.Error())
	}
	return p
}

// Is lets errors.Is match two Problems by code.
func (p *Problem) Is(target error) bool {
	var other *Problem
	if errors.As(target, &other) {
		return other.Code == p.Code
	}
	return false
}

// From converts any error into a Problem. Errors that are already Problems pass
// through unchanged, so context added deep in the stack survives.
func From(err error) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return New("internal", "Something went wrong").
		WithCause("%s", err.Error()).
		WithImpact("The action you asked for did not complete.").
		WithFix("Try again. If it keeps happening, copy this error and open an issue.").
		Wrap(err)
}

// Text renders a Problem for a terminal.
func (p *Problem) Text() string {
	var b strings.Builder
	b.WriteString(p.Title)
	b.WriteString("\n")
	if p.Cause != "" {
		fmt.Fprintf(&b, "\n  What happened: %s\n", p.Cause)
	}
	if p.Impact != "" {
		fmt.Fprintf(&b, "  What it means: %s\n", p.Impact)
	}
	if p.Fix != "" {
		fmt.Fprintf(&b, "  How to fix it: %s\n", p.Fix)
	}
	if len(p.Context) > 0 {
		b.WriteString("\n  Details:\n")
		for _, k := range slices.Sorted(maps.Keys(p.Context)) {
			fmt.Fprintf(&b, "    %s: %s\n", k, p.Context[k])
		}
	}
	if p.DocsPath != "" {
		fmt.Fprintf(&b, "\n  Docs: %s\n", p.DocsPath)
	}
	fmt.Fprintf(&b, "\n  Error code: %s\n", p.Code)
	return b.String()
}

// Markdown renders a Problem as a block to paste into an AI assistant. This is
// what the "Copy error for AI" button produces: enough context to act on,
// nothing an assistant would have to guess at.
func (p *Problem) Markdown(productName, productVersion string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s error: %s\n\n", productName, p.Title)
	fmt.Fprintf(&b, "- **Error code**: `%s`\n", p.Code)
	fmt.Fprintf(&b, "- **When**: %s\n", p.At.Format(time.RFC3339))
	fmt.Fprintf(&b, "- **%s version**: %s\n", productName, productVersion)
	if p.Cause != "" {
		fmt.Fprintf(&b, "\n**What happened**\n\n%s\n", p.Cause)
	}
	if p.Impact != "" {
		fmt.Fprintf(&b, "\n**What it means**\n\n%s\n", p.Impact)
	}
	if p.Fix != "" {
		fmt.Fprintf(&b, "\n**Suggested fix**\n\n%s\n", p.Fix)
	}
	if len(p.Context) > 0 {
		b.WriteString("\n**Context**\n\n```\n")
		for _, k := range slices.Sorted(maps.Keys(p.Context)) {
			fmt.Fprintf(&b, "%s: %s\n", k, p.Context[k])
		}
		b.WriteString("```\n")
	}
	b.WriteString("\n**What I need**\n\nExplain what is wrong and give me the exact steps to fix it.\n")
	return b.String()
}

// JSON renders a Problem as the API response body.
func (p *Problem) JSON() ([]byte, error) {
	type envelope struct {
		Error *Problem `json:"error"`
	}
	data, err := json.Marshal(envelope{Error: p})
	if err != nil {
		return nil, fmt.Errorf("encode problem: %w", err)
	}
	return data, nil
}
