package cluster

import (
	"context"
	"strings"

	"skifity/internal/kube"
	"skifity/internal/version"
)

// MaintainNetworkPolicies keeps the fence around the nodes in step with the
// nodes.
//
// Every tenant policy names the cluster's own public addresses, so that a pod
// cannot reach the Kubernetes API, the kubelet, SSH or the registry on the
// machine it runs on (kube.InternetEgress). Those policies are written when an
// environment is prepared, which is when a deploy happens; a server added later
// would stay reachable from every environment that had not deployed since, and
// a quiet environment may not deploy for months. So the policies are applied
// again when the set of addresses changes, and once after every start.
//
// It asks for the nodes and applies nothing when nothing is different, which
// is nearly always, so it is cheap enough for the minute tick.
func (c *Cluster) MaintainNetworkPolicies(ctx context.Context) {
	nodes, err := c.client.NodeAddresses(ctx)
	if err != nil {
		// The cluster not answering is somebody else's alarm.
		c.log.Debug("could not read the nodes' addresses", "error", err)
		return
	}
	key := strings.Join(kube.PublicNodeAddresses(nodes), ",")

	// A pass still running is not started twice.
	if !c.policiesMu.TryLock() {
		return
	}
	defer c.policiesMu.Unlock()
	if c.policiesApplied && c.policiesFor == key {
		return
	}

	failed := 0
	environments, err := c.client.ManagedNamespaces(ctx, version.LabelKey("team-id"))
	if err != nil {
		c.log.Warn("could not list the environments' namespaces", "error", err)
		return
	}
	for _, namespace := range environments {
		if err := c.client.RefreshEnvironmentPolicies(ctx, namespace, nodes); err != nil {
			c.log.Warn("could not refresh an environment's network policies", "namespace", namespace, "error", err)
			failed++
		}
	}

	// Builds and plugins have a fence of their own. The build namespace is
	// only touched when it exists: a cluster with no builder has no use for it.
	if ok, err := c.client.NamespaceExists(ctx, c.client.BuildNamespace()); err == nil && ok {
		if err := c.EnsureBuildNamespace(ctx); err != nil {
			c.log.Warn("could not refresh the build namespace's network policies", "error", err)
			failed++
		}
	}
	plugins, err := c.client.ManagedNamespaces(ctx, version.LabelKey("plugin-id"))
	if err != nil {
		c.log.Warn("could not list the plugins' namespaces", "error", err)
		failed++
	}
	for _, namespace := range plugins {
		if err := c.client.RefreshPluginPolicies(ctx, namespace, nodes); err != nil {
			c.log.Warn("could not refresh a plugin's network policies", "namespace", namespace, "error", err)
			failed++
		}
	}

	// A failure is tried again at the next tick rather than remembered as done.
	if failed == 0 {
		c.policiesApplied = true
		c.policiesFor = key
		c.log.Info("network policies are in step with the nodes' addresses",
			"environments", len(environments), "plugins", len(plugins))
	}
}
