package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Changing an app after it was created: its settings, a restart, deleting it,
// and stopping a deploy. Each is the request the panel's own buttons make.

// appSettingFlag is one of `apps set`'s flags, and the setting it changes.
type appSettingFlag struct {
	flag, setting, usage string
	kind                 flagKind
}

type flagKind int

const (
	textFlag flagKind = iota
	numberFlag
	switchFlag
)

// appSettingFlags are the settings `apps set` changes: the ones a first deploy
// most often gets wrong — the port, the start command, the health check, the
// folder, the build command, the memory — and the rest of what the settings
// tab saves. Only those given are sent, so nothing else is touched.
var appSettingFlags = []appSettingFlag{
	{"name", "name", "what the app is called", textFlag},
	{"branch", "branch", "the branch a deploy builds", textFlag},
	{"root", "root_dir", "the folder in the repository the app is in", textFlag},
	{"builder", "builder", "how it is built: auto, railpack, dockerfile or static", textFlag},
	{"dockerfile", "dockerfile_path", "the Dockerfile to build with, from the root", textFlag},
	{"build", "build_command", "the command that builds it, such as 'npm run build'", textFlag},
	{"output-dir", "static_dir", "the folder a static site serves, such as dist", textFlag},
	{"image", "image", "the image an image app runs", textFlag},
	{"port", "port", "the port it listens on", numberFlag},
	{"start", "start_command", "the command it starts with", textFlag},
	{"release", "release_command", "a command run before each new version takes traffic, such as a migration", textFlag},
	{"health", "health_path", "the path the health check asks, such as /healthz", textFlag},
	{"health-check", "health_check", "how an instance is checked: http, tcp or none", textFlag},
	{"health-start", "health_start_seconds", "how long a new instance may take to answer, in seconds", numberFlag},
	{"health-timeout", "health_timeout_seconds", "how long one check waits, in seconds", numberFlag},
	{"cpu-request", "cpu_request_m", "the CPU to reserve, in millicores", numberFlag},
	{"cpu-limit", "cpu_limit_m", "the CPU it may use, in millicores; 0 for no limit", numberFlag},
	{"memory-request", "mem_request_mb", "the memory to reserve, in MB", numberFlag},
	{"memory-limit", "mem_limit_mb", "the memory it may use, in MB; 0 for no limit", numberFlag},
	{"run-as-user", "run_as_user", "the uid an image somebody else built runs as; 0 leaves it to the image", numberFlag},
	{"internal", "internal", "reachable only from the environment's other apps; --internal=false to give it its address back", switchFlag},
}

// appUpdated is what saving an app's settings answers: the app, and when each
// setting that changed takes effect.
type appUpdated struct {
	store.App
	RolledOut  []string `json:"rolled_out"`
	NextDeploy []string `json:"next_deploy"`
}

