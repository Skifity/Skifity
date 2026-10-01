package logdrain

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// parsed renders the configuration and reads it back the way Vector will: as
// YAML, into plain maps.
func parsed(t *testing.T, in RenderInput) (Config, map[string]any) {
	t.Helper()
	config, err := Render(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(config.YAML, &document); err != nil {
		t.Fatalf("the configuration is not YAML: %v\n%s", err, config.YAML)
	}
	return config, document
}

func section(t *testing.T, document map[string]any, path ...string) map[string]any {
	t.Helper()
	current := document
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("the configuration has no %s", strings.Join(path, "."))
		}
		current = next
	}
	return current
}

func str(value any) string {
	s, _ := value.(string)
	return s
}

func strs(value any) []string {
	list, _ := value.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, str(item))
	}
	return out
}

// Each kind becomes the sink that service reads, pointed at the address the
// panel's test used, with the credentials where that sink takes them.
func TestEveryKindBecomesItsOwnSink(t *testing.T) {
	config, document := parsed(t, RenderInput{Drains: everyKind(t), Names: everyName(), BuildsNamespace: "skifity-builds"})
	if config.Sinks != 9 {
		t.Fatalf("%d sinks were rendered for nine drains", config.Sinks)
	}
	sinks := section(t, document, "sinks")

	type want struct {
		kind   string
		fields map[string]string
	}
	cases := map[string]want{
		"drain_ldr_http": {"http", map[string]string{
			"uri": "https://logs.example.com/ingest?source=skifity", "method": "post",
			"framing.method": "newline_delimited", "encoding.codec": "json",
			"request.headers.Authorization": fakeHeaderValue,
		}},
		"drain_ldr_loki": {"loki", map[string]string{
			"endpoint": "https://logs-prod-012.grafana.net", "path": "/loki/api/v1/push",
			"auth.strategy": "basic", "auth.user": "123456", "auth.password": fakeLokiPassword,
			"tenant_id": "acme", "labels.team": "{{ skifity.team }}", "labels.app": "{{ skifity.app }}",
			"out_of_order_action": "accept",
		}},
		"drain_ldr_es": {"elasticsearch", map[string]string{
			"api_version": "v7", "mode": "bulk", "bulk.index": DefaultIndex, "bulk.action": "index",
			"auth.strategy": "basic", "auth.user": "shipper", "auth.password": fakeESPassword,
		}},
		"drain_ldr_opensearch": {"elasticsearch", map[string]string{
			"api_version": "v8", "bulk.index": "apps-%Y.%m", "request.headers.Authorization": "ApiKey " + fakeESAPIKey,
		}},
		"drain_ldr_datadog": {"datadog_logs", map[string]string{
			"site": "datadoghq.eu", "default_api_key": fakeDatadogKey,
		}},
		"drain_ldr_axiom": {"axiom", map[string]string{
			"dataset": "apps", "region": "eu-central-1.aws.edge.axiom.co", "org_id": "globex-x1y2", "token": fakeAxiomToken,
		}},
		"drain_ldr_better": {"http", map[string]string{
			"uri": "https://s1234567.eu-nbg-2.betterstackdata.com/", "auth.strategy": "bearer", "auth.token": fakeBetterToken,
		}},
		"drain_ldr_newrelic": {"new_relic", map[string]string{
			"api": "logs", "region": "eu", "account_id": "1234567", "license_key": fakeNewRelicKey,
		}},
		"drain_ldr_syslog": {"socket", map[string]string{
			"mode": "tcp", "address": "logs5.papertrailapp.com:6514", "encoding.codec": "syslog",
			"encoding.syslog.rfc": "rfc5424", "framing.method": "newline_delimited",
		}},
	}
	for name, c := range cases {
		sink, ok := sinks[name].(map[string]any)
		if !ok {
			t.Errorf("there is no sink %s", name)
			continue
		}
		if str(sink["type"]) != c.kind {
			t.Errorf("%s is a %q sink, want %q", name, sink["type"], c.kind)
		}
		for path, value := range c.fields {
			if got := lookup(sink, path); got != value {
				t.Errorf("%s.%s is %q, want %q", name, path, got, value)
			}
		}
		// Each sink holds at most a thousand lines and drops the newest
		// rather than holding up every other team's drains.
		if lookup(sink, "buffer.when_full") != "drop_newest" || lookup(sink, "buffer.type") != "memory" {
			t.Errorf("%s would block the pipeline when its service is slow: %v", name, sink["buffer"])
		}
		if lookup(sink, "healthcheck.enabled") != "false" {
			t.Errorf("%s runs a health check of its own at every start", name)
		}
		inputs := strs(sink["inputs"])
		if len(inputs) != 1 || !strings.HasPrefix(inputs[0], name+"_") {
			t.Errorf("%s reads %v, not its own branch of the pipeline", name, inputs)
		}
	}

	// The Elasticsearch endpoint is the cluster, not its bulk path: Vector
	// adds that, and the panel's test posted to it.
	if got := strs(lookup2(sinks, "drain_ldr_es", "endpoints")); len(got) != 1 || got[0] != "https://search.example.com:9200" {
		t.Errorf("the Elasticsearch endpoints are %v", got)
	}
	if got := strs(lookup2(sinks, "drain_ldr_opensearch", "endpoints")); len(got) != 1 || got[0] != "https://os.example.com" {
		t.Errorf("an address given with /_bulk gives the endpoints %v", got)
	}
	// Syslog checks the certificate, against the name.
	if lookup(sinks["drain_ldr_syslog"].(map[string]any), "tls.verify_certificate") != "true" ||
		lookup(sinks["drain_ldr_syslog"].(map[string]any), "tls.verify_hostname") != "true" {
		t.Error("the syslog sink does not check the certificate it is sent to")
	}
}

