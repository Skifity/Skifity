package serverapp

import (
	"testing"
	"time"
)

// A tick that arrives late has to cover the minutes it stepped over, or a
// schedule that fell in one of them never fires and nothing says so.
func TestALateTickCatchesUpTheMinutesItMissed(t *testing.T) {
	last, _ := time.Parse(time.RFC3339, "2026-09-16T03:00:00Z")
	now := last.Add(3 * time.Minute)

	minutes := minutesAfter(last, now)
	if len(minutes) != 3 {
		t.Fatalf("minutesAfter covered %d minutes, want 3: %v", len(minutes), minutes)
	}
	for i, want := range []string{"03:03", "03:02", "03:01"} {
		if got := minutes[i].Format("15:04"); got != want {
			t.Errorf("minute %d = %s, want %s", i, got, want)
		}
	}
}

// A panel that was off for a week must not fire a week of backups the moment it
// comes back.
func TestCatchingUpIsBounded(t *testing.T) {
	last, _ := time.Parse(time.RFC3339, "2026-09-16T03:00:00Z")
	minutes := minutesAfter(last, last.Add(7*24*time.Hour))
	if len(minutes) != catchUpLimit {
		t.Fatalf("minutesAfter covered %d minutes, want the %d it is capped at", len(minutes), catchUpLimit)
	}
}

// A clock that went backwards is the one case where "every minute since" never
// ends.
func TestAClockThatWentBackwardsYieldsOneMinute(t *testing.T) {
	last, _ := time.Parse(time.RFC3339, "2026-09-16T03:00:00Z")
	if minutes := minutesAfter(last, last.Add(-time.Hour)); len(minutes) != 1 {
		t.Fatalf("minutesAfter covered %d minutes, want 1", len(minutes))
	}
	if minutes := minutesAfter(last, last); len(minutes) != 1 {
		t.Fatalf("the same minute covered %d minutes, want 1", len(minutes))
	}
}
