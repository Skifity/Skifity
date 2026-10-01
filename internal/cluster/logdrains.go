package cluster

import (
	"context"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logdrain"
	"skifity/internal/store"
)

// The log collector: one Vector on every server, running while any team has a
// drain to send to, and gone when none has.
//
// Its configuration is rendered from every team's enabled drains at once —
// one collector reads every server's logs and routes each line to its own
// team's drains — and applied as a Secret, because it holds the drains'
// credentials. See internal/logdrain for what is in it and why.

// logCollectorApplyTimeout bounds applying or removing the collector.
const logCollectorApplyTimeout = 30 * time.Second

// RefreshLogDrains renders every team's drains into the collector and applies
// it now, or takes it away when there is nothing to send. Called when a
// drain changes.
func (c *Cluster) RefreshLogDrains(ctx context.Context) error {
	return c.refreshLogCollector(ctx, true)
}

// MaintainLogDrains does the same when something the collector's
// configuration is made from has changed: an app renamed or added, a project
// deleted, a team gone. Nothing is applied when nothing changed.
func (c *Cluster) MaintainLogDrains(ctx context.Context) {
	if err := c.refreshLogCollector(ctx, false); err != nil {
		c.log.Warn("the log collector could not be brought up to date", "error", err)
	}
}

func (c *Cluster) refreshLogCollector(ctx context.Context, force bool) error {
	c.logsMu.Lock()
	defer c.logsMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, logCollectorApplyTimeout)
	defer cancel()

	config, err := c.renderLogCollector(ctx)
	if err != nil {
		return c.logCollectorFailed(ctx, err)
	}
	current, err := c.db.GetComponent(ctx, logdrain.Component)
	if err != nil {
		return err
	}

	if config.Sinks == 0 {
		if !force && (current.Status == "absent" || current.Status == "removed") {
			return nil
		}
		for _, removal := range logdrain.Removals() {
			if err := c.client.Applier().Delete(ctx, removal.APIVersion, removal.Kind, removal.Namespace, removal.Name); err != nil {
				return c.logCollectorFailed(ctx, err)
			}
		}
		c.logsApplied = ""
		if current.Status != "removed" {
			c.log.Info("log collector removed", "reason", "no team has a drain to send to")
		}
		return c.db.SetComponent(ctx, store.ClusterComponent{
			Name: logdrain.Component, Status: "removed", Version: logdrain.VectorVersion, InstalledAt: time.Now().UTC(),
		})
	}

	if !force && current.Status == "installed" && config.Hash == c.logsApplied {
		return nil
	}
	objects, err := logdrain.Objects(config, "")
	if err != nil {
		return c.logCollectorFailed(ctx, err)
	}
	if err := c.client.Applier().ApplyAll(ctx, objects...); err != nil {
		return c.logCollectorFailed(ctx, err)
	}
	c.logsApplied = config.Hash
	// The number of drains and a fingerprint, never anything a drain holds.
	c.log.Info("log collector configuration applied", "drains", config.Sinks, "configuration", config.Hash)
	return c.db.SetComponent(ctx, store.ClusterComponent{
		Name: logdrain.Component, Status: "installed", Version: logdrain.VectorVersion, InstalledAt: time.Now().UTC(),
	})
}

// logCollectorFailed records why the collector could not be brought up to
// date, where the panel shows it, and says so.
func (c *Cluster) logCollectorFailed(ctx context.Context, cause error) error {
	problem := errdoc.LogCollectorFailed(cause.Error())
	problem.Err = cause
	c.logsApplied = ""
	record := context.WithoutCancel(ctx)
	if err := c.db.SetComponent(record, store.ClusterComponent{
		Name: logdrain.Component, Status: "failed", Version: logdrain.VectorVersion,
		InstalledAt: time.Now().UTC(), Detail: problem.Cause,
	}); err != nil {
		c.log.Warn("could not record the log collector's failure", "error", err)
	}
	return problem
}

// renderLogCollector reads every enabled drain, opens its credentials, and
// renders the collector's configuration.
func (c *Cluster) renderLogCollector(ctx context.Context) (logdrain.Config, error) {
	rows, err := c.db.ListEnabledLogDrains(ctx)
	if err != nil {
		return logdrain.Config{}, err
	}
	drains := make([]logdrain.Drain, 0, len(rows))
	var teams []string
	for _, row := range rows {
		drain, err := logdrain.FromRow(c.keyring, row)
		if err == nil {
			// A drain this version of the panel would refuse — saved by an
			// older one with looser rules — is left out the same way.
			err = drain.Validate()
		}
		if err != nil {
			// One drain whose credentials cannot be read must not take
			// every other team's away. It is left out and says so.
			c.log.Error("a log drain was left out of the collector", "drain", row.ID, "team", row.TeamID, "error", err)
			continue
		}
		drains = append(drains, drain)
		if !slices.Contains(teams, row.TeamID) {
			teams = append(teams, row.TeamID)
		}
	}
	names, err := c.db.LogDrainNames(ctx, teams)
	if err != nil {
		return logdrain.Config{}, err
	}
	rowsOfNames := make([]logdrain.NameRow, len(names))
	for i, name := range names {
		rowsOfNames[i] = logdrain.NameRow{Kind: name.Kind, ID: name.ID, Name: name.Name}
	}
	return logdrain.Render(logdrain.RenderInput{
		Drains: drains, Names: rowsOfNames, BuildsNamespace: kube.BuildsNamespace,
	})
}

// LogCollectorStatus reads how the collector is doing from what Kubernetes
// already knows. A collector that is not there answers as absent.
func (c *Cluster) LogCollectorStatus(ctx context.Context) (logdrain.CollectorStatus, error) {
	set, err := c.client.Clientset().AppsV1().DaemonSets(logdrain.Namespace).
		Get(ctx, logdrain.CollectorName, metav1.GetOptions{})
	if err != nil {
		if kube.IsNotFound(err) {
			return logdrain.Summarize(nil, nil, nil), nil
		}
		if kube.IsUnreachable(err) {
			return logdrain.CollectorStatus{}, errdoc.ClusterUnreachable(err)
		}
		return logdrain.CollectorStatus{}, err
	}
	pods, err := c.client.Clientset().CoreV1().Pods(logdrain.Namespace).
		List(ctx, metav1.ListOptions{LabelSelector: logdrain.Selector()})
	if err != nil {
		return logdrain.CollectorStatus{}, err
	}
	events, err := c.client.Events(ctx, logdrain.Namespace, kube.EventScope{
		Owns:     func(kind, name string) bool { return kind == "DaemonSet" && name == logdrain.CollectorName },
		Selector: logdrain.Selector(),
	})
	if err != nil {
		// The counts and the pods say most of it; the events are the detail.
		c.log.Debug("the log collector's events could not be read", "error", err)
		events = nil
	}
	return logdrain.Summarize(set, pods.Items, events), nil
}
