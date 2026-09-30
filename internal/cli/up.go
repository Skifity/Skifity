package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/term"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/upload"
	"skifity/internal/version"
)

// cmdUp deploys a folder. No repository, no Dockerfile, no settings.
//
// This is the command for somebody whose app an assistant wrote and who has
// never deployed anything. Every other way in starts with a repository URL,
// which is the one thing they do not have. So `up` does the steps they would
// not know to do, in the order they have to happen:
//
//  1. read the folder the way the panel reads a repository, and say what it
//     is and what it needs;
//  2. create the app the first time, with the databases it needs already
//     made and linked, and the settings from its .env if they say so;
//  3. pack the folder, leaving out what .gitignore leaves out and every .env;
//  4. send it, deploy it, stream the build, and print the address.
//
// The second time, it remembers the app (skifity.toml) and only sends and
// deploys. The same code sent twice is the same upload, and the panel reuses
// the image instead of building again.
func cmdUp(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("up", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "deploy to this app instead of the one in skifity.toml")
	name := flags.String("name", "", "the app's name, the first time; defaults to the folder's name")
	envID := flags.String("env", "", "the environment to create the app in, the first time")
	fresh := flags.Bool("new", false, "create a new app even if this folder is linked to one")
	noDatabase := flags.Bool("no-database", false, "do not create the databases the code looks like it needs")
	dotenv := flags.Bool("dotenv", false, "set the values in this folder's .env as the app's variables")
	dryRun := flags.Bool("dry-run", false, "show what would be sent and created, and stop there")
	follow := flags.Bool("follow", true, "stream the build log until the deploy finishes")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return errdoc.BadRequest("Name one folder to deploy, or none for this one.")
	}
	opts := FolderOptions{
		App: *appID, Name: *name, Environment: *envID,
		New: *fresh, NoDatabase: *noDatabase, DryRun: *dryRun,
	}
	if len(positional) == 1 {
		opts.Dir = positional[0]
	}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "dotenv" {
			opts.DotEnv = dotenv
		}
	})
	if opts.DotEnv == nil && isInteractive() {
		opts.AskDotEnv = func(file string, count int) bool {
			answer := prompt(out, fmt.Sprintf(
				"\n%s has %s. The file itself is never sent; set its values on the app instead? [Y/n] ",
				file, plural(count, "setting")))
			return answer == "" || strings.HasPrefix(strings.ToLower(answer), "y")
		}
	}
	// A progress line is noise in the output a script reads.
	opts.Say = func(format string, a ...any) {
		if !*asJSON {
			fmt.Fprintf(out, format, a...)
		}
	}

	var cfg Config
	var client *Client
	if !*dryRun {
		if cfg, err = LoadConfig(); err != nil {
			return err
		}
		client = NewClient(cfg)
	}
	result, err := DeployFolder(ctx, client, cfg, opts)
	if err != nil {
		return err
	}
	if *dryRun || (*asJSON && !*follow) {
		if *asJSON {
			return writeJSON(out, result)
		}
		return nil
	}
	if !*follow {
		return nil
	}
	if err := followDeployment(ctx, client, result.App.ID, result.Deployment.ID, out, *asJSON); err != nil {
		return err
	}
	printAddresses(ctx, client, result.App.ID, out, *asJSON)
	opts.Say("\nNext time, run `%s up` here again. Only what changed is built.\n", version.Binary)
	return nil
}

// FolderOptions says which folder to deploy and how. The zero value deploys
// the current folder to the app it is linked to, or to a new one.
type FolderOptions struct {
	// Dir is the folder; empty is the current one.
	Dir string
	// App deploys to this app instead of the linked one.
	App string
	// Name and Environment are for a new app.
	Name, Environment string
	// New makes a new app even when the folder is linked to one.
	New bool
	// NoDatabase skips creating the databases the code looks like it needs.
	NoDatabase bool
	// DotEnv, when set, says whether to set the folder's .env values on the
	// app. When nil, AskDotEnv is asked for a new app, and nothing is sent to
	// an existing one: by then somebody may have changed a value in the panel,
	// and a stale file would put the old one back.
	DotEnv    *bool
	AskDotEnv func(file string, count int) bool
	// DryRun reads and packs the folder and stops before anything is sent.
	DryRun bool
	// Say prints progress. Nil prints nothing.
	Say func(format string, a ...any)
}

