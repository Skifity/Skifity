package watch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/notify"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// fakeStore is a database small enough to reason about.
type fakeStore struct {
	servers     []store.Server
	apps        []store.DeployedApp
	unfinished  []store.Deployment
	domains     map[string][]store.Domain
	seen        map[string]time.Time
	appStatuses map[string]string
	domainSet   map[string]string

	stalePreviews  []store.Environment
	deletedEnvs    []string
	settingValues  map[string]string
	previewsBefore time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		domains:       map[string][]store.Domain{},
		seen:          map[string]time.Time{},
		appStatuses:   map[string]string{},
		domainSet:     map[string]string{},
		settingValues: map[string]string{},
	}
}

func (f *fakeStore) ListAllServers(context.Context) ([]store.Server, error) { return f.servers, nil }

func (f *fakeStore) SetServerStatus(_ context.Context, id string, status store.ServerStatus, detail string) error {
	for i := range f.servers {
		if f.servers[i].ID == id {
			f.servers[i].Status = status
			f.servers[i].StatusDetail = detail
		}
	}
	return nil
}

func (f *fakeStore) TouchServerSeen(_ context.Context, id string, at time.Time) error {
	f.seen[id] = at
	return nil
}

func (f *fakeStore) ListDeployedApps(context.Context) ([]store.DeployedApp, error) {
	return f.apps, nil
}

func (f *fakeStore) SetAppStatus(_ context.Context, id, status string) error {
	f.appStatuses[id] = status
	for i := range f.apps {
		if f.apps[i].ID == id {
			f.apps[i].Status = status
		}
	}
	return nil
}

func (f *fakeStore) ListUnfinishedDeployments(context.Context) ([]store.Deployment, error) {
	return f.unfinished, nil
}

func (f *fakeStore) ListDomains(_ context.Context, appID string) ([]store.Domain, error) {
	return f.domains[appID], nil
}

func (f *fakeStore) SetDomainStatus(_ context.Context, id, status, _ string) error {
	f.domainSet[id] = status
	for appID := range f.domains {
		for i := range f.domains[appID] {
			if f.domains[appID][i].ID == id {
				f.domains[appID][i].Status = status
			}
		}
	}
	return nil
}

func (f *fakeStore) StalePreviewEnvironments(_ context.Context, before time.Time) ([]store.Environment, error) {
	f.previewsBefore = before
	return f.stalePreviews, nil
}

func (f *fakeStore) DeleteEnvironment(_ context.Context, id string) error {
	f.deletedEnvs = append(f.deletedEnvs, id)
	return nil
}

func (f *fakeStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	return f.settingValues[key], false, nil
}

// fakeCluster answers with whatever the test set up.
type fakeCluster struct {
	nodes           []api.NodeInfo
	apps            map[string]api.AppRuntimeStatus
	certs           map[string]cluster.CertificateState
	deletedSpaces   []string
	deleteNamespace error
}

func (f *fakeCluster) DeleteNamespace(_ context.Context, namespace string) error {
	if f.deleteNamespace != nil {
		return f.deleteNamespace
	}
	f.deletedSpaces = append(f.deletedSpaces, namespace)
	return nil
}

func (f *fakeCluster) Summary(context.Context) (api.ClusterSummary, error) {
	return api.ClusterSummary{Reachable: true, Nodes: f.nodes}, nil
}

func (f *fakeCluster) AppStatus(_ context.Context, _, slug string) (api.AppRuntimeStatus, error) {
	return f.apps[slug], nil
}

func (f *fakeCluster) CertificateStatus(_ context.Context, _, name string) (cluster.CertificateState, error) {
	return f.certs[name], nil
}

// sentMessage is one notification a test can assert on.
type sentMessage struct {
	teamID string
	event  string
	msg    notify.Message
}

type fakeNotifier struct{ sent []sentMessage }

func (f *fakeNotifier) Notify(_ context.Context, teamID, event string, msg notify.Message) {
	f.sent = append(f.sent, sentMessage{teamID, event, msg})
}

