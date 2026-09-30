package cron

import (
	"testing"
	"time"
)

func TestParseScheduleAndMatch(t *testing.T) {
	cases := []struct {
		expression string
		at         string
		want       bool
	}{
		{"0 3 * * *", "2026-09-16T03:00:00Z", true},
		{"0 3 * * *", "2026-09-16T03:01:00Z", false},
		{"0 3 * * *", "2026-09-16T04:00:00Z", false},
		{"*/15 * * * *", "2026-09-16T10:30:00Z", true},
		{"*/15 * * * *", "2026-09-16T10:31:00Z", false},
		{"30 2 * * 0", "2026-09-20T02:30:00Z", true},  // a Sunday
		{"30 2 * * 0", "2026-09-21T02:30:00Z", false}, // a Monday
		{"0 0 1 * *", "2026-10-01T00:00:00Z", true},
		{"0 0 1 * *", "2026-10-02T00:00:00Z", false},
		{"0 9-17 * * 1-5", "2026-09-16T13:00:00Z", true}, // a Wednesday
		{"0 9-17 * * 1-5", "2026-09-19T13:00:00Z", false},
		{"0 9-17 * * 1-5", "2026-09-16T18:00:00Z", false},
		// Sunday is both 0 and 7.
		{"0 1 * * 7", "2026-09-20T01:00:00Z", true},
	}
	for _, tc := range cases {
		schedule, err := ParseSchedule(tc.expression)
		if err != nil {
			t.Fatalf("ParseSchedule(%q): %v", tc.expression, err)
		}
		at, err := time.Parse(time.RFC3339, tc.at)
		if err != nil {
			t.Fatalf("parse time: %v", err)
		}
		if got := schedule.Matches(at); got != tc.want {
			t.Errorf("%q at %s matched=%v, want %v", tc.expression, tc.at, got, tc.want)
		}
	}
}

func TestDayOfMonthAndWeekdayAreOred(t *testing.T) {
	// Cron's one genuine oddity: with both day fields restricted, either
	// matching is enough. Getting this wrong means a backup runs far more or
	// far less often than the user asked for.
	schedule, err := ParseSchedule("0 0 1 * 0")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	firstOfMonth, _ := time.Parse(time.RFC3339, "2026-10-01T00:00:00Z") // a Thursday
	sunday, _ := time.Parse(time.RFC3339, "2026-10-04T00:00:00Z")
	neither, _ := time.Parse(time.RFC3339, "2026-10-06T00:00:00Z")

	if !schedule.Matches(firstOfMonth) {
		t.Error("the first of the month did not match")
	}
	if !schedule.Matches(sunday) {
		t.Error("Sunday did not match")
	}
	if schedule.Matches(neither) {
		t.Error("a day that is neither matched")
	}
}

func TestParseScheduleRejectsNonsense(t *testing.T) {
	for _, bad := range []string{
		"", "0 3 * *", "0 3 * * * *", "60 3 * * *", "0 25 * * *",
		"0 3 32 * *", "0 3 * 13 *", "0 3 * * 8", "x 3 * * *", "0 3 5-1 * *", "*/0 * * * *",
	} {
		if _, err := ParseSchedule(bad); err == nil {
			t.Errorf("ParseSchedule(%q) was accepted", bad)
		}
	}
}

