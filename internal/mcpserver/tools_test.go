package mcpserver

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/cli"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/store"
)

// connect opens a session with a server the way an assistant's client does.
func connect(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	serverSide, clientSide := mcp.NewInMemoryTransports()
	session, err := s.mcp.Connect(t.Context(), serverSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), clientSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// call calls a tool and returns everything it answered — the text and the
// structured content both, since an assistant reads both — and whether it
// answered as an error.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var out strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	if result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		out.WriteString("\n")
		out.Write(encoded)
	}
	return out.String(), result.IsError
}

// exampleCalls is one call of every tool the panel's own endpoint offers.
// A tool added without one fails TestEveryToolCallsADescribedRouteAndPassesOnNoSecret,
// which is the point: every tool is run once against the panel's routes.
var exampleCalls = map[string]map[string]any{
	"list_projects":           {},
	"create_app":              {"name": "web", "image": "nginx:1.27"},
	"list_apps":               {"environment_id": "env_1"},
	"get_app_status":          {"app_id": "app_1"},
	"deploy_app":              {"app_id": "app_1"},
	"run_command":             {"app_id": "app_1", "command": "bin/migrate"},
	"get_app_logs":            {"app_id": "app_1"},
	"list_variables":          {"app_id": "app_1"},
	"set_variable":            {"app_id": "app_1", "key": "GREETING", "value": "hello"},
	"scale_app":               {"app_id": "app_1", "instances": 2},
	"set_process":             {"app_id": "app_1", "name": "worker", "command": "bin/work"},
	"rollback_app":            {"app_id": "app_1", "deployment_id": "dep_1"},
	"get_deployment_history":  {"app_id": "app_1"},
	"get_cluster_status":      {},
	"check_scaling_readiness": {"app_id": "app_1"},

	"list_templates":   {"query": "wordpress"},
	"install_template": {"template_id": "plausible", "environment_id": "env_1", "values": map[string]any{"BASE_URL": "https://stats.example.com"}},

	"set_variables":     {"app_id": "app_1", "set": []any{map[string]any{"key": "GREETING", "value": "hello"}}, "unset": []any{"OLD"}},
	"delete_variable":   {"app_id": "app_1", "key": "OLD"},
	"refresh_variables": {"app_id": "app_1"},

	// The secret one: its content is what must not come back.
	"list_files": {"app_id": "app_1", "path": "/app/secrets.yml"},
	"set_file":   {"app_id": "app_1", "path": "/etc/nginx/nginx.conf", "content": "events {}"},

	"list_databases":      {"environment_id": "env_1"},
	"create_database":     {"environment_id": "env_1", "name": "db", "engine": "postgres"},
	"link_database":       {"database_id": "db_1", "app_id": "app_1"},
	"get_database_status": {"database_id": "db_1"},
	"list_backups":        {"database_id": "db_1"},
	"run_backup":          {"database_id": "db_1"},
	"stop_database":       {"database_id": "db_1", "force": true},
	"start_database":      {"database_id": "db_1"},
	"resize_database":     {"database_id": "db_1", "mem_limit_mb": 2048, "storage_gb": 20},

	"list_dns_providers": {},
	"list_dns_zones":     {"provider_id": "dnsp_1"},

	"list_domains":  {"app_id": "app_1"},
	"add_domain":    {"app_id": "app_1", "hostname": "shop.example.com"},
	"remove_domain": {"app_id": "app_1", "domain_id": "dom_2"},
	"list_ports":    {"app_id": "app_1"},
	"open_port":     {"app_id": "app_1", "port": 25565},
	"close_port":    {"app_id": "app_1", "port_id": "port_1"},

	"lock_deploys":   {"app_id": "app_1", "reason": "launch day"},
	"unlock_deploys": {"app_id": "app_1"},

	"set_gpus": {"app_id": "app_1", "count": 1, "vendor": "nvidia"},

	"get_vulnerabilities": {"app_id": "app_1"},
	"get_events":          {"app_id": "app_1", "warnings_only": true},

	"list_log_drains": {},

	"update_app_settings": {"app_id": "app_1", "port": 3000, "build_command": "npm run build"},
	"restart_app":         {"app_id": "app_1"},
	"cancel_deployment":   {"app_id": "app_1", "deployment_id": "dep_2"},
	"get_attention":       {},
	"promote_app":         {"app_id": "app_1", "to_environment": "production"},
}

