package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Run dispatches a CLI command. It returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	command, rest := args[0], args[1:]
	var err error
	switch command {
	case "login":
		err = cmdLogin(ctx, rest, stdout)
	case "logout":
		err = cmdLogout(rest, stdout)
	case "whoami":
		err = cmdWhoami(ctx, rest, stdout)
	case "init":
		err = cmdInit(ctx, rest, stdout)
	case "up":
		err = cmdUp(ctx, rest, stdout)
	case "deploy":
		err = cmdDeploy(ctx, rest, stdout)
	case "logs":
		err = cmdLogs(ctx, rest, stdout)
	case "env":
		err = cmdEnv(ctx, rest, stdout)
	case "scale":
		err = cmdScale(ctx, rest, stdout)
	case "rollback":
		err = cmdRollback(ctx, rest, stdout)
	case "run":
		err = cmdRun(ctx, rest, stdout)
	case "status":
		err = cmdStatus(ctx, rest, stdout)
	case "apps":
		err = cmdApps(ctx, rest, stdout)
	case "servers":
		err = cmdServers(ctx, rest, stdout)
	case "export":
		err = cmdExport(ctx, rest, stdout)
	case "open":
		err = cmdOpen(ctx, rest, stdout)
	case "admin":
		err = cmdAdmin(ctx, rest, stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, version.Full())
		return 0
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n", version.Binary, command)
		printUsage(stderr)
		return 2
	}

	if err != nil {
		printError(stderr, err)
		return 1
	}
	return 0
}

// printError renders a failure with its cause, impact and fix.
func printError(w io.Writer, err error) {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		fmt.Fprintf(w, "\n%s\n", problem.Text())
		fmt.Fprintf(w, "  Copy this for an AI assistant with: %s status --explain %s\n\n",
			version.Binary, problem.Code)
		return
	}
	fmt.Fprintf(w, "\nError: %s\n\n", err.Error())
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `%s - %s

Usage:
  %s <command> [options]

Getting started:
  login                 Sign in to a panel and store an API token
  up                    Deploy this folder: no repository or settings needed
  init                  Create a %s.toml in this directory
  whoami                Show who you are signed in as

Working with apps:
  deploy                Deploy the current directory's app
  status                Show an app's live state
  logs                  Show or follow an app's logs
  env                   List, set or remove environment variables
  scale                 Change the number of instances or turn on autoscaling
  rollback              Go back to a previous deployment
  run                   Run a one-off command in the app's image
  apps                  List the apps in an environment
  open                  Print an app's URLs

Cluster:
  servers               List the servers in a team

Leaving:
  export                Write this team out as JSON and Kubernetes objects

On the panel's own server:
  admin                 Recover access when nobody can sign in

Other:
  version               Print the version
  help                  Show this message

Every command accepts --json for output a script or an AI assistant can read.

`, version.Name, version.Tagline, version.Binary, version.Binary)
}

// --- login ---

func cmdLogin(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(out)
	panelURL := flags.String("url", "", "the panel's address, for example https://panel.example.com")
	token := flags.String("token", "", "an API token created in the panel under Account, then Tokens")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *panelURL == "" {
		*panelURL = prompt(out, "Panel URL: ")
	}
	if *panelURL == "" {
		return errdoc.BadRequest("A panel URL is needed.")
	}
	if !strings.HasPrefix(*panelURL, "http://") && !strings.HasPrefix(*panelURL, "https://") {
		*panelURL = "https://" + *panelURL
	}

	if *token == "" {
		fmt.Fprintf(out, "\nCreate a token in the panel: Account, then Tokens, then New token.\n")
		*token = promptSecret(out, "API token: ")
	}
	if *token == "" {
		return errdoc.BadRequest("An API token is needed.")
	}

	cfg := Config{PanelURL: *panelURL, Token: *token}
	client := NewClient(cfg)

	var me struct {
		User  store.User   `json:"user"`
		Teams []store.Team `json:"teams"`
	}
	if err := client.Do(ctx, "GET", "/api/me", nil, &me); err != nil {
		return err
	}
	if len(me.Teams) > 0 {
		cfg.TeamID = me.Teams[0].ID
		cfg.TeamName = me.Teams[0].Name
	}
	if err := SaveConfig(cfg); err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(out, map[string]any{
			"panel_url": cfg.PanelURL, "user": me.User.Email, "team": cfg.TeamName,
		})
	}
	fmt.Fprintf(out, "\nSigned in to %s as %s.\n", cfg.PanelURL, me.User.Email)
	if cfg.TeamName != "" {
		fmt.Fprintf(out, "Using the team %s.\n", cfg.TeamName)
	}
	path, _ := ConfigPath()
	fmt.Fprintf(out, "Token stored in %s.\n\n", path)
	return nil
}

