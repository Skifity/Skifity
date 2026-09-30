package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// One cluster serves every team on the panel. Its summary listed every node —
// addresses, labels, how busy — to a viewer of any team, including the servers
// other teams added.
func TestATeamSeesOnlyItsOwnServersInTheCluster(t *testing.T) {
	h := newHarness(t)
	admin := adminTenant(h, "ops")
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	for owner, node := range map[*tenant]string{&acme: "node-a", &globex: "node-b"} {
		server := h.node(*owner, node)
		server.NodeName = node
		if err := h.db.UpdateServer(t.Context(), &server); err != nil {
			t.Fatal(err)
		}
	}
	summary := ClusterSummary{Reachable: true, ReadyNodes: 2, TotalCPUM: 8000, Nodes: []NodeInfo{
		{Name: "node-a", Ready: true, InternalIP: "10.0.0.1"},
		{Name: "node-b", Ready: true, InternalIP: "10.0.0.2"},
	}}
	h.withCluster(nodesCluster{summary: summary})

	read := func(as tenant) ClusterSummary {
		t.Helper()
		status, body := h.do(as, http.MethodGet, "/api/teams/"+as.team.ID+"/cluster", nil)
		var got ClusterSummary
		if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil {
			t.Fatalf("the cluster answered %d: %s", status, body)
		}
		return got
	}
	if got := read(acme); len(got.Nodes) != 1 || got.Nodes[0].Name != "node-a" || got.ReadyNodes != 1 {
		t.Fatalf("acme sees %+v", got)
	}
	// The whole panel is a panel administrator's to see.
	if got := read(admin); len(got.Nodes) != 2 {
		t.Fatalf("a panel administrator sees %d nodes", len(got.Nodes))
	}
}
