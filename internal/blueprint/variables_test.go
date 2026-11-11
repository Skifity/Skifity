package blueprint

import (
	"strings"
	"testing"
	"time"

	"skifity/internal/store"
)

// Which variables the build reads, and where secrets come from: said in the
// file, sent only when said, and a secret the file lists is one it needs.

func TestAVariableIsReadInEitherForm(t *testing.T) {
	file := mustParse(t, `
apps:
  web:
    repo: https://github.com/acme/shop
    variables:
      LOG_LEVEL: info
      NEXT_PUBLIC_API: {value: https://api.example.com, build: true}
      FEATURE: {value: "on", build: false}
`)
	variables := file.Apps["web"].Variables
	if v := variables["LOG_LEVEL"]; v.Value != "info" || v.Build != nil {
		t.Fatalf("the short form read as %+v", v)
	}
	if v := variables["NEXT_PUBLIC_API"]; v.Value != "https://api.example.com" || v.Build == nil || !*v.Build {
		t.Fatalf("the long form read as %+v", v)
	}
	if v := variables["FEATURE"]; v.Value != "on" || v.Build == nil || *v.Build {
		t.Fatalf("build: false read as %+v", v)
	}

	for text, want := range map[string]string{
		// A typo in the long form is an error, not a setting ignored.
		"{value: x, biuld: true}": `"biuld"`,
		"{build: true}":           "no value",
		// YAML's yes is not the word yes.
		"yes":  "put it in quotes",
		"3000": "put it in quotes",
	} {
		_, err := Parse([]byte("apps:\n  web:\n    image: nginx\n    variables:\n      KEY: " + text + "\n"))
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "KEY") {
			t.Errorf("%s: %v", text, err)
		}
	}
}

func TestASecretIsReadInEitherForm(t *testing.T) {
	file := mustParse(t, "apps:\n  web:\n    image: nginx\n    secrets: [STRIPE_KEY, SESSION_SECRET]\n")
	if secrets := file.Apps["web"].Secrets; len(secrets) != 2 || secrets["STRIPE_KEY"] != (Secret{}) {
		t.Fatalf("the list read as %+v", secrets)
	}
	file = mustParse(t, `
apps:
  web:
    image: nginx
    secrets:
      STRIPE_KEY: {from: " company-vault:shop#stripe_key ", build: true}
      SENTRY_DSN: {optional: true}
      SESSION_SECRET:
`)
	secrets := file.Apps["web"].Secrets
	if s := secrets["STRIPE_KEY"]; s.From != "company-vault:shop#stripe_key" || s.Build == nil || !*s.Build {
		t.Fatalf("a reference read as %+v", s)
	}
	if !secrets["SENTRY_DSN"].Optional || secrets["SESSION_SECRET"] != (Secret{}) {
		t.Fatalf("the map read as %+v", secrets)
	}

	for text, want := range map[string]string{
		"{form: vault:x}":                    `"form"`,
		"{from: no-colon}":                   "not a reference",
		"{from: vault:shop#}":                "no key after it",
		"{from: vault:shop, optional: true}": "optional is for one set by hand",
	} {
		_, err := Parse([]byte("apps:\n  web:\n    image: nginx\n    secrets:\n      KEY: " + text + "\n"))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", text, err)
		}
	}
}

// variableBatch is what a plan sends for an app's variables, by key.
func variableBatch(t *testing.T, steps []Step, app string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, step := range steps {
		if step.App != app || step.Call == nil || !strings.HasSuffix(step.Call.Path, "/variables/batch") {
			continue
		}
		for _, item := range step.Call.Body["set"].([]map[string]any) {
			out[item["key"].(string)] = item
		}
	}
	return out
}

func TestTheBuildIsSentOnlyWhenTheFileSaysIt(t *testing.T) {
	file := mustParse(t, `
apps:
  web:
    repo: https://github.com/acme/shop
    variables:
      LOG_LEVEL: info
      NEXT_PUBLIC_API: {value: https://api.example.com, build: true}
`)
	steps, err := Plan(file, State{EnvironmentID: "env_1"})
	if err != nil {
		t.Fatal(err)
	}
	sent := variableBatch(t, steps, "web")
	if sent["NEXT_PUBLIC_API"]["build_time"] != true {
		t.Errorf("a new build variable is sent as %+v", sent["NEXT_PUBLIC_API"])
	}
	// Left out, and not sent as false: the panel keeps what a variable is.
	if _, said := sent["LOG_LEVEL"]["build_time"]; said {
		t.Errorf("a variable that says nothing of the build is sent as %+v", sent["LOG_LEVEL"])
	}
}