func (f *fakeNotifier) eventsOf(event string) int {
	n := 0
	for _, m := range f.sent {
		if m.event == event {
			n++
		}
	}
	return n
}

func testWatcher(t *testing.T, db *fakeStore, c *fakeCluster) (*Watcher, *fakeNotifier) {
	t.Helper()
	notifier := &fakeNotifier{}
	w := New(db, c, nil, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w, notifier
}

// TestServerLostIsReportedOnce is the behaviour an operator depends on: the
// panel notices a node that stopped answering, says so, and then stops saying
// so until something changes.
func TestServerLostIsReportedOnce(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{{
		ID: "srv_1", TeamID: "team_1", Name: "web-1",
		NodeName: "web-1", Status: store.ServerReady,
	}}
	c := &fakeCluster{nodes: []api.NodeInfo{}} // the node is gone

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := db.servers[0].Status; got != store.ServerNotReady {
		t.Fatalf("server status is %q, want %q", got, store.ServerNotReady)
	}
	if got := notifier.eventsOf(notify.EventServerLost); got != 1 {
		t.Fatalf("sent %d server.lost notifications, want 1", got)
	}

	w.Once(t.Context())
	if got := notifier.eventsOf(notify.EventServerLost); got != 1 {
		t.Fatalf("a server that stayed down produced %d notifications, want 1", got)
	}
}

// TestServerRecoveryIsReported covers the other half: coming back is news too,
// and the status has to return to ready or the server looks broken forever.
func TestServerRecoveryIsReported(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{{
		ID: "srv_1", TeamID: "team_1", Name: "web-1",
		NodeName: "web-1", Status: store.ServerNotReady,
	}}
	c := &fakeCluster{nodes: []api.NodeInfo{{Name: "web-1", Ready: true}}}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := db.servers[0].Status; got != store.ServerReady {
		t.Fatalf("server status is %q, want %q", got, store.ServerReady)
	}
	if got := notifier.eventsOf(notify.EventServerAdded); got != 1 {
		t.Fatalf("sent %d recovery notifications, want 1", got)
	}
	if _, ok := db.seen["srv_1"]; !ok {
		t.Fatal("a server that answered was not recorded as seen")
	}
}

// TestProvisioningServerIsLeftAlone: a server halfway through being added has
// no node yet, and marking it lost would fight the operation adding it.
func TestProvisioningServerIsLeftAlone(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{
		{ID: "srv_1", TeamID: "team_1", Name: "web-1", NodeName: "web-1", Status: store.ServerProvisioning},
		{ID: "srv_2", TeamID: "team_1", Name: "web-2", NodeName: "web-2", Status: store.ServerRemoving},
		{ID: "srv_3", TeamID: "team_1", Name: "web-3", NodeName: "", Status: store.ServerReady},
	}
	c := &fakeCluster{nodes: []api.NodeInfo{}}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	for _, server := range db.servers {
		if server.Status == store.ServerNotReady {
			t.Fatalf("%s was marked not ready while it was %s", server.Name, server.Status)
		}
	}
	if len(notifier.sent) != 0 {
		t.Fatalf("sent %d notifications about servers nobody had finished adding", len(notifier.sent))
	}
}

// TestUnhealthyAppIsReported: every instance gone is the failure the panel
// could previously only show to somebody already looking at the page.
func TestUnhealthyAppIsReported(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "shop", Slug: "shop", Status: "running"},
		TeamID: "team_1", Namespace: "acme-shop-production",
	}}
	c := &fakeCluster{apps: map[string]api.AppRuntimeStatus{
		"shop": {Phase: "crashing", DesiredReplicas: 2, ReadyReplicas: 0, Detail: "The container exits immediately."},
	}}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := db.appStatuses["app_1"]; got != "unhealthy" {
		t.Fatalf("app status is %q, want %q", got, "unhealthy")
	}
	if got := notifier.eventsOf(notify.EventAppUnhealthy); got != 1 {
		t.Fatalf("sent %d app.unhealthy notifications, want 1", got)
	}
	if got := notifier.sent[0].msg.Level; got != "error" {
		t.Errorf("notification level is %q, want error", got)
	}

	w.Once(t.Context())
	if got := notifier.eventsOf(notify.EventAppUnhealthy); got != 1 {
		t.Fatalf("an app that stayed down produced %d notifications, want 1", got)
	}
}

