package cloudtest

import (
	"crypto/md5" //nolint:gosec // DigitalOcean's own key fingerprint, reproduced
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"skifity/internal/cloud"
)

// A fake DigitalOcean API, in the shapes
// https://docs.digitalocean.com/reference/api/digitalocean/ documents: the
// same tokens as the Hetzner fake, errors as {"id", "message"}, collections
// paged with links.pages.next, and a firewall that reaches a droplet through
// a tag.

// Droplet is a droplet the fake holds.
type Droplet struct {
	ID       int64
	Name     string
	Status   string
	Size     string
	Image    string
	Region   string
	UserData string
	SSHKeys  []int64
	// PublicKeys are the keys SSHKeys name, as the droplet gets them on root.
	PublicKeys []string
	Tags       []string
	IPv4       string
	IPv6       bool
	reads      int
}

// DOFirewall is a firewall the fake holds, as PUT /firewalls/{id} writes it.
type DOFirewall struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	InboundRules  []map[string]any `json:"inbound_rules"`
	OutboundRules []map[string]any `json:"outbound_rules"`
	DropletIDs    []int64          `json:"droplet_ids"`
	Tags          []string         `json:"tags"`
}

// DigitalOcean is the fake API.
type DigitalOcean struct {
	*httptest.Server

	mu        sync.Mutex
	requests  []Request
	droplets  map[int64]*Droplet
	firewalls map[string]*DOFirewall
	keys      map[int64]*SSHKey
	nextID    int64

	// BootAfter is how many reads of a new droplet answer "new" before it is
	// "active" with an address.
	BootAfter int
	// IPv4 is the address the next droplet gets.
	IPv4 string
	// PageSize is how many items a list answers with per page: two, so the
	// links are followed.
	PageSize int
	// DropletLimit refuses a droplet past this many, as an account at its
	// limit is refused; 0 is no limit.
	DropletLimit int
	// OnCreate is called with every droplet created, before the answer is
	// written, so a test can start the machine the panel will connect to.
	OnCreate func(Droplet)
}

// NewDigitalOcean starts a fake. It stops when the test ends.
func NewDigitalOcean(t *testing.T) *DigitalOcean {
	t.Helper()
	f := &DigitalOcean{
		droplets: map[int64]*Droplet{}, firewalls: map[string]*DOFirewall{}, keys: map[int64]*SSHKey{},
		nextID: 500, BootAfter: 1, IPv4: "198.51.100.60", PageSize: 2,
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// Opener opens the fake as the panel would open DigitalOcean.
func (f *DigitalOcean) Opener() cloud.Opener {
	return func(kind, token string) (cloud.Provider, error) {
		if kind != cloud.KindDigitalOcean {
			return nil, fmt.Errorf("no provider %q", kind)
		}
		return cloud.NewDigitalOceanAt(token, f.URL, f.Client()), nil
	}
}

// Requests returns every request answered, in order.
func (f *DigitalOcean) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Droplets returns the droplets that exist.
func (f *DigitalOcean) Droplets() []Droplet {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Droplet{}
	for _, d := range f.droplets {
		out = append(out, *d)
	}
	return out
}

// Firewalls returns the firewalls that exist.
func (f *DigitalOcean) Firewalls() []DOFirewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []DOFirewall{}
	for _, fw := range f.firewalls {
		out = append(out, *fw)
	}
	return out
}

// Keys returns the SSH keys that exist.
func (f *DigitalOcean) Keys() []SSHKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []SSHKey{}
	for _, k := range f.keys {
		out = append(out, *k)
	}
	return out
}

