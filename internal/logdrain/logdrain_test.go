package logdrain

import (
	"errors"
	"strings"
	"testing"
)

// What a form sends is checked before anything is kept, and the reason says
// which setting is wrong.
func TestADrainsSettingsAreCheckedBeforeTheyAreKept(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		values map[string]string
		why    string
	}{
		{"an unknown kind", "splunk", map[string]string{}, "not a kind of drain"},
		{"a setting the kind does not have", KindHTTP, map[string]string{"url": "https://a.example.com", "index": "x"}, "no setting called"},
		{"a missing address", KindHTTP, map[string]string{}, "needs url"},
		{"a credential over plain http", KindHTTP,
			map[string]string{"url": "http://logs.example.com/in", "header_name": "Authorization", "header_value": "Bearer x"}, "plain http"},
		{"a credential in the address", KindLoki,
			map[string]string{"url": "https://user:pass@logs.example.com"}, "username or password"},
		{"a credential in the query", KindHTTP,
			map[string]string{"url": "https://logs.example.com/in?token=abc"}, "token="},
		{"a query string on Loki", KindLoki, map[string]string{"url": "https://logs.example.com/?x=1"}, "query string"},
		{"a template in the address", KindHTTP, map[string]string{"url": "https://logs.example.com/{{ app }}"}, "{"},
		{"a header named without a value", KindHTTP,
			map[string]string{"url": "https://logs.example.com", "header_name": "X-Key"}, "without a value"},
		{"a header the transport owns", KindHTTP,
			map[string]string{"url": "https://logs.example.com", "header_name": "Content-Type", "header_value": "x"}, "set by the collector"},
		{"a template in a header", KindHTTP,
			map[string]string{"url": "https://logs.example.com", "header_name": "X-Key", "header_value": "{{ secret }}"}, "template"},
		{"a reference to a secret store", KindAxiom,
			map[string]string{"dataset": "apps", "token": "SECRET[env.HOME]"}, "SECRET"},
		{"a line break", KindAxiom, map[string]string{"dataset": "apps", "token": "abc\ndef: x"}, "control character"},
		{"an Axiom dataset with a slash", KindAxiom, map[string]string{"dataset": "a/b", "token": "x"}, "dataset name"},
		{"a Datadog site nobody runs", KindDatadog, map[string]string{"site": "evil.example.com", "api_key": "x"}, "one of"},
		{"a New Relic account that is not a number", KindNewRelic,
			map[string]string{"account_id": "abc", "license_key": "x"}, "is a number"},
		{"an index with capitals", KindElasticsearch, map[string]string{"url": "https://es.example.com", "index": "Logs"}, "index name"},
		{"two ways to sign in to Elasticsearch", KindElasticsearch,
			map[string]string{"url": "https://es.example.com", "username": "u", "password": "p", "api_key": "k"}, "not both"},
		{"a syslog port that is not one", KindSyslog, map[string]string{"host": "logs.example.com", "port": "99999"}, "not a port"},
		{"a syslog host with a path", KindSyslog, map[string]string{"host": "logs.example.com/x"}, "not a host name"},
		{"a Loki username without a password", KindLoki, map[string]string{"url": "https://l.example.com", "username": "u"}, "without its password"},
	}
	for _, c := range cases {
		_, _, err := Split(c.kind, c.values, nil)
		var refused *Invalid
		if !errors.As(err, &refused) {
			t.Errorf("%s: accepted (%v)", c.name, err)
			continue
		}
		if !strings.Contains(refused.Reason, c.why) {
			t.Errorf("%s: refused with %q, which does not say %q", c.name, refused.Reason, c.why)
		}
	}
}

// Defaults are filled in, secrets are kept apart from settings, and a secret
// a change leaves empty is the one already stored.
func TestSplitKeepsSecretsApartAndKeepsTheStoredOnes(t *testing.T) {
	settings, secrets, err := Split(KindDatadog, map[string]string{"api_key": fakeDatadogKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if settings["site"] != "datadoghq.com" || secrets["api_key"] != fakeDatadogKey || settings["api_key"] != "" {
		t.Errorf("split into %v and %v", settings, secrets)
	}
	settings, secrets, err = Split(KindDatadog, map[string]string{"site": "datadoghq.eu", "api_key": ""},
		map[string]string{"api_key": fakeDatadogKey})
	if err != nil {
		t.Fatal(err)
	}
	if settings["site"] != "datadoghq.eu" || secrets["api_key"] != fakeDatadogKey {
		t.Errorf("an empty secret did not keep the stored one: %v %v", settings, secrets)
	}
	// Plain http is fine for a receiver that asks for nothing.
	if _, _, err := Split(KindHTTP, map[string]string{"url": "http://10.0.0.5:8080/in"}, nil); err != nil {
		t.Errorf("a plain http receiver with no credential was refused: %v", err)
	}
}

// Endpoint is the one place a drain's address is worked out, and it is the
// address each service's own documentation gives.
func TestEachKindSendsWhereItsServiceListens(t *testing.T) {
	want := map[string]string{
		"ldr_http":       "https://logs.example.com/ingest?source=skifity",
		"ldr_loki":       "https://logs-prod-012.grafana.net/loki/api/v1/push",
		"ldr_es":         "https://search.example.com:9200/_bulk",
		"ldr_opensearch": "https://os.example.com/_bulk",
		"ldr_datadog":    "https://http-intake.logs.datadoghq.eu/api/v2/logs",
		"ldr_axiom":      "https://eu-central-1.aws.edge.axiom.co/v1/ingest/apps",
		"ldr_better":     "https://s1234567.eu-nbg-2.betterstackdata.com/",
		"ldr_newrelic":   "https://log-api.eu.newrelic.com/log/v1",
		"ldr_syslog":     "tls://logs5.papertrailapp.com:6514",
	}
	for _, d := range everyKind(t) {
		endpoint, err := d.Endpoint()
		if err != nil {
			t.Errorf("%s: %v", d.ID, err)
			continue
		}
		if endpoint.String() != want[d.ID] {
			t.Errorf("%s sends to %s, want %s", d.ID, endpoint, want[d.ID])
		}
	}

	// An address given with the push path is the same address.
	loki := Drain{Kind: KindLoki, Settings: map[string]string{"url": "https://loki.example.com/loki/api/v1/push/"}}
	if endpoint, err := loki.Endpoint(); err != nil || endpoint.String() != "https://loki.example.com/loki/api/v1/push" {
		t.Errorf("the push path was added twice: %v %v", endpoint, err)
	}
	// Axiom's own cloud without a region.
	axiom := Drain{Kind: KindAxiom, Settings: map[string]string{"dataset": "apps"}, Secrets: map[string]string{"token": "x"}}
	if endpoint, _ := axiom.Endpoint(); endpoint.String() != "https://api.axiom.co/v1/datasets/apps/ingest" {
		t.Errorf("Axiom without a region sends to %s", endpoint)
	}

	// Anybody in the team sees where a drain sends, never its query string.
	if got := everyKind(t)[0].Destination(); got != "https://logs.example.com/ingest" {
		t.Errorf("the destination shown is %q", got)
	}
}
