package logdrain

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"skifity/internal/version"
)

// The collector's configuration.
//
// One Vector on every server reads the container logs of the namespaces the
// panel manages and routes each line to the drains of the team it belongs
// to. The pipeline is the same shape for every drain:
//
//	skifity_apps (kubernetes_logs) ─┐
//	                                 ├─ skifity_named ─┬─ drain_<id>_select ─ drain_<id>_format ─ drain_<id>
//	skifity_builds (kubernetes_logs)─┘                 └─ …one branch per drain
//
// Which team a line belongs to is read from Kubernetes, not from the app: an
// app's line carries its namespace's team-id label, which only the panel
// writes, and a build's carries the build pod's, which only the panel writes.
// Nothing a line says about itself decides where it goes.
//
// Every value that came from a person — an address, a credential, a name —
// reaches this file through a YAML encoder or a CSV writer, never through
// string formatting, so none of it can end a string and start a setting. The
// only values written into the programs Vector runs (VRL) are ids the panel
// made, and those are checked against a pattern that holds no quote.

const (
	// ConfigDir is where the configuration is mounted in the collector.
	ConfigDir = "/etc/vector"
	// ConfigFile is the pipeline, and NamesFile the names it puts beside the
	// ids, both keys of the collector's Secret.
	ConfigFile = "vector.yaml"
	NamesFile  = "names.csv"
	// DataDir is where Vector keeps its place in each file: a scratch
	// volume of the pod's, never the server's disk.
	DataDir = "/var/lib/vector"
)

// The labels the panel puts on what it runs, which is how a line is told
// apart. See internal/kube.
const (
	labelTeamID     = "skifity.com/team-id"
	labelProjectID  = "skifity.com/project-id"
	labelAppID      = "skifity.com/app-id"
	labelDatabaseID = "skifity.com/database-id"
	labelProcess    = "skifity.com/process"
	labelDeployment = "skifity.com/deployment-id"
	labelComponent  = "app.kubernetes.io/component"
	labelManagedBy  = "app.kubernetes.io/managed-by"
	labelName       = "app.kubernetes.io/name"
	labelPartOf     = "app.kubernetes.io/part-of"
)

// NamespaceSelector is which namespaces the collector reads: the ones the
// panel made for a team's environments. The panel's own, the builds', the
// cluster's and anything somebody made with kubectl carry no team, and are
// not read.
var NamespaceSelector = labelManagedBy + "=" + version.Binary + "," + labelTeamID

// AppPodSelector leaves out the backup and restore Jobs the panel runs in an
// environment: they are the panel's work, not the team's apps, and what they
// print includes the short-lived addresses a backup is uploaded to.
var AppPodSelector = labelComponent + "!=backup"

// BuildsNamespaceSelector is the namespace builds run in, by the label the
// panel gives it.
var BuildsNamespaceSelector = version.LabelKey("component") + "=builds"

// BuildPodSelector is a build's pods, and only those that say whose they are.
// A vulnerability scan runs in the same namespace and is not a build.
var BuildPodSelector = labelComponent + "=build," + labelTeamID

// NameRow is one name the collector puts beside an id: a team's, a project's,
// an environment's by its namespace, or an app's.
type NameRow struct {
	Kind string // team, project, environment or app
	ID   string
	Name string
}

// RenderInput is everything the configuration is made from.
type RenderInput struct {
	// Drains are the enabled drains of every team, credentials open.
	Drains []Drain
	// Names are the names of what those teams have.
	Names []NameRow
	// BuildsNamespace is where builds run.
	BuildsNamespace string
}

// Config is the rendered configuration.
type Config struct {
	// YAML is the pipeline, and Names the table of names beside it.
	YAML  []byte
	Names []byte
	// Sinks is how many drains send anything.
	Sinks int
	// Hash identifies the pipeline, the names included, without carrying
	// any of it.
	Hash string
}

// MaxConfigBytes is what the collector's Secret is allowed to hold, under
// the megabyte Kubernetes stores in one object. The names of every app of
// every team with a drain are most of it.
const MaxConfigBytes = 900 << 10

