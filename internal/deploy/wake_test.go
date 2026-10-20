package deploy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/kube"
	"skifity/internal/store"
)

// sleepingApp is an app that scales to zero, asleep, on a cluster of fakes.
func sleepingApp(t *testing.T, replicas int32) (*Deployer, *store.DB, *fake.Clientset, kube.AppSpec, store.Deployment) {
	t.Helper()
	d, db, app, env, deployment := deployedApp(t)
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app.Slug, Namespace: env.Namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	})
	withCluster(t, d, db, clientset, syncableDynamic())
	spec := kube.AppSpec{
		Name: app.Slug, Namespace: env.Namespace, DisplayName: app.Name,
		Port: 3000, HealthPath: "/healthz", ScaleToZero: true,
		Domains: []kube.DomainSpec{{Hostname: "web.203.0.113.10.sslip.io"}},
	}
	return d, db, clientset, spec, deployment
}

// interceptor stands in for KEDA's: it scales the app up when asked for it,
// and says what it was asked for.
func interceptor(t *testing.T, clientset *fake.Clientset, spec kube.AppSpec, wakes bool) func() []string {
	t.Helper()
	var mu sync.Mutex
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Host+r.URL.Path)
		mu.Unlock()
		if wakes && r.Host == spec.Domains[0].Hostname {
			one := int32(1)
			deployment, _ := clientset.AppsV1().Deployments(spec.Namespace).Get(r.Context(), spec.Name, metav1.GetOptions{})
			deployment.Spec.Replicas = &one
			_, _ = clientset.AppsV1().Deployments(spec.Namespace).Update(r.Context(), deployment, metav1.UpdateOptions{})
		}
	}))
	t.Cleanup(server.Close)
	was, wasWait, wasEvery := interceptorURL, wakeWait, wakeEvery
	interceptorURL, wakeWait, wakeEvery = server.URL, 2*time.Second, 50*time.Millisecond
	t.Cleanup(func() { interceptorURL, wakeWait, wakeEvery = was, wasWait, wasEvery })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, seen...)
	}
}

func logOf(t *testing.T, db *store.DB, deploymentID string) string {
	t.Helper()
	lines, err := db.ListBuildLogs(t.Context(), deploymentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range lines {
		out = append(out, line.Line)
	}
	return strings.Join(out, "\n")
}

// TestASleepingAppIsWokenForItsDeploy: a rollout of no instances finishes at
// once, so every deploy of a sleeping app succeeded whatever it deployed.
func TestASleepingAppIsWokenForItsDeploy(t *testing.T) {
	d, db, clientset, spec, deployment := sleepingApp(t, 0)
	seen := interceptor(t, clientset, spec, true)

	stop := d.wakeForRollout(t.Context(), deployment.ID, spec)
	defer stop()

	asleep, err := d.cluster.Client().Asleep(t.Context(), spec.Namespace, spec.Name)
	if err != nil || asleep {
		t.Fatalf("the app is still asleep (%v) once the deploy goes on to wait for it", err)
	}
	if asked := seen(); len(asked) == 0 || asked[0] != "web.203.0.113.10.sslip.io/healthz" {
		t.Errorf("the interceptor was asked for %v, not the app by its own hostname", asked)
	}
	if !strings.Contains(logOf(t, db, deployment.ID), "is asleep, so it is woken") {
		t.Errorf("the deploy's log does not say the app was woken:\n%s", logOf(t, db, deployment.ID))
	}
}

// An app the add-on never wakes is said to be unproven, not failed: failing
// the deploy would have the next sync put the previous version back.
func TestAnAppThatDoesNotWakeIsSaidToBeUnproven(t *testing.T) {
	d, db, clientset, spec, deployment := sleepingApp(t, 0)
	interceptor(t, clientset, spec, false)

	d.wakeForRollout(t.Context(), deployment.ID, spec)()
	if log := logOf(t, db, deployment.ID); !strings.Contains(log, "has not been seen to start") {
		t.Errorf("the deploy's log does not say the version is unproven:\n%s", log)
	}
}

// An app that is awake, or that does not sleep, is left alone.
func TestAnAwakeAppIsNotAskedFor(t *testing.T) {
	d, _, clientset, spec, deployment := sleepingApp(t, 2)
	seen := interceptor(t, clientset, spec, true)
	d.wakeForRollout(t.Context(), deployment.ID, spec)()

	spec.ScaleToZero = false
	d.wakeForRollout(t.Context(), deployment.ID, spec)()
	time.Sleep(100 * time.Millisecond)
	if asked := seen(); len(asked) != 0 {
		t.Errorf("an app that was awake was asked for: %v", asked)
	}
}
