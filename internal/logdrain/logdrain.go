// Package logdrain ships the logs of a team's apps to a service outside the
// cluster: a log drain.
//
// Three things live here, and they share one idea of where a drain sends.
//
//   - The kinds of drain and what each one asks for, and the checks a drain's
//     settings are held to before anything is kept.
//   - The configuration of the collector: Vector, one per server, reading
//     every managed namespace's container logs and routing each line to the
//     drains of the team it belongs to. See vector.go and objects.go.
//   - The test a drain is given before it is saved: one clearly labelled line,
//     sent by the panel itself, to the same address, with the same
//     credentials, the collector will use. See tester.go.
//
// "The same address" is not a figure of speech: Endpoint is the only place a
// drain's address is worked out, and the collector's sink, the panel's test
// and the context its credentials are sealed under all read it.
package logdrain

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// The kinds of drain.
const (
	KindHTTP          = "http"
	KindLoki          = "loki"
	KindElasticsearch = "elasticsearch"
	KindDatadog       = "datadog"
	KindAxiom         = "axiom"
	KindBetterStack   = "betterstack"
	KindNewRelic      = "newrelic"
	KindSyslog        = "syslog"
)

// Field is one thing a kind asks for.
//
// It carries no words: the panel has them translated, keyed by the kind and
// the field. The CLI and the API describe them in docs/log-drains.md.
type Field struct {
	Key string `json:"key"`
	// Secret fields are sealed, never answered by any API, and kept when a
	// change leaves them empty.
	Secret   bool `json:"secret,omitempty"`
	Required bool `json:"required,omitempty"`
	// Options makes the field a choice between these values.
	Options []string `json:"options,omitempty"`
	// Default is what an empty value becomes.
	Default string `json:"default,omitempty"`
	// Placeholder is an example of the value, where one helps.
	Placeholder string `json:"placeholder,omitempty"`
}

// KindSpec is a kind of drain and its form.
type KindSpec struct {
	Kind   string  `json:"kind"`
	Fields []Field `json:"fields"`
}

// DatadogSites are Datadog's sites, each its own intake. Chosen from a list
// rather than typed: a site is part of the address the API key is sent to.
var DatadogSites = []string{
	"datadoghq.com", "us3.datadoghq.com", "us5.datadoghq.com",
	"datadoghq.eu", "ap1.datadoghq.com", "ap2.datadoghq.com", "ddog-gov.com",
}

// DefaultIndex is the Elasticsearch index a drain writes to when it names
// none: one a day, which is what index lifecycle policies expect.
const DefaultIndex = "skifity-%Y.%m.%d"

// DefaultSyslogPort is syslog over TLS's registered port (RFC 5425).
const DefaultSyslogPort = "6514"

// DefaultBetterStackHost is Better Stack's shared ingesting host. A source
// made since 2024 names its own on the source's page, and that one is better.
const DefaultBetterStackHost = "in.logs.betterstack.com"

