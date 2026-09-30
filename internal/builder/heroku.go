package builder

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// What a repository written for Heroku already says about itself.
//
// A Procfile names the processes, and its release line is the command Heroku
// runs after the build and before the new version takes traffic — exactly
// what a release command is here. An app.json lists the settings the app
// reads, with defaults and descriptions and which ones to generate, and the
// add-ons it needs. Railpack reads the Procfile's web line on its own; nothing
// read the rest, so a migration that ran on every Heroku deploy did not run
// here, and a repository that says "I need Postgres" in so many words got
// asked about it anyway.

// ParseProcfile reads a Procfile into process names and commands, in the
// order they are written.
func ParseProcfile(text string) []Process {
	var out []Process
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, command, found := strings.Cut(line, ":")
		name, command = strings.TrimSpace(name), strings.TrimSpace(command)
		if !found || name == "" || command == "" || strings.ContainsAny(name, " \t") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Process{Name: name, Command: command})
	}
	return out
}

// Process is one line of a Procfile.
type Process struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// applyProcfile takes the web and release lines, and names the rest.
func applyProcfile(d *Detection, tree Tree) {
	processes := ParseProcfile(tree.Read("Procfile"))
	for _, process := range processes {
		switch process.Name {
		case "web":
			// The repository saying how it starts beats a guess from its
			// framework. A Dockerfile's own CMD is the author's decision too,
			// and the Procfile beside one is usually left over from before.
			if d.Builder != BuilderDockerfile && d.Builder != BuilderStatic {
				d.StartCommand = process.Command
			}
		case "release":
			d.ReleaseCommand = process.Command
			d.note("procfile_release", "The Procfile's release line runs after each build and before the new version takes traffic.")
		default:
			name, ok := ProcessName(process.Name)
			if !ok {
				d.note("procfile_bad_name", fmt.Sprintf(
					"The Procfile's %s line cannot be a process under that name: add it on the app's page "+
						"under a lowercase name of up to 20 letters, digits and hyphens.", process.Name), "name", process.Name)
				continue
			}
			process.Name = name
			d.Processes = append(d.Processes, process)
		}
	}
	if len(d.Processes) > 0 {
		names := make([]string, 0, len(d.Processes))
		for _, process := range d.Processes {
			names = append(names, process.Name)
		}
		d.note("procfile_processes", fmt.Sprintf(
			"The Procfile also names %s, which run beside the app as its processes: the same build and "+
				"variables, a command of their own, no port.", strings.Join(names, ", ")), "names", strings.Join(names, ", "))
	}
}