// Every tool, once, against a panel that hands over every secret it holds.
//
// Three things are checked on the way. Each tool works against the routes it
// calls. Each request it makes is one the API description documents, with
// that method — so no tool calls a route somebody imagined. And nothing a
// tool answers contains a secret, and no tool asks for a database's password
// or starts a restore: those are kept from assistants on purpose, and the
// comment at the top of databases.go says why.
func TestEveryToolCallsADescribedRouteAndPassesOnNoSecret(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, NewRemote(panel.config(), nil))
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	offered := map[string]bool{}
	for _, tool := range tools.Tools {
		offered[tool.Name] = true
		args, ok := exampleCalls[tool.Name]
		if !ok {
			t.Errorf("%s has no example call, so nothing checks what it asks the panel for", tool.Name)
			continue
		}
		text, failed := call(t, session, tool.Name, args)
		if failed {
			t.Errorf("%s failed against the panel's routes: %s", tool.Name, text)
		}
		for _, secret := range planted {
			if strings.Contains(text, secret) {
				t.Errorf("%s passed a secret on to the assistant: %s", tool.Name, text)
			}
		}
	}
	for name := range exampleCalls {
		if !offered[name] {
			t.Errorf("there is an example call of %s, which the panel's endpoint does not offer", name)
		}
	}

	routes := describedRoutes(t)
	requests := panel.requests()
	if len(requests) < len(exampleCalls) {
		t.Fatalf("only %d requests reached the panel", len(requests))
	}
	for _, request := range requests {
		if strings.HasSuffix(request.Path, "/credentials") || strings.Contains(request.Path, "/restore/") ||
			(request.Method == "DELETE" && appItself.MatchString(request.Path)) {
			t.Errorf("a tool asked for %s %s", request.Method, request.Path)
		}
		if !isDescribed(routes, request.Method, request.Path) {
			t.Errorf("a tool asked for %s %s, which the API description does not document", request.Method, request.Path)
		}
	}
}

