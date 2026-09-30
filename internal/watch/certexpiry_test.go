package watch

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"skifity/internal/notify"
	"skifity/internal/store"
)

// certificateStore is the expiry check's database: the certificates, and the
// warnings recorded on them.
type certificateStore struct {
	certificates []store.Certificate
}

func (c *certificateStore) ListAllCertificates(context.Context) ([]store.Certificate, error) {
	return append([]store.Certificate(nil), c.certificates...), nil
}

func (c *certificateStore) SetCertificateExpiryNotified(_ context.Context, id string, notAfter time.Time, days int) error {
	for i := range c.certificates {
		if c.certificates[i].ID == id && c.certificates[i].NotAfter.Equal(notAfter) {
			c.certificates[i].ExpiryNotified = days
		}
	}
	return nil
}

// Each threshold once, whatever the number of checks between them: the check
// runs every hour, and a team hears three times — at 21, 7 and 1 days — not
// every hour for three weeks.
func TestACertificateIsWarnedAboutOncePerThreshold(t *testing.T) {
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	db := &certificateStore{certificates: []store.Certificate{
		{ID: "crt_1", TeamID: "team_1", Name: "Company wildcard", Hostnames: []string{"*.example.com"},
			Issuer: "Example CA", NotAfter: start.Add(30 * 24 * time.Hour)},
		{ID: "crt_2", TeamID: "team_2", Name: "Far off", Hostnames: []string{"far.example.org"},
			NotAfter: start.Add(365 * 24 * time.Hour)},
	}}
	notifier := &fakeNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Every hour for 31 days, past the date.
	for now := start; now.Before(start.Add(31 * 24 * time.Hour)); now = now.Add(time.Hour) {
		CheckCertificateExpiry(t.Context(), db, notifier, now, log)
	}

	if got := notifier.eventsOf(notify.EventCertificateExpiring); got != 3 {
		for _, sent := range notifier.sent {
			t.Logf("%s", sent.msg.Title)
		}
		t.Fatalf("sent %d warnings, want 3: at 21, 7 and 1 days", got)
	}
	for i, want := range []string{"expires in 21 days", "expires in 7 days", "expires within a day"} {
		sent := notifier.sent[i]
		if sent.teamID != "team_1" || !strings.Contains(sent.msg.Title, "Company wildcard") ||
			!strings.Contains(sent.msg.Title, want) {
			t.Errorf("warning %d is %q to %s, want %q", i+1, sent.msg.Title, sent.teamID, want)
		}
		// About the whole team: a certificate covers hostnames in any of its
		// projects, so no channel limited to projects is left out.
		if sent.msg.ProjectID != "" {
			t.Errorf("warning %d is marked with the project %q", i+1, sent.msg.ProjectID)
		}
		if sent.msg.Fields["Hostnames"] != "*.example.com" || sent.msg.Path == "" {
			t.Errorf("warning %d does not say what it covers or where to go: %+v", i+1, sent.msg)
		}
	}
	if notifier.sent[0].msg.Level != "warning" || notifier.sent[2].msg.Level != "error" {
		t.Errorf("levels are %s and %s", notifier.sent[0].msg.Level, notifier.sent[2].msg.Level)
	}
	if db.certificates[0].ExpiryNotified != 1 || db.certificates[1].ExpiryNotified != 0 {
		t.Errorf("recorded %d and %d", db.certificates[0].ExpiryNotified, db.certificates[1].ExpiryNotified)
	}

	// The renewal, uploaded under the same name, has warnings of its own to
	// come and none now.
	db.certificates[0].NotAfter = start.Add(400 * 24 * time.Hour)
	db.certificates[0].ExpiryNotified = 0
	CheckCertificateExpiry(t.Context(), db, notifier, start.Add(31*24*time.Hour), log)
	if got := notifier.eventsOf(notify.EventCertificateExpiring); got != 3 {
		t.Errorf("the renewal was warned about: %d warnings", got)
	}
}

func TestAnExpiredCertificateNeverWarnedIsWarnedOnce(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	db := &certificateStore{certificates: []store.Certificate{
		{ID: "crt_1", TeamID: "team_1", Name: "Forgotten", NotAfter: now.Add(-48 * time.Hour)},
	}}
	notifier := &fakeNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	CheckCertificateExpiry(t.Context(), db, notifier, now, log)
	CheckCertificateExpiry(t.Context(), db, notifier, now.Add(time.Hour), log)
	if len(notifier.sent) != 1 || !strings.Contains(notifier.sent[0].msg.Title, "has expired") {
		t.Fatalf("sent %+v", notifier.sent)
	}
}
