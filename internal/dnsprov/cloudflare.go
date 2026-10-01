package dnsprov

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Cloudflare, through its v4 API, with an API token.
//
// The token is the kind made under My Profile, API Tokens (or an account's
// API Tokens), with Zone → DNS → Edit on the zones it should reach. The Global
// API Key is refused before it is sent: it can do anything in the account,
// including things nobody meant a hosting panel to be able to do.
//
// Shapes, from https://developers.cloudflare.com/api/:
//
//	GET    /zones?page=&per_page=                     {success, errors, result: [{id, name}], result_info: {page, total_pages}}
//	GET    /zones/{zone}/dns_records?name.exact=&per_page=   {result: [{id, type, name, content, proxied, ttl, comment}]}
//	POST   /zones/{zone}/dns_records                  {type, name, content, ttl, proxied, comment} → {result: {...}}
//	PATCH  /zones/{zone}/dns_records/{id}             {content} → {result: {...}}
//	DELETE /zones/{zone}/dns_records/{id}             → {result: {id}}; 404 when it is not there
//
// A record the panel creates is not proxied, unless it is the CNAME to a
// tunnel: cert-manager proves a hostname over HTTP, and Let's Encrypt has to
// reach the server itself to do it. TTL 1 is Cloudflare's "automatic".

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

type cloudflare struct {
	token  string
	base   string
	client *http.Client
}

func (c *cloudflare) Kind() string { return Cloudflare }

func (c *cloudflare) api() jsonAPI {
	return jsonAPI{
		kind:   Cloudflare,
		client: c.client,
		sign:   func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+c.token) },
		message: func(body []byte) string {
			var envelope cfEnvelope
			if json.Unmarshal(body, &envelope) != nil || len(envelope.Errors) == 0 {
				return ""
			}
			messages := make([]string, 0, len(envelope.Errors))
			for _, e := range envelope.Errors {
				messages = append(messages, fmt.Sprintf("%s (%d)", e.Message, e.Code))
			}
			return strings.Join(messages, "; ")
		},
	}
}

type cfEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment"`
}

func (r cfRecord) record() Record {
	return Record{
		ID: r.ID, Type: strings.ToUpper(r.Type), Name: Canonical(r.Name),
		Content: canonicalContent(strings.ToUpper(r.Type), r.Content),
		TTL:     r.TTL, Proxied: r.Proxied, Note: r.Comment,
	}
}

// pages reads every page of a listing into each.
func (c *cloudflare) pages(ctx context.Context, path string, query url.Values, each func(json.RawMessage) error) error {
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		var envelope cfEnvelope
		if err := c.api().do(ctx, http.MethodGet, c.base+path+"?"+query.Encode(), nil, &envelope); err != nil {
			return err
		}
		if err := each(envelope.Result); err != nil {
			return fmt.Errorf("read Cloudflare's answer: %w", err)
		}
		if envelope.ResultInfo.TotalPages <= page {
			return nil
		}
	}
	return nil
}

func (c *cloudflare) Zones(ctx context.Context) ([]Zone, error) {
	zones := []Zone{}
	err := c.pages(ctx, "/zones", url.Values{"per_page": {"50"}}, func(raw json.RawMessage) error {
		var page []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, z := range page {
			zones = append(zones, Zone{ID: z.ID, Name: Canonical(z.Name)})
		}
		return nil
	})
	return sortZones(zones), err
}

func (c *cloudflare) Records(ctx context.Context, zone Zone, name string) ([]Record, error) {
	name = Canonical(name)
	out := []Record{}
	query := url.Values{"name.exact": {name}, "per_page": {"100"}}
	err := c.pages(ctx, "/zones/"+url.PathEscape(zone.ID)+"/dns_records", query, func(raw json.RawMessage) error {
		var page []cfRecord
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		// The filter is asked for and not relied on: an API that ignored it
		// would answer the whole zone, and a record at another name is not
		// one at this one.
		for _, r := range page {
			if Canonical(r.Name) == name {
				out = append(out, r.record())
			}
		}
		return nil
	})
	return out, err
}

func (c *cloudflare) Create(ctx context.Context, zone Zone, record Record) (Record, error) {
	body := map[string]any{
		"type": record.Type, "name": Canonical(record.Name), "content": record.Content,
		"ttl": 1, "proxied": record.Proxied,
	}
	if record.Note != "" {
		body["comment"] = record.Note
	}
	var envelope cfEnvelope
	if err := c.api().do(ctx, http.MethodPost, c.base+"/zones/"+url.PathEscape(zone.ID)+"/dns_records", body, &envelope); err != nil {
		return Record{}, err
	}
	return c.result(envelope)
}

func (c *cloudflare) SetContent(ctx context.Context, zone Zone, record Record, content string) (Record, error) {
	// PATCH, with the content and nothing else: somebody who turned the
	// proxy on at Cloudflare turned it on, and an address change is not a
	// reason to turn it off again.
	var envelope cfEnvelope
	path := c.base + "/zones/" + url.PathEscape(zone.ID) + "/dns_records/" + url.PathEscape(record.ID)
	if err := c.api().do(ctx, http.MethodPatch, path, map[string]any{"content": content}, &envelope); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return Record{}, ErrGone
		}
		return Record{}, err
	}
	return c.result(envelope)
}

func (c *cloudflare) Delete(ctx context.Context, zone Zone, record Record) error {
	path := c.base + "/zones/" + url.PathEscape(zone.ID) + "/dns_records/" + url.PathEscape(record.ID)
	if err := c.api().do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return ErrGone
		}
		return err
	}
	return nil
}

func (c *cloudflare) result(envelope cfEnvelope) (Record, error) {
	var r cfRecord
	if err := json.Unmarshal(envelope.Result, &r); err != nil || r.ID == "" {
		return Record{}, errors.New("the answer from Cloudflare did not say which record it made")
	}
	return r.record(), nil
}