// The schema is the first thing an assistant reads about a tool, and a field
// it thinks is optional is one it leaves out. So each new tool requires what
// the API needs to do anything at all — and no more, or the assistant is made
// to invent a value.
func TestTheToolsRequireWhatTheAPIRequires(t *testing.T) {
	tools := listTools(t, New(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"}))
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	want := map[string][]string{
		"list_templates":      nil,
		"install_template":    {"environment_id", "template_id"},
		"set_variables":       {"app_id"},
		"delete_variable":     {"app_id", "key"},
		"list_files":          {"app_id"},
		"set_file":            {"app_id", "content", "path"},
		"list_databases":      nil,
		"create_database":     {"engine", "environment_id", "name"},
		"link_database":       {"app_id", "database_id"},
		"get_database_status": {"database_id"},
		"stop_database":       {"database_id"},
		"start_database":      {"database_id"},
		"resize_database":     {"database_id"},
		// A database, or an app and its volume: the tool says which it
		// needs when it has neither, because a schema cannot say "one of".
		"list_backups":       nil,
		"run_backup":         nil,
		"list_dns_providers": nil,
		"list_dns_zones":     {"provider_id"},
		"list_domains":       {"app_id"},
		"add_domain":         {"app_id", "hostname"},
		"remove_domain":      {"app_id", "domain_id"},
		"list_ports":         {"app_id"},
		"open_port":          {"app_id", "port"},
		"close_port":         {"app_id", "port_id"},
		"lock_deploys":       {"app_id", "reason"},
		"unlock_deploys":     {"app_id"},
		// Nothing else: a vendor, a model and the workloads default to
		// what the app has, and the count is the whole question.
		"set_gpus": {"app_id", "count"},
		// Every setting is left as it is unless it is given.
		"update_app_settings": {"app_id"},
		"restart_app":         {"app_id"},
		// The one under way, when none is named.
		"cancel_deployment": {"app_id"},
		"rollback_app":      {"app_id"},
		// The version is the one running, unless another is named.
		"promote_app": {"app_id", "to_environment"},
	}
	for name, required := range want {
		tool := byName[name]
		if tool == nil {
			t.Errorf("there is no tool called %s", name)
			continue
		}
		if got := schemaStrings(schemaOf(t, tool)["required"]); !slices.Equal(got, required) {
			t.Errorf("%s requires %v, and the API needs %v", name, got, required)
		}
	}

	// Where the API takes one of a few words, the schema says which.
	enum := func(tool, property string) []string {
		properties, _ := schemaOf(t, byName[tool])["properties"].(map[string]any)
		field, _ := properties[property].(map[string]any)
		return schemaStrings(field["enum"])
	}
	if got := enum("create_database", "engine"); !slices.Equal(got, slices.Sorted(slices.Values(engine.Names()))) {
		t.Errorf("create_database's engine is one of %v, and the panel runs %v", got, engine.Names())
	}
	if got := enum("open_port", "protocol"); !slices.Equal(got, []string{"tcp", "udp"}) {
		t.Errorf("open_port's protocol is one of %v", got)
	}
	properties, _ := schemaOf(t, byName["open_port"])["properties"].(map[string]any)
	port, _ := properties["port"].(map[string]any)
	if port["minimum"] != 1.0 || port["maximum"] != 65535.0 {
		t.Errorf("open_port's port is not limited to a port number: %v", port)
	}

	// A variable set in a batch needs a name and a value, as one set alone
	// does.
	properties, _ = schemaOf(t, byName["set_variables"])["properties"].(map[string]any)
	set, _ := properties["set"].(map[string]any)
	item, _ := set["items"].(map[string]any)
	if got := schemaStrings(item["required"]); !slices.Equal(got, []string{"key", "value"}) {
		t.Errorf("a variable in set_variables requires %v", got)
	}
}

// appItself is the route of an app, as opposed to anything under it.
var appItself = regexp.MustCompile(`^/api/apps/[^/]+/?$`)

// Deleting an app takes its disks with it, for good, so no tool does it: the
// person does, in the panel or with `skifity apps rm`. The comments at the top
// of apps.go and databases.go say why; this keeps a tool from being added
// that does it anyway, under any name.
func TestNoToolDeletesAnApp(t *testing.T) {
	for _, tool := range listTools(t, New(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"})) {
		if strings.Contains(tool.Name, "delete_app") || strings.Contains(tool.Name, "remove_app") {
			t.Errorf("%s is offered, and deleting an app is the person's to do", tool.Name)
		}
	}
}

// A dry run reads the plan and changes nothing; a rollback says what it puts
// back too, since that is what a person is surprised by afterwards.
func TestARollbackSaysWhatItPutsBackAndADryRunDoesNothing(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "rollback_app", map[string]any{"app_id": "app_1", "deployment_id": "dep_1", "dry_run": true})
	if failed || !strings.Contains(text, "port") || !strings.Contains(text, "8080") {
		t.Fatalf("a dry run answered (error=%v): %s", failed, text)
	}
	for _, request := range panel.requests() {
		if request.Method != "GET" {
			t.Errorf("a dry run asked for %s %s", request.Method, request.Path)
		}
	}
	text, failed = call(t, session, "rollback_app", map[string]any{"app_id": "app_1", "deployment_id": "dep_1"})
	if failed || !strings.Contains(text, "8080") {
		t.Fatalf("a rollback answered (error=%v): %s", failed, text)
	}
	if _, ok := panel.last("POST", "/api/apps/app_1/rollback/dep_1"); !ok {
		t.Error("the rollback was not asked for")
	}
}

// Only the settings given are sent, and the answer says which rolled out and
// which wait for a deploy, so the assistant knows whether to call deploy_app.
func TestUpdatingSettingsSendsOnlyWhatWasGivenAndSaysWhatWaits(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "update_app_settings", map[string]any{"app_id": "app_1", "port": 3000, "build_command": "npm run build"})
	if failed {
		t.Fatalf("update_app_settings failed: %s", text)
	}
	request, _ := panel.last("PATCH", "/api/apps/app_1")
	if want := map[string]any{"port": 3000.0, "build_command": "npm run build"}; !reflect.DeepEqual(request.Body, want) {
		t.Errorf("the settings sent were %v, want %v", request.Body, want)
	}
	if !strings.Contains(text, "deploy_app") || !strings.Contains(text, "build_command") {
		t.Errorf("the answer does not say what waits for a deploy: %s", text)
	}
	if text, failed := call(t, session, "update_app_settings", map[string]any{"app_id": "app_1"}); !failed {
		t.Errorf("no settings at all was sent on: %s", text)
	}
}

// What waits for a deploy is said, by setting and by a variable's name, and
// a build-time value that was rebuilt says which deployment is doing it.
func TestTheStatusSaysWhatWaitsForADeployAndASetSaysItRebuilt(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "get_app_status", map[string]any{"app_id": "app_1"})
	if failed || !strings.Contains(text, "build_command") || !strings.Contains(text, "NEXT_PUBLIC_API") ||
		!strings.Contains(text, "deploy_app") {
		t.Errorf("get_app_status answered (error=%v): %s", failed, text)
	}
	text, failed = call(t, session, "set_variable", map[string]any{"app_id": "app_1", "key": "NEXT_PUBLIC_API", "value": "x", "build_time": true})
	if failed || !strings.Contains(text, "rebuilt") || !strings.Contains(text, "#3") {
		t.Errorf("set_variable answered (error=%v): %s", failed, text)
	}
}

