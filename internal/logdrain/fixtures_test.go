package logdrain

import "testing"

// Every credential here is made up, and shaped so a test can find it again in
// whatever it should or should not be in.
const (
	fakeHeaderValue  = "Bearer not-a-real-http-token-0001"
	fakeLokiPassword = "glc_not-a-real-loki-token-0002"
	fakeESPassword   = "not-a-real-es-password-0003"
	fakeDatadogKey   = "notarealdatadogkey00000000000004"
	fakeAxiomToken   = "xaat-not-a-real-axiom-token-0005"
	fakeBetterToken  = "not-a-real-betterstack-token-0006"
	fakeNewRelicKey  = "NRAK-NOTAREALLICENSEKEY0000007"
	fakeESAPIKey     = "bm90LWEtcmVhbC1lcy1hcGkta2V5LTAwMDg="
)

// fakeSecrets is every secret above, for a test that looks for any of them.
var fakeSecrets = []string{
	fakeHeaderValue, fakeLokiPassword, fakeESPassword, fakeDatadogKey,
	fakeAxiomToken, fakeBetterToken, fakeNewRelicKey, fakeESAPIKey,
}

// everyKind is one drain of each kind, with its credentials, as the panel
// would hand them to Render.
func everyKind(t *testing.T) []Drain {
	t.Helper()
	drains := []Drain{
		{ID: "ldr_http", TeamID: "team_acme", Name: "Receiver", Kind: KindHTTP,
			Settings: map[string]string{"url": "https://logs.example.com/ingest?source=skifity", "header_name": "Authorization"},
			Secrets:  map[string]string{"header_value": fakeHeaderValue}},
		{ID: "ldr_loki", TeamID: "team_acme", Name: "Grafana", Kind: KindLoki,
			Settings: map[string]string{"url": "https://logs-prod-012.grafana.net", "username": "123456", "tenant_id": "acme"},
			Secrets:  map[string]string{"password": fakeLokiPassword},
			Scoped:   true, Projects: []string{"prj_shop", "prj_blog"}},
		{ID: "ldr_es", TeamID: "team_acme", Name: "Search", Kind: KindElasticsearch,
			Settings: map[string]string{"url": "https://search.example.com:9200", "index": DefaultIndex, "api": "7", "username": "shipper"},
			Secrets:  map[string]string{"password": fakeESPassword}, IncludeBuilds: true},
		{ID: "ldr_opensearch", TeamID: "team_initech", Name: "OpenSearch", Kind: KindElasticsearch,
			Settings: map[string]string{"url": "https://os.example.com/_bulk", "index": "apps-%Y.%m", "api": "8"},
			Secrets:  map[string]string{"api_key": fakeESAPIKey}},
		{ID: "ldr_datadog", TeamID: "team_globex", Name: "Datadog", Kind: KindDatadog,
			Settings: map[string]string{"site": "datadoghq.eu"},
			Secrets:  map[string]string{"api_key": fakeDatadogKey}},
		{ID: "ldr_axiom", TeamID: "team_globex", Name: "Axiom", Kind: KindAxiom,
			Settings: map[string]string{"dataset": "apps", "region": "eu-central-1.aws.edge.axiom.co", "org_id": "globex-x1y2"},
			Secrets:  map[string]string{"token": fakeAxiomToken}},
		{ID: "ldr_better", TeamID: "team_globex", Name: "Better Stack", Kind: KindBetterStack,
			Settings: map[string]string{"host": "s1234567.eu-nbg-2.betterstackdata.com"},
			Secrets:  map[string]string{"source_token": fakeBetterToken}},
		{ID: "ldr_newrelic", TeamID: "team_initech", Name: "New Relic", Kind: KindNewRelic,
			Settings: map[string]string{"region": "eu", "account_id": "1234567"},
			Secrets:  map[string]string{"license_key": fakeNewRelicKey}},
		{ID: "ldr_syslog", TeamID: "team_initech", Name: "Papertrail", Kind: KindSyslog,
			Settings: map[string]string{"host": "logs5.papertrailapp.com", "port": "6514"}},
	}
	for _, d := range drains {
		if err := d.Validate(); err != nil {
			t.Fatalf("the fixture %s is not a valid drain: %v", d.ID, err)
		}
	}
	return drains
}

func everyName() []NameRow {
	return []NameRow{
		{Kind: "team", ID: "team_acme", Name: "Acme, \"the\" shop"},
		{Kind: "project", ID: "prj_shop", Name: "Shop"},
		{Kind: "environment", ID: "acme-shop-production", Name: "Production"},
		{Kind: "app", ID: "app_web", Name: "Web {{ not a template }}"},
	}
}
