package cloud

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Hetzner names an SSH key by its MD5 fingerprint; nothing is protected by it
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"skifity/internal/errdoc"
	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Hetzner Cloud, through its public API: https://docs.hetzner.cloud/reference/cloud.
//
// Every request carries the project's token and nothing else of the panel's.
// The address is a constant. There is no setting, no environment variable and
// no field in any request that changes it: a token is a secret, and an address
// a person could type would be a way to send it — and the panel's own requests
// — anywhere. NewHetznerAt exists for tests, which point it at a fake on
// loopback with a client of their own; the client every other caller gets goes
// through internal/netguard, so even that could not reach the panel's machine
// or the cloud's metadata service.

// HetznerEndpoint is Hetzner Cloud's API.
const HetznerEndpoint = "https://api.hetzner.cloud/v1"

// hetznerClient is shared: one small request at a time, with a bound on each.
var hetznerClient = netguard.Client(30 * time.Second)

// SupportedImages are the images a machine is created from, in the order the
// form offers them: the distributions the preflight check calls supported
// (internal/provision/preflight.go), at the releases tested.
var SupportedImages = []string{"ubuntu-24.04", "debian-12"}

// DefaultImage is the one offered first.
const DefaultImage = "ubuntu-24.04"

// Hetzner is one Hetzner Cloud project, opened with its token.
type Hetzner struct {
	token    string
	endpoint string
	client   *http.Client
}

// NewHetzner opens the real API.
func NewHetzner(token string) *Hetzner {
	return &Hetzner{token: token, endpoint: HetznerEndpoint, client: hetznerClient}
}

// NewHetznerAt opens a Hetzner-shaped API somewhere else, for a test's fake.
// A nil client is the guarded one, which refuses loopback: a fake has to
// bring a client of its own, on purpose.
func NewHetznerAt(token, endpoint string, client *http.Client) *Hetzner {
	if client == nil {
		client = hetznerClient
	}
	return &Hetzner{token: token, endpoint: strings.TrimSuffix(endpoint, "/"), client: client}
}

// Kind implements Provider.
func (h *Hetzner) Kind() string { return KindHetzner }

// APIError is Hetzner's own answer to a request it refused. Its code is the
// machine-readable one from https://docs.hetzner.cloud/reference/cloud#errors.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Hetzner Cloud answered %d %s: %s", e.Status, e.Code, e.Message)
}

// Is makes a 404 an ErrNotFound.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

// problem says what Hetzner's refusal means for the person asking.
func (e *APIError) problem() *errdoc.Problem {
	provider := Title(KindHetzner)
	var p *errdoc.Problem
	switch {
	case e.Status == http.StatusUnauthorized && e.Code == "token_readonly":
		p = errdoc.CloudTokenReadOnly(provider)
	case e.Status == http.StatusUnauthorized:
		p = errdoc.CloudTokenInvalid(provider)
	case e.Code == "resource_limit_exceeded":
		p = errdoc.CloudLimitReached(provider, e.Message)
	case e.Code == "resource_unavailable":
		p = errdoc.CloudUnavailable(provider, e.Message)
	case e.Status == http.StatusTooManyRequests:
		p = errdoc.CloudRateLimited(provider)
	default:
		p = errdoc.CloudRequestFailed(provider, e.Code, e.Message, e.Status >= 500)
	}
	return p.Wrap(e)
}

// Check implements Provider.
//
// Two requests. Listing locations proves the token is one Hetzner knows.
// Whether it may write is asked with a request that cannot succeed: an SSH
// key with no name and no key. Hetzner answers a token that can only read
// with 401 token_readonly before it looks at the body, and one that can write
// with 422 invalid_input — so a read-only token is found now, and nothing is
// created either way.
func (h *Hetzner) Check(ctx context.Context) error {
	if err := h.do(ctx, http.MethodGet, "/locations", url.Values{"per_page": {"1"}}, nil, nil); err != nil {
		return err
	}
	err := h.do(ctx, http.MethodPost, "/ssh_keys", nil, map[string]any{}, nil)
	var refused *APIError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused) && (refused.Code == "invalid_input" || refused.Code == "json_error"):
		return nil
	}
	return err
}

