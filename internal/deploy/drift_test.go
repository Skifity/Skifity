package deploy

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/cluster"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// The drift check against Kubernetes' own fake clients: the deployer renders
// the app exactly as it applies it, reads each object back, and says what is
// different. Nothing here runs a cluster.

// deployedApp is the test app with a version that succeeded, as the database
// has it after a deploy.
func deployedApp(t *testing.T) (*Deployer, *store.DB, store.App, store.Environment, store.Deployment) {
	t.Helper()
	d, db, app, env := testDeployer(t)
	deployment := store.Deployment{AppID: app.ID, CommitSHA: "abc123def456", Image: "registry.internal/acme/web:abc123"}
	if err := db.CreateDeployment(t.Context(), &deployment); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAppStatus(t.Context(), app.ID, "running"); err != nil {
		t.Fatal(err)
	}
	return d, db, app, env, deployment
}

// withCluster gives the deployer a cluster made of fakes.
func withCluster(t *testing.T, d *Deployer, db *store.DB, clientset *fake.Clientset, dynamic *dynamicfake.FakeDynamicClient) {
	t.Helper()
	d.cluster = cluster.New(kube.NewClientWith(clientset, dynamic, ""), db, d.keyring, d.log)
}

// syncableDynamic is an empty fake that an apply can run against: the apply
// lists the app's Traefik middlewares to remove a redirect nobody wants any
// more, and the fake refuses to list a kind it was not told about.
func syncableDynamic() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"}: "MiddlewareList",
	})
}

// asServerHasIt is an object the way the API server hands it back: numbers
// decoded as int64, and a record that the panel applied it.
func asServerHasIt(t *testing.T, obj *unstructured.Unstructured, managers ...metav1.ManagedFieldsEntry) *unstructured.Unstructured {
	t.Helper()
	data, err := obj.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	live := &unstructured.Unstructured{}
	if err := live.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	applied := metav1.NewTime(time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC))
	live.SetManagedFields(append([]metav1.ManagedFieldsEntry{{
		Manager: kube.FieldManager, Operation: metav1.ManagedFieldsOperationApply, Time: &applied,
		FieldsType: "FieldsV1", FieldsV1: metav1.NewFieldsV1(`{"f:metadata":{"f:labels":{}}}`),
	}}, managers...))
	return live
}

