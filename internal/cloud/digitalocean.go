package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/netguard"
	"skifity/internal/version"
)

// DigitalOcean, through its public API: https://docs.digitalocean.com/reference/api/.
//
// The same five nouns as Hetzner Cloud under other names — droplets, account
// SSH keys, cloud firewalls, regions and sizes — and three differences that
// change what the code does rather than what it is called:
//
//   - Nothing carries labels. A droplet carries tags, which are bare strings of
//     letters, digits, colons, dashes and underscores, so a label is written as
//     a tag of the form skifity:<name>:<value> and read back the same way.
//   - A firewall reaches a droplet through a tag, not a list of firewalls on
//     the droplet. Each firewall gets a tag of its own, and a machine created
//     behind it carries that tag from its first boot. Not the machine's label
//     tags: a droplet gets every firewall that names any one of its tags, and
//     every machine the panel makes shares the managed one.
//   - A firewall with no outbound rules lets nothing out. Hetzner's lets
//     everything out, which is what a node needs to download k3s and pull
//     images, so every firewall here is written with outbound open.
//
// As with Hetzner the address is a constant, the token goes in the
// Authorization header and nowhere else, and the client goes through
// internal/netguard. NewDigitalOceanAt exists for tests.

// DigitalOceanEndpoint is DigitalOcean's API.
const DigitalOceanEndpoint = "https://api.digitalocean.com/v2"

// digitalOceanClient is shared: one small request at a time, with a bound on each.
var digitalOceanClient = netguard.Client(30 * time.Second)

// digitalOceanImages are the panel's names for the images it installs, as
// DigitalOcean spells them. The panel's names are the ones SupportedImages
// lists, so the API and the form stay the same whichever provider it is.
var digitalOceanImages = map[string]string{
	"ubuntu-24.04": "ubuntu-24-04-x64",
	"debian-12":    "debian-12-x64",
}

// DigitalOcean is one DigitalOcean account, opened with its token.
type DigitalOcean struct {
	token    string
	endpoint string
	client   *http.Client
}

// NewDigitalOcean opens the real API.
func NewDigitalOcean(token string) *DigitalOcean {
	return &DigitalOcean{token: token, endpoint: DigitalOceanEndpoint, client: digitalOceanClient}
}

// NewDigitalOceanAt opens a DigitalOcean-shaped API somewhere else, for a
// test's fake. A nil client is the guarded one, which refuses loopback.
func NewDigitalOceanAt(token, endpoint string, client *http.Client) *DigitalOcean {
	if client == nil {
		client = digitalOceanClient
	}
	return &DigitalOcean{token: token, endpoint: strings.TrimSuffix(endpoint, "/"), client: client}
}

// Kind implements Provider.
func (d *DigitalOcean) Kind() string { return KindDigitalOcean }

// DigitalOceanError is DigitalOcean's own answer to a request it refused: an
// id such as "unauthorized" or "unprocessable_entity", and a sentence.
type DigitalOceanError struct {
	Status  int
	ID      string
	Message string
}

func (e *DigitalOceanError) Error() string {
	return fmt.Sprintf("DigitalOcean answered %d %s: %s", e.Status, e.ID, e.Message)
}

