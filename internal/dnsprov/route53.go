package dnsprov

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"skifity/internal/sigv4"
)

// Amazon Route 53, through its REST API, signed with Signature Version 4 by
// internal/sigv4.
//
// An IAM user's access key with route53:ListHostedZones,
// route53:ListResourceRecordSets and route53:ChangeResourceRecordSets on the
// zones it should reach is all it needs.
//
// Route 53 has no record ids and no notes. A name and a type are one record
// set, identified by both; and a change that deletes a set has to name it
// exactly as it is — its TTL and every value — or Route 53 refuses it. That
// refusal is what stops the panel from deleting or changing a set somebody
// edited after the panel made it: an address change deletes the old set and
// creates the new one in one batch, and the batch fails as a whole.
//
// Shapes, from https://docs.aws.amazon.com/Route53/latest/APIReference/:
//
//	GET  /2013-04-01/hostedzone?marker=&maxitems=          ListHostedZonesResponse
//	GET  /2013-04-01/hostedzone/{id}/rrset?name=&maxitems=  ListResourceRecordSetsResponse (starts at name, in order)
//	POST /2013-04-01/hostedzone/{id}/rrset/                 ChangeResourceRecordSetsRequest → ChangeResourceRecordSetsResponse
//
// Errors are an ErrorResponse, or for a batch that does not apply an
// InvalidChangeBatch with one message per change.

const route53API = "https://route53.amazonaws.com/2013-04-01"

// route53TTL is what a set the panel creates is given.
const route53TTL = 300

const route53Namespace = "https://route53.amazonaws.com/doc/2013-04-01/"

type route53 struct {
	creds  Credentials
	base   string
	client *http.Client
	now    func() time.Time
}

func (r *route53) Kind() string { return Route53 }

type r53Set struct {
	Name            string `xml:"Name"`
	Type            string `xml:"Type"`
	SetIdentifier   string `xml:"SetIdentifier,omitempty"`
	TTL             int    `xml:"TTL,omitempty"`
	ResourceRecords *struct {
		Values []string `xml:"ResourceRecord>Value"`
	} `xml:"ResourceRecords,omitempty"`
	AliasTarget *struct {
		DNSName string `xml:"DNSName"`
	} `xml:"AliasTarget,omitempty"`
}

// name reads a set's name the way the rest of the panel writes it: Route 53
// spells characters outside letters, digits and hyphens as octal escapes, and
// the one a hostname can have is the wildcard's \052.
func (s r53Set) name() string { return Canonical(strings.ReplaceAll(s.Name, `\052`, "*")) }

func (s r53Set) records() []Record {
	kind := strings.ToUpper(s.Type)
	// A set with a routing policy has an identifier, and is one of several
	// with the same name: never one the panel made, so its id says so.
	id := s.name() + "/" + kind
	if s.SetIdentifier != "" {
		id += "/" + s.SetIdentifier
	}
	if s.AliasTarget != nil {
		return []Record{{ID: id, Type: kind, Name: s.name(), Content: Canonical(s.AliasTarget.DNSName), Note: "alias"}}
	}
	out := []Record{}
	if s.ResourceRecords != nil {
		for _, value := range s.ResourceRecords.Values {
			out = append(out, Record{ID: id, Type: kind, Name: s.name(), Content: canonicalContent(kind, value), TTL: s.TTL})
		}
	}
	return out
}

// send signs and sends one request, and reads the answer into out.
func (r *route53) send(ctx context.Context, method, path string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/xml")
	}
	sigv4.Sign(req, body, sigv4.Credentials{AccessKeyID: r.creds.AccessKeyID, SecretAccessKey: r.creds.SecretAccessKey},
		"us-east-1", "route53", r.now())

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", Title(Route53), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("read the answer from Route 53: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Provider: Route53, Status: resp.StatusCode, Message: clip(route53Message(raw))}
	}
	if out != nil {
		if err := xml.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("read the answer from Route 53: %w", err)
		}
	}
	return nil
}

// route53Message reads either shape of error Route 53 answers with.
func route53Message(body []byte) string {
	var failure struct {
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
		Messages []string `xml:"Messages>Message"`
	}
	if xml.Unmarshal(body, &failure) != nil {
		return ""
	}
	if len(failure.Messages) > 0 {
		return strings.Join(failure.Messages, "; ")
	}
	if failure.Error.Message != "" {
		return failure.Error.Message + " (" + failure.Error.Code + ")"
	}
	return ""
}

