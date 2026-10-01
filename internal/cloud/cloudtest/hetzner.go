// Package cloudtest is a fake Hetzner Cloud API for tests: the shapes of the
// requests and answers at https://docs.hetzner.cloud/reference/cloud, kept in
// memory, on an httptest server. Nothing here is ever compiled into the panel;
// only tests import it.
package cloudtest

import (
	"crypto/md5" //nolint:gosec // Hetzner's own key fingerprint, reproduced
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"skifity/internal/cloud"
)

// Token is the read-write token the fake accepts.
const Token = "hcloud-test-read-write-token-not-real"

// ReadOnlyToken is a token the fake accepts for GET requests only.
const ReadOnlyToken = "hcloud-test-read-only-token-not-real"

// Request is one request the fake answered.
type Request struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

// Server is a machine the fake holds.
type Server struct {
	ID         int64
	Name       string
	Status     string
	ServerType string
	Image      string
	Location   string
	UserData   string
	SSHKeys    []string
	// PublicKeys are the keys SSHKeys name, as the machine gets them on root.
	PublicKeys []string
	Firewalls  []int64
	Labels     map[string]string
	IPv4       string
	reads      int
}

// Rule is one firewall rule, as POST /firewalls takes it.
type Rule struct {
	Direction   string   `json:"direction"`
	Protocol    string   `json:"protocol"`
	Port        string   `json:"port"`
	SourceIPs   []string `json:"source_ips"`
	Description string   `json:"description"`
}

// Firewall is a firewall the fake holds.
type Firewall struct {
	ID     int64
	Name   string
	Labels map[string]string
	Rules  []Rule
}

// SSHKey is a public key the fake holds.
type SSHKey struct {
	ID          int64
	Name        string
	PublicKey   string
	Fingerprint string
	Labels      map[string]string
}

// Hetzner is the fake API.
type Hetzner struct {
	*httptest.Server

	mu        sync.Mutex
	requests  []Request
	servers   map[int64]*Server
	firewalls map[int64]*Firewall
	keys      map[int64]*SSHKey
	nextID    int64

	// BootAfter is how many reads of a new server answer "initializing"
	// before it is "running".
	BootAfter int
	// IPv4 is the address the next server gets.
	IPv4 string
	// OnCreate is called with every server created, before the answer is
	// written, so a test can start the machine the panel will connect to.
	OnCreate func(Server)
	// PageSize is how many items a list answers with per page, so paging is
	// exercised: two, unless a test says otherwise.
	PageSize int
}

// New starts a fake. It stops when the test ends.
func New(t *testing.T) *Hetzner {
	t.Helper()
	f := &Hetzner{
		servers: map[int64]*Server{}, firewalls: map[int64]*Firewall{}, keys: map[int64]*SSHKey{},
		nextID: 100, BootAfter: 1, IPv4: "203.0.113.50", PageSize: 2,
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// Opener opens the fake as the panel would open Hetzner Cloud.
func (f *Hetzner) Opener() cloud.Opener {
	return func(kind, token string) (cloud.Provider, error) {
		if kind != cloud.KindHetzner {
			return nil, fmt.Errorf("no provider %q", kind)
		}
		return cloud.NewHetznerAt(token, f.URL, f.Client()), nil
	}
}

// Requests returns every request answered, in order.
func (f *Hetzner) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Servers returns the machines that exist.
func (f *Hetzner) Servers() []Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Server{}
	for _, s := range f.servers {
		out = append(out, *s)
	}
	return out
}

// Firewalls returns the firewalls that exist.
func (f *Hetzner) Firewalls() []Firewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Firewall{}
	for _, fw := range f.firewalls {
		out = append(out, *fw)
	}
	return out
}

// Keys returns the SSH keys that exist.
func (f *Hetzner) Keys() []SSHKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []SSHKey{}
	for _, k := range f.keys {
		out = append(out, *k)
	}
	return out
}