// lookup follows a dotted path through nested maps and prints what is at
// the end of it.
func lookup(value map[string]any, path string) string {
	parts := strings.SplitN(path, ".", 2)
	next, ok := value[parts[0]]
	if !ok {
		return ""
	}
	if len(parts) == 1 {
		if _, nested := next.(map[string]any); nested {
			return ""
		}
		return fmt.Sprint(next)
	}
	inner, ok := next.(map[string]any)
	if !ok {
		return ""
	}
	return lookup(inner, parts[1])
}

func lookup2(sinks map[string]any, name, key string) any {
	sink, _ := sinks[name].(map[string]any)
	return sink[key]
}

// Only the namespaces the panel made for a team's environments are read, and
// the panel's own work in them — a backup — is not an app's log.
func TestOnlyTheTeamsNamespacesAreRead(t *testing.T) {
	_, document := parsed(t, RenderInput{Drains: everyKind(t)[:1]})
	apps := section(t, document, "sources", "skifity_apps")
	if str(apps["type"]) != "kubernetes_logs" {
		t.Fatalf("the apps' source is a %v", apps["type"])
	}
	if got := str(apps["extra_namespace_label_selector"]); got != "app.kubernetes.io/managed-by=skifity,skifity.com/team-id" {
		t.Errorf("the namespaces read are %q; the panel's own, the builds' and the cluster's carry no team", got)
	}
	if got := str(apps["extra_label_selector"]); got != "app.kubernetes.io/component!=backup" {
		t.Errorf("the pods read are %q", got)
	}
	if got := str(apps["read_from"]); got != "end" {
		t.Errorf("a new collector reads from the %q of the files already there, a backlog nobody asked for", got)
	}
	// Whose a line is comes from the namespace's label, which only the panel
	// writes, and the app's own labels say only which app.
	shape := str(section(t, document, "transforms", "skifity_apps_shape")["source"])
	if !strings.Contains(shape, `"team_id": .kubernetes.namespace_labels."skifity.com/team-id"`) {
		t.Errorf("a line's team is not read from its namespace:\n%s", shape)
	}
	if !strings.Contains(shape, "del(.kubernetes)") {
		t.Error("every label and annotation of the pod is shipped with every line")
	}
	// No drain asked for builds, so there is no source for them at all.
	if _, ok := section(t, document, "sources")["skifity_builds"]; ok {
		t.Error("the builds are read with no drain asking for them")
	}
}

