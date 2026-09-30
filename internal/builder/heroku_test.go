package builder

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestAProcfileIsReadLineByLine(t *testing.T) {
	got := ParseProcfile(`# processes
web: bundle exec puma -C config/puma.rb
release:   bin/rails db:migrate
worker: bundle exec sidekiq
not a line
web: a second web line is ignored
clock:
`)
	want := "web=bundle exec puma -C config/puma.rb|release=bin/rails db:migrate|worker=bundle exec sidekiq"
	var parts []string
	for _, p := range got {
		parts = append(parts, p.Name+"="+p.Command)
	}
	if strings.Join(parts, "|") != want {
		t.Fatalf("read %q", strings.Join(parts, "|"))
	}
}

// The web line starts the app, the release line is the release command, and
// the rest are named, since an app runs one process.
func TestAProcfileSaysHowTheAppStartsAndWhatRunsBeforeIt(t *testing.T) {
	tree := Tree{
		Files: []string{"Gemfile", "Procfile"},
		Contents: map[string]string{
			"Gemfile": "gem 'rails'\ngem 'pg'\n",
			"Procfile": "web: bundle exec puma\nrelease: bin/rails db:migrate\nworker: bundle exec sidekiq\n" +
				"Celery_Beat: celery beat\n2fast: run fast\n",
		},
	}
	d := Detect(tree)
	if d.StartCommand != "bundle exec puma" || d.ReleaseCommand != "bin/rails db:migrate" {
		t.Fatalf("start %q, release %q", d.StartCommand, d.ReleaseCommand)
	}
	// Heroku's names are allowed capitals and underscores, and a Kubernetes
	// name is not; one that cannot be made into a name is said, not dropped.
	if len(d.Processes) != 2 || d.Processes[0].Name != "worker" || d.Processes[1].Name != "celery-beat" {
		t.Fatalf("processes %+v", d.Processes)
	}
	notes := strings.Join(d.Notes, " ")
	if !strings.Contains(notes, "also names worker, celery-beat") || !strings.Contains(notes, "Procfile's 2fast line") {
		t.Fatalf("the processes were not mentioned: %v", d.Notes)
	}

	// A Dockerfile's CMD is not overruled by a Procfile left beside it; its
	// release line still counts.
	tree.Files = append(tree.Files, "Dockerfile")
	tree.Contents["Dockerfile"] = "FROM ruby:3.3\nCMD [\"bin/start\"]\n"
	d = Detect(tree)
	if d.StartCommand != "" || d.ReleaseCommand != "bin/rails db:migrate" {
		t.Fatalf("with a Dockerfile: start %q, release %q", d.StartCommand, d.ReleaseCommand)
	}
}

func TestAProcfileAlwaysMakesAnAppThatCanBeCreated(t *testing.T) {
	// Two lines that become one name, and more lines than an app may have:
	// the API refuses the whole app for either, so neither reaches it.
	procfile := "web: run web\nworker_a: run a\nworker-a: run a again\nWorker: run w\nworker: run w again\n"
	for i := range MaxProcesses + 2 {
		procfile += fmt.Sprintf("extra%d: run %d\n", i, i)
	}
	d := Detect(Tree{Files: []string{"Procfile"}, Contents: map[string]string{"Procfile": procfile}})
	if len(d.Processes) != MaxProcesses {
		t.Fatalf("%d processes, want %d", len(d.Processes), MaxProcesses)
	}
	names := map[string]string{}
	for _, process := range d.Processes {
		if earlier, twice := names[process.Name]; twice {
			t.Fatalf("%s is two processes: %q and %q", process.Name, earlier, process.Command)
		}
		names[process.Name] = process.Command
	}
	if names["worker-a"] != "run a" || names["worker"] != "run w" {
		t.Fatalf("the first line of a name did not keep it: %v", names)
	}
	var codes []string
	for _, note := range d.NoteCodes {
		codes = append(codes, note.Code)
	}
	for _, want := range []string{"procfile_same_name", "procfile_over_limit"} {
		if !slices.Contains(codes, want) {
			t.Errorf("nothing says %s: %v", want, d.Notes)
		}
	}
}