// processName is the rule kube.ValidProcessName enforces, which this package
// cannot import; a test in internal/api checks the two agree.
var processName = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,18}[a-z0-9])?$`)

// ProcessName turns a Procfile's name for a process into one a process can
// have here — Heroku allows capitals and underscores, a Kubernetes name does
// not — or reports that it cannot be one.
func ProcessName(procfile string) (string, bool) {
	name := strings.ToLower(strings.ReplaceAll(procfile, "_", "-"))
	if !processName.MatchString(name) || name == "web" || name == "release" {
		return "", false
	}
	return name, true
}

// appJSON is the part of Heroku's app.json that says what the app needs.
type appJSON struct {
	Env     map[string]json.RawMessage `json:"env"`
	Addons  []json.RawMessage          `json:"addons"`
	Scripts struct {
		Postdeploy json.RawMessage `json:"postdeploy"`
	} `json:"scripts"`
}

type appJSONVariable struct {
	Description string  `json:"description"`
	Value       *string `json:"value"`
	Required    *bool   `json:"required"`
	Generator   string  `json:"generator"`
}

// herokuAddons are the add-ons that are a database this panel can name, with
// the variable Heroku sets for each.
var herokuAddons = map[string]struct{ engine, variable string }{
	"heroku-postgresql": {EnginePostgres, "DATABASE_URL"},
	"heroku-redis":      {EngineRedis, "REDIS_URL"},
	"rediscloud":        {EngineRedis, "REDISCLOUD_URL"},
	"jawsdb":            {EngineMySQL, "JAWSDB_URL"},
	"jawsdb-maria":      {EngineMySQL, "JAWSDB_MARIA_URL"},
	"cleardb":           {EngineMySQL, "CLEARDB_DATABASE_URL"},
	"mongolab":          {EngineMongoDB, "MONGODB_URI"},
}

// applyAppJSON reads app.json's add-ons into database needs and its settings
// into the variables the form starts with.
func applyAppJSON(d *Detection, tree Tree) {
	raw := tree.Read("app.json")
	if raw == "" {
		return
	}
	var manifest appJSON
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		d.note("app_json_unreadable", "app.json could not be read: "+err.Error()+".", "error", err.Error())
		return
	}

	for _, addon := range manifest.Addons {
		name, variable := addonName(addon)
		known, ok := herokuAddons[name]
		if !ok {
			continue
		}
		if variable == "" {
			variable = known.variable
		}
		need := Need{Kind: NeedDatabase, Engine: known.engine, Provided: isProvided(known.engine),
			Evidence: name, Source: "app.json", Variable: variable}
		replaced := false
		for i, existing := range d.Needs {
			// What app.json names is what the app was deployed with, so it
			// wins over a guess from a driver in the manifest.
			if existing.Kind == NeedDatabase && existing.Engine == known.engine {
				d.Needs[i], replaced = need, true
				break
			}
		}
		if !replaced {
			d.Needs = append(d.Needs, need)
		}
	}

	var required []string
	var template strings.Builder
	names := make([]string, 0, len(manifest.Env))
	for name := range manifest.Env {
		if envName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		variable := readAppJSONVariable(manifest.Env[name])
		if variable.Description != "" {
			fmt.Fprintf(&template, "# %s\n", firstLineOf(variable.Description))
		}
		switch {
		case variable.Generator == "secret":
			fmt.Fprintf(&template, "%s=%s\n", name, newSecret())
		case variable.Value != nil && !strings.ContainsAny(*variable.Value, "\r\n"):
			fmt.Fprintf(&template, "%s=%s\n", name, dotenvValue(*variable.Value))
		case variable.Required == nil || *variable.Required:
			// Heroku asks for it at deploy time; here the form says it is
			// missing. A line with no value would be a variable set to "".
			required = append(required, name)
			fmt.Fprintf(&template, "# %s=\n", name)
		default:
			fmt.Fprintf(&template, "# %s= (optional)\n", name)
		}
	}
	d.EnvTemplate = strings.TrimSpace(template.String())
	if d.EnvTemplate != "" {
		d.EnvTemplate += "\n"
	}
	if len(required) > 0 {
		merged := false
		for i, existing := range d.Needs {
			if existing.Kind == NeedVariables {
				d.Needs[i].Variables = mergeNames(existing.Variables, required)
				merged = true
			}
		}
		if !merged {
			d.Needs = append(d.Needs, Need{Kind: NeedVariables, Source: "app.json", Variables: required})
		}
	}

	var postdeploy string
	if json.Unmarshal(manifest.Scripts.Postdeploy, &postdeploy) != nil {
		var object struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(manifest.Scripts.Postdeploy, &object)
		postdeploy = object.Command
	}
	// Heroku runs postdeploy once in each new review app, which is what a
	// seed for previews is here.
	if postdeploy = strings.TrimSpace(postdeploy); postdeploy != "" {
		d.PreviewSeed = postdeploy
		d.note("app_json_postdeploy", fmt.Sprintf(
			"app.json's postdeploy script, `%s`, becomes the seed for previews: it runs once in each new one. "+
				"Run it here once with `skifity run -- %s`.", postdeploy, postdeploy), "command", postdeploy)
	}
}

func addonName(raw json.RawMessage) (name, variable string) {
	var plan string
	if json.Unmarshal(raw, &plan) != nil {
		var object struct {
			Plan string `json:"plan"`
			As   string `json:"as"`
		}
		if json.Unmarshal(raw, &object) != nil {
			return "", ""
		}
		plan = object.Plan
		if as := strings.ToUpper(strings.TrimSpace(object.As)); as != "" && envName.MatchString(as+"_URL") {
			variable = as + "_URL"
		}
	}
	name, _, _ = strings.Cut(strings.TrimSpace(plan), ":")
	return name, variable
}

func readAppJSONVariable(raw json.RawMessage) appJSONVariable {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return appJSONVariable{Value: &value}
	}
	var variable appJSONVariable
	_ = json.Unmarshal(raw, &variable)
	return variable
}

// dotenvValue quotes a value the form's .env reader would otherwise trim.
func dotenvValue(value string) string {
	if value != strings.TrimSpace(value) || strings.HasPrefix(value, "#") {
		return `"` + value + `"`
	}
	return value
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func mergeNames(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range append(append([]string{}, a...), b...) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// newSecret is what Heroku's "generator": "secret" makes: 64 hexadecimal
// characters.
func newSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail on any platform Go supports; a template
		// with no value is still better than a predictable one.
		return ""
	}
	return hex.EncodeToString(buf)
}