var kinds = []KindSpec{
	{Kind: KindHTTP, Fields: []Field{
		{Key: "url", Required: true, Placeholder: "https://logs.example.com/ingest"},
		{Key: "header_name", Placeholder: "Authorization"},
		{Key: "header_value", Secret: true},
	}},
	{Kind: KindLoki, Fields: []Field{
		{Key: "url", Required: true, Placeholder: "https://logs-prod-012.grafana.net"},
		{Key: "username"},
		{Key: "password", Secret: true},
		{Key: "tenant_id"},
	}},
	{Kind: KindElasticsearch, Fields: []Field{
		{Key: "url", Required: true, Placeholder: "https://search.example.com:9200"},
		{Key: "index", Default: DefaultIndex},
		{Key: "api", Options: []string{"8", "7"}, Default: "8"},
		{Key: "username"},
		{Key: "password", Secret: true},
		{Key: "api_key", Secret: true},
	}},
	{Kind: KindDatadog, Fields: []Field{
		{Key: "site", Options: DatadogSites, Default: DatadogSites[0]},
		{Key: "api_key", Secret: true, Required: true},
	}},
	{Kind: KindAxiom, Fields: []Field{
		{Key: "dataset", Required: true, Placeholder: "apps"},
		{Key: "region", Placeholder: "eu-central-1.aws.edge.axiom.co"},
		{Key: "org_id"},
		{Key: "token", Secret: true, Required: true},
	}},
	{Kind: KindBetterStack, Fields: []Field{
		{Key: "host", Required: true, Default: DefaultBetterStackHost, Placeholder: "s1234567.eu-nbg-2.betterstackdata.com"},
		{Key: "source_token", Secret: true, Required: true},
	}},
	{Kind: KindNewRelic, Fields: []Field{
		{Key: "region", Options: []string{"us", "eu"}, Default: "us"},
		{Key: "account_id", Required: true},
		{Key: "license_key", Secret: true, Required: true},
	}},
	{Kind: KindSyslog, Fields: []Field{
		{Key: "host", Required: true, Placeholder: "logs.papertrailapp.com"},
		{Key: "port", Default: DefaultSyslogPort},
	}},
}

// Kinds is every kind of drain, in the order the form offers them.
func Kinds() []KindSpec {
	out := make([]KindSpec, len(kinds))
	for i, kind := range kinds {
		out[i] = KindSpec{Kind: kind.Kind, Fields: slices.Clone(kind.Fields)}
	}
	return out
}

// Spec is one kind's form.
func Spec(kind string) (KindSpec, bool) {
	for _, spec := range kinds {
		if spec.Kind == kind {
			return spec, true
		}
	}
	return KindSpec{}, false
}

// Drain is a drain with its credentials open, as the collector's
// configuration and the test are made from it.
type Drain struct {
	ID     string
	TeamID string
	Name   string
	Kind   string
	// Settings are the fields that are not secret, defaults filled in.
	Settings map[string]string
	// Secrets are the secret fields that have a value.
	Secrets map[string]string
	// Scoped limits the drain to Projects. A scoped drain whose projects
	// are all gone sends nothing.
	Scoped   bool
	Projects []string
	// IncludeBuilds sends the build logs of the team's apps too.
	IncludeBuilds bool
}

// Invalid is a drain's settings being refused, and why, in a sentence.
type Invalid struct{ Reason string }

func (e *Invalid) Error() string { return e.Reason }

func invalid(format string, args ...any) error {
	return &Invalid{Reason: fmt.Sprintf(format, args...)}
}

// maxValue bounds any one value. A token is a few hundred characters; a
// value of kilobytes is a paste gone wrong.
const maxValue = 4096

// secretReference is what Vector reads as a reference to a secret store,
// SECRET[backend.name], wherever it appears in its configuration. A value
// holding one would make the collector refuse its configuration, or look a
// secret up, so no value may.
var secretReference = regexp.MustCompile(`SECRET\[[[:word:]\-]+\.[[:word:].\-/]+\]`)

// Split separates what a form sent into settings and secrets, fills in
// defaults, and checks both. stored is the secrets already kept, so a change
// that leaves one empty keeps it; nil for a new drain.
func Split(kind string, values, stored map[string]string) (settings, secrets map[string]string, err error) {
	spec, ok := Spec(kind)
	if !ok {
		names := make([]string, len(kinds))
		for i, k := range kinds {
			names[i] = k.Kind
		}
		return nil, nil, invalid("%q is not a kind of drain; the kinds are %s", kind, strings.Join(names, ", "))
	}
	known := map[string]Field{}
	for _, field := range spec.Fields {
		known[field.Key] = field
	}
	for key := range values {
		if _, ok := known[key]; !ok {
			return nil, nil, invalid("a %s drain has no setting called %q", kind, key)
		}
	}

	settings, secrets = map[string]string{}, map[string]string{}
	for _, field := range spec.Fields {
		value := strings.TrimSpace(values[field.Key])
		if field.Secret {
			if value == "" {
				value = stored[field.Key]
			}
			if value != "" {
				secrets[field.Key] = value
			}
			continue
		}
		if value == "" {
			value = field.Default
		}
		if value != "" {
			settings[field.Key] = value
		}
	}
	d := Drain{Kind: kind, Settings: settings, Secrets: secrets}
	if err := d.Validate(); err != nil {
		return nil, nil, err
	}
	return settings, secrets, nil
}