func cmdLogout(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("logout", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	if *asJSON {
		// The token is deliberately reported as still valid: signing out here
		// removes a file, and a script that assumes otherwise would leave a
		// live token behind believing it had revoked one.
		return writeJSON(out, map[string]any{
			"signed_out": true, "config_removed": path, "token_revoked": false,
		})
	}
	fmt.Fprintln(out, "Signed out. The token on the panel is still valid; revoke it there if you need to.")
	return nil
}

func cmdWhoami(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("whoami", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	var me struct {
		User  store.User   `json:"user"`
		Teams []store.Team `json:"teams"`
	}
	if err := NewClient(cfg).Do(ctx, "GET", "/api/me", nil, &me); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, me)
	}
	fmt.Fprintf(out, "%s at %s\n", me.User.Email, cfg.PanelURL)
	for _, team := range me.Teams {
		marker := " "
		if team.ID == cfg.TeamID {
			marker = "*"
		}
		fmt.Fprintf(out, " %s %s (%s)\n", marker, team.Name, team.Role)
	}
	return nil
}

// --- apps and status ---

func cmdApps(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("apps", flag.ContinueOnError)
	flags.SetOutput(out)
	envID := flags.String("env", "", "the environment id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)

	environment := *envID
	if environment == "" {
		environment, err = resolveEnvironment(ctx, client, cfg)
		if err != nil {
			return err
		}
	}

	var response struct {
		Items []store.App `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/apps", nil, &response); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, response.Items)
	}

	if len(response.Items) == 0 {
		fmt.Fprintf(out, "No apps here yet. Create one with `%s init` and `%s deploy`.\n",
			version.Binary, version.Binary)
		return nil
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tID\tSOURCE\tSTATUS")
	for _, app := range response.Items {
		source := app.RepoURL
		if app.SourceType == "image" {
			source = app.Image
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", app.Name, app.ID, shorten(source, 40), app.Status)
	}
	return table.Flush()
}

func cmdStatus(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	explain := flags.String("explain", "", "print an error code's explanation for pasting into an AI assistant")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *explain != "" {
		fmt.Fprintf(out, "Error code %s. Open the panel's Activity view and press \"Copy error for AI\" "+
			"to get the full context, including the logs.\n", *explain)
		return nil
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	var status struct {
		Phase           string   `json:"phase"`
		Detail          string   `json:"detail"`
		DesiredReplicas int      `json:"desired_replicas"`
		ReadyReplicas   int      `json:"ready_replicas"`
		Image           string   `json:"image"`
		URLs            []string `json:"urls"`
		Instances       []struct {
			Name     string `json:"name"`
			Status   string `json:"status"`
			Ready    bool   `json:"ready"`
			Restarts int    `json:"restarts"`
			Node     string `json:"node"`
		} `json:"instances"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app+"/status", nil, &status); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, status)
	}

	fmt.Fprintf(out, "\n%s\n", status.Phase)
	if status.Detail != "" {
		fmt.Fprintf(out, "%s\n", status.Detail)
	}
	fmt.Fprintf(out, "\nInstances: %d of %d ready\n", status.ReadyReplicas, status.DesiredReplicas)
	for _, instance := range status.Instances {
		marker := "x"
		if instance.Ready {
			marker = "."
		}
		fmt.Fprintf(out, "  %s %s  %s  on %s", marker, instance.Name, instance.Status, instance.Node)
		if instance.Restarts > 0 {
			fmt.Fprintf(out, "  (%d restarts)", instance.Restarts)
		}
		fmt.Fprintln(out)
	}
	if len(status.URLs) > 0 {
		fmt.Fprintf(out, "\nURLs:\n")
		for _, u := range status.URLs {
			fmt.Fprintf(out, "  %s\n", u)
		}
	}
	fmt.Fprintln(out)
	return nil
}