// AddServer puts a machine in the project that the panel did not create.
func (f *Hetzner) AddServer(s Server) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	s.ID = f.nextID
	if s.Status == "" {
		s.Status = "running"
	}
	f.servers[s.ID] = &s
	return s.ID
}

// SetLabels replaces a machine's labels, as somebody in the console could.
func (f *Hetzner) SetLabels(id int64, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.servers[id]; ok {
		s.Labels = labels
	}
}

func (f *Hetzner) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			fail(w, http.StatusBadRequest, "json_error", "invalid JSON")
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})

	// https://docs.hetzner.cloud/reference/cloud#authentication, and the
	// 401 codes under #errors.
	switch r.Header.Get("Authorization") {
	case "Bearer " + Token:
	case "Bearer " + ReadOnlyToken:
		if r.Method != http.MethodGet {
			fail(w, http.StatusUnauthorized, "token_readonly", "The token is only allowed to perform GET requests.")
			return
		}
	default:
		fail(w, http.StatusUnauthorized, "unauthorized", "unable to authenticate")
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/locations":
		f.page(w, r, "locations", locations())
	case r.Method == http.MethodGet && r.URL.Path == "/server_types":
		f.page(w, r, "server_types", serverTypes())
	case r.Method == http.MethodGet && r.URL.Path == "/images":
		f.page(w, r, "images", images(r.URL.Query().Get("type")))

	// https://docs.hetzner.cloud/reference/cloud#tag/ssh-keys
	case r.Method == http.MethodPost && r.URL.Path == "/ssh_keys":
		f.createKey(w, body)
	case r.Method == http.MethodGet && r.URL.Path == "/ssh_keys":
		out := []any{}
		for _, k := range f.keys {
			if fp := r.URL.Query().Get("fingerprint"); fp == "" || fp == k.Fingerprint {
				out = append(out, keyJSON(k))
			}
		}
		f.page(w, r, "ssh_keys", out)
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "ssh_keys":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if _, ok := f.keys[id]; !ok {
			fail(w, http.StatusNotFound, "not_found", "ssh key not found")
			return
		}
		delete(f.keys, id)
		w.WriteHeader(http.StatusNoContent)

	// https://docs.hetzner.cloud/reference/cloud#tag/firewalls
	case r.Method == http.MethodGet && r.URL.Path == "/firewalls":
		out := []any{}
		for _, fw := range f.firewalls {
			if matches(fw.Labels, r.URL.Query().Get("label_selector")) {
				out = append(out, map[string]any{"id": fw.ID, "name": fw.Name, "labels": fw.Labels})
			}
		}
		f.page(w, r, "firewalls", out)
	case r.Method == http.MethodPost && r.URL.Path == "/firewalls":
		f.createFirewall(w, body)
	case r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "firewalls" && parts[3] == "set_rules":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		fw, ok := f.firewalls[id]
		if !ok {
			fail(w, http.StatusNotFound, "not_found", "firewall not found")
			return
		}
		rules, problem := readRules(body["rules"])
		if problem != "" {
			fail(w, http.StatusUnprocessableEntity, "invalid_input", problem)
			return
		}
		fw.Rules = rules
		write(w, http.StatusCreated, map[string]any{"actions": []any{map[string]any{"id": f.id(), "status": "running"}}})
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "firewalls":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if _, ok := f.firewalls[id]; !ok {
			fail(w, http.StatusNotFound, "not_found", "firewall not found")
			return
		}
		for _, s := range f.servers {
			for _, applied := range s.Firewalls {
				if applied == id {
					fail(w, http.StatusUnprocessableEntity, "resource_in_use", "firewall is still applied")
					return
				}
			}
		}
		delete(f.firewalls, id)
		w.WriteHeader(http.StatusNoContent)

	// https://docs.hetzner.cloud/reference/cloud#tag/servers
	case r.Method == http.MethodPost && r.URL.Path == "/servers":
		f.createServer(w, body)
	case r.Method == http.MethodGet && r.URL.Path == "/servers":
		out := []any{}
		for _, s := range f.servers {
			if matches(s.Labels, r.URL.Query().Get("label_selector")) {
				out = append(out, serverJSON(s))
			}
		}
		f.page(w, r, "servers", out)
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "servers":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		s, ok := f.servers[id]
		if !ok {
			fail(w, http.StatusNotFound, "not_found", "server not found")
			return
		}
		s.reads++
		if s.Status == "initializing" && s.reads > f.BootAfter {
			s.Status = "running"
		}
		write(w, http.StatusOK, map[string]any{"server": serverJSON(s)})
	case r.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "servers":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if _, ok := f.servers[id]; !ok {
			fail(w, http.StatusNotFound, "not_found", "server not found")
			return
		}
		delete(f.servers, id)
		write(w, http.StatusOK, map[string]any{"action": map[string]any{"id": f.id(), "command": "delete_server", "status": "running"}})
	default:
		fail(w, http.StatusNotFound, "not_found", "no such endpoint in the fake: "+r.Method+" "+r.URL.Path)
	}
}

