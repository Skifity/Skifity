package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// objectEvent is one thing Kubernetes said about one of an app's objects, as
// the panel answers it.
type objectEvent struct {
	Type            string    `json:"type"`
	Reason          string    `json:"reason"`
	Kind            string    `json:"kind"`
	Name            string    `json:"name"`
	Message         string    `json:"message"`
	Count           int       `json:"count"`
	FirstSeen       time.Time `json:"first_seen"`
	LastSeen        time.Time `json:"last_seen"`
	Explanation     string    `json:"explanation,omitempty"`
	ExplanationCode string    `json:"explanation_code,omitempty"`
}

// cmdEvents prints what Kubernetes said about an app's objects, newest first:
// the scheduler, the kubelet and the controllers, with the common warnings
// explained.
//
//	skifity events                 this directory's app
//	skifity events --warnings      only what went wrong
//	skifity events --db DB_ID      a managed database's instead
func cmdEvents(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	databaseID := flags.String("db", "", "a database id, to read its events instead of an app's")
	warnings := flags.Bool("warnings", false, "only the warnings")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *appID != "" && *databaseID != "" {
		return errdoc.BadRequest("Give --app or --db, not both.")
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	path := ""
	if *databaseID != "" {
		path = "/api/databases/" + *databaseID + "/events"
	} else {
		app, err := resolveApp(ctx, client, cfg, *appID)
		if err != nil {
			return err
		}
		path = "/api/apps/" + app + "/events"
	}
	if *warnings {
		path += Query("type", "Warning")
	}

	var response struct {
		Items []objectEvent `json:"items"`
	}
	if err := client.Do(ctx, "GET", path, nil, &response); err != nil {
		return err
	}
	if response.Items == nil {
		response.Items = []objectEvent{}
	}
	if *asJSON {
		return writeJSON(out, response.Items)
	}
	printEvents(out, response.Items, *warnings)
	return nil
}

func printEvents(out io.Writer, events []objectEvent, warningsOnly bool) {
	if len(events) == 0 {
		if warningsOnly {
			fmt.Fprintln(out, "No warnings. Kubernetes keeps events for about an hour.")
		} else {
			fmt.Fprintln(out, "Nothing yet. Kubernetes keeps events for about an hour.")
		}
		return
	}
	for _, event := range events {
		count := ""
		if event.Count > 1 {
			count = fmt.Sprintf("  x%d", event.Count)
		}
		fmt.Fprintf(out, "%-10s %-7s %s  %s/%s%s\n",
			humanTime(event.LastSeen), event.Type, event.Reason, event.Kind, event.Name, count)
		// What it means first, when the panel knows, and Kubernetes' own
		// words under it: the explanation is the fix, and the message is the
		// detail somebody pastes into a search.
		if event.Explanation != "" {
			fmt.Fprintf(out, "           %s\n", event.Explanation)
		}
		if message := strings.TrimSpace(event.Message); message != "" && message != event.Explanation {
			fmt.Fprintf(out, "           %s\n", message)
		}
	}
}

// driftItem is one difference between an app's objects and what the panel
// applies.
type driftItem struct {
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Path      string    `json:"path,omitempty"`
	Change    string    `json:"change"`
	Panel     string    `json:"panel,omitempty"`
	Live      string    `json:"live,omitempty"`
	Hidden    bool      `json:"hidden,omitempty"`
	ChangedBy string    `json:"changed_by,omitempty"`
	ChangedAt time.Time `json:"changed_at,omitzero"`
}

type driftReport struct {
	Status     string      `json:"status"`
	Items      []driftItem `json:"items"`
	CheckedAt  time.Time   `json:"checked_at"`
	Since      time.Time   `json:"since,omitzero"`
	AutoRepair bool        `json:"auto_repair"`
}

// cmdDrift says whether anybody changed the app's objects outside the panel —
// with kubectl, say — and puts them back when asked.
//
//	skifity drift             what is different
//	skifity drift --repair    put it back as the panel applies it
func cmdDrift(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("drift", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	repair := flags.Bool("repair", false, "put the app's objects back as the panel applies them")
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

	var report driftReport
	if *repair {
		// Long: it waits for a rollout when the change was to the running
		// version, as the panel's own button does.
		err = client.DoLong(ctx, "POST", "/api/apps/"+app+"/drift/repair", nil, &report)
	} else {
		err = client.Do(ctx, "GET", "/api/apps/"+app+"/drift", nil, &report)
	}
	if err != nil {
		return err
	}
	if report.Items == nil {
		report.Items = []driftItem{}
	}
	if *asJSON {
		return writeJSON(out, report)
	}
	printDrift(out, report, *repair)
	return nil
}

func printDrift(out io.Writer, report driftReport, repaired bool) {
	switch report.Status {
	case "in_sync":
		if repaired {
			fmt.Fprintf(out, "Put back. The app's objects are as %s applies them.\n", version.Name)
		} else {
			fmt.Fprintf(out, "Nothing was changed outside %s.\n", version.Name)
		}
		return
	case "not_deployed":
		fmt.Fprintln(out, "Nothing is deployed yet, so there is nothing to compare.")
		return
	case "applying":
		fmt.Fprintf(out, "%s is changing this app right now; ask again once the deploy or the change has finished.\n", version.Name)
		return
	}
	fmt.Fprintf(out, "Changed outside %s:\n\n", version.Name)
	for _, item := range report.Items {
		object := item.Kind + "/" + item.Name
		switch {
		case item.Change == "deleted":
			fmt.Fprintf(out, "  %s was deleted\n", object)
		case item.Hidden:
			fmt.Fprintf(out, "  %s %s: a secret value differs\n", object, item.Path)
		case item.Change == "removed":
			fmt.Fprintf(out, "  %s %s was removed (the panel applies %s)\n", object, item.Path, orDefault(item.Panel, "a value"))
		default:
			fmt.Fprintf(out, "  %s %s is %s, the panel applies %s\n", object, item.Path, orDefault(item.Live, "empty"), item.Panel)
		}
		if item.ChangedBy != "" {
			fmt.Fprintf(out, "      by %s, %s\n", item.ChangedBy, humanTime(item.ChangedAt))
		}
	}
	if repaired {
		fmt.Fprintln(out, "\nPutting it back did not change these: something is changing them again.")
		return
	}
	fmt.Fprintf(out, "\nPut it back with `%s drift --repair`.\n", version.Binary)
}
