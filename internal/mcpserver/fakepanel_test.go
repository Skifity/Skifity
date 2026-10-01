package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/yaml"

	"skifity/internal/cli"
)

// A panel that answers the routes the tools call with canned answers, and
// keeps every request it was sent.
//
// Wherever the real panel holds a secret back, this one hands it over: a
// secret variable's value, a secret file's content, a database's password. A
// tool that passed one on is then caught doing it here, rather than being
// saved by the panel's own care. That the real panel holds them back is
// tested in internal/api.

const (
	plantedVariable = "planted-variable-value"
	plantedFile     = "planted-file-content"
	plantedPassword = "planted-database-password"
	// plantedCatalogueHeader is the token a team catalogue is fetched with,
	// which the real panel never answers with anything.
	plantedCatalogueHeader = "planted-catalogue-header"
	// plantedReference is a value read from a secret manager, sent by a panel
	// that should not have: under a variable not even marked secret.
	plantedReference = "planted-secret-manager-value"
)

var planted = []string{plantedVariable, plantedFile, plantedPassword, plantedCatalogueHeader, plantedReference}

type fakePanel struct {
	t      *testing.T
	server *httptest.Server

	mu   sync.Mutex
	seen []seenRequest
}

type seenRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

func newFakePanel(t *testing.T) *fakePanel {
	t.Helper()
	f := &fakePanel{t: t}
	mux := http.NewServeMux()

	app := map[string]any{
		"id": "app_1", "environment_id": "env_1", "name": "web", "status": "running",
		"source_type": "image", "image": "nginx:1.27", "replicas": 1,
	}
	database := map[string]any{
		"id": "db_1", "environment_id": "env_1", "name": "db", "engine": "postgres",
		"engine_version": "17", "status": "running", "instances": 1, "storage_gb": 5,
	}
	deployment := map[string]any{"id": "dep_2", "app_id": "app_1", "number": 2, "status": "queued"}
	backup := map[string]any{
		"id": "bak_1", "target_type": "database", "target_id": "db_1", "status": "succeeded",
		"kind": "manual", "location": "skifity/databases/db_1/bak_1.sql.gz", "size_bytes": 2048,
		"created_at": "2026-09-30T03:00:00Z", "encrypted": true,
	}
	lock := map[string]any{
		"app_id": "app_1", "reason": "launch day", "locked_by": "ada@example.test",
		"locked_at": "2026-09-30T09:00:00Z",
	}
	port := map[string]any{
		"id": "port_1", "app_id": "app_1", "port": 25565, "protocol": "tcp", "public_port": 25565,
		"addresses": []string{"203.0.113.10:25565"},
	}
	// A report with more findings than the tool passes on, one of them
	// critical with a fix, on an image the app has since moved on from.
	findings := []any{map[string]any{
		"id": "CVE-2026-0001", "package": "openssl", "installed": "3.0.2", "fixed_in": "3.0.15",
		"severity": "CRITICAL", "title": "A made-up hole for a test", "url": "https://avd.aquasec.com/nvd/cve-2026-0001",
	}}
	for i := range 60 {
		findings = append(findings, map[string]any{
			"id": fmt.Sprintf("CVE-2026-1%03d", i), "package": "libfoo",
			"installed": "1.0", "severity": "LOW",
		})
	}
	vulnerabilities := map[string]any{
		"enabled": true, "blocking": true, "image": "registry.example.test/acme/web:v2", "current": false,
		"scan": map[string]any{
			"id": "scan_1", "app_id": "app_1", "image": "registry.example.test/acme/web:v1", "trigger": "schedule",
			"status": "succeeded", "counts": map[string]any{"critical": 1, "high": 0, "medium": 0, "low": 60, "unknown": 0},
			"fixable": 1, "fixable_critical": 1, "findings": findings, "omitted": 0,
			"scanner_version": "0.74.0", "created_at": "2026-09-30T04:23:00Z", "finished_at": "2026-09-30T04:24:10Z",
		},
		"latest": map[string]any{
			"id": "scan_2", "app_id": "app_1", "image": "registry.example.test/acme/web:v2", "trigger": "deploy",
			"status": "failed", "counts": map[string]any{"critical": 0, "high": 0, "medium": 0, "low": 0, "unknown": 0},
			"findings": []any{}, "error_code": "scan.database_unavailable",
			"error_message": "The scanner could not fetch its database: no route to mirror.gcr.io",
			"created_at":    "2026-09-30T09:00:00Z",
		},
	}
	ok := map[string]any{"ok": true}
	items := func(values ...any) map[string]any {
		if values == nil {
			values = []any{}
		}
		return map[string]any{"items": values, "total": len(values)}
	}

	// The built-in catalogue, and the same as the team sees it: its own
	// catalogue's WordPress first, under the same id as the built-in one.
	builtIn := []any{
		map[string]any{
			"id": "wordpress-with-mariadb", "name": "WordPress with MariaDB", "category": "cms",
			"description": "The blogging platform, with its database.",
			"services":    []any{map[string]any{"name": "wordpress"}}, "databases": []any{map[string]any{"engine": "mysql"}},
		},
		map[string]any{
			"id": "wordpress", "name": "WordPress", "category": "cms", "description": "The blogging platform.",
			"services": []any{map[string]any{"name": "wordpress"}},
		},
		map[string]any{
			"id": "plausible", "name": "Plausible Analytics", "category": "analytics", "beta": true,
			"description": "Privacy-friendly website analytics.",
			"services":    []any{map[string]any{"name": "plausible"}}, "databases": []any{map[string]any{"engine": "postgres"}},
			"inputs": []any{
				map[string]any{"key": "SECRET_KEY_BASE", "label": "Secret key base", "secret": true, "generate": true},
				map[string]any{"key": "BASE_URL", "label": "Public URL", "required": true},
			},
			"notes": "Plausible also needs ClickHouse for its event data.",
		},
	}
	teamOwn := map[string]any{
		"id": "wordpress", "name": "WordPress, Acme's build", "category": "cms",
		"description": "The blogging platform, with Acme's theme baked in.",
		"services":    []any{map[string]any{"name": "wordpress"}},
		"catalogue": map[string]any{
			"id": "tcat_1", "name": "Acme",
			// What the panel never answers, handed over here so that a tool
			// passing a catalogue on whole is caught doing it.
			"auth_header_value": plantedCatalogueHeader,
		},
	}

	answers := map[string]any{
		"GET /api/teams/{team}/projects":           items(map[string]any{"id": "prj_1", "team_id": "team_1", "name": "acme"}),
		"GET /api/projects/{project}/environments": items(map[string]any{"id": "env_1", "project_id": "prj_1", "name": "production", "slug": "production", "kind": "standard"}),
		"GET /api/teams/{team}/cluster": map[string]any{
			"reachable": true, "ready_nodes": 1, "total_memory_mb": 4096, "used_memory_mb": 1024,
			"nodes": []any{map[string]any{"name": "s1", "ready": true, "roles": []string{"control-plane"}}},
		},

		"GET /api/environments/{env}/apps":       items(app),
		"POST /api/environments/{env}/apps":      map[string]any{"app": app, "webhook": map[string]any{"registered": false}},
		"GET /api/environments/{env}/databases":  items(database),
		"POST /api/environments/{env}/databases": database,

		"GET /api/apps/{app}/status":                 map[string]any{"phase": "running", "desired_replicas": 1, "ready_replicas": 1, "urls": []string{"https://web.example.test"}},
		"POST /api/apps/{app}/deploy":                deployment,
		"GET /api/apps/{app}/deployments":            items(deployment),
		"POST /api/apps/{app}/rollback/{deployment}": deployment,
		"GET /api/apps/{app}/logs":                   map[string]any{"lines": []string{"listening on 8080"}},
		"POST /api/apps/{app}/run":                   map[string]any{"run": "run_1"},
		"GET /api/apps/{app}/runs/{run}/logs":        map[string]any{"lines": []string{"migrated"}},
		"PUT /api/apps/{app}/scaling":                map[string]any{"scaling": map[string]any{"replicas": 2}},
		"PUT /api/apps/{app}/gpu": map[string]any{
			"gpu": map[string]any{"count": 1, "vendor": "nvidia", "product": "", "workloads": []any{}},
			"cluster": map[string]any{"known": true, "vendors": []any{
				map[string]any{"vendor": "nvidia", "allocatable": 2, "in_use": 1, "most_on_one_server": 2, "products": []any{"NVIDIA-A10"}},
			}},
			"warnings": []any{},
		},
		"GET /api/apps/{app}/scaling/readiness":   items(),
		"PUT /api/apps/{app}/processes/{process}": map[string]any{"app_id": "app_1", "name": "worker", "command": "bin/work", "instances": 1},

		"GET /api/apps/{app}/variables": items(
			map[string]any{"key": "GREETING", "value": "hello", "is_secret": false},
			map[string]any{"key": "API_KEY", "value": plantedVariable, "is_secret": true},
			map[string]any{"key": "STRIPE_KEY", "value": plantedReference, "is_secret": false, "reference": map[string]any{
				"connection_id": "sm_1", "connection": "company-vault", "kind": "vault", "path": "shop", "key": "stripe_key",
			}},
		),
		"PUT /api/apps/{app}/variables":          map[string]any{"variable": map[string]any{"key": "GREETING"}, "requires_rebuild": false},
		"POST /api/apps/{app}/variables/batch":   map[string]any{"set": []any{map[string]any{"key": "GREETING", "is_secret": false}}, "unset": []string{"OLD"}, "requires_rebuild": false},
		"DELETE /api/apps/{app}/variables/{key}": ok,
		"POST /api/apps/{app}/variables/refresh": map[string]any{
			"references": 1, "changed": []string{"STRIPE_KEY"}, "build_time_changed": []string{}, "rolled_out": true,
		},

		"GET /api/apps/{app}/files": items(
			map[string]any{"id": "file_1", "path": "/etc/nginx/nginx.conf", "content": "events {}", "size": 9},
			map[string]any{"id": "file_2", "path": "/app/secrets.yml", "content": plantedFile, "size": 22, "is_secret": true},
			map[string]any{"id": "file_3", "path": "/docker-entrypoint.d/10-init.sh", "content": "#!/bin/sh", "size": 9, "executable": true},
		),
		"PUT /api/apps/{app}/files": map[string]any{"file": map[string]any{"id": "file_1", "path": "/etc/nginx/nginx.conf", "size": 9}},

		"GET /api/apps/{app}/domains": items(
			map[string]any{"id": "dom_1", "hostname": "web.203-0-113-10.sslip.io", "path": "/", "auto": true, "status": "active"},
			map[string]any{"id": "dom_2", "hostname": "shop.example.com", "path": "/", "tls": true, "status": "pending", "dns_target": "203.0.113.10"},
		),
		// Adding one answers the domain without where it points, as the
		// panel does.
		"POST /api/apps/{app}/domains":            map[string]any{"id": "dom_2", "hostname": "shop.example.com", "path": "/", "tls": true, "status": "pending"},
		"DELETE /api/apps/{app}/domains/{domain}": ok,

		"GET /api/apps/{app}/ports":           items(port),
		"POST /api/apps/{app}/ports":          port,
		"DELETE /api/apps/{app}/ports/{port}": ok,

		"GET /api/apps/{app}/events": items(map[string]any{
			"type": "Warning", "reason": "BackOff", "kind": "Pod", "name": "web-7d4f8b9c5-x2x9q", "count": 14,
			"message":    "Back-off restarting failed container web",
			"first_seen": "2026-09-30T09:00:00Z", "last_seen": "2026-09-30T09:10:00Z",
			"explanation":      "The app keeps stopping soon after it starts.",
			"explanation_code": "crash_backoff",
		}),

		"PUT /api/apps/{app}/lock":    lock,
		"DELETE /api/apps/{app}/lock": ok,

		"GET /api/apps/{app}/vulnerabilities": vulnerabilities,

		"GET /api/apps/{app}/volumes":                         items(map[string]any{"id": "vol_1", "app_id": "app_1", "name": "data", "mount_path": "/data", "size_gb": 1}),
		"GET /api/apps/{app}/volumes/{volume}/backups":        items(),
		"POST /api/apps/{app}/volumes/{volume}/backups":       backup,
		"GET /api/databases/{db}":                             map[string]any{"database": database, "links": []any{map[string]any{"database_id": "db_1", "app_id": "app_1", "var_name": "DATABASE_URL"}}},
		"POST /api/databases/{db}/link":                       ok,
		"GET /api/databases/{db}/backups":                     items(backup),
		"POST /api/databases/{db}/backups":                    backup,
		"GET /api/databases/{db}/credentials":                 map[string]any{"engine": "postgres", "password": plantedPassword, "url": "postgres://app:" + plantedPassword + "@db:5432/app"},
		"POST /api/databases/{db}/restore/{backup}":           map[string]any{"id": "op_1"},
		"POST /api/apps/{app}/volumes/{volume}/restore/{bak}": map[string]any{"id": "op_1"},

		"GET /api/templates":              items(builtIn...),
		"GET /api/teams/{team}/templates": items(append([]any{teamOwn}, builtIn...)...),
		"POST /api/templates/{template}/install": map[string]any{
			"apps":      []any{map[string]any{"id": "app_9", "name": "plausible", "image": "ghcr.io/plausible/community-edition:v2.1.5"}},
			"databases": []any{database},
			"notes":     "Plausible also needs ClickHouse for its event data.",
		},
	}
	for pattern, answer := range answers {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			f.record(r)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(answer)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		t.Errorf("the fake panel was asked for %s %s, which it does not answer", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"test.unanswered","title":"The fake panel does not answer this"}}`)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakePanel) record(r *http.Request) {
	var body map[string]any
	if raw, err := io.ReadAll(r.Body); err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			f.t.Errorf("%s %s sent a body that is not a JSON object: %s", r.Method, r.URL.Path, raw)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, seenRequest{Method: r.Method, Path: r.URL.Path, Body: body})
}

func (f *fakePanel) requests() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.seen...)
}

// last is the most recent request with this method to this path.
func (f *fakePanel) last(method, path string) (seenRequest, bool) {
	requests := f.requests()
	for i := len(requests) - 1; i >= 0; i-- {
		if requests[i].Method == method && requests[i].Path == path {
			return requests[i], true
		}
	}
	return seenRequest{}, false
}

func (f *fakePanel) config() cli.Config {
	return cli.Config{PanelURL: f.server.URL, Token: "skf_test", TeamID: "team_1"}
}

// describedRoutes reads the routes the API description documents, as the
// methods each path answers.
//
// The fake panel answers what these tests were written to expect, which is
// only as right as whoever wrote them. The description is checked against the
// router by internal/api/openapi_test.go, so a request that matches it is a
// request the panel answers.
type describedRoute struct {
	method  string
	pattern *regexp.Regexp
}

var pathParameter = regexp.MustCompile(`\{[^}]+\}`)

func describedRoutes(t *testing.T) []describedRoute {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read the API description: %v", err)
	}
	var document struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse the API description: %v", err)
	}
	var routes []describedRoute
	for path, operations := range document.Paths {
		literals := pathParameter.Split(path, -1)
		for i := range literals {
			literals[i] = regexp.QuoteMeta(literals[i])
		}
		pattern := regexp.MustCompile("^" + strings.Join(literals, "[^/]+") + "$")
		for method := range operations {
			switch method {
			case "get", "post", "put", "patch", "delete":
				routes = append(routes, describedRoute{method: strings.ToUpper(method), pattern: pattern})
			}
		}
	}
	if len(routes) < 100 {
		t.Fatalf("only %d routes were read from the API description; this is not reading it", len(routes))
	}
	return routes
}

func isDescribed(routes []describedRoute, method, path string) bool {
	for _, route := range routes {
		if route.method == method && route.pattern.MatchString(path) {
			return true
		}
	}
	return false
}
