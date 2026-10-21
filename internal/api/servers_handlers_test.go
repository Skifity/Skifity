package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

// A server that is being added, removed or promoted is not removed or promoted
// again on top of it: the provisioner keeps one run per operation, not per
// server, so a second removal would drain the same node alongside the first.
func TestAServerIsNotRemovedOrPromotedWhileSomethingElseIsBeingDone(t *testing.T) {
	h := newHarness(t)
	acme := adminTenant(h, "acme")
	server := store.Server{
		TeamID: acme.team.ID, Name: "worker-1", Host: "203.0.113.20",
		SSHPort: 22, SSHUser: "root", Role: "worker", Status: store.ServerRemoving, NodeName: "worker-1",
	}
	if err := h.db.CreateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	op := store.Operation{TeamID: acme.team.ID, Kind: "server.remove", TargetType: "server", TargetID: server.ID,
		Status: store.OpRunning}
	if err := h.db.CreateOperation(t.Context(), &op, []string{"cordon", "drain"}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodDelete, "/api/servers/" + server.ID + "?wipe=true"},
		{http.MethodPost, "/api/servers/" + server.ID + "/promote"},
	} {
		if status, body := h.do(acme, request.method, request.path, nil); status != http.StatusConflict ||
			!strings.Contains(body, "server.busy") {
			t.Errorf("%s %s while a removal runs: %d %s", request.method, request.path, status, body)
		}
	}
}

// The status line is stored in English and named by a key, so the panel can
// show it in the reader's language: a fixed sentence by its own name, a
// failure by its problem's code and values, and Kubernetes' own words not at
// all.
func TestAServersStatusLineIsNamedForTheInterface(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	add := func(name string, status store.ServerStatus, detail string) store.Server {
		t.Helper()
		server := store.Server{
			TeamID: acme.team.ID, Name: name, Host: name + ".example.test", SSHPort: 22, SSHUser: "root",
			Role: "worker", Status: status, StatusDetail: detail,
		}
		if err := h.db.CreateServer(t.Context(), &server); err != nil {
			t.Fatal(err)
		}
		return server
	}
	add("gone", store.ServerNotReady, ServerDetailNodeGone)
	add("kubelet", store.ServerNotReady, "Kubelet stopped posting node status.")
	failed := add("failed", store.ServerFailed, "Kubernetes could not be installed on worker-1")
	op := store.Operation{TeamID: acme.team.ID, Kind: "server.add", TargetType: "server", TargetID: failed.ID}
	if err := h.db.CreateOperation(t.Context(), &op, []string{"install-k3s"}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetStepStatus(t.Context(), op.ID, "install-k3s", store.StepFailed, store.StepNote{
		Message: failed.StatusDetail, Key: "problem:k3s.install_failed", Args: []string{"worker-1"},
	}, ""); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/servers", nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	var list struct {
		Items []serverView `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	byName := map[string]serverView{}
	for _, item := range list.Items {
		byName[item.Name] = item
	}
	if got := byName["gone"].StatusKey; got != "nodeGone" {
		t.Errorf("a node that left is named %q", got)
	}
	if got := byName["kubelet"].StatusKey; got != "" {
		t.Errorf("Kubernetes' own words were named %q", got)
	}
	if got := byName["failed"]; got.StatusKey != "problem:k3s.install_failed" ||
		len(got.StatusArgs) != 1 || got.StatusArgs[0] != "worker-1" {
		t.Errorf("a failure is named %q %v", got.StatusKey, got.StatusArgs)
	}
	if got := byName["failed"].StatusDetail; got != failed.StatusDetail {
		t.Errorf("the English went missing: %q", got)
	}

	status, body = h.do(acme, http.MethodGet, "/api/servers/"+failed.ID, nil)
	if status != http.StatusOK || !strings.Contains(body, `"status_key":"problem:k3s.install_failed"`) ||
		!strings.Contains(body, `"operation":`) {
		t.Fatalf("one server: %d %s", status, body)
	}
}