// A version is promoted to the app of the same name in the environment named,
// and an environment with none is said rather than guessed at.
func TestPromoteFindsTheEnvironmentAndSaysWhenThereIsNone(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "promote_app", map[string]any{"app_id": "app_1", "to_environment": "production"})
	if failed || !strings.Contains(text, "#4") {
		t.Fatalf("promote_app answered (error=%v): %s", failed, text)
	}
	request, ok := panel.last("POST", "/api/apps/app_9/promote")
	if !ok || request.Body["deployment_id"] != "dep_0" || request.Body["force"] != nil {
		t.Errorf("the promotion sent %+v", request)
	}
	text, failed = call(t, session, "promote_app", map[string]any{"app_id": "app_1", "to_environment": "staging"})
	if !failed || !strings.Contains(text, "Production") || !strings.Contains(text, "promote.not_possible") {
		t.Errorf("promoting to an environment with no such app answered (error=%v): %s", failed, text)
	}
}

func schemaOf(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func schemaStrings(value any) []string {
	list, _ := value.([]any)
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// What a client asks the person about is decided by the destructive hint, so
// it has to be right for every tool that changes anything — and a tool added
// later has to be put on one side or the other by somebody who thought about
// it, not left to whatever changes() was called with.
func TestWhatCanDestroySomethingSaysSo(t *testing.T) {
	destructive := []string{
		// Replaces what runs, or does whatever it does.
		"deploy_app", "deploy_folder", "rollback_app", "run_command", "scale_app", "set_process",
		// Overwrites or removes a value, a file, a hostname or a port.
		"set_variable", "set_variables", "delete_variable", "set_file", "link_database",
		// Rolls out, or rebuilds, with whatever a secret manager holds now.
		"refresh_variables",
		"remove_domain", "close_port",
		// Can delete the oldest backup, and lifts somebody's freeze.
		"run_backup", "unlock_deploys",
		// Restarts every instance, and can take a card away from an app
		// that cannot run without one.
		"set_gpus",
		// Takes a database from its apps; restarts one, and grows a disk
		// that cannot shrink again.
		"stop_database", "resize_database",
		// Rolls out over the running instances, or replaces them; and
		// throws a build away.
		"update_app_settings", "restart_app", "cancel_deployment",
		// Replaces what the other environment runs.
		"promote_app",
	}
	additive := []string{
		"create_app", "install_template", "create_database", "add_domain", "open_port", "lock_deploys",
		"start_database",
	}

	for _, tool := range listTools(t, New(cli.Config{PanelURL: "https://panel.example", Token: "skf_test"})) {
		a := tool.Annotations
		if a.ReadOnlyHint {
			if slices.Contains(destructive, tool.Name) || slices.Contains(additive, tool.Name) {
				t.Errorf("%s only reads, and this test says it changes something", tool.Name)
			}
			continue
		}
		want := slices.Contains(destructive, tool.Name)
		if !want && !slices.Contains(additive, tool.Name) {
			t.Errorf("%s changes something and this test does not say whether it can destroy anything; decide, and add it", tool.Name)
			continue
		}
		if a.DestructiveHint == nil || *a.DestructiveHint != want {
			t.Errorf("%s says destructive=%v, and it should say %v", tool.Name, a.DestructiveHint, want)
		}
	}
}

// Installing a template sends the environment and the inputs, sends no name
// nobody gave, and hands the template's notes on: they are the steps it could
// not do, and an assistant that drops them leaves the person with an app that
// is half set up and no idea why.
func TestInstallingATemplateSendsItsInputsAndRelaysItsNotes(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "install_template", map[string]any{
		"template_id": "plausible", "environment_id": "env_1",
		"values": map[string]any{"BASE_URL": "https://stats.example.com"},
	})
	if failed {
		t.Fatalf("install_template failed: %s", text)
	}
	request, ok := panel.last("POST", "/api/templates/plausible/install")
	if !ok {
		t.Fatalf("the template was not installed; the panel saw %+v", panel.requests())
	}
	want := map[string]any{"environment_id": "env_1", "values": map[string]any{"BASE_URL": "https://stats.example.com"}}
	if !reflect.DeepEqual(request.Body, want) {
		t.Errorf("the install sent %v, and should have sent %v", request.Body, want)
	}
	for _, expected := range []string{"ClickHouse", "app_9", "db_1"} {
		if !strings.Contains(text, expected) {
			t.Errorf("the answer does not mention %s: %s", expected, text)
		}
	}
}

// A dry run is passed on as one, and answered as what would be created rather
// than as something installed: an assistant told "Installed" by a dry run
// would tell the person their app exists.
func TestADryRunOfATemplateIsPassedOnAndCreatesNothing(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "install_template", map[string]any{
		"template_id": "plausible", "environment_id": "env_1", "name": "stats", "dry_run": true,
	})
	if failed {
		t.Fatalf("install_template failed: %s", text)
	}
	request, _ := panel.last("POST", "/api/templates/plausible/install")
	if want := map[string]any{"environment_id": "env_1", "name": "stats", "dry_run": true}; !reflect.DeepEqual(request.Body, want) {
		t.Errorf("the dry run sent %v, want %v", request.Body, want)
	}
	if !strings.Contains(text, "Nothing was created") || strings.Contains(text, "Installed") ||
		!strings.Contains(text, "plausible (ghcr.io/plausible/community-edition:v2.1.5)") {
		t.Errorf("a dry run answered as an install: %s", text)
	}
}

