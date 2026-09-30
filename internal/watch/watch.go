// Package watch keeps the panel's picture of the cluster current, and says so
// when it changes for the worse.
//
// Nothing else in the panel polls. A deployment reports its own progress, and
// every page reads the cluster when somebody opens it. That leaves exactly the
// states nobody is looking at — a node that stopped answering at three in the
// morning, an app whose last instance crashed, a certificate that never
// issued — which is what a notification is for.
package watch

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/notify"
	"skifity/internal/runsafe"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Interval is how often the cluster is compared with the database.
//
// A minute is often enough that an operator hears about a lost node while it
// still matters, and rare enough that the reads are free next to what the
// Kubernetes API already does.
const Interval = time.Minute

// Store is the part of the database a watcher uses.
type Store interface {
	ListAllServers(ctx context.Context) ([]store.Server, error)
	SetServerStatus(ctx context.Context, id string, status store.ServerStatus, detail string) error
	TouchServerSeen(ctx context.Context, id string, at time.Time) error

	ListDeployedApps(ctx context.Context) ([]store.DeployedApp, error)
	SetAppStatus(ctx context.Context, id, status string) error
	ListUnfinishedDeployments(ctx context.Context) ([]store.Deployment, error)

	ListDomains(ctx context.Context, appID string) ([]store.Domain, error)
	SetDomainStatus(ctx context.Context, id, status, detail string) error

	StalePreviewEnvironments(ctx context.Context, before time.Time) ([]store.Environment, error)
	DeleteEnvironment(ctx context.Context, id string) error
	GetSetting(ctx context.Context, key string) (value string, encrypted bool, err error)
}

// Cluster is the part of the cluster a watcher reads, plus the one thing it
// removes.
type Cluster interface {
	Summary(ctx context.Context) (api.ClusterSummary, error)
	AppStatus(ctx context.Context, namespace, appSlug string) (api.AppRuntimeStatus, error)
	CertificateStatus(ctx context.Context, namespace, name string) (cluster.CertificateState, error)
	DeleteNamespace(ctx context.Context, namespace string) error
}

// Publisher is the part of the event hub a watcher uses to refresh open pages.
type Publisher interface {
	Publish(topic, eventType string, data any) events.Event
}

// Watcher compares the cluster with the database, once a minute.
type Watcher struct {
	db       Store
	cluster  Cluster
	hub      Publisher
	notifier notify.Notifier
	log      *slog.Logger
	// Interval is how often Run compares the two. Zero means Interval.
	interval time.Duration
}

// New builds a Watcher. cluster, hub and notifier may all be nil, and the
// watcher then does correspondingly less rather than crashing: a panel with no
// cluster still has to start.
func New(db Store, c Cluster, hub Publisher, notifier notify.Notifier, log *slog.Logger) *Watcher {
	return &Watcher{db: db, cluster: c, hub: hub, notifier: notifier, log: log, interval: Interval}
}

// Run compares the cluster with the database until the context is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	if w.cluster == nil {
		w.log.Info("no cluster; not watching for lost servers or unhealthy apps")
		return
	}

	// The first pass waits: at start-up the cluster connection may not be up
	// yet, and reporting everything as lost because the panel was faster than
	// its own network would be worse than saying nothing.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		// A panic costs one pass. Recovering around the loop would keep the
		// panel alive with nothing watching it, which is the failure nobody
		// notices until a server has been down for a week.
		func() {
			defer runsafe.Recover(w.log, "the health pass", nil)
			w.Once(ctx)
		}()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once runs a single pass. It is exported so a test can run one deterministically.
func (w *Watcher) Once(ctx context.Context) {
	if w.cluster == nil {
		return
	}
	// A pass must finish well inside its own interval, or two passes overlap
	// and the panel argues with itself about what a server's status is.
	ctx, cancel := context.WithTimeout(ctx, w.interval-w.interval/4)
	defer cancel()

	w.checkServers(ctx)
	w.checkApps(ctx)
	w.reclaimPreviews(ctx)
}

// DefaultPreviewTTLDays is how long a preview environment survives with no
// deployment when nothing says otherwise.
const DefaultPreviewTTLDays = 7

// reclaimPreviews removes preview environments nothing has deployed to.
//
// A preview is made by a webhook and was only ever removed by another one. A
// pull request merged while the panel was down, a repository whose webhook was
// deleted, a branch renamed on the server — each left a namespace running
// forever, and a self-hosted cluster quietly runs out of memory. That is the
// complaint people have about every product that does preview environments.
func (w *Watcher) reclaimPreviews(ctx context.Context) {
	days := w.previewTTLDays(ctx)
	if days <= 0 {
		// Deliberately off: the operator would rather keep them.
		return
	}

	stale, err := w.db.StalePreviewEnvironments(ctx, time.Now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		w.log.Warn("could not list preview environments", "error", err)
		return
	}
	for _, env := range stale {
		w.log.Info("removing a preview environment nothing has deployed to",
			"environment", env.ID, "namespace", env.Namespace, "after_days", days)
		if err := w.cluster.DeleteNamespace(ctx, env.Namespace); err != nil {
			w.log.Warn("could not remove a preview namespace",
				"namespace", env.Namespace, "error", err)
			// The row stays, so the next pass tries again rather than leaving
			// a namespace with nothing in the panel pointing at it.
			continue
		}
		if err := w.db.DeleteEnvironment(ctx, env.ID); err != nil {
			w.log.Warn("could not remove a preview environment", "environment", env.ID, "error", err)
		}
	}
}

