package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/store"
)

// What the image an app runs is known to be vulnerable to.
//
// Read-only on purpose. Starting a scan is harmless, and still not offered:
// the panel scans every image when it is deployed and again every day, so an
// assistant has the answer already, and a tool that queues work is one an
// assistant calls in a loop waiting for it. Deploying past the check for
// critical vulnerabilities is not offered either — deploy_app never accepts
// them — because going past a security check is a person's decision.

// vulnerabilityFindingsShown is how many findings the tool passes on. The
// most severe come first, and an assistant that needs the rest has the
// counts to say so.
const vulnerabilityFindingsShown = 50

type vulnerabilitiesOutput struct {
	Scanned bool `json:"scanned"`
	// Image is what the report is of, and CurrentImage what the app runs now.
	Image        string `json:"image,omitempty"`
	CurrentImage string `json:"current_image,omitempty"`
	// Current is the report being of the image the app runs now.
	Current        bool                `json:"current"`
	ScannedAt      string              `json:"scanned_at,omitempty"`
	ScannerVersion string              `json:"scanner_version,omitempty"`
	Counts         store.ScanCounts    `json:"counts"`
	Fixable        int                 `json:"fixable"`
	FixableCrit    int                 `json:"fixable_critical"`
	Findings       []store.ScanFinding `json:"findings"`
	// NotShown is how many findings there are beyond Findings.
	NotShown int `json:"not_shown"`
	// DeploysBlocked is the panel stopping deploys of an image with a
	// critical vulnerability that has a fix, which this one has.
	DeploysBlocked bool   `json:"deploys_blocked"`
	ScanningOff    bool   `json:"scanning_off"`
	Note           string `json:"note"`
}

// vulnerabilityAnswer is the panel's answer, as the API describes it.
type vulnerabilityAnswer struct {
	Enabled    bool             `json:"enabled"`
	Blocking   bool             `json:"blocking"`
	Image      string           `json:"image"`
	Scan       *store.ImageScan `json:"scan"`
	Current    bool             `json:"current"`
	Undeployed bool             `json:"undeployed"`
	Latest     *store.ImageScan `json:"latest"`
}

func (s *Server) registerVulnerabilities() {
	addTool(s, &mcp.Tool{
		Name:        "get_vulnerabilities",
		Annotations: reads("Get an app's vulnerabilities"),
		Description: "List the known vulnerabilities in the image an app runs, from the panel's newest scan: how many of each severity, " +
			"and the most severe, each with the package, the installed version, the version that fixes it and the advisory. " +
			"The panel scans every image when it is deployed and again daily. A finding with fixed_in is fixed by moving to that version, " +
			"usually through a newer base image or dependency; one without has no fix yet.",
	}, s.getVulnerabilities)
}

func (s *Server) getVulnerabilities(ctx context.Context, _ *mcp.CallToolRequest, in appIDInput) (*mcp.CallToolResult, vulnerabilitiesOutput, error) {
	var answer vulnerabilityAnswer
	if err := s.client.Do(ctx, "GET", appPath(in.AppID, "/vulnerabilities"), nil, &answer); err != nil {
		return errorResult(err), vulnerabilitiesOutput{}, nil
	}
	out := vulnerabilitiesOutput{CurrentImage: answer.Image, ScanningOff: !answer.Enabled, Findings: []store.ScanFinding{}}
	scan := answer.Scan
	if scan == nil {
		out.Note = "This app's image has not been scanned yet."
		if latest := answer.Latest; latest != nil && latest.Status == store.ScanFailed {
			out.Note = "The last scan failed: " + latest.ErrorMessage
		}
		if !answer.Enabled {
			out.Note += " Scanning is switched off for this panel."
		}
		return textResult(out.Note), out, nil
	}

	out.Scanned = true
	out.Image = scan.Image
	out.Current = answer.Current
	out.ScannedAt = stamp(scan.FinishedAt)
	out.ScannerVersion = scan.ScannerVersion
	out.Counts = scan.Counts
	out.Fixable = scan.Fixable
	out.FixableCrit = scan.FixableCritical
	out.DeploysBlocked = answer.Blocking && scan.FixableCritical > 0
	out.Findings = scan.Findings
	if len(out.Findings) > vulnerabilityFindingsShown {
		out.Findings = out.Findings[:vulnerabilityFindingsShown]
	}
	out.NotShown = scan.Counts.Total() - len(out.Findings)

	var note strings.Builder
	c := scan.Counts
	fmt.Fprintf(&note, "%d critical, %d high, %d medium, %d low and %d unknown; %d have a fix.",
		c.Critical, c.High, c.Medium, c.Low, c.Unknown, scan.Fixable)
	switch {
	case answer.Current || answer.Image == "":
	case answer.Undeployed:
		note.WriteString(" This report is of an image that has not gone out: its deploy is under way, or was stopped. " +
			"The app runs current_image.")
	default:
		note.WriteString(" This report is of another image than the one the app runs now, which is current_image.")
	}
	if out.DeploysBlocked {
		note.WriteString(" The panel stops deploys of an image with a critical vulnerability that has a fix; " +
			"a person can deploy past that, and only a person should.")
	}
	if latest := answer.Latest; latest != nil && latest.Status == store.ScanFailed {
		note.WriteString(" The newest scan failed, so this is the one before it: " + latest.ErrorMessage)
	}
	out.Note = note.String()
	return textResult(out.Note), out, nil
}
