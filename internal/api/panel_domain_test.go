package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

const (
	sslipHost   = "203-0-113-10.sslip.io"
	panelDomain = "panel.example.test"
)

// moverCluster is a cluster the panel can move itself in. It records what it
// was asked, and answers a move with moveErr.
type moverCluster struct {
	Cluster
	route   PanelRoute
	moveErr error
	// hold, when set, keeps a move going until it is closed.
	hold chan struct{}

	mu       sync.Mutex
	moved    chan PanelMove
	written  []string
	returned string
}

func newMoverCluster(hosts ...string) *moverCluster {
	return &moverCluster{
		route: PanelRoute{Hosts: hosts, Secrets: map[string]string{}, Certificate: PanelCertificate{State: PanelCertificateNone}},
		moved: make(chan PanelMove, 4),
	}
}

func (m *moverCluster) PublicAddress(context.Context, string) string { return "203.0.113.10" }

func (m *moverCluster) PanelRoute(context.Context) (PanelRoute, error) { return m.route, nil }

func (m *moverCluster) SetPanelDomain(_ context.Context, move PanelMove) error {
	m.moved <- move
	if m.hold != nil {
		<-m.hold
	}
	return m.moveErr
}

func (m *moverCluster) WritePanelRoute(_ context.Context, host string, tls bool, previous []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	scheme := "http"
	if tls {
		scheme = "https"
	}
	m.written = append(m.written, scheme+"://"+host+" "+strings.Join(previous, ","))
	return nil
}

func (m *moverCluster) ReturnPanel(_ context.Context, host string, _ PanelMove) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.returned = host
	return nil
}

func (m *moverCluster) KeepPanelRoute(context.Context, PanelMove) (bool, error) { return false, nil }

// movingHarness is a panel at url, in a cluster it can move itself in, whose
// DNS says the domain points at it.
func movingHarness(t *testing.T, url string, mover *moverCluster) *harness {
	t.Helper()
	h := newHarness(t)
	h.withCluster(mover)
	h.api.cfg.PublicURL = url
	h.api.resolver = fakeDNS{addresses: map[string][]string{panelDomain: {"203.0.113.10"}}}
	return h
}

// Moving the panel changes where everybody signs in, so it is the panel
// administrator's: owning a team, which anybody signed in can, is not enough.
func TestOnlyThePanelAdministratorMovesThePanel(t *testing.T) {
	h := movingHarness(t, "http://"+sslipHost, newMoverCluster(sslipHost))
	owner := h.newTenant("shop")
	admin := adminTenant(h, "ops")

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/panel/domain"},
		{http.MethodPost, "/api/panel/domain"},
		{http.MethodPost, "/api/panel/domain/check"},
		{http.MethodDelete, "/api/panel/domain"},
	} {
		if status, body := h.do(owner, route.method, route.path, map[string]any{"domain": panelDomain}); status != http.StatusForbidden {
			t.Errorf("a team owner: %s %s answered %d: %s", route.method, route.path, status, truncate(body, 160))
		}
	}
	status, body := h.do(admin, http.MethodGet, "/api/panel/domain", nil)
	var view panelDomainView
	if err := json.Unmarshal([]byte(body), &view); status != http.StatusOK || err != nil {
		t.Fatalf("the administrator's view answered %d: %s", status, body)
	}
	if view.Host != sslipHost || view.Scheme != "http" || !view.Movable || view.DNSTarget != "203.0.113.10" ||
		view.FreeAddress != sslipHost {
		t.Errorf("the view says %+v", view)
	}
}

