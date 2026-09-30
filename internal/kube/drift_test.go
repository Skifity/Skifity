package kube

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// What the panel would apply, stamped the way the deployer stamps it.
func prepared(t *testing.T, obj any) *unstructured.Unstructured {
	t.Helper()
	objects, err := Prepare(obj)
	if err != nil || len(objects) != 1 {
		t.Fatalf("Prepare: %v (%d objects)", err, len(objects))
	}
	return objects[0]
}

// asLive is the object as the API server hands it back: decoded the way the
// dynamic client decodes, with whole numbers as int64, and with what the
// server writes into it that the panel never did.
func asLive(t *testing.T, desired *unstructured.Unstructured, managers ...metav1.ManagedFieldsEntry) *unstructured.Unstructured {
	t.Helper()
	data, err := desired.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	live := &unstructured.Unstructured{}
	if err := live.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	live.SetResourceVersion("4711")
	live.SetUID("0b1e0a57-0000-4000-8000-000000000000")
	live.SetGeneration(3)
	live.SetManagedFields(managers)
	return live
}

var (
	panelApplied = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	later        = panelApplied.Add(time.Hour)
)

func managed(manager, operation string, at time.Time, fields string) metav1.ManagedFieldsEntry {
	stamp := metav1.NewTime(at)
	return metav1.ManagedFieldsEntry{
		Manager: manager, Operation: metav1.ManagedFieldsOperationType(operation),
		APIVersion: "apps/v1", Time: &stamp, FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(fields),
	}
}

// The panel's own apply, holding the fields a test cares about.
func panelOwns(fields string) metav1.ManagedFieldsEntry {
	return managed(FieldManager, "Apply", panelApplied, fields)
}

const imageField = `{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"web\"}":{"f:image":{}}}}}}}`

func TestAnObjectAsTheServerKeepsItIsInSync(t *testing.T) {
	desired := prepared(t, BuildDeployment(baseSpec()))
	live := asLive(t, desired, panelOwns(imageField))

	// What the API server and the controllers write into a Deployment that
	// the panel never set. None of it is drift.
	spec := live.Object["spec"].(map[string]any)
	template := spec["template"].(map[string]any)["spec"].(map[string]any)
	template["dnsPolicy"] = "ClusterFirst"
	template["restartPolicy"] = "Always"
	template["schedulerName"] = "default-scheduler"
	container := template["containers"].([]any)[0].(map[string]any)
	container["terminationMessagePath"] = "/dev/termination-log"
	container["resources"].(map[string]any)["limits"].(map[string]any)["cpu"] = "1"
	annotations := live.GetAnnotations()
	annotations["deployment.kubernetes.io/revision"] = "7"
	live.SetAnnotations(annotations)
	live.Object["status"] = map[string]any{"replicas": int64(1), "readyReplicas": int64(1)}

	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("an untouched Deployment was reported as changed: %+v", changes)
	}
}

func TestAFieldAnotherManagerChangedIsDriftAndSaysWho(t *testing.T) {
	desired := prepared(t, BuildDeployment(baseSpec()))
	live := asLive(t, desired,
		panelOwns(`{"f:spec":{"f:replicas":{}}}`),
		managed("kubectl-set", "Update", later, imageField))
	containers := live.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)
	containers[0].(map[string]any)["image"] = "nginx:latest"

	changes := CompareObject(desired, live)
	if len(changes) != 1 {
		t.Fatalf("want one change, got %+v", changes)
	}
	got := changes[0]
	if got.Path != "spec.template.spec.containers[name=web].image" || got.Change != DriftChanged {
		t.Errorf("the change is %+v", got)
	}
	if got.Panel != "registry.internal/acme/web:abc123" || got.Live != "nginx:latest" {
		t.Errorf("the values are %q and %q", got.Panel, got.Live)
	}
	if got.Manager != "kubectl-set" || !got.ChangedAt.Equal(later) {
		t.Errorf("who and when: %q at %s", got.Manager, got.ChangedAt)
	}
}

// The panel changed its mind — a new image, a variable waiting for a
// rebuild — and has not applied it yet. The field is still the panel's, so
// nobody else changed anything.
func TestThePanelsOwnPendingChangeIsNotDrift(t *testing.T) {
	applied := prepared(t, BuildDeployment(baseSpec()))
	live := asLive(t, applied, panelOwns(imageField))

	next := baseSpec()
	next.Image = "registry.internal/acme/web:def456"
	next.Revision = "a-new-revision"
	desired := prepared(t, BuildDeployment(next))

	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("a change the panel has not applied yet was reported as somebody else's: %+v", changes)
	}
}