func (r *route53) Zones(ctx context.Context) ([]Zone, error) {
	zones := []Zone{}
	marker := ""
	for range maxPages {
		query := url.Values{"maxitems": {"100"}}
		if marker != "" {
			query.Set("marker", marker)
		}
		var answer struct {
			Zones []struct {
				ID     string `xml:"Id"`
				Name   string `xml:"Name"`
				Config struct {
					PrivateZone bool `xml:"PrivateZone"`
				} `xml:"Config"`
			} `xml:"HostedZones>HostedZone"`
			IsTruncated bool   `xml:"IsTruncated"`
			NextMarker  string `xml:"NextMarker"`
		}
		if err := r.send(ctx, http.MethodGet, "/hostedzone?"+query.Encode(), nil, &answer); err != nil {
			return nil, err
		}
		for _, z := range answer.Zones {
			// A private zone answers only inside one network, which is not
			// where anybody visits an app from.
			if z.Config.PrivateZone {
				continue
			}
			zones = append(zones, Zone{ID: strings.TrimPrefix(z.ID, "/hostedzone/"), Name: Canonical(z.Name)})
		}
		if !answer.IsTruncated || answer.NextMarker == "" {
			break
		}
		marker = answer.NextMarker
	}
	return sortZones(zones), nil
}

func (r *route53) Records(ctx context.Context, zone Zone, name string) ([]Record, error) {
	name = Canonical(name)
	// The listing starts at the name, in Route 53's order, and runs on into
	// the names after it; every set at this name comes first, and there are
	// at most a dozen types. What follows is somebody else's name.
	query := url.Values{"name": {route53Name(name)}, "maxitems": {"50"}}
	var answer struct {
		Sets []r53Set `xml:"ResourceRecordSets>ResourceRecordSet"`
	}
	if err := r.send(ctx, http.MethodGet, "/hostedzone/"+url.PathEscape(zone.ID)+"/rrset?"+query.Encode(), nil, &answer); err != nil {
		return nil, err
	}
	out := []Record{}
	for _, set := range answer.Sets {
		if set.name() == name {
			out = append(out, set.records()...)
		}
	}
	return out, nil
}

// route53Name is a name as Route 53 is asked for it: with its final dot, and
// a wildcard written the way Route 53 stores it.
func route53Name(name string) string {
	name = Canonical(name)
	if strings.HasPrefix(name, "*.") {
		name = `\052` + name[1:]
	}
	return name + "."
}

type r53Change struct {
	Action string `xml:"Action"`
	Set    r53Set `xml:"ResourceRecordSet"`
}

func (r *route53) change(ctx context.Context, zone Zone, comment string, changes ...r53Change) error {
	type request struct {
		XMLName xml.Name `xml:"ChangeResourceRecordSetsRequest"`
		XMLNS   string   `xml:"xmlns,attr"`
		Batch   struct {
			Comment string      `xml:"Comment,omitempty"`
			Changes []r53Change `xml:"Changes>Change"`
		} `xml:"ChangeBatch"`
	}
	var body request
	body.XMLNS = route53Namespace
	body.Batch.Comment = comment
	body.Batch.Changes = changes
	data, err := xml.Marshal(body)
	if err != nil {
		return err
	}
	data = append([]byte(xml.Header), data...)
	err = r.send(ctx, http.MethodPost, "/hostedzone/"+url.PathEscape(zone.ID)+"/rrset/", data, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// Route 53's own words for the two refusals that matter here.
		switch {
		case strings.Contains(apiErr.Message, "but it was not found"):
			return ErrGone
		case strings.Contains(apiErr.Message, "values provided do not match"):
			return ErrChanged
		}
	}
	return err
}

// r53SetOf is one record as a change names it.
func r53SetOf(record Record, content string) r53Set {
	ttl := record.TTL
	if ttl == 0 {
		ttl = route53TTL
	}
	value := content
	if record.Type == "CNAME" {
		value = Canonical(content) + "."
	}
	set := r53Set{Name: route53Name(record.Name), Type: record.Type, TTL: ttl}
	set.ResourceRecords = &struct {
		Values []string `xml:"ResourceRecord>Value"`
	}{Values: []string{value}}
	return set
}

func (r *route53) Create(ctx context.Context, zone Zone, record Record) (Record, error) {
	record.TTL = route53TTL
	if err := r.change(ctx, zone, record.Note, r53Change{Action: "CREATE", Set: r53SetOf(record, record.Content)}); err != nil {
		return Record{}, err
	}
	record.Name = Canonical(record.Name)
	record.ID = record.Name + "/" + record.Type
	record.Content = canonicalContent(record.Type, record.Content)
	return record, nil
}

func (r *route53) SetContent(ctx context.Context, zone Zone, record Record, content string) (Record, error) {
	// The old set is deleted as it is and the new one created in the same
	// batch, which Route 53 applies whole or not at all.
	err := r.change(ctx, zone, record.Note,
		r53Change{Action: "DELETE", Set: r53SetOf(record, record.Content)},
		r53Change{Action: "CREATE", Set: r53SetOf(Record{Name: record.Name, Type: record.Type}, content)})
	if err != nil {
		return Record{}, err
	}
	record.Content = canonicalContent(record.Type, content)
	record.TTL = route53TTL
	return record, nil
}

func (r *route53) Delete(ctx context.Context, zone Zone, record Record) error {
	return r.change(ctx, zone, record.Note, r53Change{Action: "DELETE", Set: r53SetOf(record, record.Content)})
}
