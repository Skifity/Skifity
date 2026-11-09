package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"skifity/internal/cron"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/templates"
)

// A new database is backed up every day when there is somewhere to put it.
// See backup_defaults.go.

// storingDatabases is fakeDatabases that keeps what it makes, with an id
// from the name, so the schedule a database gets can be looked up and two
// databases have ids of their own.
type storingDatabases struct{ fakeDatabases }

func (f *storingDatabases) Create(ctx context.Context, env store.Environment, req CreateDatabaseRequest) (store.Database, error) {
	f.log.note("create " + req.Engine)
	slug := kube.Slugify(req.Name)
	record := store.Database{ID: "db_" + slug, EnvironmentID: env.ID, Name: req.Name, Slug: slug, Engine: req.Engine, Status: "creating"}
	if err := f.db.CreateDatabase(ctx, &record); err != nil {
		return store.Database{}, err
	}
	return record, nil
}

// backupHarness is a panel whose databases are kept, and whose backup storage
// is set up when storage says so.
func backupHarness(t *testing.T, storage bool) (*harness, *recordingBackups) {
	t.Helper()
	h := newHarness(t)
	log := &recorder{}
	h.api.databases = &storingDatabases{fakeDatabases{log: log, db: h.db}}
	h.api.deployer = &fakeDeployer{log: log}
	backups := &recordingBackups{storage: BackupStorage{Configured: storage}}
	h.api.backups = backups
	return h, backups
}

// createDatabase creates one through the API and answers what it said.
func (h *harness) createDatabase(as tenant, env store.Environment, body map[string]any) createdDatabase {
	h.t.Helper()
	status, answer := h.do(as, http.MethodPost, "/api/environments/"+env.ID+"/databases", body)
	if status != http.StatusAccepted {
		h.t.Fatalf("creating a database answered %d: %s", status, answer)
	}
	var created createdDatabase
	if err := json.Unmarshal([]byte(answer), &created); err != nil {
		h.t.Fatal(err)
	}
	return created
}

// policyOf is a database's stored schedule, and whether it has one.
func (h *harness) policyOf(databaseID string) (store.BackupPolicy, bool) {
	h.t.Helper()
	policy, err := h.db.GetBackupPolicy(h.t.Context(), "database", databaseID)
	if errors.Is(err, store.ErrNotFound) {
		return store.BackupPolicy{}, false
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return policy, true
}

// minuteOfDay reads "M H * * *" as minutes after midnight.
func minuteOfDay(t *testing.T, schedule string) int {
	t.Helper()
	fields := strings.Fields(schedule)
	if len(fields) != 5 || fields[2] != "*" || fields[3] != "*" || fields[4] != "*" {
		t.Fatalf("%q is not a daily schedule", schedule)
	}
	minute, err1 := strconv.Atoi(fields[0])
	hour, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		t.Fatalf("%q is not a daily schedule", schedule)
	}
	return hour*60 + minute
}

func TestANewDatabaseIsBackedUpEveryDayWhenThereIsStorage(t *testing.T) {
	h, _ := backupHarness(t, true)
	acme := h.newTenant("acme")

	orders := h.createDatabase(acme, acme.env, map[string]any{"name": "orders", "engine": "postgres"})
	if !orders.Backup.Enabled || orders.Backup.Reason != "" || orders.Backup.Retention != 7 {
		t.Fatalf("the answer says %+v", orders.Backup)
	}
	policy, ok := h.policyOf(orders.ID)
	if !ok || !policy.Enabled || policy.Retention != 7 || policy.Schedule != orders.Backup.Schedule {
		t.Fatalf("the stored schedule is %+v (stored %v), and the answer said %+v", policy, ok, orders.Backup)
	}
	if at := minuteOfDay(t, policy.Schedule); at < 2*60 || at >= 5*60 {
		t.Errorf("%q is not between 02:00 and 04:59", policy.Schedule)
	}

	analytics := h.createDatabase(acme, acme.env, map[string]any{"name": "analytics", "engine": "mysql"})
	if !analytics.Backup.Enabled {
		t.Fatalf("the second database: %+v", analytics.Backup)
	}
	if analytics.Backup.Schedule == orders.Backup.Schedule {
		t.Errorf("%s and %s are both backed up at %q", orders.ID, analytics.ID, orders.Backup.Schedule)
	}
}