// Is makes a 404 an ErrNotFound.
func (e *DigitalOceanError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

// problem says what DigitalOcean's refusal means for the person asking. A 403
// is a token whose scopes do not include the write: DigitalOcean's read-only
// tokens and custom scopes without it are both refused that way.
func (e *DigitalOceanError) problem() *errdoc.Problem {
	provider := Title(KindDigitalOcean)
	message := strings.ToLower(e.Message)
	var p *errdoc.Problem
	switch {
	case e.Status == http.StatusUnauthorized:
		p = errdoc.CloudTokenInvalid(provider)
	case e.Status == http.StatusForbidden:
		p = errdoc.CloudTokenReadOnly(provider)
	case e.Status == http.StatusTooManyRequests:
		p = errdoc.CloudRateLimited(provider)
	case e.Status == http.StatusUnprocessableEntity && strings.Contains(message, "limit"):
		p = errdoc.CloudLimitReached(provider, e.Message)
	case e.Status == http.StatusUnprocessableEntity && strings.Contains(message, "not available"):
		p = errdoc.CloudUnavailable(provider, e.Message)
	default:
		p = errdoc.CloudRequestFailed(provider, e.ID, e.Message, e.Status >= 500)
	}
	return p.Wrap(e)
}

// Check implements Provider.
//
// Listing regions proves the token is one DigitalOcean knows. Whether it may
// write is asked with a request that cannot succeed, an SSH key with no name
// and no key: a token without the write is refused with 403 before the body
// is read, and one with it with 422 for the missing fields. Nothing is created
// either way.
func (d *DigitalOcean) Check(ctx context.Context) error {
	if err := d.do(ctx, http.MethodGet, "/regions", url.Values{"per_page": {"1"}}, nil, nil); err != nil {
		return err
	}
	err := d.do(ctx, http.MethodPost, "/account/keys", nil, map[string]any{}, nil)
	var refused *DigitalOceanError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused) && (refused.Status == http.StatusUnprocessableEntity || refused.Status == http.StatusBadRequest):
		return nil
	}
	return err
}