func TestARemovedFieldIsDriftOnlyOnTheObjectThePanelApplied(t *testing.T) {
	spec := baseSpec()
	spec.EnvFromSecret = "web-env"
	desired := prepared(t, BuildDeployment(spec))
	live := asLive(t, desired,
		panelOwns(`{"f:spec":{"f:replicas":{}}}`),
		managed("kubectl-edit", "Update", later, `{"f:metadata":{"f:annotations":{"f:note":{}}}}`))
	container := live.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	delete(container, "envFrom")

	changes := CompareObject(desired, live)
	if len(changes) != 1 {
		t.Fatalf("want the missing envFrom, got %+v", changes)
	}
	if changes[0].Change != DriftRemoved || changes[0].Path != "spec.template.spec.containers[name=web].envFrom" {
		t.Errorf("the change is %+v", changes[0])
	}
	// Removing a field leaves no owner behind; whoever last wrote to the
	// object after the panel is the one to ask.
	if changes[0].Manager != "kubectl-edit" {
		t.Errorf("attributed to %q", changes[0].Manager)
	}

	// The same live object, compared with a rendering the panel has not
	// applied: nobody holds envFrom, and the panel may simply be about to add
	// it. That is not reported.
	next := spec
	next.Revision = "not-applied-yet"
	if changes := CompareObject(prepared(t, BuildDeployment(next)), live); len(changes) != 0 {
		t.Fatalf("a field the panel has not applied yet was reported as removed: %+v", changes)
	}
}

func TestAnAutoscaledAppsReplicasAreTheAutoscalers(t *testing.T) {
	spec := baseSpec()
	spec.Autoscale, spec.MinReplicas, spec.MaxReplicas, spec.CPUTarget = true, 2, 6, 70
	desired := prepared(t, BuildDeployment(spec))
	if _, found, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec", "replicas"); found {
		t.Fatal("an autoscaled app's Deployment names a replica count, so this test is not testing anything")
	}
	live := asLive(t, desired,
		panelOwns(imageField),
		managed("kube-controller-manager", "Update", later, `{"f:spec":{"f:replicas":{}}}`))
	if err := unstructured.SetNestedField(live.Object, int64(5), "spec", "replicas"); err != nil {
		t.Fatal(err)
	}
	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("the autoscaler's replica count was reported as drift: %+v", changes)
	}
}

func TestAnAppScaledToZeroIsNotDrift(t *testing.T) {
	spec := baseSpec()
	spec.ScaleToZero = true
	spec.Domains = []DomainSpec{{Hostname: "web.example.test", Path: "/"}}
	desired := prepared(t, BuildDeployment(spec))
	live := asLive(t, desired,
		panelOwns(imageField),
		managed("keda-operator", "Update", later, `{"f:spec":{"f:replicas":{}}}`))
	if err := unstructured.SetNestedField(live.Object, int64(0), "spec", "replicas"); err != nil {
		t.Fatal(err)
	}
	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("an app asleep was reported as drift: %+v", changes)
	}
}

// A restore stops the app by scaling it to zero, through the panel's own
// client. That is the panel, and the panel is never drift.
func TestThePanelsOwnUpdateIsNotDrift(t *testing.T) {
	desired := prepared(t, BuildDeployment(baseSpec()))
	live := asLive(t, desired,
		panelOwns(imageField),
		managed(FieldManager, "Update", later, `{"f:spec":{"f:replicas":{}}}`))
	if err := unstructured.SetNestedField(live.Object, int64(0), "spec", "replicas"); err != nil {
		t.Fatal(err)
	}
	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("the panel scaling its own app was reported as drift: %+v", changes)
	}
}

func TestAManualScaleIsDriftWhenNothingElseOwnsTheCount(t *testing.T) {
	desired := prepared(t, BuildDeployment(baseSpec()))
	live := asLive(t, desired,
		panelOwns(imageField),
		managed("kubectl", "Update", later, `{"f:spec":{"f:replicas":{}}}`))
	if err := unstructured.SetNestedField(live.Object, int64(4), "spec", "replicas"); err != nil {
		t.Fatal(err)
	}
	changes := CompareObject(desired, live)
	if len(changes) != 1 || changes[0].Path != "spec.replicas" || changes[0].Panel != "1" || changes[0].Live != "4" {
		t.Fatalf("got %+v", changes)
	}
	// And a caller that knows the count is somebody else's can say so.
	if changes := CompareObject(desired, live, "spec.replicas"); len(changes) != 0 {
		t.Fatalf("an ignored path was reported: %+v", changes)
	}
}

