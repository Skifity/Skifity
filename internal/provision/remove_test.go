package provision

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/cluster"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// A server that has stopped answering is taken out without waiting for its
// instances to stop, which they never would, and what kept its data on it is
// named.
//
// Its instances were evicted and waited for, as on a server that answers;
// each stayed "terminating" for as long as the node was listed, and after
// five minutes the removal stopped with "some instances would not move". A
// server that was gone for good could not be removed from the panel at all.
func TestAServerThatStoppedAnsweringIsTakenOutWithoutWaiting(t *testing.T) {
	controller := true
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "second", Labels: map[string]string{corev1.LabelHostname: "second"}},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionUnknown, Reason: "NodeStatusUnknown",
		}}},
	}
	app := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hello-abc-1", Namespace: "acme-shop-production",
			Labels:          map[string]string{"app.kubernetes.io/name": "hello"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "hello-abc", Controller: &controller}},
		},
		Spec: corev1.PodSpec{NodeName: "second"},
	}
	// A database whose claim is bound to a volume on the server's own disk,
	// as every claim is with the storage k3s ships.
	database := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "orders-1", Namespace: "acme-shop-production",
			Labels: map[string]string{"cnpg.io/cluster": "orders", "app.kubernetes.io/name": "postgresql"},
		},
		Spec: corev1.PodSpec{
			NodeName: "second",
			Volumes: []corev1.Volume{{Name: "pgdata", VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "orders-1"},
			}}},
		},
	}
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-1", Namespace: "acme-shop-production"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: "pvc-orders-1"},
	}
	volume := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-orders-1"},
		Spec: corev1.PersistentVolumeSpec{NodeAffinity: &corev1.VolumeNodeAffinity{
			Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: corev1.LabelHostname, Operator: corev1.NodeSelectorOpIn, Values: []string{"second"},
				}},
			}}},
		}},
	}
	// The ingress runs on every server, and is no instance of anybody's.
	ingress := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "traefik-xyz", Namespace: "kube-system",
			OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "traefik", Controller: &controller}},
		},
		Spec: corev1.PodSpec{NodeName: "second"},
	}
	// A build that finished there, which nothing starts again.
	build := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "build-shop-1", Namespace: "skifity-builds"},
		Spec:       corev1.PodSpec{NodeName: "second"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	clientset := fake.NewSimpleClientset(node, app, database, claim, volume, ingress, build)
	p := &Provisioner{
		cluster: cluster.New(kube.NewClientWith(clientset, nil, ""), nil, nil, slog.New(slog.DiscardHandler)),
		log:     slog.New(slog.DiscardHandler),
	}

	started := time.Now()
	result, err := p.drain(t.Context(), store.Server{ID: "srv_2", Name: "second", NodeName: "second"})
	if err != nil {
		t.Fatalf("taking out a server that is not answering: %v", err)
	}
	if waited := time.Since(started); waited > 10*time.Second {
		t.Errorf("waited %s for instances on a server that is not answering", waited)
	}
	if !result.unreachable || result.moved != 1 || !slices.Equal(result.stranded, []string{"orders"}) {
		t.Errorf("got %+v, want the app's one instance started elsewhere and orders named", result)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "create" && action.GetSubresource() == "eviction" ||
			action.GetVerb() == "patch" || action.GetVerb() == "delete" {
			t.Errorf("asked a server that is not answering to %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}

	note := result.note()
	if note.Key != "drainedUnreachableStranded" || !slices.Equal(note.Args, []string{"1", "orders"}) {
		t.Errorf("the step says %q with %v", note.Key, note.Args)
	}
}

// Only a node that has stopped reporting is one nothing can be asked of. One
// that reports itself not ready is answering, and is emptied as any other.
func TestOnlyANodeThatStoppedReportingIsUnreachable(t *testing.T) {
	for status, want := range map[corev1.ConditionStatus]bool{
		corev1.ConditionUnknown: true,
		corev1.ConditionFalse:   false,
		corev1.ConditionTrue:    false,
	} {
		node := &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionUnknown},
			{Type: corev1.NodeReady, Status: status},
		}}}
		if got := unreachable(node); got != want {
			t.Errorf("Ready %s: unreachable is %v, want %v", status, got, want)
		}
	}
	if unreachable(&corev1.Node{}) {
		t.Error("a node with no conditions yet is not one that stopped reporting")
	}

	answering := drainResult{moved: 3}.note()
	if answering.Key != "drained" || !slices.Equal(answering.Args, []string{"3"}) {
		t.Errorf("a server that answered says %q with %v", answering.Key, answering.Args)
	}
	gone := drainResult{moved: 2, unreachable: true}.note()
	if gone.Key != "drainedUnreachable" || !slices.Equal(gone.Args, []string{"2"}) {
		t.Errorf("a server that did not answer says %q with %v", gone.Key, gone.Args)
	}
}
