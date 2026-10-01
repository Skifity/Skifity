package dnsprov

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/sigv4"
)

// Obviously fake credentials, one per provider.
const (
	cfToken   = "cf-test-token-not-real-000000000000000000"
	hzToken   = "hz-test-token-not-real-0000000000000000000000000000000000000000"
	doToken   = "dop_v1_test_token_not_real_0000000000000000000000000000000000"
	r53Key    = "AKIAEXAMPLENOTREAL00"
	r53Secret = "not/a/real/secret/key/for/tests/000000000"
)

func writeJSONAnswer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// --- Cloudflare ---

type fakeCloudflare struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	next   int
	// records by zone id.
	records        map[string][]cfRecord
	sawExactFilter bool
	proxied        bool
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare {
	f := &fakeCloudflare{t: t, records: map[string][]cfRecord{
		"zone-com": {{ID: "pre1", Type: "A", Name: "www.example.com", Content: "198.51.100.1", TTL: 1}},
	}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCloudflare) URL() string { return f.server.URL }

func (f *fakeCloudflare) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, list := range f.records {
		n += len(list)
	}
	return n
}

func (f *fakeCloudflare) envelope(w http.ResponseWriter, status int, result any, page, pages int) {
	body := map[string]any{"success": status < 300, "errors": []any{}, "messages": []any{}, "result": result}
	if pages > 0 {
		body["result_info"] = map[string]any{"page": page, "per_page": 50, "total_pages": pages, "count": 1, "total_count": pages}
	}
	writeJSONAnswer(w, status, body)
}

func (f *fakeCloudflare) fail(w http.ResponseWriter, status, code int, message string) {
	writeJSONAnswer(w, status, map[string]any{
		"success": false, "errors": []any{map[string]any{"code": code, "message": message}}, "result": nil,
	})
}

func (f *fakeCloudflare) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+cfToken {
		f.fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		if r.URL.Query().Get("per_page") == "" {
			f.t.Error("zones were listed without a page size")
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		zones := [][]map[string]any{
			{{"id": "zone-com", "name": "example.com", "status": "active", "account": map[string]any{"id": "acc", "name": "Acme"}}},
			{{"id": "zone-org", "name": "example.org", "status": "active"}},
		}
		if page < 1 || page > len(zones) {
			f.envelope(w, 200, []any{}, page, len(zones))
			return
		}
		f.envelope(w, 200, zones[page-1], page, len(zones))

	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name.exact")
		if name != "" {
			f.sawExactFilter = true
		}
		out := []cfRecord{}
		for _, record := range f.records[parts[1]] {
			if name == "" || strings.EqualFold(record.Name, name) {
				out = append(out, record)
			}
		}
		f.envelope(w, 200, out, 1, 1)

	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "dns_records" && r.Method == http.MethodPost:
		var in cfRecord
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			f.fail(w, 400, 9207, "Request body is invalid.")
			return
		}
		if len(in.Comment) > 100 {
			f.fail(w, 400, 9101, "DNS record comment is too long")
			return
		}
		f.next++
		in.ID = fmt.Sprintf("rec%d", f.next)
		f.proxied = f.proxied || in.Proxied
		f.records[parts[1]] = append(f.records[parts[1]], in)
		f.envelope(w, 200, in, 0, 0)

	case len(parts) == 4 && parts[0] == "zones" && parts[2] == "dns_records":
		list := f.records[parts[1]]
		for i, record := range list {
			if record.ID != parts[3] {
				continue
			}
			switch r.Method {
			case http.MethodPatch:
				var in map[string]any
				_ = json.NewDecoder(r.Body).Decode(&in)
				if len(in) != 1 || in["content"] == nil {
					f.t.Errorf("a PATCH changed more than the content: %v", in)
				}
				list[i].Content, _ = in["content"].(string)
				f.envelope(w, 200, list[i], 0, 0)
			case http.MethodDelete:
				f.records[parts[1]] = append(list[:i:i], list[i+1:]...)
				f.envelope(w, 200, map[string]any{"id": record.ID}, 0, 0)
			}
			return
		}
		f.fail(w, http.StatusNotFound, 81044, "Record does not exist.")

	default:
		f.fail(w, http.StatusNotFound, 7003, "Could not route to "+r.URL.Path)
	}
}