// A name the panel cannot have is refused before anything happens: an IP
// address and a wildcard cannot be given a certificate, an sslip.io name
// shares its limit with every other install, and an app's domain would put
// two things behind one hostname.
func TestThePanelIsNotPutOnANameItCannotHave(t *testing.T) {
	mover := newMoverCluster(sslipHost)
	h := movingHarness(t, "http://"+sslipHost, mover)
	admin := adminTenant(h, "ops")
	app := h.app(admin, "shop")
	if err := h.db.CreateDomain(t.Context(), &store.Domain{AppID: app.ID, Hostname: "shop.example.test", Path: "/", Status: "pending"}); err != nil {
		t.Fatal(err)
	}

	for domain, code := range map[string]string{
		"203.0.113.10":           "request.invalid",
		"*.example.test":         "request.invalid",
		"panel." + sslipHost:     "request.invalid",
		"shop.example.test":      "panel.domain_taken",
		"https://not a domain/x": "request.invalid",
	} {
		status, body := h.do(admin, http.MethodPost, "/api/panel/domain", map[string]any{"domain": domain})
		if status != http.StatusBadRequest || problemCode(body) != code {
			t.Errorf("%q answered %d: %s", domain, status, truncate(body, 200))
		}
	}
	if _, err := h.db.GetPanelDomain(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused domain was recorded: %v", err)
	}
	if entries, _ := h.db.ListAudit(t.Context(), admin.team.ID, "panel.domain_set", "", 5); len(entries) != 0 {
		t.Errorf("a refused domain was audited: %+v", entries)
	}
}

// A domain whose DNS does not point here would only wait for a certificate
// that cannot come, so it is refused with the record to create; told it
// reaches the server some other way, the panel moves anyway.
func TestAMoveToADomainThatDoesNotPointHereIsRefused(t *testing.T) {
	mover := newMoverCluster(sslipHost)
	h := movingHarness(t, "http://"+sslipHost, mover)
	h.api.resolver = fakeDNS{addresses: map[string][]string{panelDomain: {"198.51.100.7"}}}
	admin := adminTenant(h, "ops")

	status, body := h.do(admin, http.MethodPost, "/api/panel/domain", map[string]any{"domain": panelDomain})
	if status != http.StatusConflict || problemCode(body) != "panel.domain_dns_mismatch" ||
		!strings.Contains(body, "198.51.100.7") || !strings.Contains(body, "203.0.113.10") {
		t.Fatalf("a domain pointing elsewhere answered %d: %s", status, body)
	}
	status, body = h.do(admin, http.MethodPost, "/api/panel/domain/check", map[string]any{"domain": panelDomain})
	if status != http.StatusOK || !strings.Contains(body, `"status":"elsewhere"`) {
		t.Errorf("checking it answered %d: %s", status, body)
	}

	status, body = h.do(admin, http.MethodPost, "/api/panel/domain", map[string]any{"domain": panelDomain, "ignore_dns": true})
	if status != http.StatusAccepted {
		t.Fatalf("moving anyway answered %d: %s", status, body)
	}
	select {
	case <-mover.moved:
	case <-time.After(5 * time.Second):
		t.Fatal("moving anyway did not move")
	}
}

