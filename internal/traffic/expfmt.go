package traffic

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Reading Prometheus's text exposition format, version 0.0.4: what Traefik
// serves at /metrics by default, and the only one asked for.
//
// Written here rather than taken from Prometheus's own packages, for the same
// reason internal/metrics writes the format by hand: the part needed is a
// sample line, and the library is a dependency tree for it. The grammar is the
// one in Prometheus's exposition_formats.md:
//
//	metric_name [ "{" label_name "=" `"` label_value `"` { "," ... } [ "," ] "}" ] value [ timestamp ]
//
// Comments, HELP and TYPE lines are skipped. The lines of the metrics that are
// kept are read strictly — a label that does not close, an escape the format
// does not have, a value that is not a number — and one such line refuses the
// whole answer: a pod's counters are compared as a set, and a set read
// partly is a minute counted wrong. Lines of any other metric are read only as
// far as their name, so something this panel does not use cannot make the
// part it does unreadable.

// maxLine is the longest line read. A sample line is a name, a few labels and a
// number; a line longer than this is not one.
const maxLine = 1 << 20

// Sample is one sample line: a metric, its labels and its value.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Parse reads the text exposition format and returns the samples of the
// metrics keep accepts, in the order they were written.
func Parse(r io.Reader, keep func(name string) bool) ([]Sample, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	var out []Sample
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if line == "" || line[0] == '#' {
			continue
		}
		name, rest, err := metricName(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if keep != nil && !keep(name) {
			continue
		}
		sample, err := parseSample(name, rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", n, name, err)
		}
		out = append(out, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the metrics: %w", err)
	}
	return out, nil
}

// metricName reads the name a sample line starts with and returns the rest.
func metricName(line string) (string, string, error) {
	end := 0
	for end < len(line) && isNameChar(line[end], end == 0, true) {
		end++
	}
	if end == 0 {
		return "", "", fmt.Errorf("%q does not start with a metric name", truncate(line))
	}
	if end == len(line) {
		return "", "", fmt.Errorf("%s has no value", line)
	}
	if c := line[end]; c != '{' && c != ' ' && c != '\t' {
		return "", "", fmt.Errorf("%q is not a metric name", truncate(line[:end+1]))
	}
	return line[:end], line[end:], nil
}

// isNameChar is the metric-name alphabet, or the label-name one without the
// colon: a letter, an underscore, a digit other than first.
func isNameChar(c byte, first, colon bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c == ':':
		return colon
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}

// parseSample reads the labels, the value and the optional timestamp after a
// metric's name.
func parseSample(name, rest string) (Sample, error) {
	sample := Sample{Name: name, Labels: map[string]string{}}
	rest = skipBlanks(rest)
	if strings.HasPrefix(rest, "{") {
		var err error
		rest, err = parseLabels(rest[1:], sample.Labels)
		if err != nil {
			return Sample{}, err
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return Sample{}, errors.New("no value")
	}
	if len(fields) > 2 {
		return Sample{}, fmt.Errorf("%q follows the value", strings.Join(fields[2:], " "))
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return Sample{}, fmt.Errorf("%q is not a number", fields[0])
	}
	if len(fields) == 2 {
		if _, err := strconv.ParseInt(fields[1], 10, 64); err != nil {
			return Sample{}, fmt.Errorf("%q is not a timestamp", fields[1])
		}
	}
	sample.Value = value
	return sample, nil
}

// parseLabels reads label pairs up to the closing brace and returns what
// follows it.
func parseLabels(s string, into map[string]string) (string, error) {
	for {
		s = skipBlanks(s)
		if strings.HasPrefix(s, "}") {
			return s[1:], nil
		}
		end := 0
		for end < len(s) && isNameChar(s[end], end == 0, false) {
			end++
		}
		if end == 0 {
			return "", fmt.Errorf("%q is not a label name", truncate(s))
		}
		label := s[:end]
		s = skipBlanks(s[end:])
		if !strings.HasPrefix(s, "=") {
			return "", fmt.Errorf("the label %s has no value", label)
		}
		s = skipBlanks(s[1:])
		if !strings.HasPrefix(s, `"`) {
			return "", fmt.Errorf("the value of %s is not quoted", label)
		}
		value, after, err := labelValue(s[1:])
		if err != nil {
			return "", fmt.Errorf("the value of %s: %w", label, err)
		}
		if _, twice := into[label]; twice {
			return "", fmt.Errorf("the label %s is given twice", label)
		}
		into[label] = value
		s = skipBlanks(after)
		switch {
		case strings.HasPrefix(s, ","):
			s = s[1:]
		case strings.HasPrefix(s, "}"):
		default:
			return "", fmt.Errorf("the labels do not close after %s", label)
		}
	}
}

// labelValue reads a quoted value up to its closing quote. The format has three
// escapes, \\, \" and \n, and nothing else.
func labelValue(s string) (string, string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), s[i+1:], nil
		case '\\':
			if i+1 == len(s) {
				return "", "", errors.New("it ends in the middle of an escape")
			}
			i++
			switch s[i] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			default:
				return "", "", fmt.Errorf(`\%c is not an escape the format has`, s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", errors.New("the quote does not close")
}

func skipBlanks(s string) string { return strings.TrimLeft(s, " \t") }

func truncate(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:37] + "..."
}
