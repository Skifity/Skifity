package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// The first screen of a brand new install.
//
// `CreateServer` was called from exactly one place — the SSH provisioner — so
// the machine Skifity installs itself onto was never recorded. The panel opened
// on "No servers yet. Add your first server", pointing at a form that asks for
// an IP address and root credentials, for the machine the person was already
// looking at. Typing it in was refused with a message about port 6443: correct,
// and no use at all as an answer.

// nodesCluster answers Summary with the nodes a real cluster would report.
type nodesCluster struct {
	Cluster
	summary ClusterSummary
}

func (n nodesCluster) Summary(context.Context) (ClusterSummary, error) { return n.summary, nil }

func oneNode() ClusterSummary {
	return ClusterSummary{
		Reachable: true,
		Nodes: []NodeInfo{{
			Name: "vps-1", Ready: true, Roles: []string{"control-plane", "master"},
			InternalIP: "10.0.0.4", ExternalIP: "203.0.113.10",
			OS: "Ubuntu 24.04", Architecture: "amd64",
			CPUCapacityM: 4000, MemCapacityMB: 8192,
		}},
	}
}

// serversOf reads a team's server list back over HTTP, which is what the panel
// itself does to decide whether to show "add your first server".
func serversOf(t *testing.T, h *harness, as tenant) []store.Server {
	t.Helper()
	status, body := h.do(as, http.MethodGet, "/api/teams/"+as.team.ID+"/servers", nil)
	if status != http.StatusOK {
		t.Fatalf("listing servers answered %d\n%s", status, body)
	}
	var out struct {
		Items []store.Server `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode the server list: %v\n%s", err, body)
	}
	return out.Items
}

func TestTheMachineThePanelRunsOnBecomesAServer(t *testing.T) {
	h := newHarness(t)
	h.withCluster(nodesCluster{summary: oneNode()})
	acme := h.newTenant("acme")

	if got := serversOf(t, h, acme); len(got) != 0 {
		t.Fatalf("the team started with %d servers", len(got))
	}
	if added := h.api.adoptClusterNodes(t.Context(), acme.team.ID); added != 1 {
		t.Fatalf("adopted %d nodes, want the one the cluster has", added)
	}

	servers := serversOf(t, h, acme)
	if len(servers) != 1 {
		t.Fatalf("the panel lists %d servers, want the one it is running on", len(servers))
	}
	server := servers[0]
	if !server.Adopted {
		t.Error("the node was recorded as one Skifity installed, which it did not")
	}
	if server.Role != "control-plane" {
		t.Errorf("role is %q, want control-plane", server.Role)
	}
	if server.Status != store.ServerReady {
		t.Errorf("status is %q, want ready: the cluster said the node was ready", server.Status)
	}
	if server.Host != "203.0.113.10" {
		t.Errorf("host is %q, want the address people reach it on", server.Host)
	}
	if server.NodeName != "vps-1" || server.CPUCores != 4 || server.MemoryMB != 8192 {
		t.Errorf("the node's own details did not come across: %+v", server)
	}
	if server.SSHKeyEnc != "" {
		t.Error("an adopted server was given a key, and there is no key to give it")
	}
}

// TestAnAdoptedServerIsListedAndNotManaged: the panel has no key to it and put
// nothing on it, so the three operations that would have to reach it over SSH
// say so rather than failing at the connection with what reads like a network
// problem.
func TestAnAdoptedServerIsListedAndNotManaged(t *testing.T) {
	h := newHarness(t)
	h.withCluster(nodesCluster{summary: oneNode()})
	acme := h.newTenant("acme")
	h.api.adoptClusterNodes(t.Context(), acme.team.ID)

	servers := serversOf(t, h, acme)
	if len(servers) != 1 {
		t.Fatalf("expected the adopted node, got %d servers", len(servers))
	}
	id := servers[0].ID

	for _, tc := range []struct {
		what   string
		method string
		path   string
	}{
		{"removing it", http.MethodDelete, "/api/servers/" + id},
		{"retrying it", http.MethodPost, "/api/servers/" + id + "/retry"},
		{"promoting it", http.MethodPost, "/api/servers/" + id + "/promote"},
	} {
		status, body := h.do(acme, tc.method, tc.path, nil)
		if status != http.StatusConflict {
			t.Errorf("%s answered %d, want 409 with a reason\n%s", tc.what, status, body)
			continue
		}
		if !strings.Contains(body, "server.not_ours") {
			t.Errorf("%s was refused for the wrong reason:\n%s", tc.what, body)
		}
	}
}

// TestAdoptionDoesNotRunTwice: a node is matched by its name and by either of
// its addresses, so a panel restarted, upgraded or reconnected does not
// accumulate a second row for the same machine.
func TestAdoptionDoesNotRunTwice(t *testing.T) {
	h := newHarness(t)
	h.withCluster(nodesCluster{summary: oneNode()})
	acme := h.newTenant("acme")

	h.api.adoptClusterNodes(t.Context(), acme.team.ID)
	if added := h.api.adoptClusterNodes(t.Context(), acme.team.ID); added != 0 {
		t.Fatalf("a second pass adopted %d more rows for the same machine", added)
	}
	if got := serversOf(t, h, acme); len(got) != 1 {
		t.Fatalf("the machine is listed %d times", len(got))
	}
}

// TestAServerSomebodyAddedIsNotAdoptedOverTheTopOfItself: a node that already
// has a row — because Skifity provisioned it — must keep that row, keys and
// all, rather than gaining a second one that says nobody can manage it.
func TestAServerSomebodyAddedIsNotAdoptedOverTheTopOfItself(t *testing.T) {
	h := newHarness(t)
	h.withCluster(nodesCluster{summary: oneNode()})
	acme := h.newTenant("acme")

	provisioned := store.Server{
		TeamID: acme.team.ID, Name: "added by hand", Host: "203.0.113.10",
		NodeName: "vps-1", Role: "control-plane", Status: store.ServerReady,
	}
	if err := h.db.CreateServer(t.Context(), &provisioned); err != nil {
		t.Fatalf("create server: %v", err)
	}

	if added := h.api.adoptClusterNodes(t.Context(), acme.team.ID); added != 0 {
		t.Fatalf("adopted %d nodes over a server that was already recorded", added)
	}
	servers := serversOf(t, h, acme)
	if len(servers) != 1 || servers[0].Adopted {
		t.Fatalf("the provisioned server was duplicated or overwritten: %+v", servers)
	}
}

// TestAdoptionNeverFailsSetup: a cluster that is briefly unreachable, or a
// panel with none at all, must not be able to stop an account being created.
func TestAdoptionNeverFailsSetup(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	if added := h.api.adoptClusterNodes(t.Context(), acme.team.ID); added != 0 {
		t.Fatalf("a panel with no cluster adopted %d nodes", added)
	}

	h.withCluster(nodesCluster{summary: ClusterSummary{Reachable: false}})
	if added := h.api.adoptClusterNodes(t.Context(), acme.team.ID); added != 0 {
		t.Fatalf("an unreachable cluster adopted %d nodes", added)
	}
}