// panelID is an id the panel made. Nothing else is written into a program
// the collector runs.
var panelID = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// Render makes the collector's configuration.
func Render(in RenderInput) (Config, error) {
	drains := slices.Clone(in.Drains)
	sort.Slice(drains, func(i, j int) bool { return drains[i].ID < drains[j].ID })

	builds := false
	for _, d := range drains {
		if d.IncludeBuilds && sends(d) {
			builds = true
		}
	}

	sources := map[string]any{
		"skifity_apps": map[string]any{
			"type":                           "kubernetes_logs",
			"extra_namespace_label_selector": NamespaceSelector,
			"extra_label_selector":           AppPodSelector,
			// Where the collector is in each file is kept in DataDir for
			// as long as its pod lives, so a reload or a restart carries on
			// where it was. A collector that starts afresh — the first
			// drain, an upgrade — starts at the end of the files already
			// there rather than sending everything the kubelet still keeps
			// again, which on a service that bills by the gigabyte is a bill
			// for a backlog nobody asked for. A file that appears after it
			// has started is read from its beginning whatever this says, so
			// a new instance's first lines are sent.
			"read_from": "end",
			// A new instance is found within five seconds. The default is
			// a minute, and a one-off command is often over by then.
			"glob_minimum_cooldown_ms": 5000,
		},
	}
	named := []string{"skifity_apps_shape"}
	transforms := map[string]any{
		"skifity_apps_shape": remap([]string{"skifity_apps"}, appShape),
	}
	if builds {
		namespace := in.BuildsNamespace
		if namespace == "" {
			return Config{}, fmt.Errorf("a drain includes builds and the builds namespace is not known")
		}
		sources["skifity_builds"] = map[string]any{
			"type":                           "kubernetes_logs",
			"extra_namespace_label_selector": BuildsNamespaceSelector,
			"extra_field_selector":           "metadata.namespace=" + namespace,
			"extra_label_selector":           BuildPodSelector,
			"read_from":                      "end",
			"glob_minimum_cooldown_ms":       2000,
		}
		transforms["skifity_builds_shape"] = remap([]string{"skifity_builds"}, buildShape)
		named = append(named, "skifity_builds_shape")
	}
	transforms["skifity_named"] = remap(named, nameLines)

	sinks := map[string]any{}
	for _, d := range drains {
		if !panelID.MatchString(d.ID) || !panelID.MatchString(d.TeamID) {
			return Config{}, fmt.Errorf("the drain %q has an id the collector's configuration cannot hold", d.ID)
		}
		if err := d.Validate(); err != nil {
			return Config{}, fmt.Errorf("the drain %s: %w", d.ID, err)
		}
		if !sends(d) {
			continue
		}
		condition, err := selectCondition(d)
		if err != nil {
			return Config{}, err
		}
		prefix := "drain_" + d.ID
		transforms[prefix+"_select"] = map[string]any{
			"type":      "filter",
			"inputs":    []string{"skifity_named"},
			"condition": map[string]any{"type": "vrl", "source": condition},
		}
		input := prefix + "_select"
		if program := formats[d.Kind]; program != "" {
			transforms[prefix+"_format"] = remap([]string{input}, program)
			input = prefix + "_format"
		}
		sink, err := sinkFor(d)
		if err != nil {
			return Config{}, fmt.Errorf("the drain %s: %w", d.ID, err)
		}
		sink["inputs"] = []string{input}
		// A drain that cannot deliver drops its own newest lines once a
		// thousand are waiting, rather than filling up and holding the
		// pipeline behind it: Vector's default is to block, and blocking
		// here would stop every other team's drains with it.
		sink["buffer"] = map[string]any{"type": "memory", "max_events": 1000, "when_full": "drop_newest"}
		// The panel's test is the health check; Vector's would be a second
		// request at every start with nothing listening to the answer.
		sink["healthcheck"] = map[string]any{"enabled": false}
		sinks[prefix] = sink
	}

	document := map[string]any{
		"data_dir": DataDir,
		"api":      map[string]any{"enabled": false},
		"enrichment_tables": map[string]any{
			"skifity_names": map[string]any{
				"type": "file",
				"file": map[string]any{
					"path":     ConfigDir + "/" + NamesFile,
					"encoding": map[string]any{"type": "csv"},
				},
			},
		},
		"sources":    sources,
		"transforms": transforms,
	}
	if len(sinks) > 0 {
		document["sinks"] = sinks
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return Config{}, fmt.Errorf("render the collector's configuration: %w", err)
	}
	names, err := renderNames(in.Names)
	if err != nil {
		return Config{}, err
	}
	header := []byte("# Written by " + version.Name + " from the team's log drains. Changes here are overwritten.\n")
	out := Config{YAML: append(header, encoded...), Names: names, Sinks: len(sinks)}
	if size := len(out.YAML) + len(out.Names); size > MaxConfigBytes {
		return Config{}, fmt.Errorf("the collector's configuration comes to %d KB, and a Secret holds %d KB at most",
			size>>10, MaxConfigBytes>>10)
	}
	sum := sha256.New()
	sum.Write(out.YAML)
	sum.Write([]byte{0})
	sum.Write(out.Names)
	out.Hash = hex.EncodeToString(sum.Sum(nil))[:16]
	return out, nil
}

