package store

import (
	"errors"
	"sync"
	"testing"
)

// The panel always keeps one enabled administrator. Demoting, disabling and
// deleting each check it in the statement that makes the change, and a
// disabled administrator does not count as one.
func TestTheLastAdministratorIsKept(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	person := func(email string, admin, disabled bool) User {
		t.Helper()
		u := User{Email: email, PasswordHash: "x", IsAdmin: admin, Disabled: disabled}
		if err := db.CreateUser(ctx, &u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	first := person("first@example.test", true, false)
	second := person("second@example.test", true, false)
	person("gone@example.test", true, true)
	member := person("member@example.test", false, false)

	if err := db.SetUserAdmin(ctx, second.ID, false); err != nil {
		t.Fatalf("demoting one of two administrators: %v", err)
	}
	if n, _ := db.CountEnabledAdmins(ctx); n != 1 {
		t.Fatalf("%d enabled administrators, want 1", n)
	}
	for name, change := range map[string]func() error{
		"demoting":  func() error { return db.SetUserAdmin(ctx, first.ID, false) },
		"disabling": func() error { return db.SetUserDisabled(ctx, first.ID, true) },
		"deleting":  func() error { return db.DeleteAccount(ctx, first.ID) },
	} {
		if err := change(); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("%s the last enabled administrator answered %v", name, err)
		}
	}
	if user, _ := db.GetUser(ctx, first.ID); !user.IsAdmin || user.Disabled {
		t.Fatalf("the last administrator was changed anyway: %+v", user)
	}

	// Anybody else can still be disabled, and an unknown id is not a
	// last-administrator refusal.
	if err := db.SetUserDisabled(ctx, member.ID, true); err != nil {
		t.Errorf("disabling a member: %v", err)
	}
	if err := db.SetUserDisabled(ctx, "usr_missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabling nobody answered %v", err)
	}

	// Two administrators demoting each other at once: one wins.
	if err := db.SetUserAdmin(ctx, second.ID, true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = db.SetUserAdmin(ctx, id, false)
		}()
	}
	wg.Wait()
	if n, _ := db.CountEnabledAdmins(ctx); n != 1 {
		t.Errorf("after both demoted each other there are %d administrators (%v)", n, errs)
	}
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Errorf("exactly one demotion should win: %v", errs)
	}
}

// An account is not deleted while a team has no other owner or a plugin runs
// with its token; once neither is true it goes, with what hangs off it.
func TestDeletingAnAccountWaitsForWhatDependsOnIt(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	owner, team, _, _ := seedTeam(t, db)
	other := User{Email: "other@example.test", PasswordHash: "x", IsAdmin: true}
	if err := db.CreateUser(ctx, &other); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkIdentity(ctx, owner.ID, "https://id.example.test", "sub-1", owner.Email); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAPIToken(ctx, &APIToken{UserID: owner.ID, TeamID: team.ID, Name: "ci", TokenHash: "h1", Prefix: "skf_"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreatePlugin(ctx, &Plugin{ID: "com.example.chat", Manifest: "x", Version: "1.0.0", InstalledBy: owner.ID}); err != nil {
		t.Fatal(err)
	}

	accounts, err := db.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mine Account
	for _, account := range accounts {
		if account.User.ID == owner.ID {
			mine = account
		}
	}
	if len(mine.Teams) != 1 || mine.Teams[0].Role != RoleOwner || mine.Tokens != 1 ||
		len(mine.SSOIssuers) != 1 || len(mine.Blockers.SoleOwnerOf) != 1 || len(mine.Blockers.Plugins) != 1 {
		t.Fatalf("the account is listed as %+v", mine)
	}

	err = db.DeleteAccount(ctx, owner.ID)
	blockers, blocked := IsAccountBlocked(err)
	if !blocked || len(blockers.SoleOwnerOf) != 1 || blockers.SoleOwnerOf[0].Name != team.Name ||
		len(blockers.Plugins) != 1 || blockers.Plugins[0] != "com.example.chat" {
		t.Fatalf("deleting the only owner and a plugin's installer answered %v", err)
	}

	if err := db.AddMember(ctx, team.ID, other.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := db.DeletePlugin(ctx, "com.example.chat"); err != nil {
		t.Fatal(err)
	}
	if blockers, err := db.AccountBlockersFor(ctx, owner.ID); err != nil || blockers.Any() {
		t.Fatalf("with another owner and no plugin, the blockers are %+v (%v)", blockers, err)
	}
	if err := db.DeleteAccount(ctx, owner.ID); err != nil {
		t.Fatalf("deleting an account nothing depends on: %v", err)
	}
	if _, err := db.GetUser(ctx, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the account is still there: %v", err)
	}
	if tokens, _ := db.ListAPITokens(ctx, owner.ID); len(tokens) != 0 {
		t.Errorf("%d of its tokens are still there", len(tokens))
	}
	if err := db.DeleteAccount(ctx, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it twice answered %v", err)
	}
}

// An account changing its own name, password or second factor writes back the
// row it read at the start of the request. Disabling or demoting it in the
// meantime has to survive that, so UpdateUser leaves both columns alone.
func TestAnAccountsOwnChangesDoNotUndoAnAdministrators(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	keeper := User{Email: "keeper@example.test", PasswordHash: "x", IsAdmin: true}
	user := User{Email: "user@example.test", PasswordHash: "x", IsAdmin: true}
	for _, u := range []*User{&keeper, &user} {
		if err := db.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserDisabled(ctx, user.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserAdmin(ctx, user.ID, false); err != nil {
		t.Fatal(err)
	}

	stale.Name = "Renamed"
	if err := db.UpdateUser(ctx, &stale); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Disabled || after.IsAdmin || after.Name != "Renamed" {
		t.Errorf("after the account's own change it is %+v", after)
	}
}
