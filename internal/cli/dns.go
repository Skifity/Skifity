package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// dnsProviderAnswer is a DNS provider connection as the panel lists it. There
// is no credential in it: the panel never sends one.
type dnsProviderAnswer struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"zones"`
	ZonesListedAt time.Time `json:"zones_listed_at"`
	Records       int       `json:"records"`
}

// dnsKinds are the providers a connection can be to.
var dnsKinds = []string{"cloudflare", "hetzner", "digitalocean", "route53"}

// cmdDNS manages the team's DNS providers, where the panel creates the
// records of its domains.
//
//	skifity dns providers                                    what the team has connected
//	skifity dns providers add --kind cloudflare --credentials token.txt
//	skifity dns providers add --kind route53 --credentials ~/.aws/credentials
//	pass show cf | skifity dns providers add --kind cloudflare --credentials -
//	skifity dns providers test Cloudflare                    sign in again and list its zones
//	skifity dns providers zones Cloudflare                   the zones, asked now
//	skifity dns providers remove Cloudflare
//
// The credentials are read from a file or from standard input, or typed at a
// prompt that does not echo, and never taken from the command line, where
// they would be in the shell's history and in every process listing.
func cmdDNS(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns", flag.ContinueOnError)
	flags.SetOutput(out)
	kind := flags.String("kind", "", "cloudflare, hetzner, digitalocean or route53, for add")
	name := flags.String("name", "", "what to call the connection, for add; the provider's name when left out")
	credentials := flags.String("credentials", "", "a file holding the token — or, for route53, aws_access_key_id and "+
		"aws_secret_access_key lines — or - for standard input; never the credential itself")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 || positional[0] != "providers" {
		return errdoc.BadRequest(fmt.Sprintf("Use `%s dns providers`, with list, add, test, zones or remove.", version.Binary))
	}
	positional = positional[1:]
	action := "list"
	if len(positional) > 0 {
		action, positional = positional[0], positional[1:]
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	teamID, err := resolveTeam(ctx, client, cfg)
	if err != nil {
		return err
	}
	path := "/api/teams/" + url.PathEscape(teamID) + "/dns-providers"

	var listed struct {
		Items []dnsProviderAnswer `json:"items"`
	}
	find := func() (dnsProviderAnswer, error) {
		if len(positional) != 1 {
			return dnsProviderAnswer{}, errdoc.BadRequest(fmt.Sprintf("Name the connection: `%s dns providers %s NAME`.", version.Binary, action))
		}
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return dnsProviderAnswer{}, err
		}
		i := slices.IndexFunc(listed.Items, func(p dnsProviderAnswer) bool {
			return strings.EqualFold(p.Name, positional[0]) || p.ID == positional[0]
		})
		if i < 0 {
			return dnsProviderAnswer{}, errdoc.BadRequest(fmt.Sprintf("The team has no DNS provider called %q. `%s dns providers` lists the ones it has.",
				positional[0], version.Binary))
		}
		return listed.Items[i], nil
	}

	switch action {
	case "list", "ls":
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed.Items)
		}
		if len(listed.Items) == 0 {
			fmt.Fprintf(out, "No DNS providers: the record of each domain is created by hand.\n"+
				"Connect one with `%s dns providers add --kind cloudflare --credentials token.txt`.\n", version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tPROVIDER\tZONES\tRECORDS KEPT")
		for _, p := range listed.Items {
			zones := make([]string, 0, len(p.Zones))
			for _, z := range p.Zones {
				zones = append(zones, z.Name)
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%d\n", p.Name, p.Title, orNone(strings.Join(zones, ", ")), p.Records)
		}
		return table.Flush()

	case "add", "connect":
		if !slices.Contains(dnsKinds, *kind) {
			return errdoc.BadRequest("Say which provider with --kind: cloudflare, hetzner, digitalocean or route53.")
		}
		body, err := dnsCredentials(*kind, *credentials, out)
		if err != nil {
			return err
		}
		body["kind"] = *kind
		if *name != "" {
			body["name"] = *name
		}
		var connected dnsProviderAnswer
		if err := client.Do(ctx, "POST", path, body, &connected); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, connected)
		}
		fmt.Fprintf(out, "%s is connected, with %d zone(s): %s.\n"+
			"A domain added in one of them gets its DNS record created there.\n",
			connected.Name, len(connected.Zones), zoneNames(connected))
		return nil

	case "test", "zones":
		provider, err := find()
		if err != nil {
			return err
		}
		var answer dnsProviderAnswer
		if action == "test" {
			if err := client.Do(ctx, "POST", path+"/"+url.PathEscape(provider.ID)+"/test", map[string]any{}, &answer); err != nil {
				return err
			}
		} else {
			var zones struct {
				Items []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"items"`
			}
			if err := client.Do(ctx, "GET", path+"/"+url.PathEscape(provider.ID)+"/zones", nil, &zones); err != nil {
				return err
			}
			answer = provider
			answer.Zones = zones.Items
			if *asJSON {
				return writeJSON(out, zones.Items)
			}
		}
		if *asJSON {
			return writeJSON(out, answer)
		}
		fmt.Fprintf(out, "%s answers, and serves %d zone(s): %s.\n", answer.Name, len(answer.Zones), zoneNames(answer))
		return nil

	case "remove", "rm", "delete":
		provider, err := find()
		if err != nil {
			return err
		}
		var removed struct {
			Left int `json:"left"`
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(provider.ID), nil, &removed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]any{"removed": provider.Name, "left": removed.Left})
		}
		fmt.Fprintf(out, "%s is removed.", provider.Name)
		if removed.Left > 0 {
			fmt.Fprintf(out, " The %d record(s) the panel created through it stay at the provider, and are yours now.", removed.Left)
		}
		fmt.Fprintln(out)
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("dns providers takes ls, add, test, zones or remove, not %q.", action))
}

