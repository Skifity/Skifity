package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// processAnswer is one process as the panel lists it.
type processAnswer struct {
	store.AppProcess
	Ready *int   `json:"ready,omitempty"`
	Phase string `json:"phase,omitempty"`
}

// cmdProcesses lists, adds, changes and removes an app's other processes.
//
//	skifity processes                                   what runs beside the app
//	skifity processes set worker -- celery -A shop worker
//	skifity processes set worker --instances 3          more of one, same command
//	skifity processes set worker --instances 0          stopped, command kept
//	skifity processes rm worker
//
// The command goes after `--`, unsplit, as with `run`: flags a worker takes
// are its own, not this command's.
func cmdProcesses(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("processes", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	instances := flags.Int("instances", -1, "how many instances of the process to run; 0 stops it")
	asJSON := flags.Bool("json", false, "print the result as JSON")

	var command string
	if i := slices.Index(args, "--"); i >= 0 {
		command = strings.TrimSpace(strings.Join(args[i+1:], " "))
		args = args[:i]
	}
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	action := "list"
	if len(positional) > 0 {
		action, positional = positional[0], positional[1:]
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
	path := "/api/apps/" + app + "/processes"

	var listed struct {
		Items []processAnswer `json:"items"`
	}
	switch action {
	case "list", "ls":
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed.Items)
		}
		if len(listed.Items) == 0 {
			fmt.Fprintf(out, "Only the app itself runs. Add a worker with `%s processes set worker -- <command>`.\n", version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tINSTANCES\tREADY\tCOMMAND")
		for _, process := range listed.Items {
			ready := "-"
			if process.Ready != nil {
				ready = strconv.Itoa(*process.Ready)
			}
			fmt.Fprintf(table, "%s\t%d\t%s\t%s\n", process.Name, process.Instances, ready, process.Command)
		}
		return table.Flush()

	case "set":
		if len(positional) == 0 {
			return errdoc.BadRequest(fmt.Sprintf(
				"Name the process: `%s processes set worker -- celery -A shop worker`.", version.Binary))
		}
		name := positional[0]
		if command == "" && len(positional) > 1 {
			command = strings.Join(positional[1:], " ")
		}
		body := map[string]any{"command": command}
		if *instances >= 0 {
			body["instances"] = *instances
		}
		// Changing only the instances keeps the command it has.
		if command == "" {
			if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
				return err
			}
			i := slices.IndexFunc(listed.Items, func(p processAnswer) bool { return p.Name == name })
			if i < 0 {
				return errdoc.BadRequest(fmt.Sprintf(
					"There is no process called %s yet, so it needs a command: `%s processes set %s -- <command>`.",
					name, version.Binary, name))
			}
			body["command"] = listed.Items[i].Command
			if *instances < 0 {
				body["instances"] = listed.Items[i].Instances
			}
		}
		var saved store.AppProcess
		if err := client.Do(ctx, "PUT", path+"/"+url.PathEscape(name), body, &saved); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, saved)
		}
		if saved.Instances == 0 {
			fmt.Fprintf(out, "%s is stopped; its command is kept for when it starts again.\n", saved.Name)
			return nil
		}
		fmt.Fprintf(out, "%s runs %s, %s, on the app's current version.\n",
			saved.Name, saved.Command, plural(saved.Instances, "instance"))
		return nil

	case "rm", "remove":
		if len(positional) == 0 {
			return errdoc.BadRequest(fmt.Sprintf("Name the process: `%s processes rm worker`.", version.Binary))
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(positional[0]), nil, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]string{"removed": positional[0]})
		}
		fmt.Fprintf(out, "%s is removed, and its instances are stopped.\n", positional[0])
		return nil

	default:
		return errdoc.BadRequest(fmt.Sprintf(
			"Say set or rm, or nothing to list them: `%s processes set worker -- <command>`.", version.Binary))
	}
}