// Validate checks a drain's settings and secrets as they would be sent.
func (d Drain) Validate() error {
	spec, ok := Spec(d.Kind)
	if !ok {
		return invalid("%q is not a kind of drain", d.Kind)
	}
	for _, field := range spec.Fields {
		value := d.value(field)
		if field.Required && value == "" {
			return invalid("a %s drain needs %s", d.Kind, field.Key)
		}
		if value == "" {
			continue
		}
		if len(value) > maxValue {
			return invalid("%s is longer than %d characters", field.Key, maxValue)
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return invalid("%s holds a line break or another control character", field.Key)
		}
		if secretReference.MatchString(value) {
			return invalid("%s holds SECRET[…], which the collector would read as a reference to a secret store", field.Key)
		}
		if len(field.Options) > 0 && !slices.Contains(field.Options, value) {
			return invalid("%s is one of %s, not %q", field.Key, strings.Join(field.Options, ", "), value)
		}
	}
	if err := d.validateKind(); err != nil {
		return err
	}
	_, err := d.Endpoint()
	return err
}

func (d Drain) value(field Field) string {
	if field.Secret {
		return d.Secrets[field.Key]
	}
	return d.Settings[field.Key]
}

// Credentialed reports whether the drain has any credential at all.
func (d Drain) Credentialed() bool { return len(d.Secrets) > 0 }