func zoneNames(p dnsProviderAnswer) string {
	names := make([]string, 0, len(p.Zones))
	for _, z := range p.Zones {
		names = append(names, z.Name)
	}
	return orNone(strings.Join(names, ", "))
}

// dnsStdin is where `--credentials -` reads from; a test replaces it.
var dnsStdin io.Reader = os.Stdin

// dnsCredentials reads a connection's credentials: from the file --credentials
// names, from standard input for -, or from a prompt that does not echo.
func dnsCredentials(kind, source string, out io.Writer) (map[string]any, error) {
	var data []byte
	switch {
	case source == "-":
		read, err := io.ReadAll(io.LimitReader(dnsStdin, 64<<10))
		if err != nil {
			return nil, fmt.Errorf("read the credentials from standard input: %w", err)
		}
		data = read
	case source != "":
		read, err := os.ReadFile(source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !strings.ContainsAny(source, `/\.`) && len(source) >= 20 {
				return nil, errdoc.BadRequest("--credentials takes the name of a file, or - for standard input, not the credential itself. " +
					"Something typed on the command line is in the shell's history; if that was a token, revoke it and make another.")
			}
			return nil, errdoc.BadRequest(fmt.Sprintf("--credentials names %s, which could not be read: %v.", source, err))
		}
		data = read
	case isInteractive():
		if kind == "route53" {
			id := prompt(out, "Access key id: ")
			secret := promptSecret(out, "Secret access key: ")
			return map[string]any{"access_key_id": id, "secret_access_key": secret}, nil
		}
		return map[string]any{"token": promptSecret(out, "API token: ")}, nil
	default:
		return nil, errdoc.BadRequest("Give the credentials with --credentials FILE, or --credentials - to read them from standard input.")
	}
	if kind == "route53" {
		id, secret := awsKeys(data)
		if id == "" || secret == "" {
			return nil, errdoc.BadRequest("The credentials need an aws_access_key_id line and an aws_secret_access_key line, " +
				"as in ~/.aws/credentials, or AWS_ACCESS_KEY_ID= and AWS_SECRET_ACCESS_KEY= lines.")
		}
		return map[string]any{"access_key_id": id, "secret_access_key": secret}, nil
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errdoc.BadRequest("The credentials file should hold the token alone, on one line.")
	}
	return map[string]any{"token": token}, nil
}

// awsKeys reads an access key from the shape ~/.aws/credentials has, or from
// environment-style lines. The first profile wins.
func awsKeys(data []byte) (id, secret string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && (id != "" || secret != "") {
			break
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export ")))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "aws_access_key_id":
			if id == "" {
				id = value
			}
		case "aws_secret_access_key":
			if secret == "" {
				secret = value
			}
		}
	}
	return id, secret
}

