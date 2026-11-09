package store

import (
	"encoding/json"
	"errors"
	"testing"
)

// The panel's domain is one row: a second move replaces the first, a state
// moves on with its problem, and forgetting it leaves nothing.
func TestThePanelHasOneDomain(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()

	if _, err := db.GetPanelDomain(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a panel never moved has a domain: %v", err)
	}
	if err := db.SetPanelDomainState(ctx, PanelDomainLive, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a state was recorded for a move that never started: %v", err)
	}

	if err := db.StartPanelDomain(ctx, PanelDomain{
		Domain: "panel.example.test", Previous: []string{"203-0-113-10.sslip.io"},
		State: PanelDomainMoving, StartedBy: "ops@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	problem := json.RawMessage(`{"code":"panel.domain_certificate_pending"}`)
	if err := db.SetPanelDomainState(ctx, PanelDomainFailed, problem); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetPanelDomain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "panel.example.test" || got.State != PanelDomainFailed || string(got.Problem) != string(problem) ||
		len(got.Previous) != 1 || got.Previous[0] != "203-0-113-10.sslip.io" || got.StartedBy != "ops@example.test" ||
		got.StartedAt.IsZero() {
		t.Errorf("read back %+v", got)
	}

	if err := db.StartPanelDomain(ctx, PanelDomain{Domain: "other.example.test", State: PanelDomainMoving}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetPanelDomain(ctx)
	if got.Domain != "other.example.test" || got.Problem != nil || got.Previous == nil || len(got.Previous) != 0 {
		t.Errorf("a second move left %+v", got)
	}

	if err := db.DeletePanelDomain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPanelDomain(ctx); !errors.Is(err, ErrNotFound) {
		t.Errorf("a forgotten domain is still there: %v", err)
	}
}
