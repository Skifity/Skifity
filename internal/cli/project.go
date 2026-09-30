package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// ProjectFile is the optional per-directory configuration.
//
// It is optional on purpose: `skifity deploy` works without it as long as the
// app can be identified, and a file that must exist before anything works is a
// step between a user and their first deploy.
type ProjectFile struct {
	// App is the app's id, which is what everything else is resolved from.
	App string `json:"app" toml:"app"`
	// Name is kept for readability; nothing depends on it.
	Name string `json:"name,omitempty" toml:"name"`
	// Environment is the environment's id, used when creating an app.
	Environment string `json:"environment,omitempty" toml:"environment"`
	// Panel lets one directory target a panel other than the signed-in default.
	Panel string `json:"panel,omitempty" toml:"panel"`
}

// ProjectFileName is what the file is called.
const ProjectFileName = "skifity.toml"

// LoadProjectFile reads the file from the current directory or any parent, the
// way git finds its root, so a command works from a subdirectory.
func LoadProjectFile() (ProjectFile, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return ProjectFile{}, "", fmt.Errorf("find the current directory: %w", err)
	}
	return loadProjectFileFrom(dir)
}

// loadProjectFileFrom looks for the file in a directory and then its parents.
func loadProjectFileFrom(dir string) (ProjectFile, string, error) {
	for {
		path := filepath.Join(dir, ProjectFileName)
		data, err := os.ReadFile(path)
		if err == nil {
			file, err := parseProjectFile(string(data))
			return file, path, err
		}
		if !errors.Is(err, os.ErrNotExist) {
			return ProjectFile{}, "", fmt.Errorf("read %s: %w", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ProjectFile{}, "", os.ErrNotExist
		}
		dir = parent
	}
}

// parseProjectFile reads the flat `key = "value"` form.
func parseProjectFile(content string) (ProjectFile, error) {
	var file ProjectFile
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return file, fmt.Errorf("line %d of %s: expected key = \"value\"", i+1, ProjectFileName)
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "app":
			file.App = value
		case "name":
			file.Name = value
		case "environment":
			file.Environment = value
		case "panel":
			file.Panel = value
		default:
			return file, fmt.Errorf("line %d of %s: %q is not a setting Skifity knows",
				i+1, ProjectFileName, strings.TrimSpace(key))
		}
	}
	return file, nil
}

// SaveProjectFile writes the file to the current directory.
func SaveProjectFile(file ProjectFile) (string, error) {
	return saveProjectFileIn(".", file)
}

// saveProjectFileIn writes the file to a directory.
func saveProjectFileIn(dir string, file ProjectFile) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s project settings.\n", version.Name)
	fmt.Fprintf(&b, "# Safe to commit: it contains no credentials.\n\n")
	if file.Name != "" {
		fmt.Fprintf(&b, "name = %q\n", file.Name)
	}
	fmt.Fprintf(&b, "app = %q\n", file.App)
	if file.Environment != "" {
		fmt.Fprintf(&b, "environment = %q\n", file.Environment)
	}
	if file.Panel != "" {
		fmt.Fprintf(&b, "panel = %q\n", file.Panel)
	}

	path := filepath.Join(dir, ProjectFileName)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// cmdInit creates an app and writes the project file.
