package blueprint

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// State is what an environment has now, as the API answers it.
type State struct {
	EnvironmentID string
	Apps          []AppState
	Databases     []store.Database
	// GitSources are the team's Git connections, read when the file names
	// one.
	GitSources []store.GitSource
}

// AppState is one app and what hangs off it.
type AppState struct {
	App       store.App
	Variables []store.Variable
	Processes []store.AppProcess
	Domains   []store.Domain
	Jobs      []store.AppJob
	// Links are its databases: variable name to database id.
	Links map[string]string
	// Deployed is whether it has ever been deployed. An app made by an apply
	// that stopped before its first deploy is still owed one.
	Deployed bool
	// Running is the version it runs — its newest deployment that succeeded —
	// and Pending its newest one still under way; nil when there is none.
	// Together they say what the stored settings have that nothing running
	// has yet: an apply whose deploy was refused stored them and stopped.
	Running, Pending *store.Deployment
}

// owed says why an app has to be deployed for what is already stored to run,
// or "" when nothing is owed or it cannot be told.
//
// It is read from the state rather than from the plan's own changes, because
// those are gone after the apply that made them: the next plan compared the
// file with what the apply had stored, found them equal, and never deployed.
func owed(have AppState) string {
	if have.Running == nil {
		return ""
	}
	app := have.App
	if app.SourceType == "image" {
		if app.Image == "" || have.Running.Image == app.Image || (have.Pending != nil && have.Pending.Image == app.Image) {
			return ""
		}
		return "to run " + app.Image + ", which is stored and not running yet"
	}
	// A value the build reads is in the image: one changed after the running
	// version was started, and after any deploy now under way, is not in it.
	since := have.Running.CreatedAt
	if have.Pending != nil && have.Pending.CreatedAt.After(since) {
		since = have.Pending.CreatedAt
	}
	for _, variable := range have.Variables {
		if variable.BuildTime && variable.UpdatedAt.After(since) {
			return "to build with the value of " + variable.Key + " it has now"
		}
	}
	return ""
}

// Step is one line of a plan, and what applying it sends.
type Step struct {
	// Op is "create", "change" or "note". A note is said and not applied.
	Op   string `json:"op"`
	Kind string `json:"kind"`
	// App is the app a step belongs to, when it belongs to one.
	App  string `json:"app,omitempty"`
	Name string `json:"name"`
	// Detail says what changes, in words: "3000 → 8080".
	Detail string `json:"detail,omitempty"`
	Call   *Call  `json:"-"`
}

// Call is an API request. Paths and string values may name something the
// plan creates before it, as {app:web} or {database:main}; apply fills in
// the id.
type Call struct {
	Method string
	Path   string
	Body   map[string]any
	// Creates is what this call makes, for the calls after it to name.
	Creates string
}

