package mcpserver

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/store"
)

// How an app is reached: its domains, and its ports that are not HTTP.

type domainSummary struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	Path      string `json:"path"`
	HTTPS     bool   `json:"https"`
	Automatic bool   `json:"automatic"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	// DNSRecord is the record the person creates at their DNS provider for
	// a domain of their own, when the panel knows where it has to point.
	DNSRecord *dnsRecord `json:"dns_record,omitempty"`
}

type dnsRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type listDomainsOutput struct {
	Domains []domainSummary `json:"domains"`
}

type addDomainInput struct {
	AppID    string `json:"app_id" jsonschema:"the app's id"`
	Hostname string `json:"hostname" jsonschema:"the hostname, such as shop.example.com, with no scheme and no path"`
	TLS      *bool  `json:"tls,omitempty" jsonschema:"get a certificate and serve HTTPS; on unless set to false"`
	Path     string `json:"path,omitempty" jsonschema:"only requests under this path reach the app, such as /api; left out means all of them"`
}

type addDomainOutput struct {
	Domain domainSummary `json:"domain"`
	Note   string        `json:"note"`
}

type domainIDInput struct {
	AppID    string `json:"app_id" jsonschema:"the app's id"`
	DomainID string `json:"domain_id" jsonschema:"the domain's id, as returned by list_domains"`
}

type portSummary struct {
	ID         string   `json:"id"`
	Port       int      `json:"port"`
	Protocol   string   `json:"protocol"`
	PublicPort int      `json:"public_port"`
	Addresses  []string `json:"addresses"`
}

type listPortsOutput struct {
	Ports []portSummary `json:"ports"`
}

type openPortInput struct {
	AppID      string `json:"app_id" jsonschema:"the app's id"`
	Port       int    `json:"port" jsonschema:"the port the app listens on"`
	Protocol   string `json:"protocol,omitempty" jsonschema:"tcp or udp; tcp when left out"`
	PublicPort int    `json:"public_port,omitempty" jsonschema:"the port opened on every server; the app's own port when left out, which is what clients usually expect"`
}

type openPortOutput struct {
	Port portSummary `json:"port"`
	Note string      `json:"note"`
}

type portIDInput struct {
	AppID  string `json:"app_id" jsonschema:"the app's id"`
	PortID string `json:"port_id" jsonschema:"the port's id, as returned by list_ports"`
}

// portAnswer is a port as the panel lists it.
type portAnswer struct {
	store.AppPort
	Addresses []string `json:"addresses"`
}

func (s *Server) registerDomains() {
	addTool(s, &mcp.Tool{
		Name:        "list_domains",
		Annotations: reads("List domains"),
		Description: "List the hostnames an app answers on, with whether each is serving yet, and for a domain of the person's own the DNS record it needs. " +
			"A domain that stays pending usually has no DNS record yet, or one that does not point here.",
	}, s.listDomains)

	addTool(s, &mcp.Tool{
		Name:        "add_domain",
		Annotations: changes("Add a domain", false, false),
		Description: "Put an app on a hostname of the person's own, with HTTPS. " +
			"Returns the DNS record they have to create at their DNS provider — its type, name and value — which you cannot create for them: give it to them exactly. " +
			"The certificate is issued once the record points here, which can take minutes to hours. The panel's own hostname is refused.",
	}, s.addDomain)

	addTool(s, &mcp.Tool{
		Name:        "remove_domain",
		Annotations: changes("Remove a domain", true, true),
		Description: "Take a hostname off an app: it stops reaching the app. The automatic address cannot be removed. " +
			"Ask the person first when the domain is serving visitors.",
	}, s.removeDomain)

	addTool(s, &mcp.Tool{
		Name:        "list_ports",
		Annotations: reads("List ports"),
		Description: "List the TCP and UDP ports an app takes connections on besides HTTP, with the addresses they are reached at.",
	}, s.listPorts)

	addTool(s, &mcp.Tool{
		Name:        "open_port",
		Annotations: changes("Open a port", false, false),
		InputSchema: inputSchema[openPortInput](func(p map[string]*jsonschema.Schema) {
			portRange(p["port"])
			portRange(p["public_port"])
			oneOf(p["protocol"], "tcp", "udp")
		}),
		Description: "Open a TCP or UDP port on every server, for connections that are not HTTP: a game server, an MQTT broker, a mail server. " +
			"Returns the addresses it is reached at. A public port belongs to one app on the whole panel. " +
			"Not for a website or an API: those get add_domain, because an open port has no HTTPS and the app's firewall does not apply to it.",
	}, s.openPort)

	addTool(s, &mcp.Tool{
		Name:        "close_port",
		Annotations: changes("Close a port", true, true),
		Description: "Close one of an app's public ports on every server: whatever connects to it is cut off. Take the port_id from list_ports.",
	}, s.closePort)
}

// recordFor is the DNS record a domain needs, or nil when there is none to
// make: the automatic address already points here, and an unknown target
// cannot be written as one.
//
// The type follows the value, as it does in the panel: an IPv4 address is an
// A record, an IPv6 address an AAAA, and a name — a load balancer's — a
// CNAME.
func recordFor(domain store.Domain) *dnsRecord {
	target := strings.TrimSpace(domain.DNSTarget)
	if domain.Auto || target == "" {
		return nil
	}
	kind := "CNAME"
	if ip := net.ParseIP(target); ip != nil {
		kind = "AAAA"
		if ip.To4() != nil {
			kind = "A"
		}
	}
	return &dnsRecord{Type: kind, Name: domain.Hostname, Value: target}
}

func summariseDomain(domain store.Domain) domainSummary {
	return domainSummary{
		ID: domain.ID, Hostname: domain.Hostname, Path: domain.Path, HTTPS: domain.TLS,
		Automatic: domain.Auto, Status: domain.Status, Detail: domain.StatusDetail,
		DNSRecord: recordFor(domain),
	}
}

func (s *Server) domains(ctx context.Context, appID string) ([]store.Domain, error) {
	var response struct {
		Items []store.Domain `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", appPath(appID, "/domains"), nil, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (s *Server) listDomains(ctx context.Context, _ *mcp.CallToolRequest, in appIDInput) (*mcp.CallToolResult, listDomainsOutput, error) {
	domains, err := s.domains(ctx, in.AppID)
	if err != nil {
		return errorResult(err), listDomainsOutput{}, nil
	}
	out := listDomainsOutput{Domains: make([]domainSummary, 0, len(domains))}
	for _, domain := range domains {
		out.Domains = append(out.Domains, summariseDomain(domain))
	}
	return textResult(fmt.Sprintf("%d domain(s).", len(out.Domains))), out, nil
}

