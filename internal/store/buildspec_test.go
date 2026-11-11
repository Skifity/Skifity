package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"skifity/internal/kube"
)

// The spec is what tells "waiting for a deploy" from "already running", so it
// has to name what changed, nothing that did not, and never a variable's
// value or its digest.
func TestBuildChangesNameWhatTheLiveVersionWasNotBuiltWith(t *testing.T) {
	app := App{SourceType: "git", RepoURL: "https://github.com/acme/web", Branch: "main", BuildCommand: "npm run build"}
	live := BuildSpecOf(app, map[string]string{"API_URL": "digest-1", "SENTRY_DSN": "digest-2"})

	if changes := BuildChanges(live, live); len(changes) != 0 {
		t.Fatalf("a spec compared with itself changed: %+v", changes)
	}

	app.Branch, app.BuildCommand = "develop", "npm run build:prod"
	app.Port = 3000 // runtime: never a build change
	current := BuildSpecOf(app, map[string]string{"API_URL": "digest-3", "NEW_FLAG": "digest-4"})
	got := BuildChanges(live, current)
	want := []BuildChange{
		{Setting: "branch", From: "main", To: "develop"},
		{Setting: "build_command", From: "npm run build", To: "npm run build:prod"},
		{Setting: "variable", Key: "API_URL"},
		{Setting: "variable", Key: "NEW_FLAG"},
		{Setting: "variable", Key: "SENTRY_DSN"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the changes are %+v, want %+v", got, want)
	}
	encoded, _ := json.Marshal(got)
	for _, digest := range []string{"digest-1", "digest-2", "digest-3", "digest-4"} {
		if strings.Contains(string(encoded), digest) {
			t.Errorf("the changes carry a variable's digest: %s", encoded)
		}
	}
}

// An image app is its image, and a folder has no repository: neither is told
// it is waiting for a branch nobody can set.
func TestABuildSpecHoldsWhatTheSourceBuildsWith(t *testing.T) {
	image := App{SourceType: "image", Image: "valkey/valkey:8", Branch: "main", BuildCommand: "x"}
	if spec := BuildSpecOf(image, map[string]string{"A": "1"}); !reflect.DeepEqual(spec, BuildSpec{Image: "valkey/valkey:8"}) {
		t.Errorf("an image app's spec is %+v", spec)
	}
	moved := image
	moved.Image = "valkey/valkey:8.1"
	if got := BuildChanges(BuildSpecOf(image, nil), BuildSpecOf(moved, nil)); len(got) != 1 || got[0].Setting != "image" {
		t.Errorf("a new image is %+v", got)
	}

	upload := App{SourceType: "upload", RepoURL: "ignored", Branch: "ignored", Builder: "auto"}
	if spec := BuildSpecOf(upload, nil); spec.RepoURL != "" || spec.Branch != "" || spec.Builder != "auto" {
		t.Errorf("a folder's spec is %+v", spec)
	}
	// No builder is the same choice as auto.
	unset := upload
	unset.Builder = ""
	if got := BuildChanges(BuildSpecOf(upload, nil), BuildSpecOf(unset, nil)); len(got) != 0 {
		t.Errorf("no builder and auto differ: %+v", got)
	}
}

// Every setting kube.BuildInputs names is one the spec records, so a new build
// input cannot be left out of what is compared.
func TestTheBuildSpecRecordsEveryBuildInput(t *testing.T) {
	for _, input := range kube.BuildInputs {
		if input.Setting == "variable" {
			continue
		}
		if _, ok := (BuildSpec{}).setting(input.Setting); !ok {
			t.Errorf("BuildSpec does not record %s", input.Setting)
		}
	}
}

// The spec goes in with the deployment and comes out with it; the API never
// answers it.
func TestADeploymentKeepsItsBuildSpec(t *testing.T) {
	db := testDB(t)
	_, _, _, env := seedTeam(t, db)
	app := App{EnvironmentID: env.ID, Name: "web", Slug: "web"}
	if err := db.CreateApp(t.Context(), &app); err != nil {
		t.Fatal(err)
	}
	deployment := Deployment{AppID: app.ID, BuildSpec: "SKF1-sealed"}
	if err := db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatal(err)
	}
	read, err := db.GetDeployment(t.Context(), deployment.ID)
	if err != nil || read.BuildSpec != "SKF1-sealed" {
		t.Fatalf("read back %q, %v", read.BuildSpec, err)
	}
	if encoded, _ := json.Marshal(read); strings.Contains(string(encoded), "SKF1-sealed") {
		t.Errorf("a deployment's JSON carries its build spec: %s", encoded)
	}
}