// --- Hetzner ---

type hzSet struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	TTL     *int              `json:"ttl"`
	Labels  map[string]string `json:"labels"`
	Records []struct {
		Value   string `json:"value"`
		Comment string `json:"comment,omitempty"`
	} `json:"records"`
}

type fakeHetzner struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	sets     map[string][]hzSet // by zone id
	labelled bool
}

func newFakeHetzner(t *testing.T) *fakeHetzner {
	f := &fakeHetzner{t: t, sets: map[string][]hzSet{}}
	www := hzSet{Name: "www", Type: "A"}
	www.Records = append(www.Records, struct {
		Value   string `json:"value"`
		Comment string `json:"comment,omitempty"`
	}{Value: "198.51.100.1"})
	f.sets["1001"] = []hzSet{www}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeHetzner) URL() string { return f.server.URL }

func (f *fakeHetzner) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, sets := range f.sets {
		for _, set := range sets {
			n += len(set.Records)
		}
	}
	return n
}

func (f *fakeHetzner) fail(w http.ResponseWriter, status int, code, message string) {
	writeJSONAnswer(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": nil}})
}

func (f *fakeHetzner) action() map[string]any {
	return map[string]any{"action": map[string]any{"id": 1, "command": "change_rrset", "status": "running"}}
}

func (s hzSet) answer() map[string]any {
	return map[string]any{
		"id": s.Name + "/" + s.Type, "name": s.Name, "type": s.Type, "ttl": s.TTL, "labels": s.Labels,
		"protection": map[string]any{"change": false}, "records": s.Records, "zone": 1001,
	}
}

func (f *fakeHetzner) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+hzToken {
		f.fail(w, http.StatusUnauthorized, "unauthorized", "unable to authenticate")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages := [][]map[string]any{
			{
				{"id": 1001, "name": "example.com", "mode": "primary", "status": "ok"},
				{"id": 1003, "name": "copied.example", "mode": "secondary", "status": "ok"},
			},
			{{"id": 1002, "name": "example.net", "mode": "primary", "status": "ok"}},
		}
		zones := []map[string]any{}
		if page >= 1 && page <= len(pages) {
			zones = pages[page-1]
		}
		writeJSONAnswer(w, 200, map[string]any{"zones": zones, "meta": map[string]any{"pagination": map[string]any{
			"page": page, "per_page": 100, "last_page": len(pages), "total_entries": 3,
		}}})

	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "rrsets" && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")
		out := []map[string]any{}
		for _, set := range f.sets[parts[1]] {
			if name == "" || set.Name == name {
				out = append(out, set.answer())
			}
		}
		writeJSONAnswer(w, 200, map[string]any{"rrsets": out, "meta": map[string]any{"pagination": map[string]any{"page": 1, "last_page": 1}}})

	case len(parts) == 3 && parts[0] == "zones" && parts[2] == "rrsets" && r.Method == http.MethodPost:
		var in hzSet
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			f.fail(w, 400, "invalid_input", "invalid input")
			return
		}
		if strings.Contains(in.Name, ".example.") {
			f.t.Errorf("a record set was named %q, not relative to its zone", in.Name)
		}
		if in.Type == "CNAME" && !strings.HasSuffix(in.Records[0].Value, ".") {
			f.fail(w, 422, "invalid_input", "a CNAME's value has to be a fully qualified name")
			return
		}
		for _, set := range f.sets[parts[1]] {
			if set.Name == in.Name && set.Type == in.Type {
				f.fail(w, 409, "uniqueness_error", "rrset already exists")
				return
			}
		}
		f.labelled = f.labelled || in.Labels["managed-by"] == "skifity"
		f.sets[parts[1]] = append(f.sets[parts[1]], in)
		answer := f.action()
		answer["rrset"] = in.answer()
		writeJSONAnswer(w, 201, answer)

	case len(parts) >= 5 && parts[0] == "zones" && parts[2] == "rrsets":
		sets := f.sets[parts[1]]
		for i, set := range sets {
			if set.Name != parts[3] || set.Type != parts[4] {
				continue
			}
			switch {
			case r.Method == http.MethodDelete && len(parts) == 5:
				f.sets[parts[1]] = append(sets[:i:i], sets[i+1:]...)
				writeJSONAnswer(w, 201, f.action())
			case r.Method == http.MethodPost && len(parts) == 7 && parts[6] == "set_records":
				var in hzSet
				_ = json.NewDecoder(r.Body).Decode(&in)
				sets[i].Records = in.Records
				writeJSONAnswer(w, 201, f.action())
			default:
				f.fail(w, 404, "not_found", "no such action")
			}
			return
		}
		f.fail(w, http.StatusNotFound, "not_found", "rrset not found")

	default:
		f.fail(w, http.StatusNotFound, "not_found", "no route "+r.URL.Path)
	}
}

