package api

import (
	"bytes"
	"context"
	"encoding/json"
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

// storingDeployer records a deployment the way the real one does, so a plan
// after an apply knows the apps it deployed were.
type storingDeployer struct {
	recordingDeployer
	db *store.DB
}

func (d *storingDeployer) Deploy(ctx context.Context, req DeployRequest) (store.Deployment, error) {
	d.requests = append(d.requests, req)
	deployment := store.Deployment{AppID: req.AppID, Trigger: "manual"}
	err := d.db.CreateDeployment(ctx, &deployment)
	return deployment, err
}

// blueprintRun is a panel with a database manager and a deployer that keeps
// its deployments, and the CLI pointed at it with one tenant's token.
type blueprintRun struct {
	h        *harness
	acme     tenant
	deployer *storingDeployer
	path     string
}

func newBlueprintRun(t *testing.T) blueprintRun {
	t.Helper()
	h := newHarness(t)
	acme := h.newTenant("acme")
	deployer := &storingDeployer{recordingDeployer: recordingDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}, db: h.db}
	h.server.Config.Handler = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Databases: &previewDatabaseManager{fakeDatabases: fakeDatabases{log: &recorder{}, db: h.db}, keyring: h.keyring},
		Deployer:  deployer,
	})
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("SKIFITY_URL", h.server.URL)
	t.Setenv("SKIFITY_TOKEN", acme.token)
	return blueprintRun{h: h, acme: acme, deployer: deployer, path: filepath.Join(t.TempDir(), "skifity.yaml")}
}

