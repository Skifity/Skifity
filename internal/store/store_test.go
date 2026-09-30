package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	// A file-backed database in a temp dir, because ":memory:" with one
	// connection hides the locking behaviour we actually ship with.
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	v1, err := db.SchemaVersion(t.Context())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v1 < 1 {
		t.Fatalf("schema version is %d, expected at least 1", v1)
	}
	db.Close()

	// Reopening must not try to re-apply migrations.
	db2, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()
	v2, _ := db2.SchemaVersion(t.Context())
	if v2 != v1 {
		t.Fatalf("schema version changed on reopen: %d then %d", v1, v2)
	}
}

func TestNewIDIsSortableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	var previous string
	for range 200 {
		id := NewID("app")
		if !strings.HasPrefix(id, "app_") {
			t.Fatalf("id %q lost its prefix", id)
		}
		if seen[id] {
			t.Fatalf("NewID returned %q twice", id)
		}
		seen[id] = true
		if previous != "" && id < previous {
			// Ids generated in the same millisecond may tie, but must never
			// go backwards across milliseconds.
			time.Sleep(2 * time.Millisecond)
			if next := NewID("app"); next < id {
				t.Fatalf("ids are not time-ordered: %q then %q", id, next)
			}
		}
		previous = id
	}
}

// seedTeam creates the user/team/project/environment chain most tests need.
func seedTeam(t *testing.T, db *DB) (User, Team, Project, Environment) {
	t.Helper()
	ctx := t.Context()
	u := User{Email: "owner@example.test", Name: "Owner", PasswordHash: "x"}
	if err := db.CreateUser(ctx, &u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if err := db.AddMember(ctx, team.ID, u.ID, RoleOwner); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	prj := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &prj); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	env := Environment{ProjectID: prj.ID, Name: "Production", Slug: "production", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	return u, team, prj, env
}

func TestUniqueConstraintsBecomeConflicts(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, _ := seedTeam(t, db)

	dup := User{Email: "OWNER@example.test", PasswordHash: "y"}
	err := db.CreateUser(ctx, &dup)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email gave %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("conflict error should be readable, got: %v", err)
	}

	// Emails must match case-insensitively, or two people can register the
	// same address with different capitalisation.
	if _, err := db.GetUserByEmail(ctx, "Owner@Example.Test"); err != nil {
		t.Fatalf("GetUserByEmail is case sensitive: %v", err)
	}

	s := Server{TeamID: team.ID, Name: "node-1", Host: "203.0.113.10", SSHPort: 22}
	if err := db.CreateServer(ctx, &s); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	again := Server{TeamID: team.ID, Name: "node-1-again", Host: "203.0.113.10", SSHPort: 22}
	if err := db.CreateServer(ctx, &again); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate server gave %v, want ErrConflict", err)
	}
}

