package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
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
	case "secrets":
		err = cmdSecrets(ctx, rest, stdout)
	case "scale":
		err = cmdScale(ctx, rest, stdout)
	case "gpus", "gpu":
		err = cmdGPUs(ctx, rest, stdout)
	case "rollback":
		err = cmdRollback(ctx, rest, stdout)
	case "lock":
		err = cmdLock(ctx, rest, stdout)
	case "unlock":
		err = cmdUnlock(ctx, rest, stdout)
	case "maintenance":
		err = cmdMaintenance(ctx, rest, stdout)
	case "run":
		err = cmdRun(ctx, rest, stdout)
	case "status":
		err = cmdStatus(ctx, rest, stdout)
	case "events":
		err = cmdEvents(ctx, rest, stdout)
	case "drift":
		err = cmdDrift(ctx, rest, stdout)
	case "apps":
		err = cmdApps(ctx, rest, stdout)
	case "servers":
		err = cmdServers(ctx, rest, stdout)
	case "cloud":
		err = cmdCloud(ctx, rest, stdout)
	case "git":
		err = cmdGit(ctx, rest, stdout)
	case "db":
		err = cmdDB(ctx, rest, stdout)
	case "processes", "ps":
		err = cmdProcesses(ctx, rest, stdout)
	case "files":
		err = cmdFiles(ctx, rest, stdout)
	case "ports":
		err = cmdPorts(ctx, rest, stdout)
	case "preview":
		err = cmdPreview(ctx, rest, stdout)
	case "scan":
		err = cmdScan(ctx, rest, stdout)
	case "certs":
		err = cmdCerts(ctx, rest, stdout)
	case "domains":
		err = cmdDomains(ctx, rest, stdout)
	case "dns":
		err = cmdDNS(ctx, rest, stdout)
	case "drains":
		err = cmdDrains(ctx, rest, stdout)
	case "templates":
		err = cmdTemplates(ctx, rest, stdout)
	case "plan", "apply":
		err = cmdBlueprint(ctx, command, rest, stdout)
	case "export":
		err = cmdExport(ctx, rest, stdout)
	case "open":
		err = cmdOpen(ctx, rest, stdout)
	case "admin":
		err = cmdAdmin(ctx, rest, stdout)
	case "api":
		err = cmdAPI(ctx, rest, stdout)
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

	// The command's own --help printed its flags; that is what was asked.
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var exit commandExit
	if errors.As(err, &exit) {
		fmt.Fprintf(stderr, "\nThe command exited with status %d.\n", exit.code)
		return max(1, min(exit.code, 255))
	}
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return 0
}

// printError renders a failure with its cause, impact and fix.
//
// The problem is kept, too, so `status --explain CODE` can print it whole for
// an AI assistant. That line used to promise an explanation and print a
// pointer to the panel.
func printError(w io.Writer, err error) {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		fmt.Fprintf(w, "\n%s\n", problem.Text())
		if saveLastError(problem) == nil {
			fmt.Fprintf(w, "  Copy this for an AI assistant with: %s status --explain %s\n\n",
				version.Binary, problem.Code)
		}
		return
	}
	fmt.Fprintf(w, "\nError: %s\n\n", err.Error())
}

// lastErrorPath is where the last problem printed is kept, beside the
// configuration and as private as it.
func lastErrorPath() (string, error) {
	path, err := ConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "last-error.json"), nil
}

type lastError struct {
	Problem *errdoc.Problem `json:"problem"`
	Command []string        `json:"command"`
	Version string          `json:"version"`
	At      time.Time       `json:"at"`
}

func saveLastError(problem *errdoc.Problem) error {
	path, err := lastErrorPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(lastError{Problem: problem, Command: redactArgs(os.Args[1:]), Version: version.Full(), At: time.Now().UTC()})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// redactArgs keeps a command line fit to paste: a value given as KEY=value
// keeps its key, never its value.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		if key, _, ok := strings.Cut(arg, "="); ok && !strings.HasPrefix(arg, "-") {
			arg = key + "=…"
		}
		out[i] = arg
	}
	return out
}

