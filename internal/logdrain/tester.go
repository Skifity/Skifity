package logdrain

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/netguard"
	"skifity/internal/version"
)

// The test a drain is given before it is saved.
//
// The panel sends one line itself, to the address Endpoint works out, with
// the credentials the collector will be given and in the shape the
// collector's sink sends — the same request, as near as one line can be to a
// batch of them. What that proves is the address, the credentials and that
// this network can reach the service. It does not prove the collector: that
// is a different process on every server, and whether it is running is what
// the drain's status says.
//
// The request goes through the same guard as every address the panel is
// given (internal/netguard): never the machine itself, never the cloud's
// metadata service. And it follows no redirect, as the collector follows
// none — a service that answers with one would never receive a line.

// TestTimeout bounds one test, start to finish.
const TestTimeout = 15 * time.Second

// maxAnswer is how much of a service's answer is read to say why it refused.
const maxAnswer = 64 << 10

// Tester sends the test line. The zero value is what the panel uses.
type Tester struct {
	// Allowed decides which addresses may be dialled. Nil is Reachable; a
	// test sets it to reach its own server on loopback.
	Allowed func(net.IP) bool
	// RootCAs are the certificates a service is trusted by. Nil is the
	// system's; a test gives its own server's.
	RootCAs *x509.CertPool
	// Timeout bounds one test. Zero is TestTimeout.
	Timeout time.Duration
	// Now is the time the line says it was written. Nil is time.Now.
	Now func() time.Time

	// dialTo sends a connection for one host:port to another, for a test
	// of a kind whose address is the service's own.
	dialTo map[string]string
}

// Reachable is what a drain may send to, from the panel's test and from the
// collector alike: what internal/netguard allows, and nothing inside the
// cluster. The collector's NetworkPolicy refuses the pod and service
// networks, so a drain pointed at an app or a Service here — another team's
// included — would pass a test from the panel and never receive a line.
func Reachable(ip net.IP) bool {
	if !netguard.Allowed(ip) {
		return false
	}
	for _, network := range clusterNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

var clusterNetworks = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{kube.PodCIDR, kube.ServiceCIDR} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}()

// TestFailed is a test the service did not accept, and why.
type TestFailed struct {
	// Status is the service's answer, zero when there was none.
	Status int
	Reason string
}

func (e *TestFailed) Error() string { return e.Reason }

// TestMessage is the line itself: plainly a test, from where, and for what.
func TestMessage(name string) string {
	return fmt.Sprintf("%s log drain test: if you can read this, the drain %q reaches this service. "+
		"It was sent by the panel, not by an app.", version.Name, name)
}

// testEvent is the line as a JSON object, in the shape the collector sends
// every line: its time, the message, and what it is about.
func testEvent(d Drain, now time.Time) map[string]any {
	return map[string]any{
		"timestamp": now.UTC().Format(time.RFC3339Nano),
		"message":   TestMessage(d.Name),
		"stream":    "stdout",
		"skifity": map[string]any{
			"kind":    "test",
			"test":    true,
			"team_id": d.TeamID,
			"drain":   d.Name,
		},
	}
}

// Send sends the test line and reports whether the service took it.
func (t *Tester) Send(ctx context.Context, d Drain) error {
	if err := d.Validate(); err != nil {
		return err
	}
	endpoint, err := d.Endpoint()
	if err != nil {
		return err
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = TestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}

	if d.Kind == KindSyslog {
		return t.sendSyslog(ctx, d, endpoint, now)
	}
	req, err := testRequest(ctx, d, endpoint, now)
	if err != nil {
		return err
	}
	resp, err := t.client().Do(req)
	if err != nil {
		return &TestFailed{Reason: unreachable(endpoint.URL.Host, err)}
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return &TestFailed{Status: resp.StatusCode, Reason: fmt.Sprintf(
			"%s answered %s and a redirect to %q, which the collector would not follow; give the address it redirects to",
			endpoint.URL.Host, resp.Status, clean(resp.Header.Get("Location"), d))}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return &TestFailed{Status: resp.StatusCode, Reason: fmt.Sprintf(
			"%s refused the credentials: %s%s", endpoint.URL.Host, resp.Status, excerpt(answer, d))}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return &TestFailed{Status: resp.StatusCode, Reason: fmt.Sprintf(
			"%s did not accept the line: %s%s", endpoint.URL.Host, resp.Status, excerpt(answer, d))}
	}
	if d.Kind == KindElasticsearch {
		// The bulk API answers 200 with each document's own outcome in it;
		// a refused one is "errors": true and a reason beside it.
		var bulk struct {
			Errors bool `json:"errors"`
			Items  []map[string]struct {
				Status int `json:"status"`
				Error  struct {
					Type   string `json:"type"`
					Reason string `json:"reason"`
				} `json:"error"`
			} `json:"items"`
		}
		if err := json.Unmarshal(answer, &bulk); err != nil {
			return &TestFailed{Status: resp.StatusCode, Reason: fmt.Sprintf(
				"%s answered %s with something that is not the bulk API's answer; is it Elasticsearch or OpenSearch?",
				endpoint.URL.Host, resp.Status)}
		}
		if bulk.Errors {
			reason := "the document was refused"
			for _, item := range bulk.Items {
				for _, outcome := range item {
					if outcome.Error.Reason != "" {
						reason = outcome.Error.Type + ": " + outcome.Error.Reason
					}
				}
			}
			return &TestFailed{Status: resp.StatusCode, Reason: fmt.Sprintf(
				"%s took the request and refused the line: %s", endpoint.URL.Host, clean(reason, d))}
		}
	}
	return nil
}

