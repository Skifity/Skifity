package api

import (
	"context"
	"strings"

	"skifity/internal/store"
)

// The server the panel is already running on.
//
// Skifity installs itself onto a machine and makes that machine the cluster's
// first node. Nothing recorded it: `CreateServer` was called from exactly one
// place, the SSH provisioner, so a brand new install opened on
//
//	No servers yet — Add your first server
//
// while looking at a panel served by a cluster that was already running. The
// only sensible thing a person could do next was type that machine's own
// address into the form, which the preflight refused with a message about
// something listening on port 6443. Correct, and no use at all as an answer.
//
// So the nodes the cluster already has are adopted into the team: listed like
// any other server, and not managed like one. The panel has no key to them and
// put nothing on them, so `adopted` marks the row and the operations that would
// have to reach them over SSH refuse with a reason instead of failing at the
// connection.

// adoptClusterNodes records the cluster's existing nodes as this team's
// servers, and returns how many it added.
//
// Best effort by design: it runs when a team is created, and a cluster that is
// briefly unreachable must not be able to fail first-run setup.
func (s *Server) adoptClusterNodes(ctx context.Context, teamID string) int {
	if s.cluster == nil {
		return 0
	}
	summary, err := s.cluster.Summary(ctx)
	if err != nil || !summary.Reachable {
		return 0
	}

	existing, err := s.db.ListServers(ctx, teamID)
	if err != nil {
		return 0
	}
	known := make(map[string]bool, len(existing)*3)
	for _, server := range existing {
		for _, key := range []string{server.NodeName, server.Host, server.InternalIP, server.ExternalIP} {
			if key != "" {
				known[key] = true
			}
		}
	}

	added := 0
	for _, node := range summary.Nodes {
		if known[node.Name] || known[node.InternalIP] || known[node.ExternalIP] {
			continue
		}
		// The address people would reach it on, falling back to the one the
		// cluster uses between nodes. A server row with no address at all is
		// worse than none: the panel would show a machine nobody can name.
		host := node.ExternalIP
		if host == "" {
			host = node.InternalIP
		}
		if host == "" {
			continue
		}

		server := store.Server{
			TeamID:     teamID,
			Name:       node.Name,
			Host:       host,
			SSHUser:    "root",
			Role:       nodeRole(node.Roles),
			Status:     nodeStatus(node.Ready),
			NodeName:   node.Name,
			ExternalIP: node.ExternalIP,
			InternalIP: node.InternalIP,
			OSInfo:     node.OS,
			Arch:       node.Architecture,
			CPUCores:   int(node.CPUCapacityM / 1000),
			MemoryMB:   int(node.MemCapacityMB),
			Adopted:    true,
		}
		if server.Status == store.ServerReady {
			server.StatusDetail = "Found in the cluster. Skifity did not install this server and has no way in over SSH."
		}
		if err := s.db.CreateServer(ctx, &server); err != nil {
			s.log.Warn("could not adopt a node the cluster already has",
				"node", node.Name, "team", teamID, "error", err)
			continue
		}
		added++
	}
	if added > 0 {
		s.log.Info("adopted the nodes the cluster already had", "team", teamID, "count", added)
	}
	return added
}

func nodeRole(roles []string) string {
	for _, role := range roles {
		if strings.Contains(role, "control-plane") || strings.Contains(role, "master") {
			return "control-plane"
		}
	}
	return "worker"
}

func nodeStatus(ready bool) store.ServerStatus {
	if ready {
		return store.ServerReady
	}
	return store.ServerNotReady
}