// The minute is spread over the three hours, the same for one database
// whenever it is worked out, and always a schedule the scheduler reads.
func TestTheDefaultMinuteIsSpreadOverThreeHours(t *testing.T) {
	seen := map[int]bool{}
	for i := range 2000 {
		id := store.NewID("db")
		schedule := defaultBackupSchedule(id)
		if schedule != defaultBackupSchedule(id) {
			t.Fatalf("%s gets %q one time and something else the next", id, schedule)
		}
		if _, err := cron.ParseSchedule(schedule); err != nil {
			t.Fatalf("%q (%d) does not parse: %v", schedule, i, err)
		}
		at := minuteOfDay(t, schedule)
		if at < 2*60 || at >= 5*60 {
			t.Fatalf("%q is not between 02:00 and 04:59", schedule)
		}
		seen[at] = true
	}
	// 2000 ids over 180 minutes leave none empty unless the spread is bad.
	if len(seen) < 170 {
		t.Errorf("2000 databases share %d minutes of the 180", len(seen))
	}
}

func TestNoScheduleIsMadeWhenItCannotOrShouldNotBe(t *testing.T) {
	cases := []struct {
		name    string
		storage bool
		setting string
		body    map[string]any
		preview bool
		reason  string
	}{
		{name: "no storage", storage: false, body: map[string]any{"engine": "postgres"}, reason: backupNoStorage},
		{name: "an engine that is not backed up", storage: true, body: map[string]any{"engine": "dragonfly"}, reason: backupNotOffered},
		{name: "asked not to", storage: true, body: map[string]any{"engine": "postgres", "backup": false}, reason: backupDeclined},
		{name: "the default turned off", storage: true, setting: "false", body: map[string]any{"engine": "postgres"}, reason: backupTurnedOff},
		{name: "a preview", storage: true, preview: true, body: map[string]any{"engine": "postgres", "backup": true}, reason: backupPreview},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := backupHarness(t, c.storage)
			acme := h.newTenant("acme")
			if c.setting != "" {
				if err := h.db.SetSetting(t.Context(), settings.KeyDefaultBackups, c.setting, false, acme.user.ID); err != nil {
					t.Fatal(err)
				}
			}
			env := acme.env
			if c.preview {
				env = store.Environment{ProjectID: acme.project.ID, Name: "pr-7", Slug: "pr-7",
					Kind: store.EnvPreview, Namespace: "acme-pr-7"}
				if err := h.db.CreateEnvironment(t.Context(), &env); err != nil {
					t.Fatal(err)
				}
			}
			c.body["name"] = "orders"
			created := h.createDatabase(acme, env, c.body)
			if created.Backup.Enabled || created.Backup.Reason != c.reason {
				t.Errorf("the answer says %+v, want the reason %s", created.Backup, c.reason)
			}
			if policy, ok := h.policyOf(created.ID); ok {
				t.Errorf("a schedule was stored: %+v", policy)
			}
		})
	}
}

// Asking for one is an answer of its own: it is made with the panel's
// default off.
func TestAskingForABackupOverridesTheDefaultBeingOff(t *testing.T) {
	h, _ := backupHarness(t, true)
	acme := h.newTenant("acme")
	if err := h.db.SetSetting(t.Context(), settings.KeyDefaultBackups, "false", false, acme.user.ID); err != nil {
		t.Fatal(err)
	}
	created := h.createDatabase(acme, acme.env, map[string]any{"name": "orders", "engine": "postgres", "backup": true})
	if !created.Backup.Enabled {
		t.Fatalf("the answer says %+v", created.Backup)
	}
	if _, ok := h.policyOf(created.ID); !ok {
		t.Error("no schedule was stored")
	}
}