func renderedFor(t *testing.T, d *Deployer, app store.App, env store.Environment, deployment store.Deployment) rendered {
	t.Helper()
	out, err := d.render(t.Context(), deployment, app, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestAnAppAsThePanelAppliedItIsInSync(t *testing.T) {
	d, db, app, env, deployment := deployedApp(t)
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	out := renderedFor(t, d, app, env, deployment)

	var live []runtime.Object
	for _, obj := range out.objects {
		live = append(live, asServerHasIt(t, obj))
	}
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live...))

	report, err := d.CheckDrift(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if report.Status != store.DriftInSync || len(report.Items) != 0 {
		t.Fatalf("an untouched app is %s: %+v", report.Status, report.Items)
	}
}

func TestAnEditedAndADeletedObjectAreReportedWithoutTheSecrets(t *testing.T) {
	d, db, app, env, deployment := deployedApp(t)
	const secret = "fake-token-for-the-test-1234"
	setVariable(t, d, db, app.ID, "API_TOKEN", secret, false)
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	out := renderedFor(t, d, app, env, deployment)

	edited := metav1.NewTime(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	var live []runtime.Object
	for _, obj := range out.objects {
		switch obj.GetKind() {
		case "Service":
			// Deleted with kubectl delete: not there at all.
			continue
		case "Deployment":
			changed := asServerHasIt(t, obj, metav1.ManagedFieldsEntry{
				Manager: "kubectl-set", Operation: metav1.ManagedFieldsOperationUpdate, Time: &edited,
				FieldsType: "FieldsV1", FieldsV1: metav1.NewFieldsV1(
					`{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"web\"}":{` +
						`"f:image":{},"f:env":{"k:{\"name\":\"PORT\"}":{"f:value":{}}}}}}}}}`),
			})
			container := changed.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
			container["image"] = "nginx:latest"
			for _, entry := range container["env"].([]any) {
				if entry.(map[string]any)["name"] == "PORT" {
					// Somebody pasted a secret where a port goes.
					entry.(map[string]any)["value"] = secret
				}
			}
			live = append(live, changed)
		default:
			live = append(live, asServerHasIt(t, obj))
		}
	}
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live...))

	report, err := d.CheckDrift(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if report.Status != store.DriftMissing {
		t.Fatalf("with a Service deleted the app is %s", report.Status)
	}
	found := map[string]bool{}
	for _, item := range report.Items {
		found[item.Kind+"/"+item.Name+" "+item.Path+" "+item.Change] = true
		switch {
		case item.Path == "spec.template.spec.containers[name=web].image":
			if item.Panel != deployment.Image || item.Live != "nginx:latest" || item.ChangedBy != "kubectl-set" ||
				!item.ChangedAt.Equal(edited.Time) {
				t.Errorf("the image change is %+v", item)
			}
		case strings.HasSuffix(item.Path, "env[name=PORT].value"):
			if item.Panel != "3000" || strings.Contains(item.Live, secret) {
				t.Errorf("the port change is %+v", item)
			}
		}
		encoded, _ := json.Marshal(item)
		if strings.Contains(string(encoded), secret) {
			t.Errorf("a secret reached the report: %s", encoded)
		}
	}
	for _, want := range []string{
		"Service/web  deleted",
		"Deployment/web spec.template.spec.containers[name=web].image changed",
		"Deployment/web spec.template.spec.containers[name=web].env[name=PORT].value changed",
	} {
		if !found[want] {
			t.Errorf("the report does not have %q; it has %v", want, found)
		}
	}
}

func TestAnAppNeverDeployedHasNothingToCompare(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	report, err := d.CheckDrift(t.Context(), app.ID)
	if err != nil || report.Status != store.DriftNotDeployed {
		t.Fatalf("got %+v, %v", report, err)
	}
}

// appliedObjects captures every server-side apply the fake cluster is sent,
// and answers each as though it were applied.
type appliedObjects struct {
	mu      sync.Mutex
	objects []*unstructured.Unstructured
}

func (a *appliedObjects) react(action k8stesting.Action) (bool, runtime.Object, error) {
	patch, ok := action.(k8stesting.PatchAction)
	if !ok || patch.GetPatchType() != types.ApplyPatchType {
		return false, nil, nil
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(patch.GetPatch()); err != nil {
		return true, nil, err
	}
	a.mu.Lock()
	a.objects = append(a.objects, obj)
	a.mu.Unlock()
	return true, obj, nil
}

// "Put it back" is the apply a variable change makes: the rendered objects,
// stamped the same, and no deployment recorded.
func TestPuttingBackAppliesTheRenderedObjectsAndRecordsNoDeployment(t *testing.T) {
	d, db, app, env, deployment := deployedApp(t)
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	want := renderedFor(t, d, app, env, deployment)

	applied := &appliedObjects{}
	dynamic := syncableDynamic()
	dynamic.PrependReactor("patch", "*", applied.react)
	// The rollout it waits for has finished.
	replicas := int32(1)
	ready := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: env.Namespace, Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, UpdatedReplicas: 1, ReadyReplicas: 1, Replicas: 1},
	}
	withCluster(t, d, db, fake.NewSimpleClientset(ready), dynamic)

	before, err := db.ListDeployments(t.Context(), app.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RepairDrift(t.Context(), app.ID); err != nil {
		t.Fatalf("RepairDrift: %v", err)
	}
	after, err := db.ListDeployments(t.Context(), app.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("putting the app back recorded a deployment: %d before, %d after", len(before), len(after))
	}

	got := map[string]string{}
	for _, obj := range applied.objects {
		got[obj.GetKind()+"/"+obj.GetName()] = obj.GetAnnotations()[kube.AppliedAnnotation]
	}
	for _, obj := range append(want.objects, want.processObjects...) {
		key := obj.GetKind() + "/" + obj.GetName()
		stamp, ok := got[key]
		if !ok {
			t.Errorf("%s was not applied; applied were %v", key, keys(got))
			continue
		}
		if stamp != obj.GetAnnotations()[kube.AppliedAnnotation] {
			t.Errorf("%s was applied as something other than what was rendered", key)
		}
	}
	if d.Busy(app.ID) {
		t.Error("the app still reads as being applied after the apply finished")
	}
	// And what it wrote is recorded, so a deleted object can be told from
	// one the panel has yet to create.
	stored, err := db.GetAppDrift(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range want.objects {
		key := obj.GetKind() + "/" + obj.GetName()
		if stored.Applied[key] != obj.GetAnnotations()[kube.AppliedAnnotation] {
			t.Errorf("%s is recorded as applied with %q", key, stored.Applied[key])
		}
	}
}

// An object the panel would apply and never wrote — a domain added while the
// cluster could not be reached — is the panel's to create, not somebody's
// deletion.
func TestAnObjectThePanelHasYetToCreateIsNotDeleted(t *testing.T) {
	d, db, app, env, deployment := deployedApp(t)
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	out := renderedFor(t, d, app, env, deployment)

	var live []runtime.Object
	written := map[string]string{}
	for _, obj := range out.objects {
		if obj.GetKind() == "Service" {
			continue
		}
		live = append(live, asServerHasIt(t, obj))
		written[obj.GetKind()+"/"+obj.GetName()] = obj.GetAnnotations()[kube.AppliedAnnotation]
	}
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live...))

	if err := db.RecordApplied(t.Context(), app.ID, written, true); err != nil {
		t.Fatal(err)
	}
	report, err := d.CheckDrift(t.Context(), app.ID)
	if err != nil || report.Status != store.DriftInSync {
		t.Fatalf("a Service the panel never wrote was reported: %+v, %v", report, err)
	}

	// Once it has been written, its absence is somebody's doing.
	written["Service/web"] = "any"
	if err := db.RecordApplied(t.Context(), app.ID, written, true); err != nil {
		t.Fatal(err)
	}
	report, err = d.CheckDrift(t.Context(), app.ID)
	if err != nil || report.Status != store.DriftMissing {
		t.Fatalf("a Service the panel wrote and somebody deleted: %+v, %v", report, err)
	}
}

