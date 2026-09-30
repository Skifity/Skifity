package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"skifity/internal/blueprint"
	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// cmdBlueprint is `plan`, which says what skifity.yaml would change and
// changes nothing, and `apply`, which changes it.
func cmdBlueprint(ctx context.Context, command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	path := flags.String("file", "", "the blueprint to read; by default skifity.yaml here or in a parent directory")
	envID := flags.String("env", "", "the environment id")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	file, where, err := readBlueprint(*path)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	environment := *envID
	if environment == "" {
		if environment, err = resolveEnvironment(ctx, client, cfg); err != nil {
			return err
		}
	}
	state, err := fetchState(ctx, client, environment, file)
	if err != nil {
		return err
	}
	steps, err := blueprint.Plan(file, state)
	if err != nil {
		return err
	}
	create, change, note := blueprint.Counts(steps)

	if command == "plan" {
		if *asJSON {
			return writeJSON(out, map[string]any{"file": where, "steps": steps, "create": create, "change": change, "note": note})
		}
		fmt.Fprintf(out, "Reading %s.\n\n", where)
		for _, step := range steps {
			printStep(out, step)
		}
		if create+change == 0 {
			fmt.Fprintln(out, "\nNothing to change: the environment is what the file says.")
			return nil
		}
		fmt.Fprintf(out, "\n%d to add, %d to change. `%s apply` makes these changes.\n", create, change, version.Binary)
		return nil
	}

	applied, err := applySteps(ctx, client, state, steps, *asJSON, out)
	// What was done, not what was planned: after a failure they differ.
	create, change, _ = blueprint.Counts(applied)
	if *asJSON {
		answer := map[string]any{"file": where, "applied": applied, "create": create, "change": change, "note": note}
		if err != nil {
			answer["error"] = errdoc.From(err)
		}
		if writeErr := writeJSON(out, answer); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		fmt.Fprintf(out, "\nStopped after %d of the changes. What was done stays done; `%s plan` shows what is left.\n",
			len(applied), version.Binary)
		return err
	}
	if len(applied) == 0 {
		fmt.Fprintln(out, "Nothing to change: the environment is what the file says.")
		return nil
	}
	fmt.Fprintf(out, "\nDone: %d added, %d changed.\n", create, change)
	return nil
}

// readBlueprint finds and parses the file.
func readBlueprint(path string) (blueprint.File, string, error) {
	if path == "" {
		dir, err := os.Getwd()
		if err != nil {
			return blueprint.File{}, "", err
		}
		for {
			candidate := filepath.Join(dir, blueprint.FileName)
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return blueprint.File{}, "", errdoc.New("cli.no_blueprint", "There is no skifity.yaml here").
					WithCause("Neither this directory nor any above it has one.").
					WithImpact("Nothing was read or changed.").
					WithFix("Write one — the documentation's example is a start — or pass --file.")
			}
			dir = parent
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return blueprint.File{}, "", fmt.Errorf("read %s: %w", path, err)
	}
	file, err := blueprint.Parse(data)
	return file, path, err
}

// fetchState asks the panel what the environment has now. The apps the file
// does not name are listed and not looked into: all a plan says about them is
// that they are there.
func fetchState(ctx context.Context, client *Client, environment string, file blueprint.File) (blueprint.State, error) {
	state := blueprint.State{EnvironmentID: environment}
	var apps struct {
		Items []store.App `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/apps", nil, &apps); err != nil {
		return state, err
	}
	var databases struct {
		Items []store.Database `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/databases", nil, &databases); err != nil {
		return state, err
	}
	state.Databases = databases.Items

	// The team's Git connections, when the file names one for a repository.
	if slices.ContainsFunc(slices.Collect(maps.Values(file.Apps)), func(app blueprint.App) bool { return app.Git != "" }) {
		sources, err := gitSources(ctx, client, environment)
		if err != nil {
			return state, err
		}
		state.GitSources = sources
	}

	// Which app reads which database as what.
	links := map[string]map[string]string{}
	for _, database := range databases.Items {
		var detail struct {
			Links []store.DatabaseLink `json:"links"`
		}
		if err := client.Do(ctx, "GET", "/api/databases/"+database.ID, nil, &detail); err != nil {
			return state, err
		}
		for _, link := range detail.Links {
			if links[link.AppID] == nil {
				links[link.AppID] = map[string]string{}
			}
			links[link.AppID][link.VarName] = database.ID
		}
	}

	for _, app := range apps.Items {
		one := blueprint.AppState{App: app, Links: links[app.ID]}
		_, byName := file.Apps[app.Name]
		_, bySlug := file.Apps[app.Slug]
		if byName || bySlug {
			base := "/api/apps/" + app.ID
			for _, part := range []struct {
				path string
				into any
			}{
				{base + "/variables", &listOf[store.Variable]{}},
				{base + "/processes", &listOf[store.AppProcess]{}},
				{base + "/domains", &listOf[store.Domain]{}},
				{base + "/jobs", &listOf[store.AppJob]{}},
				{base + "/deployments?limit=1", &listOf[store.Deployment]{}},
			} {
				if err := client.Do(ctx, "GET", part.path, nil, part.into); err != nil {
					return state, err
				}
				switch list := part.into.(type) {
				case *listOf[store.Variable]:
					one.Variables = list.Items
				case *listOf[store.AppProcess]:
					one.Processes = list.Items
				case *listOf[store.Domain]:
					one.Domains = list.Items
				case *listOf[store.AppJob]:
					one.Jobs = list.Items
				case *listOf[store.Deployment]:
					one.Deployed = len(list.Items) > 0
				}
			}
		}
		state.Apps = append(state.Apps, one)
	}
	return state, nil
}

