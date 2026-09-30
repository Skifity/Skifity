package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/errdoc"
	"skifity/internal/logging"
)

// What Kubernetes said about an app's objects: the scheduler's refusal, the
// probe that failed, the instance the kernel killed for its memory. When an
// app will not start and its logs are empty, this is where the answer is.

type eventsInput struct {
	AppID        string `json:"app_id,omitempty" jsonschema:"the app's id; give this or database_id"`
	DatabaseID   string `json:"database_id,omitempty" jsonschema:"a managed database's id, for its events instead of an app's"`
	WarningsOnly bool   `json:"warnings_only,omitempty" jsonschema:"only what went wrong"`
}

type eventSummary struct {
	Type        string `json:"type"`
	Reason      string `json:"reason"`
	Object      string `json:"object"`
	Count       int    `json:"count"`
	LastSeen    string `json:"last_seen"`
	Message     string `json:"message"`
	Explanation string `json:"explanation,omitempty"`
}

type eventsOutput struct {
	Events []eventSummary `json:"events"`
	Note   string         `json:"note,omitempty"`
}

func (s *Server) registerEvents() {
	addTool(s, &mcp.Tool{
		Name:        "get_events",
		Annotations: reads("Get Kubernetes events"),
		Description: "List what Kubernetes said about an app's or a database's instances, newest first, with repeats counted: " +
			"a pod the scheduler could not place, a failed health check, an instance killed for using too much memory, an image that would not pull. " +
			"The common warnings come with what they mean and what to do. " +
			"Use it when an app will not start or keeps restarting and get_app_logs has nothing: the reason is usually here. " +
			"Kubernetes keeps events for about an hour.",
	}, s.getEvents)
}

func (s *Server) getEvents(ctx context.Context, _ *mcp.CallToolRequest, in eventsInput) (*mcp.CallToolResult, eventsOutput, error) {
	var path string
	switch {
	case in.AppID != "" && in.DatabaseID != "":
		return errorResult(errdoc.BadRequest("Give app_id or database_id, not both.")), eventsOutput{}, nil
	case in.DatabaseID != "":
		path = databasePath(in.DatabaseID, "/events")
	case in.AppID != "":
		path = appPath(in.AppID, "/events")
	default:
		return errorResult(errdoc.BadRequest("Give the app_id from list_apps, or a database_id.")), eventsOutput{}, nil
	}
	if in.WarningsOnly {
		path += "?type=Warning"
	}
	var response struct {
		Items []struct {
			Type        string    `json:"type"`
			Reason      string    `json:"reason"`
			Kind        string    `json:"kind"`
			Name        string    `json:"name"`
			Message     string    `json:"message"`
			Count       int       `json:"count"`
			LastSeen    time.Time `json:"last_seen"`
			Explanation string    `json:"explanation"`
		} `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", path, nil, &response); err != nil {
		return errorResult(err), eventsOutput{}, nil
	}

	out := eventsOutput{Events: []eventSummary{}}
	var text strings.Builder
	for _, event := range response.Items {
		// The panel has already taken the app's own secrets out; this takes
		// out anything that merely looks like one, because what reaches an
		// assistant's context cannot be taken back.
		summary := eventSummary{
			Type: event.Type, Reason: event.Reason, Object: event.Kind + "/" + event.Name,
			Count: event.Count, LastSeen: stamp(event.LastSeen),
			Message:     logging.Scrub(event.Message),
			Explanation: logging.Scrub(event.Explanation),
		}
		out.Events = append(out.Events, summary)
		fmt.Fprintf(&text, "%s %s %s (%d×, last %s): %s\n", summary.Type, summary.Reason, summary.Object,
			summary.Count, summary.LastSeen, summary.Message)
		if summary.Explanation != "" {
			fmt.Fprintf(&text, "  What it means: %s\n", summary.Explanation)
		}
	}
	if len(out.Events) == 0 {
		out.Note = "Nothing in the last hour or so, which is as long as Kubernetes keeps events."
		return textResult(out.Note), out, nil
	}
	return textResult(strings.TrimSpace(text.String())), out, nil
}
