package logdrain

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/netguard"
)

// The hosts the fixtures send to, every one answered by the test's own
// server with a certificate for its name.
var serviceHosts = []string{
	"logs.example.com", "logs-prod-012.grafana.net", "search.example.com", "os.example.com",
	"http-intake.logs.datadoghq.eu", "eu-central-1.aws.edge.axiom.co", "s1234567.eu-nbg-2.betterstackdata.com",
	"log-api.eu.newrelic.com", "logs5.papertrailapp.com",
}

// testCertificate is a certificate for every host above, and the pool that
// trusts it.
func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "log drain test"},
		DNSNames:     serviceHosts,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

type received struct {
	method, path, query string
	header              http.Header
	body                []byte
}

// service is one test server standing in for every service, recording what
// it was sent and answering what the test tells it to.
type service struct {
	mu       sync.Mutex
	requests []received
	answer   func(w http.ResponseWriter, r *http.Request)
}

func (s *service) last(t *testing.T) received {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		t.Fatal("the service was sent nothing")
	}
	return s.requests[len(s.requests)-1]
}

func loopback(ip net.IP) bool { return ip.IsLoopback() || Reachable(ip) }

// newService starts the stand-in and a tester that reaches it for every
// service host, the way the panel reaches each service.
func newService(t *testing.T) (*service, *Tester) {
	t.Helper()
	cert, pool := testCertificate(t)
	svc := &service{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		svc.mu.Lock()
		svc.requests = append(svc.requests, received{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		answer := svc.answer
		svc.mu.Unlock()
		if answer != nil {
			answer(w, r)
			return
		}
		if r.URL.Path == "/_bulk" {
			_, _ = io.WriteString(w, `{"took":1,"errors":false,"items":[{"index":{"status":201}}]}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	t.Cleanup(server.Close)

	tester := &Tester{
		Allowed: loopback, RootCAs: pool, Timeout: 5 * time.Second,
		Now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		dialTo: map[string]string{},
	}
	for _, host := range serviceHosts {
		for _, port := range []string{"443", "9200"} {
			tester.dialTo[net.JoinHostPort(host, port)] = server.Listener.Addr().String()
		}
	}
	return svc, tester
}

func drainByID(t *testing.T, id string) Drain {
	t.Helper()
	for _, d := range everyKind(t) {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no fixture %s", id)
	return Drain{}
}

// The one line every kind is sent: plainly a test, and named as the drain's.
func assertTestLine(t *testing.T, kind string, event map[string]any) {
	t.Helper()
	message, _ := event["message"].(string)
	if !strings.Contains(message, "log drain test") || !strings.Contains(message, "not by an app") {
		t.Errorf("%s was sent %q, which does not say it is a test", kind, message)
	}
	marker, _ := event["skifity"].(map[string]any)
	if marker["test"] != true || marker["kind"] != "test" {
		t.Errorf("%s's line is not marked as a test: %v", kind, marker)
	}
}

// Each kind is sent one line, to the address its sink sends to, with its
// credentials where that service reads them, in the shape its sink sends.
func TestTheTestIsTheRequestTheCollectorMakes(t *testing.T) {
	svc, tester := newService(t)

	checks := map[string]func(r received){
		"ldr_http": func(r received) {
			if r.path != "/ingest" || r.query != "source=skifity" || r.header.Get("Authorization") != fakeHeaderValue ||
				r.header.Get("Content-Type") != "application/x-ndjson" {
				t.Errorf("the generic drain was sent %s?%s with %v", r.path, r.query, r.header)
			}
			var event map[string]any
			if err := json.Unmarshal(r.body, &event); err != nil || !strings.HasSuffix(string(r.body), "\n") {
				t.Fatalf("the generic drain was not sent a JSON line: %q", r.body)
			}
			assertTestLine(t, "http", event)
		},
		"ldr_loki": func(r received) {
			user, password, ok := basic(r.header.Get("Authorization"))
			if r.path != "/loki/api/v1/push" || !ok || user != "123456" || password != fakeLokiPassword ||
				r.header.Get("X-Scope-OrgID") != "acme" {
				t.Errorf("Loki was sent %s with %v", r.path, r.header)
			}
			var push struct {
				Streams []struct {
					Stream map[string]string `json:"stream"`
					Values [][]string        `json:"values"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(r.body, &push); err != nil || len(push.Streams) != 1 || len(push.Streams[0].Values) != 1 {
				t.Fatalf("Loki was not sent a push: %s", r.body)
			}
			var event map[string]any
			_ = json.Unmarshal([]byte(push.Streams[0].Values[0][1]), &event)
			assertTestLine(t, "loki", event)
			if push.Streams[0].Values[0][0] != strconv.FormatInt(tester.Now().UnixNano(), 10) {
				t.Errorf("Loki's timestamp is %s", push.Streams[0].Values[0][0])
			}
		},
		"ldr_es": func(r received) {
			user, password, ok := basic(r.header.Get("Authorization"))
			if r.path != "/_bulk" || !ok || user != "shipper" || password != fakeESPassword {
				t.Errorf("Elasticsearch was sent %s with %v", r.path, r.header)
			}
			lines := strings.Split(strings.TrimSpace(string(r.body)), "\n")
			if len(lines) != 2 || lines[0] != `{"index":{"_index":"skifity-2026.09.30"}}` {
				t.Fatalf("Elasticsearch was sent %q", r.body)
			}
			var event map[string]any
			_ = json.Unmarshal([]byte(lines[1]), &event)
			assertTestLine(t, "elasticsearch", event)
			if event["@timestamp"] == nil {
				t.Error("the document has no @timestamp")
			}
		},
		"ldr_opensearch": func(r received) {
			if r.path != "/_bulk" || r.header.Get("Authorization") != "ApiKey "+fakeESAPIKey {
				t.Errorf("OpenSearch was sent %s with %v", r.path, r.header)
			}
			if !strings.HasPrefix(string(r.body), `{"index":{"_index":"apps-2026.09"}}`) {
				t.Errorf("OpenSearch was sent %q", r.body)
			}
		},
		"ldr_datadog": func(r received) {
			if r.path != "/api/v2/logs" || r.header.Get("DD-API-KEY") != fakeDatadogKey {
				t.Errorf("Datadog was sent %s with %v", r.path, r.header)
			}
			var entries []map[string]any
			if err := json.Unmarshal(r.body, &entries); err != nil || len(entries) != 1 || entries[0]["ddsource"] != "skifity" {
				t.Fatalf("Datadog was sent %s", r.body)
			}
			assertTestLine(t, "datadog", entries[0])
		},
		"ldr_axiom": func(r received) {
			if r.path != "/v1/ingest/apps" || r.header.Get("Authorization") != "Bearer "+fakeAxiomToken ||
				r.header.Get("X-Axiom-Org-Id") != "globex-x1y2" {
				t.Errorf("Axiom was sent %s with %v", r.path, r.header)
			}
		},
		"ldr_better": func(r received) {
			if r.path != "/" || r.header.Get("Authorization") != "Bearer "+fakeBetterToken {
				t.Errorf("Better Stack was sent %s with %v", r.path, r.header)
			}
			var event map[string]any
			if err := json.Unmarshal(r.body, &event); err != nil || event["dt"] == nil {
				t.Fatalf("Better Stack was sent %s", r.body)
			}
			assertTestLine(t, "betterstack", event)
		},
		"ldr_newrelic": func(r received) {
			if r.path != "/log/v1" || r.header.Get("Api-Key") != fakeNewRelicKey {
				t.Errorf("New Relic was sent %s with %v", r.path, r.header)
			}
			var batches []struct {
				Logs []map[string]any `json:"logs"`
			}
			if err := json.Unmarshal(r.body, &batches); err != nil || len(batches) != 1 || len(batches[0].Logs) != 1 {
				t.Fatalf("New Relic was sent %s", r.body)
			}
			assertTestLine(t, "newrelic", batches[0].Logs[0])
		},
	}
	for id, check := range checks {
		d := drainByID(t, id)
		if err := tester.Send(t.Context(), d); err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		request := svc.last(t)
		if request.method != http.MethodPost || !strings.HasPrefix(request.header.Get("User-Agent"), "skifity/") {
			t.Errorf("%s was sent a %s from %q", id, request.method, request.header.Get("User-Agent"))
		}
		check(request)
	}
}

func basic(header string) (string, string, bool) {
	encoded, ok := strings.CutPrefix(header, "Basic ")
	if !ok {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	user, password, ok := strings.Cut(string(raw), ":")
	return user, password, ok
}

// A service that refuses says why, in the service's words, without the
// credential it was sent even when the service repeats it.
func TestARefusalSaysWhyAndNothingSecret(t *testing.T) {
	svc, tester := newService(t)
	svc.answer = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"invalid key `+r.Header.Get("DD-API-KEY")+`"}`)
	}
	err := tester.Send(t.Context(), drainByID(t, "ldr_datadog"))
	var failed *TestFailed
	if !errors.As(err, &failed) || failed.Status != http.StatusForbidden {
		t.Fatalf("a refused key answered %v", err)
	}
	if !strings.Contains(failed.Reason, "refused the credentials") || !strings.Contains(failed.Reason, "invalid key") {
		t.Errorf("the reason is %q", failed.Reason)
	}
	if strings.Contains(failed.Reason, fakeDatadogKey) {
		t.Errorf("the reason hands the key back: %q", failed.Reason)
	}

	// A redirect is reported, not followed with the credentials.
	svc.answer = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example.com/in", http.StatusFound)
	}
	before := len(svc.requests)
	err = tester.Send(t.Context(), drainByID(t, "ldr_axiom"))
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "redirect") || !strings.Contains(failed.Reason, "elsewhere.example.com") {
		t.Errorf("a redirect answered %v", err)
	}
	if len(svc.requests) != before+1 {
		t.Errorf("the redirect was followed: %d requests", len(svc.requests)-before)
	}

	// Elasticsearch takes the request and refuses the document.
	svc.answer = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errors":true,"items":[{"index":{"status":403,"error":{"type":"security_exception",`+
			`"reason":"action [indices:data/write/bulk[s]] is unauthorized for user [shipper]"}}}]}`)
	}
	err = tester.Send(t.Context(), drainByID(t, "ldr_es"))
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "security_exception") || !strings.Contains(failed.Reason, "unauthorized") {
		t.Errorf("a refused document answered %v", err)
	}
}

