package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"skifity/internal/logdrain"
	"skifity/internal/store"
)

// A made-up credential, shaped so the tests can find it wherever it should
// not be.
const fakeDrainToken = "Bearer not-a-real-drain-token-0000001"

// drainReceiver is an https server standing in for a log service: it records
// what it was sent, and answers what the test tells it to.
type drainReceiver struct {
	server *httptest.Server
	mu     sync.Mutex
	lines  []string
	auth   []string
	status int
}

func newDrainReceiver(t *testing.T) *drainReceiver {
	t.Helper()
	receiver := &drainReceiver{status: http.StatusAccepted}
	receiver.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receiver.mu.Lock()
		receiver.lines = append(receiver.lines, string(body))
		receiver.auth = append(receiver.auth, r.Header.Get("Authorization"))
		status := receiver.status
		receiver.mu.Unlock()
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = io.WriteString(w, `{"error":"unknown token"}`)
		}
	}))
	t.Cleanup(receiver.server.Close)
	return receiver
}

func (d *drainReceiver) answer(status int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = status
}

func (d *drainReceiver) received() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.lines)
}

// fakeCollector counts the times the panel brought the collector up to date.
type fakeCollector struct{ refreshed atomic.Int32 }

func (f *fakeCollector) RefreshLogDrains(context.Context) error { f.refreshed.Add(1); return nil }
func (f *fakeCollector) LogCollectorStatus(context.Context) (logdrain.CollectorStatus, error) {
	return logdrain.CollectorStatus{State: logdrain.CollectorRunning, Desired: 1, Ready: 1, Problems: []logdrain.CollectorProblem{}}, nil
}

// trustReceivers lets the panel's test reach servers on loopback, trusting
// their certificates, the way it reaches a real service.
func trustReceivers(h *harness, receivers ...*drainReceiver) {
	pool := x509.NewCertPool()
	for _, receiver := range receivers {
		pool.AddCert(receiver.server.Certificate())
	}
	h.api.drainTester = &logdrain.Tester{
		Allowed: func(ip net.IP) bool { return ip.IsLoopback() || logdrain.Reachable(ip) },
		RootCAs: pool, Timeout: 5 * time.Second,
	}
}

type drainAnswer struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Destination string            `json:"destination"`
	Settings    map[string]string `json:"settings"`
	Secrets     []string          `json:"secrets"`
	Scoped      bool              `json:"scoped"`
	Projects    []string          `json:"projects"`
}

