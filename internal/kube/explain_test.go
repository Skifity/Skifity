package kube

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestExplanationsCarryTheFix(t *testing.T) {
	// The value is not that the panel repeats Kubernetes, it is that each
	// answer says what to do. A sentence with no fix is the one the panel is
	// replacing.
	cases := []struct {
		name, message, want string
		explain             func(string) string
	}{
		{"cpu", "0/3 nodes are available: 3 Insufficient cpu.", "add a server", ExplainUnschedulable},
		{"memory", "0/3 nodes are available: 3 Insufficient memory.", "free memory", ExplainUnschedulable},
		{"both", "0/2 nodes are available: 1 Insufficient cpu, 1 Insufficient memory.",
			"CPU and memory", ExplainUnschedulable},
		{"volume", "0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims.",
			"volume", ExplainUnschedulable},
		{"taint", "0/1 nodes are available: 1 node(s) had untolerated taint {a: b}.",
			"not accepting apps", ExplainUnschedulable},
		{"anti-affinity", "0/2 nodes are available: 2 node(s) didn't match pod anti-affinity rules.",
			"Add a server", ExplainUnschedulable},
		{"quota", `pods "web-abc" is forbidden: exceeded quota: environment, requested: pods=1`,
			"reached its limit", ExplainReplicaFailure},
		{"pull auth", "unauthorized: authentication required", "credentials", ExplainImagePull},
		{"pull missing", "manifest unknown", "deploy the commit again", ExplainImagePull},
		{"pull network", "dial tcp: lookup registry: no such host", "route to it", ExplainImagePull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.explain(tc.message)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("%q\n  gave %q\n  want it to mention %q", tc.message, got, tc.want)
			}
		})
	}
}

func TestAnUnrecognisedReasonKeepsKubernetesOwnWords(t *testing.T) {
	// Falling back to a guess is what this replaced. Kubernetes' own sentence
	// is the truth, and the truth beats a friendlier sentence that is wrong.
	message := "0/1 nodes are available: 1 node(s) were unschedulable for a reason nobody has written words for."
	for _, explain := range []func(string) string{ExplainUnschedulable, ExplainReplicaFailure, ExplainImagePull} {
		if got := explain(message); !strings.Contains(got, "nobody has written words for") {
			t.Errorf("the original message was dropped: %q", got)
		}
	}
	// And an empty message still says something rather than nothing.
	for _, explain := range []func(string) string{ExplainUnschedulable, ExplainReplicaFailure, ExplainImagePull} {
		if got := strings.TrimSpace(explain("")); got == "" {
			t.Error("an empty message produced an empty explanation")
		}
	}
}

func TestAQuotaFailureIsVisibleWithNoPodsAtAll(t *testing.T) {
	// The one failure that never reaches a pod: the ReplicaSet was refused, so
	// nothing was created and the instance list is empty. A person seeing
	// "0 of 3 ready" with no instances and no message has nowhere to go.
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(3)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "web"}},
		},
		Status: appsv1.DeploymentStatus{
			Replicas: 0, ReadyReplicas: 0,
			Conditions: []appsv1.DeploymentCondition{{
				Type:   appsv1.DeploymentReplicaFailure,
				Status: corev1.ConditionTrue,
				Message: `pods "web-7d4" is forbidden: exceeded quota: environment, ` +
					`requested: requests.memory=512Mi, used: requests.memory=16Gi, limited: requests.memory=16Gi`,
			}},
		},
	}

	c := &Client{clientset: fake.NewSimpleClientset(deployment), systemNamespace: "skifity-system"}
	status, err := c.AppStatus(t.Context(), "ns", "web")
	if err != nil {
		t.Fatalf("AppStatus: %v", err)
	}
	if len(status.Instances) != 0 {
		t.Fatalf("the test is not exercising the no-pods case: %d instances", len(status.Instances))
	}
	if !strings.Contains(status.Detail, "reached its limit") {
		t.Fatalf("the quota is not explained: %q", status.Detail)
	}
	// The raw message is kept too, because "which limit" is the next question.
	if !strings.Contains(status.Detail, "requests.memory") {
		t.Errorf("the detail does not say which limit was hit: %q", status.Detail)
	}
}