func (f *DigitalOcean) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			doFail(w, http.StatusBadRequest, "bad_request", "invalid JSON")
			return
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/")
	f.requests = append(f.requests, Request{Method: r.Method, Path: "/" + path, Query: r.URL.RawQuery, Body: body})

	switch r.Header.Get("Authorization") {
	case "Bearer " + Token:
	case "Bearer " + ReadOnlyToken:
		if r.Method != http.MethodGet {
			doFail(w, http.StatusForbidden, "forbidden", "You are not authorized to perform this operation")
			return
		}
	default:
		doFail(w, http.StatusUnauthorized, "unauthorized", "Unable to authenticate you")
		return
	}

	parts := strings.Split(path, "/")
	switch {
	case r.Method == http.MethodGet && path == "regions":
		f.page(w, r, "regions", []any{
			map[string]any{"slug": "ams3", "name": "Amsterdam 3", "available": true},
			map[string]any{"slug": "fra1", "name": "Frankfurt 1", "available": true},
			map[string]any{"slug": "nyc1", "name": "New York 1", "available": true},
			map[string]any{"slug": "sfo1", "name": "San Francisco 1", "available": false},
		})
	case r.Method == http.MethodGet && path == "sizes":
		f.page(w, r, "sizes", []any{
			map[string]any{"slug": "s-1vcpu-1gb", "memory": 1024, "vcpus": 1, "disk": 25, "price_monthly": 6.0,
				"price_hourly": 0.00893, "regions": []string{"fra1", "ams3"}, "available": true, "description": "Basic"},
			map[string]any{"slug": "g-2vcpu-8gb", "memory": 8192, "vcpus": 2, "disk": 25, "price_monthly": 63.0,
				"price_hourly": 0.09375, "regions": []string{"nyc1"}, "available": true, "description": "General Purpose"},
			map[string]any{"slug": "s-1vcpu-512mb-10gb", "memory": 512, "vcpus": 1, "disk": 10, "price_monthly": 4.0,
				"price_hourly": 0.00595, "regions": []string{}, "available": false, "description": "Basic"},
			map[string]any{"slug": "gpu-h100x1-80gb", "memory": 245760, "vcpus": 20, "disk": 720, "price_monthly": 2500.0,
				"price_hourly": 3.39, "regions": []string{"nyc1"}, "available": true, "description": "GPU"},
		})
	case r.Method == http.MethodGet && path == "images":
		f.page(w, r, "images", []any{
			map[string]any{"slug": "debian-12-x64", "name": "12 x64", "distribution": "Debian", "status": "available"},
			map[string]any{"slug": "ubuntu-24-04-x64", "name": "24.04 (LTS) x64", "distribution": "Ubuntu", "status": "available"},
			map[string]any{"slug": "fedora-41-x64", "name": "41 x64", "distribution": "Fedora", "status": "available"},
		})

	case r.Method == http.MethodPost && path == "account/keys":
		f.createKey(w, body)
	case r.Method == http.MethodGet && path == "account/keys":
		items := []any{}
		for _, id := range sortedIDs(f.keys) {
			items = append(items, doKey(f.keys[id]))
		}
		f.page(w, r, "ssh_keys", items)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "account" && parts[1] == "keys":
		for _, k := range f.keys {
			if k.Fingerprint == parts[2] || strconv.FormatInt(k.ID, 10) == parts[2] {
				doWrite(w, http.StatusOK, map[string]any{"ssh_key": doKey(k)})
				return
			}
		}
		doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
	case r.Method == http.MethodDelete && len(parts) == 3 && parts[0] == "account" && parts[1] == "keys":
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		if _, ok := f.keys[id]; !ok {
			doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
			return
		}
		delete(f.keys, id)
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && path == "firewalls":
		items := []any{}
		ids := make([]string, 0, len(f.firewalls))
		for id := range f.firewalls {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			items = append(items, f.firewalls[id])
		}
		f.page(w, r, "firewalls", items)
	case r.Method == http.MethodPost && path == "firewalls":
		var fw DOFirewall
		_ = json.Unmarshal(raw, &fw)
		if fw.Name == "" || len(fw.OutboundRules) == 0 {
			// The real API accepts no outbound rules and then lets nothing
			// out; the fake refuses it, so a test sees the mistake.
			doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "a firewall here needs a name and outbound rules")
			return
		}
		f.nextID++
		fw.ID = fmt.Sprintf("fw-%d", f.nextID)
		f.firewalls[fw.ID] = &fw
		doWrite(w, http.StatusAccepted, map[string]any{"firewall": fw})
	case len(parts) == 2 && parts[0] == "firewalls":
		fw, ok := f.firewalls[parts[1]]
		if !ok {
			doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			doWrite(w, http.StatusOK, map[string]any{"firewall": fw})
		case http.MethodPut:
			var updated DOFirewall
			_ = json.Unmarshal(raw, &updated)
			if updated.Name == "" || len(updated.OutboundRules) == 0 {
				doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "PUT replaces the whole firewall")
				return
			}
			updated.ID = fw.ID
			f.firewalls[fw.ID] = &updated
			doWrite(w, http.StatusOK, map[string]any{"firewall": updated})
		case http.MethodDelete:
			delete(f.firewalls, fw.ID)
			w.WriteHeader(http.StatusNoContent)
		}

	case r.Method == http.MethodPost && path == "droplets":
		f.createDroplet(w, raw)
	case r.Method == http.MethodGet && path == "droplets":
		tag := r.URL.Query().Get("tag_name")
		items := []any{}
		for _, id := range sortedIDs(f.droplets) {
			d := f.droplets[id]
			if tag == "" || slices.Contains(d.Tags, tag) {
				items = append(items, d.json())
			}
		}
		f.page(w, r, "droplets", items)
	case len(parts) == 2 && parts[0] == "droplets":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		d, ok := f.droplets[id]
		if !ok {
			doFail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			d.reads++
			if d.Status == "new" && d.reads > f.BootAfter {
				d.Status = "active"
			}
			doWrite(w, http.StatusOK, map[string]any{"droplet": d.json()})
		case http.MethodDelete:
			delete(f.droplets, id)
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		doFail(w, http.StatusNotFound, "not_found", "no route "+r.Method+" /"+path)
	}
}

