package cluster

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/crypto"
	"skifity/internal/kube"
	"skifity/internal/logdrain"
	"skifity/internal/store"
)

// A made-up token, shaped so the test can find it in what it should and
// should not be in.
const fakeAxiomToken = "xaat-not-a-real-token-for-a-test-0001"

type collectorCalls struct {
	mu      sync.Mutex
	applied []*unstructured.Unstructured
	deleted []string
}

func (c *collectorCalls) react(action k8stesting.Action) (bool, runtime.Object, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch typed := action.(type) {
	case k8stesting.PatchAction:
		if typed.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(typed.GetPatch()); err != nil {
			return true, nil, err
		}
		c.applied = append(c.applied, obj)
		return true, obj, nil
	case k8stesting.DeleteAction:
		c.deleted = append(c.deleted, typed.GetResource().Resource+"/"+typed.GetName())
		return true, nil, nil
	}
	return false, nil, nil
}

func collectorCluster(t *testing.T, objects ...runtime.Object) (*Cluster, *store.DB, *crypto.Keyring, *collectorCalls) {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	calls := &collectorCalls{}
	dynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamic.PrependReactor("patch", "*", calls.react)
	dynamic.PrependReactor("delete", "*", calls.react)
	client := kube.NewClientWith(fake.NewSimpleClientset(objects...), dynamic, "")
	return New(client, db, keyring, slog.New(slog.NewTextHandler(io.Discard, nil))), db, keyring, calls
}

func seedDrainTeam(t *testing.T, db *store.DB, name string) (store.Team, store.Project) {
	t.Helper()
	ctx := t.Context()
	team := store.Team{Name: name, Slug: strings.ToLower(name)}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := store.Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	return team, project
}