// existingWeb is an app that runs: built at a time, with a runtime and a
// build-time variable.
func existingWeb() (State, time.Time) {
	built := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	return State{EnvironmentID: "env_1", Apps: []AppState{{
		App: store.App{ID: "app_web", Name: "web", Slug: "web", SourceType: "git", RepoURL: "https://github.com/acme/shop"},
		Variables: []store.Variable{
			{Key: "LOG_LEVEL", Value: "info", UpdatedAt: built.Add(-time.Hour)},
			{Key: "NEXT_PUBLIC_API", Value: "https://old.example.com", BuildTime: true, UpdatedAt: built.Add(-time.Hour)},
		},
		Deployed: true,
		Running:  &store.Deployment{Status: store.DeploySucceeded, CreatedAt: built},
	}}}, built
}

func TestOnlyAValueTheBuildReadsIsBuiltAgain(t *testing.T) {
	state, _ := existingWeb()
	app := "apps:\n  web:\n    repo: https://github.com/acme/shop\n    variables:\n"

	// A runtime value: rolled out by the batch, never built.
	if deploys := plannedDeploys(t, app+"      LOG_LEVEL: debug\n      NEXT_PUBLIC_API: https://old.example.com\n", state); deploys != "" {
		t.Fatalf("a runtime value planned deploys for %q", deploys)
	}
	// A value the build reads, in the short form: it stays one, and is built.
	file := app + "      LOG_LEVEL: info\n      NEXT_PUBLIC_API: https://new.example.com\n"
	if deploys := plannedDeploys(t, file, state); deploys != "web" {
		t.Fatalf("a new build value planned deploys for %q", deploys)
	}
	steps, _ := Plan(mustParse(t, file), state)
	if _, said := variableBatch(t, steps, "web")["NEXT_PUBLIC_API"]["build_time"]; said {
		t.Fatal("a build variable the file gives a value only was sent with build_time")
	}

	// A runtime value the file says the build reads: built.
	file = app + "      LOG_LEVEL: {value: info, build: true}\n      NEXT_PUBLIC_API: https://old.example.com\n"
	steps, _ = Plan(mustParse(t, file), state)
	var detail string
	for _, step := range steps {
		if step.Kind == "variable" {
			detail = step.Detail
		}
	}
	if detail != "build false → true" || variableBatch(t, steps, "web")["LOG_LEVEL"]["build_time"] != true {
		t.Fatalf("moving a variable into the build planned %q and %+v", detail, variableBatch(t, steps, "web"))
	}
	if deploys := plannedDeploys(t, file, state); deploys != "web" {
		t.Fatalf("a value moved into the build planned deploys for %q", deploys)
	}
	// And out of it: rolled out, as the same change in the panel is.
	file = app + "      LOG_LEVEL: info\n      NEXT_PUBLIC_API: {value: https://old.example.com, build: false}\n"
	if deploys := plannedDeploys(t, file, state); deploys != "" {
		t.Fatalf("a value moved out of the build planned deploys for %q", deploys)
	}
	// A new build variable on an app that runs needs a build to be in it.
	file = app + "      VITE_TITLE: {value: Shop, build: true}\n"
	if deploys := plannedDeploys(t, file, state); deploys != "web" {
		t.Fatalf("a new build variable planned deploys for %q", deploys)
	}
}