// A move is recorded and audited, keeps the address the panel is at, and is
// one at a time. One that stops is recorded with why, and cancelling it puts
// the route back without restarting anything.
func TestMovingThePanelIsRecordedAndCanBeCancelled(t *testing.T) {
	mover := newMoverCluster(sslipHost)
	mover.moveErr = errdoc.PanelCertificatePending(panelDomain, "Waiting on certificate issuance")
	h := movingHarness(t, "http://"+sslipHost, mover)
	admin := adminTenant(h, "ops")

	// Held until the test has seen it busy.
	mover.hold = make(chan struct{})

	status, body := h.do(admin, http.MethodPost, "/api/panel/domain", map[string]any{"domain": "Panel.Example.Test."})
	var view panelDomainView
	if err := json.Unmarshal([]byte(body), &view); status != http.StatusAccepted || err != nil {
		t.Fatalf("the move answered %d: %s", status, body)
	}
	if view.Domain != panelDomain || view.State != store.PanelDomainMoving {
		t.Errorf("the move answered %+v", view)
	}
	if entries, _ := h.db.ListAudit(t.Context(), admin.team.ID, "panel.domain_set", "", 5); len(entries) != 1 || entries[0].TargetLabel != panelDomain {
		t.Errorf("the move was audited as %+v", entries)
	}
	if status, body := h.do(admin, http.MethodPost, "/api/panel/domain", map[string]any{"domain": "other.example.test", "ignore_dns": true}); status != http.StatusConflict || problemCode(body) != "panel.domain_busy" {
		t.Errorf("a second move while one is under way answered %d: %s", status, body)
	}
	if status, body := h.do(admin, http.MethodDelete, "/api/panel/domain", nil); status != http.StatusConflict || problemCode(body) != "panel.domain_busy" {
		t.Errorf("taking the domain off while it is being moved answered %d: %s", status, body)
	}
	close(mover.hold)

	move := <-mover.moved
	if move.Domain != panelDomain || !slices.Equal(move.Previous, []string{sslipHost}) {
		t.Errorf("the cluster was asked to move to %+v", move)
	}
	var row store.PanelDomain
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if row, _ = h.db.GetPanelDomain(t.Context()); row.State == store.PanelDomainFailed {
			break
		}
	}
	if row.State != store.PanelDomainFailed || !strings.Contains(string(row.Problem), "panel.domain_certificate_pending") {
		t.Fatalf("a move that stopped is recorded as %s: %s", row.State, row.Problem)
	}
	_, body = h.do(admin, http.MethodGet, "/api/panel/domain", nil)
	if !strings.Contains(body, `"state":"failed"`) || !strings.Contains(body, "Waiting on certificate issuance") {
		t.Errorf("the view of a move that stopped says %s", body)
	}

	status, body = h.do(admin, http.MethodDelete, "/api/panel/domain", nil)
	if status != http.StatusOK {
		t.Fatalf("cancelling answered %d: %s", status, body)
	}
	if !slices.Equal(mover.written, []string{"http://" + sslipHost + " "}) || mover.returned != "" {
		t.Errorf("cancelling wrote %v and returned to %q", mover.written, mover.returned)
	}
	if _, err := h.db.GetPanelDomain(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a cancelled move is still recorded: %v", err)
	}
	if entries, _ := h.db.ListAudit(t.Context(), admin.team.ID, "panel.domain_cancelled", "", 5); len(entries) != 1 {
		t.Errorf("cancelling was audited as %+v", entries)
	}
}

// Taking the domain off a panel at it puts it back on the sslip.io address it
// came from.
func TestTakingTheDomainOffGoesBackToTheFreeAddress(t *testing.T) {
	mover := newMoverCluster(panelDomain, sslipHost)
	h := movingHarness(t, "https://"+panelDomain, mover)
	admin := adminTenant(h, "ops")
	if err := h.db.StartPanelDomain(t.Context(), store.PanelDomain{Domain: panelDomain, Previous: []string{sslipHost}, State: store.PanelDomainLive}); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(admin, http.MethodDelete, "/api/panel/domain", nil)
	if status != http.StatusAccepted || !strings.Contains(body, "http://"+sslipHost) {
		t.Fatalf("taking the domain off answered %d: %s", status, body)
	}
	if mover.returned != sslipHost {
		t.Errorf("the panel was sent back to %q", mover.returned)
	}
	if _, err := h.db.GetPanelDomain(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the domain taken off is still recorded: %v", err)
	}
	if entries, _ := h.db.ListAudit(t.Context(), admin.team.ID, "panel.domain_removed", "", 5); len(entries) != 1 {
		t.Errorf("taking it off was audited as %+v", entries)
	}

	// With nothing to take off, there is nothing to do.
	plain := movingHarness(t, "http://"+sslipHost, newMoverCluster(sslipHost))
	other := adminTenant(plain, "ops")
	if status, body := plain.do(other, http.MethodDelete, "/api/panel/domain", nil); status != http.StatusBadRequest {
		t.Errorf("taking the domain off a panel with none answered %d: %s", status, body)
	}
}

