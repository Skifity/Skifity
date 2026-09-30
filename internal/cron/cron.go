// Package cron parses the five-field schedules the panel accepts.
package cron

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A small cron parser.
//
// Two things need one — backup schedules and an app's scheduled commands — and
// the subset used is the five-field form with numbers, ranges, lists and
// steps. Pulling in a scheduling library for that would be more code to audit
// than this file, and a wrong schedule here means a backup or a nightly job
// that silently does not run, which is exactly the kind of thing worth being
// able to read end to end.

// Schedule is a parsed cron expression.
type Schedule struct {
	Minutes     []int
	Hours       []int
	DaysOfWeek  []int
	DaysOfMonth []int
	Months      []int
	// dayOfMonthRestricted and dayOfWeekRestricted record whether each field
	// was "*", because cron matches either when both are set.
	dayOfMonthRestricted bool
	dayOfWeekRestricted  bool
}

// ParseSchedule reads a five-field cron expression.
func ParseSchedule(expression string) (Schedule, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("a schedule needs five fields (minute hour day month weekday), got %d", len(fields))
	}

	minutes, err := parseField(fields[0], 0, 59)
	if err != nil {
		return Schedule{}, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return Schedule{}, fmt.Errorf("hour: %w", err)
	}
	daysOfMonth, err := parseField(fields[2], 1, 31)
	if err != nil {
		return Schedule{}, fmt.Errorf("day of month: %w", err)
	}
	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return Schedule{}, fmt.Errorf("month: %w", err)
	}
	daysOfWeek, err := parseField(fields[4], 0, 7)
	if err != nil {
		return Schedule{}, fmt.Errorf("day of week: %w", err)
	}
	// Cron accepts both 0 and 7 for Sunday.
	for i, d := range daysOfWeek {
		if d == 7 {
			daysOfWeek[i] = 0
		}
	}

	return Schedule{
		Minutes: minutes, Hours: hours,
		DaysOfMonth: daysOfMonth, Months: months, DaysOfWeek: daysOfWeek,
		dayOfMonthRestricted: fields[2] != "*",
		dayOfWeekRestricted:  fields[4] != "*",
	}, nil
}

// Matches reports whether a time falls on the schedule, to the minute.
func (s Schedule) Matches(t time.Time) bool {
	if !contains(s.Minutes, t.Minute()) || !contains(s.Hours, t.Hour()) ||
		!contains(s.Months, int(t.Month())) {
		return false
	}

	dayOfMonth := contains(s.DaysOfMonth, t.Day())
	dayOfWeek := contains(s.DaysOfWeek, int(t.Weekday()))

	switch {
	case s.dayOfMonthRestricted && s.dayOfWeekRestricted:
		// Cron's one genuine oddity: with both fields restricted, either
		// matching is enough.
		return dayOfMonth || dayOfWeek
	case s.dayOfMonthRestricted:
		return dayOfMonth
	case s.dayOfWeekRestricted:
		return dayOfWeek
	default:
		return true
	}
}

// parseField reads one cron field.
func parseField(field string, low, high int) ([]int, error) {
	if field == "*" {
		return rangeOf(low, high, 1), nil
	}

	var out []int
	for _, part := range strings.Split(field, ",") {
		step := 1
		if base, stepText, hasStep := strings.Cut(part, "/"); hasStep {
			parsed, err := strconv.Atoi(stepText)
			if err != nil || parsed < 1 {
				return nil, fmt.Errorf("%q is not a valid step", stepText)
			}
			step = parsed
			part = base
		}

		switch {
		case part == "*":
			out = append(out, rangeOf(low, high, step)...)
		case strings.Contains(part, "-"):
			startText, endText, _ := strings.Cut(part, "-")
			start, err := strconv.Atoi(startText)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", startText)
			}
			end, err := strconv.Atoi(endText)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", endText)
			}
			if start < low || end > high || start > end {
				return nil, fmt.Errorf("the range %s is outside %d-%d", part, low, high)
			}
			out = append(out, rangeOf(start, end, step)...)
		default:
			value, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", part)
			}
			if value < low || value > high {
				return nil, fmt.Errorf("%d is outside %d-%d", value, low, high)
			}
			// "5/15" is cron for "from 5 to the end of the field, every 15",
			// and this used to parse it as plain 5 and drop the step on the
			// floor. A schedule that runs a quarter as often as it was written
			// is the failure this package exists to avoid, and it is silent.
			if step > 1 {
				out = append(out, rangeOf(value, high, step)...)
				continue
			}
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no values")
	}
	return out, nil
}