// A drain is tested before it is kept: the service is sent one labelled line
// with the drain's credential, a refusal keeps nothing, and the credential is
// never answered — not to the administrator who typed it, not to a viewer.
func TestADrainIsTestedBeforeItIsKept(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	viewer := h.newMember(acme, "watcher", store.RoleViewer)
	receiver := newDrainReceiver(t)
	trustReceivers(h, receiver)
	collector := &fakeCollector{}
	h.api.logs = collector

	path := "/api/teams/" + acme.team.ID + "/log-drains"
	body := map[string]any{
		"name": "Receiver", "kind": "http",
		"settings": map[string]string{"url": receiver.server.URL + "/in", "header_name": "Authorization", "header_value": fakeDrainToken},
	}

	// Refused by the service: nothing is kept.
	receiver.answer(http.StatusUnauthorized)
	status, answer := h.do(acme, http.MethodPost, path, body)
	if status != http.StatusBadGateway || !strings.Contains(answer, "drain.test_failed") || !strings.Contains(answer, "refused the credentials") {
		t.Fatalf("a refused test answered %d: %s", status, answer)
	}
	if drains, _ := h.db.ListLogDrains(t.Context(), acme.team.ID); len(drains) != 0 {
		t.Fatal("a drain whose test was refused was kept")
	}

	receiver.answer(http.StatusAccepted)
	status, answer = h.do(acme, http.MethodPost, path, body)
	if status != http.StatusCreated {
		t.Fatalf("create answered %d: %s", status, answer)
	}
	if strings.Contains(answer, fakeDrainToken) {
		t.Errorf("the credential was answered: %s", answer)
	}
	var created drainAnswer
	_ = json.Unmarshal([]byte(answer), &created)
	if created.Status != "pending" && created.Status != "applied" {
		t.Errorf("a new drain is %q", created.Status)
	}
	if len(created.Secrets) != 1 || created.Secrets[0] != "header_value" {
		t.Errorf("the drain says it has the secrets %v", created.Secrets)
	}
	if collector.refreshed.Load() != 1 {
		t.Errorf("the collector was brought up to date %d times", collector.refreshed.Load())
	}
	receiver.mu.Lock()
	last, auth := receiver.lines[len(receiver.lines)-1], receiver.auth[len(receiver.auth)-1]
	receiver.mu.Unlock()
	if auth != fakeDrainToken || !strings.Contains(last, "log drain test") || !strings.Contains(last, `"drain":"Receiver"`) {
		t.Errorf("the service was sent %q with %q", last, auth)
	}

	// The credential is sealed in the database, under the drain's address.
	row, err := h.db.GetLogDrain(t.Context(), acme.team.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.SealedSecrets, "not-a-real") || row.SealedSecrets == "" {
		t.Errorf("the credential is stored as %q", row.SealedSecrets)
	}

	// A viewer sees where the logs go and nothing of how.
	status, answer = h.do(viewer, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("a viewer's list answered %d: %s", status, answer)
	}
	var listed struct {
		Items     []drainAnswer `json:"items"`
		Kinds     []any         `json:"kinds"`
		Collector struct {
			Live *logdrain.CollectorStatus `json:"live"`
		} `json:"collector"`
	}
	_ = json.Unmarshal([]byte(answer), &listed)
	if len(listed.Items) != 1 || listed.Items[0].Settings != nil || listed.Items[0].Secrets != nil ||
		listed.Items[0].Destination != receiver.server.URL+"/in" {
		t.Errorf("a viewer was answered %s", answer)
	}
	if len(listed.Kinds) != 8 || listed.Collector.Live == nil || listed.Collector.Live.State != logdrain.CollectorRunning {
		t.Errorf("the list did not carry the kinds and the collector: %s", answer)
	}
	if strings.Contains(answer, fakeDrainToken) {
		t.Error("a viewer was answered the credential")
	}
	// An administrator sees the settings, and still not the credential.
	_, answer = h.do(acme, http.MethodGet, path, nil)
	if !strings.Contains(answer, `"header_name":"Authorization"`) || strings.Contains(answer, fakeDrainToken) {
		t.Errorf("an administrator was answered %s", answer)
	}

	// A second drain of the same name is a conflict, found before anything
	// is sent.
	sent := receiver.received()
	status, _ = h.do(acme, http.MethodPost, path, body)
	if status != http.StatusConflict || receiver.received() != sent {
		t.Errorf("a second drain called Receiver answered %d after %d lines", status, receiver.received()-sent)
	}
}

