package dnsprov

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// DigitalOcean, through its v2 API, with a personal access token. A token
// limited to the domain scopes — domain:read, domain:create, domain:update,
// domain:delete — is all it needs.
//
// A record here has a numeric id, a name relative to its domain ("@" for the
// domain itself), and nowhere to keep a note: the panel's own record of what
// it created is the only one.
//
// Shapes, from https://docs.digitalocean.com/reference/api/digitalocean/#tag/Domains
// and #tag/Domain-Records:
//
//	GET    /domains?page=&per_page=                    {domains: [{name}], links: {pages: {next}}}
//	GET    /domains/{domain}/records?name={fqdn}       {domain_records: [{id, type, name, data, ttl}], links}
//	POST   /domains/{domain}/records                   {type, name, data, ttl} → 201 {domain_record}
//	PATCH  /domains/{domain}/records/{id}              {type, data} → {domain_record}
//	DELETE /domains/{domain}/records/{id}              → 204; 404 when it is not there

const digitaloceanAPI = "https://api.digitalocean.com/v2"

// digitaloceanTTL is what a record the panel creates is given. DigitalOcean's
// minimum is 30 seconds; five minutes lets an address change through soon.
const digitaloceanTTL = 300

type digitalocean struct {
	token  string
	base   string
	client *http.Client
}

func (d *digitalocean) Kind() string { return DigitalOcean }

func (d *digitalocean) api() jsonAPI {
	return jsonAPI{
		kind:   DigitalOcean,
		client: d.client,
		sign:   func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+d.token) },
		message: func(body []byte) string {
			var answer struct {
				ID      string `json:"id"`
				Message string `json:"message"`
			}
			if json.Unmarshal(body, &answer) != nil || answer.Message == "" {
				return ""
			}
			return answer.Message + " (" + answer.ID + ")"
		},
	}
}

type doRecord struct {
	ID   json.Number `json:"id"`
	Type string      `json:"type"`
	Name string      `json:"name"`
	Data string      `json:"data"`
	TTL  int         `json:"ttl"`
}

func (r doRecord) record(zone Zone) Record {
	kind := strings.ToUpper(r.Type)
	return Record{
		ID: r.ID.String(), Type: kind, Name: absolute(r.Name, zone.Name),
		Content: canonicalContent(kind, doContent(kind, r.Data, zone.Name)), TTL: r.TTL,
	}
}

// doContent reads a value the way DigitalOcean writes it: "@" for the domain
// itself, and a name with no final dot for one inside it.
func doContent(kind, data, zone string) string {
	if kind != "CNAME" {
		return data
	}
	if data == "@" {
		return zone
	}
	if !strings.HasSuffix(data, ".") && !strings.Contains(data, ".") {
		return data + "." + zone
	}
	return data
}

type doLinks struct {
	Pages struct {
		Next string `json:"next"`
	} `json:"pages"`
}

func (d *digitalocean) Zones(ctx context.Context) ([]Zone, error) {
	zones := []Zone{}
	for page := 1; page <= maxPages; page++ {
		var answer struct {
			Domains []struct {
				Name string `json:"name"`
			} `json:"domains"`
			Links doLinks `json:"links"`
		}
		query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"200"}}
		if err := d.api().do(ctx, http.MethodGet, d.base+"/domains?"+query.Encode(), nil, &answer); err != nil {
			return nil, err
		}
		for _, domain := range answer.Domains {
			name := Canonical(domain.Name)
			zones = append(zones, Zone{ID: name, Name: name})
		}
		if answer.Links.Pages.Next == "" {
			break
		}
	}
	return sortZones(zones), nil
}

func (d *digitalocean) Records(ctx context.Context, zone Zone, name string) ([]Record, error) {
	name = Canonical(name)
	out := []Record{}
	for page := 1; page <= maxPages; page++ {
		var answer struct {
			Records []doRecord `json:"domain_records"`
			Links   doLinks    `json:"links"`
		}
		query := url.Values{"name": {name}, "page": {strconv.Itoa(page)}, "per_page": {"200"}}
		if err := d.api().do(ctx, http.MethodGet, d.recordsPath(zone)+"?"+query.Encode(), nil, &answer); err != nil {
			return nil, err
		}
		for _, r := range answer.Records {
			if record := r.record(zone); record.Name == name {
				out = append(out, record)
			}
		}
		if answer.Links.Pages.Next == "" {
			break
		}
	}
	return out, nil
}

func (d *digitalocean) Create(ctx context.Context, zone Zone, record Record) (Record, error) {
	body := map[string]any{
		"type": record.Type, "name": relative(record.Name, zone.Name),
		"data": doValue(record.Type, record.Content), "ttl": digitaloceanTTL,
	}
	var answer struct {
		Record doRecord `json:"domain_record"`
	}
	if err := d.api().do(ctx, http.MethodPost, d.recordsPath(zone), body, &answer); err != nil {
		return Record{}, err
	}
	return answer.Record.record(zone), nil
}

func (d *digitalocean) SetContent(ctx context.Context, zone Zone, record Record, content string) (Record, error) {
	body := map[string]any{"type": record.Type, "data": doValue(record.Type, content)}
	var answer struct {
		Record doRecord `json:"domain_record"`
	}
	if err := d.api().do(ctx, http.MethodPatch, d.recordsPath(zone)+"/"+url.PathEscape(record.ID), body, &answer); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return Record{}, ErrGone
		}
		return Record{}, err
	}
	return answer.Record.record(zone), nil
}

func (d *digitalocean) Delete(ctx context.Context, zone Zone, record Record) error {
	if err := d.api().do(ctx, http.MethodDelete, d.recordsPath(zone)+"/"+url.PathEscape(record.ID), nil, nil); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return ErrGone
		}
		return err
	}
	return nil
}

func (d *digitalocean) recordsPath(zone Zone) string {
	return d.base + "/domains/" + url.PathEscape(zone.ID) + "/records"
}

// doValue is a value as DigitalOcean wants it: a CNAME's name with its final
// dot, which it requires for a name outside the domain.
func doValue(kind, content string) string {
	if kind == "CNAME" {
		return Canonical(content) + "."
	}
	return content
}
