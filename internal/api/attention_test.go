package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"skifity/internal/store"
)

// What needs attention across a team: one item per thing that is wrong, with
// the page to deal with it on, to the people who may see that thing.

// attentionAnswer asks for a team's list as somebody and decodes it.
func (h *harness) attentionAnswer(as tenant) []attentionItem {
	h.t.Helper()
	status, body := h.do(as, http.MethodGet, "/api/teams/"+as.team.ID+"/attention", nil)
	if status != http.StatusOK {
		h.t.Fatalf("attention answered %d:\n%s", status, body)
	}
	var answer struct {
		Items []attentionItem `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		h.t.Fatal(err)
	}
	if answer.Items == nil || answer.Total != len(answer.Items) {
		h.t.Fatalf("the list is not a list: %s", body)
	}
	return answer.Items
}

// deployed records a deployment of an app that ended as given.
func (h *harness) deployed(app store.App, status store.DeploymentStatus, image string) store.Deployment {
	h.t.Helper()
	deployment := store.Deployment{AppID: app.ID, Image: image}
	if err := h.db.CreateDeployment(h.t.Context(), &deployment); err != nil {
		h.t.Fatal(err)
	}
	if status != store.DeployQueued {
		if err := h.db.UpdateDeploymentStatus(h.t.Context(), deployment.ID, status, "", "", ""); err != nil {
			h.t.Fatal(err)
		}
	}
	deployment.Status = status
	return deployment
}

// machine adds a server to a team at an address of its own: the harness's
// node is always the same one, and a team cannot have it twice.
func (h *harness) machine(owner tenant, name, host string) store.Server {
	h.t.Helper()
	record := store.Server{TeamID: owner.team.ID, Name: name, Host: host, SSHPort: 22, SSHUser: "root",
		Role: "worker", Status: store.ServerReady}
	if err := h.db.CreateServer(h.t.Context(), &record); err != nil {
		h.t.Fatalf("create server: %v", err)
	}
	return record
}

// sql runs a statement the store has no reason to offer, such as moving a
// row back in time.
func (h *harness) sql(query string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(h.t.Context(), query, args...); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
}

// nightlyBackups puts a target on a 03:00 schedule set long enough ago that
// last night's run was due, with its newest backup taken at the time given
// and ending as given; a zero time is no backup at all.
func (h *harness) nightlyBackups(targetType, targetID string, taken time.Time, status string) {
	h.t.Helper()
	policy := store.BackupPolicy{TargetType: targetType, TargetID: targetID, Schedule: "0 3 * * *", Retention: 7, Enabled: true}
	if err := h.db.SetBackupPolicy(h.t.Context(), &policy); err != nil {
		h.t.Fatal(err)
	}
	h.sql(`UPDATE backup_policies SET updated_at = ? WHERE id = ?`,
		store.FormatTime(time.Now().Add(-5*24*time.Hour)), policy.ID)
	if taken.IsZero() {
		return
	}
	backup := store.Backup{TargetType: targetType, TargetID: targetID, Status: "running"}
	if err := h.db.CreateBackup(h.t.Context(), &backup); err != nil {
		h.t.Fatal(err)
	}
	if err := h.db.FinishBackup(h.t.Context(), backup.ID, status, "key", 1, ""); err != nil {
		h.t.Fatal(err)
	}
	h.sql(`UPDATE backups SET created_at = ?, finished_at = ? WHERE id = ?`,
		store.FormatTime(taken), store.FormatTime(taken.Add(time.Minute)), backup.ID)
}

// A team with one of everything that can go wrong, and one of everything
// that is fine: each wrong thing is said once, with its words and its page,
// and nothing fine is said at all.
func TestAttentionSaysEachThingThatIsWrongOnce(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ctx := t.Context()
	now := time.Now()

	// Fine: deployed, running, backed up after its last 03:00, scanned clean.
	healthy := h.app(acme, "healthy")
	h.deployed(healthy, store.DeploySucceeded, "registry.local/healthy:1")
	if err := h.db.SetAppStatus(ctx, healthy.ID, "running"); err != nil {
		t.Fatal(err)
	}
	fine := h.database(acme, "fine-db")
	h.nightlyBackups("database", fine.ID, now, "succeeded")
	h.machine(acme, "node-ok", "198.51.100.11")

	// A deploy that failed after one that worked: the failed one is named.
	broken := h.app(acme, "broken")
	h.deployed(broken, store.DeploySucceeded, "registry.local/broken:1")
	failed := h.deployed(broken, store.DeployFailed, "")
	h.sql(`UPDATE apps SET status = 'failed' WHERE id = ?`, broken.ID)

	// A deploy that failed and was followed by one that worked is history.
	recovered := h.app(acme, "recovered")
	h.deployed(recovered, store.DeployFailed, "")
	h.deployed(recovered, store.DeploySucceeded, "registry.local/recovered:2")

	crashing := h.app(acme, "crashing")
	h.deployed(crashing, store.DeploySucceeded, "registry.local/crashing:1")
	h.sql(`UPDATE apps SET status = 'unhealthy' WHERE id = ?`, crashing.ID)

	hot := h.app(acme, "hot")
	if err := h.db.SetAlertsFiring(ctx, hot.ID, []string{"memory", "errors"}); err != nil {
		t.Fatal(err)
	}

	vulnerable := h.app(acme, "vulnerable")
	live := h.deployed(vulnerable, store.DeploySucceeded, "registry.local/vulnerable:1")
	scan := store.ImageScan{AppID: vulnerable.ID, DeploymentID: live.ID, Image: live.Image, Trigger: "deploy"}
	if err := h.db.CreateImageScan(ctx, &scan); err != nil {
		t.Fatal(err)
	}
	if err := h.db.FinishImageScan(ctx, scan.ID, store.ScanResult{Counts: store.ScanCounts{Critical: 2, High: 5}}); err != nil {
		t.Fatal(err)
	}

	drifted := h.app(acme, "drifted")
	if err := h.db.RecordAppDrift(ctx, drifted.ID, store.DriftDrifted, "[]", "f1", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	withDomain := h.app(acme, "shop")
	domain := h.domain(withDomain, "shop.example.com")
	if err := h.db.SetDomainStatus(ctx, domain.ID, "failed", "the DNS record points elsewhere"); err != nil {
		t.Fatal(err)
	}

	sick := h.database(acme, "sick-db")
	if err := h.db.SetDatabaseStatus(ctx, sick.ID, "failed", "it did not start"); err != nil {
		t.Fatal(err)
	}

	failing := h.database(acme, "failing-db")
	h.nightlyBackups("database", failing.ID, now.Add(-time.Minute), "failed")

	files := h.app(acme, "files")
	volume := store.Volume{AppID: files.ID, Name: "uploads", MountPath: "/data", SizeGB: 1}
	if err := h.db.CreateVolume(ctx, &volume); err != nil {
		t.Fatal(err)
	}
	h.nightlyBackups("volume", volume.ID, now.Add(-2*24*time.Hour), "succeeded")

	lost := h.machine(acme, "node-lost", "198.51.100.12")
	if err := h.db.SetServerStatus(ctx, lost.ID, store.ServerNotReady, ""); err != nil {
		t.Fatal(err)
	}
	full := h.machine(acme, "node-full", "198.51.100.13")
	if err := h.db.SetServerAlertsFiring(ctx, full.ID, []string{"disk"}); err != nil {
		t.Fatal(err)
	}

	certificate := store.Certificate{
		ID: store.NewID("cert"), TeamID: acme.team.ID, Name: "wildcard", ChainPEM: "chain",
		Hostnames: []string{"*.example.com"}, NotBefore: now.Add(-80 * 24 * time.Hour),
		NotAfter: now.Add(5*24*time.Hour - time.Hour),
	}
	if _, err := h.db.SaveCertificate(ctx, &certificate, "sealed"); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		kind, code, severity, target, path string
	}{
		{"deploy_failed", "deploy_failed", "error", broken.ID,
			"/apps/" + broken.ID + "?tab=deployments&deployment=" + failed.ID},
		{"app_unhealthy", "app_unhealthy", "error", crashing.ID, "/apps/" + crashing.ID},
		{"app_alert", "app_alert", "warning", hot.ID, "/apps/" + hot.ID},
		{"app_vulnerable", "app_vulnerable", "warning", vulnerable.ID, "/apps/" + vulnerable.ID + "?tab=security"},
		{"app_drifted", "app_drifted", "warning", drifted.ID, "/apps/" + drifted.ID + "?tab=advanced"},
		{"domain_cert_failed", "domain_cert_failed", "error", domain.ID, "/apps/" + withDomain.ID + "?tab=domains"},
		{"database_unwell", "database_failed", "error", sick.ID, "/databases/" + sick.ID},
		{"backup_failed", "backup_failed", "error", failing.ID, "/databases/" + failing.ID + "?tab=backups"},
		{"backup_missed", "backup_missed", "error", volume.ID, "/apps/" + files.ID + "?tab=storage"},
		{"server_not_ready", "server_not_ready", "error", lost.ID, "/servers/" + lost.ID},
		{"server_alert", "server_alert", "warning", full.ID, "/servers/" + full.ID},
		{"certificate_expiring", "certificate_expiring", "warning", certificate.ID, "/settings?tab=certificates"},
		{"no_channel", "no_channel", "warning", acme.team.ID, "/settings?tab=notifications"},
	}

	items := h.attentionAnswer(acme)
	if len(items) != len(want) {
		var got []string
		for _, item := range items {
			got = append(got, item.Kind+" "+item.TargetLabel)
		}
		t.Errorf("%d items, want %d: %v", len(items), len(want), got)
	}
	for _, w := range want {
		i := slices.IndexFunc(items, func(item attentionItem) bool { return item.Kind == w.kind && item.TargetID == w.target })
		if i < 0 {
			t.Errorf("nothing says %s about %s", w.kind, w.target)
			continue
		}
		item := items[i]
		if item.Code != w.code || item.Severity != w.severity || item.Path != w.path {
			t.Errorf("%s: code %q, severity %q, path %q; want %q, %q, %q",
				w.kind, item.Code, item.Severity, item.Path, w.code, w.severity, w.path)
		}
		if strings.TrimSpace(item.Message) == "" {
			t.Errorf("%s has no words for the CLI", w.kind)
		}
	}
	for _, item := range items {
		for _, fine := range []string{healthy.ID, recovered.ID, fine.ID} {
			if item.TargetID == fine {
				t.Errorf("something fine was said to need attention: %+v", item)
			}
		}
	}

	// Errors before warnings, whatever order they were found in.
	firstWarning := slices.IndexFunc(items, func(item attentionItem) bool { return item.Severity == "warning" })
	if firstWarning >= 0 && slices.ContainsFunc(items[firstWarning:], func(item attentionItem) bool { return item.Severity == "error" }) {
		t.Error("an error comes after a warning")
	}

	// What fills each sentence in, and when it started where that is known.
	byKind := map[string]attentionItem{}
	for _, item := range items {
		byKind[item.Kind] = item
	}
	if p := byKind["deploy_failed"].Params; p["app"] != "broken" || p["number"] != float64(failed.Number) {
		t.Errorf("deploy_failed params = %v", p)
	}
	if byKind["deploy_failed"].Since.IsZero() || byKind["app_drifted"].Since.IsZero() {
		t.Error("a failed deploy or a drift does not say since when")
	}
	if p := byKind["app_alert"].Params; p["alerts"] != "memory,errors" {
		t.Errorf("app_alert params = %v", p)
	}
	if p := byKind["app_vulnerable"].Params; p["count"] != float64(2) {
		t.Errorf("app_vulnerable params = %v", p)
	}
	if p := byKind["certificate_expiring"].Params; p["count"] != float64(5) {
		t.Errorf("certificate_expiring params = %v, want 5 days, counted as the warnings count", p)
	}
	if p := byKind["backup_missed"].Params; p["target"] != "files / uploads" {
		t.Errorf("a volume is not named with its app: %v", p)
	}
	if item := byKind["deploy_failed"]; item.ProjectName != "acme" || item.EnvironmentName != "production" {
		t.Errorf("an app's item does not say where it lives: %+v", item)
	}
}

// Missed is the question the catch-up after a restart asks, answered the same
// way: a backup started at or after the time it was due was not missed,
// whichever way it ended.
func TestABackupIsMissedOnlyWhenNothingStartedAfterItWasDue(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	due := time.Date(2026, 3, 4, 3, 0, 0, 0, time.UTC)
	scheduled := func(latest store.Backup) store.ScheduledBackup {
		return store.ScheduledBackup{
			Policy:     store.BackupPolicy{TargetType: "database", TargetID: "db_1", Schedule: "0 3 * * *", Enabled: true, UpdatedAt: now.Add(-30 * 24 * time.Hour)},
			TargetName: "orders", Latest: latest,
		}
	}
	judge := func(s store.ScheduledBackup, at time.Time) []attentionItem {
		var got []attentionItem
		backupAttention(s, at, func(item attentionItem) { got = append(got, item) })
		return got
	}

	// The newest backup two days old: last night's was missed, and the item
	// says when it was due.
	got := judge(scheduled(store.Backup{ID: "b1", Status: "succeeded", CreatedAt: now.Add(-48 * time.Hour)}), now)
	if len(got) != 1 || got[0].Kind != attentionBackupMissed || !got[0].Since.Equal(due) {
		t.Fatalf("two days without a backup: %+v", got)
	}
	// Never backed up at all is missed too.
	if got := judge(scheduled(store.Backup{}), now); len(got) != 1 || got[0].Kind != attentionBackupMissed {
		t.Fatalf("never backed up: %+v", got)
	}
	// Taken at 03:00:05, after it was due: not missed.
	if got := judge(scheduled(store.Backup{ID: "b2", Status: "succeeded", CreatedAt: due.Add(5 * time.Second)}), now); len(got) != 0 {
		t.Fatalf("a backup taken on time was called missed: %+v", got)
	}
	// Still running: neither missed nor failed.
	if got := judge(scheduled(store.Backup{ID: "b3", Status: "running", CreatedAt: due.Add(time.Second)}), now); len(got) != 0 {
		t.Fatalf("a backup under way was called a problem: %+v", got)
	}
	// Taken on time and failed: failed, once.
	got = judge(scheduled(store.Backup{ID: "b4", Status: "failed", CreatedAt: due, FinishedAt: due.Add(time.Minute)}), now)
	if len(got) != 1 || got[0].Kind != attentionBackupFailed || !got[0].Since.Equal(due.Add(time.Minute)) {
		t.Fatalf("a failed backup: %+v", got)
	}
	// A database somebody stopped was skipped on purpose.
	if got := judge(scheduled(store.Backup{ID: "b5", Status: "skipped", CreatedAt: due}), now); len(got) != 0 {
		t.Fatalf("a skipped backup was called a problem: %+v", got)
	}
	// Five minutes after it was due, the tick has had no room to be late
	// yet: the night before's backup is the one asked about.
	if got := judge(scheduled(store.Backup{ID: "b6", Status: "succeeded", CreatedAt: due.Add(-24 * time.Hour)}), due.Add(5*time.Minute)); len(got) != 0 {
		t.Fatalf("a backup was called missed while it was still being started: %+v", got)
	}
	// A schedule set this morning was not due at 03:00.
	fresh := scheduled(store.Backup{})
	fresh.Policy.UpdatedAt = now.Add(-time.Hour)
	if got := judge(fresh, now); len(got) != 0 {
		t.Fatalf("a schedule set after its time was called missed: %+v", got)
	}
}

// Each person hears about what they could open: a member limited to a
// project hears nothing of another project, the servers or the team's
// settings; a viewer of the whole team hears about the servers and not the
// team's settings; another team's list is not there at all.
func TestAttentionIsOnlyWhatThePersonMaySee(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	ctx := t.Context()
	secret := h.otherProject(acme, "secret")

	mine := h.app(acme, "mine")
	h.sql(`UPDATE apps SET status = 'unhealthy' WHERE id = ?`, mine.ID)
	theirs := h.app(secret, "theirs")
	h.sql(`UPDATE apps SET status = 'unhealthy' WHERE id = ?`, theirs.ID)
	theirDB := h.database(secret, "their-db")
	if err := h.db.SetDatabaseStatus(ctx, theirDB.ID, "failed", ""); err != nil {
		t.Fatal(err)
	}
	server := h.node(acme, "node-1")
	if err := h.db.SetServerStatus(ctx, server.ID, store.ServerFailed, ""); err != nil {
		t.Fatal(err)
	}

	limited := h.limitedMember(acme, "contractor", store.RoleMember, acme.project.ID)
	items := h.attentionAnswer(limited)
	if len(items) != 1 || items[0].TargetID != mine.ID {
		t.Errorf("a member limited to one project was told about %+v", items)
	}

	viewer := h.newMember(acme, "viewer", store.RoleViewer)
	var kinds []string
	for _, item := range h.attentionAnswer(viewer) {
		kinds = append(kinds, item.Kind+":"+item.Code)
	}
	slices.Sort(kinds)
	if want := []string{"app_unhealthy:app_unhealthy", "app_unhealthy:app_unhealthy",
		"database_unwell:database_failed", "server_not_ready:server_failed"}; !slices.Equal(kinds, want) {
		t.Errorf("a viewer was told %v, want %v", kinds, want)
	}

	// The owner manages the team, so the missing channel is theirs to hear
	// about, and adding one ends it.
	owner := h.attentionAnswer(acme)
	if !slices.ContainsFunc(owner, func(item attentionItem) bool { return item.Kind == attentionNoChannel }) {
		t.Error("the owner of a team with no channel is not told nobody hears about failures")
	}
	h.createChannel(acme, map[string]any{
		"kind": "telegram", "name": "ops",
		"config": map[string]string{"bot_token": fakeBotToken, "chat_id": "-100"},
		"events": []string{"deploy.failed"},
	})
	if slices.ContainsFunc(h.attentionAnswer(acme), func(item attentionItem) bool { return item.Kind == attentionNoChannel }) {
		t.Error("the team has a channel and is still told it has none")
	}

	other := h.newTenant("other")
	if status, _ := h.do(other, http.MethodGet, "/api/teams/"+acme.team.ID+"/attention", nil); status != http.StatusNotFound {
		t.Errorf("another team's list answered %d, want 404", status)
	}
}

// Nothing wrong is an empty list, not a missing one.
func TestAHealthyTeamNeedsNoAttention(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	h.createChannel(acme, map[string]any{
		"kind": "telegram", "name": "ops",
		"config": map[string]string{"bot_token": fakeBotToken, "chat_id": "-100"},
		"events": []string{"deploy.failed"},
	})
	app := h.app(acme, "web")
	h.deployed(app, store.DeploySucceeded, "registry.local/web:1")
	h.sql(`UPDATE apps SET status = 'running' WHERE id = ?`, app.ID)
	h.node(acme, "node-1")

	if items := h.attentionAnswer(acme); len(items) != 0 {
		t.Errorf("a healthy team was told %+v", items)
	}
}

// The panel's own backup is its administrator's to hear about, in whichever
// team they look at; a team's owner who does not run the panel cannot do
// anything about it.
func TestThePanelsOwnBackupIsItsAdministrators(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	backup := store.Backup{TargetType: "panel", TargetID: "panel", Status: "running", Kind: "scheduled"}
	if err := h.db.CreateBackup(t.Context(), &backup); err != nil {
		t.Fatal(err)
	}
	if err := h.db.FinishBackup(t.Context(), backup.ID, "failed", "", 0, "the bucket refused the upload"); err != nil {
		t.Fatal(err)
	}
	has := func(items []attentionItem) bool {
		return slices.ContainsFunc(items, func(item attentionItem) bool { return item.Kind == attentionPanelBackupFailed })
	}
	if has(h.attentionAnswer(acme)) {
		t.Error("a team owner who does not run the panel was told about its backup")
	}
	h.sql(`UPDATE users SET is_admin = 1 WHERE id = ?`, acme.user.ID)
	items := h.attentionAnswer(acme)
	if !has(items) {
		t.Fatalf("the panel's administrator was not told its backup failed: %+v", items)
	}
	for _, item := range items {
		if item.Kind == attentionPanelBackupFailed && item.Path != "/settings?tab=panel" {
			t.Errorf("the panel's backup links to %q", item.Path)
		}
	}
}

// The team's apps, wherever they live, in one answer: each says its project
// and environment and how its newest deploy went, and a member limited to a
// project gets that project's.
func TestTheTeamsAppsComeWithWhereTheyLive(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	second := h.otherProject(acme, "second")
	web := h.app(acme, "web")
	h.deployed(web, store.DeploySucceeded, "registry.local/web:1")
	latest := h.deployed(web, store.DeployFailed, "")
	h.domain(web, "a.example.com")
	h.sql(`UPDATE domains SET auto = 1 WHERE app_id = ?`, web.ID)
	h.domain(web, "shop.example.com")
	api := h.app(second, "api")

	// Another team with the same names has nothing to do with this one.
	other := h.newTenant("other")
	h.app(other, "web")

	list := func(as tenant) []store.TeamApp {
		t.Helper()
		status, body := h.do(as, http.MethodGet, "/api/teams/"+acme.team.ID+"/apps", nil)
		if status != http.StatusOK {
			t.Fatalf("apps answered %d:\n%s", status, body)
		}
		var answer struct {
			Items []store.TeamApp `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &answer); err != nil {
			t.Fatal(err)
		}
		return answer.Items
	}

	apps := list(acme)
	if len(apps) != 2 || apps[0].ID != web.ID || apps[1].ID != api.ID {
		t.Fatalf("the owner got %+v, want web then api", apps)
	}
	if apps[0].ProjectName != "acme" || apps[0].EnvironmentName != "production" ||
		apps[1].ProjectName != "second" || apps[1].EnvironmentID != second.env.ID {
		t.Errorf("an app does not say where it lives: %+v", apps)
	}
	if d := apps[0].LastDeployment; d == nil || d.ID != latest.ID || d.Status != store.DeployFailed {
		t.Errorf("web's newest deployment is %+v, want the failed one", d)
	}
	if apps[0].URL != "https://shop.example.com" {
		t.Errorf("web's address is %q, want the team's own domain over the panel's", apps[0].URL)
	}
	if apps[1].LastDeployment != nil || apps[1].URL != "" {
		t.Errorf("an app never deployed, with no domain, says %+v", apps[1])
	}

	limited := h.limitedMember(acme, "contractor", store.RoleViewer, second.project.ID)
	if apps := list(limited); len(apps) != 1 || apps[0].ID != api.ID {
		t.Errorf("a member limited to the second project got %+v", apps)
	}
}