func TestASecretFromASecretManagerIsAReference(t *testing.T) {
	file := `
apps:
  web:
    repo: https://github.com/acme/shop
    secrets:
      STRIPE_KEY: {from: company-vault:shop#stripe_key}
`
	steps, err := Plan(mustParse(t, file), State{EnvironmentID: "env_1"})
	if err != nil {
		t.Fatal(err)
	}
	item, ok := variableBatch(t, steps, "web")["STRIPE_KEY"]
	if !ok {
		t.Fatalf("no reference was planned: %+v", steps)
	}
	if _, value := item["value"]; value || item["from"] == nil {
		t.Fatalf("a reference is sent as %+v", item)
	}
	from := item["from"].(map[string]any)
	if from["connection"] != "company-vault" || from["path"] != "shop" || from["key"] != "stripe_key" {
		t.Fatalf("the reference is %+v", from)
	}
	if _, said := item["build_time"]; said {
		t.Fatalf("a reference that says nothing of the build is sent as %+v", item)
	}
	if deploys := plannedDeploys(t, file, State{EnvironmentID: "env_1"}); deploys != "web" {
		t.Fatalf("an app with its secrets from a manager is not deployed: %q", deploys)
	}

	// Once the app reads it from there, there is nothing to do; read from
	// somewhere else, it is changed, and built when the build reads it.
	state, _ := existingWeb()
	state.Apps[0].Variables = append(state.Apps[0].Variables, store.Variable{Key: "STRIPE_KEY", IsSecret: true, BuildTime: true,
		Reference: &store.SecretReference{ConnectionID: "sm_1", Connection: "company-vault", Path: "shop", Key: "stripe_key"}})
	if steps := planned(t, file, state); len(steps) != 0 {
		t.Fatalf("a reference the app has planned %+v", steps)
	}
	moved := strings.Replace(file, "shop#stripe_key", "shop#stripe_live", 1)
	steps = planned(t, moved, state)
	if len(steps) != 1 || steps[0].Op != "change" || !strings.Contains(steps[0].Detail, "→ company-vault:shop#stripe_live") {
		t.Fatalf("a moved reference planned %+v", steps)
	}
	if deploys := plannedDeploys(t, moved, state); deploys != "web" {
		t.Fatalf("a moved reference the build reads planned deploys for %q", deploys)
	}
}

func TestASecretTheFileListsIsRequired(t *testing.T) {
	state, _ := existingWeb()
	file := `
apps:
  web:
    repo: https://github.com/acme/shop
    secrets:
      STRIPE_KEY: {}
      SENTRY_DSN: {optional: true}
`
	steps, err := Plan(mustParse(t, file), state)
	if err != nil {
		t.Fatal(err)
	}
	missing, blocking := Missing(steps)
	if len(missing) != 1 || missing[0].Name != "STRIPE_KEY" || len(blocking) != 1 || missing[0].Call != nil {
		t.Fatalf("missing %+v, blocking %+v", missing, blocking)
	}
	if !strings.Contains(missing[0].Detail, "--app app_web") {
		t.Errorf("the step does not say how to set it: %s", missing[0].Detail)
	}
	var note *Step
	for i, step := range steps {
		if step.Name == "SENTRY_DSN" {
			note = &steps[i]
		}
	}
	if note == nil || note.Op != "note" || note.Blocks {
		t.Fatalf("an optional secret planned %+v", note)
	}

	// An app the file makes cannot have it yet: it is made, nothing blocks,
	// and its first deploy waits.
	steps, err = Plan(mustParse(t, file), State{EnvironmentID: "env_1"})
	if err != nil {
		t.Fatal(err)
	}
	missing, blocking = Missing(steps)
	if len(missing) != 1 || len(blocking) != 0 {
		t.Fatalf("for a new app: missing %+v, blocking %+v", missing, blocking)
	}
	for _, step := range steps {
		if step.Kind == "deploy" && (step.Op != "note" || !strings.Contains(step.Detail, "STRIPE_KEY")) {
			t.Fatalf("a new app missing a secret is deployed: %+v", step)
		}
	}
	if steps[0].Op != "create" || steps[0].Kind != "app" {
		t.Fatalf("the app is not made: %+v", steps)
	}

	// Set, it is nothing to do, whether it is a secret or a plain value.
	state.Apps[0].Variables = append(state.Apps[0].Variables, store.Variable{Key: "STRIPE_KEY", IsSecret: true})
	if steps := planned(t, file, state); len(steps) != 0 {
		t.Fatalf("a secret that is set planned %+v", steps)
	}
}
