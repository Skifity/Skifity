package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Where the team's logs are shipped.
//
// Read-only on purpose. Adding a drain takes a credential, and an assistant
// handed one would have it in its context and its transcript; changing where
// a team's logs go is a person's decision; and the panel never answers a
// drain's secrets, so there is nothing here that could pass one on.

type logDrainSummary struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Destination   string `json:"destination"`
	Status        string `json:"status"`
	Enabled       bool   `json:"enabled"`
	Projects      int    `json:"projects,omitempty"`
	EveryProject  bool   `json:"every_project"`
	IncludeBuilds bool   `json:"include_builds"`
	TestError     string `json:"test_error,omitempty"`
}

type listLogDrainsOutput struct {
	Drains []logDrainSummary `json:"drains"`
	// Collector is how the collector on the servers is doing.
	Collector string `json:"collector"`
	Note      string `json:"note"`
}

type drainListAnswer struct {
	Items []struct {
		Name          string   `json:"name"`
		Kind          string   `json:"kind"`
		Destination   string   `json:"destination"`
		Status        string   `json:"status"`
		Enabled       bool     `json:"enabled"`
		Scoped        bool     `json:"scoped"`
		Projects      []string `json:"projects"`
		IncludeBuilds bool     `json:"include_builds"`
		TestError     string   `json:"test_error"`
	} `json:"items"`
	Collector struct {
		Configuration string `json:"configuration"`
		Error         string `json:"error"`
		Live          *struct {
			State   string `json:"state"`
			Desired int    `json:"desired"`
			Ready   int    `json:"ready"`
		} `json:"live"`
	} `json:"collector"`
}

func (s *Server) registerLogDrains() {
	addTool(s, &mcp.Tool{
		Name:        "list_log_drains",
		Annotations: reads("List log drains"),
		Description: "List where the team's apps' logs are shipped — Loki, Elasticsearch, Datadog, Axiom, Better Stack, New Relic, " +
			"syslog or an HTTPS endpoint — with each drain's destination, whether it is limited to some projects, whether it " +
			"includes build logs, and whether the collector on the servers is running. Read-only: drains are added in the panel " +
			"or with `skifity drains add`, because adding one takes a credential.",
	}, s.listLogDrains)
}

func (s *Server) listLogDrains(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listLogDrainsOutput, error) {
	teamID, err := s.teamID(ctx)
	if err != nil {
		return errorResult(err), listLogDrainsOutput{}, nil
	}
	var answer drainListAnswer
	if err := s.client.Do(ctx, "GET", "/api/teams/"+url.PathEscape(teamID)+"/log-drains", nil, &answer); err != nil {
		return errorResult(err), listLogDrainsOutput{}, nil
	}
	out := listLogDrainsOutput{Drains: []logDrainSummary{}}
	for _, item := range answer.Items {
		out.Drains = append(out.Drains, logDrainSummary{
			Name: item.Name, Kind: item.Kind, Destination: item.Destination, Status: item.Status, Enabled: item.Enabled,
			Projects: len(item.Projects), EveryProject: !item.Scoped, IncludeBuilds: item.IncludeBuilds, TestError: item.TestError,
		})
	}
	collector := answer.Collector
	switch {
	case collector.Error != "":
		out.Collector = "failed: " + collector.Error
	case collector.Live != nil:
		out.Collector = fmt.Sprintf("%s on %d of %d servers", collector.Live.State, collector.Live.Ready, collector.Live.Desired)
	default:
		out.Collector = collector.Configuration
	}
	if len(out.Drains) == 0 {
		out.Note = "This team ships its logs nowhere."
		return textResult(out.Note), out, nil
	}
	names := make([]string, len(out.Drains))
	for i, drain := range out.Drains {
		names[i] = drain.Name + " (" + drain.Kind + ", " + drain.Status + ")"
	}
	out.Note = fmt.Sprintf("%d drain(s): %s. The collector: %s.", len(out.Drains), strings.Join(names, "; "), out.Collector)
	return textResult(out.Note), out, nil
}