func (f *DigitalOcean) createKey(w http.ResponseWriter, body map[string]any) {
	name, _ := body["name"].(string)
	publicKey, _ := body["public_key"].(string)
	if name == "" || publicKey == "" {
		doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Name can't be blank, Public key can't be blank")
		return
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Key invalid")
		return
	}
	sum := md5.Sum(parsed.Marshal()) //nolint:gosec // DigitalOcean's fingerprint
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	fingerprint := strings.Join(parts, ":")
	for _, k := range f.keys {
		if k.Fingerprint == fingerprint {
			doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "SSH Key is already in use on your account")
			return
		}
	}
	f.nextID++
	k := &SSHKey{ID: f.nextID, Name: name, PublicKey: publicKey, Fingerprint: fingerprint}
	f.keys[k.ID] = k
	doWrite(w, http.StatusCreated, map[string]any{"ssh_key": doKey(k)})
}

func (f *DigitalOcean) createDroplet(w http.ResponseWriter, raw []byte) {
	var req struct {
		Name     string   `json:"name"`
		Region   string   `json:"region"`
		Size     string   `json:"size"`
		Image    string   `json:"image"`
		SSHKeys  []int64  `json:"ssh_keys"`
		UserData string   `json:"user_data"`
		Tags     []string `json:"tags"`
		IPv6     bool     `json:"ipv6"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		// ssh_keys as names, not ids, lands here: the real API refuses them.
		doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "ssh_keys are ids or fingerprints")
		return
	}
	if req.Name == "" || req.Region == "" || req.Size == "" || req.Image == "" {
		doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "name, region, size and image are required")
		return
	}
	if req.Size == "g-2vcpu-8gb" && req.Region != "nyc1" {
		doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "Size is not available in this region.")
		return
	}
	if f.DropletLimit > 0 && len(f.droplets) >= f.DropletLimit {
		doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity",
			"creating this/these droplet(s) will exceed your droplet limit")
		return
	}
	for _, id := range req.SSHKeys {
		if _, ok := f.keys[id]; !ok {
			doFail(w, http.StatusUnprocessableEntity, "unprocessable_entity", "an SSH key does not exist")
			return
		}
	}
	f.nextID++
	d := &Droplet{
		ID: f.nextID, Name: req.Name, Status: "new", Size: req.Size, Image: req.Image, Region: req.Region,
		UserData: req.UserData, SSHKeys: req.SSHKeys, Tags: req.Tags, IPv4: f.IPv4, IPv6: req.IPv6,
	}
	for _, id := range req.SSHKeys {
		d.PublicKeys = append(d.PublicKeys, f.keys[id].PublicKey)
	}
	f.droplets[d.ID] = d
	if f.OnCreate != nil {
		f.OnCreate(*d)
	}
	doWrite(w, http.StatusAccepted, map[string]any{"droplet": d.json()})
}

func (d *Droplet) json() map[string]any {
	v4 := []any{map[string]any{"ip_address": "10.110.0.2", "type": "private"}}
	if d.Status == "active" {
		v4 = append(v4, map[string]any{"ip_address": d.IPv4, "type": "public"})
	}
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"id": d.ID, "name": d.Name, "status": d.Status, "tags": tags,
		"networks": map[string]any{"v4": v4, "v6": []any{}},
	}
}

func doKey(k *SSHKey) map[string]any {
	return map[string]any{"id": k.ID, "name": k.Name, "fingerprint": k.Fingerprint, "public_key": k.PublicKey}
}

// page answers one page of a collection, with links.pages.next while there is
// more, as DigitalOcean does.
func (f *DigitalOcean) page(w http.ResponseWriter, r *http.Request, key string, items []any) {
	size := f.PageSize
	if size <= 0 {
		size = 200
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	start := min((page-1)*size, len(items))
	end := min(start+size, len(items))
	links := map[string]any{}
	if end < len(items) {
		next := *r.URL
		query := next.Query()
		query.Set("page", strconv.Itoa(page+1))
		next.RawQuery = query.Encode()
		links["pages"] = map[string]any{"next": "https://api.digitalocean.com/v2" + next.RequestURI()}
	}
	doWrite(w, http.StatusOK, map[string]any{key: items[start:end], "links": links, "meta": map[string]any{"total": len(items)}})
}

func sortedIDs[T any](m map[int64]T) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func doWrite(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func doFail(w http.ResponseWriter, status int, id, message string) {
	doWrite(w, status, map[string]any{"id": id, "message": message})
}
