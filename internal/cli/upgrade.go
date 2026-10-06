package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"skifity/internal/errdoc"
)

// upgradeCheck is what POST /api/upgrade/check answers.
type upgradeCheck struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseURL     string `json:"release_url"`
	State          string `json:"state"`
	Repository     string `json:"repository"`
}

// upgradeStarted is what POST /api/upgrade answers, before the panel stops.
type upgradeStarted struct {
	PreviousImage string   `json:"previous_image"`
	Snapshot      string   `json:"snapshot"`
	OffSiteCopy   string   `json:"off_site_copy"`
	Rollback      []string `json:"rollback"`
}

// cmdUpgrade asks which release of the panel is the newest, and upgrades the
// panel to one. A panel administrator's.
//
//	skifity upgrade               ask which release is the newest; changes nothing
//	skifity upgrade --latest      upgrade to the newest release, when it is newer
//	skifity upgrade --to v0.2.0   upgrade to that release
//
// The panel never asks about new releases on its own. Running this is what
// makes it ask, once, and the answer is printed rather than remembered.
func cmdUpgrade(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	flags.SetOutput(out)
	latest := flags.Bool("latest", false, "upgrade to the newest release, when there is a newer one")
	to := flags.String("to", "", "upgrade to this release, such as v0.2.0")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *latest && *to != "" {
		return errdoc.BadRequest("Give --latest or --to, not both.")
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)

	// Naming the release asks nobody anything.
	target := strings.TrimSpace(*to)
	if target == "" {
		var check upgradeCheck
		if err := client.Do(ctx, "POST", "/api/upgrade/check", nil, &check); err != nil {
			return err
		}
		if !*latest {
			if *asJSON {
				return writeJSON(out, check)
			}
			printUpgradeCheck(out, check)
			return nil
		}
		if check.State != "available" {
			if *asJSON {
				return writeJSON(out, check)
			}
			printUpgradeCheck(out, check)
			fmt.Fprintln(out, "Nothing was changed.")
			return nil
		}
		target = check.LatestVersion
		if !*asJSON {
			fmt.Fprintf(out, "Upgrading %s to %s.\n", check.CurrentVersion, target)
		}
	}

	var started upgradeStarted
	if err := client.Do(ctx, "POST", "/api/upgrade", map[string]string{"version": target}, &started); err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, started)
	}
	fmt.Fprintf(out, "\nThe upgrade to %s has started, and the panel is restarting. Your apps keep running.\n", target)
	fmt.Fprintf(out, "The database was copied to %s first.\n", started.Snapshot)
	if started.OffSiteCopy != "" {
		fmt.Fprintf(out, "And to %s.\n", started.OffSiteCopy)
	}
	fmt.Fprintf(out, "\nIf the new version does not come up, run these on the server, in order:\n\n")
	for _, command := range started.Rollback {
		fmt.Fprintf(out, "  %s\n", command)
	}
	fmt.Fprintln(out)
	return nil
}

// printUpgradeCheck says what a check found, in a sentence a person can act on.
func printUpgradeCheck(out io.Writer, check upgradeCheck) {
	fmt.Fprintf(out, "\nRunning  %s\n", check.CurrentVersion)
	if check.LatestVersion != "" {
		fmt.Fprintf(out, "Newest   %s  %s\n", check.LatestVersion, check.ReleaseURL)
	}
	fmt.Fprintln(out)
	switch check.State {
	case "available":
		fmt.Fprintln(out, "A newer release is available. `skifity upgrade --latest` moves to it:")
		fmt.Fprintln(out, "the database is copied first, and the way back is printed.")
	case "up_to_date":
		fmt.Fprintln(out, "You are running the newest release.")
	case "ahead":
		fmt.Fprintln(out, "This is newer than the newest release.")
	case "development":
		fmt.Fprintln(out, "This is a build made after the newest release, not a release.")
	case "no_release":
		fmt.Fprintf(out, "No release has been published at %s yet.\n", check.Repository)
	default:
		fmt.Fprintln(out, "This version is not a release number, so it cannot be compared with the newest release.")
	}
	fmt.Fprintln(out)
}