// Catalogue implements Provider, from GET /regions, GET /sizes and
// GET /images?type=distribution.
func (d *DigitalOcean) Catalogue(ctx context.Context) (Catalogue, error) {
	var out Catalogue

	err := d.list(ctx, "/regions", nil, "regions", func(raw json.RawMessage) error {
		var r struct {
			Slug      string `json:"slug"`
			Name      string `json:"name"`
			Available bool   `json:"available"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if !r.Available {
			return nil
		}
		out.Locations = append(out.Locations, Location{
			Name: r.Slug, Description: r.Name, City: regionCity(r.Name), Country: regionCountry(r.Slug),
		})
		return nil
	})
	if err != nil {
		return Catalogue{}, err
	}
	sort.Slice(out.Locations, func(i, j int) bool { return out.Locations[i].Name < out.Locations[j].Name })

	err = d.list(ctx, "/sizes", nil, "sizes", func(raw json.RawMessage) error {
		var s digitalOceanSize
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		// A GPU droplet needs an image of its own and is not a node this
		// panel makes; neither is a size that cannot be ordered anywhere.
		if !s.Available || strings.HasPrefix(s.Slug, "gpu-") || len(s.Regions) == 0 {
			return nil
		}
		out.ServerTypes = append(out.ServerTypes, s.normalise())
		return nil
	})
	if err != nil {
		return Catalogue{}, err
	}
	sort.SliceStable(out.ServerTypes, func(i, j int) bool {
		a, b := out.ServerTypes[i], out.ServerTypes[j]
		if a.Cores != b.Cores {
			return a.Cores < b.Cores
		}
		return a.MemoryGB < b.MemoryGB
	})

	available := map[string]string{}
	err = d.list(ctx, "/images", url.Values{"type": {"distribution"}}, "images", func(raw json.RawMessage) error {
		var i struct {
			Slug         string `json:"slug"`
			Name         string `json:"name"`
			Distribution string `json:"distribution"`
			Status       string `json:"status"`
		}
		if err := json.Unmarshal(raw, &i); err != nil {
			return err
		}
		if i.Status == "" || i.Status == "available" {
			available[i.Slug] = strings.TrimSpace(i.Distribution + " " + i.Name)
		}
		return nil
	})
	if err != nil {
		return Catalogue{}, err
	}
	for _, name := range SupportedImages {
		if description, ok := available[digitalOceanImages[name]]; ok {
			out.Images = append(out.Images, Image{Name: name, Description: description, Arch: ArchAMD64})
		}
	}
	return out, nil
}

type digitalOceanSize struct {
	Slug         string   `json:"slug"`
	Description  string   `json:"description"`
	Memory       int      `json:"memory"`
	VCPUs        int      `json:"vcpus"`
	Disk         int      `json:"disk"`
	PriceMonthly float64  `json:"price_monthly"`
	PriceHourly  float64  `json:"price_hourly"`
	Regions      []string `json:"regions"`
	Available    bool     `json:"available"`
}

func (s digitalOceanSize) normalise() ServerType {
	out := ServerType{
		Name: s.Slug, Description: s.Description, Cores: s.VCPUs,
		MemoryGB: float64(s.Memory) / 1024, DiskGB: s.Disk,
		// Droplets are x86 only.
		Arch: ArchAMD64, CPUType: "shared", Locations: slices.Clone(s.Regions),
	}
	// Everything but the Basic droplets runs on dedicated vCPUs: General
	// Purpose, CPU-, Memory- and Storage-Optimized.
	if !strings.HasPrefix(s.Slug, "s-") {
		out.CPUType = "dedicated"
	}
	sort.Strings(out.Locations)
	// One price everywhere, in US dollars and before tax, which is how
	// DigitalOcean writes it.
	for _, region := range out.Locations {
		out.Prices = append(out.Prices, Price{
			Location: region,
			Monthly:  strconv.FormatFloat(s.PriceMonthly, 'f', 2, 64),
			Hourly:   strconv.FormatFloat(s.PriceHourly, 'f', 5, 64),
			Currency: "USD",
		})
	}
	return out
}

// regionCity is the city of a region named "New York 1": the name without
// the number that tells its data centres apart.
func regionCity(name string) string {
	fields := strings.Fields(name)
	if len(fields) > 1 {
		if _, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			fields = fields[:len(fields)-1]
		}
	}
	return strings.Join(fields, " ")
}

// regionCountry is the country a region's slug is in. DigitalOcean's regions
// carry no country of their own; their slugs are a city's and change rarely.
func regionCountry(slug string) string {
	switch strings.TrimRight(slug, "0123456789") {
	case "nyc", "sfo", "atl":
		return "US"
	case "ams":
		return "NL"
	case "sgp":
		return "SG"
	case "lon":
		return "GB"
	case "fra":
		return "DE"
	case "tor":
		return "CA"
	case "blr":
		return "IN"
	case "syd":
		return "AU"
	}
	return ""
}

// ImportSSHKey implements Provider, with POST /account/keys. DigitalOcean's
// keys carry no labels, so labels are not written.
func (d *DigitalOcean) ImportSSHKey(ctx context.Context, name, publicKey string, _ map[string]string) (SSHKey, error) {
	var created struct {
		SSHKey digitalOceanSSHKey `json:"ssh_key"`
	}
	body := map[string]any{"name": name, "public_key": strings.TrimSpace(publicKey)}
	err := d.do(ctx, http.MethodPost, "/account/keys", nil, body, &created)
	if err == nil {
		return created.SSHKey.key(), nil
	}
	// The same key twice is refused as already in use, and a retry after an
	// answer lost on the way back is exactly that. The existing one is it,
	// found by its MD5 fingerprint.
	var refused *DigitalOceanError
	if !errors.As(err, &refused) || refused.Status != http.StatusUnprocessableEntity {
		return SSHKey{}, err
	}
	fingerprint, fpErr := md5Fingerprint(publicKey)
	if fpErr != nil {
		return SSHKey{}, err
	}
	var found struct {
		SSHKey digitalOceanSSHKey `json:"ssh_key"`
	}
	if lookErr := d.do(ctx, http.MethodGet, "/account/keys/"+url.PathEscape(fingerprint), nil, nil, &found); lookErr != nil {
		if errors.Is(lookErr, ErrNotFound) {
			return SSHKey{}, err
		}
		return SSHKey{}, lookErr
	}
	return found.SSHKey.key(), nil
}

type digitalOceanSSHKey struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (k digitalOceanSSHKey) key() SSHKey {
	return SSHKey{ID: strconv.FormatInt(k.ID, 10), Name: k.Name}
}

// DeleteSSHKey implements Provider, with DELETE /account/keys/{id}.
func (d *DigitalOcean) DeleteSSHKey(ctx context.Context, id string) error {
	return ignoreNotFound(d.do(ctx, http.MethodDelete, "/account/keys/"+url.PathEscape(id), nil, nil, nil))
}

// digitalOceanFirewall is a firewall as GET /firewalls/{id} answers it, and as
// PUT /firewalls/{id} takes it back: the whole object, every time.
type digitalOceanFirewall struct {
	ID            string                     `json:"id,omitempty"`
	Name          string                     `json:"name"`
	InboundRules  []digitalOceanInboundRule  `json:"inbound_rules"`
	OutboundRules []digitalOceanOutboundRule `json:"outbound_rules"`
	DropletIDs    []int64                    `json:"droplet_ids"`
	Tags          []string                   `json:"tags"`
}

type digitalOceanAddresses struct {
	Addresses []string `json:"addresses"`
}

type digitalOceanInboundRule struct {
	Protocol string                `json:"protocol"`
	Ports    string                `json:"ports,omitempty"`
	Sources  digitalOceanAddresses `json:"sources"`
}

type digitalOceanOutboundRule struct {
	Protocol     string                `json:"protocol"`
	Ports        string                `json:"ports,omitempty"`
	Destinations digitalOceanAddresses `json:"destinations"`
}

// EnsureFirewall implements Provider. DigitalOcean's firewalls carry no
// labels, so the one for name is found by its name, with GET /firewalls, and
// created with POST /firewalls when there is none.
func (d *DigitalOcean) EnsureFirewall(ctx context.Context, name string, _ map[string]string, rules []Rule) (string, error) {
	name = digitalOceanName(name)
	var existing string
	err := d.list(ctx, "/firewalls", nil, "firewalls", func(raw json.RawMessage) error {
		var f digitalOceanFirewall
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		if f.Name == name && existing == "" {
			existing = f.ID
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if existing != "" {
		return existing, d.SetFirewallRules(ctx, existing, rules)
	}
	var created struct {
		Firewall digitalOceanFirewall `json:"firewall"`
	}
	body := digitalOceanFirewall{
		Name: name, InboundRules: digitalOceanInbound(rules), OutboundRules: digitalOceanOutbound(),
		DropletIDs: []int64{}, Tags: []string{firewallTag(name)},
	}
	if err := d.do(ctx, http.MethodPost, "/firewalls", nil, body, &created); err != nil {
		return "", err
	}
	return created.Firewall.ID, nil
}

// SetFirewallRules implements Provider: the firewall is read, and written back
// whole with these rules, by PUT /firewalls/{id}.
func (d *DigitalOcean) SetFirewallRules(ctx context.Context, id string, rules []Rule) error {
	firewall, err := d.firewall(ctx, id)
	if err != nil {
		return err
	}
	firewall.ID = ""
	firewall.InboundRules = digitalOceanInbound(rules)
	firewall.OutboundRules = digitalOceanOutbound()
	if firewall.DropletIDs == nil {
		firewall.DropletIDs = []int64{}
	}
	if firewall.Tags == nil {
		firewall.Tags = []string{}
	}
	return d.do(ctx, http.MethodPut, "/firewalls/"+url.PathEscape(id), nil, firewall, nil)
}

func (d *DigitalOcean) firewall(ctx context.Context, id string) (digitalOceanFirewall, error) {
	var found struct {
		Firewall digitalOceanFirewall `json:"firewall"`
	}
	if err := d.do(ctx, http.MethodGet, "/firewalls/"+url.PathEscape(id), nil, nil, &found); err != nil {
		return digitalOceanFirewall{}, err
	}
	return found.Firewall, nil
}

// DeleteFirewall implements Provider, with DELETE /firewalls/{id}.
func (d *DigitalOcean) DeleteFirewall(ctx context.Context, id string) error {
	return ignoreNotFound(d.do(ctx, http.MethodDelete, "/firewalls/"+url.PathEscape(id), nil, nil, nil))
}

// digitalOceanInbound writes rules the way the Firewalls API reads them. A
// TCP or UDP rule with no port is every port; ICMP has none.
func digitalOceanInbound(rules []Rule) []digitalOceanInboundRule {
	out := make([]digitalOceanInboundRule, 0, len(rules))
	for _, rule := range rules {
		if len(rule.Sources) == 0 {
			// A rule with nobody to let in is no rule; DigitalOcean refuses one.
			continue
		}
		entry := digitalOceanInboundRule{Protocol: rule.Protocol, Sources: digitalOceanAddresses{Addresses: rule.Sources}}
		if rule.Protocol != "icmp" {
			entry.Ports = rule.Port
			if entry.Ports == "" {
				entry.Ports = "all"
			}
		}
		out = append(out, entry)
	}
	return out
}

// digitalOceanOutbound lets everything out, which a DigitalOcean firewall
// otherwise does not: a node downloads k3s and pulls images.
func digitalOceanOutbound() []digitalOceanOutboundRule {
	everywhere := digitalOceanAddresses{Addresses: Anywhere}
	return []digitalOceanOutboundRule{
		{Protocol: "tcp", Ports: "all", Destinations: everywhere},
		{Protocol: "udp", Ports: "all", Destinations: everywhere},
		{Protocol: "icmp", Destinations: everywhere},
	}
}

// CreateMachine implements Provider, with POST /droplets.
//
// The keys are ids by the time they reach DigitalOcean, which takes ids or
// fingerprints and not names; a name is looked up. The firewalls are tags:
// the droplet carries each firewall's own, so it is behind them from its
// first boot rather than from whenever it was added to them.
func (d *DigitalOcean) CreateMachine(ctx context.Context, spec MachineSpec) (Machine, error) {
	image, ok := digitalOceanImages[spec.Image]
	if !ok {
		return Machine{}, fmt.Errorf("DigitalOcean has no image the panel calls %q", spec.Image)
	}
	keys, err := d.keyIDs(ctx, spec.SSHKeys)
	if err != nil {
		return Machine{}, err
	}
	tags, err := labelTags(spec.Labels)
	if err != nil {
		return Machine{}, err
	}
	for _, id := range spec.Firewalls {
		firewall, err := d.firewall(ctx, id)
		if err != nil {
			return Machine{}, err
		}
		tags = append(tags, firewall.Tags...)
	}
	body := map[string]any{
		"name": spec.Name, "region": spec.Location, "size": spec.Type, "image": image,
		"ssh_keys": keys, "user_data": spec.UserData, "tags": tags,
		// IPv4 is how the cluster's nodes reach each other today; IPv6 comes
		// with it at no cost.
		"ipv6": true,
	}
	var created struct {
		Droplet digitalOceanDroplet `json:"droplet"`
	}
	if err := d.do(ctx, http.MethodPost, "/droplets", nil, body, &created); err != nil {
		return Machine{}, err
	}
	return created.Droplet.machine(), nil
}

// keyIDs turns key names or ids into ids.
func (d *DigitalOcean) keyIDs(ctx context.Context, keys []string) ([]int64, error) {
	ids := make([]int64, 0, len(keys))
	var byName map[string]int64
	for _, key := range keys {
		if id, err := strconv.ParseInt(key, 10, 64); err == nil {
			ids = append(ids, id)
			continue
		}
		if byName == nil {
			byName = map[string]int64{}
			err := d.list(ctx, "/account/keys", nil, "ssh_keys", func(raw json.RawMessage) error {
				var k digitalOceanSSHKey
				if err := json.Unmarshal(raw, &k); err != nil {
					return err
				}
				byName[k.Name] = k.ID
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		id, ok := byName[key]
		if !ok {
			return nil, fmt.Errorf("DigitalOcean has no SSH key called %q", key)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Machine implements Provider, with GET /droplets/{id}.
func (d *DigitalOcean) Machine(ctx context.Context, id string) (Machine, error) {
	var found struct {
		Droplet digitalOceanDroplet `json:"droplet"`
	}
	if err := d.do(ctx, http.MethodGet, "/droplets/"+url.PathEscape(id), nil, nil, &found); err != nil {
		return Machine{}, err
	}
	return found.Droplet.machine(), nil
}

// FindMachine implements Provider, with GET /droplets?tag_name=, which takes
// one tag: the droplets carrying the first are read, and the one carrying
// every other is it.
func (d *DigitalOcean) FindMachine(ctx context.Context, labels map[string]string) (Machine, bool, error) {
	tags, err := labelTags(labels)
	if err != nil || len(tags) == 0 {
		return Machine{}, false, err
	}
	var match *digitalOceanDroplet
	err = d.list(ctx, "/droplets", url.Values{"tag_name": {tags[0]}}, "droplets", func(raw json.RawMessage) error {
		var droplet digitalOceanDroplet
		if err := json.Unmarshal(raw, &droplet); err != nil {
			return err
		}
		if match == nil && containsAll(droplet.Tags, tags) {
			match = &droplet
		}
		return nil
	})
	if err != nil || match == nil {
		return Machine{}, false, err
	}
	return match.machine(), true, nil
}

func containsAll(have, want []string) bool {
	for _, tag := range want {
		if !slices.Contains(have, tag) {
			return false
		}
	}
	return true
}

// DeleteMachine implements Provider, with DELETE /droplets/{id}. A droplet
// that is already gone is not an error: gone is what was asked for.
func (d *DigitalOcean) DeleteMachine(ctx context.Context, id string) error {
	return ignoreNotFound(d.do(ctx, http.MethodDelete, "/droplets/"+url.PathEscape(id), nil, nil, nil))
}

type digitalOceanDroplet struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Networks struct {
		V4 []digitalOceanNetwork `json:"v4"`
		V6 []digitalOceanNetwork `json:"v6"`
	} `json:"networks"`
	Tags []string `json:"tags"`
}

type digitalOceanNetwork struct {
	IPAddress string `json:"ip_address"`
	Type      string `json:"type"`
}

func (d digitalOceanDroplet) machine() Machine {
	m := Machine{ID: strconv.FormatInt(d.ID, 10), Name: d.Name, Labels: tagLabels(d.Tags)}
	// DigitalOcean says active; the panel's word, Hetzner's, is running.
	switch d.Status {
	case "active":
		m.Status = "running"
	case "new":
		m.Status = "starting"
	default:
		m.Status = d.Status
	}
	for _, network := range d.Networks.V4 {
		if network.Type == "public" && m.IPv4 == "" {
			m.IPv4 = network.IPAddress
		}
	}
	for _, network := range d.Networks.V6 {
		if network.Type == "public" && m.IPv6 == "" {
			m.IPv6 = network.IPAddress
		}
	}
	return m
}

// tagPart is what a label's name or value may be to travel as part of a tag:
// what DigitalOcean allows in one, less the colon that separates them.
var tagPart = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// labelTags writes the panel's labels as tags, skifity:<name>:<value>, in a
// stable order. A label is the panel's own — skifity.com/<name> — or it is
// refused: a tag cannot hold a dot or a slash, and the label could not be read
// back as what it was.
func labelTags(labels map[string]string) ([]string, error) {
	prefix := version.LabelKey("")
	tags := make([]string, 0, len(labels))
	for key, value := range labels {
		name, ours := strings.CutPrefix(key, prefix)
		if !ours || !tagPart.MatchString(name) || !tagPart.MatchString(value) {
			return nil, fmt.Errorf("the label %s=%s cannot be written as a DigitalOcean tag", key, value)
		}
		tags = append(tags, version.Binary+":"+name+":"+value)
	}
	sort.Strings(tags)
	return tags, nil
}

// tagLabels reads labelTags back. Tags that are not of that form — a
// firewall's, or one somebody added by hand — are not labels.
func tagLabels(tags []string) map[string]string {
	labels := map[string]string{}
	for _, tag := range tags {
		rest, ours := strings.CutPrefix(tag, version.Binary+":")
		if !ours {
			continue
		}
		name, value, ok := strings.Cut(rest, ":")
		if ok && tagPart.MatchString(name) && tagPart.MatchString(value) {
			labels[version.LabelKey(name)] = value
		}
	}
	return labels
}

// firewallTag is the tag that puts a droplet behind one firewall, and nothing
// else carries it.
func firewallTag(name string) string {
	return version.Binary + "-firewall:" + strings.ReplaceAll(name, ".", "-")
}

// digitalOceanName is a name a firewall can have: letters, digits, dashes and
// dots, with anything else a dash.
func digitalOceanName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		}
		return '-'
	}, name)
}

// list reads every page of a collection, following links.pages.next:
// https://docs.digitalocean.com/reference/api/digitalocean/#section/Introduction/Links-and-Pagination.
func (d *DigitalOcean) list(ctx context.Context, path string, query url.Values, key string, each func(json.RawMessage) error) error {
	if query == nil {
		query = url.Values{}
	}
	page := 1
	// Two hundred to a page, the most DigitalOcean gives; twenty pages is
	// more than any of these collections has, and a bound on an answer that
	// always says there is a next page.
	for range 20 {
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", "200")
		var body map[string]json.RawMessage
		if err := d.do(ctx, http.MethodGet, path, query, nil, &body); err != nil {
			return err
		}
		var items []json.RawMessage
		if raw, ok := body[key]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return fmt.Errorf("read DigitalOcean's %s: %w", key, err)
			}
		}
		for _, item := range items {
			if err := each(item); err != nil {
				return fmt.Errorf("read DigitalOcean's %s: %w", key, err)
			}
		}
		var links struct {
			Pages struct {
				Next string `json:"next"`
			} `json:"pages"`
		}
		if raw, ok := body["links"]; ok {
			_ = json.Unmarshal(raw, &links)
		}
		number, more := nextPage(links.Pages.Next, page)
		if !more {
			return nil
		}
		page = number
	}
	return nil
}

// nextPage reads the page number out of a links.pages.next address, and says
// whether there is a page after current. Only the number is taken: the
// address is DigitalOcean's, and the request goes to the endpoint regardless.
// A link that cannot be read is the end of the list, not an error, since
// everything before it has been read.
func nextPage(link string, current int) (int, bool) {
	if link == "" {
		return 0, false
	}
	next, err := url.Parse(link)
	if err != nil {
		return 0, false
	}
	number, err := strconv.Atoi(next.Query().Get("page"))
	if err != nil || number <= current {
		return 0, false
	}
	return number, true
}

// do makes one request and reads its answer into out.
func (d *DigitalOcean) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	address := d.endpoint + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request to DigitalOcean: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return fmt.Errorf("build the request to DigitalOcean: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errdoc.CloudUnreachable(Title(KindDigitalOcean), err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return errdoc.CloudUnreachable(Title(KindDigitalOcean), err)
	}

	if resp.StatusCode >= 400 {
		refused := &DigitalOceanError{Status: resp.StatusCode, ID: http.StatusText(resp.StatusCode)}
		var shaped struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		}
		if json.Unmarshal(answer, &shaped) == nil && shaped.ID != "" {
			refused.ID, refused.Message = shaped.ID, shaped.Message
		}
		return refused.problem()
	}
	if out == nil || len(bytes.TrimSpace(answer)) == 0 {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("read DigitalOcean's answer to %s %s: %w", method, path, err)
	}
	return nil
}