// A change to where a drain sends forgets the stored credential, so a
// credential nobody can read back cannot be sent somewhere new by changing
// the address under it; a change that is not to the destination sends no test.
func TestMovingADrainForgetsItsCredential(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	first, second := newDrainReceiver(t), newDrainReceiver(t)
	trustReceivers(h, first, second)

	path := "/api/teams/" + acme.team.ID + "/log-drains"
	status, answer := h.do(acme, http.MethodPost, path, map[string]any{
		"name": "Receiver", "kind": "http",
		"settings": map[string]string{"url": first.server.URL + "/in", "header_name": "Authorization", "header_value": fakeDrainToken},
	})
	if status != http.StatusCreated {
		t.Fatalf("create answered %d: %s", status, answer)
	}
	var created drainAnswer
	_ = json.Unmarshal([]byte(answer), &created)
	drainPath := path + "/" + created.ID

	// A rename sends nothing.
	before := first.received()
	if status, answer := h.do(acme, http.MethodPatch, drainPath, map[string]any{"name": "Renamed"}); status != http.StatusOK {
		t.Fatalf("rename answered %d: %s", status, answer)
	}
	if first.received() != before {
		t.Error("a rename sent a test line")
	}

	// Moved without the credential: refused, and the new address was sent
	// nothing — least of all the old credential.
	status, answer = h.do(acme, http.MethodPatch, drainPath, map[string]any{
		"settings": map[string]string{"url": second.server.URL + "/in"},
	})
	if status != http.StatusBadRequest || !strings.Contains(answer, "address changed") {
		t.Errorf("moving the drain without its credential answered %d: %s", status, answer)
	}
	if second.received() != 0 {
		t.Error("the new address was sent something")
	}

	// Moved with it: tested at the new address, and kept.
	status, answer = h.do(acme, http.MethodPatch, drainPath, map[string]any{
		"settings": map[string]string{"url": second.server.URL + "/in", "header_value": fakeDrainToken},
	})
	if status != http.StatusOK {
		t.Fatalf("moving the drain answered %d: %s", status, answer)
	}
	if second.received() != 1 {
		t.Errorf("the new address was sent %d lines", second.received())
	}

	// The credential is cleared on request.
	status, answer = h.do(acme, http.MethodPatch, drainPath, map[string]any{
		"settings": map[string]string{"header_name": ""}, "clear": []string{"header_value"},
	})
	if status != http.StatusOK || strings.Contains(answer, "header_value") {
		t.Errorf("clearing the header answered %d: %s", status, answer)
	}

	// Limited to a project of another team: refused as one that does not
	// exist.
	globex := h.newTenant("globex")
	status, _ = h.do(acme, http.MethodPatch, drainPath, map[string]any{"projects": []string{globex.project.ID}})
	if status != http.StatusNotFound {
		t.Errorf("limiting a drain to another team's project answered %d", status)
	}
	status, answer = h.do(acme, http.MethodPatch, drainPath, map[string]any{"projects": []string{acme.project.ID}})
	if status != http.StatusOK || !strings.Contains(answer, `"scoped":true`) {
		t.Errorf("limiting a drain to the team's own project answered %d: %s", status, answer)
	}

	// Another team cannot read, test, change or remove it.
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api/teams/" + globex.team.ID + "/log-drains/" + created.ID + "/test"},
		{http.MethodPatch, "/api/teams/" + globex.team.ID + "/log-drains/" + created.ID},
		{http.MethodDelete, "/api/teams/" + globex.team.ID + "/log-drains/" + created.ID},
	} {
		if status, _ := h.do(globex, request.method, request.path, map[string]any{}); status != http.StatusNotFound {
			t.Errorf("%s %s by another team answered %d", request.method, request.path, status)
		}
		if status, _ := h.do(globex, request.method, strings.Replace(request.path, globex.team.ID, acme.team.ID, 1), map[string]any{}); status != http.StatusNotFound {
			t.Errorf("%s on acme's drain by globex answered %d", request.method, status)
		}
	}

	// The test route sends again, and records a failure where the list
	// shows it.
	second.answer(http.StatusInternalServerError)
	status, answer = h.do(acme, http.MethodPost, drainPath+"/test", nil)
	if status != http.StatusBadGateway {
		t.Errorf("a failed test answered %d: %s", status, answer)
	}
	row, _ := h.db.GetLogDrain(t.Context(), acme.team.ID, created.ID)
	if !strings.Contains(row.TestError, "did not accept the line") {
		t.Errorf("the failed test was recorded as %q", row.TestError)
	}

	if status, _ := h.do(acme, http.MethodDelete, drainPath, nil); status != http.StatusOK {
		t.Errorf("remove answered %d", status)
	}
}

// The test refuses what the collector could never reach: the panel itself,
// the metadata service, an address inside the cluster.
func TestADrainCannotPointAtThePanel(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	// The default tester, as the panel runs it.
	status, answer := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/log-drains", map[string]any{
		"kind": "http", "settings": map[string]string{"url": h.server.URL + "/api/health"},
	})
	if status != http.StatusBadGateway || !strings.Contains(answer, "this machine itself") {
		t.Errorf("a drain pointed at the panel answered %d: %s", status, answer)
	}
	status, answer = h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/log-drains", map[string]any{
		"kind": "datadog", "settings": map[string]string{"site": "attacker.example.com", "api_key": "x"},
	})
	if status != http.StatusBadRequest || !strings.Contains(answer, "drain.invalid") {
		t.Errorf("a Datadog site nobody runs answered %d: %s", status, answer)
	}
}