// FolderResult is what DeployFolder did.
type FolderResult struct {
	Folder     string            `json:"folder"`
	Files      int               `json:"files"`
	Size       int64             `json:"size"`
	Skipped    []string          `json:"skipped,omitempty"`
	Detection  builder.Detection `json:"detection"`
	Created    bool              `json:"created"`
	Databases  []initialDatabase `json:"databases,omitempty"`
	App        store.App         `json:"app"`
	Upload     upload.Summary    `json:"upload"`
	Deployment store.Deployment  `json:"deployment"`
	// Missing are settings the app's .env.example lists and the app does not
	// have: the likeliest reason for a first deploy that crashes.
	Missing []string `json:"missing,omitempty"`
	// DotEnvFile is the folder's .env, when it has one with values in it, and
	// DotEnvSent says whether they were set on the app. An assistant reads
	// these to ask the person, rather than deciding about their keys itself.
	DotEnvFile string `json:"dotenv_file,omitempty"`
	DotEnvSent bool   `json:"dotenv_sent"`
}

// DeployFolder reads a folder, creates its app the first time, sends it and
// starts a deploy. It does not wait for the deploy; followDeployment does, for
// the CLI. The MCP server calls this too, so an assistant asked to "deploy
// this" does exactly what `skifity up` does.
func DeployFolder(ctx context.Context, client *Client, cfg Config, opts FolderOptions) (FolderResult, error) {
	say := opts.Say
	if say == nil {
		say = func(string, ...any) {}
	}
	var positional []string
	if opts.Dir != "" {
		positional = []string{opts.Dir}
	}
	root, linked, err := upRoot(positional)
	if err != nil {
		return FolderResult{}, err
	}
	if linked.App != "" && opts.Dir == "" && !sameDir(root, ".") {
		say("Looking at %s, the app this folder is part of...\n", displayPath(root))
	} else {
		say("Looking at %s...\n", displayPath(root))
	}
	folder, err := pack(root)
	if err != nil {
		return FolderResult{}, err
	}
	defer os.Remove(folder.Archive)
	if err := checkPacked(folder, say); err != nil {
		return FolderResult{}, err
	}

	detection := builder.Detect(folder.Tree)
	describeDetection(detection, say)
	say("  %s, %s to send.\n", plural(len(folder.Files), "file"), humanSize(folder.Size))
	if len(folder.Skipped) > 0 {
		say("  Not sent, because links and special files never are: %s\n", listSome(folder.Skipped))
	}

	target := opts.App
	if target == "" && !opts.New {
		target = linked.App
	}
	result := FolderResult{
		Folder: root, Files: len(folder.Files), Size: folder.Size, Skipped: folder.Skipped,
		Detection: detection, Created: target == "",
	}
	if result.Created {
		result.Databases = wantedDatabases(detection.Needs, opts.NoDatabase)
	}

	if opts.DryRun {
		if result.Created {
			say("\nWould create a new app and deploy it.\n")
			for _, db := range result.Databases {
				say("Would create a %s database for it, as %s.\n", engineName(db.Engine), orDefault(db.Variable, "its default variable"))
			}
		} else {
			say("\nWould send this to the app %s and deploy it.\n", target)
		}
		return result, nil
	}

	// Settings from .env: offered the first time, since that is when they are
	// all missing, and sent later only when asked for.
	values, valuesFrom := readDotEnv(root)
	result.DotEnvFile = valuesFrom
	sendValues := false
	if len(values) > 0 {
		switch {
		case opts.DotEnv != nil:
			sendValues = *opts.DotEnv
		case result.Created && opts.AskDotEnv != nil:
			sendValues = opts.AskDotEnv(valuesFrom, len(values))
		case result.Created:
			say("\n%s has %s, and they are not sent unless you say so. Run again with --dotenv to set them on the app.\n",
				valuesFrom, plural(len(values), "setting"))
		}
	}
	result.DotEnvSent = sendValues
	if sendValues {
		// A database the panel creates sets its own variable, and the one in
		// the file points at the computer it was written on.
		for _, db := range result.Databases {
			if _, ok := values[db.Variable]; ok && db.Variable != "" {
				delete(values, db.Variable)
				say("  %s is left out: it will point at the %s database this creates, not at the one on this computer.\n",
					db.Variable, engineName(db.Engine))
			}
		}
	}

	if result.Created {
		result.App, err = createUploadApp(ctx, client, cfg, createUploadRequest{
			root: root, name: opts.Name, env: opts.Environment, detection: detection,
			databases: result.Databases, values: values, sendValues: sendValues,
		}, say)
		if err != nil {
			return result, err
		}
	} else {
		if err := client.Do(ctx, "GET", "/api/apps/"+target, nil, &result.App); err != nil {
			return result, err
		}
		if sendValues {
			for _, key := range sortedKeys(values) {
				if err := client.Do(ctx, "PUT", "/api/apps/"+result.App.ID+"/variables",
					map[string]any{"key": key, "value": values[key]}, nil); err != nil {
					return result, err
				}
			}
			say("Set %s from %s.\n", plural(len(values), "variable"), valuesFrom)
		}
	}
	result.Missing = reportMissing(ctx, client, detection.Needs, result.App, say)

	say("Sending %s...\n", humanSize(folder.Size))
	archive, err := os.Open(folder.Archive)
	if err != nil {
		return result, err
	}
	err = client.Upload(ctx, "/api/apps/"+result.App.ID+"/source", archive, folder.Size, &result.Upload)
	archive.Close()
	if err != nil {
		return result, err
	}

	if err := client.Do(ctx, "POST", "/api/apps/"+result.App.ID+"/deploy",
		map[string]any{"commit_sha": result.Upload.SHA256}, &result.Deployment); err != nil {
		return result, err
	}
	say("Deploying (#%d).\n\n", result.Deployment.Number)
	return result, nil
}