// A template whose image starts as root says so in the list, and accepting
// root images is passed on only when asked for: it changes the whole
// environment, and the tool's description tells an assistant to ask first.
func TestATemplateThatNeedsRootSaysSoAndAllowRootIsPassedOn(t *testing.T) {
	panel := newFakePanel(t)
	s := New(panel.config())

	_, out, _ := s.listTemplates(t.Context(), nil, listTemplatesInput{Query: "wordpress", Category: "cms"})
	for _, template := range out.Templates {
		if template.ID == "wordpress" && template.CatalogueID == "" && !template.NeedsRoot {
			t.Errorf("the built-in WordPress does not say it needs root: %+v", template)
		}
		if template.CatalogueID != "" && template.NeedsRoot {
			t.Errorf("a template that does not need root says it does: %+v", template)
		}
	}

	session := connect(t, New(panel.config()))
	for _, allow := range []bool{false, true} {
		args := map[string]any{"template_id": "wordpress", "environment_id": "env_1"}
		if allow {
			args["allow_root"] = true
		}
		if text, failed := call(t, session, "install_template", args); failed {
			t.Fatalf("install_template failed: %s", text)
		}
		request, _ := panel.last("POST", "/api/templates/wordpress/install")
		if sent, _ := request.Body["allow_root"].(bool); sent != allow {
			t.Errorf("asked with allow_root %v, the install sent %v", allow, request.Body)
		}
	}
}