// testRequest is the request for one kind, in the shape its sink sends.
func testRequest(ctx context.Context, d Drain, endpoint Endpoint, now time.Time) (*http.Request, error) {
	event := testEvent(d, now)
	line, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	s := d.Settings
	var body []byte
	headers := http.Header{}

	switch d.Kind {
	case KindHTTP:
		body = append(line, '\n')
		headers.Set("Content-Type", "application/x-ndjson")
		if name := s["header_name"]; name != "" {
			headers.Set(name, d.Secrets["header_value"])
		}
	case KindLoki:
		labels := map[string]string{"source": version.Binary, "kind": "test", "team": d.TeamID}
		body, err = json.Marshal(map[string]any{
			"streams": []any{map[string]any{
				"stream": labels,
				"values": [][]string{{strconv.FormatInt(now.UnixNano(), 10), string(line)}},
			}},
		})
		headers.Set("Content-Type", "application/json")
		setLokiAuth(headers, d)
		if tenant := s["tenant_id"]; tenant != "" {
			headers.Set("X-Scope-OrgID", tenant)
		}
	case KindElasticsearch:
		document := event
		document["@timestamp"] = document["timestamp"]
		delete(document, "timestamp")
		doc, _ := json.Marshal(document)
		action, _ := json.Marshal(map[string]any{"index": map[string]string{"_index": RenderIndex(s["index"], now)}})
		body = append(append(append(action, '\n'), doc...), '\n')
		headers.Set("Content-Type", "application/x-ndjson")
		if key := d.Secrets["api_key"]; key != "" {
			headers.Set("Authorization", "ApiKey "+key)
		} else if s["username"] != "" {
			headers.Set("Authorization", basicAuth(s["username"], d.Secrets["password"]))
		}
	case KindDatadog:
		entry := datadogShape(event)
		body, err = json.Marshal([]any{entry})
		headers.Set("Content-Type", "application/json")
		headers.Set("DD-API-KEY", d.Secrets["api_key"])
	case KindAxiom:
		body = append(line, '\n')
		headers.Set("Content-Type", "application/x-ndjson")
		headers.Set("Authorization", "Bearer "+d.Secrets["token"])
		if org := s["org_id"]; org != "" {
			headers.Set("X-Axiom-Org-Id", org)
		}
	case KindBetterStack:
		entry := event
		entry["dt"] = entry["timestamp"]
		delete(entry, "timestamp")
		body, err = json.Marshal(entry)
		headers.Set("Content-Type", "application/json")
		headers.Set("Authorization", "Bearer "+d.Secrets["source_token"])
	case KindNewRelic:
		body, err = json.Marshal([]any{map[string]any{"logs": []any{event}}})
		headers.Set("Content-Type", "application/json")
		headers.Set("Api-Key", d.Secrets["license_key"])
	default:
		return nil, invalid("%q is not a kind of drain", d.Kind)
	}
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, invalid("%s is not an address a request can be made to", endpoint.URL.Host)
	}
	req.Header = headers
	req.Header.Set("User-Agent", version.UserAgent())
	return req, nil
}

// setLokiAuth is how a Loki drain signs in: a username and a password is
// basic authentication, which is what Grafana Cloud wants, and a token alone
// is a bearer token, which is what a gateway in front of Loki usually wants.
func setLokiAuth(headers http.Header, d Drain) {
	password := d.Secrets["password"]
	switch {
	case d.Settings["username"] != "":
		headers.Set("Authorization", basicAuth(d.Settings["username"], password))
	case password != "":
		headers.Set("Authorization", "Bearer "+password)
	}
}

func basicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// datadogShape is a line as Datadog's intake reads it: ddsource and service
// are what it is filed under.
func datadogShape(event map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range event {
		out[key] = value
	}
	out["ddsource"] = version.Binary
	out["service"] = version.Binary
	out["ddtags"] = "source:" + version.Binary
	return out
}