// A drain receives its own team's lines and nobody else's; a drain limited
// to projects receives only theirs; builds only when asked for.
func TestEachDrainSelectsItsOwnLines(t *testing.T) {
	_, document := parsed(t, RenderInput{Drains: everyKind(t), BuildsNamespace: "skifity-builds"})
	transforms := section(t, document, "transforms")
	condition := func(id string) string {
		selected, ok := transforms["drain_"+id+"_select"].(map[string]any)
		if !ok {
			t.Fatalf("drain %s has no select step", id)
		}
		if str(selected["type"]) != "filter" || strs(selected["inputs"])[0] != "skifity_named" {
			t.Fatalf("drain %s selects from %v", id, selected["inputs"])
		}
		return lookup(selected, "condition.source")
	}

	if got := condition("ldr_http"); got != `.skifity.team_id == "team_acme" && .skifity.kind == "app"` {
		t.Errorf("the generic drain selects %s", got)
	}
	if got := condition("ldr_loki"); got != `.skifity.team_id == "team_acme" && includes(["prj_blog", "prj_shop"], .skifity.project_id) && .skifity.kind == "app"` {
		t.Errorf("the drain limited to two projects selects %s", got)
	}
	if got := condition("ldr_es"); got != `.skifity.team_id == "team_acme"` {
		t.Errorf("the drain that asked for builds selects %s", got)
	}
	if got := condition("ldr_datadog"); !strings.HasPrefix(got, `.skifity.team_id == "team_globex"`) {
		t.Errorf("globex's drain selects %s", got)
	}

	// One drain asked for builds, so they are read: from the builds'
	// namespace, and only the pods of a build that say whose they are.
	builds := section(t, document, "sources", "skifity_builds")
	if str(builds["extra_field_selector"]) != "metadata.namespace=skifity-builds" ||
		str(builds["extra_label_selector"]) != "app.kubernetes.io/component=build,skifity.com/team-id" {
		t.Errorf("the builds are read with %v", builds)
	}
	if shape := str(section(t, document, "transforms", "skifity_builds_shape")["source"]); !strings.Contains(shape,
		`"team_id": .kubernetes.pod_labels."skifity.com/team-id"`) || !strings.Contains(shape, `"kind": "build"`) {
		t.Errorf("a build line's team is not the build pod's:\n%s", shape)
	}
	if inputs := strs(section(t, document, "transforms", "skifity_named")["inputs"]); len(inputs) != 2 {
		t.Errorf("the names are put on %v", inputs)
	}
}

// A drain limited to projects that are all gone sends nothing, and when no
// drain sends anything there is nothing to run.
func TestADrainWithNothingToSendIsLeftOut(t *testing.T) {
	d := everyKind(t)[0]
	d.Scoped, d.Projects = true, nil
	config, document := parsed(t, RenderInput{Drains: []Drain{d}})
	if config.Sinks != 0 {
		t.Errorf("%d sinks for a drain whose projects are all gone", config.Sinks)
	}
	if _, ok := document["sinks"]; ok {
		t.Error("there are sinks with nothing to send")
	}
}

// Names are data in a table, not text in a program: a name with a quote, a
// comma or a template in it is that name and nothing else.
func TestNamesAreATableNotTheConfiguration(t *testing.T) {
	config, _ := parsed(t, RenderInput{Drains: everyKind(t)[:1], Names: everyName()})
	if bytes.Contains(config.YAML, []byte("Acme")) {
		t.Error("a team's name is written into the pipeline")
	}
	rows, err := csv.NewReader(bytes.NewReader(config.Names)).ReadAll()
	if err != nil {
		t.Fatalf("the names are not CSV: %v", err)
	}
	if len(rows) != 5 || strings.Join(rows[0], ",") != "kind,id,name" {
		t.Fatalf("the names table is %v", rows)
	}
	found := map[string]string{}
	for _, row := range rows[1:] {
		found[row[0]+"/"+row[1]] = row[2]
	}
	if found["team/team_acme"] != `Acme, "the" shop` || found["app/app_web"] != "Web {{ not a template }}" {
		t.Errorf("the names came back as %v", found)
	}

	// Changing a name changes the hash, so the collector is given the new
	// one; the same input is the same hash, so nothing is applied for
	// nothing.
	again, _ := parsed(t, RenderInput{Drains: everyKind(t)[:1], Names: everyName()})
	renamed := everyName()
	renamed[1].Name = "Storefront"
	other, _ := parsed(t, RenderInput{Drains: everyKind(t)[:1], Names: renamed})
	if again.Hash != config.Hash || other.Hash == config.Hash {
		t.Errorf("the hashes are %s, %s and %s", config.Hash, again.Hash, other.Hash)
	}
}

// Only the panel's own ids are written into the programs the collector runs,
// and one that could end a string is refused rather than written.
func TestNothingAPersonTypedIsWrittenIntoAProgram(t *testing.T) {
	d := everyKind(t)[1]
	d.Projects = []string{`prj_shop"] || true || ["`}
	if _, err := Render(RenderInput{Drains: []Drain{d}}); err == nil {
		t.Error("a project id with a quote in it was written into a condition")
	}
	d = everyKind(t)[0]
	d.TeamID = `team") || true || ("`
	if _, err := Render(RenderInput{Drains: []Drain{d}}); err == nil {
		t.Error("a team id with a quote in it was written into a condition")
	}

	// A value with YAML in it stays a value.
	d = everyKind(t)[0]
	d.Secrets = map[string]string{"header_value": `Bearer x" y: [z] # 'q' &a *b`}
	config, document := parsed(t, RenderInput{Drains: []Drain{d}})
	if got := lookup(section(t, document, "sinks", "drain_ldr_http"), "request.headers.Authorization"); got != d.Secrets["header_value"] {
		t.Errorf("the header came back as %q", got)
	}
	if config.Sinks != 1 {
		t.Errorf("%d sinks", config.Sinks)
	}
}