// --- DigitalOcean ---

type fakeDigitalOcean struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	next    int
	records map[string][]map[string]any // by domain
}

func newFakeDigitalOcean(t *testing.T) *fakeDigitalOcean {
	f := &fakeDigitalOcean{t: t, next: 100, records: map[string][]map[string]any{
		"example.com": {
			{"id": 1, "type": "NS", "name": "@", "data": "ns1.digitalocean.com", "ttl": 1800},
			{"id": 2, "type": "A", "name": "www", "data": "198.51.100.1", "ttl": 3600},
		},
	}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeDigitalOcean) URL() string { return f.server.URL }

func (f *fakeDigitalOcean) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, list := range f.records {
		n += len(list)
	}
	return n
}

func (f *fakeDigitalOcean) fail(w http.ResponseWriter, status int, id, message string) {
	writeJSONAnswer(w, status, map[string]any{"id": id, "message": message})
}

func (f *fakeDigitalOcean) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+doToken {
		f.fail(w, http.StatusUnauthorized, "Unauthorized", "Unable to authenticate you")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/domains":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		links := map[string]any{"pages": map[string]any{}}
		domains := []any{map[string]any{"name": "example.io", "ttl": 1800}}
		if page <= 1 {
			domains = []any{map[string]any{"name": "example.com", "ttl": 1800}}
			links["pages"] = map[string]any{"next": f.server.URL + "/domains?page=2&per_page=200", "last": f.server.URL + "/domains?page=2&per_page=200"}
		}
		writeJSONAnswer(w, 200, map[string]any{"domains": domains, "links": links, "meta": map[string]any{"total": 2}})

	case len(parts) == 3 && parts[0] == "domains" && parts[2] == "records" && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")
		if name != "" && !strings.HasSuffix(name, "."+parts[1]) && name != parts[1] {
			f.fail(w, 422, "unprocessable_entity", "name must be a fully qualified record name")
			return
		}
		out := []map[string]any{}
		for _, record := range f.records[parts[1]] {
			full := record["name"].(string) + "." + parts[1]
			if record["name"] == "@" {
				full = parts[1]
			}
			if name == "" || full == name {
				out = append(out, record)
			}
		}
		writeJSONAnswer(w, 200, map[string]any{"domain_records": out, "links": map[string]any{}, "meta": map[string]any{"total": len(out)}})

	case len(parts) == 3 && parts[0] == "domains" && parts[2] == "records" && r.Method == http.MethodPost:
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if name, _ := in["name"].(string); strings.HasSuffix(name, parts[1]) {
			f.t.Errorf("a record was named %q, not relative to its domain", name)
		}
		if in["type"] == "CNAME" && !strings.HasSuffix(in["data"].(string), ".") {
			f.fail(w, 422, "unprocessable_entity", "Data needs to end with a dot (.)")
			return
		}
		f.next++
		in["id"] = f.next
		f.records[parts[1]] = append(f.records[parts[1]], in)
		writeJSONAnswer(w, 201, map[string]any{"domain_record": in})

	case len(parts) == 4 && parts[0] == "domains" && parts[2] == "records":
		id, _ := strconv.Atoi(parts[3])
		list := f.records[parts[1]]
		for i, record := range list {
			if number, _ := record["id"].(int); number != id {
				if n, ok := record["id"].(float64); !ok || int(n) != id {
					continue
				}
			}
			switch r.Method {
			case http.MethodPatch:
				var in map[string]any
				_ = json.NewDecoder(r.Body).Decode(&in)
				list[i]["data"] = in["data"]
				writeJSONAnswer(w, 200, map[string]any{"domain_record": list[i]})
			case http.MethodDelete:
				f.records[parts[1]] = append(list[:i:i], list[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		f.fail(w, http.StatusNotFound, "not_found", "The resource you were accessing could not be found.")

	default:
		f.fail(w, http.StatusNotFound, "not_found", "no route "+r.URL.Path)
	}
}

// --- Route 53 ---

type fakeRoute53 struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	// sets by zone id, then "name|type".
	sets   map[string]map[string]r53Set
	signed int
}

func newFakeRoute53(t *testing.T) *fakeRoute53 {
	f := &fakeRoute53{t: t, sets: map[string]map[string]r53Set{"Z1EXAMPLE": {}, "Z2EXAMPLE": {}}}
	f.put("Z1EXAMPLE", "www.example.com.", "A", 300, "198.51.100.1")
	f.put("Z1EXAMPLE", "zzz.example.com.", "A", 300, "198.51.100.2")
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeRoute53) URL() string { return f.server.URL + "/2013-04-01" }

func (f *fakeRoute53) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, sets := range f.sets {
		n += len(sets)
	}
	return n
}

func (f *fakeRoute53) put(zone, name, kind string, ttl int, values ...string) {
	set := r53Set{Name: name, Type: kind, TTL: ttl}
	set.ResourceRecords = &struct {
		Values []string `xml:"ResourceRecord>Value"`
	}{Values: values}
	f.sets[zone][name+"|"+kind] = set
}

// edit is somebody changing a set in the AWS console.
func (f *fakeRoute53) edit(name, kind, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.put("Z1EXAMPLE", name, kind, 300, value)
}

func (f *fakeRoute53) fail(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header+body)
}