func cmdInit(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(out)
	name := flags.String("name", "", "the app's name; defaults to the directory name")
	envID := flags.String("env", "", "the environment id to create the app in")
	repo := flags.String("repo", "", "the Git repository to deploy; detected from the git remote when omitted")
	image := flags.String("image", "", "run a prebuilt image instead of building from source")
	port := flags.Int("port", 0, "the port the app listens on")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)

	if existing, path, err := LoadProjectFile(); err == nil && existing.App != "" {
		return errdoc.New("cli.already_initialised", "This directory already has an app").
			WithCause("%s points at the app %s.", path, existing.App).
			WithImpact("Nothing was changed.").
			WithFix("Run `%s deploy` to deploy it, or delete %s to start again.",
				version.Binary, ProjectFileName)
	}

	appName := *name
	if appName == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("find the current directory: %w", err)
		}
		appName = filepath.Base(cwd)
	}

	repoURL := *repo
	if repoURL == "" && *image == "" {
		repoURL = detectGitRemote()
		if repoURL == "" {
			return errdoc.BadRequest(
				"This directory has no git remote, so there is nothing to deploy from. " +
					"Push it to a Git host and try again, or use --image to run a prebuilt image.")
		}
		fmt.Fprintf(out, "Using the git remote %s.\n", repoURL)
	}

	environment := *envID
	if environment == "" {
		environment, err = resolveEnvironment(ctx, client, cfg)
		if err != nil {
			return err
		}
	}

	body := map[string]any{"name": appName, "port": *port}
	if *image != "" {
		body["source_type"] = "image"
		body["image"] = *image
	} else {
		body["source_type"] = "git"
		body["repo_url"] = repoURL
		body["branch"] = detectGitBranch()
	}

	// The answer is an object with the app inside it, not the app: creating
	// one can also start a deployment and register a webhook, and both belong
	// in the reply. Decoding it as a bare App used to leave every field empty
	// and write a project file pointing at no app at all.
	var created struct {
		App        store.App `json:"app"`
		Deployment *struct {
			ID string `json:"id"`
		} `json:"deployment"`
		Webhook *struct {
			Registered bool   `json:"registered"`
			URL        string `json:"url"`
			Reason     string `json:"reason"`
		} `json:"webhook"`
	}
	if err := client.Do(ctx, "POST", "/api/environments/"+environment+"/apps", body, &created); err != nil {
		return err
	}
	app := created.App
	if app.ID == "" {
		return appNotCreated()
	}

	if created.Webhook != nil && created.Webhook.URL != "" {
		if created.Webhook.Registered {
			fmt.Println("Deploy on push is set up: a push to this repository deploys it.")
		} else {
			fmt.Printf("Deploy on push needs a webhook adding by hand: %s\n", created.Webhook.URL)
			if created.Webhook.Reason != "" {
				fmt.Printf("  (%s)\n", created.Webhook.Reason)
			}
		}
	}

	path, err := SaveProjectFile(ProjectFile{
		App: app.ID, Name: app.Name, Environment: environment,
	})
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(out, map[string]any{"app": app, "file": path})
	}
	fmt.Fprintf(out, "\nCreated the app %s.\nWrote %s.\n\nDeploy it with: %s deploy\n\n",
		app.Name, path, version.Binary)
	return nil
}

// appNotCreated is a create that answered without the app it made.
//
// It used to say "run `skifity link`", a command that does not exist.
func appNotCreated() *errdoc.Problem {
	return errdoc.New("cli.app_not_created", "The panel did not say which app it created").
		WithCause("The reply to creating an app had no app in it.").
		WithImpact("The project file was not written, so `skifity deploy` here would not know what to deploy.").
		WithFix("Find it with `skifity apps`, and put its id in skifity.toml as app = \"<id>\".")
}