// Plan works out what applying a file to an environment would do, in the
// order it has to be done: databases before the apps linked to them, an app
// before its variables, and a new app's first deploy last, so it never
// starts without what the file gives it.
func Plan(file File, state State) ([]Step, error) {
	steps := []Step{}
	existingDatabase := map[string]store.Database{}
	for _, database := range state.Databases {
		existingDatabase[database.Name] = database
		existingDatabase[database.Slug] = database
	}

	for _, name := range sortedKeys(file.Databases) {
		want := file.Databases[name]
		have, ok := existingDatabase[name]
		if !ok {
			detail := want.Engine
			if want.Version != "" {
				detail += " " + want.Version
			}
			steps = append(steps, Step{Op: "create", Kind: "database", Name: name, Detail: detail, Call: &Call{
				Method: "POST", Path: "/api/environments/" + state.EnvironmentID + "/databases",
				Body: map[string]any{"name": name, "engine": want.Engine, "version": want.Version,
					"storage_gb": want.Storage, "instances": want.Instances},
				Creates: "database:" + name,
			}})
			continue
		}
		// A database's engine, version and size are not changed from a file:
		// each is a migration of somebody's data, and deserves the panel.
		var differs []string
		if have.Engine != want.Engine || (want.Version != "" && have.EngineVersion != want.Version) {
			differs = append(differs, fmt.Sprintf("is %s %s here and %s %s in the file",
				have.Engine, have.EngineVersion, want.Engine, want.Version))
		}
		if want.Storage > 0 && have.StorageGB != want.Storage {
			differs = append(differs, fmt.Sprintf("has %d GB here and %d GB in the file", have.StorageGB, want.Storage))
		}
		if len(differs) > 0 {
			steps = append(steps, Step{Op: "note", Kind: "database", Name: name,
				Detail: strings.Join(differs, ", and ") + "; a database is not changed from a file"})
		}
	}
	// databaseRef is a database's id, or the reference to the one the plan
	// makes, and its engine.
	databaseRef := func(name string) (string, string, bool) {
		if want, ok := file.Databases[name]; ok {
			if have, exists := existingDatabase[name]; exists {
				return have.ID, have.Engine, true
			}
			return "{database:" + name + "}", want.Engine, true
		}
		if have, ok := existingDatabase[name]; ok {
			return have.ID, have.Engine, true
		}
		return "", "", false
	}

	existingApp := map[string]AppState{}
	for _, app := range state.Apps {
		existingApp[app.App.Name] = app
		existingApp[app.App.Slug] = app
	}
	var deploys []Step
	for _, name := range sortedKeys(file.Apps) {
		want := file.Apps[name]
		have, exists := existingApp[name]
		ref := "{app:" + name + "}"
		if exists {
			ref = have.App.ID
		} else {
			gitSource, err := gitSourceID(name, want, state.GitSources)
			if err != nil {
				return nil, err
			}
			create, err := createApp(name, want, state.EnvironmentID, gitSource)
			if err != nil {
				return nil, err
			}
			steps = append(steps, create)
		}
		changes, redeploy := appSteps(name, ref, want, have, exists)
		if redeploy == "" && exists {
			redeploy = owed(have)
		}
		steps = append(steps, changes...)
		switch {
		case !exists || (!have.Deployed && have.App.SourceType != "upload"):
			// A new app's first deploy, or the one an apply that stopped
			// part-way never got to. A folder sent with `skifity up` is
			// deployed by sending it.
			deploys = append(deploys, Step{Op: "create", Kind: "deploy", App: name, Name: name,
				Detail: "its first deploy, once everything above is in place",
				Call:   &Call{Method: "POST", Path: "/api/apps/" + ref + "/deploy", Body: map[string]any{}}})
		case redeploy != "":
			deploys = append(deploys, Step{Op: "change", Kind: "deploy", App: name, Name: name, Detail: redeploy,
				Call: &Call{Method: "POST", Path: "/api/apps/" + ref + "/deploy", Body: map[string]any{}}})
		}

		for _, database := range sortedKeys(want.Databases) {
			id, engine, ok := databaseRef(database)
			if !ok {
				return nil, errdoc.BlueprintInvalid(fmt.Sprintf(
					"app %s links the database %s, which is neither in the file nor in the environment", name, database))
			}
			variable := want.Databases[database]
			if variable == "" {
				variable = DefaultVariable(engine)
			}
			if exists && have.Links[variable] == id {
				continue
			}
			steps = append(steps, Step{Op: "create", Kind: "link", App: name, Name: database, Detail: "as " + variable,
				Call: &Call{Method: "POST", Path: "/api/databases/" + id + "/link",
					Body: map[string]any{"app_id": ref, "var_name": variable}}})
		}
	}

	for _, app := range state.Apps {
		if _, ok := file.Apps[app.App.Name]; ok {
			continue
		}
		if _, ok := file.Apps[app.App.Slug]; ok {
			continue
		}
		steps = append(steps, Step{Op: "note", Kind: "app", Name: app.App.Name, Detail: "is here and not in the file; it is left alone"})
	}
	for _, database := range state.Databases {
		if _, ok := file.Databases[database.Name]; ok {
			continue
		}
		if _, ok := file.Databases[database.Slug]; ok {
			continue
		}
		steps = append(steps, Step{Op: "note", Kind: "database", Name: database.Name, Detail: "is here and not in the file; it is left alone"})
	}
	return append(steps, deploys...), nil
}

