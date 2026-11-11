package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// attentionItem is one thing that needs attention, as the panel answers it.
type attentionItem struct {
	Kind            string         `json:"kind"`
	Severity        string         `json:"severity"`
	Code            string         `json:"code"`
	Params          map[string]any `json:"params"`
	Message         string         `json:"message"`
	TargetType      string         `json:"target_type"`
	TargetID        string         `json:"target_id"`
	TargetLabel     string         `json:"target_label"`
	ProjectID       string         `json:"project_id,omitempty"`
	ProjectName     string         `json:"project_name,omitempty"`
	EnvironmentID   string         `json:"environment_id,omitempty"`
	EnvironmentName string         `json:"environment_name,omitempty"`
	Since           time.Time      `json:"since,omitzero"`
	Path            string         `json:"path"`
}

// attentionNeeded is the answer having an error in it. Run turns it into exit
// status 1 and prints nothing more: the list already said what is wrong.
type attentionNeeded struct{ errors int }

func (e attentionNeeded) Error() string {
	return fmt.Sprintf("%d %s need attention", e.errors, pluralWord(e.errors, "thing", "things"))
}

// cmdAttention prints what needs attention across the team, the list the
// overview leads with: `skifity status --all`, or `skifity attention`.
//
// It exits 1 when anything on it is an error — an app down, a deploy or a
// backup that failed — so a cron job or a CI step can ask "is anything
// broken?" and be answered by the exit status alone. Warnings print and exit 0.
func cmdAttention(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("attention", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return showAttention(ctx, *asJSON, out)
}

// showAttention is the command itself, shared by `attention` and
// `status --all`.
func showAttention(ctx context.Context, asJSON bool, out io.Writer) error {
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
		Items []attentionItem `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/teams/"+teamID+"/attention", nil, &response); err != nil {
		return err
	}
	if response.Items == nil {
		response.Items = []attentionItem{}
	}

	errors := 0
	for _, item := range response.Items {
		if item.Severity == "error" {
			errors++
		}
	}

	if asJSON {
		if err := writeJSON(out, response.Items); err != nil {
			return err
		}
	} else {
		printAttention(out, response.Items, errors, strings.TrimSuffix(cfg.PanelURL, "/"))
	}
	if errors > 0 {
		return attentionNeeded{errors: errors}
	}
	return nil
}

// printAttention lists the items, each with where it is, since when, and the
// page in the panel to deal with it on.
func printAttention(out io.Writer, items []attentionItem, errors int, panelURL string) {
	if len(items) == 0 {
		fmt.Fprintln(out, "Nothing needs attention.")
		return
	}
	warnings := len(items) - errors
	fmt.Fprintf(out, "%d %s, %d %s\n\n", errors, pluralWord(errors, "error", "errors"),
		warnings, pluralWord(warnings, "warning", "warnings"))
	for _, item := range items {
		label := "WARNING"
		if item.Severity == "error" {
			label = "ERROR"
		}
		fmt.Fprintf(out, "%-8s %s\n", label, item.Message)
		var where []string
		if item.ProjectName != "" {
			where = append(where, item.ProjectName+" / "+item.EnvironmentName)
		}
		if !item.Since.IsZero() {
			where = append(where, attentionWhen(item.Since))
		}
		if len(where) > 0 {
			fmt.Fprintf(out, "%-8s %s\n", "", strings.Join(where, " · "))
		}
		if item.Path != "" {
			fmt.Fprintf(out, "%-8s %s\n", "", panelURL+item.Path)
		}
		fmt.Fprintln(out)
	}
}

// attentionWhen is humanTime, and also the future, which is where a
// certificate's date is.
func attentionWhen(t time.Time) string {
	left := time.Until(t)
	switch {
	case left <= 0:
		return humanTime(t)
	case left < time.Hour:
		return fmt.Sprintf("in %dm", int(left.Minutes())+1)
	case left < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(left.Hours())+1)
	default:
		return "on " + t.Local().Format("2 Jan 15:04")
	}
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
