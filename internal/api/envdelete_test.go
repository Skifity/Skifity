package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"skifity/internal/store"
)

// namespaceRefuser is a cluster that will not delete one namespace, and
// remembers the ones it did delete.
type namespaceRefuser struct {
	fakeCluster
	mu      sync.Mutex
	refuse  string
	deleted []string
}

func (c *namespaceRefuser) DeleteNamespace(_ context.Context, namespace string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if namespace == c.refuse {
		return errors.New("the API server did not answer in time")
	}
	c.deleted = append(c.deleted, namespace)
	return nil
}

func (c *namespaceRefuser) set(refuse string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refuse = refuse
}

func (c *namespaceRefuser) removed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.deleted...)
}

// An environment whose namespace the cluster would not delete was deleted
// anyway: the failure was logged, the rows went, and the request answered 200
// while its apps went on running with nothing in the panel to find them by.
func TestAnEnvironmentTheClusterKeepsIsKept(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	cluster := &namespaceRefuser{refuse: acme.env.Namespace}
	h.withCluster(cluster)
	path := "/api/environments/" + acme.env.ID

	status, body := h.do(acme, http.MethodDelete, path, nil)
	if status != http.StatusBadGateway || !strings.Contains(body, "environment.not_removed") {
		t.Fatalf("answered %d: %s", status, truncate(body, 300))
	}
	if _, err := h.db.GetEnvironment(ctx, acme.env.ID); err != nil {
		t.Errorf("the environment's row went: %v", err)
	}
	if _, err := h.db.GetApp(ctx, app.ID); err != nil {
		t.Errorf("its app's row went: %v", err)
	}

	cluster.set("")
	if status, body := h.do(acme, http.MethodDelete, path, nil); status != http.StatusOK {
		t.Fatalf("once the cluster let it go, deleting answered %d: %s", status, truncate(body, 300))
	}
	if _, err := h.db.GetEnvironment(ctx, acme.env.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the environment is still there: %v", err)
	}
}

// The same for a project: one environment the cluster keeps keeps the
// project and all its environments in the panel.
func TestAProjectWithAnEnvironmentTheClusterKeepsIsKept(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	staging := store.Environment{
		ProjectID: acme.project.ID, Name: "staging", Slug: "staging", Namespace: "acme-staging",
	}
	if err := h.db.CreateEnvironment(ctx, &staging); err != nil {
		t.Fatal(err)
	}
	h.withCluster(&namespaceRefuser{refuse: staging.Namespace})

	status, body := h.do(acme, http.MethodDelete, "/api/projects/"+acme.project.ID, nil)
	if status != http.StatusBadGateway || !strings.Contains(body, "environment.not_removed") {
		t.Fatalf("answered %d: %s", status, truncate(body, 300))
	}
	if _, err := h.db.GetProject(ctx, acme.project.ID); err != nil {
		t.Errorf("the project's row went: %v", err)
	}
	if envs, _ := h.db.ListEnvironments(ctx, acme.project.ID); len(envs) != 2 {
		t.Errorf("%d environments left, want both", len(envs))
	}
}

// A deployment carries on after its namespace is deleted, and its rollout puts
// the namespace back. So neither an environment nor its project is deleted
// while one of its apps is being deployed, and nothing is removed from the
// cluster in asking.
func TestNothingIsDeletedUnderADeployment(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	app := h.app(acme, "web")
	cluster := &namespaceRefuser{}
	h.withCluster(cluster)

	deployment := store.Deployment{AppID: app.ID, Status: store.DeployBuilding}
	if err := h.db.CreateDeployment(ctx, &deployment); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/environments/" + acme.env.ID, "/api/projects/" + acme.project.ID} {
		status, body := h.do(acme, http.MethodDelete, path, nil)
		if status != http.StatusConflict || !strings.Contains(body, "environment.deploying") ||
			!strings.Contains(body, "web") {
			t.Errorf("deleting %s under a deployment answered %d: %s", path, status, truncate(body, 300))
		}
	}
	if removed := cluster.removed(); len(removed) != 0 {
		t.Errorf("a refused delete removed %v from the cluster", removed)
	}
	if _, err := h.db.GetEnvironment(ctx, acme.env.ID); err != nil {
		t.Errorf("the environment's row went: %v", err)
	}

	if err := h.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeploySucceeded, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(acme, http.MethodDelete, "/api/environments/"+acme.env.ID, nil); status != http.StatusOK {
		t.Errorf("once it had finished, deleting answered %d: %s", status, truncate(body, 300))
	}
}