// Catalogue implements Provider, from GET /locations, GET /server_types and
// GET /images.
func (h *Hetzner) Catalogue(ctx context.Context) (Catalogue, error) {
	var out Catalogue

	err := h.list(ctx, "/locations", nil, "locations", func(raw json.RawMessage) error {
		var l struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			City        string `json:"city"`
			Country     string `json:"country"`
		}
		if err := json.Unmarshal(raw, &l); err != nil {
			return err
		}
		out.Locations = append(out.Locations, Location(l))
		return nil
	})
	if err != nil {
		return Catalogue{}, err
	}
	sort.Slice(out.Locations, func(i, j int) bool { return out.Locations[i].Name < out.Locations[j].Name })

	err = h.list(ctx, "/server_types", nil, "server_types", func(raw json.RawMessage) error {
		var t hetznerServerType
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		if t.Deprecated || isSet(t.Deprecation) {
			return nil
		}
		out.ServerTypes = append(out.ServerTypes, t.normalise())
		return nil
	})
	if err != nil {
		return Catalogue{}, err
	}

	query := url.Values{"type": {"system"}, "status": {"available"}}
	err = h.list(ctx, "/images", query, "images", func(raw json.RawMessage) error {
		var i struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Arch        string          `json:"architecture"`
			Deprecation json.RawMessage `json:"deprecation"`
		}
		if err := json.Unmarshal(raw, &i); err != nil {
			return err
		}
		if !slices.Contains(SupportedImages, i.Name) || isSet(i.Deprecation) {
			return nil
		}
		out.Images = append(out.Images, Image{Name: i.Name, Description: i.Description, Arch: hetznerArch(i.Arch)})
		return nil
	})
	if err != nil {
		return Catalogue{}, err
	}
	sort.SliceStable(out.Images, func(i, j int) bool {
		a, b := slices.Index(SupportedImages, out.Images[i].Name), slices.Index(SupportedImages, out.Images[j].Name)
		if a != b {
			return a < b
		}
		return out.Images[i].Arch < out.Images[j].Arch
	})
	return out, nil
}

type hetznerServerType struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Cores       int             `json:"cores"`
	Memory      float64         `json:"memory"`
	Disk        int             `json:"disk"`
	CPUType     string          `json:"cpu_type"`
	Arch        string          `json:"architecture"`
	Deprecated  bool            `json:"deprecated"`
	Deprecation json.RawMessage `json:"deprecation"`
	Prices      []struct {
		Location     string `json:"location"`
		PriceHourly  amount `json:"price_hourly"`
		PriceMonthly amount `json:"price_monthly"`
	} `json:"prices"`
	Locations []struct {
		Name        string          `json:"name"`
		Available   *bool           `json:"available"`
		Deprecation json.RawMessage `json:"deprecation"`
	} `json:"locations"`
}

type amount struct {
	Gross string `json:"gross"`
}

func (t hetznerServerType) normalise() ServerType {
	out := ServerType{
		Name: t.Name, Description: t.Description, Cores: t.Cores, MemoryGB: t.Memory,
		DiskGB: t.Disk, Arch: hetznerArch(t.Arch), CPUType: t.CPUType,
	}
	for _, price := range t.Prices {
		// Hetzner Cloud prices in euros, VAT included in gross.
		out.Prices = append(out.Prices, Price{
			Location: price.Location, Monthly: price.PriceMonthly.Gross,
			Hourly: price.PriceHourly.Gross, Currency: "EUR",
		})
	}
	// Where it can be ordered. The per-location list is newer than the
	// prices; an answer without it is one where every priced location is.
	if len(t.Locations) > 0 {
		for _, l := range t.Locations {
			if (l.Available == nil || *l.Available) && !isSet(l.Deprecation) {
				out.Locations = append(out.Locations, l.Name)
			}
		}
	} else {
		for _, price := range t.Prices {
			out.Locations = append(out.Locations, price.Location)
		}
	}
	return out
}

func hetznerArch(arch string) string {
	switch arch {
	case "arm":
		return ArchARM64
	case "x86":
		return ArchAMD64
	}
	return arch
}

func isSet(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

// ImportSSHKey implements Provider, with POST /ssh_keys.
func (h *Hetzner) ImportSSHKey(ctx context.Context, name, publicKey string, labels map[string]string) (SSHKey, error) {
	var created struct {
		SSHKey hetznerSSHKey `json:"ssh_key"`
	}
	body := map[string]any{"name": name, "public_key": strings.TrimSpace(publicKey), "labels": labels}
	err := h.do(ctx, http.MethodPost, "/ssh_keys", nil, body, &created)
	if err == nil {
		return created.SSHKey.key(), nil
	}
	// The same key twice is a uniqueness error, and a retry after an answer
	// that was lost on the way back is exactly that. The existing one is it.
	var refused *APIError
	if !errors.As(err, &refused) || refused.Code != "uniqueness_error" {
		return SSHKey{}, err
	}
	fingerprint, fpErr := md5Fingerprint(publicKey)
	if fpErr != nil {
		return SSHKey{}, err
	}
	var found struct {
		SSHKeys []hetznerSSHKey `json:"ssh_keys"`
	}
	if lookErr := h.do(ctx, http.MethodGet, "/ssh_keys", url.Values{"fingerprint": {fingerprint}}, nil, &found); lookErr != nil {
		return SSHKey{}, lookErr
	}
	if len(found.SSHKeys) == 0 {
		return SSHKey{}, err
	}
	return found.SSHKeys[0].key(), nil
}

type hetznerSSHKey struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (k hetznerSSHKey) key() SSHKey { return SSHKey{ID: strconv.FormatInt(k.ID, 10), Name: k.Name} }

// md5Fingerprint is the fingerprint Hetzner filters keys by: MD5 of the
// public key's wire form, in colon-separated hex.
func md5Fingerprint(publicKey string) (string, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return "", err
	}
	sum := md5.Sum(key.Marshal()) //nolint:gosec // an identifier, not a protection
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":"), nil
}