// While a deploy or a sync is changing the app, nothing is compared: what the
// cluster holds is half the old and half the new.
func TestNothingIsComparedWhileThePanelIsChangingTheApp(t *testing.T) {
	d, db, app, _, _ := deployedApp(t)
	reads := 0
	dynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamic.PrependReactor("get", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		reads++
		return false, nil, nil
	})
	withCluster(t, d, db, fake.NewSimpleClientset(), dynamic)

	rolling := store.Deployment{AppID: app.ID, Image: "registry.internal/acme/web:def456"}
	if err := db.CreateDeployment(t.Context(), &rolling); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `UPDATE deployments SET status = 'deploying' WHERE id = ?`, rolling.ID); err != nil {
		t.Fatal(err)
	}
	report, err := d.CheckDrift(t.Context(), app.ID)
	if err != nil || report.Status != store.DriftApplying || reads != 0 {
		t.Fatalf("during a rollout: %+v, %v, %d reads", report, err, reads)
	}

	if _, err := db.Exec(t.Context(), `UPDATE deployments SET status = 'succeeded' WHERE id = ?`, rolling.ID); err != nil {
		t.Fatal(err)
	}
	d.beginSync(app.ID)
	report, err = d.CheckDrift(t.Context(), app.ID)
	d.endSync(app.ID)
	if err != nil || report.Status != store.DriftApplying || reads != 0 {
		t.Fatalf("during a sync: %+v, %v, %d reads", report, err, reads)
	}
}
