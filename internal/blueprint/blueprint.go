// Package blueprint reads skifity.yaml: an environment described in a file
// that lives in the repository, reviewed like code and applied by
// `skifity plan` and `skifity apply`.
//
// Render has render.yaml, DigitalOcean an app spec, Porter porter.yaml,
// Railway railway.json. What they share is the reason to have one: the
// settings that make an app work are in a pull request next to the code that
// needs them, instead of in somebody's memory of which boxes they ticked.
//
// This package only reads the file and works out what would change. It has no
// network and no store: the CLI fetches what the environment has now through
// the API, asks Plan what to do, and sends what Plan says through the same
// API, so every change is checked, authorized and audited the way a click in
// the panel is.
//
// Nothing is deleted. What the environment has and the file does not is said
// and left alone: a line removed from a file is a much smaller act than an app
// or a database removed from a cluster, and the two should not be one step.
package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"skifity/internal/dbsvc/engine"

	"skifity/internal/builder"
	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/kube"
)

// FileName is where the CLI looks for a blueprint.
const FileName = "skifity.yaml"

// File is skifity.yaml.
type File struct {
	Apps      map[string]App      `json:"apps"`
	Databases map[string]Database `json:"databases,omitempty"`
}

// App is one app. A field left out is left as it is; the file says what it
// wants, not everything there is.
type App struct {
	// Where it comes from: a repository or an image. An app with neither can
	// only describe one that exists already — a folder sent with `skifity up`.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	Root   string `json:"root,omitempty"`
	Image  string `json:"image,omitempty"`
	// Git is the name of the team's Git connection a private repository is
	// read through, as it is listed under Settings. A public one needs none.
	Git string `json:"git,omitempty"`

	Builder    string `json:"builder,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
	Build      string `json:"build,omitempty"`
	Static     string `json:"static,omitempty"`
	Start      string `json:"start,omitempty"`
	Release    string `json:"release,omitempty"`
	Port       *int   `json:"port,omitempty"`
	Internal   *bool  `json:"internal,omitempty"`
	// Health is how its instances are checked; see Health.
	Health *Health `json:"health,omitempty"`
	// Watch is the paths a push has to touch to deploy the app.
	Watch []string `json:"watch,omitempty"`

	Instances *int       `json:"instances,omitempty"`
	Autoscale *Autoscale `json:"autoscale,omitempty"`
	Resources *Resources `json:"resources,omitempty"`

	// Variables are plain values: this file is in a repository, so anything
	// in it is not a secret. Secrets names the ones that are, whose values
	// are set in the panel or with `skifity env set --secret`.
	Variables map[string]string `json:"variables,omitempty"`
	Secrets   []string          `json:"secrets,omitempty"`

	Processes map[string]Process  `json:"processes,omitempty"`
	Domains   []string            `json:"domains,omitempty"`
	Schedules map[string]Schedule `json:"schedules,omitempty"`
	// Databases links a database by name to the variable it is read from.
	Databases map[string]string `json:"databases,omitempty"`

	DeployOnPush *bool `json:"deploy_on_push,omitempty"`
	// DeployTrigger is branch, every push to the branch, or tag, only a pushed
	// tag whose name matches TagPattern.
	DeployTrigger string `json:"deploy_trigger,omitempty"`
	TagPattern    string `json:"tag_pattern,omitempty"`
	Previews      *bool  `json:"previews,omitempty"`
	PreviewSeed   string `json:"preview_seed,omitempty"`
}

// Health is how an app's instances are checked.
//
// `health: /healthz` is the path, which is all the file took before the rest
// could be chosen, and still means what it meant: an HTTP check of that path.
// The long form chooses the rest, each left out as it is:
//
//	health: {check: http, path: /healthz, start: 600, timeout: 5}
//	health: {check: none}
//
// start and timeout are seconds: how long a new instance may take to answer,
// and how long one check waits for it.
type Health struct {
	Check   string `json:"check,omitempty"`
	Path    string `json:"path,omitempty"`
	Start   int    `json:"start,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// health is what the file says about the app's health check, with nothing
// where it says nothing.
func (a App) health() Health {
	if a.Health == nil {
		return Health{}
	}
	return *a.Health
}

// UnmarshalJSON takes the path on its own as well as the long form.
func (h *Health) UnmarshalJSON(data []byte) error {
	var path string
	if err := json.Unmarshal(data, &path); err == nil {
		*h = Health{Path: path}
		return nil
	}
	type plain Health
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var long plain
	if err := decoder.Decode(&long); err != nil {
		return err
	}
	*h = Health(long)
	return nil
}

// Autoscale is instances decided by load.
type Autoscale struct {
	Min    int `json:"min"`
	Max    int `json:"max"`
	CPU    int `json:"cpu,omitempty"`
	Memory int `json:"memory,omitempty"`
}

// Resources is what one instance reserves and may use, in millicores and MB.
type Resources struct {
	CPU         int `json:"cpu,omitempty"`
	CPULimit    int `json:"cpu_limit,omitempty"`
	Memory      int `json:"memory,omitempty"`
	MemoryLimit int `json:"memory_limit,omitempty"`
}

// Process is a worker or other process beside the app: a command, or a
// command and a number of instances.
type Process struct {
	Command   string `json:"command"`
	Instances *int   `json:"instances,omitempty"`
}

// UnmarshalJSON takes `worker: celery -A shop worker` as well as the long
// form, because the short one is what a Procfile line looks like.
func (p *Process) UnmarshalJSON(data []byte) error {
	var command string
	if err := json.Unmarshal(data, &command); err == nil {
		*p = Process{Command: command}
		return nil
	}
	type plain Process
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var long plain
	if err := decoder.Decode(&long); err != nil {
		return err
	}
	*p = Process(long)
	return nil
}

// Schedule is a command on a five-field cron schedule.
type Schedule struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
}

