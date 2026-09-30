package api

import (
	"context"
	"net/http"
	"testing"

	"skifity/internal/store"
)

// A safety check that is skipped when the query it depends on fails is not a
// safety check. Four of them were written as `if err == nil && <the dangerous
// condition>`, which reads as caution and means the opposite: the moment the
// panel's own database is unwell, the guard silently steps aside and the
// destructive path runs.
//
// These make the query fail for real — the table it reads is dropped — and then
// ask for the thing the guard exists to stop.

// breakTable removes a table so the next query against it returns an error
// rather than an empty result. This is the difference the tests below turn on:
// "nothing came back" and "nothing could be asked" are not the same answer.
func (h *harness) breakTable(name string) {
	h.t.Helper()
	if _, err := h.db.ExecContext(h.t.Context(), "DROP TABLE "+name); err != nil {
		h.t.Fatalf("drop %s: %v", name, err)
	}
}

func TestADatabaseIsNotDeletedWhenItsLinksCannotBeRead(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	record := store.Database{
		EnvironmentID: acme.env.ID, Name: "shop-db", Slug: "shop-db",
		Engine: "postgres", Status: "running",
	}
	if err := h.db.CreateDatabase(t.Context(), &record); err != nil {
		t.Fatalf("create database: %v", err)
	}

	h.breakTable("database_links")

	status, body := h.do(acme, http.MethodDelete, "/api/databases/"+record.ID, nil)
	if status == http.StatusOK {
		t.Fatal("a database was deleted without the check for apps still using it ever running")
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("got %d %s, want the failure to be reported", status, body)
	}
}

func TestTheLastOwnerIsNotDemotedWhenTheOwnersCannotBeCounted(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	// A second account, so the request itself is well formed.
	other := store.User{Email: "second@example.test", Name: "second", PasswordHash: "x"}
	if err := h.db.CreateUser(t.Context(), &other); err != nil {
		t.Fatalf("create user: %v", err)
	}

	h.breakTable("memberships")

	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/members",
		map[string]string{"email": acme.user.Email, "role": string(store.RoleMember)})
	if status == http.StatusOK {
		t.Fatal("a role was changed without the last-owner check ever running")
	}
	_ = body
}

// Only an owner may remove another owner. An admin manages servers and
// settings; the people who can remove them are not theirs to remove.
func TestAnAdminCannotRemoveAnOwner(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")

	// An admin of the same team, with their own token, and a second owner so
	// the request is refused for the reason under test rather than for leaving
	// the team without one.
	admin := h.newMember(acme, "admin", store.RoleAdmin)
	secondOwner := h.newMember(acme, "owner2", store.RoleOwner)

	status, body := h.do(admin, http.MethodDelete,
		"/api/teams/"+acme.team.ID+"/members/"+secondOwner.user.ID, nil)
	if status != http.StatusForbidden {
		t.Fatalf("an admin removed an owner: %d %s", status, body)
	}

}

// The guard above reads the actor's own role a second time, and used to allow
// the removal when that read failed. Nothing can reach that branch — a caller
// with no readable membership is already refused by authorizeTeam — so there is
// no test here, only the change: a failed read is now "not an owner" rather
// than "carry on". A permission check that passes because a query failed is the
// wrong way round even where nothing can currently make the query fail.

// TestDeletingADatabaseIsAuditedAgainstItsTeam: which team a database belongs
// to is resolved through the database's own row. Asking after the row is gone
// returns nothing, so the deletion was recorded with no team at all — filed
// with the panel-wide events, which belong to no team by design, and therefore
// shown in the audit log of every team that person happens to be in.
func TestDeletingADatabaseIsAuditedAgainstItsTeam(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	h.withDatabases(deletingDatabases{db: h.db})

	record := store.Database{
		EnvironmentID: acme.env.ID, Name: "shop-db", Slug: "shop-db",
		Engine: "postgres", Status: "running",
	}
	if err := h.db.CreateDatabase(t.Context(), &record); err != nil {
		t.Fatalf("create database: %v", err)
	}

	status, body := h.do(acme, http.MethodDelete, "/api/databases/"+record.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("delete: %d %s", status, body)
	}

	events, err := h.db.ListAudit(t.Context(), acme.team.ID, "", "", 50)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	for _, event := range events {
		if event.Action != "database.deleted" || event.TargetID != record.ID {
			continue
		}
		if event.TeamID != acme.team.ID {
			t.Fatalf("the deletion was recorded against team %q, not %q", event.TeamID, acme.team.ID)
		}
		return
	}
	t.Fatalf("the team's audit log does not record the deletion: %+v", events)
}

// deletingDatabases is the smallest DatabaseManager that actually removes the
// row, which is what makes the ordering above matter.
type deletingDatabases struct {
	DatabaseManager
	db *store.DB
}

func (d deletingDatabases) Delete(ctx context.Context, databaseID string) error {
	return d.db.DeleteDatabase(ctx, databaseID)
}