// sends reports whether a drain has anything to send: a drain limited to
// projects that are all gone has nothing.
func sends(d Drain) bool { return !d.Scoped || len(d.Projects) > 0 }

func remap(inputs []string, program string) map[string]any {
	return map[string]any{"type": "remap", "inputs": inputs, "source": program}
}

// vrlString writes a panel id as a VRL string. Only ids reach here; see
// panelID.
func vrlString(id string) (string, error) {
	if !panelID.MatchString(id) {
		return "", fmt.Errorf("%q is not an id the collector's configuration can hold", id)
	}
	return `"` + id + `"`, nil
}

// selectCondition is which lines a drain receives: its team's, of its
// projects if it is limited to some, and of builds if it asked for them.
func selectCondition(d Drain) (string, error) {
	team, err := vrlString(d.TeamID)
	if err != nil {
		return "", err
	}
	parts := []string{".skifity.team_id == " + team}
	if d.Scoped {
		projects := make([]string, 0, len(d.Projects))
		for _, id := range slices.Sorted(slices.Values(d.Projects)) {
			quoted, err := vrlString(id)
			if err != nil {
				return "", err
			}
			projects = append(projects, quoted)
		}
		parts = append(parts, "includes(["+strings.Join(projects, ", ")+"], .skifity.project_id)")
	}
	if !d.IncludeBuilds {
		parts = append(parts, `.skifity.kind == "app"`)
	}
	return strings.Join(parts, " && "), nil
}

// appShape turns a line from an environment's namespace into the line every
// drain sends: the message, its time and stream, and what it is about under
// "skifity". Kubernetes' own metadata — every label and annotation of the pod
// — is dropped rather than shipped: it is most of the line's size, and a
// service that bills by the gigabyte bills for it.
const appShape = `.skifity = compact({
  "kind": "app",
  "team_id": .kubernetes.namespace_labels."` + labelTeamID + `",
  "project_id": .kubernetes.namespace_labels."` + labelProjectID + `",
  "environment": .kubernetes.pod_labels."` + labelPartOf + `",
  "app": .kubernetes.pod_labels."` + labelName + `",
  "app_id": .kubernetes.pod_labels."` + labelAppID + `",
  "database_id": .kubernetes.pod_labels."` + labelDatabaseID + `",
  "process": .kubernetes.pod_labels."` + labelProcess + `",
  "namespace": .kubernetes.pod_namespace,
  "instance": .kubernetes.pod_name,
  "container": .kubernetes.container_name,
  "server": .kubernetes.pod_node_name
})
del(.kubernetes)
del(.file)
del(.source_type)
`

