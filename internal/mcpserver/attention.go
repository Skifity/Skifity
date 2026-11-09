package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What needs attention across the team: the list the panel's overview leads
// with. Read-only, and built by the panel from what it has recorded, so it is
// the cheap first question when somebody asks an assistant whether anything
// is wrong, before reaching for an app's logs or events.

type attentionEntry struct {
	Severity    string `json:"severity"`
	Kind        string `json:"kind"`
	Message     string `json:"message"`
	TargetType  string `json:"target_type"`
	TargetID    string `json:"target_id"`
	TargetName  string `json:"target_name,omitempty"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Since       string `json:"since,omitempty"`
	// URL is the panel page a person deals with it on.
	URL string `json:"url"`
}

type attentionOutput struct {
	Items    []attentionEntry `json:"items"`
	Errors   int              `json:"errors"`
	Warnings int              `json:"warnings"`
}

func (s *Server) registerAttention() {
	addTool(s, &mcp.Tool{
		Name:        "get_attention",
		Annotations: reads("Get what needs attention"),
		Description: "List what needs attention across the team, errors first: failed deploys, apps with no instance running, " +
			"usage alerts going off, databases that failed, backups that failed or were not taken when due, domains whose " +
			"certificate could not be issued, apps with critical vulnerabilities or changed outside the panel, and servers " +
			"that stopped answering. Each says what is wrong in a sentence, what it is about, and the panel page to deal with " +
			"it on. Call this first when asked whether anything is wrong; an empty list means nothing is.",
	}, s.getAttention)
}

func (s *Server) getAttention(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, attentionOutput, error) {
	teamID, err := s.teamID(ctx)
	if err != nil {
		return errorResult(err), attentionOutput{}, nil
	}
	var answer struct {
		Items []struct {
			Kind            string    `json:"kind"`
			Severity        string    `json:"severity"`
			Message         string    `json:"message"`
			TargetType      string    `json:"target_type"`
			TargetID        string    `json:"target_id"`
			TargetLabel     string    `json:"target_label"`
			ProjectName     string    `json:"project_name"`
			EnvironmentName string    `json:"environment_name"`
			Since           time.Time `json:"since"`
			Path            string    `json:"path"`
		} `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", "/api/teams/"+teamID+"/attention", nil, &answer); err != nil {
		return errorResult(err), attentionOutput{}, nil
	}

	panel := strings.TrimSuffix(s.config.PanelURL, "/")
	out := attentionOutput{Items: make([]attentionEntry, 0, len(answer.Items))}
	for _, item := range answer.Items {
		if item.Severity == "error" {
			out.Errors++
		} else {
			out.Warnings++
		}
		out.Items = append(out.Items, attentionEntry{
			Severity: item.Severity, Kind: item.Kind, Message: item.Message,
			TargetType: item.TargetType, TargetID: item.TargetID, TargetName: item.TargetLabel,
			Project: item.ProjectName, Environment: item.EnvironmentName,
			Since: stamp(item.Since), URL: panel + item.Path,
		})
	}
	if len(out.Items) == 0 {
		return textResult("Nothing needs attention."), out, nil
	}
	return textResult(fmt.Sprintf("%d error(s) and %d warning(s) need attention, errors first.",
		out.Errors, out.Warnings)), out, nil
}
