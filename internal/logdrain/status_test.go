package logdrain

import (
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/kube"
)

// The collector's state is what Kubernetes says about it: running when every
// server's is ready, failing when one cannot start or the DaemonSet cannot
// make one, and why — with nothing secret-shaped in the reason.
func TestTheCollectorsStateIsWhatKubernetesSays(t *testing.T) {
	set := func(desired, ready int32) *appsv1.DaemonSet {
		return &appsv1.DaemonSet{Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: desired, NumberReady: ready}}
	}
	if got := Summarize(nil, nil, nil); got.State != CollectorAbsent || got.Problems == nil {
		t.Errorf("no DaemonSet reads as %+v", got)
	}
	if got := Summarize(set(3, 3), nil, nil); got.State != CollectorRunning {
		t.Errorf("every server ready reads as %s", got.State)
	}
	if got := Summarize(set(3, 1), nil, nil); got.State != CollectorStarting {
		t.Errorf("one of three ready reads as %s", got.State)
	}

	pulling := corev1.Pod{
		Spec: corev1.PodSpec{NodeName: "node-3"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "ImagePullBackOff", Message: "pull with Bearer abcdefghijklmnopqrstuvwxyz0123 refused",
			}},
		}}},
	}
	unscheduled := corev1.Pod{
		Spec: corev1.PodSpec{NodeName: ""},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/3 nodes are available",
			LastTransitionTime: metav1.NewTime(time.Now()),
		}}},
	}
	events := []kube.ObjectEvent{
		{Type: corev1.EventTypeWarning, Kind: "DaemonSet", Reason: "FailedCreate", Message: "forbidden: violates PodSecurity"},
		{Type: corev1.EventTypeNormal, Kind: "Pod", Reason: "Pulled", Message: "pulled"},
	}
	got := Summarize(set(3, 1), []corev1.Pod{pulling, unscheduled}, events)
	if got.State != CollectorFailing {
		t.Errorf("a collector that cannot pull reads as %s", got.State)
	}
	if len(got.Problems) != 3 {
		t.Fatalf("the problems are %+v", got.Problems)
	}
	reasons := map[string]CollectorProblem{}
	for _, problem := range got.Problems {
		reasons[problem.Reason] = problem
	}
	if reasons["ImagePullBackOff"].Server != "node-3" || reasons["Unschedulable"].Message == "" || reasons["FailedCreate"].Message == "" {
		t.Errorf("the problems are %+v", got.Problems)
	}
	if message := reasons["ImagePullBackOff"].Message; message == "" || strings.Contains(message, "abcdefghijklmnopqrstuvwxyz0123") {
		t.Errorf("a token in Kubernetes' message was passed on: %q", message)
	}
}