func cmdOpen(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("open", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}
	var response struct {
		Items []store.Domain `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app+"/domains", nil, &response); err != nil {
		return err
	}
	urls := make([]string, 0, len(response.Items))
	for _, domain := range response.Items {
		scheme := "http://"
		if domain.TLS {
			scheme = "https://"
		}
		urls = append(urls, scheme+domain.Hostname)
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"app": app, "urls": urls})
	}
	if len(urls) == 0 {
		fmt.Fprintln(out, "This app has no domains yet.")
		return nil
	}
	for _, url := range urls {
		fmt.Fprintln(out, url)
	}
	return nil
}

func cmdServers(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("servers", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	teamID, err := resolveTeam(ctx, client, cfg)
	if err != nil {
		return err
	}

	var response struct {
		Items []store.Server `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/servers", nil, &response); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, response.Items)
	}

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tADDRESS\tROLE\tSTATUS\tCPU\tMEMORY")
	for _, server := range response.Items {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%d MB\n",
			server.Name, server.Host, server.Role, server.Status, server.CPUCores, server.MemoryMB)
	}
	return table.Flush()
}

// --- deploy ---

func cmdDeploy(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	commit := flags.String("commit", "", "the commit to deploy; defaults to the branch tip")
	force := flags.Bool("force", false, "rebuild even when nothing about the build has changed")
	follow := flags.Bool("follow", true, "stream the build log until the deploy finishes")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	var deployment store.Deployment
	body := map[string]any{"force": *force}
	if *commit != "" {
		body["commit_sha"] = *commit
	}
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/deploy", body, &deployment); err != nil {
		return err
	}

	if *asJSON && !*follow {
		return writeJSON(out, deployment)
	}
	fmt.Fprintf(out, "Deployment #%d started.\n", deployment.Number)
	if !*follow {
		return nil
	}
	return followDeployment(ctx, client, app, deployment.ID, out, *asJSON)
}