// cmdAppsSet changes an app's settings: `skifity apps set --port 3000`.
func cmdAppsSet(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("apps set", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	values := map[string]func() any{}
	for _, f := range appSettingFlags {
		switch f.kind {
		case textFlag:
			v := flags.String(f.flag, "", f.usage)
			values[f.flag] = func() any { return strings.TrimSpace(*v) }
		case numberFlag:
			v := flags.Int(f.flag, 0, f.usage)
			values[f.flag] = func() any { return *v }
		case switchFlag:
			v := flags.Bool(f.flag, false, f.usage)
			values[f.flag] = func() any { return *v }
		}
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return errdoc.BadRequest(fmt.Sprintf("%q is not a setting. Settings are flags, such as --port 3000; `%s apps set --help` lists them.",
			flags.Arg(0), version.Binary))
	}
	body := map[string]any{}
	flags.Visit(func(given *flag.Flag) {
		for _, f := range appSettingFlags {
			if f.flag == given.Name {
				body[f.setting] = values[f.flag]()
			}
		}
	})
	if len(body) == 0 {
		return errdoc.BadRequest(fmt.Sprintf("Say what to change, such as --port 3000 or --build 'npm run build'. `%s apps set --help` lists every setting.",
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
	var updated appUpdated
	if err := client.Do(ctx, "PATCH", "/api/apps/"+app, body, &updated); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, updated)
	}
	fmt.Fprintf(out, "Saved %s.\n", updated.Name)
	if len(updated.RolledOut) > 0 {
		fmt.Fprintf(out, "Rolled out now, no rebuild: %s\n", strings.Join(updated.RolledOut, ", "))
	}
	if len(updated.NextDeploy) > 0 {
		fmt.Fprintf(out, "Waiting for the next deploy: %s\nDeploy with `%s deploy` when you are ready.\n",
			strings.Join(updated.NextDeploy, ", "), version.Binary)
	}
	return nil
}

// cmdAppsRm deletes an app: `skifity apps rm web`. Its disks go with it and
// nothing brings them back, so it asks for the name to be typed, or for --yes
// where there is nobody to ask.
func cmdAppsRm(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("apps rm", flag.ContinueOnError)
	flags.SetOutput(out)
	envID := flags.String("env", "", "the environment id")
	yes := flags.Bool("yes", false, "delete it without asking; for a script")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errdoc.BadRequest(fmt.Sprintf("Name the app to delete, for example `%s apps rm web`.", version.Binary))
	}
	named := positional[0]

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := findApp(ctx, client, cfg, *envID, named)
	if err != nil {
		return err
	}

	if !*yes {
		if !isInteractive() || *asJSON {
			return errdoc.New("cli.delete_unconfirmed", "Deleting an app has to be confirmed").
				WithCause("There is nobody here to type %s's name, and --yes was not given.", app.Name).
				WithImpact("Nothing was deleted.").
				WithFix("Run it again with --yes, once you are sure: the app's disks go with it, and nothing brings them back.")
		}
		fmt.Fprintf(out, "Deleting %s removes it from the cluster with its disks and their data. Nothing brings them back.\n", app.Name)
		// The whole line: a name can have spaces in it, and prompt reads
		// one word.
		fmt.Fprintf(out, "Type %s to delete it: ", app.Slug)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if typed := strings.TrimSpace(line); typed != app.Slug && typed != app.Name {
			fmt.Fprintln(out, "That is not its name. Nothing was deleted.")
			return nil
		}
	}
	if err := client.Do(ctx, "DELETE", "/api/apps/"+app.ID, nil, nil); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"deleted": true, "app": app.ID, "name": app.Name})
	}
	fmt.Fprintf(out, "Deleted %s.\n", app.Name)
	return nil
}

// findApp is an app in the environment by its name, slug or id. A name is
// what somebody types to delete an app, and an id says nothing about which
// one is about to go.
func findApp(ctx context.Context, client *Client, cfg Config, envID, named string) (store.App, error) {
	environment := envID
	if environment == "" {
		var err error
		if environment, err = resolveEnvironment(ctx, client, cfg); err != nil {
			return store.App{}, err
		}
	}
	var response struct {
		Items []store.App `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/apps", nil, &response); err != nil {
		return store.App{}, err
	}
	names := make([]string, 0, len(response.Items))
	for _, app := range response.Items {
		if app.ID == named || app.Slug == named || app.Name == named {
			return app, nil
		}
		names = append(names, app.Name)
	}
	return store.App{}, errdoc.New("cli.app_not_found", "There is no app by that name here").
		WithCause("The environment has no app called %s. It has: %s.", named, orDefault(strings.Join(names, ", "), "none")).
		WithImpact("The command did not run.").
		WithFix("Give one of those names, or --env for another environment.")
}

// cmdRestart replaces an app's instances one at a time, on the version and
// settings it has: `skifity restart`.
func cmdRestart(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("restart", flag.ContinueOnError)
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
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/restart", nil, nil); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, map[string]any{"restarting": true, "app": app})
	}
	fmt.Fprintf(out, "Restarting the instances one at a time. Nothing is rebuilt. Watch with `%s status`.\n", version.Binary)
	return nil
}

// cancelDeployment stops a deployment that has not finished: the one named,
// by id or number, or the one under way.
func cancelDeployment(ctx context.Context, client *Client, app string, named []string, out io.Writer, asJSON bool) error {
	if len(named) > 1 {
		return errdoc.BadRequest("Name one deployment to cancel, by its number or id.")
	}
	var history struct {
		Items []store.Deployment `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/apps/"+app+"/deployments"+Query("limit", "50"), nil, &history); err != nil {
		return err
	}
	var target *store.Deployment
	for i, deployment := range history.Items {
		switch {
		case len(named) == 0:
			if !deployment.Status.Terminal() && target == nil {
				target = &history.Items[i]
			}
		case deployment.ID == named[0] || "#"+strconv.Itoa(deployment.Number) == named[0] ||
			strconv.Itoa(deployment.Number) == named[0]:
			target = &history.Items[i]
		}
	}
	switch {
	case target == nil && len(named) == 0:
		return errdoc.BadRequest("Nothing is being deployed, so there is nothing to cancel.")
	case target == nil:
		return errdoc.BadRequest(fmt.Sprintf("This app has no deployment %s among its last 50.", named[0]))
	}
	if err := client.Do(ctx, "POST", "/api/apps/"+app+"/deployments/"+target.ID+"/cancel", nil, nil); err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, map[string]any{"cancelled": true, "deployment": target.ID, "number": target.Number})
	}
	fmt.Fprintf(out, "Deployment #%d is cancelled. The version that was running keeps running.\n", target.Number)
	return nil
}

