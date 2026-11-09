package deploy

import (
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/store"
	"skifity/internal/version"
)

// A version that stops as it starts, against Kubernetes' fake clients: the
// Deployment is rolling the new version out, and its one new instance has
// stopped twice and printed why.

const fakeToken = "fake-token-0123456789abcdef"

// crashCluster is a cluster on which the new deployment's instance keeps
// exiting. Applying the serving deployment again does what the controller
// would: the template is that version's again and the new instance goes.
type crashCluster struct {
	typed   *fake.Clientset
	mu      sync.Mutex
	applied []*unstructured.Unstructured
}

func newCrashCluster(t *testing.T, d *Deployer, db *store.DB, env store.Environment, bad, good store.Deployment) *crashCluster {
	t.Helper()
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: env.Namespace, Generation: 2},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "web"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{version.LabelKey("deployment-id"): bad.ID},
			}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, ReadyReplicas: 1},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-bad-a", Namespace: env.Namespace,
			Labels:      map[string]string{"app.kubernetes.io/name": "web"},
			Annotations: map[string]string{version.LabelKey("deployment-id"): bad.ID},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "web", RestartCount: 2,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 1, Reason: "Error",
			}},
		}}},
	}
	c := &crashCluster{typed: fake.NewSimpleClientset(deployment, pod)}
	c.typed.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "log" {
			return false, nil, nil
		}
		return true, &runtime.Unknown{Raw: []byte("listening on 3000\nconnecting with " + fakeToken + "\nError: the database refused the connection\n")}, nil
	})

	dynamic := syncableDynamic()
	dynamic.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(k8stesting.PatchAction)
		if !ok || patch.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patch.GetPatch()); err != nil {
			return true, nil, err
		}
		c.mu.Lock()
		c.applied = append(c.applied, obj)
		c.mu.Unlock()
		if obj.GetKind() == "Deployment" && obj.GetName() == "web" {
			id, _, _ := unstructured.NestedString(obj.Object, "spec", "template", "metadata", "annotations", version.LabelKey("deployment-id"))
			if id == good.ID {
				back := deployment.DeepCopy()
				back.Spec.Template.Annotations[version.LabelKey("deployment-id")] = good.ID
				back.Status = appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}
				_, _ = c.typed.AppsV1().Deployments(env.Namespace).Update(t.Context(), back, metav1.UpdateOptions{})
				_ = c.typed.CoreV1().Pods(env.Namespace).Delete(t.Context(), pod.Name, metav1.DeleteOptions{})
			}
		}
		return true, obj, nil
	})
	withCluster(t, d, db, c.typed, dynamic)
	return c
}

// image is the image of the last Deployment the panel applied for the app.
func (c *crashCluster) image() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	image := ""
	for _, obj := range c.applied {
		if obj.GetKind() != "Deployment" || obj.GetName() != "web" {
			continue
		}
		containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
		if len(containers) > 0 {
			image, _, _ = unstructured.NestedString(containers[0].(map[string]any), "image")
		}
	}
	return image
}

func ptr[T any](v T) *T { return &v }