func saveDrain(t *testing.T, db *store.DB, keyring *crypto.Keyring, team store.Team, enabled bool) store.LogDrainRow {
	t.Helper()
	id := store.NewID("ldr")
	row := store.LogDrainRow{LogDrain: store.LogDrain{
		ID: id, TeamID: team.ID, Name: "Axiom " + id, Kind: logdrain.KindAxiom,
		Settings: map[string]string{"dataset": "apps"}, Enabled: enabled,
	}}
	var err error
	row.SealedSecrets, err = logdrain.Seal(keyring, logdrain.Drain{ID: row.ID, TeamID: team.ID, Kind: row.Kind,
		Settings: row.Settings, Secrets: map[string]string{"token": fakeAxiomToken}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateLogDrain(t.Context(), &row); err != nil {
		t.Fatal(err)
	}
	return row
}

// A drain brings the collector up with its credentials in the Secret; the
// last one going takes it all away again, and the credentials with it.
func TestTheCollectorComesAndGoesWithTheDrains(t *testing.T) {
	c, db, keyring, calls := collectorCluster(t)
	ctx := t.Context()
	team, _ := seedDrainTeam(t, db, "Acme")
	row := saveDrain(t, db, keyring, team, true)

	if err := c.RefreshLogDrains(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	kinds := []string{}
	var secret, daemonSet *unstructured.Unstructured
	for _, obj := range calls.applied {
		kinds = append(kinds, obj.GetKind())
		switch obj.GetKind() {
		case "Secret":
			secret = obj
		case "DaemonSet":
			daemonSet = obj
		}
	}
	if !slices.Equal(kinds, []string{"Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "NetworkPolicy", "Secret", "DaemonSet"}) {
		t.Fatalf("the collector was applied as %v", kinds)
	}
	encodedSecret, _ := secret.MarshalJSON()
	var typed corev1.Secret
	if err := json.Unmarshal(encodedSecret, &typed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typed.Data[logdrain.ConfigFile]), fakeAxiomToken) {
		t.Error("the collector's Secret does not carry the drain's token")
	}
	encodedSet, _ := daemonSet.MarshalJSON()
	if strings.Contains(string(encodedSet), fakeAxiomToken) {
		t.Error("the DaemonSet carries the drain's token")
	}
	component, _ := db.GetComponent(ctx, logdrain.Component)
	if component.Status != "installed" || component.Version != logdrain.VectorVersion {
		t.Errorf("the collector is recorded as %+v", component)
	}

	// Nothing changed: the periodic refresh applies nothing.
	calls.applied = nil
	c.MaintainLogDrains(ctx)
	if len(calls.applied) != 0 {
		t.Errorf("an unchanged configuration was applied again: %d objects", len(calls.applied))
	}

	// The last drain goes, and the collector with it.
	if err := db.DeleteLogDrain(ctx, team.ID, row.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshLogDrains(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	for _, want := range []string{"daemonsets/" + logdrain.CollectorName, "secrets/" + logdrain.CollectorName,
		"clusterroles/" + logdrain.CollectorName, "clusterrolebindings/" + logdrain.CollectorName} {
		if !slices.Contains(calls.deleted, want) {
			t.Errorf("%s was left behind; deleted were %v", want, calls.deleted)
		}
	}
	if calls.deleted[0] != "daemonsets/"+logdrain.CollectorName {
		t.Errorf("the collector was not the first thing taken away: %v", calls.deleted)
	}
	component, _ = db.GetComponent(ctx, logdrain.Component)
	if component.Status != "removed" {
		t.Errorf("after the last drain went the collector is %s", component.Status)
	}

	// With nothing to send and nothing there, the periodic refresh asks the
	// cluster for nothing.
	calls.deleted = nil
	c.MaintainLogDrains(ctx)
	if len(calls.deleted) != 0 {
		t.Errorf("the periodic refresh deleted %v with nothing there", calls.deleted)
	}
}

// A paused drain is not the collector's, and one whose credentials do not
// open is left out without taking any other team's drain with it.
func TestOneBrokenDrainDoesNotStopTheOthers(t *testing.T) {
	c, db, keyring, calls := collectorCluster(t)
	ctx := t.Context()
	acme, _ := seedDrainTeam(t, db, "Acme")
	globex, _ := seedDrainTeam(t, db, "Globex")
	broken := saveDrain(t, db, keyring, acme, true)
	saveDrain(t, db, keyring, globex, true)
	saveDrain(t, db, keyring, globex, false)

	// The broken one's address was changed by hand: its seal names the old
	// one, so it does not open.
	if _, err := db.Exec(ctx, `UPDATE log_drains SET settings = '{"dataset":"elsewhere"}' WHERE id = ?`, broken.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshLogDrains(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	var config string
	for _, obj := range calls.applied {
		if obj.GetKind() == "Secret" {
			data, _, _ := unstructured.NestedString(obj.Object, "data", logdrain.ConfigFile)
			config = data
		}
	}
	if config == "" {
		t.Fatal("no configuration was applied")
	}
	decoded := decodeBase64(t, config)
	if strings.Contains(decoded, "drain_"+broken.ID) {
		t.Error("a drain whose credentials do not open was given to the collector")
	}
	if strings.Count(decoded, "type: axiom") != 1 {
		t.Errorf("the collector has %d Axiom sinks; one team's working drain, not the paused one", strings.Count(decoded, "type: axiom"))
	}
}

// The collector's state is read from the DaemonSet, its pods and its events:
// a collector refusing its configuration says so.
func TestTheCollectorsStateIsReadFromKubernetes(t *testing.T) {
	set := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: logdrain.CollectorName, Namespace: logdrain.Namespace,
			Annotations: map[string]string{"skifity.com/collector-version": logdrain.VectorVersion}},
		Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 2, NumberReady: 1},
	}
	crashing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "collector-a", Namespace: logdrain.Namespace,
			Labels: map[string]string{"app.kubernetes.io/name": logdrain.CollectorName}},
		Spec: corev1.PodSpec{NodeName: "node-2"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 78}},
		}}},
	}
	c, _, _, _ := collectorCluster(t, set, crashing)
	status, err := c.LogCollectorStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != logdrain.CollectorFailing || status.Desired != 2 || status.Ready != 1 || status.Version != logdrain.VectorVersion {
		t.Errorf("the collector reads as %+v", status)
	}
	if len(status.Problems) != 1 || status.Problems[0].Reason != logdrain.ReasonConfigurationRefused || status.Problems[0].Server != "node-2" {
		t.Errorf("the problems are %+v", status.Problems)
	}

	empty, _, _, _ := collectorCluster(t)
	if status, err := empty.LogCollectorStatus(t.Context()); err != nil || status.State != logdrain.CollectorAbsent {
		t.Errorf("no collector reads as %+v (%v)", status, err)
	}
}

func decodeBase64(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("the Secret's data is not base64: %v", err)
	}
	return string(raw)
}