// A step after a plain number means "from here to the end of the field", and
// dropping it is silent: the schedule parses, it just fires a fraction as often
// as it was written.
func TestAStepAfterANumberIsARange(t *testing.T) {
	schedule, err := ParseSchedule("0 5/6 * * *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	want := []int{5, 11, 17, 23}
	if len(schedule.Hours) != len(want) {
		t.Fatalf("hours = %v, want %v", schedule.Hours, want)
	}
	for i, hour := range want {
		if schedule.Hours[i] != hour {
			t.Fatalf("hours = %v, want %v", schedule.Hours, want)
		}
	}

	for _, at := range []string{"2026-09-16T11:00:00Z", "2026-09-16T23:00:00Z"} {
		moment, _ := time.Parse(time.RFC3339, at)
		if !schedule.Matches(moment) {
			t.Errorf("%s should be on the schedule 0 5/6 * * *", at)
		}
	}
	noon, _ := time.Parse(time.RFC3339, "2026-09-16T12:00:00Z")
	if schedule.Matches(noon) {
		t.Error("12:00 is not on the schedule 0 5/6 * * *")
	}
}

func TestNextRun(t *testing.T) {
	after, _ := time.Parse(time.RFC3339, "2026-09-16T10:15:00Z")
	next, err := NextRun("0 3 * * *", after)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	want, _ := time.Parse(time.RFC3339, "2026-09-17T03:00:00Z")
	if !next.Equal(want) {
		t.Fatalf("NextRun = %s, want %s", next, want)
	}
}

// What the panel validated has to be what the cluster runs.
//
// Kubernetes parses the schedule string itself, with a different
// implementation, so any spelling this parser accepts and that one does not is
// a scheduled command stored, shown with a next-run time, and refused by the
// API server.
func TestCanonicalRewritesTheSundayThisParserAcceptsAndOthersNeedNot(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0 3 * * 7", "0 3 * * 0"},
		{"0 3 * * 0", "0 3 * * 0"},
		{"0 3 * * 1-7", "0 3 * * 0,1,2,3,4,5,6"},
		{"0 3 * * 6,7", "0 3 * * 0,6"},
		{"0 3 * * 0-7/2", "0 3 * * 0,2,4,6"},
		// "*" carries meaning beyond its values, so it is never expanded.
		{"0 3 * * *", "0 3 * * *"},
		{"*/15 * * * *", "*/15 * * * *"},
		// Extra spacing is normalised away, because the string is compared and
		// stored rather than only parsed.
		{"0   3 * *   7", "0 3 * * 0"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Canonical(tc.in)
			if err != nil {
				t.Fatalf("Canonical(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Canonical(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Rewriting a schedule is only safe if it still means the same thing. This is
// the property that makes Canonical worth having rather than dangerous.
func TestCanonicalNeverChangesWhenAScheduleFires(t *testing.T) {
	expressions := []string{
		"0 3 * * 7", "0 3 * * 0", "0 3 * * 1-7", "0 3 * * 6,7", "0 3 * * 0-7/2",
		"0 3 * * *", "*/15 * * * *", "0 0 1 * *", "30 2 * * 1-5",
		"0 0 1 1 7", "5/15 * * * 7", "0 0 29 2 *", "0 12 13 * 5",
		// Both fields restricted: cron matches either, and that is exactly the
		// rule a careless rewrite would break.
		"0 4 1 * 7", "0 4 15 * 0",
	}
	for _, expression := range expressions {
		t.Run(expression, func(t *testing.T) {
			original, err := ParseSchedule(expression)
			if err != nil {
				t.Fatalf("parse %q: %v", expression, err)
			}
			canonical, err := Canonical(expression)
			if err != nil {
				t.Fatalf("Canonical(%q): %v", expression, err)
			}
			rewritten, err := ParseSchedule(canonical)
			if err != nil {
				t.Fatalf("the canonical form %q does not parse: %v", canonical, err)
			}

			// Every 13 minutes across four years, which crosses a leap day and
			// every weekday-of-month combination.
			start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(4, 0, 0)
			for at := start; at.Before(end); at = at.Add(13 * time.Minute) {
				if original.Matches(at) != rewritten.Matches(at) {
					t.Fatalf("%q and its canonical form %q disagree at %s: %v and %v",
						expression, canonical, at.Format(time.RFC3339),
						original.Matches(at), rewritten.Matches(at))
				}
			}
		})
	}
}

func TestCanonicalRefusesWhatTheParserRefuses(t *testing.T) {
	for _, bad := range []string{"", "0 3 * *", "0 3 * * 8", "@daily", "0 3 * * MON"} {
		if _, err := Canonical(bad); err == nil {
			t.Errorf("Canonical(%q) accepted it", bad)
		}
	}
}

// A schedule that fires only in a leap year fires. Saying "this schedule never
// fires" to somebody whose backup is in fact scheduled is worse than saying
// nothing.
func TestNextRunFindsTheTwentyNinthOfFebruary(t *testing.T) {
	after := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	next, err := NextRun("0 0 29 2 *", after)
	if err != nil {
		t.Fatalf("NextRun for a leap-day schedule: %v", err)
	}
	want := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next run is %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// A schedule that genuinely never fires still says so, rather than the horizon
// being widened until every mistake looks like a valid schedule.
func TestAScheduleThatCannotFireStillSaysSo(t *testing.T) {
	after := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NextRun("0 0 30 2 *", after); err == nil {
		t.Error("the 30th of February was reported as a time that comes round")
	}
}
