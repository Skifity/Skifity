package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// vulnerabilityReport is an app's Security tab, as the panel answers it.
type vulnerabilityReport struct {
	Enabled    bool             `json:"enabled"`
	Blocking   bool             `json:"blocking"`
	Image      string           `json:"image,omitempty"`
	Scan       *store.ImageScan `json:"scan"`
	Current    bool             `json:"current"`
	Undeployed bool             `json:"undeployed"`
	Latest     *store.ImageScan `json:"latest,omitempty"`
}

// scanPoll is how often `scan --now` asks whether its scan has finished. A
// variable, so a test need not wait for it.
var scanPoll = 3 * time.Second

// scanWait bounds how long `scan --now` waits: the scan's own limit, and the
// scans queued ahead of it.
const scanWait = 45 * time.Minute

// shownFindings is how many findings are listed without --all.
const shownFindings = 20

// cmdScan shows what the newest scan of an app's image found, or scans it.
//
//	skifity scan                   the newest report
//	skifity scan --now             scan the image the app runs now, and wait
//	skifity scan --now --wait=false
//	skifity scan --all             every finding kept, not the first twenty
func cmdScan(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	now := flags.Bool("now", false, "scan the image the app runs now, rather than show the last report")
	wait := flags.Bool("wait", true, "with --now, wait for the scan to finish")
	all := flags.Bool("all", false, "list every finding the report kept, not only the first 20")
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
	path := "/api/apps/" + app + "/vulnerabilities"

	if !*now {
		var report vulnerabilityReport
		if err := client.Do(ctx, "GET", path, nil, &report); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, report)
		}
		printReport(out, report, *all)
		return nil
	}

	var started store.ImageScan
	if err := client.Do(ctx, "POST", path+"/scan", nil, &started); err != nil {
		return err
	}
	if !*wait {
		if *asJSON {
			return writeJSON(out, started)
		}
		fmt.Fprintf(out, "Scanning %s. `%s scan` shows the report once it has finished.\n", started.Image, version.Binary)
		return nil
	}
	if !*asJSON {
		fmt.Fprintf(out, "Scanning %s…\n", started.Image)
	}
	report, err := waitForScan(ctx, client, path, started.ID)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, report)
	}
	printReport(out, report, *all)
	return nil
}

// waitForScan asks after a scan until it has a report, or has failed.
func waitForScan(ctx context.Context, client *Client, path, scanID string) (vulnerabilityReport, error) {
	ctx, cancel := context.WithTimeout(ctx, scanWait)
	defer cancel()
	for {
		var report vulnerabilityReport
		if err := client.Do(ctx, "GET", path, nil, &report); err != nil {
			return report, err
		}
		if report.Scan != nil && report.Scan.ID == scanID {
			return report, nil
		}
		if latest := report.Latest; latest != nil && latest.ID == scanID && latest.Status == store.ScanFailed {
			return report, scanProblem(*latest)
		}
		select {
		case <-ctx.Done():
			return report, errdoc.New("cli.scan_wait", "The scan has not finished yet").
				WithCause("It was still queued or running after %d minutes.", int(scanWait.Minutes())).
				WithImpact("Nothing is wrong with the app. The scan carries on in the panel.").
				WithFix("Run `%s scan` later to read its report.", version.Binary)
		case <-time.After(scanPoll):
		}
	}
}

// scanProblem is a failed scan, as the error the CLI prints for it.
func scanProblem(scan store.ImageScan) *errdoc.Problem {
	return &errdoc.Problem{
		Code:     scan.ErrorCode,
		Title:    "The image could not be scanned",
		Cause:    scan.ErrorMessage,
		Impact:   "Nothing was recorded for this scan. The app is not affected.",
		Fix:      scan.ErrorHint,
		Severity: errdoc.SeverityError,
		At:       scan.FinishedAt,
	}
}

// printReport writes a report for a person.
func printReport(out io.Writer, report vulnerabilityReport, all bool) {
	if !report.Enabled {
		fmt.Fprintln(out, "Image scanning is switched off for this panel; an administrator turns it on under Settings, Image scanning.")
	}
	if latest := report.Latest; latest != nil {
		switch latest.Status {
		case store.ScanQueued, store.ScanRunning:
			fmt.Fprintf(out, "A scan of %s is %s.\n", latest.Image, latest.Status)
		case store.ScanFailed:
			fmt.Fprintf(out, "The last scan, %s, failed: %s\n", humanTime(latest.FinishedAt), latest.ErrorMessage)
		}
	}
	scan := report.Scan
	if scan == nil {
		fmt.Fprintf(out, "This app's image has not been scanned yet. Scan it with `%s scan --now`.\n", version.Binary)
		return
	}

	fmt.Fprintf(out, "Image:    %s\n", scan.Image)
	scanned := "Scanned:  " + humanTime(scan.FinishedAt)
	if scan.ScannerVersion != "" {
		scanned += ", with Trivy " + scan.ScannerVersion
	}
	fmt.Fprintln(out, scanned)
	switch {
	case report.Current || report.Image == "":
	case report.Undeployed:
		fmt.Fprintf(out, "          This image has not gone out: its deploy is under way, or was stopped. The app runs %s.\n", report.Image)
	default:
		fmt.Fprintf(out, "          The app runs another image now: %s.\n", report.Image)
	}
	c := scan.Counts
	fmt.Fprintf(out, "Found:    %d critical, %d high, %d medium, %d low, %d unknown; %d with a fix\n",
		c.Critical, c.High, c.Medium, c.Low, c.Unknown, scan.Fixable)
	if report.Blocking && scan.FixableCritical > 0 {
		fmt.Fprintf(out, "Deploys:  stopped, by %d critical vulnerabilities with a fix. `%s deploy --accept-vulnerabilities` deploys anyway.\n",
			scan.FixableCritical, version.Binary)
	}
	if len(scan.Findings) == 0 {
		return
	}

	shown := scan.Findings
	if !all && len(shown) > shownFindings {
		shown = shown[:shownFindings]
	}
	fmt.Fprintln(out)
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "SEVERITY\tPACKAGE\tINSTALLED\tFIXED IN\tADVISORY")
	for _, f := range shown {
		fixed := f.FixedIn
		if fixed == "" {
			fixed = "-"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", f.Severity, shorten(f.Package, 40), shorten(f.Installed, 30),
			shorten(fixed, 30), f.ID)
	}
	_ = table.Flush()
	more := len(scan.Findings) - len(shown) + scan.Omitted
	switch {
	case more > 0 && len(shown) < len(scan.Findings):
		fmt.Fprintf(out, "\n%d more; --all lists the %d the report kept.\n", more, len(scan.Findings))
	case more > 0:
		fmt.Fprintf(out, "\n%d more, less severe, that the report did not keep.\n", more)
	}
	if strings.TrimSpace(scan.Digest) != "" {
		fmt.Fprintf(out, "Digest:   %s\n", scan.Digest)
	}
}