// Database is a managed database.
type Database struct {
	Engine    string `json:"engine"`
	Version   string `json:"version,omitempty"`
	Storage   int    `json:"storage,omitempty"`
	Instances int    `json:"instances,omitempty"`
}

var (
	// validKey is a name that is its own slug: an underscore or two hyphens
	// would be folded into one hyphen by the panel, and the file would then
	// name an app the environment calls something else.
	validKey      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	validVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Engines a database can be: the panel's own catalogue, so a file can name
// every engine the panel runs and nothing it does not.
var Engines = engine.Names()

// Parse reads and checks a blueprint. Every problem is reported at once, so
// a file is fixed in one pass rather than one error at a time.
func Parse(data []byte) (File, error) {
	var file File
	if err := yaml.UnmarshalStrict(data, &file); err != nil {
		return File{}, errdoc.BlueprintInvalid(cleanYAMLError(err.Error()))
	}
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if len(file.Apps) == 0 && len(file.Databases) == 0 {
		say("it describes no apps and no databases")
	}
	for _, name := range sortedKeys(file.Databases) {
		database := file.Databases[name]
		if !validKey.MatchString(name) || strings.Contains(name, "--") {
			say("database %q: a name is lowercase letters, digits and single hyphens", name)
		}
		if _, clash := file.Apps[name]; clash {
			say("%s is both an app and a database; in one environment they need names of their own", name)
		}
		if !contains(Engines, database.Engine) {
			say("database %s: engine is one of %s, not %q", name, strings.Join(Engines, ", "), database.Engine)
		}
		if database.Storage < 0 || database.Instances < 0 {
			say("database %s: storage and instances cannot be negative", name)
		}
	}
	for _, name := range sortedKeys(file.Apps) {
		app := file.Apps[name]
		where := "app " + name
		if !validKey.MatchString(name) || strings.Contains(name, "--") {
			say("app %q: a name is lowercase letters, digits and single hyphens", name)
		}
		if app.Repo != "" && app.Image != "" {
			say("%s: it comes from a repository or an image, not both", where)
		}
		if app.Git != "" && app.Repo == "" {
			say("%s: git names the connection a repository is read through, and there is no repo", where)
		}
		if app.Image != "" && (app.Build != "" || app.Dockerfile != "" || app.Builder != "") {
			say("%s: an image is not built, so build, builder and dockerfile do not apply", where)
		}
		if app.Port != nil && (*app.Port < 1 || *app.Port > 65535) {
			say("%s: port %d is not a port", where, *app.Port)
		}
		if trigger := strings.TrimSpace(app.DeployTrigger); trigger != "" &&
			trigger != gitsrc.DeployOnBranch && trigger != gitsrc.DeployOnTag {
			say("%s: deploy_trigger is branch or tag, not %q", where, app.DeployTrigger)
		}
		if pattern := strings.TrimSpace(app.TagPattern); pattern != "" && gitsrc.ValidTagPattern(pattern) != nil {
			say("%s: tag_pattern %q is not a pattern such as v* or release-*", where, app.TagPattern)
		}
		if h := app.Health; h != nil {
			if check := strings.ToLower(strings.TrimSpace(h.Check)); check != "" && !kube.ValidHealthCheck(check) {
				say("%s: health check is http, tcp or none, not %q", where, h.Check)
			}
			if h.Start != 0 && (h.Start < kube.MinHealthStartSeconds || h.Start > kube.MaxHealthStartSeconds) {
				say("%s: health start is from %d to %d seconds, not %d", where,
					kube.MinHealthStartSeconds, kube.MaxHealthStartSeconds, h.Start)
			}
			if h.Timeout != 0 && (h.Timeout < kube.MinHealthTimeoutSeconds || h.Timeout > kube.MaxHealthTimeoutSeconds) {
				say("%s: health timeout is from %d to %d seconds, not %d", where,
					kube.MinHealthTimeoutSeconds, kube.MaxHealthTimeoutSeconds, h.Timeout)
			}
		}
		if app.Instances != nil && app.Autoscale != nil {
			say("%s: give instances or autoscale, not both", where)
		}
		if app.Instances != nil && (*app.Instances < 0 || *app.Instances > 100) {
			say("%s: instances is between 0 and 100", where)
		}
		if a := app.Autoscale; a != nil && (a.Min < 1 || a.Max < a.Min || a.Max > 100 || (a.CPU == 0 && a.Memory == 0)) {
			say("%s: autoscale needs min of at least 1, max from min to 100, and a cpu or memory target", where)
		}
		for key := range app.Variables {
			if !validVariable.MatchString(key) {
				say("%s: %q is not a variable name", where, key)
			}
			if contains(app.Secrets, key) {
				say("%s: %s is listed as a secret and given a value here, where it is not one", where, key)
			}
		}
		for _, key := range app.Secrets {
			if !validVariable.MatchString(key) {
				say("%s: %q is not a variable name", where, key)
			}
		}
		for _, process := range sortedKeys(app.Processes) {
			if normalised, ok := builder.ProcessName(process); !ok || normalised != process {
				say("%s: %q cannot be a process's name; it is lowercase letters, digits and hyphens, and not web or release", where, process)
			}
			if strings.TrimSpace(app.Processes[process].Command) == "" {
				say("%s: process %s has no command", where, process)
			}
		}
		for _, schedule := range sortedKeys(app.Schedules) {
			s := app.Schedules[schedule]
			if strings.TrimSpace(s.Schedule) == "" || strings.TrimSpace(s.Command) == "" {
				say("%s: schedule %s needs a schedule and a command", where, schedule)
			} else if _, err := cron.Canonical(strings.TrimSpace(s.Schedule)); err != nil {
				say("%s: schedule %s: %q is not five cron fields, such as \"0 3 * * *\"", where, schedule, s.Schedule)
			}
		}
		for database, variable := range app.Databases {
			if variable != "" && !validVariable.MatchString(variable) {
				say("%s: database %s is linked as %q, which is not a variable name", where, database, variable)
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return File{}, errdoc.BlueprintInvalid(strings.Join(problems, "; "))
	}
	normalize(&file)
	return file, nil
}

// normalize writes the file's values the way the panel stores them — trimmed,
// a root without its leading slash, a schedule in its canonical form, a
// hostname bare and lowercase — so that a plan made right after an apply is
// empty rather than the same change again, forever.
func normalize(file *File) {
	for name, app := range file.Apps {
		for _, field := range []*string{&app.Repo, &app.Branch, &app.Image, &app.Git, &app.Builder, &app.Dockerfile,
			&app.Build, &app.Static, &app.Start, &app.Release, &app.PreviewSeed,
			&app.DeployTrigger, &app.TagPattern} {
			*field = strings.TrimSpace(*field)
		}
		if app.Health != nil {
			app.Health.Path = strings.TrimSpace(app.Health.Path)
			app.Health.Check = strings.ToLower(strings.TrimSpace(app.Health.Check))
		}
		app.Root = strings.TrimPrefix(strings.TrimSpace(app.Root), "/")
		watch := app.Watch[:0:0]
		for _, pattern := range app.Watch {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				watch = append(watch, pattern)
			}
		}
		app.Watch = watch
		for i, hostname := range app.Domains {
			app.Domains[i] = kube.CleanHostname(hostname)
		}
		for key, process := range app.Processes {
			process.Command = strings.TrimSpace(process.Command)
			app.Processes[key] = process
		}
		for key, schedule := range app.Schedules {
			schedule.Command = strings.TrimSpace(schedule.Command)
			schedule.Schedule, _ = cron.Canonical(strings.TrimSpace(schedule.Schedule))
			app.Schedules[key] = schedule
		}
		file.Apps[name] = app
	}
}

// cleanYAMLError drops the library's prefix, which names a Go type nobody
// writing YAML has heard of.
func cleanYAMLError(message string) string {
	message = strings.TrimPrefix(message, "error unmarshaling JSON: while decoding JSON: ")
	message = strings.ReplaceAll(message, "json: ", "")
	return message
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
