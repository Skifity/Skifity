package blueprint

import (
	"errors"
	"strings"
	"testing"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

const shop = `
databases:
  main:
    engine: postgres
    version: "17"
    storage: 10

apps:
  web:
    repo: https://github.com/acme/shop
    root: apps/web
    start: npm start
    release: npm run migrate
    port: 3000
    health: /healthz
    watch: [apps/web/**, packages/**]
    instances: 2
    resources: {cpu: 100, memory: 256}
    variables:
      LOG_LEVEL: info
    secrets: [STRIPE_KEY]
    processes:
      worker: npm run worker
      clock: {command: npm run clock, instances: 0}
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

func mustParse(t *testing.T, text string) File {
	t.Helper()
	file, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return file
}

func TestAFileIsReadStrictly(t *testing.T) {
	file := mustParse(t, shop)
	web := file.Apps["web"]
	if web.Processes["worker"].Command != "npm run worker" || web.Processes["worker"].Instances != nil {
		t.Fatalf("the short form of a process read as %+v", web.Processes["worker"])
	}
	if clock := web.Processes["clock"]; clock.Command != "npm run clock" || clock.Instances == nil || *clock.Instances != 0 {
		t.Fatalf("the long form of a process read as %+v", clock)
	}

	// A typo is an error, not a setting silently ignored.
	_, err := Parse([]byte("apps:\n  web:\n    repo: https://github.com/acme/shop\n    stat: npm start\n"))
	if err == nil || !strings.Contains(err.Error(), `"stat"`) {
		t.Fatalf("a misspelt field: %v", err)
	}

	// Every problem at once.
	_, err = Parse([]byte(`
databases:
  main: {engine: oracle}
apps:
  web:
    repo: https://github.com/acme/shop
    image: nginx
    port: 70000
    instances: 2
    autoscale: {min: 1, max: 3, cpu: 70}
    variables: {STRIPE_KEY: sk_live, "bad key": x}
    secrets: [STRIPE_KEY]
    processes: {Web: run}
`))
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "blueprint.invalid" {
		t.Fatalf("a wrong file: %v", err)
	}
	for _, want := range []string{"oracle", "not both", "70000", "instances or autoscale", "bad key",
		"STRIPE_KEY is listed as a secret", `"Web" cannot be a process`} {
		if !strings.Contains(problem.Cause, want) {
			t.Errorf("the problems do not mention %q: %s", want, problem.Cause)
		}
	}
}

func TestAnEmptyEnvironmentIsBuiltInOrder(t *testing.T) {
	steps, err := Plan(mustParse(t, shop), State{EnvironmentID: "env_1"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, step := range steps {
		order = append(order, step.Op+" "+step.Kind+" "+step.Name)
	}
	joined := strings.Join(order, "\n")

	// The database before the app linked to it, the app before anything of
	// its own, and the first deploys last.
	before := func(a, b string) {
		t.Helper()
		i, j := strings.Index(joined, a), strings.Index(joined, b)
		if i < 0 || j < 0 || i > j {
			t.Errorf("%q is not before %q in\n%s", a, b, joined)
		}
	}
	before("create database main", "create app web")
	before("create app web", "create link main")
	before("create app web", "create variable LOG_LEVEL")
	before("create link main", "create deploy web")
	before("create schedule nightly", "create deploy web")
	if !strings.HasSuffix(joined, "create deploy web") && !strings.HasSuffix(joined, "create deploy cache") {
		t.Errorf("the deploys are not last:\n%s", joined)
	}
	if !strings.Contains(joined, "note secret STRIPE_KEY") {
		t.Errorf("a secret the file names and nobody set is not said:\n%s", joined)
	}

	// A new app is made without deploying, and its references are to what
	// the plan makes before it.
	for _, step := range steps {
		if step.Kind == "app" && step.Name == "web" {
			if step.Call.Body["deploy"] != false || step.Call.Body["root_dir"] != "apps/web" ||
				step.Call.Body["watch_paths"] != "apps/web/**\npackages/**" || step.Call.Creates != "app:web" {
				t.Errorf("web is made with %+v", step.Call)
			}
		}
		if step.Kind == "link" {
			if step.Call.Path != "/api/databases/{database:main}/link" || step.Call.Body["app_id"] != "{app:web}" {
				t.Errorf("the link is %+v", step.Call)
			}
		}
		if step.Kind == "variable" && step.Call != nil {
			set := step.Call.Body["set"].([]map[string]any)
			if len(set) != 1 || set[0]["is_secret"] != false {
				t.Errorf("the variables are sent as %+v", set)
			}
		}
	}
}

func TestAnEnvironmentThatMatchesHasNothingToDo(t *testing.T) {
	file := mustParse(t, shop)
	state := matching()
	steps, err := Plan(file, state)
	if err != nil {
		t.Fatal(err)
	}
	create, change, _ := Counts(steps)
	if create != 0 || change != 0 {
		t.Fatalf("a matching environment plans %d creates and %d changes: %+v", create, change, steps)
	}

	// Something here that is not in the file is said, and left.
	state.Apps = append(state.Apps, AppState{App: store.App{ID: "app_old", Name: "legacy", Slug: "legacy"}})
	steps, _ = Plan(file, state)
	if len(steps) == 0 || steps[len(steps)-1].Op != "note" || steps[len(steps)-1].Name != "legacy" || steps[len(steps)-1].Call != nil {
		t.Fatalf("an app not in the file: %+v", steps)
	}
}

func TestOnlyWhatDiffersIsChanged(t *testing.T) {
	state := matching()
	web := &state.Apps[0]
	web.App.StartCommand = "node server.js"
	web.App.Replicas = 1
	web.Variables[0].Value = "debug"
	web.Processes[0].Command = "node worker.js"
	web.Jobs[0].Schedule = "0 4 * * *"

	steps, err := Plan(mustParse(t, shop), state)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Step{}
	for _, step := range steps {
		if step.Op != "note" {
			got[step.Kind] = step
		}
	}
	if len(got) != 5 {
		t.Fatalf("%d kinds of change: %+v", len(got), steps)
	}
	if s := got["settings"]; len(s.Call.Body) != 1 || s.Call.Body["start_command"] != "npm start" || s.Call.Path != "/api/apps/app_web" {
		t.Errorf("settings: %+v", s.Call)
	}
	if s := got["scaling"]; s.Call.Body["replicas"] != 2 || s.Detail != "1 → 2" {
		t.Errorf("scaling: %+v %s", s.Call, s.Detail)
	}
	if s := got["process"]; s.Call.Body["command"] != "npm run worker" || s.Call.Body["instances"] != 1 {
		t.Errorf("process: %+v", s.Call)
	}
	if s := got["schedule"]; s.Call.Method != "PATCH" || s.Call.Path != "/api/apps/app_web/jobs/job_1" {
		t.Errorf("schedule: %+v", s.Call)
	}
}

func TestAPlanRefusesWhatItCannotDo(t *testing.T) {
	_, err := Plan(mustParse(t, "apps:\n  web:\n    start: npm start\n"), State{EnvironmentID: "env_1"})
	if err == nil || !strings.Contains(err.Error(), "skifity up") {
		t.Fatalf("an app with no source and nowhere to come from: %v", err)
	}
	_, err = Plan(mustParse(t, "apps:\n  web:\n    image: nginx\n    databases: {orders: DATABASE_URL}\n"), State{EnvironmentID: "env_1"})
	if err == nil || !strings.Contains(err.Error(), "orders") {
		t.Fatalf("a link to a database nobody has: %v", err)
	}
	// An engine is not changed from a file.
	state := matching()
	state.Databases[0].EngineVersion = "16"
	steps, _ := Plan(mustParse(t, shop), state)
	for _, step := range steps {
		if step.Kind == "database" && step.Op != "note" {
			t.Fatalf("a database version was changed from a file: %+v", step)
		}
	}
}

func TestReferencesAreFilledFromWhatWasMade(t *testing.T) {
	call, err := Resolve(Call{Method: "POST", Path: "/api/databases/{database:main}/link",
		Body: map[string]any{"app_id": "{app:web}", "var_name": "DATABASE_URL", "note": "a {brace} in text"}},
		map[string]string{"database:main": "db_1", "app:web": "app_1"})
	if err != nil || call.Path != "/api/databases/db_1/link" || call.Body["app_id"] != "app_1" || call.Body["note"] != "a {brace} in text" {
		t.Fatalf("resolved %+v (%v)", call, err)
	}
	if _, err := Resolve(Call{Path: "/api/apps/{app:web}/deploy"}, map[string]string{}); err == nil {
		t.Fatal("a reference to nothing was sent")
	}
}

func TestANameIsItsOwnSlug(t *testing.T) {
	// my_app and my--app are both my-app to the panel, so a file calling an
	// app that would plan to make it and be refused as taken.
	for _, text := range []string{
		"apps:\n  my_app:\n    image: nginx\n",
		"apps:\n  my--app:\n    image: nginx\n",
		"databases:\n  main: {engine: postgres}\napps:\n  main:\n    image: nginx\n",
		"apps:\n  web:\n    image: nginx\n    git: github\n",
		"apps:\n  web:\n    image: nginx\n    schedules:\n      x: {schedule: every day, command: run}\n",
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("accepted:\n%s", text)
		}
	}
}

func TestWhereAnAppComesFromIsSaidAndNotChanged(t *testing.T) {
	state := matching()
	state.Apps[0].App.RepoURL = "https://github.com/acme/shop.git"
	steps, _ := Plan(mustParse(t, shop), state)
	for _, step := range steps {
		if step.Kind == "source" || step.Kind == "settings" {
			t.Fatalf("a repository that differs by .git: %+v", step)
		}
	}

	state.Apps[0].App.RepoURL = "https://github.com/acme/old-shop"
	state.Apps[1].App.SourceType = "git"
	steps, _ = Plan(mustParse(t, shop), state)
	var notes []string
	for _, step := range steps {
		if step.Call != nil && step.Call.Body["repo_url"] != nil {
			t.Fatalf("a repository was sent in a change the API refuses: %+v", step.Call)
		}
		if step.Kind == "source" && step.Op == "note" {
			notes = append(notes, step.App)
		}
	}
	if strings.Join(notes, " ") != "cache web" {
		t.Fatalf("the source notes are for %v", notes)
	}
}

func TestAnAppOwedItsFirstDeployIsDeployed(t *testing.T) {
	state := matching()
	state.Apps[0].Deployed = false
	state.Apps[1].Deployed = false
	state.Apps[1].App.SourceType = "upload"
	steps, _ := Plan(mustParse(t, shop), state)
	var deployed []string
	for _, step := range steps {
		if step.Kind == "deploy" {
			deployed = append(deployed, step.Name)
		}
	}
	// A folder sent with `skifity up` is deployed by sending it again.
	if strings.Join(deployed, " ") != "web" {
		t.Fatalf("deploys planned for %v", deployed)
	}
}

func TestADatabaseOfAnotherSizeIsSaid(t *testing.T) {
	state := matching()
	state.Databases[0].StorageGB = 20
	steps, _ := Plan(mustParse(t, shop), state)
	for _, step := range steps {
		if step.Kind == "database" {
			if step.Op != "note" || !strings.Contains(step.Detail, "20 GB here and 10 GB in the file") {
				t.Fatalf("a database of another size: %+v", step)
			}
			return
		}
	}
	t.Fatal("a database of another size was not mentioned")
}

func TestAPrivateRepositoryIsReadThroughTheConnectionNamed(t *testing.T) {
	file := mustParse(t, "apps:\n  web:\n    repo: https://github.com/acme/private\n    git: acme-github\n")
	steps, err := Plan(file, State{EnvironmentID: "env_1", GitSources: []store.GitSource{{ID: "gs_1", Name: "acme-github"}}})
	if err != nil || steps[0].Call.Body["git_source_id"] != "gs_1" {
		t.Fatalf("the app is made with %+v (%v)", steps[0].Call, err)
	}
	if _, err := Plan(file, State{EnvironmentID: "env_1"}); err == nil || !strings.Contains(err.Error(), "acme-github") {
		t.Fatalf("a connection the team does not have: %v", err)
	}
}

func TestACommandInBracesIsNotAReference(t *testing.T) {
	call, err := Resolve(Call{Path: "/api/apps/{app:web}/processes/worker",
		Body: map[string]any{"command": "{ npm run a; npm run b; }", "app_id": "{app:web}"}},
		map[string]string{"app:web": "app_1"})
	if err != nil || call.Body["command"] != "{ npm run a; npm run b; }" || call.Body["app_id"] != "app_1" ||
		call.Path != "/api/apps/app_1/processes/worker" {
		t.Fatalf("resolved %+v (%v)", call, err)
	}
}

// matching is an environment that is what shop describes.
func matching() State {
	return State{
		EnvironmentID: "env_1",
		Databases:     []store.Database{{ID: "db_1", Name: "main", Slug: "main", Engine: "postgres", EngineVersion: "17", StorageGB: 10}},
		Apps: []AppState{
			{
				App: store.App{ID: "app_web", Name: "web", Slug: "web", SourceType: "git", RepoURL: "https://github.com/acme/shop",
					RootDir: "apps/web", StartCommand: "npm start", ReleaseCommand: "npm run migrate", Port: 3000,
					HealthPath: "/healthz", WatchPaths: "apps/web/**\npackages/**", Replicas: 2,
					CPURequestM: 100, MemRequestMB: 256, PreviewSeed: "npm run seed"},
				Variables: []store.Variable{{Key: "LOG_LEVEL", Value: "info"}, {Key: "STRIPE_KEY", IsSecret: true}},
				Processes: []store.AppProcess{{Name: "worker", Command: "npm run worker", Instances: 1},
					{Name: "clock", Command: "npm run clock", Instances: 0}},
				Domains:  []store.Domain{{Hostname: "shop.example.com"}, {Hostname: "web-production.203.0.113.10.sslip.io", Auto: true}},
				Jobs:     []store.AppJob{{ID: "job_1", Name: "nightly", Schedule: "0 3 * * *", Command: "npm run report", Enabled: true}},
				Links:    map[string]string{"DATABASE_URL": "db_1"},
				Deployed: true,
			},
			{App: store.App{ID: "app_cache", Name: "cache", Slug: "cache", SourceType: "image", Image: "valkey/valkey:8", Port: 6379, Internal: true},
				Deployed: true},
		},
	}
}

// The file's image was stored and its deploy refused — the app locked, the
// cluster away. The next plan compared the file with the stored image, found
// them equal and planned nothing, and the image never ran.
func TestAnImageStoredButNeverRunIsDeployed(t *testing.T) {
	state := matching()
	cache := &state.Apps[1]
	cache.App.Image = "valkey/valkey:9"
	cache.Running = &store.Deployment{Image: "valkey/valkey:8", Status: store.DeploySucceeded}
	file := strings.Replace(shop, "valkey/valkey:8", "valkey/valkey:9", 1)
	if !strings.Contains(file, "valkey/valkey:9") {
		t.Fatal("the fixture no longer names valkey/valkey:8")
	}
	if deploys := plannedDeploys(t, file, state); deploys != "cache" {
		t.Fatalf("deploys planned for %q", deploys)
	}
	// Not while a deploy of it is still under way.
	cache.Pending = &store.Deployment{Image: "valkey/valkey:9", Status: store.DeployQueued}
	if deploys := plannedDeploys(t, file, state); deploys != "" {
		t.Fatalf("a deploy already on its way was planned again: %q", deploys)
	}
}

// A value the build reads is in the image, so a new one needs a build. It was
// stored and nothing was deployed, and the next plan had nothing to do while
// the image still had the old value baked in.
func TestABuildVariableIsBuiltIn(t *testing.T) {
	state := matching()
	web := &state.Apps[0]
	built := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	web.Running = &store.Deployment{Status: store.DeploySucceeded, CreatedAt: built}
	web.Variables = append(web.Variables, store.Variable{Key: "API_URL", Value: "https://old.example.test", BuildTime: true, UpdatedAt: built.Add(-time.Hour)})

	// Changed by this file: deployed after.
	file := strings.Replace(shop, "LOG_LEVEL: info", "LOG_LEVEL: info\n      API_URL: https://new.example.test", 1)
	if !strings.Contains(file, "API_URL") {
		t.Fatal("the fixture's variables moved")
	}
	if deploys := plannedDeploys(t, file, state); deploys != "web" {
		t.Fatalf("a new build value planned deploys for %q", deploys)
	}

	// Changed by an apply whose deploy did not happen: still owed.
	web.Variables[len(web.Variables)-1].Value = "https://new.example.test"
	web.Variables[len(web.Variables)-1].UpdatedAt = built.Add(time.Hour)
	if deploys := plannedDeploys(t, file, state); deploys != "web" {
		t.Fatalf("a build value changed after the running build planned deploys for %q", deploys)
	}
	// And not once a build that has it is under way.
	web.Pending = &store.Deployment{Status: store.DeployBuilding, CreatedAt: built.Add(2 * time.Hour)}
	if deploys := plannedDeploys(t, file, state); deploys != "" {
		t.Fatalf("a build already on its way was planned again: %q", deploys)
	}
}

func plannedDeploys(t *testing.T, file string, state State) string {
	t.Helper()
	steps, err := Plan(mustParse(t, file), state)
	if err != nil {
		t.Fatal(err)
	}
	var deployed []string
	for _, step := range steps {
		if step.Kind == "deploy" {
			deployed = append(deployed, step.Name)
		}
	}
	return strings.Join(deployed, " ")
}