// TestScaledToZeroIsNotUnhealthy: an app deliberately at zero has no instances
// to be ready, and calling that a failure would make scale-to-zero unusable.
func TestScaledToZeroIsNotUnhealthy(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "docs", Slug: "docs", Status: "running"},
		TeamID: "team_1", Namespace: "acme-docs-production",
	}}
	c := &fakeCluster{apps: map[string]api.AppRuntimeStatus{
		"docs": {Phase: "stopped", DesiredReplicas: 0, ReadyReplicas: 0},
	}}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := db.appStatuses["app_1"]; got != "" {
		t.Fatalf("app status was changed to %q; a scaled-to-zero app is not unhealthy", got)
	}
	if len(notifier.sent) != 0 {
		t.Fatalf("sent %d notifications about an app that is scaled to zero", len(notifier.sent))
	}
}

// TestDeployingAppIsLeftAlone: a rollout has no ready instances for a moment,
// and the deployment owns the app's status until it finishes.
func TestDeployingAppIsLeftAlone(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "shop", Slug: "shop", Status: "running"},
		TeamID: "team_1", Namespace: "acme-shop-production",
	}}
	db.unfinished = []store.Deployment{{ID: "dep_1", AppID: "app_1", Status: store.DeployDeploying}}
	c := &fakeCluster{apps: map[string]api.AppRuntimeStatus{
		"shop": {Phase: "starting", DesiredReplicas: 2, ReadyReplicas: 0},
	}}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := db.appStatuses["app_1"]; got != "" {
		t.Fatalf("app status was changed to %q while it was being deployed", got)
	}
	if len(notifier.sent) != 0 {
		t.Fatalf("sent %d notifications about an app that was mid-rollout", len(notifier.sent))
	}
}

// TestCertificateFailureIsReported: a domain that never gets a certificate is
// the failure people wait out for hours, because nothing says it has stopped.
func TestCertificateFailureIsReported(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "shop", Slug: "shop", Status: "running"},
		TeamID: "team_1", Namespace: "acme-shop-production",
	}}
	db.domains["app_1"] = []store.Domain{{
		ID: "dom_1", AppID: "app_1", Hostname: "shop.example.com", TLS: true,
		Status: "pending", CreatedAt: time.Now().Add(-time.Hour),
	}}
	c := &fakeCluster{
		apps: map[string]api.AppRuntimeStatus{
			"shop": {Phase: "running", DesiredReplicas: 1, ReadyReplicas: 1},
		},
		certs: map[string]cluster.CertificateState{
			"shop-tls": {Found: true, Ready: false, Reason: "the DNS record does not point at this cluster"},
		},
	}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := notifier.eventsOf(notify.EventCertificate); got != 1 {
		t.Fatalf("sent %d certificate notifications, want 1", got)
	}
}

// TestIssuedCertificateMarksTheDomainActive: the common case has to stop
// showing "pending" once the certificate is there.
func TestIssuedCertificateMarksTheDomainActive(t *testing.T) {
	db := newFakeStore()
	db.apps = []store.DeployedApp{{
		App:    store.App{ID: "app_1", Name: "shop", Slug: "shop", Status: "running"},
		TeamID: "team_1", Namespace: "acme-shop-production",
	}}
	db.domains["app_1"] = []store.Domain{{
		ID: "dom_1", AppID: "app_1", Hostname: "shop.example.com", TLS: true,
		Status: "pending", CreatedAt: time.Now(),
	}}
	c := &fakeCluster{
		apps:  map[string]api.AppRuntimeStatus{"shop": {DesiredReplicas: 1, ReadyReplicas: 1}},
		certs: map[string]cluster.CertificateState{"shop-tls": {Found: true, Ready: true}},
	}

	w, notifier := testWatcher(t, db, c)
	w.Once(t.Context())

	if got := db.domainSet["dom_1"]; got != "active" {
		t.Fatalf("domain status is %q, want active", got)
	}
	if got := notifier.eventsOf(notify.EventCertificate); got != 0 {
		t.Fatalf("sent %d notifications about a certificate that worked", got)
	}
}

