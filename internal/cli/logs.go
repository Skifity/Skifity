package cli

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// instanceExit is how an instance's previous container ended, as the
// panel's status says it.
type instanceExit struct {
	Reason     string    `json:"reason"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// summary says how it ended in words: the kubelet's OOMKilled is the one
// reason that matters most and reads least like one.
func (e instanceExit) summary() string {
	switch e.Reason {
	case "OOMKilled":
		return fmt.Sprintf("out of memory (exit code %d)", e.ExitCode)
	case "Completed":
		return "exited by itself (exit code 0)"
	case "", "Error":
		return fmt.Sprintf("exit code %d", e.ExitCode)
	default:
		return fmt.Sprintf("%s (exit code %d)", e.Reason, e.ExitCode)
	}
}

// logLine is one line of an app's log as the panel sends it: which instance
// wrote it, and when.
type logLine struct {
	Instance string    `json:"instance,omitempty"`
	At       time.Time `json:"at,omitzero"`
	Line     string    `json:"line"`
}

// logPrinter writes an app's log for a person: each line with the instance
// that wrote it in front, once more than one is being read, and its time when
// that was asked for.
type logPrinter struct {
	out        io.Writer
	timestamps bool
	// several stays true once it is: a column that comes and goes as an
	// instance does is harder to read than one that is always there.
	several bool
	seen    string
}

// reading is which instances the panel says it is reading, and how many more
// it is not.
func (p *logPrinter) reading(instances []string, omitted int) {
	if len(instances) > 1 {
		p.several = true
	}
	if omitted > 0 {
		fmt.Fprintf(p.out, "Reading %d of %d instances: --instance NAME reads one of the others.\n",
			len(instances), len(instances)+omitted)
	}
}

func (p *logPrinter) print(line logLine) {
	// A panel that does not say which it is reading still says who wrote
	// each line.
	if line.Instance != "" {
		if p.seen != "" && p.seen != line.Instance {
			p.several = true
		}
		p.seen = line.Instance
	}
	var b strings.Builder
	if p.timestamps && !line.At.IsZero() {
		b.WriteString(line.At.UTC().Format("2006-01-02T15:04:05.000Z07:00"))
		b.WriteByte(' ')
	}
	if p.several && line.Instance != "" {
		fmt.Fprintf(&b, "[%s] ", shortInstance(line.Instance))
	}
	b.WriteString(line.Line)
	fmt.Fprintln(p.out, b.String())
}

// shortInstance is the part of an instance's name after its last hyphen: what
// tells an app's instances apart, and what --instance takes.
func shortInstance(name string) string {
	if i := strings.LastIndexByte(name, '-'); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return name
}