// gitSourceID is the id of the Git connection the file names for an app, or
// "" when it names none.
func gitSourceID(name string, want App, sources []store.GitSource) (string, error) {
	if want.Git == "" {
		return "", nil
	}
	for _, source := range sources {
		if source.Name == want.Git {
			return source.ID, nil
		}
	}
	return "", errdoc.BlueprintInvalid(fmt.Sprintf(
		"app %s is read through the Git connection %q, and the team has none of that name", name, want.Git))
}

// createApp is the request that makes an app, with what the create form
// takes; the rest follows as ordinary changes to it.
func createApp(name string, want App, envID, gitSource string) (Step, error) {
	body := map[string]any{"name": name, "deploy": false}
	if gitSource != "" {
		body["git_source_id"] = gitSource
	}
	detail := ""
	switch {
	case want.Image != "":
		body["source_type"], body["image"] = "image", want.Image
		detail = want.Image
	case want.Repo != "":
		body["source_type"], body["repo_url"] = "git", want.Repo
		detail = want.Repo
	default:
		return Step{}, errdoc.BlueprintInvalid(fmt.Sprintf("app %s is not in the environment, and without a repo or an image "+
			"it cannot be made from the file: send its folder once with `skifity up`, then the file describes it", name))
	}
	for key, value := range map[string]string{
		"branch": want.Branch, "root_dir": want.Root, "builder": want.Builder, "dockerfile_path": want.Dockerfile,
		"build_command": want.Build, "static_dir": want.Static, "start_command": want.Start,
		"release_command": want.Release, "health_path": want.Health, "watch_paths": strings.Join(want.Watch, "\n"),
		"deploy_trigger": want.DeployTrigger, "tag_pattern": want.TagPattern,
	} {
		if value != "" {
			body[key] = value
		}
	}
	if want.Port != nil {
		body["port"] = *want.Port
	}
	if want.Internal != nil {
		body["internal"] = *want.Internal
	}
	return Step{Op: "create", Kind: "app", App: name, Name: name, Detail: detail,
		Call: &Call{Method: "POST", Path: "/api/environments/" + envID + "/apps", Body: body, Creates: "app:" + name}}, nil
}