// followDeployment streams a build log and reports the outcome.
func followDeployment(ctx context.Context, client *Client, appID, deploymentID string, out io.Writer, asJSON bool) error {
	path := "/api/events" + Query("topics", "deployment:"+deploymentID)

	finished := make(chan error, 1)
	err := client.Stream(ctx, path, func(event, data string) bool {
		switch event {
		case "log":
			var line struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal([]byte(data), &line); err == nil && line.Line != "" {
				fmt.Fprintln(out, line.Line)
			}
		case "deployment":
			var deployment store.Deployment
			if err := json.Unmarshal([]byte(data), &deployment); err != nil {
				return true
			}
			if !deployment.Status.Terminal() {
				return true
			}
			if deployment.Status == store.DeploySucceeded {
				fmt.Fprintf(out, "\nDeployment #%d succeeded.\n", deployment.Number)
				finished <- nil
			} else {
				finished <- errdoc.New(deployment.ErrorCode, "The deployment failed").
					WithCause("%s", deployment.ErrorMessage).
					WithImpact("The previous version is still running.").
					WithFix("%s", orDefault(deployment.ErrorHint, "Read the build log above for the cause."))
			}
			return false
		case "failed":
			var problem errdoc.Problem
			if err := json.Unmarshal([]byte(data), &problem); err == nil {
				finished <- &problem
				return false
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	select {
	case err := <-finished:
		return err
	default:
		// The stream ended without a terminal event, which happens if the
		// connection dropped. Ask for the final state rather than guessing.
		var deployment store.Deployment
		if err := client.Do(ctx, "GET",
			"/api/apps/"+appID+"/deployments/"+deploymentID, nil, &deployment); err != nil {
			return err
		}
		if deployment.Status == store.DeploySucceeded {
			fmt.Fprintf(out, "\nDeployment #%d succeeded.\n", deployment.Number)
			return nil
		}
		if deployment.Status.Terminal() {
			return errdoc.New(orDefault(deployment.ErrorCode, "deploy.failed"), "The deployment failed").
				WithCause("%s", deployment.ErrorMessage).
				WithFix("%s", orDefault(deployment.ErrorHint, "Read the build log for the cause."))
		}
		fmt.Fprintln(out, "\nThe connection dropped; the deployment is still running. Check it with `"+
			version.Binary+" status`.")
		return nil
	}
}

// --- logs ---

func cmdLogs(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	follow := flags.Bool("follow", false, "keep the stream open")
	tail := flags.Int("tail", 200, "how many lines to show first")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	if *follow {
		return client.Stream(ctx, "/api/apps/"+app+"/logs"+Query(
			"follow", "true", "tail", strconv.Itoa(*tail)),
			func(event, data string) bool {
				var line string
				if err := json.Unmarshal([]byte(data), &line); err == nil {
					fmt.Fprintln(out, line)
				}
				return true
			})
	}

	var response struct {
		Lines []string `json:"lines"`
	}
	if err := client.Do(ctx, "GET",
		"/api/apps/"+app+"/logs"+Query("tail", strconv.Itoa(*tail)), nil, &response); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, response)
	}
	for _, line := range response.Lines {
		fmt.Fprintln(out, line)
	}
	return nil
}

// --- env ---

func cmdEnv(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("env", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	secret := flags.Bool("secret", false, "store the value encrypted and never show it again")
	buildTime := flags.Bool("build", false, "the value is needed during the build, so setting it rebuilds")
	asJSON := flags.Bool("json", false, "print the result as JSON")

	// The subcommand is the first word that is not a flag, so all of these
	// work: `env list --app web`, `env --app web list`, `env set A=1 --app web`.
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}

	sub, values := envSubcommand(positional)

	// Whether --secret was on the command line at all, as opposed to left at
	// its default. See the set subcommand.
	secretGiven := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "secret" {
			secretGiven = true
		}
	})

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	switch sub {
	case "list":
		var response struct {
			Items []store.Variable `json:"items"`
		}
		if err := client.Do(ctx, "GET", "/api/apps/"+app+"/variables", nil, &response); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, response.Items)
		}
		if len(response.Items) == 0 {
			fmt.Fprintln(out, "This app has no environment variables yet.")
			return nil
		}
		sort.Slice(response.Items, func(i, j int) bool { return response.Items[i].Key < response.Items[j].Key })
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, variable := range response.Items {
			value := variable.Value
			if variable.IsSecret {
				// A secret is write-only once set; a placeholder is the honest
				// thing rather than pretending it is empty.
				value = "(secret)"
			}
			fmt.Fprintf(table, "%s\t%s\n", variable.Key, value)
		}
		return table.Flush()

	case "set":
		if len(values) == 0 {
			return errdoc.BadRequest("Give at least one KEY=value pair.")
		}
		for _, pair := range values {
			key, value, found := strings.Cut(pair, "=")
			if !found {
				return errdoc.BadRequest(fmt.Sprintf("%q is not in the form KEY=value.", pair))
			}
			body := map[string]any{"key": key, "value": value, "build_time": *buildTime}
			// Only when --secret was actually given. Sending the flag's default
			// said "not a secret" on every set, so overwriting an API key
			// without remembering the flag turned it into a variable anybody
			// could read back. Left out, the panel keeps a secret a secret and
			// decides a new one by its name and value.
			if secretGiven {
				body["is_secret"] = *secret
			}
			var result struct {
				RequiresRebuild bool `json:"requires_rebuild"`
			}
			if err := client.Do(ctx, "PUT", "/api/apps/"+app+"/variables", body, &result); err != nil {
				return err
			}
			if result.RequiresRebuild {
				fmt.Fprintf(out, "%s set. It is used during the build, so the next deploy will rebuild.\n", key)
			} else {
				fmt.Fprintf(out, "%s set and rolled out. No rebuild was needed.\n", key)
			}
		}
		return nil

	case "unset":
		if len(values) == 0 {
			return errdoc.BadRequest("Give at least one variable name.")
		}
		for _, key := range values {
			if err := client.Do(ctx, "DELETE", "/api/apps/"+app+"/variables/"+key, nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s removed.\n", key)
		}
		return nil

	default:
		return errdoc.BadRequest(fmt.Sprintf("%q is not an env subcommand. Use list, set or unset.", sub))
	}
}