func TestCascadeDeleteRemovesChildren(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, _, prj, env := seedTeam(t, db)

	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	v := Variable{AppID: app.ID, Key: "PORT"}
	if err := db.SetVariable(ctx, &v, "SKF1.sealed"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	d := Deployment{AppID: app.ID}
	if err := db.CreateDeployment(ctx, &d); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Deleting the project must take the environment, app, variables and
	// deployments with it. Without foreign_keys ON, these rows would be orphaned.
	if err := db.DeleteProject(ctx, prj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := db.GetApp(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app survived the project deletion: %v", err)
	}
	if _, err := db.GetDeployment(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deployment survived the project deletion: %v", err)
	}
	vars, err := db.ListVariables(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListVariables: %v", err)
	}
	if len(vars) != 0 {
		t.Fatalf("%d variables survived the project deletion", len(vars))
	}
}

func TestDeploymentNumbersIncrementPerApp(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)

	appA := App{EnvironmentID: env.ID, Name: "a", Slug: "a"}
	appB := App{EnvironmentID: env.ID, Name: "b", Slug: "b"}
	if err := db.CreateApp(ctx, &appA); err != nil {
		t.Fatalf("CreateApp a: %v", err)
	}
	if err := db.CreateApp(ctx, &appB); err != nil {
		t.Fatalf("CreateApp b: %v", err)
	}

	for want := 1; want <= 3; want++ {
		d := Deployment{AppID: appA.ID}
		if err := db.CreateDeployment(ctx, &d); err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if d.Number != want {
			t.Fatalf("deployment number %d, want %d", d.Number, want)
		}
	}
	// A different app starts its own numbering at 1.
	other := Deployment{AppID: appB.ID}
	if err := db.CreateDeployment(ctx, &other); err != nil {
		t.Fatalf("CreateDeployment for second app: %v", err)
	}
	if other.Number != 1 {
		t.Fatalf("second app's first deployment is number %d, want 1", other.Number)
	}
}

func TestFindDeploymentByFingerprintOnlyMatchesSuccess(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web"}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	failed := Deployment{AppID: app.ID, BuildFingerprint: "fp-1", Image: "reg/img:1"}
	if err := db.CreateDeployment(ctx, &failed); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := db.UpdateDeploymentStatus(ctx, failed.ID, DeployFailed, "build_failed", "boom", ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	// A failed build must never let a later deploy skip building.
	if _, err := db.FindDeploymentByFingerprint(ctx, app.ID, "fp-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a failed build was reused: %v", err)
	}

	ok := Deployment{AppID: app.ID, BuildFingerprint: "fp-1", Image: "reg/img:2"}
	if err := db.CreateDeployment(ctx, &ok); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := db.SetDeploymentImage(ctx, ok.ID, "reg/img:2"); err != nil {
		t.Fatalf("SetDeploymentImage: %v", err)
	}
	if err := db.UpdateDeploymentStatus(ctx, ok.ID, DeploySucceeded, "", "", ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	found, err := db.FindDeploymentByFingerprint(ctx, app.ID, "fp-1")
	if err != nil {
		t.Fatalf("FindDeploymentByFingerprint: %v", err)
	}
	if found.Image != "reg/img:2" {
		t.Fatalf("reused image %q, want reg/img:2", found.Image)
	}
	// An unknown fingerprint must force a build rather than reuse anything.
	if _, err := db.FindDeploymentByFingerprint(ctx, app.ID, "fp-other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unrelated fingerprint matched: %v", err)
	}
}

func TestOperationStepsDriveProgress(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, _ := seedTeam(t, db)

	op := Operation{TeamID: team.ID, Kind: "server.add", TargetType: "server", TargetID: "srv_1"}
	steps := []string{"connect", "preflight", "install-key", "firewall", "join"}
	if err := db.CreateOperation(ctx, &op, steps); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}

	loaded, err := db.GetOperation(ctx, op.ID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if len(loaded.Steps) != len(steps) {
		t.Fatalf("got %d steps, want %d", len(loaded.Steps), len(steps))
	}
	for i, s := range loaded.Steps {
		if s.Key != steps[i] {
			t.Fatalf("step %d is %q, want %q: steps must stay in order", i, s.Key, steps[i])
		}
	}

	if err := db.SetStepStatus(ctx, op.ID, "connect", StepSucceeded, StepNote{Message: "Connected", Key: "connected"}, ""); err != nil {
		t.Fatalf("SetStepStatus: %v", err)
	}
	if err := db.SetStepStatus(ctx, op.ID, "preflight", StepFailed, StepNote{Message: "Port 6443 is blocked"}, "ufw allow 6443"); err != nil {
		t.Fatalf("SetStepStatus: %v", err)
	}

	// Retry resets the failed step and everything after it, but not the
	// steps that already succeeded.
	if err := db.ResetStepsFrom(ctx, op.ID, "preflight"); err != nil {
		t.Fatalf("ResetStepsFrom: %v", err)
	}
	after, err := db.ListOperationSteps(ctx, op.ID)
	if err != nil {
		t.Fatalf("ListOperationSteps: %v", err)
	}
	if after[0].Status != StepSucceeded {
		t.Fatalf("retry undid a completed step: %s", after[0].Status)
	}
	for _, s := range after[1:] {
		if s.Status != StepPending {
			t.Fatalf("step %q is %s after retry, want pending", s.Key, s.Status)
		}
	}
}

func TestBuildLogsAreSequencedAndResumable(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web"}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	d := Deployment{AppID: app.ID}
	if err := db.CreateDeployment(ctx, &d); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	batch := make([]LogLine, 0, 10)
	for i := range 10 {
		batch = append(batch, LogLine{Line: "line " + string(rune('0'+i))})
	}
	if err := db.AppendBuildLogs(ctx, d.ID, batch); err != nil {
		t.Fatalf("AppendBuildLogs: %v", err)
	}
	seq, err := db.AppendBuildLog(ctx, d.ID, "stderr", "warning")
	if err != nil {
		t.Fatalf("AppendBuildLog: %v", err)
	}
	if seq != 11 {
		t.Fatalf("single append continued at %d, want 11", seq)
	}

	// A reconnecting client asks for everything after the last line it saw.
	tail, err := db.ListBuildLogs(ctx, d.ID, 8, 0)
	if err != nil {
		t.Fatalf("ListBuildLogs: %v", err)
	}
	if len(tail) != 3 {
		t.Fatalf("resume from seq 8 returned %d lines, want 3", len(tail))
	}
	if tail[0].Seq != 9 {
		t.Fatalf("resume started at seq %d, want 9", tail[0].Seq)
	}
}

func TestRoleOrdering(t *testing.T) {
	if !RoleOwner.AtLeast(RoleAdmin) || !RoleAdmin.AtLeast(RoleMember) || !RoleMember.AtLeast(RoleMember) {
		t.Fatal("role ordering is wrong going down")
	}
	if RoleMember.AtLeast(RoleAdmin) || RoleAdmin.AtLeast(RoleOwner) {
		t.Fatal("a lower role satisfied a higher requirement")
	}
	if Role("nonsense").Valid() {
		t.Fatal("an unknown role reported itself valid")
	}
}

func TestTeamResolutionForAuthorization(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web"}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dbrec := Database{EnvironmentID: env.ID, Name: "main", Slug: "main", Engine: "postgres"}
	if err := db.CreateDatabase(ctx, &dbrec); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	for name, got := range map[string]func() (string, error){
		"environment": func() (string, error) { return db.TeamIDForEnvironment(ctx, env.ID) },
		"app":         func() (string, error) { return db.TeamIDForApp(ctx, app.ID) },
		"database":    func() (string, error) { return db.TeamIDForDatabase(ctx, dbrec.ID) },
	} {
		id, err := got()
		if err != nil {
			t.Fatalf("resolve team for %s: %v", name, err)
		}
		if id != team.ID {
			t.Fatalf("team for %s resolved to %q, want %q", name, id, team.ID)
		}
	}

	// Authorization must fail closed for something that does not exist.
	if _, err := db.TeamIDForApp(ctx, "app_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolving a missing app gave %v, want ErrNotFound", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	u, _, _, _ := seedTeam(t, db)

	live := Session{UserID: u.ID, TokenHash: "hash-live", ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.CreateSession(ctx, &live); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	dead := Session{UserID: u.ID, TokenHash: "hash-dead", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := db.CreateSession(ctx, &dead); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := db.GetSessionByHash(ctx, "hash-live"); err != nil {
		t.Fatalf("live session not found: %v", err)
	}
	if _, err := db.GetSessionByHash(ctx, "hash-dead"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session was returned: %v", err)
	}
	n, err := db.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d sessions, want 1", n)
	}
}

func TestFailedLoginCounting(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	since := time.Now().Add(-time.Minute)

	for range 3 {
		if err := db.RecordLoginAttempt(ctx, "user@example.test", "198.51.100.5", false); err != nil {
			t.Fatalf("RecordLoginAttempt: %v", err)
		}
	}
	// A different account from the same address still counts against the address.
	if err := db.RecordLoginAttempt(ctx, "other@example.test", "198.51.100.5", false); err != nil {
		t.Fatalf("RecordLoginAttempt: %v", err)
	}

	byID, byIP, err := db.CountFailedLogins(ctx, "USER@example.test", "198.51.100.5", since)
	if err != nil {
		t.Fatalf("CountFailedLogins: %v", err)
	}
	if byID != 3 {
		t.Fatalf("counted %d failures for the identifier, want 3", byID)
	}
	if byIP != 4 {
		t.Fatalf("counted %d failures for the IP, want 4", byIP)
	}

	if err := db.ClearLoginAttempts(ctx, "user@example.test"); err != nil {
		t.Fatalf("ClearLoginAttempts: %v", err)
	}
	byID, _, _ = db.CountFailedLogins(ctx, "user@example.test", "", since)
	if byID != 0 {
		t.Fatalf("a successful sign-in left %d failures behind", byID)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()

	// A setting nobody has configured yet must read as empty, not as an error.
	v, enc, err := db.GetSetting(ctx, "s3.bucket")
	if err != nil || v != "" || enc {
		t.Fatalf("unset setting gave %q/%v/%v, want empty", v, enc, err)
	}
	if err := db.SetSetting(ctx, "s3.bucket", "backups", false, "usr_1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := db.SetSetting(ctx, "s3.secret_key", "SKF1.sealed", true, "usr_1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := db.SetSetting(ctx, "s3.bucket", "backups-2", false, "usr_1"); err != nil {
		t.Fatalf("SetSetting (overwrite): %v", err)
	}

	all, err := db.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if all["s3.bucket"].Value != "backups-2" {
		t.Fatalf("setting did not overwrite: %q", all["s3.bucket"].Value)
	}
	if !all["s3.secret_key"].Encrypted {
		t.Fatal("encrypted flag was lost")
	}
}

func TestListSealedSecretsFindsEveryEncryptedColumn(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, env := seedTeam(t, db)

	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web"}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if err := db.SetVariable(ctx, &Variable{AppID: app.ID, Key: "SECRET", IsSecret: true}, "SKF1.a"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	srv := Server{TeamID: team.ID, Name: "n1", Host: "203.0.113.1", SSHKeyEnc: "SKF1.b"}
	if err := db.CreateServer(ctx, &srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if err := db.CreateDatabase(ctx, &Database{EnvironmentID: env.ID, Name: "main", Slug: "main",
		Engine: "postgres", CredentialsEnc: "SKF1.c"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if err := db.SetSetting(ctx, "smtp.password", "SKF1.d", true, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	refs, err := db.ListSealedSecrets(ctx)
	if err != nil {
		t.Fatalf("ListSealedSecrets: %v", err)
	}
	tables := map[string]bool{}
	for _, r := range refs {
		tables[r.Table] = true
	}
	// Rotation is only correct if it can find every encrypted column. Missing
	// one here means secrets silently stay on a retired key.
	for _, want := range []string{"app_variables", "servers", "databases", "settings"} {
		if !tables[want] {
			t.Fatalf("rotation would miss encrypted values in %s", want)
		}
	}

	// And rewriting through UpdateSealed must land in the right row.
	for _, r := range refs {
		if r.Table == "servers" {
			if err := db.UpdateSealed(ctx, r, "SKF1.rewrapped"); err != nil {
				t.Fatalf("UpdateSealed: %v", err)
			}
		}
	}
	reloaded, err := db.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if reloaded.SSHKeyEnc != "SKF1.rewrapped" {
		t.Fatalf("rewrapped key is %q, want SKF1.rewrapped", reloaded.SSHKeyEnc)
	}
}

func TestLastOwnerProtectionData(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, _ := seedTeam(t, db)

	n, err := db.CountOwners(ctx, team.ID)
	if err != nil {
		t.Fatalf("CountOwners: %v", err)
	}
	if n != 1 {
		t.Fatalf("team has %d owners, want 1", n)
	}

	second := User{Email: "second@example.test", PasswordHash: "x"}
	if err := db.CreateUser(ctx, &second); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.AddMember(ctx, team.ID, second.ID, RoleMember); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	// AddMember doubles as "change role", so adding again must not duplicate.
	if err := db.AddMember(ctx, team.ID, second.ID, RoleOwner); err != nil {
		t.Fatalf("AddMember (promote): %v", err)
	}
	members, err := db.ListMembers(ctx, team.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("team has %d members, want 2", len(members))
	}
	if n, _ := db.CountOwners(ctx, team.ID); n != 2 {
		t.Fatalf("team has %d owners after promotion, want 2", n)
	}
}

// TestSnapshotIsCompleteWhileTheWriterIsRunning: the instruction used to be
// "copy panel.db", and in WAL mode a committed transaction can be in
// panel.db-wal and not yet in panel.db. A copy of the one file comes back
// missing whatever was not checkpointed, and nothing says so — which is the
// worst way for a backup to fail, because it is found out during a restore.
func TestSnapshotIsCompleteWhileTheWriterIsRunning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.db")

	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	snapshot := filepath.Join(dir, "copy.db")
	if err := db.Snapshot(t.Context(), snapshot); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Read it back as its own database, without the original's WAL beside it.
	// This is what a restore on another machine does.
	copied, err := Open(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("open the snapshot: %v", err)
	}
	defer copied.Close()

	restored, err := copied.GetTeam(t.Context(), team.ID)
	if err != nil {
		t.Fatalf("the team written before the snapshot is not in it: %v", err)
	}
	if restored.Slug != "acme" {
		t.Fatalf("the snapshot holds %q, want acme", restored.Slug)
	}
}

// TestSnapshotRefusesToOverwrite: a backup that quietly replaces the previous
// one is a backup that exists once.
func TestSnapshotRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	existing := filepath.Join(dir, "copy.db")
	if err := os.WriteFile(existing, []byte("not a database"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.Snapshot(t.Context(), existing); err == nil {
		t.Fatal("an existing file was overwritten")
	}
	if err := db.Snapshot(t.Context(), filepath.Join(dir, "it's.db")); err == nil {
		t.Fatal("a path with a quote in it was accepted into a literal SQL statement")
	}
}

// TestPanelWideAuditEventsAreVisible: a password change, a two-factor change, a
// settings change, a key rotation and a panel upgrade are all recorded with no
// team, because there is one panel however many teams share it. The audit list
// filtered on the team alone, so every one of them went into the table and came
// out of nothing — which is the half of the log an operator most wants.
func TestPanelWideAuditEventsAreVisible(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	user, team, _, _ := seedTeam(t, db)

	events := []AuditEvent{
		{TeamID: team.ID, ActorID: user.ID, ActorLabel: user.Email, Action: "app.created"},
		{ActorID: user.ID, ActorLabel: user.Email, Action: "auth.password_changed"},
		{Action: "security.key_rotated"}, // nobody's: the panel did it
	}
	for i := range events {
		if err := db.RecordAudit(ctx, &events[i]); err != nil {
			t.Fatalf("RecordAudit: %v", err)
		}
	}

	listed, err := db.ListAudit(ctx, team.ID, "", "", 100)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range listed {
		seen[e.Action] = true
	}
	for _, want := range []string{"app.created", "auth.password_changed", "security.key_rotated"} {
		if !seen[want] {
			t.Errorf("%s was recorded and is not in the audit log", want)
		}
	}
}

// TestAnotherTeamsPeopleStayOutOfTheAuditLog: a panel-wide event carries the
// actor's address, and a team's admin should not learn from it that somebody in
// another team exists.
func TestAnotherTeamsPeopleStayOutOfTheAuditLog(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, _ := seedTeam(t, db)

	outsider := User{Email: "elsewhere@example.test", PasswordHash: "x"}
	if err := db.CreateUser(ctx, &outsider); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	theirs := AuditEvent{
		ActorID: outsider.ID, ActorLabel: outsider.Email, Action: "auth.password_changed",
	}
	if err := db.RecordAudit(ctx, &theirs); err != nil {
		t.Fatalf("RecordAudit: %v", err)
	}

	listed, err := db.ListAudit(ctx, team.ID, "", "", 100)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	for _, e := range listed {
		if e.ActorID == outsider.ID {
			t.Fatalf("the audit log shows %s, who is in no team of this one", e.ActorLabel)
		}
	}
}

func TestAnAppAndADatabaseCannotShareAName(t *testing.T) {
	// They share a namespace and both render a Service under their slug, so
	// two of them under one name is not two things side by side: the second
	// takes the first's address over, and deleting either takes the other's
	// Service with it.
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)

	app := App{EnvironmentID: env.ID, Name: "Cache", Slug: "cache", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	owner, err := db.SlugOwnerInEnvironment(ctx, env.ID, "cache")
	if err != nil {
		t.Fatalf("SlugOwnerInEnvironment: %v", err)
	}
	if owner != "app" {
		t.Fatalf("the name is held by %q, want app", owner)
	}

	// A free name says so.
	if owner, err := db.SlugOwnerInEnvironment(ctx, env.ID, "queue"); err != nil || owner != "" {
		t.Fatalf("an unused name reported %q (%v), want it free", owner, err)
	}

	// And the other direction.
	data := Database{EnvironmentID: env.ID, Name: "Queue", Slug: "queue", Engine: "redis"}
	if err := db.CreateDatabase(ctx, &data); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if owner, err := db.SlugOwnerInEnvironment(ctx, env.ID, "queue"); err != nil || owner != "database" {
		t.Fatalf("the name is held by %q (%v), want database", owner, err)
	}

	// Another environment is its own world: the same name in staging and in
	// production has never been a problem, and refusing it would be.
	other := Environment{ProjectID: env.ProjectID, Name: "Staging", Slug: "staging", Namespace: "acme-staging"}
	if err := db.CreateEnvironment(ctx, &other); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}
	if owner, err := db.SlugOwnerInEnvironment(ctx, other.ID, "cache"); err != nil || owner != "" {
		t.Fatalf("a name used in another environment reported %q (%v), want it free", owner, err)
	}
}

func TestImagesWorthKeepingBoundsTheHistory(t *testing.T) {
	// The registry sweep deletes what is not in this list, so anything missing
	// here is an image somebody could still want and will not have.
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)

	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	for i := 1; i <= 15; i++ {
		d := Deployment{AppID: app.ID, Image: fmt.Sprintf("registry:5000/acme-prod/web:v%d", i)}
		if err := db.CreateDeployment(ctx, &d); err != nil {
			t.Fatalf("CreateDeployment %d: %v", i, err)
		}
	}

	keep, err := db.ImagesWorthKeeping(ctx, 10)
	if err != nil {
		t.Fatalf("ImagesWorthKeeping: %v", err)
	}
	if len(keep) != 10 {
		t.Fatalf("%d images kept, want the last 10", len(keep))
	}

	held := map[string]bool{}
	for _, image := range keep {
		held[image] = true
	}
	// The newest is kept and the oldest is not: a rollback further back than
	// the Deployment's own revision history is not offered, so the image for
	// it would be kept for nobody.
	if !held["registry:5000/acme-prod/web:v15"] {
		t.Error("the newest image is not kept, so the running app's image would be deleted")
	}
	if held["registry:5000/acme-prod/web:v1"] {
		t.Error("an image from further back than the rollback history is kept for nobody")
	}

	// An app's own current image is kept even when no deployment row carries
	// it, which is the case for an app created from a prebuilt image.
	other := App{EnvironmentID: env.ID, Name: "api", Slug: "api", Image: "nginx:1.27", Replicas: 1}
	if err := db.CreateApp(ctx, &other); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	keep, err = db.ImagesWorthKeeping(ctx, 10)
	if err != nil {
		t.Fatalf("ImagesWorthKeeping: %v", err)
	}
	found := false
	for _, image := range keep {
		if image == "nginx:1.27" {
			found = true
		}
	}
	if !found {
		t.Error("an app's own image is not kept when no deployment row carries it")
	}
}

func TestPruneKeepsWhatIsStillInFlight(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	_, team, _, env := seedTeam(t, db)

	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	for i := 1; i <= 12; i++ {
		d := Deployment{AppID: app.ID, Image: fmt.Sprintf("registry:5000/acme-prod/web:v%d", i)}
		if err := db.CreateDeployment(ctx, &d); err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		// The oldest is left queued, as a panel killed mid-build leaves one.
		status := DeploySucceeded
		if i == 1 {
			status = DeployQueued
		}
		if _, err := db.Exec(ctx, `UPDATE deployments SET status = ? WHERE id = ?`, status, d.ID); err != nil {
			t.Fatalf("set status: %v", err)
		}
		if i == 2 {
			// One line of build output, to prove it goes with its deployment.
			if _, err := db.AppendBuildLog(ctx, d.ID, "stdout", "compiling"); err != nil {
				t.Fatalf("AppendBuildLog: %v", err)
			}
		}
	}

	// An audit entry old enough to go, and one that is not.
	if _, err := db.Exec(ctx, `INSERT INTO audit_events (id, team_id, action, at) VALUES (?,?,?,?)`,
		"aud_old", team.ID, "app.created", FormatTime(time.Now().AddDate(-3, 0, 0))); err != nil {
		t.Fatalf("insert old audit: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO audit_events (id, team_id, action, at) VALUES (?,?,?,?)`,
		"aud_new", team.ID, "app.deployed", Now()); err != nil {
		t.Fatalf("insert new audit: %v", err)
	}

	report, err := db.Prune(ctx, Retention{DeploymentsPerApp: 5, OperationDays: 90, AuditDays: 365})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.Empty() {
		t.Fatal("the pass removed nothing at all")
	}

	deployments, err := db.ListDeployments(ctx, app.ID, 100)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	// Five finished ones, plus the queued one that must never be touched: a
	// panel killed mid-build leaves it, and deleting it takes the log that says
	// what happened with it.
	if len(deployments) != 6 {
		var left []int
		for _, d := range deployments {
			left = append(left, d.Number)
		}
		t.Fatalf("%d deployments left (%v), want 5 finished plus the unfinished one", len(deployments), left)
	}
	queued := false
	for _, d := range deployments {
		if d.Status == DeployQueued {
			queued = true
		}
	}
	if !queued {
		t.Error("the unfinished deployment was pruned")
	}

	// Build logs go with their deployment rather than being left orphaned.
	var logs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM build_logs`).Scan(&logs); err != nil {
		t.Fatalf("count build logs: %v", err)
	}
	if logs != 0 {
		t.Errorf("%d build log lines survived the deployment they belong to", logs)
	}

	var audits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&audits); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if audits != 1 {
		t.Errorf("%d audit entries left, want only the recent one", audits)
	}

	// Running it again finds nothing, which is what makes it safe on a timer.
	again, err := db.Prune(ctx, Retention{DeploymentsPerApp: 5, OperationDays: 90, AuditDays: 365})
	if err != nil {
		t.Fatalf("Prune again: %v", err)
	}
	if !again.Empty() {
		t.Errorf("a second pass removed %s, so the first one was not complete", again.String())
	}
}

func TestOnlyRecentVersionsCanBeRolledBackTo(t *testing.T) {
	// The registry keeps the last few images per app. A deployment record older
	// than that is worth reading and is no longer somewhere to go back to:
	// offering it would offer a rollout that fails on a pull.
	db := testDB(t)
	ctx := t.Context()
	_, _, _, env := seedTeam(t, db)

	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	ids := []string{}
	for i := 1; i <= 6; i++ {
		d := Deployment{AppID: app.ID, Image: fmt.Sprintf("registry:5000/acme-prod/web:v%d", i)}
		if err := db.CreateDeployment(ctx, &d); err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if _, err := db.Exec(ctx, `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, d.ID); err != nil {
			t.Fatalf("set status: %v", err)
		}
		ids = append(ids, d.ID)
	}

	newest, oldest := ids[5], ids[0]
	if ok, err := db.WithinRollbackWindow(ctx, app.ID, newest, 3); err != nil || !ok {
		t.Fatalf("the newest version cannot be rolled back to (%v, %v)", ok, err)
	}
	if ok, err := db.WithinRollbackWindow(ctx, app.ID, oldest, 3); err != nil || ok {
		t.Fatalf("a version whose image was collected is still offered (%v, %v)", ok, err)
	}

	list, err := db.ListDeployments(ctx, app.ID, 100)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	MarkRollbackTargets(list, 3)
	marked := 0
	for _, d := range list {
		if d.CanRollback {
			marked++
		}
	}
	if marked != 3 {
		t.Fatalf("%d versions are offered for rollback, want 3", marked)
	}
	if !list[0].CanRollback {
		t.Error("the newest version is not offered")
	}
	if list[len(list)-1].CanRollback {
		t.Error("the oldest version is offered although its image is gone")
	}
}

// TestEverySealedColumnIsRotated walks the schema the panel actually creates.
//
// A column that holds an envelope and is not in sealedSources is a secret the
// master key rotation steps over: it stays wrapped in a key the rotation drops
// at the end, and nothing says so until something tries to read it back. That
// happened — the plugins table and its settings were both missed — so this
// reads the schema rather than a list somebody remembered to extend.
func TestEverySealedColumnIsRotated(t *testing.T) {
	db := testDB(t)

	covered := map[string]bool{}
	for _, source := range sealedSources {
		covered[source.table+"."+source.valueCol] = true
	}

	tables, err := db.QueryContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer tables.Close()

	checked := 0
	for tables.Next() {
		var table string
		if err := tables.Scan(&table); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		columns, encrypted := columnsOf(t, db, table)
		for _, column := range columns {
			// Two spellings mean "this holds an envelope": a name ending in
			// _enc or _sealed, and a value column beside an `encrypted` flag.
			sealed := strings.HasSuffix(column, "_enc") || strings.HasSuffix(column, "_sealed") ||
				(encrypted && column == "value")
			if !sealed {
				continue
			}
			checked++
			if !covered[table+"."+column] {
				t.Errorf("%s.%s holds sealed values and is not in sealedSources, "+
					"so a master key rotation would leave it behind", table, column)
			}
		}
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	if checked < len(sealedSources) {
		t.Fatalf("only %d sealed columns were found in the schema and %d are listed; "+
			"this test is not reading the schema", checked, len(sealedSources))
	}
}

// columnsOf returns a table's column names and whether one of them is the
// `encrypted` flag that marks a value column as holding an envelope.
func columnsOf(t *testing.T, db *DB, table string) ([]string, bool) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("read the columns of %s: %v", table, err)
	}
	defer rows.Close()
	var names []string
	encrypted := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan a column of %s: %v", table, err)
		}
		if name == "encrypted" {
			encrypted = true
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names, encrypted
}

// A step's note survives the round trip, key and values included.
//
// The step's own name has been translated since the panel shipped; the sentence
// under it was the English the Go code wrote. It carries a key now, and the
// values that went into it, and both have to come back out of SQLite — a key
// that is written and not read is a translation nobody sees.
func TestAStepRemembersHowToSayItselfAgain(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()

	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	op := Operation{TeamID: team.ID, Kind: "add_server", TargetType: "server", TargetID: "srv_1"}
	if err := db.CreateOperation(ctx, &op, []string{"connect", "preflight"}); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}

	note := StepNote{
		Message: "Ubuntu 24.04, 4 cores, 8192 MB memory, 40 GB free",
		Key:     "preflightOk",
		Args:    []string{"Ubuntu 24.04", "4", "8192", "40"},
		Details: []StepDetail{
			{Text: "Docker is already running on this server.", Key: "preflight.conflict_docker.detail"},
			{Text: "Host key ssh-ed25519 AAAA", Key: "hostKey", Args: []string{"ssh-ed25519 AAAA"}},
		},
	}
	if err := db.SetStepStatus(ctx, op.ID, "preflight", StepSucceeded, note, ""); err != nil {
		t.Fatalf("SetStepStatus: %v", err)
	}

	steps, err := db.ListOperationSteps(ctx, op.ID)
	if err != nil {
		t.Fatalf("ListOperationSteps: %v", err)
	}
	var got OperationStep
	for _, step := range steps {
		if step.Key == "preflight" {
			got = step
		}
	}
	if got.Message != note.Message {
		t.Errorf("the English came back as %q", got.Message)
	}
	if got.MessageKey != note.Key {
		t.Errorf("the key came back as %q, want %q", got.MessageKey, note.Key)
	}
	if len(got.MessageArgs) != len(note.Args) {
		t.Fatalf("the values came back as %q, want %q", got.MessageArgs, note.Args)
	}
	for i := range note.Args {
		if got.MessageArgs[i] != note.Args[i] {
			t.Errorf("value %d came back as %q, want %q", i, got.MessageArgs[i], note.Args[i])
		}
	}

	// The extra lines come back too. They used to be one joined English string
	// in a column the interface only read for a failed step, so everything
	// written against a step that succeeded was stored and shown to nobody.
	if len(got.Notes) != len(note.Details) {
		t.Fatalf("the notes came back as %+v, want %+v", got.Notes, note.Details)
	}
	for i := range note.Details {
		if got.Notes[i].Key != note.Details[i].Key || got.Notes[i].Text != note.Details[i].Text {
			t.Errorf("note %d came back as %+v, want %+v", i, got.Notes[i], note.Details[i])
		}
	}

	// Retry clears the sentence, so a step that ran again does not show what it
	// said the time before.
	if err := db.ResetStepsFrom(ctx, op.ID, "preflight"); err != nil {
		t.Fatalf("ResetStepsFrom: %v", err)
	}
	steps, _ = db.ListOperationSteps(ctx, op.ID)
	for _, step := range steps {
		if step.Key == "preflight" && (step.MessageKey != "" || len(step.MessageArgs) != 0 || len(step.Notes) != 0) {
			t.Errorf("a reset step still says %q %q %+v", step.MessageKey, step.MessageArgs, step.Notes)
		}
	}
}