// After the move the old address keeps taking webhooks and API tokens, since
// a Git host does not follow a redirect on a POST; a browser is sent on.
func TestTheOldAddressKeepsTakingWebhooks(t *testing.T) {
	h := movingHarness(t, "https://"+panelDomain, newMoverCluster(panelDomain, sslipHost))
	admin := adminTenant(h, "ops")
	if err := h.db.StartPanelDomain(t.Context(), store.PanelDomain{Domain: panelDomain, Previous: []string{sslipHost}, State: store.PanelDomainLive}); err != nil {
		t.Fatal(err)
	}
	h.api.SettlePanelDomain(t.Context())

	at := func(host, method, path string, headers map[string]string) (int, http.Header, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		client := *h.server.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(body)
	}

	status, _, body := at(sslipHost, http.MethodPost, "/api/webhooks/git/gsrc_00000000000000000000", map[string]string{"Content-Type": "application/json"})
	if status == http.StatusPermanentRedirect || status == http.StatusGone || problemCode(body) == "panel.domain_moved" {
		t.Errorf("a webhook at the old address answered %d: %s", status, body)
	}
	status, headers, _ := at(sslipHost, http.MethodGet, "/settings?tab=panel", nil)
	if status != http.StatusPermanentRedirect || headers.Get("Location") != "https://"+panelDomain+"/settings?tab=panel" {
		t.Errorf("a browser at the old address answered %d to %q", status, headers.Get("Location"))
	}
	status, _, body = at(sslipHost, http.MethodGet, "/api/me", map[string]string{"Cookie": "skifity_session=not-a-real-session"})
	if status != http.StatusGone || problemCode(body) != "panel.domain_moved" || !strings.Contains(body, "https://"+panelDomain) {
		t.Errorf("a browser's API request at the old address answered %d: %s", status, body)
	}
	status, _, body = at(sslipHost, http.MethodGet, "/api/me", map[string]string{"Authorization": "Bearer " + admin.token})
	if status != http.StatusOK {
		t.Errorf("an API token at the old address answered %d: %s", status, truncate(body, 200))
	}
	if status, _, _ := at(panelDomain, http.MethodGet, "/settings", nil); status == http.StatusPermanentRedirect {
		t.Error("the panel's own address was redirected")
	}

	for _, host := range []string{panelDomain, sslipHost} {
		if !h.api.isPanelHostname(t.Context(), host) {
			t.Errorf("%s is not reserved as the panel's", host)
		}
	}
	app := h.app(admin, "shop")
	if status, body := h.do(admin, http.MethodPost, "/api/apps/"+app.ID+"/domains", map[string]any{"hostname": sslipHost}); status != http.StatusConflict || problemCode(body) != "domain.is_the_panel" {
		t.Errorf("an app given the panel's old address answered %d: %s", status, truncate(body, 200))
	}
}

// The panel that starts after a move finishes it: at its domain it is
// there, anywhere else it says so, and a move the restart caught halfway is
// recorded as stopped.
func TestTheNextStartFinishesTheMove(t *testing.T) {
	for _, tc := range []struct {
		name, state, url, want, code string
	}{
		{"restarted at the domain", store.PanelDomainRestarting, "https://" + panelDomain, store.PanelDomainLive, ""},
		{"restarted elsewhere", store.PanelDomainRestarting, "http://" + sslipHost, store.PanelDomainFailed, "panel.domain_not_restarted"},
		{"caught halfway", store.PanelDomainMoving, "http://" + sslipHost, store.PanelDomainFailed, "panel.domain_interrupted"},
		{"moved by the installer since", store.PanelDomainLive, "https://other.example.test", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := movingHarness(t, tc.url, newMoverCluster())
			if err := h.db.StartPanelDomain(t.Context(), store.PanelDomain{Domain: panelDomain, Previous: []string{sslipHost}, State: tc.state}); err != nil {
				t.Fatal(err)
			}
			h.api.SettlePanelDomain(t.Context())
			row, err := h.db.GetPanelDomain(t.Context())
			if tc.want == "" {
				if !errors.Is(err, store.ErrNotFound) {
					t.Errorf("the domain is still recorded as %+v", row)
				}
				return
			}
			if row.State != tc.want || (tc.code != "" && !strings.Contains(string(row.Problem), tc.code)) {
				t.Errorf("recorded %s: %s", row.State, row.Problem)
			}
			if tc.want == store.PanelDomainLive {
				if url, _, _ := h.db.GetSetting(t.Context(), settings.KeyPanelURL); url != tc.url {
					t.Errorf("the Panel URL is %q", url)
				}
			}
		})
	}
}