func TestAnAppJSONSaysWhatTheAppNeeds(t *testing.T) {
	tree := Tree{
		Files: []string{"package.json", "app.json"},
		Contents: map[string]string{
			"package.json": `{"dependencies": {"express": "^4", "pg": "^8"}}`,
			"app.json": `{
				"name": "shop",
				"env": {
					"SECRET_KEY_BASE": {"description": "Signs the sessions.", "generator": "secret"},
					"WEB_CONCURRENCY": {"description": "Workers per instance.", "value": "2"},
					"STRIPE_KEY": {"description": "From the Stripe dashboard.\nThe live one."},
					"SENTRY_DSN": {"description": "Where errors go.", "required": false},
					"GREETING": "  hello  ",
					"not a name": "x"
				},
				"addons": ["heroku-postgresql:essential-0", {"plan": "heroku-redis:mini", "as": "CACHE"}, "papertrail"],
				"scripts": {"postdeploy": "npm run seed"}
			}`,
		},
	}
	d := Detect(tree)

	var postgres, redis *Need
	for i, need := range d.Needs {
		switch need.Engine {
		case EnginePostgres:
			postgres = &d.Needs[i]
		case EngineRedis:
			redis = &d.Needs[i]
		}
	}
	if postgres == nil || postgres.Source != "app.json" || postgres.Variable != "DATABASE_URL" || !postgres.Provided {
		t.Fatalf("postgres: %+v", postgres)
	}
	if redis == nil || redis.Variable != "CACHE_URL" {
		t.Fatalf("redis, named with as: %+v", redis)
	}
	count := 0
	for _, need := range d.Needs {
		if need.Engine == EnginePostgres {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("postgres is needed %d times: the driver and app.json are one database", count)
	}

	template := d.EnvTemplate
	if !regexp.MustCompile(`(?m)^SECRET_KEY_BASE=[0-9a-f]{64}$`).MatchString(template) {
		t.Errorf("no generated secret in:\n%s", template)
	}
	for _, want := range []string{
		"# Signs the sessions.\n",
		"WEB_CONCURRENCY=2\n",
		`GREETING="  hello  "`,
		"# From the Stripe dashboard.\n# STRIPE_KEY=\n",
		"# SENTRY_DSN= (optional)",
	} {
		if !strings.Contains(template, want) {
			t.Errorf("the template does not have %q:\n%s", want, template)
		}
	}
	if strings.Contains(template, "not a name") {
		t.Errorf("a name no variable can have made it in:\n%s", template)
	}
	var missing []string
	for _, need := range d.Needs {
		if need.Kind == NeedVariables {
			missing = need.Variables
		}
	}
	if strings.Join(missing, ",") != "STRIPE_KEY" {
		t.Errorf("required with no value: %v", missing)
	}
	// Heroku runs postdeploy in each new review app: here, a preview's seed.
	if d.PreviewSeed != "npm run seed" || !strings.Contains(strings.Join(d.Notes, " "), "skifity run -- npm run seed") {
		t.Errorf("the postdeploy script became seed %q and notes %v", d.PreviewSeed, d.Notes)
	}

	// Two detections are two secrets.
	if again := Detect(tree); again.EnvTemplate == template {
		t.Error("the generated secret is the same twice")
	}
}

func TestABrokenAppJSONIsSaidAndIgnored(t *testing.T) {
	d := Detect(Tree{Files: []string{"package.json", "app.json"}, Contents: map[string]string{
		"package.json": `{"dependencies": {"express": "^4"}}`,
		"app.json":     `{"env": `,
	}})
	if d.EnvTemplate != "" || !strings.Contains(strings.Join(d.Notes, " "), "app.json could not be read") {
		t.Fatalf("template %q, notes %v", d.EnvTemplate, d.Notes)
	}
}
