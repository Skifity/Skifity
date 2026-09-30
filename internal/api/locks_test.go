package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/store"
)

func TestLockingAnAppsDeploys(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	path := "/api/apps/" + app.ID + "/lock"

	if status, _ := h.do(acme, http.MethodPut, path, map[string]string{"reason": "  "}); status != http.StatusBadRequest {
		t.Fatalf("a lock with no reason answered %d", status)
	}
	status, body := h.do(acme, http.MethodPut, path, map[string]string{"reason": "incident 42"})
	if status != http.StatusOK {
		t.Fatalf("locking answered %d: %s", status, body)
	}

	// The app says it is locked, by whom and why.
	status, body = h.do(acme, http.MethodGet, "/api/apps/"+app.ID, nil)
	var answer struct {
		ID         string            `json:"id"`
		DeployLock *store.DeployLock `json:"deploy_lock"`
	}
	_ = json.Unmarshal([]byte(body), &answer)
	if status != http.StatusOK || answer.ID != app.ID || answer.DeployLock == nil ||
		answer.DeployLock.Reason != "incident 42" || answer.DeployLock.LockedBy != acme.user.Email {
		t.Fatalf("the app answered %d: %s", status, body)
	}

	if status, _ := h.do(acme, http.MethodDelete, path, nil); status != http.StatusOK {
		t.Fatalf("unlocking answered %d", status)
	}
	if _, err := h.db.GetDeployLock(t.Context(), app.ID); err == nil {
		t.Fatal("the lock is still there")
	}
	for _, action := range []string{"app.deploys_locked", "app.deploys_unlocked"} {
		if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, action, "", 5); len(events) != 1 {
			t.Errorf("%s was not audited", action)
		}
	}
}

func TestARollbackPlanSaysWhatItChangesAndWhatItLeaves(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	old := store.Deployment{AppID: app.ID, Trigger: "manual", CommitSHA: "aaa1111", Image: "registry/web:1",
		RuntimeSpec: `{"replicas":4,"port":` + "0" + `}`}
	if err := h.db.CreateDeployment(t.Context(), &old); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpdateDeploymentStatus(t.Context(), old.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(acme, http.MethodGet, "/api/apps/"+app.ID+"/rollback/"+old.ID+"/plan", nil)
	if status != http.StatusOK {
		t.Fatalf("the plan answered %d: %s", status, body)
	}
	for _, want := range []string{`"field":"replicas"`, `"to":"4"`, `"unchanged":["variables","domains","volumes"]`, `"commit_sha":"aaa1111"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the plan does not say %s:\n%s", want, body)
		}
	}
}