// upRoot works out which folder to send, and the project file linked to it.
//
// A project file in a parent means the command was run from inside an app it
// already knows, and the app is the whole of that folder, not the corner of it
// the terminal happens to be in. Sending just the subfolder would replace the
// app with part of itself.
func upRoot(positional []string) (string, ProjectFile, error) {
	dir := "."
	if len(positional) == 1 {
		dir = positional[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ProjectFile{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", ProjectFile{}, errdoc.BadRequest(fmt.Sprintf("%s is not a folder.", dir))
	}
	file, path, err := loadProjectFileFrom(abs)
	if errors.Is(err, os.ErrNotExist) {
		return abs, ProjectFile{}, nil
	}
	if err != nil {
		return "", ProjectFile{}, err
	}
	return filepath.Dir(path), file, nil
}

// checkPacked refuses, before anything is sent, what the panel would refuse
// after — and says which part of the folder is the problem, which the panel
// cannot see.
func checkPacked(folder *packed, say func(string, ...any)) error {
	limits := upload.DefaultLimits
	largest := func() {
		say("  The largest parts of the folder:\n")
		for _, entry := range folder.Largest {
			say("    %-24s %s\n", entry.Name, humanSize(entry.Size))
		}
	}
	switch {
	case len(folder.Files) == 0:
		return errdoc.UploadEmpty()
	case len(folder.Files) > limits.Entries:
		largest()
		return errdoc.UploadTooManyFiles(limits.Entries)
	case folder.Unpacked > limits.Unpacked:
		largest()
		return errdoc.UploadUnpacksTooLarge(limits.Unpacked >> 20)
	case folder.Size > limits.Compressed:
		largest()
		return errdoc.UploadTooLarge(limits.Compressed >> 20)
	}
	return nil
}

// describeDetection says what the folder looks like, in one or two lines.
func describeDetection(d builder.Detection, say func(string, ...any)) {
	what := strings.Join(nonEmpty(d.Framework, d.Language), " on ")
	switch {
	case d.Builder == builder.BuilderDockerfile:
		say("  It has a Dockerfile (%s), which is used as it is.\n", d.DockerfilePath)
	case what != "" && d.Confidence == "high":
		say("  It looks like %s.\n", what)
	case what != "":
		say("  It might be %s; the build will work it out.\n", what)
	default:
		say("  Nothing in it says what it is; the build will try to work it out.\n")
	}
	if d.StaticDir != "" {
		if d.BuildCommand != "" {
			say("  It is a website: `%s` builds it, and %s/ is served.\n", d.BuildCommand, d.StaticDir)
		} else {
			say("  It is a website, served as it is.\n")
		}
	}
	for _, need := range d.Needs {
		switch need.Kind {
		case builder.NeedDatabase:
			if need.Provided {
				say("  It uses %s (%s in %s).\n", engineName(need.Engine), need.Evidence, need.Source)
			} else {
				say("  It uses %s (%s in %s), which this panel does not run. Point %s at one you have.\n",
					engineName(need.Engine), need.Evidence, need.Source, orDefault(need.Variable, "its connection setting"))
			}
		case builder.NeedEphemeral:
			say("  ! It keeps data in %s (%s). Every deploy starts from a fresh copy of the code, so that data "+
				"is erased each time. Add a volume for it in the panel, or use a database.\n",
				orDefault(need.Source, "a file"), need.Evidence)
		}
	}
}

// initialDatabase is one database to create with a new app, in the shape the
// create request takes.
type initialDatabase struct {
	Engine   string `json:"engine"`
	Variable string `json:"variable,omitempty"`
}

// wantedDatabases are the databases to create with a new app.
func wantedDatabases(needs []builder.Need, none bool) []initialDatabase {
	if none {
		return nil
	}
	var out []initialDatabase
	for _, need := range needs {
		if need.Kind == builder.NeedDatabase && need.Provided {
			out = append(out, initialDatabase{Engine: need.Engine, Variable: need.Variable})
		}
	}
	return out
}

type createUploadRequest struct {
	root, name, env string
	detection       builder.Detection
	databases       []initialDatabase
	values          map[string]string
	sendValues      bool
}

// createUploadApp makes the app, the first time, with everything detection
// found already set, and links the folder to it.
func createUploadApp(ctx context.Context, client *Client, cfg Config, req createUploadRequest, say func(string, ...any)) (store.App, error) {
	appName := req.name
	if appName == "" {
		appName = friendlyName(filepath.Base(req.root))
	}
	environment := req.env
	if environment == "" {
		var err error
		environment, err = resolveOrCreateEnvironment(ctx, client, cfg, appName, say)
		if err != nil {
			return store.App{}, err
		}
	}

	d := req.detection
	body := map[string]any{"name": appName, "source_type": "upload"}
	// The same rule the panel's form follows: a guess fills the form only when
	// it is a statement, not a question.
	if d.Confidence == "high" {
		body["port"] = d.Port
		body["health_path"] = d.HealthPath
		body["start_command"] = d.StartCommand
		body["build_command"] = d.BuildCommand
		body["static_dir"] = d.StaticDir
		body["dockerfile_path"] = d.DockerfilePath
		if d.Builder == builder.BuilderDockerfile || d.Builder == builder.BuilderStatic {
			body["builder"] = string(d.Builder)
		}
	}
	if len(req.databases) > 0 {
		body["databases"] = req.databases
	}
	if req.sendValues && len(req.values) > 0 {
		body["variables"] = req.values
	}

	say("Creating the app %s...\n", appName)
	var created struct {
		App       store.App `json:"app"`
		Databases []struct {
			Engine   string `json:"engine"`
			Name     string `json:"name"`
			Variable string `json:"variable"`
			Error    string `json:"error"`
		} `json:"databases"`
	}
	if err := client.Do(ctx, "POST", "/api/environments/"+environment+"/apps", body, &created); err != nil {
		return store.App{}, err
	}
	if created.App.ID == "" {
		return store.App{}, appNotCreated()
	}
	for _, db := range created.Databases {
		if db.Error != "" {
			say("  The %s database could not be created: %s\n  The app is deployed anyway; add one under Databases in the panel.\n",
				engineName(db.Engine), db.Error)
			continue
		}
		say("  Created the %s database %s, and set %s for the app.\n", engineName(db.Engine), db.Name, db.Variable)
	}
	if req.sendValues && len(req.values) > 0 {
		say("  Set %s from .env.\n", plural(len(req.values), "variable"))
	}

	path, err := saveProjectFileIn(req.root, ProjectFile{
		App: created.App.ID, Name: created.App.Name, Environment: environment,
	})
	if err != nil {
		return store.App{}, err
	}
	say("  Wrote %s, so the next `%s up` here goes to the same app.\n", filepath.Base(path), version.Binary)
	return created.App, nil
}

// resolveOrCreateEnvironment finds where a new app goes. Somebody deploying
// their first app has no project, and "create a project in the panel first" is
// one more step between them and a running app, so a project is made for them,
// named after the app.
func resolveOrCreateEnvironment(ctx context.Context, client *Client, cfg Config, appName string, say func(string, ...any)) (string, error) {
	environment, err := resolveEnvironment(ctx, client, cfg)
	var problem *errdoc.Problem
	if err == nil || !errors.As(err, &problem) || problem.Code != "cli.no_project" {
		return environment, err
	}
	teamID, err := resolveTeam(ctx, client, cfg)
	if err != nil {
		return "", err
	}
	say("Creating the project %s...\n", appName)
	var project store.Project
	if err := client.Do(ctx, "POST", "/api/teams/"+teamID+"/projects",
		map[string]any{"name": appName}, &project); err != nil {
		return "", err
	}
	return resolveEnvironment(ctx, client, cfg)
}

// reportMissing names the settings the app's .env.example lists and the app
// does not have, so the first crash is not the first anybody hears of them.
// It asks the app rather than the folder: a value set in the panel last week
// is set, whatever this folder's .env says.
func reportMissing(ctx context.Context, client *Client, needs []builder.Need, app store.App, say func(string, ...any)) []string {
	var listed []builder.Need
	for _, need := range needs {
		if need.Kind == builder.NeedVariables && len(need.Variables) > 0 {
			listed = append(listed, need)
		}
	}
	if len(listed) == 0 {
		return nil
	}
	var response struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app.ID+"/variables", nil, &response); err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, variable := range response.Items {
		set[variable.Key] = true
	}
	var all []string
	for _, need := range listed {
		var missing []string
		for _, name := range need.Variables {
			if !set[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		all = append(all, missing...)
		say("  %s lists settings the app does not have yet: %s\n", need.Source, strings.Join(missing, ", "))
		say("  Set them with `%s env set NAME=value`, or under Variables in the panel.\n", version.Binary)
	}
	return all
}

// printAddresses prints where the app can be reached, which is the answer to
// the only question anybody has once the deploy is done.
func printAddresses(ctx context.Context, client *Client, appID string, out io.Writer, asJSON bool) {
	if asJSON {
		return
	}
	var response struct {
		Items []store.Domain `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+appID+"/domains", nil, &response); err != nil || len(response.Items) == 0 {
		fmt.Fprintln(out, "\nIt has no address yet. Add a domain to it in the panel.")
		return
	}
	fmt.Fprintln(out)
	for _, domain := range response.Items {
		scheme := "http://"
		if domain.TLS {
			scheme = "https://"
		}
		fmt.Fprintf(out, "It is live at %s%s\n", scheme, domain.Hostname)
	}
}

// readDotEnv reads the folder's .env, if it has one. The file itself is never
// sent: see ignore.go.
func readDotEnv(root string) (map[string]string, string) {
	for _, name := range []string{".env", ".env.local", ".env.production"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		values := parseDotEnv(string(data))
		if len(values) > 0 {
			return values, name
		}
	}
	return nil, ""
}

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseDotEnv reads a .env file the way web/src/lib/dotenv.ts does, so a file
// that works pasted into the panel works here too.
func parseDotEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		key, value, ok := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if !ok || !envKey.MatchString(key) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out
}

// friendlyName turns a folder name into an app name: "My Cool App" becomes
// "my-cool-app", which is what the panel would make of it anyway.
func friendlyName(folder string) string {
	name := strings.ToLower(strings.TrimSpace(folder))
	name = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "app"
	}
	return name
}

func engineName(engine string) string {
	switch engine {
	case builder.EnginePostgres:
		return "PostgreSQL"
	case builder.EngineMySQL:
		return "MySQL"
	case builder.EngineRedis:
		return "Redis"
	case builder.EngineMongoDB:
		return "MongoDB"
	case builder.EngineSQLServer:
		return "SQL Server"
	case builder.EngineSQLite:
		return "SQLite"
	}
	return engine
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func listSome(items []string) string {
	if len(items) <= 5 {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:5], ", ") + fmt.Sprintf(" and %d more", len(items)-5)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// plural says "1 file" and "2 files".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

func isInteractive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// Upload sends a file as a request body, with no deadline but the context's:
// a few megabytes on a slow line take longer than the minute ordinary requests
// get.
func (c *Client) Upload(ctx context.Context, path string, body io.Reader, size int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the panel at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeProblem(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("read the panel's answer: %w", err)
	}
	return nil
}