func rangeOf(low, high, step int) []int {
	var out []int
	for v := low; v <= high; v += step {
		out = append(out, v)
	}
	return out
}

func contains(values []int, v int) bool {
	for _, item := range values {
		if item == v {
			return true
		}
	}
	return false
}

// Canonical returns the expression as this parser understood it.
//
// # Why this has to exist
//
// The panel validates a schedule here and then hands the raw string to
// Kubernetes, which parses it with a different implementation. Those two are
// allowed to disagree, and on one point they reliably do: cron's day-of-week
// field has two spellings for Sunday, 0 and 7, and this parser accepts both
// while the cluster's need not. A schedule written "0 3 * * 7" — a completely
// ordinary way to say Sunday — passed validation, was stored, was shown on the
// page with a next-run time this package worked out, and was then refused by
// the API server.
//
// So the string that goes to the cluster is the one this parser agrees with.
// What was validated is what runs, which is the only version of that sentence
// worth anything.
//
// Only the day-of-week field is rewritten, and only when it is not "*". Two
// reasons for the exception: "*" carries meaning beyond its values — cron
// matches day-of-month or day-of-week when both are restricted — so expanding
// it would change what the schedule means; and a field rewritten into a list
// for no reason is a schedule somebody no longer recognises as the one they
// typed.
func Canonical(expression string) (string, error) {
	schedule, err := ParseSchedule(expression)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(expression)
	if fields[4] == "*" {
		return strings.Join(fields, " "), nil
	}
	fields[4] = joinValues(schedule.DaysOfWeek)
	return strings.Join(fields, " "), nil
}

// joinValues renders a field's values, sorted and without repeats.
//
// Sorted because "0-7" parses to a set ending in a second Sunday and a reader
// should not have to know that; deduplicated for the same reason.
func joinValues(values []int) string {
	seen := map[int]bool{}
	sorted := make([]int, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		sorted = append(sorted, v)
	}
	slices.Sort(sorted)

	parts := make([]string, 0, len(sorted))
	for _, v := range sorted {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, ",")
}

// DueNow reports whether a schedule fires in the minute now falls in.
//
// The scheduler runs once a minute and compares against the minute, so a backup
// runs exactly once even if the tick is a few seconds late.
func DueNow(expression string, now time.Time) bool {
	schedule, err := ParseSchedule(expression)
	if err != nil {
		return false
	}
	return schedule.Matches(now)
}

// NextRun returns the next time a schedule fires after a given time.
func NextRun(expression string, after time.Time) (time.Time, error) {
	schedule, err := ParseSchedule(expression)
	if err != nil {
		return time.Time{}, err
	}
	// Scanning minute by minute is simple and fast enough: this runs when a
	// user opens a settings page, not in a hot loop.
	//
	// Five years and not one. "0 0 29 2 *" is a valid schedule that fires only
	// in a leap year, so a one-year horizon answered "this schedule never
	// fires" for a schedule that fires — and the panel showed that sentence to
	// somebody whose backup was in fact scheduled. Five covers the longest gap
	// the five-field form can express.
	candidate := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(5, 0, 0)
	for candidate.Before(limit) {
		if schedule.Matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("this schedule never fires")
}