// appSteps are the changes to one app, new or not: its settings, scaling,
// variables, processes, domains and schedules. redeploy says why the app has
// to be deployed again for them to take effect, when one does.
func appSteps(name, ref string, want App, have AppState, exists bool) (steps []Step, redeploy string) {
	current := have.App

	// Settings, sent together. A new app got most of them when it was made.
	patch := map[string]any{}
	var changed []string
	field := func(label, key, wantValue, haveValue string) {
		if wantValue == "" || (exists && wantValue == haveValue) || (!exists && key != "preview_seed") {
			return
		}
		patch[key] = wantValue
		changed = append(changed, fmt.Sprintf("%s %q → %q", label, haveValue, wantValue))
	}
	if exists {
		steps = append(steps, sourceNotes(name, want, current)...)
		if current.SourceType == "image" && want.Image != "" && want.Image != current.Image {
			// An image app runs the image its last deployment ran: a new
			// one is a new deployment, not a setting.
			field("image", "image", want.Image, current.Image)
			redeploy = "to run " + want.Image
		}
		field("branch", "branch", want.Branch, current.Branch)
		field("root", "root_dir", want.Root, current.RootDir)
		field("builder", "builder", want.Builder, current.Builder)
		field("dockerfile", "dockerfile_path", want.Dockerfile, current.DockerfilePath)
		field("build", "build_command", want.Build, current.BuildCommand)
		field("static", "static_dir", want.Static, current.StaticDir)
		field("start", "start_command", want.Start, current.StartCommand)
		field("release", "release_command", want.Release, current.ReleaseCommand)
		field("health", "health_path", want.Health, current.HealthPath)
		field("watch", "watch_paths", strings.Join(want.Watch, "\n"), current.WatchPaths)
		field("deploy trigger", "deploy_trigger", want.DeployTrigger, current.DeployTrigger)
		field("tag pattern", "tag_pattern", want.TagPattern, current.TagPattern)
		if want.Port != nil && *want.Port != current.Port {
			patch["port"] = *want.Port
			changed = append(changed, fmt.Sprintf("port %d → %d", current.Port, *want.Port))
		}
		if want.Internal != nil && *want.Internal != current.Internal {
			patch["internal"] = *want.Internal
			changed = append(changed, fmt.Sprintf("internal %t → %t", current.Internal, *want.Internal))
		}
	}
	field("preview seed", "preview_seed", want.PreviewSeed, current.PreviewSeed)
	if want.DeployOnPush != nil && (!exists || *want.DeployOnPush != current.AutoDeploy) {
		patch["auto_deploy"] = *want.DeployOnPush
		changed = append(changed, fmt.Sprintf("deploy on push → %t", *want.DeployOnPush))
	}
	if want.Previews != nil && (!exists || *want.Previews != current.PreviewDeploys) {
		patch["preview_deploys"] = *want.Previews
		changed = append(changed, fmt.Sprintf("previews → %t", *want.Previews))
	}
	if r := want.Resources; r != nil {
		for _, c := range []struct {
			label, key string
			want, have int
		}{
			{"cpu", "cpu_request_m", r.CPU, current.CPURequestM},
			{"cpu limit", "cpu_limit_m", r.CPULimit, current.CPULimitM},
			{"memory", "mem_request_mb", r.Memory, current.MemRequestMB},
			{"memory limit", "mem_limit_mb", r.MemoryLimit, current.MemLimitMB},
		} {
			if c.want > 0 && (!exists || c.want != c.have) {
				patch[c.key] = c.want
				changed = append(changed, fmt.Sprintf("%s %d → %d", c.label, c.have, c.want))
			}
		}
	}
	if len(patch) > 0 {
		steps = append(steps, Step{Op: "change", Kind: "settings", App: name, Name: name,
			Detail: strings.Join(changed, ", "), Call: &Call{Method: "PATCH", Path: "/api/apps/" + ref, Body: patch}})
	}

	if s := scaling(want, current, exists); s != nil {
		s.App, s.Name = name, name
		s.Call.Path = "/api/apps/" + ref + "/scaling"
		steps = append(steps, *s)
	}

	// Variables, in one batch: one rollout for the lot.
	existing := map[string]store.Variable{}
	for _, variable := range have.Variables {
		existing[variable.Key] = variable
	}
	var set []map[string]any
	for _, key := range sortedKeys(want.Variables) {
		value := want.Variables[key]
		variable, ok := existing[key]
		switch {
		case !ok:
			steps = append(steps, Step{Op: "create", Kind: "variable", App: name, Name: key})
		case variable.IsSecret:
			steps = append(steps, Step{Op: "change", Kind: "variable", App: name, Name: key,
				Detail: "is a secret here; the file's value replaces it, and it will not be one"})
		case variable.Value != value:
			steps = append(steps, Step{Op: "change", Kind: "variable", App: name, Name: key,
				Detail: fmt.Sprintf("%q → %q", variable.Value, value)})
			// The build reads it, so the running image has the old value in it.
			if variable.BuildTime && current.SourceType != "image" && redeploy == "" {
				redeploy = "to build with the new value of " + key
			}
		default:
			continue
		}
		// A variable the build reads stays one: the file says its value, not
		// when it is read, and sending false would move it out of the build.
		set = append(set, map[string]any{"key": key, "value": value, "is_secret": false, "build_time": variable.BuildTime})
	}
	if len(set) > 0 {
		steps[len(steps)-1].Call = &Call{Method: "POST", Path: "/api/apps/" + ref + "/variables/batch",
			Body: map[string]any{"set": set, "unset": []string{}}}
	}
	for _, key := range want.Secrets {
		if _, ok := existing[key]; !ok {
			steps = append(steps, Step{Op: "note", Kind: "secret", App: name, Name: key,
				Detail: "is not set; set it with `skifity env set --secret " + key + "=...`"})
		}
	}

	processes := map[string]store.AppProcess{}
	for _, process := range have.Processes {
		processes[process.Name] = process
	}
	for _, process := range sortedKeys(want.Processes) {
		p := want.Processes[process]
		instances := 1
		if current, ok := processes[process]; ok {
			instances = current.Instances
		}
		if p.Instances != nil {
			instances = *p.Instances
		}
		current, ok := processes[process]
		if ok && current.Command == p.Command && current.Instances == instances {
			continue
		}
		step := Step{Op: "create", Kind: "process", App: name, Name: process, Detail: p.Command}
		if ok {
			step.Op, step.Detail = "change", fmt.Sprintf("%q × %d → %q × %d", current.Command, current.Instances, p.Command, instances)
		}
		step.Call = &Call{Method: "PUT", Path: "/api/apps/" + ref + "/processes/" + process,
			Body: map[string]any{"command": p.Command, "instances": instances}}
		steps = append(steps, step)
	}
	for _, process := range have.Processes {
		if _, ok := want.Processes[process.Name]; !ok && want.Processes != nil {
			steps = append(steps, Step{Op: "note", Kind: "process", App: name, Name: process.Name,
				Detail: "is here and not in the file; it is left alone"})
		}
	}

	domains := map[string]bool{}
	for _, domain := range have.Domains {
		domains[domain.Hostname] = true
	}
	for _, hostname := range want.Domains {
		if domains[hostname] {
			continue
		}
		steps = append(steps, Step{Op: "create", Kind: "domain", App: name, Name: hostname,
			Call: &Call{Method: "POST", Path: "/api/apps/" + ref + "/domains", Body: map[string]any{"hostname": hostname}}})
	}

	jobs := map[string]store.AppJob{}
	for _, job := range have.Jobs {
		jobs[job.Name] = job
	}
	for _, schedule := range sortedKeys(want.Schedules) {
		s := want.Schedules[schedule]
		job, ok := jobs[schedule]
		switch {
		case !ok:
			steps = append(steps, Step{Op: "create", Kind: "schedule", App: name, Name: schedule, Detail: s.Schedule + " " + s.Command,
				Call: &Call{Method: "POST", Path: "/api/apps/" + ref + "/jobs",
					Body: map[string]any{"name": schedule, "schedule": s.Schedule, "command": s.Command}}})
		case job.Schedule != s.Schedule || job.Command != s.Command:
			// The whole command, as the panel's form sends it: its name, and
			// whether it is switched on, which the file does not say and a
			// change of time must not.
			steps = append(steps, Step{Op: "change", Kind: "schedule", App: name, Name: schedule,
				Detail: fmt.Sprintf("%s %s → %s %s", job.Schedule, job.Command, s.Schedule, s.Command),
				Call: &Call{Method: "PATCH", Path: "/api/apps/" + ref + "/jobs/" + job.ID,
					Body: map[string]any{"name": job.Name, "schedule": s.Schedule, "command": s.Command, "enabled": job.Enabled}}})
		}
	}
	return steps, redeploy
}