// The other two ways a database is made: with a new app, and by a template.
func TestDatabasesMadeWithAnAppOrByATemplateAreBackedUp(t *testing.T) {
	h, _ := backupHarness(t, true)
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
		"name": "shop", "source_type": "image", "image": "nginx:1.27", "port": 80,
		"databases": []map[string]any{{"engine": "postgres"}, {"engine": "dragonfly"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the app answered %d: %s", status, body)
	}
	var answer createAppAnswerWithBackups
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	for _, result := range answer.Databases {
		_, stored := h.policyOf(result.DatabaseID)
		want := result.Engine == "postgres"
		if result.Backup == nil || result.Backup.Enabled != want || stored != want {
			t.Errorf("%s: the answer says %+v and a schedule is stored: %v", result.Engine, result.Backup, stored)
		}
	}

	tpl := templates.Template{
		ID: "notes", Name: "Notes",
		Services:  []templates.Service{{Name: "web", Image: "example/notes:1.0.0", Port: 80, Public: true}},
		Databases: []templates.DatabaseSpec{{Name: "notes-db", Engine: "postgres", LinkTo: []string{"web"}}},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/templates/notes/install", nil).WithContext(t.Context())
	plan, err := h.api.planTemplateInstall(request.Context(), acme.env, tpl, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.api.installTemplate(request, plan, acme.user)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Databases) != 1 {
		t.Fatalf("the template made %+v", result.Databases)
	}
	if policy, ok := h.policyOf(result.Databases[0].ID); !ok || !policy.Enabled {
		t.Errorf("the template's database has the schedule %+v (stored %v)", policy, ok)
	}
}

// createAppAnswerWithBackups is the part of creating an app these tests read.
type createAppAnswerWithBackups struct {
	Databases []databaseResult `json:"databases"`
}

// A clone keeps the schedule somebody chose, and a database on its default
// minute gets a default minute of its own.
func TestAClonedDatabaseKeepsItsSourcesSchedule(t *testing.T) {
	h, _ := backupHarness(t, true)
	acme := h.newTenant("acme")
	ctx := t.Context()
	onDefault := h.database(acme, "orders")
	chosen := h.database(acme, "analytics")
	h.database(acme, "cache")
	for _, policy := range []store.BackupPolicy{
		defaultBackupPolicy(onDefault.ID),
		{TargetType: "database", TargetID: chosen.ID, Schedule: "0 */6 * * *", Retention: 30, Enabled: false},
	} {
		if err := h.db.SetBackupPolicy(ctx, &policy); err != nil {
			t.Fatal(err)
		}
	}

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/clone", map[string]any{"name": "Staging"})
	if status != http.StatusCreated {
		t.Fatalf("cloning answered %d: %s", status, body)
	}
	var result clonedEnvironment
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	copies := map[string]store.Database{}
	for _, made := range result.Databases {
		copies[made.Name] = made
	}
	if policy, ok := h.policyOf(copies["orders"].ID); !ok || !policy.Enabled ||
		policy.Schedule != defaultBackupSchedule(copies["orders"].ID) {
		t.Errorf("the copy of a database on its default minute has %+v (stored %v)", policy, ok)
	}
	if policy, ok := h.policyOf(copies["analytics"].ID); !ok || policy.Enabled ||
		policy.Schedule != "0 */6 * * *" || policy.Retention != 30 {
		t.Errorf("the copy of a database with a chosen schedule has %+v (stored %v)", policy, ok)
	}
	if policy, ok := h.policyOf(copies["cache"].ID); ok {
		t.Errorf("the copy of a database with no schedule was given %+v", policy)
	}
}

func TestEveryDatabaseOfATeamCanBeGivenTheDefault(t *testing.T) {
	h, _ := backupHarness(t, true)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	ctx := t.Context()

	bare := h.database(acme, "orders")
	off := h.database(acme, "analytics")
	turnedOff := store.BackupPolicy{TargetType: "database", TargetID: off.ID, Schedule: "0 3 * * *", Retention: 7, Enabled: false}
	if err := h.db.SetBackupPolicy(ctx, &turnedOff); err != nil {
		t.Fatal(err)
	}
	cache := store.Database{EnvironmentID: acme.env.ID, Name: "cache", Slug: "cache", Engine: "dragonfly", Status: "running"}
	if err := h.db.CreateDatabase(ctx, &cache); err != nil {
		t.Fatal(err)
	}
	preview := store.Environment{ProjectID: acme.project.ID, Name: "pr-7", Slug: "pr-7", Kind: store.EnvPreview, Namespace: "acme-pr-7"}
	if err := h.db.CreateEnvironment(ctx, &preview); err != nil {
		t.Fatal(err)
	}
	previewDB := store.Database{EnvironmentID: preview.ID, Name: "orders", Slug: "orders", Engine: "postgres", Status: "running"}
	if err := h.db.CreateDatabase(ctx, &previewDB); err != nil {
		t.Fatal(err)
	}
	theirs := h.database(other, "theirs")

	path := "/api/teams/" + acme.team.ID + "/backup-policies/defaults"
	member := h.newMember(acme, "dev", store.RoleMember)
	if status, body := h.do(member, http.MethodPost, path, nil); status != http.StatusForbidden {
		t.Fatalf("a member was answered %d: %s", status, body)
	}
	if _, ok := h.policyOf(bare.ID); ok {
		t.Fatal("a member's refused request made a schedule")
	}

	admin := h.newMember(acme, "ops", store.RoleAdmin)
	status, body := h.do(admin, http.MethodPost, path, nil)
	if status != http.StatusOK {
		t.Fatalf("an admin was answered %d: %s", status, body)
	}
	var answer struct {
		Items []defaultedDatabase `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Items) != 1 || answer.Items[0].DatabaseID != bare.ID {
		t.Fatalf("the defaults were applied to %+v", answer.Items)
	}
	if policy, ok := h.policyOf(bare.ID); !ok || !policy.Enabled || policy.Schedule != defaultBackupSchedule(bare.ID) {
		t.Errorf("the database with none has %+v (stored %v)", policy, ok)
	}
	if policy, _ := h.policyOf(off.ID); policy.Enabled {
		t.Error("a schedule somebody turned off was turned on")
	}
	for _, untouched := range []store.Database{cache, previewDB, theirs} {
		if policy, ok := h.policyOf(untouched.ID); ok {
			t.Errorf("%s (%s) was given %+v", untouched.Name, untouched.Engine, policy)
		}
	}
	audited, err := h.db.ListAudit(ctx, acme.team.ID, "backup.policy_changed", bare.ID, 10)
	if err != nil || len(audited) != 1 || audited[0].ActorID != admin.user.ID {
		t.Errorf("the audit log has %+v (%v)", audited, err)
	}

	// Again: nothing is left without one.
	if status, body := h.do(admin, http.MethodPost, path, nil); status != http.StatusOK || !strings.Contains(body, `"total":0`) {
		t.Errorf("the second time answered %d: %s", status, body)
	}
}

func TestTheDefaultsNeedStorage(t *testing.T) {
	h, _ := backupHarness(t, false)
	acme := h.newTenant("acme")
	bare := h.database(acme, "orders")
	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/backup-policies/defaults", nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "backup.storage_not_configured") {
		t.Fatalf("with no storage it answered %d: %s", status, body)
	}
	if _, ok := h.policyOf(bare.ID); ok {
		t.Error("a schedule was made with nowhere to put a backup")
	}
}

// What anybody signed in is told about backup storage, and nothing more.
func TestEveryoneMayKnowWhetherThereIsBackupStorage(t *testing.T) {
	h, backups := backupHarness(t, true)
	backups.storage.Sealed = true
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "reader", store.RoleViewer)
	status, body := h.do(viewer, http.MethodGet, "/api/backup-storage", nil)
	if status != http.StatusOK {
		t.Fatalf("a viewer was answered %d: %s", status, body)
	}
	var answer map[string]any
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"configured": true, "sealed": true, "default_backups": true}
	if len(answer) != len(want) {
		t.Errorf("the answer has more than whether: %s", body)
	}
	for key, value := range want {
		if answer[key] != value {
			t.Errorf("%s is %v, want %v", key, answer[key], value)
		}
	}
}

// A schedule outlives nothing: deleting the database deletes it, and one
// left behind by an environment that went is not run.
func TestAScheduleGoesWithItsDatabase(t *testing.T) {
	h, _ := backupHarness(t, true)
	acme := h.newTenant("acme")
	ctx := t.Context()
	kept := h.database(acme, "kept")
	gone := h.database(acme, "gone")
	for _, id := range []string{kept.ID, gone.ID, "db_never_was"} {
		policy := defaultBackupPolicy(id)
		if err := h.db.SetBackupPolicy(ctx, &policy); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.db.DeleteDatabase(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.policyOf(gone.ID); ok {
		t.Error("the deleted database's schedule is still there")
	}
	due, err := h.db.ListEnabledBackupPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].TargetID != kept.ID {
		t.Errorf("the scheduler would run %+v", due)
	}
}
