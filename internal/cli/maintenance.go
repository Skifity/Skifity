package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// defaultMaintenanceMessage is what visitors read when `maintenance on` is
// given no message of its own. The command line is English, and so is this;
// the panel's form offers it in the person's own language.
const defaultMaintenanceMessage = "We are doing some maintenance and will be back shortly."

type maintenanceAnswer struct {
	Active bool `json:"active"`
	store.Maintenance
	Hostnames   []string `json:"hostnames"`
	YourAddress string   `json:"your_address,omitempty"`
}

// cmdMaintenance shows visitors a page instead of the app, and stops.
//
//	skifity maintenance                         whether it is on, and what it says
//	skifity maintenance on "Back at 14:00"      start it, or change the message
//	skifity maintenance on --allow 203.0.113.7  and let that address through
//	skifity maintenance off                     end it
func cmdMaintenance(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	allow := flags.String("allow", "", "addresses or ranges that still reach the app, comma-separated")
	allowMe := flags.Bool("allow-me", false, "let the address this command comes from through")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	action := "status"
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
	path := "/api/apps/" + app + "/maintenance"

	var answer maintenanceAnswer
	switch action {
	case "status":
		if err := client.Do(ctx, "GET", path, nil, &answer); err != nil {
			return err
		}
	case "on":
		message := strings.TrimSpace(strings.Join(positional, " "))
		if message == "" {
			message = defaultMaintenanceMessage
		}
		var addresses []string
		for _, entry := range strings.Split(*allow, ",") {
			if entry = strings.TrimSpace(entry); entry != "" {
				addresses = append(addresses, entry)
			}
		}
		if *allowMe {
			var current maintenanceAnswer
			if err := client.Do(ctx, "GET", path, nil, &current); err != nil {
				return err
			}
			if current.YourAddress == "" {
				return errdoc.BadRequest("The panel sees this command coming from a private address, which is not the one visitors' requests come from. Pass --allow with your public address instead.")
			}
			addresses = append(addresses, current.YourAddress)
		}
		body := map[string]any{"message": message, "allow": addresses}
		if err := client.Do(ctx, "PUT", path, body, &answer); err != nil {
			return err
		}
	case "off":
		if err := client.Do(ctx, "DELETE", path, nil, nil); err != nil {
			return err
		}
	default:
		return errdoc.BadRequest(fmt.Sprintf("Say on, off, or nothing to see where it stands: `%s maintenance on \"Back at 14:00\"`.", version.Binary))
	}

	if *asJSON {
		if action == "off" {
			return writeJSON(out, map[string]bool{"active": false})
		}
		return writeJSON(out, answer)
	}
	switch {
	case action == "off":
		fmt.Fprintln(out, "Maintenance is over: visitors reach the app again.")
	case !answer.Active:
		fmt.Fprintf(out, "Not in maintenance. Start it with `%s maintenance on \"message\"`.\n", version.Binary)
	default:
		fmt.Fprintf(out, "In maintenance since %s, started by %s.\n", answer.StartedAt, answer.StartedBy)
		fmt.Fprintf(out, "Visitors to %s see: %s\n", strings.Join(answer.Hostnames, ", "), answer.Message)
		if len(answer.Allow) > 0 {
			fmt.Fprintf(out, "Still reaching the app: %s\n", strings.Join(answer.Allow, ", "))
		}
		fmt.Fprintf(out, "End it with `%s maintenance off`.\n", version.Binary)
	}
	return nil
}