// The test goes through the same guard as every address the panel is given:
// never this machine, never the metadata service, never inside the cluster.
func TestTheTestIsGuarded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the panel's own address was reached")
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	d := Drain{ID: "ldr_local", TeamID: "team_acme", Name: "local", Kind: KindHTTP,
		Settings: map[string]string{"url": server.URL + "/in"}}
	err := (&Tester{}).Send(t.Context(), d)
	var failed *TestFailed
	if !errors.As(err, &failed) || !strings.Contains(failed.Reason, "this machine itself") {
		t.Errorf("sending to the machine itself answered %v", err)
	}

	for address, allowed := range map[string]bool{
		"169.254.169.254": false, "127.0.0.1": false, "10.42.3.7": false, "10.43.0.10": false,
		"10.0.0.5": true, "192.168.1.20": true, "140.82.121.4": true,
	} {
		if Reachable(net.ParseIP(address)) != allowed {
			t.Errorf("Reachable(%s) = %v", address, !allowed)
		}
	}
	var blocked *netguard.Blocked
	if _, err := (&Tester{}).dial(t.Context(), "tcp", "169.254.169.254:80"); !errors.As(err, &blocked) {
		t.Errorf("the metadata service was dialled: %v", err)
	}
}

// Syslog has no answer: the test proves the address, the TLS handshake and
// the certificate's name, and writes one RFC 5424 line.
func TestSyslogIsTestedOverTLS(t *testing.T) {
	cert, pool := testCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	lines := make(chan string, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
			select {
			case lines <- line:
			default:
			}
		}
	}()

	tester := &Tester{Allowed: loopback, RootCAs: pool, Timeout: 5 * time.Second,
		Now:    func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
		dialTo: map[string]string{"logs5.papertrailapp.com:6514": listener.Addr().String()}}
	if err := tester.Send(t.Context(), drainByID(t, "ldr_syslog")); err != nil {
		t.Fatalf("syslog: %v", err)
	}
	select {
	case line := <-lines:
		if !strings.HasPrefix(line, "<14>1 2026-09-30T12:00:00.000000Z skifity skifity - - - ") ||
			!strings.Contains(line, "log drain test") {
			t.Errorf("syslog was sent %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("syslog was sent nothing")
	}

	// A certificate for another name is refused, as the collector refuses it.
	tester.RootCAs = x509.NewCertPool()
	var failed *TestFailed
	if err := tester.Send(t.Context(), drainByID(t, "ldr_syslog")); !errors.As(err, &failed) ||
		!strings.Contains(failed.Reason, "TLS") {
		t.Errorf("an untrusted certificate answered %v", err)
	}
}
