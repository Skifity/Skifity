package dnsprov

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Hetzner, through the Hetzner Cloud API's DNS zones.
//
// Not dns.hetzner.com: Hetzner moved DNS into the Hetzner Console, stopped
// changes through the old DNS Console on 20 May 2026 and switched its API off
// after that. A token is made in the Hetzner Console under a project's
// Security, API tokens, with Read & Write.
//
// Hetzner stores a name and a type as one record set with a list of values,
// and names inside a zone relative to it, "@" for the zone itself. A set the
// panel creates carries the label managed-by=skifity and each value a comment.
//
// Shapes, from https://docs.hetzner.cloud/reference/cloud#zones and the
// hcloud-go client's schema (hcloud/schema/zone_rrset.go):
//
//	GET    /zones?page=&per_page=                          {zones: [{id, name, mode}], meta: {pagination: {page, last_page}}}
//	GET    /zones/{zone}/rrsets?name=&per_page=            {rrsets: [{id: "www/A", name, type, ttl, labels, records: [{value, comment}]}]}
//	POST   /zones/{zone}/rrsets                            {name, type, ttl, labels, records} → {rrset, action}
//	POST   /zones/{zone}/rrsets/{name}/{type}/actions/set_records   {records} → {action}
//	DELETE /zones/{zone}/rrsets/{name}/{type}             → {action}; 404 when it is not there

const hetznerAPI = "https://api.hetzner.cloud/v1"

// hetznerTTL is what a set the panel creates is given: five minutes, so an
// address change reaches visitors soon.
const hetznerTTL = 300

type hetzner struct {
	token  string
	base   string
	client *http.Client
}

func (h *hetzner) Kind() string { return Hetzner }

func (h *hetzner) api() jsonAPI {
	return jsonAPI{
		kind:   Hetzner,
		client: h.client,
		sign:   func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+h.token) },
		message: func(body []byte) string {
			var answer struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &answer) != nil || answer.Error.Message == "" {
				return ""
			}
			return answer.Error.Message + " (" + answer.Error.Code + ")"
		},
	}
}

type hzRRSet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	TTL     *int   `json:"ttl"`
	Records []struct {
		Value   string `json:"value"`
		Comment string `json:"comment"`
	} `json:"records"`
}

func (s hzRRSet) records(zone Zone) []Record {
	out := make([]Record, 0, len(s.Records))
	ttl := 0
	if s.TTL != nil {
		ttl = *s.TTL
	}
	kind := strings.ToUpper(s.Type)
	id := s.ID
	if id == "" {
		id = s.Name + "/" + kind
	}
	for _, value := range s.Records {
		out = append(out, Record{
			ID: id, Type: kind, Name: absolute(s.Name, zone.Name),
			Content: canonicalContent(kind, value.Value), TTL: ttl, Note: value.Comment,
		})
	}
	return out
}

func (h *hetzner) Zones(ctx context.Context) ([]Zone, error) {
	zones := []Zone{}
	for page := 1; page <= maxPages; page++ {
		var answer struct {
			Zones []struct {
				ID   json.Number `json:"id"`
				Name string      `json:"name"`
				Mode string      `json:"mode"`
			} `json:"zones"`
			Meta struct {
				Pagination struct {
					LastPage int `json:"last_page"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
		if err := h.api().do(ctx, http.MethodGet, h.base+"/zones?"+query.Encode(), nil, &answer); err != nil {
			return nil, err
		}
		for _, z := range answer.Zones {
			// A secondary zone is copied from somewhere else, and its
			// records are changed there, not here.
			if z.Mode == "secondary" {
				continue
			}
			zones = append(zones, Zone{ID: z.ID.String(), Name: Canonical(z.Name)})
		}
		if answer.Meta.Pagination.LastPage <= page {
			break
		}
	}
	return sortZones(zones), nil
}

func (h *hetzner) Records(ctx context.Context, zone Zone, name string) ([]Record, error) {
	name = Canonical(name)
	query := url.Values{"name": {relative(name, zone.Name)}, "per_page": {"100"}}
	var answer struct {
		RRSets []hzRRSet `json:"rrsets"`
	}
	if err := h.api().do(ctx, http.MethodGet, h.zonePath(zone)+"/rrsets?"+query.Encode(), nil, &answer); err != nil {
		return nil, err
	}
	out := []Record{}
	for _, set := range answer.RRSets {
		for _, record := range set.records(zone) {
			if record.Name == name {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (h *hetzner) Create(ctx context.Context, zone Zone, record Record) (Record, error) {
	body := map[string]any{
		"name": relative(record.Name, zone.Name), "type": record.Type, "ttl": hetznerTTL,
		"labels":  map[string]string{"managed-by": "skifity"},
		"records": []map[string]string{{"value": hetznerValue(record.Type, record.Content), "comment": record.Note}},
	}
	var answer struct {
		RRSet hzRRSet `json:"rrset"`
	}
	if err := h.api().do(ctx, http.MethodPost, h.zonePath(zone)+"/rrsets", body, &answer); err != nil {
		return Record{}, err
	}
	made := answer.RRSet.records(zone)
	if len(made) == 0 {
		return Record{}, errors.New("the answer from Hetzner did not say which record it made")
	}
	return made[0], nil
}

func (h *hetzner) SetContent(ctx context.Context, zone Zone, record Record, content string) (Record, error) {
	body := map[string]any{
		"records": []map[string]string{{"value": hetznerValue(record.Type, content), "comment": record.Note}},
	}
	if err := h.api().do(ctx, http.MethodPost, h.setPath(zone, record)+"/actions/set_records", body, nil); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return Record{}, ErrGone
		}
		return Record{}, err
	}
	record.Content = canonicalContent(record.Type, content)
	return record, nil
}

func (h *hetzner) Delete(ctx context.Context, zone Zone, record Record) error {
	if err := h.api().do(ctx, http.MethodDelete, h.setPath(zone, record), nil, nil); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return ErrGone
		}
		return err
	}
	return nil
}

func (h *hetzner) zonePath(zone Zone) string { return h.base + "/zones/" + url.PathEscape(zone.ID) }

func (h *hetzner) setPath(zone Zone, record Record) string {
	return h.zonePath(zone) + "/rrsets/" + url.PathEscape(relative(record.Name, zone.Name)) + "/" + url.PathEscape(record.Type)
}

// hetznerValue is a value as Hetzner wants it: a name in full, with its final
// dot, since a name without one would be read as inside the zone.
func hetznerValue(kind, content string) string {
	if kind == "CNAME" {
		return Canonical(content) + "."
	}
	return content
}