// buildShape does the same for a build's pod, whose team is its own label:
// builds share one namespace.
const buildShape = `.skifity = compact({
  "kind": "build",
  "team_id": .kubernetes.pod_labels."` + labelTeamID + `",
  "project_id": .kubernetes.pod_labels."` + labelProjectID + `",
  "app_id": .kubernetes.pod_labels."` + labelAppID + `",
  "deployment_id": .kubernetes.pod_labels."` + labelDeployment + `",
  "namespace": .kubernetes.pod_namespace,
  "instance": .kubernetes.pod_name,
  "container": .kubernetes.container_name,
  "server": .kubernetes.pod_node_name
})
del(.kubernetes)
del(.file)
del(.source_type)
`

// nameLines puts the names beside the ids, from names.csv: the team's and
// the project's, the environment's in place of its slug, and the app's in
// place of its slug. A row that is not there yet — an app made since the
// table was written — leaves the slug, which is a name too.
const nameLines = `team, err = get_enrichment_table_record("skifity_names", {"kind": "team", "id": to_string(.skifity.team_id) ?? ""})
if err == null { .skifity.team = team.name }
project, err = get_enrichment_table_record("skifity_names", {"kind": "project", "id": to_string(.skifity.project_id) ?? ""})
if err == null { .skifity.project = project.name }
environment, err = get_enrichment_table_record("skifity_names", {"kind": "environment", "id": to_string(.skifity.namespace) ?? ""})
if err == null { .skifity.environment = environment.name }
app, err = get_enrichment_table_record("skifity_names", {"kind": "app", "id": to_string(.skifity.app_id) ?? ""})
if err == null { .skifity.app = app.name }
`

// formats reshape a line for the services that read particular fields.
var formats = map[string]string{
	// Loki files a line under its labels; one missing is a label the sink
	// cannot render, so each has a value.
	KindLoki: `.skifity.team = .skifity.team || .skifity.team_id || "unknown"
.skifity.project = .skifity.project || .skifity.project_id || "unknown"
.skifity.environment = .skifity.environment || "none"
.skifity.app = .skifity.app || "none"
`,
	// Kibana and OpenSearch Dashboards look for @timestamp.
	KindElasticsearch: `."@timestamp" = del(.timestamp)
`,
	// Datadog files a line under its source, service and host.
	KindDatadog: `.ddsource = "` + version.Binary + `"
.service = .skifity.app || "` + version.Binary + `"
.hostname = .skifity.server
.ddtags = "source:` + version.Binary + `"
`,
	// Better Stack reads the time from dt.
	KindBetterStack: `.dt = del(.timestamp)
`,
	// Syslog's APP-NAME, PROCID and HOSTNAME.
	KindSyslog: `.app_name = .skifity.app || "` + version.Binary + `"
.proc_id = .skifity.instance
.host = .skifity.server
`,
}