// resolveApp works out which app a command is about.
//
// The order is: an explicit flag, then the project file, then the only app in
// the environment. Asking the user is the last resort.
func resolveApp(ctx context.Context, client *Client, cfg Config, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if file, _, err := LoadProjectFile(); err == nil && file.App != "" {
		return file.App, nil
	}

	environment, err := resolveEnvironment(ctx, client, cfg)
	if err != nil {
		return "", err
	}
	var response struct {
		Items []store.App `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/apps", nil, &response); err != nil {
		return "", err
	}
	switch len(response.Items) {
	case 0:
		return "", errdoc.New("cli.no_app", "There is no app here yet").
			WithCause("This directory has no %s, and the environment has no apps.", ProjectFileName).
			WithImpact("The command did not run.").
			WithFix("Run `%s init` to create one.", version.Binary)
	case 1:
		return response.Items[0].ID, nil
	default:
		names := make([]string, 0, len(response.Items))
		for _, app := range response.Items {
			names = append(names, fmt.Sprintf("%s (%s)", app.Name, app.ID))
		}
		return "", errdoc.New("cli.ambiguous_app", "Which app did you mean?").
			WithCause("This directory has no %s, and the environment has several apps.", ProjectFileName).
			WithImpact("The command did not run.").
			WithFix("Pass --app with one of: %s", strings.Join(names, ", "))
	}
}

// ResolveTeam finds the team to work in.
//
// `skifity login` stores one, but a CLI authenticating from SKIFITY_TOKEN in a
// CI job or an AI assistant's sandbox never logged in. Almost everybody has one
// team, so asking which one is a question with a single possible answer.
//
// It is exported because the MCP server needs exactly this: a token created
// without a team, which is how CI and an assistant's sandbox are set up, still
// has to find the one team it can act on.
func ResolveTeam(ctx context.Context, client *Client, cfg Config) (string, error) {
	return resolveTeam(ctx, client, cfg)
}

func resolveTeam(ctx context.Context, client *Client, cfg Config) (string, error) {
	if cfg.TeamID != "" {
		return cfg.TeamID, nil
	}

	var me struct {
		Teams []store.Team `json:"teams"`
	}
	if err := client.Do(ctx, "GET", "/api/me", nil, &me); err != nil {
		return "", err
	}
	switch len(me.Teams) {
	case 0:
		return "", errdoc.New("cli.no_team", "You are not in any team").
			WithCause("This account belongs to no team, so there is nothing to act on.").
			WithImpact("Nothing was changed.").
			WithFix("Ask someone in the team to add you, or create a team in the panel.")
	case 1:
		return me.Teams[0].ID, nil
	}

	names := make([]string, 0, len(me.Teams))
	for _, team := range me.Teams {
		names = append(names, team.Name+" ("+team.ID+")")
	}
	return "", errdoc.New("cli.team_ambiguous", "You are in more than one team").
		WithCause("This account is in %d teams, so the one to use is not obvious.", len(me.Teams)).
		WithImpact("Nothing was changed.").
		WithFix("Run `%s login` to choose one, or set SKIFITY_TEAM to the id of: %s",
			version.Binary, strings.Join(names, ", "))
}

// resolveEnvironment finds the environment to work in.
func resolveEnvironment(ctx context.Context, client *Client, cfg Config) (string, error) {
	if file, _, err := LoadProjectFile(); err == nil && file.Environment != "" {
		return file.Environment, nil
	}
	teamID, err := resolveTeam(ctx, client, cfg)
	if err != nil {
		return "", err
	}

	var projects struct {
		Items []store.Project `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/projects", nil, &projects); err != nil {
		return "", err
	}
	if len(projects.Items) == 0 {
		return "", errdoc.New("cli.no_project", "This team has no projects").
			WithCause("There is nowhere to put an app.").
			WithImpact("The command did not run.").
			WithFix("Create a project in the panel first.")
	}

	var environments struct {
		Items []store.Environment `json:"items"`
	}
	if err := client.Do(ctx, "GET",
		"/api/projects/"+projects.Items[0].ID+"/environments", nil, &environments); err != nil {
		return "", err
	}
	for _, env := range environments.Items {
		if env.Slug == "production" {
			return env.ID, nil
		}
	}
	if len(environments.Items) > 0 {
		return environments.Items[0].ID, nil
	}
	return "", errdoc.BadRequest("That project has no environments.")
}

// detectGitRemote reads the origin URL from .git/config without shelling out to
// git, so the CLI works on a machine that has no git installed.
func detectGitRemote() string {
	data, err := os.ReadFile(filepath.Join(".git", "config"))
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.Contains(line, `remote "origin"`)
			continue
		}
		if !inOrigin {
			continue
		}
		if url, found := strings.CutPrefix(line, "url = "); found {
			return normaliseRemote(strings.TrimSpace(url))
		}
	}
	return ""
}

// normaliseRemote turns an SSH remote into the HTTPS form the panel clones with.
func normaliseRemote(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	if path, found := strings.CutPrefix(raw, "git@"); found {
		if host, repo, ok := strings.Cut(path, ":"); ok {
			return "https://" + host + "/" + repo
		}
	}
	return raw
}

// detectGitBranch reads the current branch from .git/HEAD.
func detectGitBranch() string {
	data, err := os.ReadFile(filepath.Join(".git", "HEAD"))
	if err != nil {
		return "main"
	}
	head := strings.TrimSpace(string(data))
	if branch, found := strings.CutPrefix(head, "ref: refs/heads/"); found {
		return branch
	}
	return "main"
}