// previewTTLDays reads the setting, falling back to the default.
func (w *Watcher) previewTTLDays(ctx context.Context) int {
	value, _, err := w.db.GetSetting(ctx, settings.KeyPreviewTTLDays)
	if err != nil || strings.TrimSpace(value) == "" {
		return DefaultPreviewTTLDays
	}
	days, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		w.log.Warn("the preview lifetime setting is not a number", "value", value)
		return DefaultPreviewTTLDays
	}
	return days
}

// checkServers marks servers whose node stopped being ready, and records that
// the rest were seen.
func (w *Watcher) checkServers(ctx context.Context) {
	summary, err := w.cluster.Summary(ctx)
	if err != nil {
		// The cluster being unreachable is already visible on every page. It
		// does not mean every server is lost, and saying so would be wrong.
		w.log.Debug("could not read the cluster while watching servers", "error", err)
		return
	}

	nodes := make(map[string]api.NodeInfo, len(summary.Nodes))
	for _, node := range summary.Nodes {
		nodes[node.Name] = node
	}

	servers, err := w.db.ListAllServers(ctx)
	if err != nil {
		w.log.Warn("could not list servers while watching", "error", err)
		return
	}

	now := time.Now()
	for _, server := range servers {
		// Only a server that finished provisioning has a node to compare
		// against. One still being added, or being removed, is somebody's
		// deliberate act and its status belongs to them.
		if server.Status != store.ServerReady && server.Status != store.ServerNotReady {
			continue
		}
		if server.NodeName == "" {
			continue
		}

		node, present := nodes[server.NodeName]
		switch {
		case present && node.Ready:
			if err := w.db.TouchServerSeen(ctx, server.ID, now); err != nil {
				w.log.Warn("could not record that a server was seen", "server", server.ID, "error", err)
			}
			if server.Status == store.ServerReady {
				continue
			}
			w.setServerStatus(ctx, server, store.ServerReady, "")
			w.notifyTeam(ctx, server.TeamID, notify.EventServerAdded, notify.Message{
				Title:  server.Name + " is back",
				Body:   "The node is ready again and can run apps.",
				Level:  "success",
				Path:   "/servers/" + server.ID,
				Fields: map[string]string{"Server": server.Name},
			})

		default:
			detail := "The node is no longer part of the cluster."
			if present {
				detail = node.Reason
				if detail == "" {
					detail = "The node reports that it is not ready."
				}
			}
			// A server that is already known to be down is not news.
			if server.Status == store.ServerNotReady {
				continue
			}
			w.setServerStatus(ctx, server, store.ServerNotReady, detail)
			w.log.Warn("a server stopped being ready",
				"server", server.ID, "node", server.NodeName, "detail", detail)
			w.notifyTeam(ctx, server.TeamID, notify.EventServerLost, notify.Message{
				Title:  server.Name + " stopped answering",
				Body:   detail,
				Level:  "error",
				Path:   "/servers/" + server.ID,
				Fields: map[string]string{"Server": server.Name, "Node": server.NodeName},
			})
		}
	}
}

// checkApps notices an app whose instances have all gone, and an app whose
// certificate will not issue.
func (w *Watcher) checkApps(ctx context.Context) {
	apps, err := w.db.ListDeployedApps(ctx)
	if err != nil {
		w.log.Warn("could not list apps while watching", "error", err)
		return
	}

	// An app being deployed right now is expected to have no ready instances
	// for a moment. The deployment owns the app's status until it finishes.
	deploying := map[string]bool{}
	if unfinished, err := w.db.ListUnfinishedDeployments(ctx); err == nil {
		for _, deployment := range unfinished {
			deploying[deployment.AppID] = true
		}
	}

	for _, app := range apps {
		if deploying[app.ID] {
			continue
		}
		w.checkApp(ctx, app)
		w.checkCertificate(ctx, app)
	}
}