// DeleteSSHKey implements Provider, with DELETE /ssh_keys/{id}.
func (h *Hetzner) DeleteSSHKey(ctx context.Context, id string) error {
	return ignoreNotFound(h.do(ctx, http.MethodDelete, "/ssh_keys/"+url.PathEscape(id), nil, nil, nil))
}

// EnsureFirewall implements Provider, with GET /firewalls?label_selector=,
// POST /firewalls and POST /firewalls/{id}/actions/set_rules.
func (h *Hetzner) EnsureFirewall(ctx context.Context, name string, labels map[string]string, rules []Rule) (string, error) {
	var found struct {
		Firewalls []struct {
			ID int64 `json:"id"`
		} `json:"firewalls"`
	}
	if err := h.do(ctx, http.MethodGet, "/firewalls", url.Values{"label_selector": {LabelSelector(labels)}}, nil, &found); err != nil {
		return "", err
	}
	if len(found.Firewalls) > 0 {
		id := strconv.FormatInt(found.Firewalls[0].ID, 10)
		return id, h.SetFirewallRules(ctx, id, rules)
	}
	var created struct {
		Firewall struct {
			ID int64 `json:"id"`
		} `json:"firewall"`
	}
	body := map[string]any{"name": name, "labels": labels, "rules": hetznerRules(rules)}
	if err := h.do(ctx, http.MethodPost, "/firewalls", nil, body, &created); err != nil {
		return "", err
	}
	return strconv.FormatInt(created.Firewall.ID, 10), nil
}

// SetFirewallRules implements Provider, with POST /firewalls/{id}/actions/set_rules.
func (h *Hetzner) SetFirewallRules(ctx context.Context, id string, rules []Rule) error {
	return h.do(ctx, http.MethodPost, "/firewalls/"+url.PathEscape(id)+"/actions/set_rules", nil,
		map[string]any{"rules": hetznerRules(rules)}, nil)
}

// DeleteFirewall implements Provider, with DELETE /firewalls/{id}.
func (h *Hetzner) DeleteFirewall(ctx context.Context, id string) error {
	return ignoreNotFound(h.do(ctx, http.MethodDelete, "/firewalls/"+url.PathEscape(id), nil, nil, nil))
}

// hetznerRules writes rules the way the Firewall API reads them: all inbound.
func hetznerRules(rules []Rule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		if len(rule.Sources) == 0 {
			// A rule with nobody to let in is no rule; Hetzner refuses one.
			continue
		}
		entry := map[string]any{
			"direction": "in", "protocol": rule.Protocol,
			"source_ips": rule.Sources, "description": rule.Description,
		}
		if rule.Port != "" {
			entry["port"] = rule.Port
		}
		out = append(out, entry)
	}
	return out
}

// CreateMachine implements Provider, with POST /servers.
//
// The answer's root_password is never read: it is only set when no SSH key
// was given, and a key always is — which is also why Hetzner sends nobody a
// password for the machine.
func (h *Hetzner) CreateMachine(ctx context.Context, spec MachineSpec) (Machine, error) {
	firewalls := make([]map[string]int64, 0, len(spec.Firewalls))
	for _, id := range spec.Firewalls {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return Machine{}, fmt.Errorf("the firewall id %q is not a number", id)
		}
		firewalls = append(firewalls, map[string]int64{"firewall": n})
	}
	body := map[string]any{
		"name": spec.Name, "server_type": spec.Type, "image": spec.Image, "location": spec.Location,
		"ssh_keys": spec.SSHKeys, "user_data": spec.UserData, "labels": spec.Labels,
		"firewalls": firewalls, "start_after_create": true,
		// IPv4 is how the cluster's nodes reach each other today; IPv6 comes
		// with it at no cost.
		"public_net": map[string]bool{"enable_ipv4": true, "enable_ipv6": true},
	}
	var created struct {
		Server hetznerServer `json:"server"`
	}
	if err := h.do(ctx, http.MethodPost, "/servers", nil, body, &created); err != nil {
		return Machine{}, err
	}
	return created.Server.machine(), nil
}