// envSubcommand works out what `env` was asked to do.
//
// With no subcommand it lists, and a bare KEY=value is treated as a set,
// because that is what someone typing it means.
func envSubcommand(positional []string) (sub string, values []string) {
	if len(positional) == 0 {
		return "list", nil
	}
	switch positional[0] {
	case "list", "ls":
		return "list", positional[1:]
	case "set":
		return "set", positional[1:]
	case "unset", "rm", "remove", "delete":
		return "unset", positional[1:]
	}
	if strings.Contains(positional[0], "=") {
		return "set", positional
	}
	return positional[0], positional[1:]
}

// --- scale ---

func cmdScale(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("scale", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	instances := flags.Int("instances", -1, "a fixed number of instances")
	auto := flags.Bool("auto", false, "turn on autoscaling")
	minReplicas := flags.Int("min", 0, "the fewest instances when autoscaling")
	maxReplicas := flags.Int("max", 0, "the most instances when autoscaling")
	cpuTarget := flags.Int("cpu", 0, "the CPU percentage to scale on")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if *instances >= 0 {
		body["replicas"] = *instances
		body["autoscale"] = false
	}
	if *auto {
		body["autoscale"] = true
		if *minReplicas > 0 {
			body["min_replicas"] = *minReplicas
		}
		if *maxReplicas > 0 {
			body["max_replicas"] = *maxReplicas
		}
		if *cpuTarget > 0 {
			body["cpu_target"] = *cpuTarget
		}
	}
	if len(body) == 0 {
		// With no arguments, show the current settings rather than doing nothing.
		var current map[string]any
		if err := client.Do(ctx, "GET", "/api/apps/"+app+"/scaling", nil, &current); err != nil {
			return err
		}
		return writeJSON(out, current)
	}

	var result struct {
		Scaling  map[string]any `json:"scaling"`
		Warnings []struct {
			Severity string `json:"severity"`
			Title    string `json:"title"`
			Detail   string `json:"detail"`
			Fix      string `json:"fix"`
		} `json:"warnings"`
	}
	if err := client.Do(ctx, "PUT", "/api/apps/"+app+"/scaling", body, &result); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, result)
	}

	fmt.Fprintln(out, "Scaling updated.")
	for _, warning := range result.Warnings {
		fmt.Fprintf(out, "\n  %s: %s\n    %s\n    Fix: %s\n",
			strings.ToUpper(warning.Severity), warning.Title, warning.Detail, warning.Fix)
	}
	return nil
}

// --- rollback ---