// explainError prints the last problem this CLI printed, when it is the one
// asked about, as something to paste into an assistant.
func explainError(out io.Writer, code string) error {
	path, err := lastErrorPath()
	if err != nil {
		return err
	}
	var last lastError
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &last)
	}
	if err != nil || last.Problem == nil || last.Problem.Code != code {
		return errdoc.New("cli.explain_unknown", "There is no error with that code to explain").
			WithCause("The last error this CLI printed is not %s.", code).
			WithImpact("Nothing was printed.").
			WithFix("Run the command again, then `%s status --explain` with the code it prints. An error in the panel has a \"Copy error for AI\" button in Activity.", version.Binary)
	}
	p := last.Problem
	fmt.Fprintf(out, "I ran `%s %s` (%s) and it failed.\n\n", version.Binary, strings.Join(last.Command, " "), last.Version)
	fmt.Fprintf(out, "Error code: %s\nWhat happened: %s\n", p.Code, p.Title)
	for _, part := range []struct{ label, text string }{
		{"Cause", p.Cause}, {"Impact", p.Impact}, {"Suggested fix", p.Fix}, {"Documentation", p.DocsPath},
	} {
		if strings.TrimSpace(part.text) != "" {
			fmt.Fprintf(out, "%s: %s\n", part.label, part.text)
		}
	}
	fmt.Fprintf(out, "When: %s\n\nWhat should I do next?\n", last.At.Format(time.RFC3339))
	return nil
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
  events                What Kubernetes said about an app's instances, newest first
  drift                 What was changed outside the panel, with kubectl; --repair puts it back
  env                   List, set, import or remove environment variables, or refresh
                        the ones read from a secret manager
  scale                 Change the number of instances or turn on autoscaling
  gpus                  Show or change the GPUs the app's instances are given
  processes             List, add or stop the app's workers and other processes
  files                 List, save or remove files the app reads, such as an nginx.conf
  ports                 Open or close a port that is not HTTP, such as a game server's
  preview               Preview a branch without a pull request, or --close it
  rollback              Go back to a previous deployment
  scan                  Show the known vulnerabilities in the app's image, or scan it now
  lock, unlock          Stop every deploy and rollback of an app, and start them again
  maintenance           Show visitors a page instead of the app, and stop
  run                   Run a one-off command in the app's image
  apps                  List the apps in an environment
  open                  Print an app's URLs
  domains               List, add or remove an app's domains; --manage-dns creates the record
  git                   List Git connections, and the repositories and branches they read

Described in a file:
  plan                  Say what skifity.yaml would change in the environment
  apply                 Make the environment what skifity.yaml says

Templates:
  templates             List the templates the team can install, built in and its own
  templates catalogues  List, add, refresh or remove the team's own template catalogues

Databases:
  db                    List the managed databases in an environment
  db connect            Reach one from this computer, on a local port
  db stop, db start     Stop one and keep its disk, and start it again
  db resize             Change its CPU and memory, or grow its disk
  db password           Give it a new password, and its apps the new connection string
  db import             Load a dump file, or - for standard input, into it

Cluster:
  servers               List the servers in a team
  servers create        Create a server at Hetzner Cloud and join it to the cluster
  cloud providers       List, add, test or remove the team's cloud connections
  certs                 List, upload or remove the team's own TLS certificates
  dns providers         List, connect, test or remove the team's DNS providers
  drains                List, add, test or remove where the team's logs are shipped

Secret managers:
  secrets connections   List, add, test or remove the Vault, Infisical, Doppler or
                        AWS Secrets Manager connections variables are read from

Leaving:
  export                Write this team out as JSON and Kubernetes objects

Building on it:
  api spec              Print the OpenAPI description of the panel's HTTP API

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
	teamFlag := flags.String("team", "", "the team to use, by id, slug or name, when you are in more than one")
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
	// The first team, always, was the one stored, and every later command
	// acted in it without saying so. One team is that team; more are asked
	// about, or named with --team.
	team, err := chooseTeam(me.Teams, *teamFlag, out)
	if err != nil {
		return err
	}
	cfg.TeamID, cfg.TeamName = team.ID, team.Name
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

// chooseTeam picks the team login stores: the one named, the only one, or
// the one picked from a list when there is somebody to ask. With several and
// nobody to ask, none is stored, and each command says which to name.
func chooseTeam(teams []store.Team, named string, out io.Writer) (store.Team, error) {
	named = strings.TrimSpace(named)
	if named != "" {
		for _, team := range teams {
			if team.ID == named || team.Slug == named || strings.EqualFold(team.Name, named) {
				return team, nil
			}
		}
		return store.Team{}, errdoc.BadRequest(fmt.Sprintf("You are not in a team called %s. You are in: %s.", named, teamNames(teams)))
	}
	switch {
	case len(teams) == 1:
		return teams[0], nil
	case len(teams) == 0 || !isInteractive():
		return store.Team{}, nil
	}
	fmt.Fprintln(out, "\nYou are in more than one team:")
	for i, team := range teams {
		fmt.Fprintf(out, "  %d. %s\n", i+1, team.Name)
	}
	answer := prompt(out, "Which one? ")
	if n, err := strconv.Atoi(strings.TrimSpace(answer)); err == nil && n >= 1 && n <= len(teams) {
		return teams[n-1], nil
	}
	return chooseTeam(teams, answer, out)
}

