package serverapp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"skifity/internal/config"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// seedApp creates the minimum chain a deployment needs to exist: a team, a
// project, an environment and an app.
func seedApp(t *testing.T, db *store.DB) store.App {
	t.Helper()
	ctx := context.Background()

	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatalf("create a team: %v", err)
	}
	project := store.Project{TeamID: team.ID, Name: "Website", Slug: "website"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatalf("create a project: %v", err)
	}
	environment := store.Environment{
		ProjectID: project.ID, Name: "Production", Slug: "production",
		Kind: "standard", Namespace: "t-website-production",
	}
	if err := db.CreateEnvironment(ctx, &environment); err != nil {
		t.Fatalf("create an environment: %v", err)
	}
	app := store.App{
		EnvironmentID: environment.ID, Name: "web", Slug: "web",
		SourceType: "git", RepoURL: "https://example.test/web.git", Status: "created",
	}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatalf("create an app: %v", err)
	}
	return app
}

// The installer knows which pod network it started the cluster with and the
// panel cannot find out any other way. If that never reaches the database, the
// panel installs the second server with its own default, which on a cluster
// that had to use vxlan is the wrong one — and a node on the wrong backend
// joins without error and then reaches nothing.
func TestThePodNetworkTheInstallerChoseIsRecordedOnce(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.Default()
	cfg.PodNetwork = settings.FlannelVXLAN
	if err := seedPodNetwork(ctx, db, cfg, log); err != nil {
		t.Fatalf("seed the pod network: %v", err)
	}
	value, _, err := db.GetSetting(ctx, settings.KeyFlannelBackend)
	if err != nil {
		t.Fatalf("read the setting: %v", err)
	}
	if value != settings.FlannelVXLAN {
		t.Fatalf("the pod network was recorded as %q, want %q", value, settings.FlannelVXLAN)
	}

	// And an operator who changes it afterwards keeps their answer: the panel
	// restarts with the same environment every time, so seeding on every start
	// would undo the change silently.
	if err := db.SetSetting(ctx, settings.KeyFlannelBackend, settings.FlannelWireGuard, false, "someone"); err != nil {
		t.Fatalf("change the setting: %v", err)
	}
	if err := seedPodNetwork(ctx, db, cfg, log); err != nil {
		t.Fatalf("seed the pod network again: %v", err)
	}
	value, _, err = db.GetSetting(ctx, settings.KeyFlannelBackend)
	if err != nil {
		t.Fatalf("read the setting: %v", err)
	}
	if value != settings.FlannelWireGuard {
		t.Fatalf("a restart overwrote the operator's choice with %q", value)
	}
}

// On AWS, Google Cloud, Azure and behind any NAT the node has no external
// address, and the first real cluster this ran on gave its first app no URL at
// all. The installer found the address; the panel records it, once.
func TestThePublicIPTheInstallerFoundIsRecordedOnce(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.Default()
	cfg.PublicIP = "52.188.87.62"
	if err := seedPublicIP(ctx, db, cfg, log); err != nil {
		t.Fatalf("seed the public IP: %v", err)
	}
	if value, _, _ := db.GetSetting(ctx, settings.KeyClusterIP); value != "52.188.87.62" {
		t.Fatalf("the cluster's public IP is %q, want the installer's", value)
	}

	// An operator's own answer stands across restarts.
	if err := db.SetSetting(ctx, settings.KeyClusterIP, "203.0.113.10", false, "someone"); err != nil {
		t.Fatalf("change the setting: %v", err)
	}
	if err := seedPublicIP(ctx, db, cfg, log); err != nil {
		t.Fatalf("seed again: %v", err)
	}
	if value, _, _ := db.GetSetting(ctx, settings.KeyClusterIP); value != "203.0.113.10" {
		t.Fatalf("a restart overwrote the operator's address with %q", value)
	}

	// An IPv6 address goes where IPv6 addresses go, and nothing at all is
	// written when the installer found none.
	cfg.PublicIP = "2001:db8::10"
	if err := seedPublicIP(ctx, db, cfg, log); err != nil {
		t.Fatalf("seed an IPv6 address: %v", err)
	}
	if value, _, _ := db.GetSetting(ctx, settings.KeyClusterIPv6); value != "2001:db8::10" {
		t.Errorf("the IPv6 address was recorded as %q", value)
	}
	cfg.PublicIP = ""
	if err := seedPublicIP(ctx, db, cfg, log); err != nil {
		t.Errorf("an empty address was an error: %v", err)
	}
}

// A pod network the panel does not understand is a typo that would otherwise
// reach a k3s command line on a server, where it fails halfway through adding
// it rather than before the panel starts.
func TestAnUnknownPodNetworkStopsThePanelStarting(t *testing.T) {
	cfg := config.Default()
	cfg.PodNetwork = "wiregaurd-native"
	if err := cfg.Validate(); err == nil {
		t.Fatal("a misspelled pod network was accepted")
	}
	for _, valid := range []string{"", settings.FlannelWireGuard, settings.FlannelVXLAN} {
		cfg.PodNetwork = valid
		if err := cfg.Validate(); err != nil {
			t.Errorf("pod network %q was rejected: %v", valid, err)
		}
	}
}