// The search is the tool's own, over the whole catalogue: the template called
// what was asked for comes first, a category narrows it, and every category
// is named so the next search can use one.
func TestListTemplatesFindsWhatWasAskedForFirst(t *testing.T) {
	panel := newFakePanel(t)
	s := New(panel.config())

	_, out, _ := s.listTemplates(t.Context(), nil, listTemplatesInput{Query: "WordPress"})
	if out.Matches != 3 || out.Templates[0].ID != "wordpress" || out.Templates[1].ID != "wordpress" {
		t.Errorf("searching for WordPress found %+v", out.Templates)
	}
	if !slices.Equal(out.Categories, []string{"analytics", "cms"}) {
		t.Errorf("the categories are %v", out.Categories)
	}
	_, out, _ = s.listTemplates(t.Context(), nil, listTemplatesInput{Category: "analytics"})
	if out.Matches != 1 || out.Templates[0].ID != "plausible" || len(out.Templates[0].Inputs) != 2 {
		t.Errorf("the analytics templates are %+v", out.Templates)
	}
	_, out, _ = s.listTemplates(t.Context(), nil, listTemplatesInput{Limit: 1})
	if out.Matches != 4 || len(out.Templates) != 1 {
		t.Errorf("a limit of one returned %d of %d", len(out.Templates), out.Matches)
	}
}

// The team's own catalogue is part of the catalogue an assistant searches,
// and a template from it says which catalogue it is in: the same id can be in
// two. Installing one passes the catalogue on, and installing the built-in
// one of that id does not.
func TestATeamsOwnTemplateIsFoundAndInstalledFromItsCatalogue(t *testing.T) {
	panel := newFakePanel(t)
	s := New(panel.config())

	_, out, _ := s.listTemplates(t.Context(), nil, listTemplatesInput{Query: "acme"})
	if out.Matches != 1 || out.Templates[0].ID != "wordpress" || out.Templates[0].CatalogueID != "tcat_1" ||
		out.Templates[0].Catalogue != "Acme" {
		t.Fatalf("searching for the catalogue's name found %+v", out.Templates)
	}
	if _, ok := panel.last("GET", "/api/teams/team_1/templates"); !ok {
		t.Errorf("the team's catalogue was not asked for: %+v", panel.requests())
	}
	_, out, _ = s.listTemplates(t.Context(), nil, listTemplatesInput{Query: "wordpress", Category: "cms"})
	builtIn := 0
	for _, template := range out.Templates {
		if template.ID == "wordpress" && template.CatalogueID == "" {
			builtIn++
		}
	}
	if builtIn != 1 {
		t.Errorf("the built-in WordPress is not told apart from the team's: %+v", out.Templates)
	}

	session := connect(t, New(panel.config()))
	if text, failed := call(t, session, "install_template", map[string]any{
		"template_id": "wordpress", "catalogue_id": "tcat_1", "environment_id": "env_1",
	}); failed {
		t.Fatalf("install_template failed: %s", text)
	}
	request, _ := panel.last("POST", "/api/templates/wordpress/install")
	if want := map[string]any{"environment_id": "env_1", "catalogue_id": "tcat_1"}; !reflect.DeepEqual(request.Body, want) {
		t.Errorf("installing the team's template sent %v, want %v", request.Body, want)
	}
	if text, failed := call(t, session, "install_template", map[string]any{
		"template_id": "wordpress", "environment_id": "env_1",
	}); failed {
		t.Fatalf("install_template failed: %s", text)
	}
	request, _ = panel.last("POST", "/api/templates/wordpress/install")
	if _, sent := request.Body["catalogue_id"]; sent {
		t.Errorf("installing the built-in template named a catalogue: %v", request.Body)
	}
}