type listOf[T any] struct {
	Items []T `json:"items"`
}

// gitSources are the Git connections of the team an environment is in.
func gitSources(ctx context.Context, client *Client, environment string) ([]store.GitSource, error) {
	var env store.Environment
	if err := client.Do(ctx, "GET", "/api/environments/"+environment, nil, &env); err != nil {
		return nil, err
	}
	var project store.Project
	if err := client.Do(ctx, "GET", "/api/projects/"+env.ProjectID, nil, &project); err != nil {
		return nil, err
	}
	var sources listOf[store.GitSource]
	if err := client.Do(ctx, "GET", "/api/teams/"+project.TeamID+"/git-sources", nil, &sources); err != nil {
		return nil, err
	}
	return sources.Items, nil
}

// applySteps sends each step's call in order, filling in the ids of what the
// earlier ones made, and stops at the first that fails. A step with no call
// that is not a note is part of the next call — the variables of one app are
// sent together — and is done when that call is.
func applySteps(ctx context.Context, client *Client, state blueprint.State, steps []blueprint.Step, quiet bool, out io.Writer) ([]blueprint.Step, error) {
	ids := map[string]string{}
	for _, app := range state.Apps {
		ids["app:"+app.App.Name], ids["app:"+app.App.Slug] = app.App.ID, app.App.ID
	}
	for _, database := range state.Databases {
		ids["database:"+database.Name], ids["database:"+database.Slug] = database.ID, database.ID
	}

	applied := []blueprint.Step{}
	var batch []blueprint.Step
	say := func(mark string, done []blueprint.Step) {
		if quiet {
			return
		}
		for _, step := range done {
			fmt.Fprintf(out, "  %s %s\n", mark, describe(step))
		}
	}
	for _, step := range steps {
		if step.Call == nil {
			if step.Op == "note" {
				say(stepMarks["note"], []blueprint.Step{step})
			} else {
				batch = append(batch, step)
			}
			continue
		}
		done := append(batch, step)
		batch = nil
		call, err := blueprint.Resolve(*step.Call, ids)
		if err != nil {
			say("✗", done)
			return applied, err
		}
		var answer json.RawMessage
		if err := client.DoLong(ctx, call.Method, call.Path, call.Body, &answer); err != nil {
			say("✗", done)
			return applied, err
		}
		if call.Creates != "" {
			id := createdID(answer)
			if id == "" {
				return applied, errors.New("the panel made " + call.Creates + " and did not say its id")
			}
			ids[call.Creates] = id
		}
		applied = append(applied, done...)
		say("✓", done)
	}
	return applied, nil
}

// createdID reads the id out of what a create answered: a database is the
// object itself, an app is under "app".
func createdID(answer json.RawMessage) string {
	var shape struct {
		ID  string `json:"id"`
		App struct {
			ID string `json:"id"`
		} `json:"app"`
	}
	if json.Unmarshal(answer, &shape) != nil {
		return ""
	}
	if shape.ID != "" {
		return shape.ID
	}
	return shape.App.ID
}

var stepMarks = map[string]string{"create": "+", "change": "~", "note": "·"}

func printStep(out io.Writer, step blueprint.Step) {
	fmt.Fprintf(out, "  %s %s\n", stepMarks[step.Op], describe(step))
}

func describe(step blueprint.Step) string {
	text := step.Kind + " " + step.Name
	switch {
	case step.App == "" || step.Kind == "app" || step.Kind == "deploy":
	case step.Kind == "settings" || step.Kind == "scaling" || step.Kind == "source":
		// The app's own settings, scaling and source: "web: scaling". A
		// process named like its app is still "worker: process worker".
		text = step.App + ": " + step.Kind
	default:
		text = step.App + ": " + text
	}
	if step.Detail != "" {
		text += " — " + step.Detail
	}
	return text
}