// TestARestartDoesNotLeaveABackupOrADatabaseInProgressForever.
//
// A deployment and a provisioning operation were both recovered at startup.
// A backup row written as "running" and a database written as "creating" were
// not, and they are the same shape: a row that only the goroutine holding it
// ever finishes. The database is the worse of the two — the backup manager
// refuses a target that is not running, so one stuck at "creating" cannot even
// be backed up, and the only way out was to delete it.
func TestARestartDoesNotLeaveABackupOrADatabaseInProgressForever(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	defer db.Close()

	app := seedApp(t, db)

	creating := store.Database{
		EnvironmentID: app.EnvironmentID, Name: "shop-db", Slug: "shop-db",
		Engine: "postgres", Status: "creating",
	}
	if err := db.CreateDatabase(ctx, &creating); err != nil {
		t.Fatalf("create a database: %v", err)
	}
	healthy := store.Database{
		EnvironmentID: app.EnvironmentID, Name: "blog-db", Slug: "blog-db",
		Engine: "postgres", Status: "running",
	}
	if err := db.CreateDatabase(ctx, &healthy); err != nil {
		t.Fatalf("create a database: %v", err)
	}

	interrupted := store.Backup{TargetType: "database", TargetID: creating.ID, Status: "running"}
	if err := db.CreateBackup(ctx, &interrupted); err != nil {
		t.Fatalf("create a backup: %v", err)
	}
	done := store.Backup{TargetType: "database", TargetID: healthy.ID, Status: "succeeded"}
	if err := db.CreateBackup(ctx, &done); err != nil {
		t.Fatalf("create a backup: %v", err)
	}

	// A template update waiting for its backups, whose goroutine the restart
	// took: left at backing_up, it refused every later update of the app.
	if err := db.RecordAppTemplate(ctx, store.AppTemplate{AppID: app.ID, TemplateID: "budget",
		Service: "app", InstalledImage: "example/app:1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTemplateUpdate(ctx, app.ID, store.TemplateUpdateBackingUp, "example/app:2.0", ""); err != nil {
		t.Fatal(err)
	}

	// Components a restart caught mid-way: one installing, one upgrading.
	for name, status := range map[string]string{"keda": "installing", "longhorn": "upgrading"} {
		if err := db.SetComponent(ctx, store.ClusterComponent{Name: name, Status: status, Version: "1.7.0"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := markInterruptedWork(ctx, db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("markInterruptedWork: %v", err)
	}

	if keda, _ := db.GetComponent(ctx, "keda"); keda.Status != "failed" || keda.Detail == "" {
		t.Errorf("a component being installed at the restart is %+v", keda)
	}
	if longhorn, _ := db.GetComponent(ctx, "longhorn"); longhorn.Status != "installed" || longhorn.Version != "1.7.0" {
		t.Errorf("a component being upgraded at the restart is %+v", longhorn)
	}

	if record, err := db.GetAppTemplate(ctx, app.ID); err != nil || record.UpdateStatus != store.TemplateUpdateFailed ||
		record.UpdateError == "" || record.InstalledImage != "example/app:1.0" {
		t.Errorf("the interrupted template update is %+v (%v)", record, err)
	}

	after, err := db.GetBackup(ctx, interrupted.ID)
	if err != nil {
		t.Fatalf("read back the backup: %v", err)
	}
	if after.Status != "failed" {
		t.Errorf("the interrupted backup is %q, want failed", after.Status)
	}
	if after.ErrorMessage == "" {
		t.Error("the interrupted backup does not say what happened")
	}

	stuck, err := db.GetDatabase(ctx, creating.ID)
	if err != nil {
		t.Fatalf("read back the database: %v", err)
	}
	if stuck.Status != "failed" {
		t.Errorf("the interrupted database is %q, want failed", stuck.Status)
	}
	if stuck.StatusDetail == "" {
		t.Error("the interrupted database does not say what happened")
	}

	// Nothing that had already finished, or was healthy, is touched.
	if b, _ := db.GetBackup(ctx, done.ID); b.Status != "succeeded" {
		t.Errorf("a finished backup became %q", b.Status)
	}
	if d, _ := db.GetDatabase(ctx, healthy.ID); d.Status != "running" {
		t.Errorf("a running database became %q", d.Status)
	}

	// A restart loop must not rewrite history on every pass.
	if err := markInterruptedWork(ctx, db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	left, err := db.ListUnfinishedBackups(ctx)
	if err != nil {
		t.Fatalf("list unfinished backups: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("expected nothing left running, got %d", len(left))
	}
	if being, err := db.ListDatabasesBeingCreated(ctx); err != nil || len(being) != 0 {
		t.Errorf("expected no database left creating, got %d (%v)", len(being), err)
	}
}
