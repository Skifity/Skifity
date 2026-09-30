package api

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skifity/internal/cli"
	"skifity/internal/events"
	"skifity/internal/store"
)

// skifity.yaml, applied by the CLI through this API: the two halves against
// each other, so a request the plan makes and the API refuses is a failure
// here rather than on somebody's first `apply`.

const blueprintShop = `
databases:
  main:
    engine: postgres
    version: "17"

apps:
  web:
    repo: https://github.com/acme/shop
    start: npm start
    release: npm run migrate
    port: 3000
    instances: 2
    resources: {cpu: 100, memory: 256}
    variables:
      LOG_LEVEL: info
    secrets: [STRIPE_KEY]
    processes:
      worker: npm run worker
    domains: [shop.example.com]
    schedules:
      nightly: {schedule: "0 3 * * *", command: npm run report}
    databases:
      main: DATABASE_URL
    preview_seed: npm run seed

  cache:
    image: valkey/valkey:8
    port: 6379
    internal: true
`

func TestSkifityYAMLIsAppliedThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	deployer := &recordingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	h.server.Config.Handler = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Databases: &previewDatabaseManager{fakeDatabases: fakeDatabases{log: &recorder{}, db: h.db}, keyring: h.keyring},
		Deployer:  deployer,
	})
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("SKIFITY_URL", h.server.URL)
	t.Setenv("SKIFITY_TOKEN", acme.token)
	path := filepath.Join(t.TempDir(), "skifity.yaml")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(command string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if code := cli.Run(t.Context(), []string{command, "--file", path, "--env", acme.env.ID}, &out, &errs); code != 0 {
			t.Fatalf("%s exited %d:\n%s\n%s", command, code, out.String(), errs.String())
		}
		return out.String()
	}

	write(blueprintShop)
	plan := run("plan")
	for _, want := range []string{"+ database main", "+ app web", "+ app cache", "+ web: link main — as DATABASE_URL",
		"· web: secret STRIPE_KEY", "+ deploy web"} {
		if !strings.Contains(plan, want) {
			t.Errorf("the plan does not say %q:\n%s", want, plan)
		}
	}
	if apps, _ := h.db.ListApps(t.Context(), acme.env.ID); len(apps) != 0 {
		t.Fatal("plan changed something")
	}

	run("apply")
	apps, _ := h.db.ListApps(t.Context(), acme.env.ID)
	byName := map[string]store.App{}
	for _, app := range apps {
		byName[app.Name] = app
	}
	web, cache := byName["web"], byName["cache"]
	if web.StartCommand != "npm start" || web.ReleaseCommand != "npm run migrate" || web.Port != 3000 ||
		web.Replicas != 2 || web.CPURequestM != 100 || web.MemRequestMB != 256 || web.PreviewSeed != "npm run seed" {
		t.Fatalf("web is %+v", web)
	}
	if cache.Image != "valkey/valkey:8" || !cache.Internal || cache.Port != 6379 {
		t.Fatalf("cache is %+v", cache)
	}
	variables, _ := h.db.ListVariables(t.Context(), web.ID)
	keys := map[string]bool{}
	for _, row := range variables {
		keys[row.Key] = row.IsSecret
	}
	if secret, ok := keys["LOG_LEVEL"]; !ok || secret {
		t.Errorf("LOG_LEVEL is %v (present %v); a value in a repository is not a secret", secret, ok)
	}
	if _, ok := keys["DATABASE_URL"]; !ok {
		t.Error("the database was not linked")
	}
	if processes, _ := h.db.ListProcesses(t.Context(), web.ID); len(processes) != 1 || processes[0].Command != "npm run worker" {
		t.Errorf("processes %+v", processes)
	}
	if jobs, _ := h.db.ListAppJobs(t.Context(), web.ID); len(jobs) != 1 || jobs[0].Schedule != "0 3 * * *" {
		t.Errorf("schedules %+v", jobs)
	}
	if domains, _ := h.db.ListDomains(t.Context(), web.ID); len(domains) != 1 || domains[0].Hostname != "shop.example.com" {
		t.Errorf("domains %+v", domains)
	}
	// Both deployed, last, once each.
	if len(deployer.requests) != 2 {
		t.Fatalf("%d deploys", len(deployer.requests))
	}

	// Applied twice is applied once.
	if again := run("plan"); !strings.Contains(again, "Nothing to change") {
		t.Fatalf("a second plan:\n%s", again)
	}

	// A change to the file is a change to that, and nothing else.
	write(strings.Replace(strings.Replace(blueprintShop, "instances: 2", "instances: 3", 1), "LOG_LEVEL: info", "LOG_LEVEL: debug", 1))
	plan = run("plan")
	if !strings.Contains(plan, "~ web: scaling — 2 → 3") || !strings.Contains(plan, `~ web: variable LOG_LEVEL — "info" → "debug"`) ||
		!strings.Contains(plan, "0 to add, 2 to change") {
		t.Fatalf("the plan for a change:\n%s", plan)
	}
	run("apply")
	if app, _ := h.db.GetApp(t.Context(), web.ID); app.Replicas != 3 {
		t.Fatalf("web runs %d", app.Replicas)
	}
	if len(deployer.requests) != 2 {
		t.Fatalf("a change to an existing app deployed it from the file: %d deploys", len(deployer.requests))
	}
}

func TestSkifityYAMLIsAMembersToApply(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "auditor", store.RoleViewer)
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("SKIFITY_URL", h.server.URL)
	t.Setenv("SKIFITY_TOKEN", viewer.token)
	path := filepath.Join(t.TempDir(), "skifity.yaml")
	if err := os.WriteFile(path, []byte("apps:\n  cache:\n    image: valkey/valkey:8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	// A viewer can see the plan: it only reads.
	if code := cli.Run(t.Context(), []string{"plan", "--file", path, "--env", acme.env.ID}, &out, &errs); code != 0 {
		t.Fatalf("a viewer's plan exited %d: %s", code, errs.String())
	}
	if code := cli.Run(t.Context(), []string{"apply", "--file", path, "--env", acme.env.ID}, &out, &errs); code == 0 {
		t.Fatal("a viewer applied a blueprint")
	}
	if apps, _ := h.db.ListApps(t.Context(), acme.env.ID); len(apps) != 0 {
		t.Fatal("a viewer's apply made an app")
	}
}