func (b blueprintRun) write(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(b.path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cli runs a command and answers what it printed, what it printed as
// errors, and its exit code.
func (b blueprintRun) cli(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := cli.Run(t.Context(), append(args, "--file", b.path, "--env", b.acme.env.ID), &out, &errs)
	return out.String(), errs.String(), code
}

func (b blueprintRun) run(t *testing.T, command string) string {
	t.Helper()
	out, errs, code := b.cli(t, command)
	if code != 0 {
		t.Fatalf("%s exited %d:\n%s\n%s", command, code, out, errs)
	}
	return out
}

func TestSkifityYAMLIsAppliedThroughTheAPI(t *testing.T) {
	b := newBlueprintRun(t)
	h, acme, deployer := b.h, b.acme, b.deployer
	write := func(text string) { t.Helper(); b.write(t, text) }
	run := func(command string) string { t.Helper(); return b.run(t, command) }

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

func TestAFileThatMatchesStaysMatched(t *testing.T) {
	// What the panel stores is not always what the file says: a schedule in
	// its canonical form, a hostname lowercased, a root without its slash, a
	// command without its trailing newline. None of that is a change.
	b := newBlueprintRun(t)
	b.write(t, `
apps:
  web:
    repo: https://github.com/acme/shop
    root: /apps/web
    start: |
      npm start
    domains: [Shop.Example.com.]
    schedules:
      weekdays: {schedule: "0 9 * * 1-5", command: "npm run report  "}
`)
	b.run(t, "apply")
	if again := b.run(t, "plan"); !strings.Contains(again, "Nothing to change") {
		t.Fatalf("a plan straight after an apply:\n%s", again)
	}

	// A schedule changed from the file keeps its name, and stays switched off
	// when somebody switched it off.
	apps, _ := b.h.db.ListApps(t.Context(), b.acme.env.ID)
	web := apps[0]
	jobs, _ := b.h.db.ListAppJobs(t.Context(), web.ID)
	jobs[0].Enabled = false
	if err := b.h.db.UpdateAppJob(t.Context(), &jobs[0]); err != nil {
		t.Fatal(err)
	}
	b.write(t, `
apps:
  web:
    repo: https://github.com/acme/shop.git
    root: apps/web
    schedules:
      weekdays: {schedule: "30 9 * * 1-5", command: npm run report}
`)
	b.run(t, "apply")
	jobs, _ = b.h.db.ListAppJobs(t.Context(), web.ID)
	if jobs[0].Name != "weekdays" || jobs[0].Enabled || !strings.HasPrefix(jobs[0].Schedule, "30 9 ") {
		t.Fatalf("the schedule became %+v", jobs[0])
	}
}

func TestAnApplyKeepsWhatTheFileDoesNotSay(t *testing.T) {
	b := newBlueprintRun(t)
	b.write(t, "apps:\n  web:\n    repo: https://github.com/acme/shop\n    variables: {NEXT_PUBLIC_API: https://old.example.com}\n")
	b.run(t, "apply")
	apps, _ := b.h.db.ListApps(t.Context(), b.acme.env.ID)
	web := apps[0]

	// Read by the build, as somebody marked it in the panel.
	if status, body := b.h.do(b.acme, "POST", "/api/apps/"+web.ID+"/variables/batch", map[string]any{
		"set": []map[string]any{{"key": "NEXT_PUBLIC_API", "value": "https://old.example.com", "is_secret": false, "build_time": true}},
	}); status >= 400 {
		t.Fatalf("marking it build-time answered %d: %s", status, body)
	}
	b.write(t, "apps:\n  web:\n    repo: https://github.com/acme/shop\n    variables: {NEXT_PUBLIC_API: https://new.example.com}\n")
	b.run(t, "apply")
	variables, _ := b.h.db.ListVariables(t.Context(), web.ID)
	for _, variable := range variables {
		if variable.Key != "NEXT_PUBLIC_API" {
			continue
		}
		value, err := b.h.keyring.Open(variable.Sealed, variableContext(web.ID, variable.Key))
		if err != nil || !variable.BuildTime || string(value) != "https://new.example.com" {
			t.Fatalf("a new value from the file made the variable %q, build-time %v (%v)", value, variable.BuildTime, err)
		}
	}
}

func TestAnImageChangedInTheFileIsDeployed(t *testing.T) {
	b := newBlueprintRun(t)
	b.write(t, "apps:\n  cache:\n    image: valkey/valkey:8\n")
	b.run(t, "apply")
	b.write(t, "apps:\n  cache:\n    image: valkey/valkey:8.1\n")
	plan := b.run(t, "plan")
	if !strings.Contains(plan, "~ deploy cache") {
		t.Fatalf("a new image is not deployed:\n%s", plan)
	}
	b.run(t, "apply")
	if len(b.deployer.requests) != 2 {
		t.Fatalf("%d deploys; the new image was not deployed", len(b.deployer.requests))
	}
}

func TestAnApplyThatStoppedIsFinishedByTheNext(t *testing.T) {
	// The app is made, and then the apply stops before its first deploy —
	// here on a schedule a member may not write. The next plan still owes it
	// that deploy, rather than finding nothing to change.
	b := newBlueprintRun(t)
	member := b.h.newMember(b.acme, "dev", store.RoleMember)
	t.Setenv("SKIFITY_TOKEN", member.token)
	b.write(t, `
apps:
  web:
    repo: https://github.com/acme/shop
    variables: {A: "1", B: "2"}
    schedules:
      nightly: {schedule: "0 3 * * *", command: npm run report}
`)
	out, _, code := b.cli(t, "apply", "--json")
	if code == 0 {
		t.Fatalf("a member's apply of a schedule succeeded:\n%s", out)
	}
	var answer struct {
		Applied []struct{ Kind, Name string } `json:"applied"`
		Create  int                           `json:"create"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	// The app and both of its variables, sent as one request, are what was
	// done; the counts are of that, not of the plan.
	if len(answer.Applied) != 3 || answer.Create != 3 {
		t.Fatalf("applied %+v, counted %d", answer.Applied, answer.Create)
	}
	if len(b.deployer.requests) != 0 {
		t.Fatal("an app was deployed before what the file gives it was in place")
	}

	t.Setenv("SKIFITY_TOKEN", b.acme.token)
	if plan := b.run(t, "plan"); !strings.Contains(plan, "+ deploy web") || !strings.Contains(plan, "+ web: schedule nightly") {
		t.Fatalf("the plan after a stopped apply:\n%s", plan)
	}
	b.run(t, "apply")
	if len(b.deployer.requests) != 1 {
		t.Fatalf("%d deploys after finishing", len(b.deployer.requests))
	}
}