func (s *Server) addDomain(ctx context.Context, _ *mcp.CallToolRequest, in addDomainInput) (*mcp.CallToolResult, addDomainOutput, error) {
	body := map[string]any{"hostname": in.Hostname}
	if in.TLS != nil {
		body["tls"] = *in.TLS
	}
	if in.Path != "" {
		body["path"] = in.Path
	}
	var added store.Domain
	if err := s.client.Do(ctx, "POST", appPath(in.AppID, "/domains"), body, &added); err != nil {
		return errorResult(err), addDomainOutput{}, nil
	}

	// The panel's answer to adding one does not say where it has to point;
	// its list does, because that is a fact about the cluster rather than
	// about the domain. The record is the one thing the person has to act
	// on, so it is worth the second request.
	listed, listErr := s.domains(ctx, in.AppID)
	for _, domain := range listed {
		if domain.ID == added.ID {
			added.DNSTarget = domain.DNSTarget
		}
	}

	out := addDomainOutput{Domain: summariseDomain(added)}
	switch record := out.Domain.DNSRecord; {
	case record != nil:
		out.Note = fmt.Sprintf("Ask the person to create a DNS record at their DNS provider: type %s, name %s, value %s. "+
			"Some providers want only the part before their domain as the name. "+
			"The certificate is issued once the record is live; list_domains shows when the domain is serving.",
			record.Type, record.Name, record.Value)
	case listErr != nil:
		// Added, and then the panel could not be asked where it points. That
		// is not the same as the panel not knowing, and saying it was would
		// send the person looking for a setting that is fine.
		out.Note = "Call list_domains for the DNS record the person has to create."
	default:
		out.Note = "The panel does not know which public address its apps answer on, so it cannot say where the DNS record should point. " +
			"The person points " + added.Hostname + " at their server's public IP address, and an administrator can set that address under Settings, Domains."
	}
	return textResult(fmt.Sprintf("Added %s. %s", added.Hostname, out.Note)), out, nil
}

func (s *Server) removeDomain(ctx context.Context, _ *mcp.CallToolRequest, in domainIDInput) (*mcp.CallToolResult, doneOutput, error) {
	if err := s.client.Do(ctx, "DELETE", appPath(in.AppID, "/domains/"+url.PathEscape(in.DomainID)), nil, nil); err != nil {
		return errorResult(err), doneOutput{}, nil
	}
	return done("Removed. The hostname no longer reaches the app.")
}

func summarisePort(port portAnswer) portSummary {
	addresses := port.Addresses
	if addresses == nil {
		addresses = []string{}
	}
	return portSummary{
		ID: port.ID, Port: port.Port, Protocol: port.Protocol, PublicPort: port.PublicPort, Addresses: addresses,
	}
}

func (s *Server) listPorts(ctx context.Context, _ *mcp.CallToolRequest, in appIDInput) (*mcp.CallToolResult, listPortsOutput, error) {
	var response struct {
		Items []portAnswer `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", appPath(in.AppID, "/ports"), nil, &response); err != nil {
		return errorResult(err), listPortsOutput{}, nil
	}
	out := listPortsOutput{Ports: make([]portSummary, 0, len(response.Items))}
	for _, port := range response.Items {
		out.Ports = append(out.Ports, summarisePort(port))
	}
	return textResult(fmt.Sprintf("%d port(s) besides HTTP.", len(out.Ports))), out, nil
}

func (s *Server) openPort(ctx context.Context, _ *mcp.CallToolRequest, in openPortInput) (*mcp.CallToolResult, openPortOutput, error) {
	body := map[string]any{"port": in.Port}
	if in.Protocol != "" {
		body["protocol"] = in.Protocol
	}
	if in.PublicPort > 0 {
		body["public_port"] = in.PublicPort
	}
	var opened portAnswer
	if err := s.client.Do(ctx, "POST", appPath(in.AppID, "/ports"), body, &opened); err != nil {
		return errorResult(err), openPortOutput{}, nil
	}
	out := openPortOutput{Port: summarisePort(opened)}
	where := strings.Join(opened.Addresses, ", ")
	if where == "" {
		where = fmt.Sprintf("any of the servers' public addresses, port %d", opened.PublicPort)
	}
	out.Note = fmt.Sprintf("%d/%s is open on every server: reach it at %s.", opened.PublicPort, opened.Protocol, where)
	return textResult(out.Note), out, nil
}

func (s *Server) closePort(ctx context.Context, _ *mcp.CallToolRequest, in portIDInput) (*mcp.CallToolResult, doneOutput, error) {
	if err := s.client.Do(ctx, "DELETE", appPath(in.AppID, "/ports/"+url.PathEscape(in.PortID)), nil, nil); err != nil {
		return errorResult(err), doneOutput{}, nil
	}
	return done("Closed on every server.")
}