var (
	// headerName is an HTTP token (RFC 9110).
	headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	// datasetName is what Axiom allows a dataset to be called.
	datasetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	// plainValue is a value that reaches the collector's configuration as a
	// label or a header and has no business holding anything else.
	plainValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,149}$`)
	// indexName is an Elasticsearch index name, with %Y, %m and %d, which
	// the collector fills in from each line's time.
	indexName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	digits    = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// reservedHeaders are headers a drain may not set: the ones the transport
// owns, which a stray value would break the request with.
var reservedHeaders = []string{
	"host", "content-length", "content-type", "content-encoding", "transfer-encoding",
	"connection", "keep-alive", "upgrade", "te", "trailer", "expect",
}

func (d Drain) validateKind() error {
	s := d.Settings
	switch d.Kind {
	case KindHTTP:
		name, value := s["header_name"], d.Secrets["header_value"]
		if name == "" && value != "" {
			return invalid("the header's value was given without its name")
		}
		if name != "" {
			if value == "" {
				return invalid("the header %s was named without a value", name)
			}
			if !headerName.MatchString(name) || len(name) > 64 {
				return invalid("%q is not a header name", name)
			}
			if slices.Contains(reservedHeaders, strings.ToLower(name)) {
				return invalid("the header %s is set by the collector itself", name)
			}
			if err := templateSafe("the header's value", value); err != nil {
				return err
			}
		}
	case KindLoki:
		if s["username"] != "" && d.Secrets["password"] == "" {
			return invalid("a username was given without its password or token")
		}
		if tenant := s["tenant_id"]; tenant != "" && !plainValue.MatchString(tenant) {
			return invalid("%q is not a tenant id: letters, digits, dots, dashes and underscores", tenant)
		}
	case KindElasticsearch:
		if err := validIndex(s["index"]); err != nil {
			return err
		}
		basic, key := s["username"] != "" || d.Secrets["password"] != "", d.Secrets["api_key"] != ""
		if basic && key {
			return invalid("give a username and password, or an API key, not both")
		}
		if basic && (s["username"] == "" || d.Secrets["password"] == "") {
			return invalid("a username and a password go together")
		}
		if key {
			if err := templateSafe("the API key", d.Secrets["api_key"]); err != nil {
				return err
			}
		}
	case KindAxiom:
		if !datasetName.MatchString(s["dataset"]) {
			return invalid("%q is not an Axiom dataset name", s["dataset"])
		}
		if region := s["region"]; region != "" && !validHostname(region) {
			return invalid("the region is a host name such as eu-central-1.aws.edge.axiom.co, not %q", region)
		}
		if org := s["org_id"]; org != "" && !plainValue.MatchString(org) {
			return invalid("%q is not an Axiom organization id", org)
		}
	case KindBetterStack:
		if !validHostname(s["host"]) {
			return invalid("the ingesting host is a host name such as %s, not %q", DefaultBetterStackHost, s["host"])
		}
	case KindNewRelic:
		if !digits.MatchString(s["account_id"]) {
			return invalid("a New Relic account id is a number, not %q", s["account_id"])
		}
	case KindSyslog:
		host := s["host"]
		if !validHostname(host) && net.ParseIP(host) == nil {
			return invalid("%q is not a host name or an address", host)
		}
		port, err := strconv.Atoi(s["port"])
		if err != nil || port < 1 || port > 65535 {
			return invalid("%q is not a port", s["port"])
		}
	}
	return nil
}

// templateSafe refuses a value the collector would read as a template rather
// than send as it is: {{ field }} is a field of the line and %Y a year.
func templateSafe(what, value string) error {
	if strings.ContainsAny(value, "{}%") {
		return invalid("%s holds {, } or %%, which the collector would read as a template; it cannot be sent", what)
	}
	return nil
}

// validIndex checks an index name, with the date in it.
func validIndex(index string) error {
	if len(index) > 200 {
		return invalid("the index name is longer than 200 characters")
	}
	plain := strings.NewReplacer("%Y", "0000", "%m", "00", "%d", "00").Replace(index)
	if !indexName.MatchString(plain) {
		return invalid("%q is not an index name: lowercase letters, digits, dots, dashes and underscores, with %%Y, %%m and %%d for the date", index)
	}
	return nil
}

// validHostname reports whether a string is a DNS name.
func validHostname(host string) bool {
	if host == "" || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// credentialQuery are query parameters that carry a credential, which
// belongs in a sealed field and not in an address shown to every viewer.
var credentialQuery = []string{
	"token", "access_token", "api_key", "apikey", "key", "secret", "password", "auth", "signature", "sig",
}

// parseAddress reads a drain's http(s) address.
//
// https always; plain http only for a drain with no credential, so a
// credential is never sent in the clear. No credential in the address, no
// template characters, and no query string except on the generic kind, where
// a receiver may want one.
func (d Drain) parseAddress(raw string, query bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return nil, invalid("%q is not an address such as https://logs.example.com", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if d.Credentialed() {
			return nil, invalid("%s sends a credential over plain http; give its https address", raw)
		}
	default:
		return nil, invalid("%q is not an http or https address", raw)
	}
	if u.User != nil {
		return nil, invalid("the address carries a username or password; put the credential in its own field, where it is sealed")
	}
	if u.Fragment != "" || strings.ContainsAny(raw, "{}%") {
		return nil, invalid("%q holds #, {, } or %%, which the collector cannot send to as it is", raw)
	}
	if u.RawQuery != "" {
		if !query {
			return nil, invalid("the address has a query string, which a %s drain does not take", d.Kind)
		}
		for key := range u.Query() {
			if slices.Contains(credentialQuery, strings.ToLower(key)) {
				return nil, invalid("the address carries %s= in its query; put the credential in the header, where it is sealed", key)
			}
		}
	}
	host := u.Hostname()
	if !validHostname(host) && net.ParseIP(host) == nil {
		return nil, invalid("%q is not a host name or an address", host)
	}
	return u, nil
}

// Endpoint is where a drain's lines go.
type Endpoint struct {
	// URL is the address of an HTTP drain's requests.
	URL *url.URL
	// Address is host:port, for syslog.
	Address string
	// Host is the name the certificate is checked against, for syslog.
	Host string
}

// String is the address, as the panel shows it and as the credentials are
// sealed with.
func (e Endpoint) String() string {
	if e.URL != nil {
		return e.URL.String()
	}
	return "tls://" + e.Address
}

// Endpoint works out where a drain sends. It is the one place that does: the
// collector's sink, the panel's test and the seal on the credentials all read
// it, so the three cannot disagree about where the credentials go.
func (d Drain) Endpoint() (Endpoint, error) {
	s := d.Settings
	switch d.Kind {
	case KindHTTP:
		u, err := d.parseAddress(s["url"], true)
		if err != nil {
			return Endpoint{}, err
		}
		return Endpoint{URL: u}, nil
	case KindLoki:
		u, err := d.parseAddress(s["url"], false)
		if err != nil {
			return Endpoint{}, err
		}
		// The push path is the collector's to add; an address given with
		// it is the same address.
		prefix := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), lokiPushPath)
		u.Path = strings.TrimSuffix(prefix, "/") + lokiPushPath
		return Endpoint{URL: u}, nil
	case KindElasticsearch:
		u, err := d.parseAddress(s["url"], false)
		if err != nil {
			return Endpoint{}, err
		}
		u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/_bulk") + "/_bulk"
		return Endpoint{URL: u}, nil
	case KindDatadog:
		// The same address Vector's datadog_logs sink sends to.
		return Endpoint{URL: &url.URL{Scheme: "https", Host: "http-intake.logs." + s["site"], Path: "/api/v2/logs"}}, nil
	case KindAxiom:
		// The same address Vector's axiom sink builds: the regional edge
		// when there is one, the cloud's own otherwise.
		if region := s["region"]; region != "" {
			return Endpoint{URL: &url.URL{Scheme: "https", Host: region, Path: "/v1/ingest/" + s["dataset"]}}, nil
		}
		return Endpoint{URL: &url.URL{Scheme: "https", Host: "api.axiom.co", Path: "/v1/datasets/" + s["dataset"] + "/ingest"}}, nil
	case KindBetterStack:
		return Endpoint{URL: &url.URL{Scheme: "https", Host: s["host"], Path: "/"}}, nil
	case KindNewRelic:
		host := "log-api.newrelic.com"
		if s["region"] == "eu" {
			host = "log-api.eu.newrelic.com"
		}
		return Endpoint{URL: &url.URL{Scheme: "https", Host: host, Path: "/log/v1"}}, nil
	case KindSyslog:
		return Endpoint{Address: net.JoinHostPort(s["host"], s["port"]), Host: s["host"]}, nil
	}
	return Endpoint{}, invalid("%q is not a kind of drain", d.Kind)
}

// Address is where a drain of this kind with these settings sends, as its
// credentials are sealed with: worked out from the settings alone, so the
// seal can be checked before the credentials are opened.
func Address(kind string, settings map[string]string) (string, error) {
	endpoint, err := Drain{Kind: kind, Settings: settings}.Endpoint()
	if err != nil {
		return "", err
	}
	return endpoint.String(), nil
}

// Component is the name the collector's state is recorded under.
const Component = "log-collector"

// MaxPerTeam is how many drains a team may have. Every one is a sink in the
// collector on every server, and the number is far above what anybody
// sends one team's logs to.
const MaxPerTeam = 10

// lokiPushPath is Loki's push API.
const lokiPushPath = "/loki/api/v1/push"

// Destination is where a drain sends, for a list any member reads: the
// address without its query string, which is the one part of it that may
// say more than where.
func (d Drain) Destination() string {
	endpoint, err := d.Endpoint()
	if err != nil {
		return ""
	}
	if endpoint.URL == nil {
		return endpoint.String()
	}
	shown := *endpoint.URL
	shown.RawQuery = ""
	return shown.String()
}