func TestAVersionThatCrashesFailsWithWhatItPrinted(t *testing.T) {
	d, db, app, env, good := deployedApp(t)
	sealed, err := d.keyring.Seal([]byte(fakeToken), "variable:"+app.ID+":API_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetVariable(t.Context(), &store.Variable{AppID: app.ID, Key: "API_TOKEN", IsSecret: true}, sealed); err != nil {
		t.Fatal(err)
	}
	bad := store.Deployment{AppID: app.ID, CommitSHA: "bbbbbbbbbbbb", Image: "registry.internal/acme/web:bad", Status: store.DeployDeploying}
	if err := db.CreateDeployment(t.Context(), &bad); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `UPDATE deployments SET status = 'deploying' WHERE id = ?`, bad.ID); err != nil {
		t.Fatal(err)
	}
	cluster := newCrashCluster(t, d, db, env, bad, good)

	_, err = d.apply(t.Context(), bad, app, env, true)
	problem := errdoc.From(err)
	if problem.Code != "deploy.crashed" {
		t.Fatalf("the rollout failed with %s: %v", problem.Code, err)
	}
	if problem.Context["kind"] != kube.StopExited || problem.Context["exit_code"] != "1" || problem.Context["stops"] != "2" {
		t.Errorf("the problem's context is %v", problem.Context)
	}
	if cluster.image() != bad.Image {
		t.Fatalf("the new version was applied as %q", cluster.image())
	}

	d.fail(t.Context(), bad, problem)

	stored, err := db.GetDeployment(t.Context(), bad.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != store.DeployFailed || stored.ErrorCode != "deploy.crashed" {
		t.Fatalf("the deployment is %s with %q", stored.Status, stored.ErrorCode)
	}
	if stored.Problem == nil || stored.Problem.Context["kind"] != kube.StopExited ||
		!strings.Contains(stored.Problem.Context["log_tail"], "refused the connection") {
		t.Fatalf("the problem kept with the deployment is %+v", stored.Problem)
	}

	lines, err := db.ListBuildLogs(t.Context(), bad.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for _, line := range lines {
		log.WriteString(line.Line + "\n")
	}
	text := log.String()
	for _, want := range []string{"stopped twice", "Its last lines:", "Error: the database refused the connection", logging.Redacted} {
		if !strings.Contains(text, want) {
			t.Errorf("the deployment log does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, fakeToken) || strings.Contains(stored.Problem.Context["log_tail"], fakeToken) {
		t.Errorf("a secret variable's value reached the deployment log:\n%s", text)
	}

	// The app updates by rolling, so the version that worked kept serving;
	// its configuration is applied again, and the instance that would
	// never start goes with the new version's template.
	if got := cluster.image(); got != good.Image {
		t.Errorf("the Deployment was left on %q, want the version that worked, %q", got, good.Image)
	}
	if !strings.Contains(text, "Applying version 1 again") {
		t.Errorf("the log does not say the serving version was applied again:\n%s", text)
	}
	if rolled := rollbacksOf(t, db, app.ID); len(rolled) != 0 {
		t.Errorf("a rolling app was rolled back by a new deployment: %+v", rolled)
	}
}

// An app with a volume has its old version stopped first, so a version that
// crashes leaves it down, and it is rolled back the way a timeout is.
func TestAnAppWithAVolumeIsRolledBackWhenItsNewVersionCrashes(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	if err := db.CreateVolume(t.Context(), &store.Volume{AppID: app.ID, Name: "data", MountPath: "/data", SizeGB: 1}); err != nil {
		t.Fatal(err)
	}
	good, bad := failedRollout(t, d, db, app)

	d.fail(t.Context(), bad, errdoc.DeployCrashed(app.Name, "web-bad-a", 3, 137, "OOMKilled", kube.StopOutOfMemory, ""))

	rolled := rollbacksOf(t, db, app.ID)
	if len(rolled) != 1 || rolled[0].RollbackOf != good.Number {
		t.Fatalf("the app was not put back on the version that worked: %+v", rolled)
	}
}

// The reason a rollout that ran out of time gives is the new instance it was
// waiting for, not the old version's "1 instance(s) running".
func TestATimeoutSaysWhichNewInstanceItWaitedFor(t *testing.T) {
	d, db, app, env, good := deployedApp(t)
	waiting := &kube.RolloutTimeout{
		Ready: 0, Desired: 1, Instance: "web-bad-a",
		Waiting: "its health check fails: Readiness probe failed: HTTP probe failed with statuscode: 404",
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: env.Namespace, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "web"}},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, ReadyReplicas: 1},
	}
	withCluster(t, d, db, fake.NewSimpleClientset(deployment), syncableDynamic())

	problem := d.rolloutFailed(t.Context(), good, app, env, rendered{}, waiting)
	if problem.Code != "deploy.rollout_timeout" {
		t.Fatalf("the failure is %s", problem.Code)
	}
	if !strings.Contains(problem.Cause, "web-bad-a is not ready") || !strings.Contains(problem.Cause, "statuscode: 404") {
		t.Errorf("the cause does not name the instance and what it waits for: %q", problem.Cause)
	}
	if strings.Contains(problem.Cause, "running") || !strings.HasPrefix(problem.Cause, "0 of 1") {
		t.Errorf("the cause counts the old version: %q", problem.Cause)
	}
}