// Machine implements Provider, with GET /servers/{id}.
func (h *Hetzner) Machine(ctx context.Context, id string) (Machine, error) {
	var found struct {
		Server hetznerServer `json:"server"`
	}
	if err := h.do(ctx, http.MethodGet, "/servers/"+url.PathEscape(id), nil, nil, &found); err != nil {
		return Machine{}, err
	}
	return found.Server.machine(), nil
}

// FindMachine implements Provider, with GET /servers?label_selector=.
func (h *Hetzner) FindMachine(ctx context.Context, labels map[string]string) (Machine, bool, error) {
	var found struct {
		Servers []hetznerServer `json:"servers"`
	}
	if err := h.do(ctx, http.MethodGet, "/servers", url.Values{"label_selector": {LabelSelector(labels)}}, nil, &found); err != nil {
		return Machine{}, false, err
	}
	if len(found.Servers) == 0 {
		return Machine{}, false, nil
	}
	return found.Servers[0].machine(), true, nil
}

// DeleteMachine implements Provider, with DELETE /servers/{id}. A machine
// that is already gone is not an error: gone is what was asked for.
func (h *Hetzner) DeleteMachine(ctx context.Context, id string) error {
	return ignoreNotFound(h.do(ctx, http.MethodDelete, "/servers/"+url.PathEscape(id), nil, nil, nil))
}

type hetznerServer struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	PublicNet struct {
		IPv4 *struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
		IPv6 *struct {
			IP string `json:"ip"`
		} `json:"ipv6"`
	} `json:"public_net"`
	Labels map[string]string `json:"labels"`
}

func (s hetznerServer) machine() Machine {
	m := Machine{ID: strconv.FormatInt(s.ID, 10), Name: s.Name, Status: s.Status, Labels: s.Labels}
	if s.PublicNet.IPv4 != nil {
		m.IPv4 = s.PublicNet.IPv4.IP
	}
	if s.PublicNet.IPv6 != nil {
		m.IPv6 = s.PublicNet.IPv6.IP
	}
	return m
}

// LabelSelector writes labels as the selector that matches all of them, in a
// stable order: https://docs.hetzner.cloud/reference/cloud#label-selector.
func LabelSelector(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"=="+labels[key])
	}
	return strings.Join(parts, ",")
}

func ignoreNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// list reads every page of a collection: https://docs.hetzner.cloud/reference/cloud#pagination.
func (h *Hetzner) list(ctx context.Context, path string, query url.Values, key string, each func(json.RawMessage) error) error {
	if query == nil {
		query = url.Values{}
	}
	page := 1
	// Fifty to a page; twenty pages is more than any of these collections
	// has, and a bound on an answer that always says there is a next page.
	for range 20 {
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", "50")
		var body map[string]json.RawMessage
		if err := h.do(ctx, http.MethodGet, path, query, nil, &body); err != nil {
			return err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(body[key], &items); err != nil {
			return fmt.Errorf("read Hetzner Cloud's %s: %w", key, err)
		}
		for _, item := range items {
			if err := each(item); err != nil {
				return fmt.Errorf("read Hetzner Cloud's %s: %w", key, err)
			}
		}
		var meta struct {
			Pagination struct {
				NextPage *int `json:"next_page"`
			} `json:"pagination"`
		}
		if raw, ok := body["meta"]; ok {
			_ = json.Unmarshal(raw, &meta)
		}
		if meta.Pagination.NextPage == nil || *meta.Pagination.NextPage <= page {
			return nil
		}
		page = *meta.Pagination.NextPage
	}
	return nil
}

// maxAnswer bounds what is read of one answer.
const maxAnswer = 8 << 20

// do makes one request and reads its answer into out.
func (h *Hetzner) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	address := h.endpoint + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request to Hetzner Cloud: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return fmt.Errorf("build the request to Hetzner Cloud: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errdoc.CloudUnreachable(Title(KindHetzner), err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return errdoc.CloudUnreachable(Title(KindHetzner), err)
	}

	if resp.StatusCode >= 400 {
		refused := &APIError{Status: resp.StatusCode, Code: http.StatusText(resp.StatusCode)}
		var shaped struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(answer, &shaped) == nil && shaped.Error.Code != "" {
			refused.Code, refused.Message = shaped.Error.Code, shaped.Error.Message
		}
		return refused.problem()
	}
	if out == nil || len(bytes.TrimSpace(answer)) == 0 {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("read Hetzner Cloud's answer to %s %s: %w", method, path, err)
	}
	return nil
}
