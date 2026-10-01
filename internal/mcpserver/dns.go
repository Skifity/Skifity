package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/store"
)

// The team's DNS providers, where the panel creates its domains' records.
//
// An assistant can see which providers are connected and which zones they
// serve, so it can tell the person whether a domain's record will be created
// for them. It cannot connect one or remove one: connecting means handing
// over a credential that can change the team's DNS, and that is done in the
// panel or with `skifity dns providers add`, which reads it from a file.

type dnsProviderSummary struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Zones    []string `json:"zones"`
	// Records is how many records the panel keeps through it.
	Records int `json:"records"`
}

type listDNSProvidersOutput struct {
	Providers []dnsProviderSummary `json:"providers"`
}

type dnsProviderIDInput struct {
	ProviderID string `json:"provider_id" jsonschema:"the connection's id, as returned by list_dns_providers"`
}

type listDNSZonesOutput struct {
	Zones []string `json:"zones"`
}

// dnsProviderAnswer is the part of a connection the tools read. The panel
// never sends a credential; nothing here would carry one if it did.
type dnsProviderAnswer struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Title   string          `json:"title"`
	Zones   []store.DNSZone `json:"zones"`
	Records int             `json:"records"`
}

func (s *Server) registerDNS() {
	addTool(s, &mcp.Tool{
		Name:        "list_dns_providers",
		Annotations: reads("List DNS providers"),
		Description: "List the DNS providers the team has connected — Cloudflare, Hetzner, DigitalOcean, Route 53 — with the zones each serves, as last seen. " +
			"A domain added under one of those zones gets its DNS record created there by the panel; any other domain's record the person creates by hand. " +
			"Never returns a credential. Connecting one is done in the panel or with `skifity dns providers add`.",
	}, s.listDNSProviders)

	addTool(s, &mcp.Tool{
		Name:        "list_dns_zones",
		Annotations: reads("List DNS zones"),
		Description: "Ask one connected DNS provider for the zones it serves now, which also updates the panel's list. " +
			"Use it when a zone the person just added at the provider is not in list_dns_providers yet. Take the provider_id from list_dns_providers.",
	}, s.listDNSZones)
}

func (s *Server) listDNSProviders(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listDNSProvidersOutput, error) {
	teamID, err := s.teamID(ctx)
	if err != nil {
		return errorResult(err), listDNSProvidersOutput{}, nil
	}
	var response struct {
		Items []dnsProviderAnswer `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", "/api/teams/"+url.PathEscape(teamID)+"/dns-providers", nil, &response); err != nil {
		return errorResult(err), listDNSProvidersOutput{}, nil
	}
	out := listDNSProvidersOutput{Providers: make([]dnsProviderSummary, 0, len(response.Items))}
	for _, p := range response.Items {
		summary := dnsProviderSummary{ID: p.ID, Name: p.Name, Provider: p.Title, Zones: zoneNames(p.Zones), Records: p.Records}
		out.Providers = append(out.Providers, summary)
	}
	if len(out.Providers) == 0 {
		return textResult("No DNS providers are connected: the person creates each domain's record by hand."), out, nil
	}
	return textResult(fmt.Sprintf("%d DNS provider(s).", len(out.Providers))), out, nil
}

func (s *Server) listDNSZones(ctx context.Context, _ *mcp.CallToolRequest, in dnsProviderIDInput) (*mcp.CallToolResult, listDNSZonesOutput, error) {
	teamID, err := s.teamID(ctx)
	if err != nil {
		return errorResult(err), listDNSZonesOutput{}, nil
	}
	var response struct {
		Items []store.DNSZone `json:"items"`
	}
	path := "/api/teams/" + url.PathEscape(teamID) + "/dns-providers/" + url.PathEscape(in.ProviderID) + "/zones"
	if err := s.client.Do(ctx, "GET", path, nil, &response); err != nil {
		return errorResult(err), listDNSZonesOutput{}, nil
	}
	out := listDNSZonesOutput{Zones: zoneNames(response.Items)}
	return textResult(fmt.Sprintf("%d zone(s): %s.", len(out.Zones), strings.Join(out.Zones, ", "))), out, nil
}

func zoneNames(zones []store.DNSZone) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		out = append(out, z.Name)
	}
	return out
}

// managedSummary says in one line what the panel does about a domain's
// record at the team's DNS provider, or "" when no connected zone covers it.
func managedSummary(m *store.ManagedDNS) string {
	if m == nil {
		return ""
	}
	where := m.ProviderName + " (zone " + m.Zone + ")"
	switch m.State {
	case store.DNSStateCreated:
		values := make([]string, 0, len(m.Records))
		for _, r := range m.Records {
			values = append(values, r.Type+" "+r.Content)
		}
		return "created and kept by the panel at " + where + ": " + strings.Join(values, ", ")
	case store.DNSStateElsewhere:
		return "managed elsewhere: a record somebody else made at " + where + " already points here"
	case store.DNSStateRefused:
		return "not created at " + where + ": a record somebody else made is in the way; the panel never overwrites one, so the person removes or changes it at the provider and tries again"
	case store.DNSStateFailed:
		return "not created yet: " + where + " did not answer; the panel tries again every few minutes"
	case store.DNSStatePending:
		return "being created at " + where
	case store.DNSStateOff:
		return "left to the person at " + where + ": the panel was told not to keep it"
	}
	return "not created: " + where + " covers it, and the person can have the panel create it from the Domains tab"
}