func teamNames(teams []store.Team) string {
	names := make([]string, 0, len(teams))
	for _, team := range teams {
		names = append(names, team.Name+" ("+team.ID+")")
	}
	return strings.Join(names, ", ")
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
		return explainError(out, *explain)
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
	if len(args) > 0 && args[0] == "create" {
		return cmdServersCreate(ctx, args[1:], out)
	}
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
	accept := flags.Bool("accept-vulnerabilities", false,
		"deploy even if the image has a critical vulnerability with a fix, when the panel stops those; recorded in the activity log")
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
	if *accept {
		body["accept_vulnerabilities"] = true
	}
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/deploy", body, &deployment); err != nil {
		return err
	}

	if *asJSON && !*follow {
		return writeJSON(out, deployment)
	}
	if !*asJSON {
		fmt.Fprintf(out, "Deployment #%d started.\n", deployment.Number)
	}
	if !*follow {
		return nil
	}
	final, err := followDeployment(ctx, client, app, deployment.ID, out, *asJSON)
	return printFollowed(out, *asJSON, final, err)
}

// followPoll is how often followDeployment asks for the deployment's state
// while it streams. A variable, so a test need not wait for it.
var followPoll = 5 * time.Second

// followDeployment streams a build log until the deployment ends, and answers
// with how it ended. With asJSON it prints nothing: the caller prints one JSON
// document, which is what a script parses — build lines and "succeeded" in
// the middle of it made --json output that was not JSON.
//
// The stream is opened after the deploy started, and the panel replays
// nothing to a new subscriber, so a deploy that ended before the stream
// connected was never heard of and this waited for ever. The deployment is
// asked for every followPoll as well, which ends the wait whatever the stream
// missed.
func followDeployment(ctx context.Context, client *Client, appID, deploymentID string, out io.Writer, asJSON bool) (store.Deployment, error) {
	path := "/api/events" + Query("topics", "deployment:"+deploymentID)
	read := func(ctx context.Context) (store.Deployment, error) {
		var deployment store.Deployment
		err := client.Do(ctx, "GET", "/api/apps/"+appID+"/deployments/"+deploymentID, nil, &deployment)
		return deployment, err
	}

	streaming, stop := context.WithCancel(ctx)
	defer stop()
	ended := make(chan store.Deployment, 2)
	go func() {
		defer runsafe.Recover(nil, "check a deployment's state", nil)
		ticker := time.NewTicker(followPoll)
		defer ticker.Stop()
		for {
			select {
			case <-streaming.Done():
				return
			case <-ticker.C:
			}
			if deployment, err := read(streaming); err == nil && deployment.Status.Terminal() {
				select {
				case ended <- deployment:
				default:
				}
				stop()
				return
			}
		}
	}()

	var failure *errdoc.Problem
	err := client.Stream(streaming, path, func(event, data string) bool {
		switch event {
		case "log":
			var line struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal([]byte(data), &line); err == nil && line.Line != "" && !asJSON {
				fmt.Fprintln(out, line.Line)
			}
		case "deployment":
			var deployment store.Deployment
			if err := json.Unmarshal([]byte(data), &deployment); err != nil || !deployment.Status.Terminal() {
				return true
			}
			select {
			case ended <- deployment:
			default:
			}
			return false
		case "failed":
			var problem errdoc.Problem
			if err := json.Unmarshal([]byte(data), &problem); err == nil {
				failure = &problem
				return false
			}
		}
		return true
	})
	stop()

	select {
	case deployment := <-ended:
		return deploymentOutcome(deployment, out, asJSON)
	default:
	}
	if failure != nil {
		deployment, _ := read(ctx)
		return deployment, failure
	}
	if err != nil && ctx.Err() != nil {
		return store.Deployment{}, err
	}
	// The stream ended without a terminal event, which happens if the
	// connection dropped. Ask for the final state rather than guessing.
	deployment, readErr := read(ctx)
	if readErr != nil {
		return store.Deployment{}, readErr
	}
	if deployment.Status.Terminal() {
		return deploymentOutcome(deployment, out, asJSON)
	}
	if !asJSON {
		fmt.Fprintln(out, "\nThe connection dropped; the deployment is still running. Check it with `"+
			version.Binary+" status`.")
	}
	return deployment, nil
}