// sinkFor is a drain's Vector sink, its credentials included: this is the one
// place they are written down outside the panel's database, and the object it
// ends up in is a Secret.
func sinkFor(d Drain) (map[string]any, error) {
	endpoint, err := d.Endpoint()
	if err != nil {
		return nil, err
	}
	s := d.Settings
	switch d.Kind {
	case KindHTTP:
		sink := map[string]any{
			"type":        "http",
			"uri":         endpoint.URL.String(),
			"method":      "post",
			"compression": "none",
			"encoding":    map[string]any{"codec": "json"},
			"framing":     map[string]any{"method": "newline_delimited"},
		}
		if name := s["header_name"]; name != "" {
			sink["request"] = map[string]any{"headers": map[string]any{name: d.Secrets["header_value"]}}
		}
		return sink, nil

	case KindLoki:
		base := *endpoint.URL
		path := base.Path
		base.Path, base.RawPath = "", ""
		sink := map[string]any{
			"type":                "loki",
			"endpoint":            base.String(),
			"path":                path,
			"encoding":            map[string]any{"codec": "json"},
			"out_of_order_action": "accept",
			"labels": map[string]any{
				"source":      version.Binary,
				"kind":        "{{ skifity.kind }}",
				"team":        "{{ skifity.team }}",
				"project":     "{{ skifity.project }}",
				"environment": "{{ skifity.environment }}",
				"app":         "{{ skifity.app }}",
			},
		}
		password := d.Secrets["password"]
		switch {
		case s["username"] != "":
			sink["auth"] = map[string]any{"strategy": "basic", "user": s["username"], "password": password}
		case password != "":
			sink["auth"] = map[string]any{"strategy": "bearer", "token": password}
		}
		if tenant := s["tenant_id"]; tenant != "" {
			sink["tenant_id"] = tenant
		}
		return sink, nil

	case KindElasticsearch:
		base := strings.TrimSuffix(endpoint.URL.String(), "/_bulk")
		api := "v8"
		if s["api"] == "7" {
			api = "v7"
		}
		sink := map[string]any{
			"type":        "elasticsearch",
			"endpoints":   []string{base},
			"api_version": api,
			"mode":        "bulk",
			"bulk":        map[string]any{"action": "index", "index": s["index"]},
			"compression": "none",
		}
		if key := d.Secrets["api_key"]; key != "" {
			sink["request"] = map[string]any{"headers": map[string]any{"Authorization": "ApiKey " + key}}
		} else if s["username"] != "" {
			sink["auth"] = map[string]any{"strategy": "basic", "user": s["username"], "password": d.Secrets["password"]}
		}
		return sink, nil

	case KindDatadog:
		return map[string]any{
			"type":            "datadog_logs",
			"site":            s["site"],
			"default_api_key": d.Secrets["api_key"],
			"compression":     "gzip",
		}, nil

	case KindAxiom:
		sink := map[string]any{
			"type":    "axiom",
			"dataset": s["dataset"],
			"token":   d.Secrets["token"],
		}
		if region := s["region"]; region != "" {
			sink["region"] = region
		}
		if org := s["org_id"]; org != "" {
			sink["org_id"] = org
		}
		return sink, nil

	case KindBetterStack:
		return map[string]any{
			"type":        "http",
			"uri":         endpoint.URL.String(),
			"method":      "post",
			"compression": "gzip",
			"encoding":    map[string]any{"codec": "json"},
			"auth":        map[string]any{"strategy": "bearer", "token": d.Secrets["source_token"]},
		}, nil

	case KindNewRelic:
		return map[string]any{
			"type":        "new_relic",
			"api":         "logs",
			"region":      s["region"],
			"account_id":  s["account_id"],
			"license_key": d.Secrets["license_key"],
			"compression": "gzip",
		}, nil

	case KindSyslog:
		return map[string]any{
			"type":    "socket",
			"mode":    "tcp",
			"address": endpoint.Address,
			// The certificate is checked, and against the name: the
			// panel's test checked the same one.
			"tls": map[string]any{"enabled": true, "verify_certificate": true, "verify_hostname": true},
			"encoding": map[string]any{
				"codec": "syslog",
				"syslog": map[string]any{
					"rfc":      "rfc5424",
					"app_name": ".app_name",
					"proc_id":  ".proc_id",
				},
			},
			"framing": map[string]any{"method": "newline_delimited"},
		}, nil
	}
	return nil, invalid("%q is not a kind of drain", d.Kind)
}

// renderNames writes the table of names as CSV. A CSV writer quotes what a
// name needs quoted, so a name is a name and nothing more.
func renderNames(rows []NameRow) ([]byte, error) {
	sorted := slices.Clone(rows)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].ID < sorted[j].ID
	})
	var out bytes.Buffer
	w := csv.NewWriter(&out)
	if err := w.Write([]string{"kind", "id", "name"}); err != nil {
		return nil, err
	}
	seen := map[[2]string]bool{}
	for _, row := range sorted {
		key := [2]string{row.Kind, row.ID}
		if row.ID == "" || row.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		if err := w.Write([]string{row.Kind, row.ID, cleanName(row.Name)}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("render the collector's names: %w", err)
	}
	return out.Bytes(), nil
}

// cleanName keeps a name to one line with no control characters in it.
func cleanName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, name)
}

// Summary is what the configuration says, for a log line: how many drains,
// and nothing that came from one.
func (c Config) Summary() string {
	encoded, _ := json.Marshal(map[string]any{"sinks": c.Sinks, "hash": c.Hash})
	return string(encoded)
}