func (f *Hetzner) id() int64 {
	f.nextID++
	return f.nextID
}

func (f *Hetzner) createKey(w http.ResponseWriter, body map[string]any) {
	name, _ := body["name"].(string)
	public, _ := body["public_key"].(string)
	if name == "" || public == "" {
		fail(w, http.StatusUnprocessableEntity, "invalid_input", "invalid input in fields 'name', 'public_key'")
		return
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(public))
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'public_key'")
		return
	}
	sum := md5.Sum(key.Marshal()) //nolint:gosec // reproducing Hetzner's fingerprint
	hexed := make([]string, len(sum))
	for i, b := range sum {
		hexed[i] = fmt.Sprintf("%02x", b)
	}
	fingerprint := strings.Join(hexed, ":")
	for _, existing := range f.keys {
		if existing.Fingerprint == fingerprint || existing.Name == name {
			fail(w, http.StatusConflict, "uniqueness_error", "SSH key with the same fingerprint already exists")
			return
		}
	}
	k := &SSHKey{ID: f.id(), Name: name, PublicKey: public, Fingerprint: fingerprint, Labels: labelsOf(body["labels"])}
	f.keys[k.ID] = k
	write(w, http.StatusCreated, map[string]any{"ssh_key": keyJSON(k)})
}

func (f *Hetzner) createFirewall(w http.ResponseWriter, body map[string]any) {
	name, _ := body["name"].(string)
	if name == "" {
		fail(w, http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'name'")
		return
	}
	for _, existing := range f.firewalls {
		if existing.Name == name {
			fail(w, http.StatusConflict, "uniqueness_error", "firewall name is already used")
			return
		}
	}
	rules, problem := readRules(body["rules"])
	if problem != "" {
		fail(w, http.StatusUnprocessableEntity, "invalid_input", problem)
		return
	}
	fw := &Firewall{ID: f.id(), Name: name, Labels: labelsOf(body["labels"]), Rules: rules}
	f.firewalls[fw.ID] = fw
	write(w, http.StatusCreated, map[string]any{
		"firewall": map[string]any{"id": fw.ID, "name": fw.Name, "labels": fw.Labels},
		"actions":  []any{},
	})
}

func (f *Hetzner) createServer(w http.ResponseWriter, body map[string]any) {
	name, _ := body["name"].(string)
	serverType, _ := body["server_type"].(string)
	image, _ := body["image"].(string)
	if name == "" || serverType == "" || image == "" {
		fail(w, http.StatusUnprocessableEntity, "invalid_input", "name, server_type and image are required")
		return
	}
	for _, existing := range f.servers {
		if existing.Name == name {
			fail(w, http.StatusConflict, "uniqueness_error", "server name is already used")
			return
		}
	}
	s := &Server{
		ID: f.id(), Name: name, Status: "initializing", ServerType: serverType, Image: image,
		Labels: labelsOf(body["labels"]), IPv4: f.IPv4,
	}
	s.Location, _ = body["location"].(string)
	s.UserData, _ = body["user_data"].(string)
	if keys, ok := body["ssh_keys"].([]any); ok {
		for _, k := range keys {
			named := fmt.Sprint(k)
			s.SSHKeys = append(s.SSHKeys, named)
			found := false
			for _, key := range f.keys {
				if key.Name == named || strconv.FormatInt(key.ID, 10) == named {
					s.PublicKeys = append(s.PublicKeys, key.PublicKey)
					found = true
				}
			}
			if !found {
				fail(w, http.StatusNotFound, "not_found", "ssh key "+named+" not found")
				return
			}
		}
	}
	if firewalls, ok := body["firewalls"].([]any); ok {
		for _, entry := range firewalls {
			if m, ok := entry.(map[string]any); ok {
				if id, ok := m["firewall"].(float64); ok {
					s.Firewalls = append(s.Firewalls, int64(id))
				}
			}
		}
	}
	f.servers[s.ID] = s
	if f.OnCreate != nil {
		f.OnCreate(*s)
	}
	answer := map[string]any{
		"server": serverJSON(s),
		"action": map[string]any{"id": f.id(), "command": "create_server", "status": "running"},
	}
	if len(s.SSHKeys) == 0 {
		// What Hetzner does when no key is given, and what the panel must
		// never need.
		answer["root_password"] = "not-a-real-password"
	}
	write(w, http.StatusCreated, answer)
}

// page answers a list the way https://docs.hetzner.cloud/reference/cloud#pagination
// describes, PageSize items at a time.
func (f *Hetzner) page(w http.ResponseWriter, r *http.Request, key string, items []any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size := f.PageSize
	if size < 1 {
		size = 25
	}
	start := min((page-1)*size, len(items))
	end := min(start+size, len(items))
	var next any
	if end < len(items) {
		next = page + 1
	}
	write(w, http.StatusOK, map[string]any{
		key: items[start:end],
		"meta": map[string]any{"pagination": map[string]any{
			"page": page, "per_page": size, "next_page": next, "total_entries": len(items),
		}},
	})
}

func serverJSON(s *Server) map[string]any {
	return map[string]any{
		"id": s.ID, "name": s.Name, "status": s.Status, "labels": s.Labels,
		"public_net": map[string]any{
			"ipv4": map[string]any{"ip": s.IPv4, "blocked": false, "dns_ptr": ""},
			"ipv6": map[string]any{"ip": "2001:db8::/64", "blocked": false},
		},
		"server_type": map[string]any{"name": s.ServerType},
		"location":    map[string]any{"name": s.Location},
	}
}

func keyJSON(k *SSHKey) map[string]any {
	return map[string]any{"id": k.ID, "name": k.Name, "fingerprint": k.Fingerprint, "public_key": k.PublicKey, "labels": k.Labels}
}

func readRules(raw any) ([]Rule, string) {
	encoded, _ := json.Marshal(raw)
	var rules []Rule
	if err := json.Unmarshal(encoded, &rules); err != nil {
		return nil, "invalid input in field 'rules'"
	}
	for _, rule := range rules {
		if rule.Direction != "in" && rule.Direction != "out" {
			return nil, "invalid input in field 'direction'"
		}
		if (rule.Protocol == "tcp" || rule.Protocol == "udp") && rule.Port == "" {
			return nil, "port is required for tcp and udp"
		}
		if rule.Direction == "in" && len(rule.SourceIPs) == 0 {
			return nil, "source_ips is required for inbound rules"
		}
	}
	return rules, ""
}

func labelsOf(raw any) map[string]string {
	out := map[string]string{}
	if m, ok := raw.(map[string]any); ok {
		for k, v := range m {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

// matches applies a label selector of k==v and k=v terms:
// https://docs.hetzner.cloud/reference/cloud#label-selector.
func matches(labels map[string]string, selector string) bool {
	if selector == "" {
		return true
	}
	for _, term := range strings.Split(selector, ",") {
		key, value, ok := strings.Cut(term, "==")
		if !ok {
			key, value, _ = strings.Cut(term, "=")
		}
		if labels[key] != value {
			return false
		}
	}
	return true
}

func fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func write(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func locations() []any {
	return []any{
		map[string]any{"id": 1, "name": "fsn1", "description": "Falkenstein DC Park 1", "city": "Falkenstein", "country": "DE", "network_zone": "eu-central"},
		map[string]any{"id": 3, "name": "hel1", "description": "Helsinki DC Park 1", "city": "Helsinki", "country": "FI", "network_zone": "eu-central"},
		map[string]any{"id": 4, "name": "ash", "description": "Ashburn, VA", "city": "Ashburn, VA", "country": "US", "network_zone": "us-east"},
	}
}

func price(location, hourly, monthly string) map[string]any {
	return map[string]any{
		"location":      location,
		"price_hourly":  map[string]any{"net": hourly, "gross": hourly},
		"price_monthly": map[string]any{"net": monthly, "gross": monthly},
	}
}

func serverTypes() []any {
	available := func(names ...string) []any {
		out := []any{}
		for i, name := range names {
			out = append(out, map[string]any{"id": i + 1, "name": name, "deprecation": nil, "available": true, "recommended": i == 0})
		}
		return out
	}
	return []any{
		map[string]any{
			"id": 22, "name": "cx22", "description": "CX22", "cores": 2, "memory": 4.0, "disk": 40,
			"cpu_type": "shared", "architecture": "x86", "deprecated": false, "deprecation": nil,
			"prices":    []any{price("fsn1", "0.0071", "4.5100000000"), price("hel1", "0.0071", "4.5100000000")},
			"locations": available("fsn1", "hel1"),
		},
		map[string]any{
			"id": 45, "name": "cax11", "description": "CAX11", "cores": 2, "memory": 4.0, "disk": 40,
			"cpu_type": "shared", "architecture": "arm", "deprecated": false, "deprecation": nil,
			"prices":    []any{price("fsn1", "0.0063", "4.5100000000")},
			"locations": available("fsn1"),
		},
		map[string]any{
			"id": 1, "name": "cx11", "description": "CX11", "cores": 1, "memory": 2.0, "disk": 20,
			"cpu_type": "shared", "architecture": "x86", "deprecated": true,
			"deprecation": map[string]any{"announced": "2024-06-06T00:00:00Z", "unavailable_after": "2024-09-06T00:00:00Z"},
			"prices":      []any{price("fsn1", "0.0060", "3.9500000000")},
			"locations":   available("fsn1"),
		},
		map[string]any{
			"id": 96, "name": "ccx13", "description": "CCX13", "cores": 2, "memory": 8.0, "disk": 80,
			"cpu_type": "dedicated", "architecture": "x86", "deprecated": false, "deprecation": nil,
			"prices":    []any{price("ash", "0.0250", "15.5900000000")},
			"locations": available("ash"),
		},
	}
}

func images(kind string) []any {
	if kind != "" && kind != "system" {
		return []any{}
	}
	image := func(id int, name, arch string) map[string]any {
		return map[string]any{
			"id": id, "type": "system", "status": "available", "name": name, "description": strings.ToUpper(name[:1]) + name[1:],
			"architecture": arch, "deprecation": nil, "os_flavor": strings.Split(name, "-")[0],
		}
	}
	return []any{
		image(161547269, "ubuntu-24.04", "x86"),
		image(161547270, "ubuntu-24.04", "arm"),
		image(114690387, "debian-12", "x86"),
		image(114690389, "debian-12", "arm"),
		image(40093140, "fedora-40", "x86"),
	}
}