// deploymentOutcome says how a finished deployment went.
func deploymentOutcome(deployment store.Deployment, out io.Writer, asJSON bool) (store.Deployment, error) {
	if deployment.Status == store.DeploySucceeded {
		if !asJSON {
			fmt.Fprintf(out, "\nDeployment #%d succeeded.\n", deployment.Number)
		}
		return deployment, nil
	}
	return deployment, errdoc.New(orDefault(deployment.ErrorCode, "deploy.failed"), "The deployment failed").
		WithCause("%s", orDefault(deployment.ErrorMessage, "It ended "+string(deployment.Status)+".")).
		WithImpact("The previous version is still running.").
		WithFix("%s", orDefault(deployment.ErrorHint, "Read the build log for the cause."))
}

// printFollowed prints a followed deployment as JSON when asked, whatever the
// outcome, and passes the outcome on.
func printFollowed(out io.Writer, asJSON bool, deployment store.Deployment, err error) error {
	if asJSON && deployment.ID != "" {
		if writeErr := writeJSON(out, deployment); writeErr != nil {
			return writeErr
		}
	}
	return err
}

// --- logs ---

func cmdLogs(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	follow := flags.Bool("follow", false, "keep the stream open")
	tail := flags.Int("tail", 200, "how many lines to show first")
	process := flags.String("process", "", "one of the app's other processes, such as worker")
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
		// With --json, one JSON object a line: a stream has no end to put a
		// document's closing bracket after.
		encoder := json.NewEncoder(out)
		return client.Stream(ctx, "/api/apps/"+app+"/logs"+Query(
			"follow", "true", "tail", strconv.Itoa(*tail), "process", *process),
			func(event, data string) bool {
				var line string
				if err := json.Unmarshal([]byte(data), &line); err != nil {
					return true
				}
				if *asJSON {
					_ = encoder.Encode(map[string]string{"line": line})
				} else {
					fmt.Fprintln(out, line)
				}
				return true
			})
	}

	var response struct {
		Lines []string `json:"lines"`
	}
	if err := client.Do(ctx, "GET",
		"/api/apps/"+app+"/logs"+Query("tail", strconv.Itoa(*tail), "process", *process), nil, &response); err != nil {
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
	from := flags.String("from", "", "read the value from a secret manager instead of storing it: connection:path or connection:path#key")
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
	secretGiven, buildTimeGiven := false, false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "secret" {
			secretGiven = true
		}
		if f.Name == "build" {
			buildTimeGiven = true
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
			switch {
			case variable.Reference != nil:
				// Where it is read from; its value is never in the panel.
				value = "(from " + variable.Reference.String() + ")"
			case variable.IsSecret:
				// A secret is write-only once set; a placeholder is the honest
				// thing rather than pretending it is empty.
				value = "(secret)"
			}
			fmt.Fprintf(table, "%s\t%s\n", variable.Key, value)
		}
		return table.Flush()

	case "refresh":
		var result struct {
			References       int               `json:"references"`
			Changed          []string          `json:"changed"`
			BuildTimeChanged []string          `json:"build_time_changed"`
			RolledOut        bool              `json:"rolled_out"`
			Deployment       *store.Deployment `json:"deployment,omitempty"`
			NotDeployed      bool              `json:"not_deployed,omitempty"`
		}
		if err := client.Do(ctx, "POST", "/api/apps/"+app+"/variables/refresh", nil, &result); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, result)
		}
		switch {
		case result.References == 0:
			fmt.Fprintln(out, "None of this app's variables is read from a secret manager.")
		case len(result.Changed) == 0:
			fmt.Fprintf(out, "Nothing changed in the secret managers, so nothing was rolled out (%d read from one).\n", result.References)
		case result.NotDeployed:
			fmt.Fprintf(out, "%s changed. The app has not been deployed yet; its first deploy reads them.\n", strings.Join(result.Changed, ", "))
		case result.Deployment != nil:
			fmt.Fprintf(out, "%s changed, and the build reads %s, so the running version is being rebuilt with it: deployment #%d.\n",
				strings.Join(result.Changed, ", "), strings.Join(result.BuildTimeChanged, ", "), result.Deployment.Number)
		default:
			fmt.Fprintf(out, "%s changed and was rolled out. No rebuild was needed.\n", strings.Join(result.Changed, ", "))
		}
		return nil

	case "set", "import":
		if *from != "" {
			return setFromSecretManager(ctx, client, app, sub, values, *from, buildTimeGiven, *buildTime, *asJSON, out)
		}
		// Every pair in one request: all of them or none, and one rollout.
		// One request each rolled the app out once per pair.
		pairs := map[string]string{}
		order := []string{}
		if sub == "import" {
			if len(values) != 1 {
				return errdoc.BadRequest(fmt.Sprintf("Give the file to import, for example `%s env import .env`.", version.Binary))
			}
			text, err := os.ReadFile(values[0])
			if err != nil {
				return errdoc.BadRequest(fmt.Sprintf("Could not read %s: %v", values[0], err))
			}
			pairs = parseDotEnv(string(text))
			for key := range pairs {
				order = append(order, key)
			}
			sort.Strings(order)
			if len(order) == 0 {
				return errdoc.BadRequest(fmt.Sprintf("%s has no KEY=value lines.", values[0]))
			}
		} else {
			if len(values) == 0 {
				return errdoc.BadRequest("Give at least one KEY=value pair.")
			}
			for _, pair := range values {
				key, value, found := strings.Cut(pair, "=")
				if !found {
					return errdoc.BadRequest(fmt.Sprintf("%q is not in the form KEY=value.", pair))
				}
				if _, again := pairs[key]; !again {
					order = append(order, key)
				}
				pairs[key] = value
			}
		}
		set := make([]map[string]any, 0, len(order))
		for _, key := range order {
			item := map[string]any{"key": key, "value": pairs[key]}
			// Only when --build was given, as with --secret: sending false
			// took a variable the build reads out of the build.
			if buildTimeGiven {
				item["build_time"] = *buildTime
			}
			// Only when --secret was actually given. Sending the flag's
			// default said "not a secret" on every set, so overwriting an API
			// key without remembering the flag turned it into a variable
			// anybody could read back. Left out, the panel keeps a secret a
			// secret and decides a new one by its name and value.
			if secretGiven {
				item["is_secret"] = *secret
			}
			set = append(set, item)
		}
		var result struct {
			Set             []store.Variable `json:"set"`
			RequiresRebuild bool             `json:"requires_rebuild"`
		}
		if err := client.Do(ctx, "POST", "/api/apps/"+app+"/variables/batch", map[string]any{"set": set}, &result); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, result)
		}
		names := strings.Join(order, ", ")
		if result.RequiresRebuild {
			fmt.Fprintf(out, "%s set. Used during the build, so the next deploy will rebuild.\n", names)
		} else {
			fmt.Fprintf(out, "%s set and rolled out, once. No rebuild was needed.\n", names)
		}
		for _, variable := range result.Set {
			if variable.IsSecret {
				fmt.Fprintf(out, "%s is stored as a secret: it will not be shown again.\n", variable.Key)
			}
		}
		return nil

	case "unset":
		if len(values) == 0 {
			return errdoc.BadRequest("Give at least one variable name.")
		}
		if err := client.Do(ctx, "POST", "/api/apps/"+app+"/variables/batch", map[string]any{"unset": values}, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]any{"unset": values})
		}
		fmt.Fprintf(out, "%s removed.\n", strings.Join(values, ", "))
		return nil

	default:
		return errdoc.BadRequest(fmt.Sprintf("%q is not an env subcommand. Use list, set, unset, import or refresh.", sub))
	}
}

