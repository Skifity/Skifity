package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/builder"
	"skifity/internal/kube"
	"skifity/internal/store"
)

const stackCompose = `
services:
  web:
    build:
      context: ./web
      dockerfile: Dockerfile.prod
    ports: ["8080:3000"]
    environment:
      DATABASE_URL: postgres://app:secret@db:5432/app
      REDIS_URL: redis://cache:6379
      LOG_LEVEL: ${LOG_LEVEL:-info}
      API_KEY: ${API_KEY}
    depends_on: [db, cache]
  worker:
    build: ./web
    command: ["npm", "run", "worker"]
    depends_on: [db]
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: secret
    volumes:
      - db-data:/var/lib/postgresql/data
      - ./init.sql:/docker-entrypoint-initdb.d/init.sql
  cache:
    image: redis:7
  Admin_UI:
    image: adminer
    expose: ["8080"]
volumes:
  db-data:
`

func composeServices(t *testing.T) []builder.ComposeService {
	t.Helper()
	services, _, err := builder.ParseCompose(stackCompose)
	if err != nil {
		t.Fatal(err)
	}
	return services
}

// Every service becomes an app, created together, reaching the others by the
// name and port Compose used.
func TestAComposeFileBecomesAStack(t *testing.T) {
	h := newHarness(t)
	deploys := &recorder{}
	h.api.deployer = &fakeDeployer{log: deploys}
	acme := h.newTenant("acme")

	status, body := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/stack", map[string]any{
		"repo_url": "https://github.com/acme/shop", "branch": "main", "root_dir": "deploy",
		"services": composeServices(t), "deploy": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("answered %d: %s", status, truncate(body, 300))
	}
	var answer struct {
		Apps  []store.App `json:"apps"`
		Notes []stackNote `json:"notes"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	apps := map[string]store.App{}
	for _, app := range answer.Apps {
		apps[app.Slug] = app
	}
	if len(apps) != 5 {
		t.Fatalf("%d apps, want 5: %v", len(apps), apps)
	}

	web := apps["web"]
	if web.SourceType != "git" || web.RootDir != "deploy/web" || web.Builder != "dockerfile" ||
		web.DockerfilePath != "Dockerfile.prod" || web.Port != 3000 || web.Internal {
		t.Errorf("web: %+v", web)
	}
	if worker := apps["worker"]; worker.StartCommand != "'npm' 'run' 'worker'" || !worker.Internal || worker.Port != 0 {
		t.Errorf("worker: %+v", worker)
	}
	// Published nowhere, so internal, on the port its image is known for.
	if db := apps["db"]; db.SourceType != "image" || db.Image != "postgres:16" || db.Port != 5432 || !db.Internal {
		t.Errorf("db: %+v", db)
	}
	if cache := apps["cache"]; cache.Port != 6379 || !cache.Internal {
		t.Errorf("cache: %+v", cache)
	}
	if admin := apps["admin-ui"]; admin.Port != 8080 || !admin.Internal {
		t.Errorf("an exposed port is still internal: %+v", admin)
	}

	// The named volume became a disk; the folder on somebody's machine did not.
	volumes, _ := h.db.ListVolumes(t.Context(), apps["db"].ID)
	if len(volumes) != 1 || volumes[0].MountPath != "/var/lib/postgresql/data" || volumes[0].SizeGB != 5 {
		t.Errorf("db volumes: %+v", volumes)
	}
	// Variables arrive as Compose would have set them: a default taken, a
	// reference with none kept and said.
	vars, _ := h.db.ListVariables(t.Context(), web.ID)
	if len(vars) != 4 {
		t.Errorf("web has %d variables", len(vars))
	}
	codes := map[string]bool{}
	for _, note := range answer.Notes {
		codes[note.Service+":"+note.Code+":"+note.Value] = true
	}
	for _, want := range []string{
		"Admin_UI:renamed:admin-ui",
		"db:bind_mount:./init.sql:/docker-entrypoint-initdb.d/init.sql",
		"web:interpolation:API_KEY",
	} {
		if !codes[want] {
			t.Errorf("no note %s in %v", want, answer.Notes)
		}
	}
	if codes["web:interpolation:LOG_LEVEL"] {
		t.Error("a default was reported as unresolved")
	}

	// Every one deployed, and the database before what depends on it.
	order := deploys.all()
	if len(order) != 5 {
		t.Fatalf("%d deploys", len(order))
	}
	if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, "stack.created", "", 10); len(events) != 1 {
		t.Errorf("the stack was not audited")
	}
}

// A problem with one service creates none of them.
func TestAStackIsAllOrNothing(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	path := "/api/environments/" + acme.env.ID + "/stack"
	h.app(acme, "cache")

	cases := map[string]struct {
		body map[string]any
		code string
	}{
		"a name already used": {map[string]any{"repo_url": "https://github.com/acme/shop", "services": composeServices(t)}, "resource.name_taken"},
		"no repository for a build": {map[string]any{"services": []builder.ComposeService{
			{Name: "web", Build: "."},
		}}, "stack.needs_repository"},
		"nothing to run": {map[string]any{"services": []builder.ComposeService{{Name: "web"}}}, "stack.no_source"},
		"two services, one name": {map[string]any{"services": []builder.ComposeService{
			{Name: "api_v2", Image: "a"}, {Name: "api-v2", Image: "b"},
		}}, "stack.duplicate"},
		"none at all": {map[string]any{"services": []builder.ComposeService{}}, "stack.size"},
	}
	for name, c := range cases {
		status, body := h.do(acme, http.MethodPost, path, c.body)
		if status < 400 || !strings.Contains(body, c.code) {
			t.Errorf("%s answered %d: %s", name, status, truncate(body, 200))
		}
	}
	apps, _ := h.db.ListApps(t.Context(), acme.env.ID)
	if len(apps) != 1 {
		t.Fatalf("a refused stack left %d apps behind", len(apps)-1)
	}
}

func TestADependencyIsDeployedFirst(t *testing.T) {
	plan := func(name string, deps ...string) plannedApp {
		return plannedApp{service: builder.ComposeService{Name: name, DependsOn: deps}, app: store.App{Name: name}}
	}
	order := deployOrder([]plannedApp{plan("web", "db", "cache"), plan("db"), plan("cache", "db"), plan("a", "web")})
	var names []string
	for _, app := range order {
		names = append(names, app.Name)
	}
	if strings.Join(names, ",") != "db,cache,web,a" {
		t.Fatalf("deployed in the order %v", names)
	}
	// A cycle does not hang it.
	if got := deployOrder([]plannedApp{plan("a", "b"), plan("b", "a")}); len(got) != 2 {
		t.Fatalf("a cycle deployed %d", len(got))
	}
}

// A Compose file names its images itself, which was the way round the rule a
// single app is held to: an image this panel built for another environment runs
// only in the environment that built it.
func TestAComposeFileCannotRunAnotherEnvironmentsBuiltImage(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	path := "/api/environments/" + acme.env.ID + "/stack"

	theirs := kube.RegistryHost() + "/" + rival.env.Namespace + "/web:d1"
	status, body := h.do(acme, http.MethodPost, path, map[string]any{
		"services": []builder.ComposeService{{Name: "web", Image: theirs}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("another environment's image was accepted: %d %s", status, truncate(body, 200))
	}
	if apps, _ := h.db.ListApps(t.Context(), acme.env.ID); len(apps) != 0 {
		t.Errorf("a refused stack left %d apps behind", len(apps))
	}

	// Its own environment's image, and any public one, are fine.
	own := kube.RegistryHost() + "/" + acme.env.Namespace + "/web:d1"
	status, body = h.do(acme, http.MethodPost, path, map[string]any{
		"services": []builder.ComposeService{{Name: "web", Image: own}, {Name: "cache", Image: "redis:7"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("its own environment's image was refused: %d %s", status, truncate(body, 200))
	}
}