// domainAnswer is one of an app's domains as the panel lists it.
type domainAnswer struct {
	ID         string `json:"id"`
	Hostname   string `json:"hostname"`
	TLS        bool   `json:"tls"`
	Auto       bool   `json:"auto"`
	Status     string `json:"status"`
	RedirectTo string `json:"redirect_to,omitempty"`
	DNSTarget  string `json:"dns_target,omitempty"`
	ManagedDNS *struct {
		ProviderName string `json:"provider_name"`
		Zone         string `json:"zone"`
		Manage       bool   `json:"manage"`
		State        string `json:"state"`
		Records      []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"records"`
		Problem *errdoc.Problem `json:"problem"`
	} `json:"managed_dns,omitempty"`
}

// cmdDomains lists, adds and removes an app's domains.
//
//	skifity domains                                   the app's domains and their records
//	skifity domains add shop.example.com              the record is created at the team's
//	                                                  DNS provider when a connected zone covers it
//	skifity domains add shop.example.com --manage-dns=false
//	skifity domains remove shop.example.com
func cmdDomains(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("domains", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	manageDNS := flags.Bool("manage-dns", true, "create and keep the domain's DNS record at the team's DNS provider; "+
		"left out, it is whenever a connected zone covers the domain, and true where none does is refused")
	noTLS := flags.Bool("no-tls", false, "serve plain HTTP only, with no certificate")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	manageSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "manage-dns" {
			manageSet = true
		}
	})
	action := "list"
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
	path := "/api/apps/" + url.PathEscape(app) + "/domains"

	var listed struct {
		Items []domainAnswer `json:"items"`
	}
	switch action {
	case "list", "ls":
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed.Items)
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "DOMAIN\tSTATUS\tDNS RECORD")
		for _, d := range listed.Items {
			fmt.Fprintf(table, "%s\t%s\t%s\n", d.Hostname, d.Status, describeRecord(d))
		}
		return table.Flush()

	case "add":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the domain: `%s domains add shop.example.com`.", version.Binary))
		}
		body := map[string]any{"hostname": positional[0], "tls": !*noTLS}
		if manageSet {
			body["manage_dns"] = *manageDNS
		}
		var added domainAnswer
		if err := client.Do(ctx, "POST", path, body, &added); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, added)
		}
		fmt.Fprintf(out, "%s is added. DNS: %s.\n", added.Hostname, describeRecord(added))
		if m := added.ManagedDNS; m != nil && m.Problem != nil {
			fmt.Fprintf(out, "\n%s\n", m.Problem.Text())
		}
		return nil

	case "remove", "rm", "delete":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the domain: `%s domains remove shop.example.com`.", version.Binary))
		}
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		i := slices.IndexFunc(listed.Items, func(d domainAnswer) bool { return strings.EqualFold(d.Hostname, positional[0]) })
		if i < 0 {
			return errdoc.BadRequest(fmt.Sprintf("This app has no domain %s. `%s domains` lists the ones it has.", positional[0], version.Binary))
		}
		if err := client.Do(ctx, "DELETE", path+"/"+url.PathEscape(listed.Items[i].ID), nil, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]string{"removed": listed.Items[i].Hostname})
		}
		fmt.Fprintf(out, "%s is removed", listed.Items[i].Hostname)
		if m := listed.Items[i].ManagedDNS; m != nil && m.State == "created" {
			fmt.Fprintf(out, ", and the record the panel created at %s with it", m.ProviderName)
		}
		fmt.Fprintln(out, ".")
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("domains takes ls, add or remove, not %q.", action))
}

// describeRecord says where a domain's record stands, in a few words.
func describeRecord(d domainAnswer) string {
	if d.Auto && d.ManagedDNS == nil {
		return "points here already"
	}
	m := d.ManagedDNS
	if m == nil {
		if d.DNSTarget == "" {
			return "create it by hand"
		}
		return "create it by hand, pointing at " + d.DNSTarget
	}
	switch m.State {
	case "created":
		values := make([]string, 0, len(m.Records))
		for _, r := range m.Records {
			values = append(values, r.Type+" "+r.Content)
		}
		return "created at " + m.ProviderName + " (" + strings.Join(values, ", ") + ")"
	case "elsewhere":
		return "managed elsewhere, already pointing here"
	case "refused":
		return "not created at " + m.ProviderName + ": another record is in the way"
	case "failed":
		return "not created yet: " + m.ProviderName + " did not answer; tried again"
	case "pending":
		return "being created at " + m.ProviderName
	}
	return "left to you, in " + m.ProviderName + "'s zone " + m.Zone
}