// setFromSecretManager makes one variable a reference: its value is read from
// a secret manager at every deploy, and never stored in the panel.
func setFromSecretManager(ctx context.Context, client *Client, app, sub string, values []string, from string,
	buildTimeGiven, buildTime, asJSON bool, out io.Writer) error {
	if sub != "set" || len(values) != 1 || strings.Contains(values[0], "=") {
		return errdoc.BadRequest(fmt.Sprintf("--from sets one variable, named without a value: `%s env set STRIPE_KEY --from company-vault:shop#stripe_key`.",
			version.Binary))
	}
	reference, err := referenceBody(from)
	if err != nil {
		return err
	}
	body := map[string]any{"key": values[0], "from": reference}
	if buildTimeGiven {
		body["build_time"] = buildTime
	}
	var result struct {
		Variable        store.Variable `json:"variable"`
		RequiresRebuild bool           `json:"requires_rebuild"`
	}
	if err := client.Do(ctx, "PUT", "/api/apps/"+app+"/variables", body, &result); err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, result)
	}
	where := from
	if result.Variable.Reference != nil {
		where = result.Variable.Reference.String()
	}
	fmt.Fprintf(out, "%s is read from %s at every deploy. It was read once to check it, and its value is not stored in the panel.\n",
		values[0], where)
	if result.RequiresRebuild {
		fmt.Fprintln(out, "It is used during the build, so the next deploy will rebuild.")
	} else {
		fmt.Fprintln(out, "Rolled out. No rebuild was needed.")
	}
	return nil
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
	case "import":
		return "import", positional[1:]
	case "refresh":
		return "refresh", positional[1:]
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
	limits := *minReplicas > 0 || *maxReplicas > 0 || *cpuTarget > 0
	if *instances >= 0 {
		if *auto || limits {
			return errdoc.BadRequest("--instances is a fixed number, and --auto, --min, --max and --cpu are autoscaling. Give one or the other.")
		}
		body["replicas"] = *instances
		body["autoscale"] = false
	}
	if *auto {
		body["autoscale"] = true
	}
	// The limits on their own change them and nothing else: `scale --max 8`
	// on an app that autoscales sent nothing, printed the settings and exited
	// 0, as if it had worked.
	if *minReplicas > 0 {
		body["min_replicas"] = *minReplicas
	}
	if *maxReplicas > 0 {
		body["max_replicas"] = *maxReplicas
	}
	if *cpuTarget > 0 {
		body["cpu_target"] = *cpuTarget
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
	if !*asJSON {
		fmt.Fprintf(out, "Rolling back. Deployment #%d started.\n", deployment.Number)
	}
	final, err := followDeployment(ctx, client, app, deployment.ID, out, *asJSON)
	return printFollowed(out, *asJSON, final, err)
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
	if !*asJSON {
		fmt.Fprintf(out, "Running: %s\n", command)
		fmt.Fprintf(out, "Waiting for it to finish. Ctrl-C stops the waiting, not the command.\n\n")
	}

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
		Run      string   `json:"run"`
		Lines    []string `json:"lines"`
		Finished bool     `json:"finished"`
		ExitCode *int     `json:"exit_code,omitempty"`
	}
	if err := client.DoLong(ctx, "GET",
		"/api/apps/"+app+"/runs/"+started.Run+"/logs?follow=true", nil, &logs); err != nil {
		return err
	}
	if *asJSON {
		if err := writeJSON(out, logs); err != nil {
			return err
		}
	} else {
		for _, line := range logs.Lines {
			fmt.Fprintln(out, line)
		}
	}
	// The command's own exit status is this one's, so a CI step that runs a
	// migration fails when the migration does. It exited 0 whatever happened.
	switch {
	case !logs.Finished || logs.ExitCode == nil:
		return errdoc.New("run.outcome_unknown", "How the command ended could not be read").
			WithCause("Its output ended, and the panel could not read its exit status: %s is still running, or its pod is gone.", started.Run).
			WithImpact("It may have succeeded or failed; the output above is all there is.").
			WithFix("Look at the app's Console tab, or run it again.")
	case *logs.ExitCode != 0:
		return commandExit{code: *logs.ExitCode}
	}
	return nil
}

// commandExit is a command run in the cluster that ended with a status other
// than zero, which becomes this process's own.
type commandExit struct{ code int }

func (e commandExit) Error() string {
	return fmt.Sprintf("the command exited with status %d", e.code)
}

// --- lock ---

// cmdLock stops every deploy and rollback of an app until it is unlocked:
// `skifity lock "incident 42"`.
func cmdLock(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("lock", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(strings.Join(positional, " "))
	if reason == "" {
		return errdoc.BadRequest(fmt.Sprintf("Say why, for example `%s lock \"incident 42: failover\"`.", version.Binary))
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
	var lock store.DeployLock
	if err := client.Do(ctx, "PUT", "/api/apps/"+app+"/lock", map[string]string{"reason": reason}, &lock); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, lock)
	}
	fmt.Fprintf(out, "Deploys and rollbacks are locked: %s\nUnlock with `%s unlock`.\n", lock.Reason, version.Binary)
	return nil
}

func cmdUnlock(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("unlock", flag.ContinueOnError)
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
	if err := client.Do(ctx, "DELETE", "/api/apps/"+app+"/lock", nil, nil); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, map[string]bool{"unlocked": true})
	}
	fmt.Fprintln(out, "Deploys are unlocked.")
	return nil
}