// variableEffect is what changing variables did to the app, as the panel
// answers it: a rebuild of the version it runs, or why there was none.
type variableEffect struct {
	RequiresRebuild bool              `json:"requires_rebuild"`
	Deployment      *store.Deployment `json:"deployment,omitempty"`
	NotRebuilt      string            `json:"not_rebuilt,omitempty"`
}

// note says it in a sentence.
func (e variableEffect) note() string {
	switch {
	case e.RequiresRebuild && e.Deployment != nil:
		return fmt.Sprintf("The build reads it, so the version the app runs is being rebuilt with it: deployment #%d. "+
			"Follow it with `%s status`.", e.Deployment.Number, version.Binary)
	case e.NotRebuilt == "locked":
		return "The build reads it, and deploys are locked, so it takes effect at the first deploy after they are unlocked."
	case e.NotRebuilt == "not_deployed":
		return "The build reads it. The app has not been deployed yet, and its first deploy builds with it."
	case e.NotRebuilt != "":
		return fmt.Sprintf("The build reads it, and a rebuild could not be started, so it takes effect at the next `%s deploy`.", version.Binary)
	}
	return "Rolled out, once. No rebuild was needed."
}

// interrupted is Ctrl-C while a deployment was being followed. What stopped
// is the following; the exit status says so the way a shell expects.
type interrupted struct{}

func (interrupted) Error() string { return "interrupted" }

// followStarted follows a deployment that was just started until it ends.
// Ctrl-C ends the following and not the deployment, which goes on in the
// panel, and says so — with how to stop it, which is what somebody pressing
// Ctrl-C on a deploy usually wanted.
func followStarted(ctx context.Context, client *Client, app string, deployment store.Deployment, out io.Writer, asJSON bool) error {
	final, err := followDeployment(ctx, client, app, deployment.ID, out, asJSON)
	if ctx.Err() != nil && !final.Status.Terminal() {
		if !asJSON {
			fmt.Fprintf(out, "\nStopped following. Deployment #%d is still running in the panel; "+
				"stop it with `%s deploy --cancel %d`.\n", deployment.Number, version.Binary, deployment.Number)
		}
		return interrupted{}
	}
	return printFollowed(out, asJSON, final, err)
}

// rollbackPlan is what GET .../rollback/{id}/plan answers.
type rollbackPlan struct {
	Target struct {
		Number    int    `json:"number"`
		CommitSHA string `json:"commit_sha"`
	} `json:"target"`
	Current *struct {
		Number    int    `json:"number"`
		CommitSHA string `json:"commit_sha"`
	} `json:"current"`
	Changes     []store.SpecChange `json:"changes"`
	CanRollback bool               `json:"can_rollback"`
	Unchanged   []string           `json:"unchanged"`
}

// printRollbackPlan says what a rollback would change before it is done: the
// settings it puts back, from what to what, and what it leaves as it is.
func printRollbackPlan(out io.Writer, plan rollbackPlan) {
	label := func(number int, commit string) string {
		if commit == "" {
			return "#" + strconv.Itoa(number)
		}
		return fmt.Sprintf("#%d (%s)", number, commit[:min(7, len(commit))])
	}
	from := "nothing running"
	if plan.Current != nil {
		from = label(plan.Current.Number, plan.Current.CommitSHA)
	}
	fmt.Fprintf(out, "Going back from %s to %s.\n", from, label(plan.Target.Number, plan.Target.CommitSHA))
	if len(plan.Changes) == 0 {
		fmt.Fprintln(out, "No setting changes: only the code does.")
	} else {
		fmt.Fprintln(out, "It puts these settings back:")
		width := 0
		for _, change := range plan.Changes {
			width = max(width, len(change.Field))
		}
		for _, change := range plan.Changes {
			fmt.Fprintf(out, "  %-*s  %s -> %s\n", width, change.Field, orDefault(change.From, "(none)"), orDefault(change.To, "(none)"))
		}
	}
	if len(plan.Unchanged) > 0 {
		fmt.Fprintf(out, "It leaves %s as they are now.\n", strings.Join(plan.Unchanged, ", "))
	}
}