// RenderIndex fills in an index name's date the way the collector does, from
// the line's time.
func RenderIndex(index string, at time.Time) string {
	at = at.UTC()
	return strings.NewReplacer(
		"%Y", fmt.Sprintf("%04d", at.Year()),
		"%m", fmt.Sprintf("%02d", int(at.Month())),
		"%d", fmt.Sprintf("%02d", at.Day()),
	).Replace(index)
}

// sendSyslog opens the TLS connection the collector will, checks the
// certificate the same way, and writes one RFC 5424 line.
//
// Syslog has no answer, so what this proves is less than for the others: the
// address answers, speaks TLS, and has a certificate for its name. Whether the
// line was kept is for the service's own page to say.
func (t *Tester) sendSyslog(ctx context.Context, d Drain, endpoint Endpoint, now time.Time) error {
	conn, err := t.dial(ctx, "tcp", endpoint.Address)
	if err != nil {
		return &TestFailed{Reason: unreachable(endpoint.Address, err)}
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	secured := tls.Client(conn, &tls.Config{ServerName: endpoint.Host, RootCAs: t.RootCAs, MinVersion: tls.VersionTLS12})
	if err := secured.HandshakeContext(ctx); err != nil {
		return &TestFailed{Reason: fmt.Sprintf("%s did not complete a TLS handshake: %s", endpoint.Address, err)}
	}
	if _, err := io.WriteString(secured, SyslogLine(d.Name, now)+"\n"); err != nil {
		return &TestFailed{Reason: fmt.Sprintf("%s closed the connection before the line was written: %s", endpoint.Address, err)}
	}
	_ = secured.Close()
	return nil
}

// SyslogLine is the test line as RFC 5424 frames it: facility user, severity
// informational, the time, the panel as the host and the app.
func SyslogLine(name string, now time.Time) string {
	return fmt.Sprintf("<14>1 %s %s %s - - - %s",
		now.UTC().Format("2006-01-02T15:04:05.000000Z07:00"), version.Binary, version.Binary, TestMessage(name))
}

// client is an HTTP client that dials only what Allowed allows, uses no proxy
// from the environment, and follows no redirect.
func (t *Tester) client() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			// Never a proxy from the environment: the address checked by the
			// dialler would be the proxy's, and the proxy goes wherever it is
			// told.
			Proxy:                 nil,
			DialContext:           t.dial,
			TLSClientConfig:       &tls.Config{RootCAs: t.RootCAs, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConns:          1,
			DisableKeepAlives:     true,
		},
		// A redirect is reported, not followed: the collector follows none,
		// and the credentials are not the next host's to receive.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (t *Tester) dialer() *net.Dialer {
	allowed := t.Allowed
	if allowed == nil {
		allowed = Reachable
	}
	return &net.Dialer{
		Timeout: 10 * time.Second,
		// Checked on the address being dialled, after the name resolved:
		// see internal/netguard for why that is the only place it holds.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing to connect to %q", address)
			}
			ip := net.ParseIP(host)
			if !allowed(ip) {
				return &netguard.Blocked{IP: ip}
			}
			return nil
		},
	}
}

// dial connects through the guarded dialler.
func (t *Tester) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if to, ok := t.dialTo[address]; ok {
		address = to
	}
	return t.dialer().DialContext(ctx, network, address)
}

// unreachable says why a connection did not happen, without the noise of a
// URL error wrapped three times.
func unreachable(where string, err error) string {
	var blocked *netguard.Blocked
	if errors.As(err, &blocked) {
		return blocked.Error()
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Sprintf("%s does not resolve: %s", where, dnsErr.Err)
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return fmt.Sprintf("%s has a certificate this server does not trust: %s", where, certErr.Err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("%s did not answer within %s", where, TestTimeout)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return fmt.Sprintf("%s could not be reached: %s", where, opErr.Err)
	}
	return fmt.Sprintf("%s could not be reached: %s", where, err)
}

// excerpt is the start of a service's answer, which is usually its reason,
// with anything shaped like a secret taken out — the drain's own credentials
// first, which a service that echoes the request would hand straight back.
func excerpt(answer []byte, d Drain) string {
	text := strings.TrimSpace(string(answer))
	if text == "" {
		return ""
	}
	return " — " + clean(text, d)
}

// clean keeps a service's words short, on one line, and without a secret.
func clean(text string, d Drain) string {
	for _, secret := range d.Secrets {
		if len(secret) >= 4 {
			text = strings.ReplaceAll(text, secret, logging.Redacted)
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 300 {
		text = strings.ToValidUTF8(text[:300], "") + "…"
	}
	return logging.Scrub(text)
}
