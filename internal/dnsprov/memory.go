package dnsprov

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Memory is a DNS provider that keeps its zones in memory.
//
// It is what the panel's own tests connect a team to — here, in internal/api
// and in the CLI's — so that the rules about whose record is whose are tested
// through the same Manager the panel runs, with no network. It behaves like
// the providers it stands in for where that matters: ids are its own, a
// record at a name it does not have is ErrGone, and notes are kept only when
// it is told it keeps them. Nothing in the panel constructs one.
type Memory struct {
	// Named is the kind it answers as: cloudflare, hetzner and so on.
	Named string
	// Token is the credential it accepts; any other is refused as a
	// provider refuses one.
	Token string

	mu      sync.Mutex
	zones   []Zone
	records map[string][]Record // by zone id
	next    int
	// Fail, when set, is what every request answers.
	Fail error
	// Asked counts the requests, by method name.
	Asked map[string]int
}

// NewMemory makes one with the given zones, each its name as its id.
func NewMemory(kind, token string, zones ...string) *Memory {
	m := &Memory{Named: kind, Token: token, records: map[string][]Record{}, Asked: map[string]int{}}
	for _, name := range zones {
		m.zones = append(m.zones, Zone{ID: "zone-" + name, Name: Canonical(name)})
	}
	return m
}

// Opener is the Manager.Open that signs in to this provider.
func (m *Memory) Opener() func(kind string, creds Credentials) (Provider, error) {
	return func(kind string, creds Credentials) (Provider, error) {
		if err := CheckCredentials(kind, creds); err != nil {
			return nil, err
		}
		return &memorySession{Memory: m, creds: creds}, nil
	}
}

// Put adds a record somebody else made, as it would be at the provider.
func (m *Memory) Put(r Record) Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.put(r)
}

func (m *Memory) put(r Record) Record {
	m.next++
	if r.ID == "" {
		r.ID = fmt.Sprintf("mem%d", m.next)
	}
	r.Name = Canonical(r.Name)
	r.Type = strings.ToUpper(r.Type)
	r.Content = canonicalContent(r.Type, r.Content)
	if !KeepsNotes(m.Named) {
		r.Note = ""
	}
	zone := m.zoneOf(r.Name)
	m.records[zone] = append(m.records[zone], r)
	return r
}

// Edit changes a record's value, as somebody at the provider's dashboard.
func (m *Memory) Edit(name, kind, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for zone, list := range m.records {
		for i, r := range list {
			if r.Name == Canonical(name) && r.Type == kind {
				m.records[zone][i].Content = canonicalContent(kind, content)
			}
		}
	}
}

// All is every record at a name.
func (m *Memory) All(name string) []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, list := range m.records {
		for _, r := range list {
			if r.Name == Canonical(name) {
				out = append(out, r)
			}
		}
	}
	return out
}

func (m *Memory) zoneOf(name string) string {
	names := make([]string, len(m.zones))
	for i, z := range m.zones {
		names[i] = z.Name
	}
	if i := MatchZone(name, names); i >= 0 {
		return m.zones[i].ID
	}
	return ""
}

type memorySession struct {
	*Memory
	creds Credentials
}

func (s *memorySession) Kind() string { return s.Named }

func (s *memorySession) check(method string) error {
	s.Asked[method]++
	if s.Fail != nil {
		return s.Fail
	}
	if s.creds.Token != s.Token && s.creds.SecretAccessKey != s.Token {
		return &APIError{Provider: s.Named, Status: 403, Message: "Authentication error"}
	}
	return nil
}

func (s *memorySession) Zones(context.Context) ([]Zone, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check("Zones"); err != nil {
		return nil, err
	}
	return append([]Zone(nil), s.zones...), nil
}

func (s *memorySession) Records(_ context.Context, zone Zone, name string) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check("Records"); err != nil {
		return nil, err
	}
	out := []Record{}
	for _, r := range s.records[zone.ID] {
		if r.Name == Canonical(name) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *memorySession) Create(_ context.Context, zone Zone, r Record) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check("Create"); err != nil {
		return Record{}, err
	}
	r.ID = ""
	return s.put(r), nil
}

func (s *memorySession) SetContent(_ context.Context, zone Zone, r Record, content string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check("SetContent"); err != nil {
		return Record{}, err
	}
	for i, found := range s.records[zone.ID] {
		if found.ID == r.ID && found.Type == r.Type {
			s.records[zone.ID][i].Content = canonicalContent(r.Type, content)
			return s.records[zone.ID][i], nil
		}
	}
	return Record{}, ErrGone
}

func (s *memorySession) Delete(_ context.Context, zone Zone, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check("Delete"); err != nil {
		return err
	}
	list := s.records[zone.ID]
	for i, found := range list {
		if found.ID == r.ID && found.Type == r.Type {
			s.records[zone.ID] = append(list[:i:i], list[i+1:]...)
			return nil
		}
	}
	return ErrGone
}