// A file that is not a secret is read whole; a secret one is not read at all.
// The first half is what makes the second worth anything: a tool that never
// returned content would pass a test of the secret alone.
func TestAFileIsReadWholeUnlessItIsASecret(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "list_files", map[string]any{"app_id": "app_1", "path": "/etc/nginx/nginx.conf"})
	if failed || !strings.Contains(text, "events {}") {
		t.Errorf("a plain file's content was not returned (error=%v): %s", failed, text)
	}
	text, failed = call(t, session, "list_files", map[string]any{"app_id": "app_1", "path": "/app/secrets.yml"})
	if failed || strings.Contains(text, plantedFile) || !strings.Contains(text, "secret") {
		t.Errorf("a secret file was answered (error=%v): %s", failed, text)
	}
	// The list is paths, not content: every file's content at once is
	// too much, and a list cut short to fit is saved back cut short.
	text, _ = call(t, session, "list_files", map[string]any{"app_id": "app_1"})
	if strings.Contains(text, "events {}") || strings.Contains(text, plantedFile) {
		t.Errorf("the list carried content: %s", text)
	}
	text, _ = call(t, session, "list_variables", map[string]any{"app_id": "app_1"})
	if !strings.Contains(text, "hello") || strings.Contains(text, plantedVariable) {
		t.Errorf("list_variables answered: %s", text)
	}
}

// Replacing a script's content without saying anything about it keeps it
// executable, and says nothing about whether it is a secret, so the panel
// keeps that too.
func TestSavingAFileKeepsWhatItWas(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	if text, failed := call(t, session, "set_file", map[string]any{
		"app_id": "app_1", "path": "/docker-entrypoint.d/10-init.sh", "content": "#!/bin/sh\nexec nginx",
	}); failed {
		t.Fatalf("set_file failed: %s", text)
	}
	request, _ := panel.last("PUT", "/api/apps/app_1/files")
	if request.Body["executable"] != true {
		t.Errorf("the script was saved as executable=%v", request.Body["executable"])
	}
	if _, said := request.Body["is_secret"]; said {
		t.Errorf("set_file decided whether the file is a secret when nobody said: %v", request.Body)
	}

	call(t, session, "set_file", map[string]any{
		"app_id": "app_1", "path": "/docker-entrypoint.d/10-init.sh", "content": "#!/bin/sh", "executable": false,
	})
	if request, _ := panel.last("PUT", "/api/apps/app_1/files"); request.Body["executable"] != false {
		t.Errorf("asked for not executable, and sent %v", request.Body["executable"])
	}
}

// A backup is of a database or of a volume, and the tool says so rather than
// guessing. Given only an app, list_backups names the app's volumes, which is
// how an assistant finds the id run_backup wants.
func TestABackupIsOfADatabaseOrAVolume(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	for _, args := range []map[string]any{{}, {"app_id": "app_1"}, {"database_id": "db_1", "app_id": "app_1", "volume_id": "vol_1"}} {
		if text, failed := call(t, session, "run_backup", args); !failed || !strings.Contains(text, "database_id") {
			t.Errorf("run_backup with %v answered (error=%v): %s", args, failed, text)
		}
	}
	text, failed := call(t, session, "list_backups", map[string]any{"app_id": "app_1"})
	if failed || !strings.Contains(text, "vol_1") {
		t.Errorf("list_backups for an app answered (error=%v): %s", failed, text)
	}
	if _, asked := panel.last("GET", "/api/apps/app_1/volumes/vol_1/backups"); !asked {
		t.Error("the app's volume's backups were not asked for")
	}
	if text, failed := call(t, session, "run_backup", map[string]any{"app_id": "app_1", "volume_id": "vol_1"}); failed {
		t.Errorf("backing up a volume failed: %s", text)
	}
	if _, asked := panel.last("POST", "/api/apps/app_1/volumes/vol_1/backups"); !asked {
		t.Error("the volume was not backed up")
	}
}