func cmdRollback(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("rollback", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	to := flags.String("to", "", "the deployment id to go back to; defaults to the one before the current version")
	list := flags.Bool("list", false, "show the deploy history")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	var history struct {
		Items []store.Deployment `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app+"/deployments"+Query("limit", "20"), nil, &history); err != nil {
		return err
	}

	if *list {
		if *asJSON {
			return writeJSON(out, history.Items)
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "#\tSTATUS\tCOMMIT\tWHEN\tID")
		for _, deployment := range history.Items {
			fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s\n",
				deployment.Number, deployment.Status, shorten(deployment.CommitSHA, 8),
				humanTime(deployment.CreatedAt), deployment.ID)
		}
		return table.Flush()
	}

	target := *to
	if target == "" {
		// The one before the current version: skip the newest successful
		// deployment, which is what is running now.
		seen := 0
		for _, deployment := range history.Items {
			if deployment.Status != store.DeploySucceeded {
				continue
			}
			seen++
			if seen == 2 {
				target = deployment.ID
				break
			}
		}
	}
	if target == "" {
		return errdoc.BadRequest("There is no earlier successful deployment to go back to.")
	}

	var deployment store.Deployment
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/rollback/"+target, nil, &deployment); err != nil {
		return err
	}
	fmt.Fprintf(out, "Rolling back. Deployment #%d started.\n", deployment.Number)
	return followDeployment(ctx, client, app, deployment.ID, out, *asJSON)
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func humanTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	elapsed := time.Since(t)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	default:
		return t.Format("2 Jan 15:04")
	}
}

func writeJSON(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

func prompt(w io.Writer, label string) string {
	fmt.Fprint(w, label)
	var line string
	// A read that fails leaves line empty, which the caller already handles as
	// "the user gave nothing".
	_, _ = fmt.Scanln(&line)
	return strings.TrimSpace(line)
}

// promptSecret reads without echoing, so a token does not end up in a
// screenshot or a shared terminal's scrollback.
func promptSecret(w io.Writer, label string) string {
	fmt.Fprint(w, label)
	stdin := int(os.Stdin.Fd())
	if !term.IsTerminal(stdin) {
		var line string
		_, _ = fmt.Scanln(&line)
		return strings.TrimSpace(line)
	}
	data, err := term.ReadPassword(stdin)
	fmt.Fprintln(w)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// parseInterspersed parses flags that may appear before, after or between
// positional arguments.
//
// Go's flag package stops at the first argument that is not a flag, which would
// make `env set KEY=value --app web` silently ignore --app. Parsing in rounds
// and collecting what each round stops on handles both orders without having to
// know which flags take a value.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	remaining := args
	for len(remaining) > 0 {
		if err := flags.Parse(remaining); err != nil {
			return nil, err
		}
		rest := flags.Args()
		if len(rest) == 0 {
			break
		}
		// The first of these is what parsing stopped on; everything after it
		// may contain more flags.
		positional = append(positional, rest[0])
		remaining = rest[1:]
	}
	return positional, nil
}

// cmdRun runs a one-off command in the app's own image.
//
// Everything after `--` is the command, unsplit, because people type
// `npm run migrate && npm run seed` and reassembling that from arguments would
// get the quoting subtly wrong.
func cmdRun(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	command := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if command == "" {
		return errdoc.BadRequest(fmt.Sprintf(
			"Give the command to run, for example `%s run --app app_123 -- npm run migrate`.",
			version.Binary))
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}

	var started struct {
		Run string `json:"run"`
	}
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/run",
		map[string]string{"command": command}, &started); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, started)
	}
	fmt.Fprintf(out, "Running: %s\n", command)
	fmt.Fprintf(out, "Waiting for it to finish. Ctrl-C stops the waiting, not the command.\n\n")

	// follow=true, and it is what makes this command do what it says. Without
	// it the panel returns whatever the container had printed by the time the
	// pod was seen running, which for anything slower than a second or two is
	// nothing at all: "Running: npm run migrate", a blank line, and an exit
	// status of zero.
	//
	// DoLong because the wait is the migration's, not the network's. Ctrl-C
	// still ends it; the command in the cluster carries on, and the output is
	// there afterwards.
	var logs struct {
		Lines []string `json:"lines"`
	}
	if err := client.DoLong(ctx, "GET",
		"/api/apps/"+app+"/runs/"+started.Run+"/logs?follow=true", nil, &logs); err != nil {
		return err
	}
	for _, line := range logs.Lines {
		fmt.Fprintln(out, line)
	}
	return nil
}