// TestNoClusterIsNotAFailure: a panel with no cluster still starts, and the
// watcher must simply do nothing rather than report the world as lost.
func TestNoClusterIsNotAFailure(t *testing.T) {
	db := newFakeStore()
	db.servers = []store.Server{{ID: "srv_1", TeamID: "team_1", NodeName: "web-1", Status: store.ServerReady}}
	notifier := &fakeNotifier{}
	w := New(db, nil, nil, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w.Once(t.Context())
	w.Run(t.Context())

	if len(notifier.sent) != 0 {
		t.Fatalf("sent %d notifications with no cluster to watch", len(notifier.sent))
	}
	if db.servers[0].Status != store.ServerReady {
		t.Fatal("a server was marked not ready because the panel has no cluster")
	}
}

// TestAbandonedPreviewsAreReclaimed: a preview is made by a webhook and was
// only ever removed by another one. A pull request merged while the panel was
// down leaves a namespace running forever, and a self-hosted cluster quietly
// runs out of memory.
func TestAbandonedPreviewsAreReclaimed(t *testing.T) {
	db := newFakeStore()
	db.stalePreviews = []store.Environment{
		{ID: "env_1", Namespace: "acme-shop-pr-7", Kind: store.EnvPreview},
		{ID: "env_2", Namespace: "acme-shop-pr-9", Kind: store.EnvPreview},
	}
	c := &fakeCluster{}

	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())

	if len(c.deletedSpaces) != 2 {
		t.Fatalf("removed %d namespaces, want 2", len(c.deletedSpaces))
	}
	if len(db.deletedEnvs) != 2 {
		t.Fatalf("removed %d environments, want 2", len(db.deletedEnvs))
	}
	// A week by default, counted from the last deployment.
	if age := time.Since(db.previewsBefore); age < 6*24*time.Hour || age > 8*24*time.Hour {
		t.Fatalf("previews are reclaimed after %s, want about a week", age)
	}
}

// TestThePanelRowSurvivesAFailedDelete: removing the row while the namespace
// is still there would leave a namespace nothing in the panel points at.
func TestThePanelRowSurvivesAFailedDelete(t *testing.T) {
	db := newFakeStore()
	db.stalePreviews = []store.Environment{{ID: "env_1", Namespace: "acme-shop-pr-7"}}
	c := &fakeCluster{deleteNamespace: errors.New("the cluster said no")}

	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())

	if len(db.deletedEnvs) != 0 {
		t.Fatal("the environment was forgotten while its namespace was still running")
	}
}

// TestReclaimingCanBeTurnedOff: an operator who would rather keep previews
// until their pull request closes sets the lifetime to zero.
func TestReclaimingCanBeTurnedOff(t *testing.T) {
	db := newFakeStore()
	db.settingValues[settings.KeyPreviewTTLDays] = "0"
	db.stalePreviews = []store.Environment{{ID: "env_1", Namespace: "acme-shop-pr-7"}}
	c := &fakeCluster{}

	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())

	if len(c.deletedSpaces) != 0 || len(db.deletedEnvs) != 0 {
		t.Fatal("previews were reclaimed although the lifetime is set to zero")
	}
}

// TestTheConfiguredLifetimeIsUsed.
func TestTheConfiguredLifetimeIsUsed(t *testing.T) {
	db := newFakeStore()
	db.settingValues[settings.KeyPreviewTTLDays] = "2"
	c := &fakeCluster{}

	w, _ := testWatcher(t, db, c)
	w.Once(t.Context())

	if age := time.Since(db.previewsBefore); age < 36*time.Hour || age > 60*time.Hour {
		t.Fatalf("previews are reclaimed after %s, want about two days", age)
	}
}