// Creating a database passes on whether to back it up, only when the person
// said, and says what was done about it.
func TestCreatingADatabaseSaysWhetherItIsBackedUp(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "create_database", map[string]any{"environment_id": "env_1", "name": "db", "engine": "postgres"})
	if failed || !strings.Contains(text, "backed up every day at 17 3 * * *") {
		t.Errorf("create_database answered (error=%v): %s", failed, text)
	}
	request, _ := panel.last("POST", "/api/environments/env_1/databases")
	if _, said := request.Body["backup"]; said {
		t.Errorf("nothing was said about backups, and the request was %v", request.Body)
	}

	call(t, session, "create_database", map[string]any{"environment_id": "env_1", "name": "db", "engine": "postgres", "backup": false})
	request, _ = panel.last("POST", "/api/environments/env_1/databases")
	if request.Body["backup"] != false {
		t.Errorf("no was not passed on: %v", request.Body)
	}
}

// Linking with pieces passes them on, and says which the link delivers.
func TestLinkingADatabaseCanGiveItsPieces(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "link_database", map[string]any{
		"database_id": "db_1", "app_id": "app_1", "variable": "DATABASE_URL",
		"pieces": map[string]any{"host": "DB_HOST", "password": "DB_PASSWORD"},
	})
	if failed || !strings.Contains(text, "DB_HOST, DB_PASSWORD") {
		t.Errorf("link_database answered (error=%v): %s", failed, text)
	}
	request, _ := panel.last("POST", "/api/databases/db_1/link")
	pieces, _ := request.Body["pieces"].(map[string]any)
	if pieces["host"] != "DB_HOST" || pieces["password"] != "DB_PASSWORD" || len(pieces) != 2 {
		t.Errorf("the pieces were sent as %v", request.Body)
	}

	call(t, session, "link_database", map[string]any{"database_id": "db_1", "app_id": "app_1"})
	request, _ = panel.last("POST", "/api/databases/db_1/link")
	if _, sent := request.Body["pieces"]; sent {
		t.Errorf("pieces nobody named were sent: %v", request.Body)
	}
}

// The record somebody types into their DNS provider: the type follows the
// value, and the address the panel gave the app needs none.
func TestADomainSaysWhichDNSRecordToCreate(t *testing.T) {
	cases := []struct {
		target string
		auto   bool
		want   *dnsRecord
	}{
		{target: "203.0.113.10", want: &dnsRecord{Type: "A", Name: "shop.example.com", Value: "203.0.113.10"}},
		{target: "2001:db8::10", want: &dnsRecord{Type: "AAAA", Name: "shop.example.com", Value: "2001:db8::10"}},
		{target: "lb.example.net", want: &dnsRecord{Type: "CNAME", Name: "shop.example.com", Value: "lb.example.net"}},
		{target: "", want: nil},
		{target: "203.0.113.10", auto: true, want: nil},
	}
	for _, c := range cases {
		got := recordFor(store.Domain{Hostname: "shop.example.com", DNSTarget: c.target, Auto: c.auto})
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("a domain pointing at %q (auto=%v) needs %+v, not %+v", c.target, c.auto, c.want, got)
		}
	}

	// Adding one reads the list for where it points, since the answer to
	// adding does not say.
	panel := newFakePanel(t)
	text, failed := call(t, connect(t, New(panel.config())), "add_domain",
		map[string]any{"app_id": "app_1", "hostname": "shop.example.com"})
	if failed || !strings.Contains(text, "type A, name shop.example.com, value 203.0.113.10") {
		t.Errorf("add_domain answered (error=%v): %s", failed, text)
	}
}

// A version that crashed as it started says why in the history, without an
// assistant having to know to ask for the previous container's log.
func TestDeploymentHistoryCarriesWhatACrashedVersionPrinted(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, NewRemote(panel.config(), nil))

	text, failed := call(t, session, "get_deployment_history", map[string]any{"app_id": "app_1"})
	if failed {
		t.Fatalf("the history failed: %s", text)
	}
	if !strings.Contains(text, `"error_code":"deploy.crashed"`) || !strings.Contains(text, `"last_lines":"Error: DATABASE_URL is not set"`) {
		t.Errorf("the history does not carry the crashed version's last lines: %s", text)
	}
}