// sourceNotes are where an app comes from, when the file says otherwise:
// said, and not changed, because where an app is built from is changed on its
// page, where the panel can say what else that changes.
func sourceNotes(name string, want App, current store.App) []Step {
	note := func(detail string) Step {
		return Step{Op: "note", Kind: "source", App: name, Name: name, Detail: detail}
	}
	switch {
	case want.Image != "" && current.SourceType != "image":
		return []Step{note("is built from its source here and the file names the image " + want.Image +
			"; an app's source is changed on its page")}
	case want.Repo != "" && current.SourceType != "git":
		return []Step{note("is not built from a repository here and the file names " + want.Repo +
			"; an app's source is changed on its page")}
	case want.Repo != "" && !sameRepository(want.Repo, current.RepoURL):
		return []Step{note("comes from " + current.RepoURL + " here and " + want.Repo +
			" in the file; an app's repository is changed on its page")}
	}
	return nil
}

// sameRepository compares two repository addresses the way a person would:
// case, a trailing slash and ".git" aside.
func sameRepository(a, b string) bool {
	clean := func(url string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(url)), "/"), ".git")
	}
	return clean(a) == clean(b)
}

// scaling is the change to an app's instances, if there is one.
func scaling(want App, current store.App, exists bool) *Step {
	switch {
	case want.Instances != nil:
		if exists && !current.Autoscale && current.Replicas == *want.Instances {
			return nil
		}
		detail := strconv.Itoa(*want.Instances)
		switch {
		case exists && current.Autoscale:
			detail = fmt.Sprintf("%d to %d by load → %d", current.MinReplicas, current.MaxReplicas, *want.Instances)
		case exists:
			detail = fmt.Sprintf("%d → %d", current.Replicas, *want.Instances)
		}
		return &Step{Op: "change", Kind: "scaling", Detail: detail,
			Call: &Call{Method: "PUT", Body: map[string]any{"replicas": *want.Instances, "autoscale": false}}}
	case want.Autoscale != nil:
		a := want.Autoscale
		if exists && current.Autoscale && current.MinReplicas == a.Min && current.MaxReplicas == a.Max &&
			current.CPUTarget == a.CPU && current.MemoryTarget == a.Memory {
			return nil
		}
		return &Step{Op: "change", Kind: "scaling", Detail: fmt.Sprintf("%d to %d by load", a.Min, a.Max),
			Call: &Call{Method: "PUT", Body: map[string]any{"autoscale": true, "min_replicas": a.Min,
				"max_replicas": a.Max, "cpu_target": a.CPU, "memory_target": a.Memory}}}
	}
	return nil
}