// verify signs the request again with the real secret and compares: a
// signature over a different body, path or query does not match.
func (f *fakeRoute53) verify(r *http.Request, body []byte) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+r53Key+"/") || !strings.Contains(auth, "/us-east-1/route53/aws4_request") {
		return false
	}
	at, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	again, _ := http.NewRequest(r.Method, "http://"+r.Host+r.URL.RequestURI(), bytes.NewReader(body))
	if ct := r.Header.Get("Content-Type"); ct != "" {
		again.Header.Set("Content-Type", ct)
	}
	sigv4.Sign(again, body, sigv4.Credentials{AccessKeyID: r53Key, SecretAccessKey: r53Secret}, "us-east-1", "route53", at)
	return again.Header.Get("Authorization") == auth
}

func (f *fakeRoute53) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !f.verify(r, body) {
		f.fail(w, http.StatusForbidden, `<ErrorResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Error><Type>Sender</Type>`+
			`<Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match the signature you provided.</Message></Error>`+
			`<RequestId>req-1</RequestId></ErrorResponse>`)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signed++
	path := strings.TrimPrefix(r.URL.Path, "/2013-04-01")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && path == "/hostedzone":
		w.Header().Set("Content-Type", "text/xml")
		if r.URL.Query().Get("marker") == "" {
			_, _ = io.WriteString(w, xml.Header+`<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones>`+
				`<HostedZone><Id>/hostedzone/Z1EXAMPLE</Id><Name>example.com.</Name><CallerReference>a</CallerReference><Config><PrivateZone>false</PrivateZone></Config><ResourceRecordSetCount>4</ResourceRecordSetCount></HostedZone>`+
				`<HostedZone><Id>/hostedzone/Z9PRIVATE</Id><Name>internal.example.</Name><CallerReference>b</CallerReference><Config><PrivateZone>true</PrivateZone></Config><ResourceRecordSetCount>2</ResourceRecordSetCount></HostedZone>`+
				`</HostedZones><IsTruncated>true</IsTruncated><NextMarker>Z2EXAMPLE</NextMarker><MaxItems>2</MaxItems></ListHostedZonesResponse>`)
			return
		}
		_, _ = io.WriteString(w, xml.Header+`<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones>`+
			`<HostedZone><Id>/hostedzone/Z2EXAMPLE</Id><Name>example.dev.</Name><CallerReference>c</CallerReference><Config><PrivateZone>false</PrivateZone></Config><ResourceRecordSetCount>2</ResourceRecordSetCount></HostedZone>`+
			`</HostedZones><IsTruncated>false</IsTruncated><Marker>Z2EXAMPLE</Marker><MaxItems>2</MaxItems></ListHostedZonesResponse>`)

	case len(parts) == 3 && parts[0] == "hostedzone" && parts[2] == "rrset" && r.Method == http.MethodGet:
		start := r.URL.Query().Get("name")
		if !strings.HasSuffix(start, ".") {
			f.t.Errorf("records were listed from %q, without its final dot", start)
		}
		var keys []string
		for key := range f.sets[parts[1]] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var out strings.Builder
		for _, key := range keys {
			set := f.sets[parts[1]][key]
			if set.Name < start {
				continue
			}
			data, _ := xml.Marshal(struct {
				XMLName xml.Name `xml:"ResourceRecordSet"`
				r53Set
			}{r53Set: set})
			out.Write(data)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, xml.Header+`<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ResourceRecordSets>`+
			out.String()+`</ResourceRecordSets><IsTruncated>false</IsTruncated><MaxItems>50</MaxItems></ListResourceRecordSetsResponse>`)

	case len(parts) == 3 && parts[0] == "hostedzone" && parts[2] == "rrset" && r.Method == http.MethodPost:
		if !strings.HasSuffix(r.URL.Path, "/rrset/") {
			f.t.Errorf("a change was posted to %s, not .../rrset/", r.URL.Path)
		}
		var request struct {
			Changes []struct {
				Action string `xml:"Action"`
				Set    r53Set `xml:"ResourceRecordSet"`
			} `xml:"ChangeBatch>Changes>Change"`
		}
		if err := xml.Unmarshal(body, &request); err != nil || len(request.Changes) == 0 {
			f.fail(w, 400, `<ErrorResponse><Error><Type>Sender</Type><Code>MalformedInput</Code><Message>bad</Message></Error></ErrorResponse>`)
			return
		}
		zone := f.sets[parts[1]]
		next := map[string]r53Set{}
		for key, set := range zone {
			next[key] = set
		}
		for _, change := range request.Changes {
			set := change.Set
			key := set.Name + "|" + set.Type
			current, exists := next[key]
			describe := fmt.Sprintf("[name='%s', type='%s']", set.Name, set.Type)
			switch change.Action {
			case "CREATE":
				if exists {
					f.invalid(w, "Tried to create resource record set "+describe+" but it already exists")
					return
				}
				next[key] = set
			case "DELETE":
				if !exists {
					f.invalid(w, "Tried to delete resource record set "+describe+" but it was not found")
					return
				}
				if current.TTL != set.TTL || fmt.Sprint(current.ResourceRecords.Values) != fmt.Sprint(set.ResourceRecords.Values) {
					f.invalid(w, "Tried to delete resource record set "+describe+" but the values provided do not match the current values")
					return
				}
				delete(next, key)
			}
		}
		f.sets[parts[1]] = next
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, xml.Header+`<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">`+
			`<ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status><SubmittedAt>2026-09-30T12:00:00Z</SubmittedAt></ChangeInfo></ChangeResourceRecordSetsResponse>`)

	default:
		f.fail(w, 404, `<ErrorResponse><Error><Type>Sender</Type><Code>NoSuchHostedZone</Code><Message>`+url.PathEscape(path)+`</Message></Error></ErrorResponse>`)
	}
}

func (f *fakeRoute53) invalid(w http.ResponseWriter, message string) {
	f.fail(w, http.StatusBadRequest, `<InvalidChangeBatch xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Messages><Message>`+
		message+`</Message></Messages><RequestId>req-2</RequestId></InvalidChangeBatch>`)
}