func TestASecretsValuesAreNeverShown(t *testing.T) {
	desired := prepared(t, BuildEnvSecret(baseSpec(), map[string]string{"API_KEY": "fake-value-one"}))
	live := asLive(t, desired, managed("kubectl-edit", "Update", later, `{"f:data":{"f:API_KEY":{}}}`))
	if err := unstructured.SetNestedField(live.Object, "ZmFrZS12YWx1ZS10d28=", "data", "API_KEY"); err != nil {
		t.Fatal(err)
	}
	changes := CompareObject(desired, live)
	if len(changes) != 1 || !changes[0].Hidden || changes[0].Panel != "" || changes[0].Live != "" {
		t.Fatalf("got %+v", changes)
	}
	if changes[0].Path != "data.API_KEY" {
		t.Errorf("the path is %q", changes[0].Path)
	}
}

func TestAVolumeSomebodyGrewIsLeftAlone(t *testing.T) {
	spec := baseSpec()
	spec.Volumes = []VolumeSpec{{Name: "data", MountPath: "/data", SizeGB: 1}}
	desired := prepared(t, BuildPVCs(spec)[0])
	live := asLive(t, desired,
		managed("kubectl-patch", "Update", later, `{"f:spec":{"f:resources":{"f:requests":{"f:storage":{}}}}}`))
	if err := unstructured.SetNestedField(live.Object, "5Gi", "spec", "resources", "requests", "storage"); err != nil {
		t.Fatal(err)
	}
	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("a grown volume was reported as something to undo: %+v", changes)
	}
}

// A network policy's rules are one value to Kubernetes, and the API server
// writes the protocol into a port nobody gave one.
func TestWhatTheServerAddsInsideAWholeListIsNotDrift(t *testing.T) {
	spec := baseSpec()
	spec.PublicPorts = []PublicPort{{Port: 25565, PublicPort: 25565, Protocol: "tcp"}}
	policy := BuildPortsPolicy(spec)
	if policy == nil {
		t.Fatal("no policy to compare")
	}
	desired := prepared(t, policy)
	live := asLive(t, desired, panelOwns(`{"f:spec":{"f:ingress":{}}}`))
	rules, _, _ := unstructured.NestedSlice(live.Object, "spec", "ingress")
	for _, rule := range rules {
		ports, _ := rule.(map[string]any)["ports"].([]any)
		for _, port := range ports {
			if _, ok := port.(map[string]any)["protocol"]; !ok {
				port.(map[string]any)["protocol"] = "TCP"
			}
		}
	}
	_ = unstructured.SetNestedSlice(live.Object, rules, "spec", "ingress")
	if changes := CompareObject(desired, live); len(changes) != 0 {
		t.Fatalf("a defaulted protocol was reported: %+v", changes)
	}
}

func TestADeletedObjectSaysSo(t *testing.T) {
	desired := prepared(t, BuildService(baseSpec()))
	got := DeletedObject(desired)
	if got.Kind != "Service" || got.Name != "web" || got.Change != DriftDeleted {
		t.Fatalf("got %+v", got)
	}
}

func TestTheFingerprintIgnoresItself(t *testing.T) {
	desired := prepared(t, BuildDeployment(baseSpec()))
	stamp := desired.GetAnnotations()[AppliedAnnotation]
	again, err := AppliedHash(desired)
	if err != nil || again != stamp || len(stamp) != 32 {
		t.Fatalf("stamp %q, again %q, %v", stamp, again, err)
	}
	// And it is on the object, never on the pod template, where it would
	// restart the app every time it changed.
	templateAnnotations, _, _ := unstructured.NestedStringMap(desired.Object, "spec", "template", "metadata", "annotations")
	if _, ok := templateAnnotations[AppliedAnnotation]; ok {
		t.Fatal("the fingerprint is on the pod template")
	}
}

func TestAPathReadsTheWayKubectlWritesIt(t *testing.T) {
	path := fieldPath{{field: "metadata"}, {field: "labels"}, {field: "app.kubernetes.io/name"}}
	if got := path.String(); got != `metadata.labels["app.kubernetes.io/name"]` {
		t.Errorf("got %s", got)
	}
	path = fieldPath{{field: "spec"}, {field: "ports"}, {key: map[string]any{"port": int64(80), "protocol": "TCP"}}, {field: "targetPort"}}
	if got := path.String(); got != "spec.ports[port=80,protocol=TCP].targetPort" {
		t.Errorf("got %s", got)
	}
	if long := display(strings.Repeat("x", 500)); len([]rune(long)) != 240 {
		t.Errorf("a long value is %d characters", len([]rune(long)))
	}
}