// Counts is how many steps of each op a plan has.
func Counts(steps []Step) (create, change, note int) {
	for _, step := range steps {
		switch step.Op {
		case "create":
			create++
		case "change":
			change++
		case "note":
			note++
		}
	}
	return create, change, note
}

// reference is a placeholder for something the plan makes before it.
var reference = regexp.MustCompile(`\{(?:app|database):[a-z0-9-]+\}`)

// Resolve fills a call's references to what earlier calls created. A body
// value is one only when it is nothing else: `{ a; b; }` is a shell command,
// not a reference to anything.
func Resolve(call Call, ids map[string]string) (Call, error) {
	var missing []string
	fill := func(ref string) string {
		id, ok := ids[ref[1:len(ref)-1]]
		if !ok {
			missing = append(missing, ref[1:len(ref)-1])
			return ref
		}
		return id
	}
	out := Call{Method: call.Method, Path: reference.ReplaceAllStringFunc(call.Path, fill), Creates: call.Creates, Body: map[string]any{}}
	for key, value := range call.Body {
		if text, ok := value.(string); ok && reference.FindString(text) == text && text != "" {
			value = fill(text)
		}
		out.Body[key] = value
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return Call{}, fmt.Errorf("nothing earlier in the plan made %s", strings.Join(slices.Compact(missing), ", "))
	}
	return out, nil
}

// DefaultVariable is the variable a database is linked as when the file does
// not say: the panel's own default, which a test in internal/api holds this
// to.
func DefaultVariable(engine string) string {
	switch engine {
	case "redis":
		return "REDIS_URL"
	case "mysql":
		return "MYSQL_URL"
	default:
		return "DATABASE_URL"
	}
}
