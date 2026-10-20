package provision

import (
	"io"
	"log/slog"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// An app is a Deployment, its ReplicaSet and its pods, each pod on a node and
// ready or not.
func appObjects(name string, strategy appsv1.DeploymentStrategyType, pods map[string]bool) []runtime.Object {
	controller := true
	labels := map[string]string{"app": name}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "acme-shop-production"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{Type: strategy},
		},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: name + "-abc", Namespace: "acme-shop-production",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: name, Controller: &controller}},
	}}
	objects := []runtime.Object{deployment, replicaSet}
	for node, ready := range pods {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		objects = append(objects, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name + "-abc-" + node, Namespace: "acme-shop-production", Labels: labels,
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: name + "-abc", Controller: &controller}},
			},
			Spec:   corev1.PodSpec{NodeName: node},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}},
		})
	}
	return objects
}

// An app whose only ready instance is on the server being emptied is started
// elsewhere before that instance is evicted; one with a ready instance on
// another server, and one that cannot run two instances at once, are left to
// the eviction.
func TestAnAppWithNoReadyInstanceElsewhereIsStartedElsewhereFirst(t *testing.T) {
	var objects []runtime.Object
	objects = append(objects, appObjects("hello", appsv1.RollingUpdateDeploymentStrategyType, map[string]bool{"second": true})...)
	objects = append(objects, appObjects("api", appsv1.RollingUpdateDeploymentStrategyType, map[string]bool{"second": true, "first": true})...)
	objects = append(objects, appObjects("files", appsv1.RecreateDeploymentStrategyType, map[string]bool{"second": true})...)
	clientset := fake.NewSimpleClientset(objects...)

	pods, err := clientset.CoreV1().Pods("").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var onSecond []corev1.Pod
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "second" {
			onSecond = append(onSecond, pod)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if moved := moveSoleInstances(t.Context(), clientset, log, onSecond, "second", 0); moved != 1 {
		t.Errorf("moved %d instances, want hello's one", moved)
	}
	for name, want := range map[string]bool{"hello": true, "api": false, "files": false} {
		deployment, err := clientset.AppsV1().Deployments("acme-shop-production").Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, rolled := deployment.Spec.Template.Annotations[movedAnnotation]
		if rolled != want {
			t.Errorf("%s rolled out: %v, want %v", name, rolled, want)
		}
	}
}

// A disruption budget that says "not yet" is asked again, and the eviction
// goes through once it allows it; one that never does is an error by the
// deadline.
func TestAnEvictionTheBudgetDefersIsAskedAgain(t *testing.T) {
	evictRetry = time.Millisecond
	t.Cleanup(func() { evictRetry = 5 * time.Second })

	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hello-abc-second", Namespace: "acme-shop-production"}}
	clientset := fake.NewSimpleClientset()
	refusals := 2
	clientset.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		if refusals > 0 {
			refusals--
			return true, nil, apierrors.NewTooManyRequests("Cannot evict pod as it would violate the pod's disruption budget.", 0)
		}
		return true, nil, nil
	})
	if err := evict(t.Context(), clientset, pod, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("an eviction the budget allowed on the third ask failed: %v", err)
	}
	if refusals != 0 {
		t.Fatalf("the eviction was not asked again (%d refusals left)", refusals)
	}

	refusals = 1000
	if err := evict(t.Context(), clientset, pod, time.Now()); err == nil || !apierrors.IsTooManyRequests(err) {
		t.Fatalf("a budget that never allows it was not an error by the deadline: %v", err)
	}
}