func (w *Watcher) checkApp(ctx context.Context, app store.DeployedApp) {
	// Only an app the panel believes is up can fall down. One left failed by a
	// deployment, or never started, is already showing the right thing.
	if app.Status != "running" && app.Status != "unhealthy" {
		return
	}

	status, err := w.cluster.AppStatus(ctx, app.Namespace, app.Slug)
	if err != nil {
		w.log.Debug("could not read an app's status while watching", "app", app.ID, "error", err)
		return
	}

	// Scaled to zero on purpose is not unhealthy: there is nothing to be ready.
	healthy := status.DesiredReplicas == 0 || status.ReadyReplicas > 0
	want := "running"
	if !healthy {
		want = "unhealthy"
	}
	if app.Status == want {
		return
	}

	if err := w.db.SetAppStatus(ctx, app.ID, want); err != nil {
		w.log.Warn("could not update an app's status", "app", app.ID, "error", err)
		return
	}
	w.publish(app.TeamID, "app", map[string]any{"id": app.ID, "status": want})

	if healthy {
		w.notifyTeam(ctx, app.TeamID, notify.EventAppUnhealthy, notify.Message{
			Title:  app.Name + " is serving again",
			Body:   "Instances are ready and the app is answering.",
			Level:  "success",
			Path:   "/apps/" + app.ID,
			Fields: map[string]string{"App": app.Name},
		})
		return
	}

	detail := status.Detail
	if detail == "" {
		detail = "No instance is ready."
	}
	w.log.Warn("an app has no ready instances", "app", app.ID, "detail", detail)
	w.notifyTeam(ctx, app.TeamID, notify.EventAppUnhealthy, notify.Message{
		Title: app.Name + " has no running instances",
		Body:  detail,
		Level: "error",
		Path:  "/apps/" + app.ID,
		Fields: map[string]string{
			"App":      app.Name,
			"Expected": strconv.Itoa(status.DesiredReplicas),
			"Ready":    strconv.Itoa(status.ReadyReplicas),
		},
	})
}

// checkCertificate records whether cert-manager issued an app's certificate.
//
// Without this a domain sits at "pending" forever, which reads as "any moment
// now" when the real answer is often "never, because the DNS record points
// somewhere else".
func (w *Watcher) checkCertificate(ctx context.Context, app store.DeployedApp) {
	domains, err := w.db.ListDomains(ctx, app.ID)
	if err != nil {
		w.log.Warn("could not list domains while watching", "app", app.ID, "error", err)
		return
	}

	secured := make([]store.Domain, 0, len(domains))
	for _, domain := range domains {
		if domain.TLS {
			secured = append(secured, domain)
		}
	}
	if len(secured) == 0 {
		return
	}

	state, err := w.cluster.CertificateStatus(ctx, app.Namespace, kube.ResourceName(app.Slug, "tls"))
	if err != nil {
		w.log.Debug("could not read a certificate while watching", "app", app.ID, "error", err)
		return
	}
	if !state.Found {
		return
	}

	// Three states, not two. A certificate requested a minute ago is pending
	// and nobody should hear about it; one that has been trying for a quarter
	// of an hour has hit something it will not solve by waiting, which is
	// almost always a DNS record pointing somewhere else.
	status := "pending"
	switch {
	case state.Ready:
		status = "active"
	case stale(secured):
		status = "failed"
	}

	changed, failing := false, false
	for _, domain := range secured {
		if domain.Status == status {
			continue
		}
		if err := w.db.SetDomainStatus(ctx, domain.ID, status, state.Reason); err != nil {
			w.log.Warn("could not update a domain's status", "domain", domain.ID, "error", err)
			continue
		}
		changed = true
		// Only a domain that has just turned bad is news; one that was already
		// failing when this pass started has been reported. "failed" is the
		// word the rest of the panel uses for this, so the badge already knows
		// how to colour it.
		if status == "failed" {
			failing = true
		}
	}
	if !changed {
		return
	}

	w.publish(app.TeamID, "domain", map[string]any{"app_id": app.ID, "status": status})
	if !failing {
		return
	}

	reason := state.Reason
	if reason == "" {
		reason = "The certificate has not been issued."
	}
	w.log.Warn("a certificate is not being issued", "app", app.ID, "detail", reason)
	w.notifyTeam(ctx, app.TeamID, notify.EventCertificate, notify.Message{
		Title:  "No certificate for " + secured[0].Hostname,
		Body:   reason,
		Level:  "error",
		Path:   "/apps/" + app.ID + "/domains",
		Fields: map[string]string{"App": app.Name, "Domain": secured[0].Hostname},
	})
}

// stale reports whether the oldest of these domains has been waiting long
// enough that a certificate should have arrived.
func stale(domains []store.Domain) bool {
	for _, domain := range domains {
		if time.Since(domain.CreatedAt) > 15*time.Minute {
			return true
		}
	}
	return false
}

func (w *Watcher) setServerStatus(ctx context.Context, server store.Server, status store.ServerStatus, detail string) {
	if err := w.db.SetServerStatus(ctx, server.ID, status, detail); err != nil {
		w.log.Warn("could not update a server's status", "server", server.ID, "error", err)
		return
	}
	w.publish(server.TeamID, "server", map[string]any{
		"id": server.ID, "status": string(status), "status_detail": detail,
	})
}

func (w *Watcher) publish(teamID, kind string, data any) {
	if w.hub == nil || teamID == "" {
		return
	}
	w.hub.Publish(events.TeamTopic(teamID), kind, data)
}

func (w *Watcher) notifyTeam(ctx context.Context, teamID, event string, msg notify.Message) {
	if w.notifier == nil || teamID == "" {
		return
	}
	w.notifier.Notify(ctx, teamID, event, msg)
}
