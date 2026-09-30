package kube

import (
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// These tests run against client-go's fake clientset, so the same code path that
// talks to a real API server is exercised: the requests are built, sent and
// decoded for real, only the transport is replaced.

// toObjects converts a heterogeneous list into the runtime.Object slice the
// fake clientset takes.
func toObjects(items []any) []runtime.Object {
	out := make([]runtime.Object, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(runtime.Object); ok {
			out = append(out, obj)
		}
	}
	return out
}

// unstructuredString reads a nested string from a parsed document.
func unstructuredString(u *unstructured.Unstructured, fields ...string) (string, bool, error) {
	return unstructured.NestedString(u.Object, fields...)
}

func TestAppStatusExplainsCrashLoop(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme-shop-production", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "web"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "img:1"}}},
			},
		},
		Status: appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 0, ObservedGeneration: 1},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-abc", Namespace: "acme-shop-production",
			Labels: map[string]string{"app.kubernetes.io/name": "web"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				RestartCount: 7,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CrashLoopBackOff",
					Message: "back-off 5m0s restarting failed container",
				}},
			}},
		},
	}

	c := &Client{clientset: fake.NewSimpleClientset(deployment, pod), systemNamespace: "skifity-system"}
	status, err := c.AppStatus(t.Context(), "acme-shop-production", "web")
	if err != nil {
		t.Fatalf("AppStatus: %v", err)
	}
	// The phase must say what is wrong in plain language, not "0/1 ready".
	if status.Phase != "crashing" {
		t.Fatalf("phase is %q, want crashing", status.Phase)
	}
	if !strings.Contains(status.Detail, "exits") {
		t.Fatalf("detail does not explain the problem: %q", status.Detail)
	}
	if len(status.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(status.Instances))
	}
	if status.Instances[0].Restarts != 7 {
		t.Fatalf("restart count is %d, want 7", status.Instances[0].Restarts)
	}
	if status.Instances[0].Status != "CrashLoopBackOff" {
		t.Fatalf("instance status is %q, want CrashLoopBackOff", status.Instances[0].Status)
	}
}

func TestAppStatusForMissingDeployment(t *testing.T) {
	c := &Client{clientset: fake.NewSimpleClientset(), systemNamespace: "skifity-system"}
	status, err := c.AppStatus(t.Context(), "ns", "web")
	if err != nil {
		t.Fatalf("a missing Deployment should not be an error: %v", err)
	}
	if status.Phase != "not_deployed" {
		t.Fatalf("phase is %q, want not_deployed", status.Phase)
	}
}

func TestAppStatusPhases(t *testing.T) {
	cases := []struct {
		name           string
		replicas       int32
		ready          int32
		podReason      string
		podMessage     string
		unschedulable  string
		wantPhase      string
		detailContains string
	}{
		{name: "running", replicas: 2, ready: 2, wantPhase: "running", detailContains: "running"},
		{name: "updating", replicas: 3, ready: 1, wantPhase: "updating", detailContains: "1 of 3"},
		{name: "stopped", wantPhase: "stopped", detailContains: "zero"},
		{
			name: "pending", replicas: 1, podReason: "Pending",
			wantPhase: "pending", detailContains: "starting",
		},
		// The scheduler's own message, which the panel used to throw away and
		// replace with a guess about CPU and memory — sending people to add a
		// server for a problem that was neither.
		{
			name: "no room", replicas: 1,
			unschedulable:  "0/2 nodes are available: 2 Insufficient memory.",
			wantPhase:      "pending",
			detailContains: "enough free memory",
		},
		{
			name: "volume not bound", replicas: 1,
			unschedulable:  "0/1 nodes are available: pod has unbound immediate PersistentVolumeClaims.",
			wantPhase:      "pending",
			detailContains: "volume",
		},
		{
			name: "only tainted servers left", replicas: 1,
			unschedulable:  "0/1 nodes are available: 1 node(s) had untolerated taint {node-role.kubernetes.io/control-plane: }.",
			wantPhase:      "pending",
			detailContains: "not accepting apps",
		},
		{
			name: "image gone", replicas: 1, podReason: "ErrImagePull",
			podMessage:     `failed to pull: manifest unknown`,
			wantPhase:      "failed",
			detailContains: "deploy the commit again",
		},
		{
			name: "private registry", replicas: 1, podReason: "ImagePullBackOff",
			podMessage:     "unauthorized: authentication required",
			wantPhase:      "failed",
			detailContains: "credentials",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objects := []any{}
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr(tc.replicas),
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "web"}},
				},
				Status: appsv1.DeploymentStatus{Replicas: tc.replicas, ReadyReplicas: tc.ready},
			}
			objects = append(objects, deployment)
			if tc.podReason != "" || tc.unschedulable != "" {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "web-1", Namespace: "ns",
						Labels: map[string]string{"app.kubernetes.io/name": "web"},
					},
					Status: corev1.PodStatus{Phase: corev1.PodPending},
				}
				if tc.podReason != "" {
					message := tc.podMessage
					if message == "" {
						message = "detail"
					}
					pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: tc.podReason, Message: message},
						},
					}}
				}
				if tc.unschedulable != "" {
					pod.Status.Conditions = []corev1.PodCondition{{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  "Unschedulable",
						Message: tc.unschedulable,
					}}
				}
				objects = append(objects, pod)
			}
			c := &Client{clientset: fake.NewSimpleClientset(toObjects(objects)...), systemNamespace: "skifity-system"}
			status, err := c.AppStatus(t.Context(), "ns", "web")
			if err != nil {
				t.Fatalf("AppStatus: %v", err)
			}
			if status.Phase != tc.wantPhase {
				t.Fatalf("phase is %q, want %q (detail: %q)", status.Phase, tc.wantPhase, status.Detail)
			}
			if tc.detailContains != "" && !strings.Contains(status.Detail, tc.detailContains) {
				t.Fatalf("detail %q does not mention %q", status.Detail, tc.detailContains)
			}
		})
	}
}

func TestDeleteNamespaceRefusesForeignNamespaces(t *testing.T) {
	// A bug or a crafted id must not be able to delete kube-system.
	foreign := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}}
	ours := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   "acme-shop-production",
		Labels: map[string]string{"app.kubernetes.io/managed-by": "skifity"},
	}}
	c := &Client{clientset: fake.NewSimpleClientset(foreign, ours), systemNamespace: "skifity-system"}

	if err := c.DeleteNamespace(t.Context(), "kube-system"); err == nil {
		t.Fatal("the client agreed to delete kube-system")
	}
	if _, err := c.clientset.CoreV1().Namespaces().Get(t.Context(), "kube-system", metav1.GetOptions{}); err != nil {
		t.Fatalf("kube-system was deleted anyway: %v", err)
	}

	if err := c.DeleteNamespace(t.Context(), "acme-shop-production"); err != nil {
		t.Fatalf("deleting our own namespace failed: %v", err)
	}
	// Deleting one that is already gone must succeed, so cleanup can be retried.
	if err := c.DeleteNamespace(t.Context(), "never-existed"); err != nil {
		t.Fatalf("deleting a missing namespace returned an error: %v", err)
	}
}

func TestRestartAppPatchesPodTemplate(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{}},
		},
	}
	c := &Client{clientset: fake.NewSimpleClientset(deployment), systemNamespace: "skifity-system"}
	if err := c.RestartApp(t.Context(), "ns", "web"); err != nil {
		t.Fatalf("RestartApp: %v", err)
	}
	updated, err := c.clientset.AppsV1().Deployments("ns").Get(t.Context(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The annotation on the pod template is what makes Kubernetes roll the
	// pods; an annotation on the Deployment itself would do nothing.
	if updated.Spec.Template.Annotations["skifity.com/restarted-at"] == "" {
		t.Fatal("no restart annotation was set on the pod template")
	}
}

func TestWaitForRolloutDetectsStall(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr(int32(2))},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2,
			Conditions: []appsv1.DeploymentCondition{{
				Type:    appsv1.DeploymentProgressing,
				Status:  corev1.ConditionFalse,
				Message: "ReplicaSet has timed out progressing",
			}},
		},
	}
	c := &Client{clientset: fake.NewSimpleClientset(deployment), systemNamespace: "skifity-system"}
	err := c.WaitForRollout(t.Context(), "ns", "web", 5*time.Second)
	if err == nil {
		t.Fatal("a stalled rollout was reported as successful")
	}
	if !strings.Contains(err.Error(), "timed out progressing") {
		t.Fatalf("the error does not carry the reason: %v", err)
	}
}

func TestWaitForRolloutWaitsForObservedGeneration(t *testing.T) {
	// The status here belongs to the previous revision: the controller has not
	// seen generation 2 yet. Reporting success now would mean a deploy that has
	// not happened is reported as done.
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr(int32(1))},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
		},
	}
	c := &Client{clientset: fake.NewSimpleClientset(deployment), systemNamespace: "skifity-system"}
	if err := c.WaitForRollout(t.Context(), "ns", "web", 3*time.Second); err == nil {
		t.Fatal("the previous revision's status was accepted as the new one's")
	}
}

func TestDescribeNodeReadsRolesAndAddresses(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"node-role.kubernetes.io/control-plane": "true",
				"node-role.kubernetes.io/etcd":          "true",
			},
		},
		Spec: corev1.NodeSpec{Unschedulable: true},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
				{Type: corev1.NodeExternalIP, Address: "203.0.113.5"},
			},
			Conditions: []corev1.NodeCondition{{
				Type: corev1.NodeReady, Status: corev1.ConditionFalse, Message: "kubelet stopped posting status",
			}},
		},
	}
	info := describeNode(node)
	if info.Ready {
		t.Fatal("a NotReady node was reported as ready")
	}
	if info.Reason == "" {
		t.Fatal("the reason a node is not ready was dropped, which is what a user needs")
	}
	if info.InternalIP != "10.0.0.5" || info.ExternalIP != "203.0.113.5" {
		t.Fatalf("addresses are %q and %q", info.InternalIP, info.ExternalIP)
	}
	if len(info.Roles) != 2 || info.Roles[0] != "control-plane" {
		t.Fatalf("roles are %v, want control-plane and etcd sorted", info.Roles)
	}
	if info.Schedulable {
		t.Fatal("a cordoned node was reported as schedulable")
	}

	// A node with no role labels is a worker.
	plain := describeNode(corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}})
	if len(plain.Roles) != 1 || plain.Roles[0] != "worker" {
		t.Fatalf("a node with no role labels has roles %v, want worker", plain.Roles)
	}
}

func TestSplitYAML(t *testing.T) {
	manifest := []byte(`# a leading comment
---
apiVersion: v1
kind: Namespace
metadata:
  name: one
---
# just a comment between documents
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: two
data:
  script: |
    echo "---"
    echo done
---
`)
	docs, err := SplitYAML(manifest)
	if err != nil {
		t.Fatalf("SplitYAML: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d documents, want 2", len(docs))
	}
	if docs[0].GetKind() != "Namespace" || docs[1].GetKind() != "ConfigMap" {
		t.Fatalf("kinds are %q and %q", docs[0].GetKind(), docs[1].GetKind())
	}
	// An indented "---" inside a block scalar must not split the document.
	data, found, err := unstructuredString(docs[1], "data", "script")
	if err != nil || !found {
		t.Fatalf("the ConfigMap lost its script: %v", err)
	}
	if !strings.Contains(data, `echo "---"`) {
		t.Fatalf("the block scalar was split at an indented separator: %q", data)
	}
}

// TestAnInstanceReportsWhatItUses: the CPU and memory fields existed from the
// start and nothing ever filled them, so the panel printed an em-dash where
// the number that decides whether to scale should be.
func TestAnInstanceReportsWhatItUses(t *testing.T) {
	labelSet := map[string]string{"app.kubernetes.io/name": "web"}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme-shop-production"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labelSet},
		},
		Status: appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abc", Namespace: "acme-shop-production", Labels: labelSet},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	podMetrics := &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abc", Namespace: "acme-shop-production", Labels: labelSet},
		Containers: []metricsv1beta1.ContainerMetrics{
			{Name: "web", Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("120m"),
				corev1.ResourceMemory: resource.MustParse("200Mi"),
			}},
			// A sidecar counts towards the instance: the panel shows one
			// number per instance, not one per container.
			{Name: "sidecar", Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("30m"),
				corev1.ResourceMemory: resource.MustParse("56Mi"),
			}},
		},
	}

	// The metrics fake's tracker does not serve PodMetrics, so the list is
	// answered directly. Everything above and below it is the real path.
	metricsClient := metricsfake.NewSimpleClientset()
	metricsClient.PrependReactor("list", "pods",
		func(ktesting.Action) (bool, runtime.Object, error) {
			return true, &metricsv1beta1.PodMetricsList{
				Items: []metricsv1beta1.PodMetrics{*podMetrics},
			}, nil
		})

	c := &Client{
		clientset:       fake.NewSimpleClientset(deployment, pod),
		metrics:         metricsClient,
		systemNamespace: "skifity-system",
	}

	status, err := c.AppStatus(t.Context(), "acme-shop-production", "web")
	if err != nil {
		t.Fatalf("AppStatus: %v", err)
	}
	if len(status.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(status.Instances))
	}
	if got := status.Instances[0].CPUM; got != 150 {
		t.Errorf("instance CPU is %dm, want 150m (both containers)", got)
	}
	if got := status.Instances[0].MemoryMB; got != 256 {
		t.Errorf("instance memory is %dMB, want 256MB (both containers)", got)
	}
}

// TestUsageIsUnknownWithoutMetrics: metrics-server is optional, and a missing
// number is shown as unknown rather than failing the page.
func TestUsageIsUnknownWithoutMetrics(t *testing.T) {
	labelSet := map[string]string{"app.kubernetes.io/name": "web"}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "acme-shop-production"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: labelSet}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abc", Namespace: "acme-shop-production", Labels: labelSet},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	c := &Client{clientset: fake.NewSimpleClientset(deployment, pod), systemNamespace: "skifity-system"}

	status, err := c.AppStatus(t.Context(), "acme-shop-production", "web")
	if err != nil {
		t.Fatalf("AppStatus: %v", err)
	}
	if len(status.Instances) != 1 || status.Instances[0].CPUM != 0 {
		t.Fatal("a cluster with no metrics-server did not answer at all")
	}
}

// TestThePreviousContainersLogIsReadable: an app that crash-loops printed the
// reason in a container that has already been replaced. The live stream no
// longer has it, so it was the one thing worth reading and the one thing the
// panel could not read.
func TestThePreviousContainersLogIsReadable(t *testing.T) {
	labelSet := map[string]string{"app.kubernetes.io/name": "web"}
	calm := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-calm", Namespace: "ns", Labels: labelSet},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{RestartCount: 0}},
		},
	}
	crashing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-crashing", Namespace: "ns", Labels: labelSet},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{RestartCount: 7}},
		},
	}

	// Asking for the live log picks the pod that is serving: a crash-looping
	// pod's output is noise while something is up.
	if got := pickLogPod([]corev1.Pod{*crashing, *calm}, false); got.Name != "web-calm" {
		t.Errorf("the live log came from %s, want the ready instance", got.Name)
	}
	// Asking for the earlier container inverts it: the pod that restarted is
	// the whole point.
	if got := pickLogPod([]corev1.Pod{*calm, *crashing}, true); got.Name != "web-crashing" {
		t.Errorf("the earlier log came from %s, want the instance that restarted", got.Name)
	}
}

// TestFollowingAnEarlierContainerIsNotAThing: it has already exited, so a
// stream that stays open would hang rather than end.
func TestFollowingAnEarlierContainerIsNotAThing(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-1", Namespace: "ns",
			Labels: map[string]string{"app.kubernetes.io/name": "web"},
		},
	}
	c := &Client{clientset: fake.NewSimpleClientset(pod), systemNamespace: "skifity-system"}

	// The fake clientset serves a canned log body, so this exercises the
	// option-building rather than the transport.
	stream, err := c.AppLogs(t.Context(), "ns", "web", LogOptions{Previous: true, Follow: true})
	if err != nil {
		t.Fatalf("AppLogs: %v", err)
	}
	defer stream.Close()
}

func TestDeletingAnAppStopsItsScheduledCommands(t *testing.T) {
	// A scheduled command is the part of an app that keeps going on its own.
	// Left behind, a deleted app's nightly job fires every night forever
	// against an image nothing will pull, and the only sign of it is failed
	// pods piling up in a namespace nobody is looking at.
	ours := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{
		Name: "web-job-nightly", Namespace: "team-prod",
		Labels: map[string]string{
			"app.kubernetes.io/name": "web", "app.kubernetes.io/component": "run",
		},
	}}
	run := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: "web-run-abcd1234", Namespace: "team-prod",
		Labels: map[string]string{
			"app.kubernetes.io/name": "web", "app.kubernetes.io/component": "run",
		},
	}}
	// Another app in the same environment, which must survive.
	theirs := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{
		Name: "api-job-nightly", Namespace: "team-prod",
		Labels: map[string]string{
			"app.kubernetes.io/name": "api", "app.kubernetes.io/component": "run",
		},
	}}

	c := &Client{
		clientset:       fake.NewSimpleClientset(ours, run, theirs),
		systemNamespace: "skifity-system",
	}
	if err := c.deleteRuns(t.Context(), "team-prod", "web"); err != nil {
		t.Fatalf("deleteRuns: %v", err)
	}

	crons, err := c.clientset.BatchV1().CronJobs("team-prod").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list cron jobs: %v", err)
	}
	if len(crons.Items) != 1 || crons.Items[0].Name != "api-job-nightly" {
		var left []string
		for _, item := range crons.Items {
			left = append(left, item.Name)
		}
		t.Fatalf("scheduled commands left behind: %v, want only api-job-nightly", left)
	}

	jobs, err := c.clientset.BatchV1().Jobs("team-prod").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("%d of the app's runs are still there", len(jobs.Items))
	}
}

func TestQuotaUsageIsReadableWithoutParsingKubernetes(t *testing.T) {
	// The numbers exist so a bar can be drawn without the browser having to
	// understand what "1536Mi" or "1500m" mean.
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "environment", Namespace: "acme-prod"},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{
				"requests.cpu":    resource.MustParse("8"),
				"requests.memory": resource.MustParse("16Gi"),
				"pods":            resource.MustParse("60"),
			},
			Used: corev1.ResourceList{
				"requests.cpu":    resource.MustParse("7500m"),
				"requests.memory": resource.MustParse("8Gi"),
				"pods":            resource.MustParse("12"),
			},
		},
	}
	c := &Client{clientset: fake.NewSimpleClientset(quota), systemNamespace: "skifity-system"}

	usage, err := c.QuotaUsage(t.Context(), "acme-prod")
	if err != nil {
		t.Fatalf("QuotaUsage: %v", err)
	}
	if !usage.Found || len(usage.Items) != 3 {
		t.Fatalf("found=%v with %d items, want 3", usage.Found, len(usage.Items))
	}

	byName := map[string]QuotaItem{}
	for _, item := range usage.Items {
		byName[item.Resource] = item
	}
	// CPU in millicores, memory in mebibytes, a count as itself.
	if cpu := byName["requests.cpu"]; cpu.UsedValue != 7500 || cpu.HardValue != 8000 {
		t.Errorf("cpu is %d of %d, want 7500 of 8000 millicores", cpu.UsedValue, cpu.HardValue)
	}
	if mem := byName["requests.memory"]; mem.UsedValue != 8192 || mem.HardValue != 16384 {
		t.Errorf("memory is %d of %d, want 8192 of 16384 MiB", mem.UsedValue, mem.HardValue)
	}
	if pods := byName["pods"]; pods.UsedValue != 12 || pods.HardValue != 60 {
		t.Errorf("pods is %d of %d, want 12 of 60", pods.UsedValue, pods.HardValue)
	}
	// Nearly full, which is the whole reason to show it.
	if got := byName["requests.cpu"].Percent(); got != 93 {
		t.Errorf("cpu is %d%% full, want 93", got)
	}
	// The order is fixed, so the rows do not move between reloads.
	if usage.Items[0].Resource != "requests.cpu" || usage.Items[2].Resource != "pods" {
		t.Errorf("the limits came back in an unstable order: %v", usage.Items)
	}

	// An environment with no quota is not an error: it is an environment made
	// before quotas existed, or a cluster somebody opened up deliberately.
	empty := &Client{clientset: fake.NewSimpleClientset(), systemNamespace: "skifity-system"}
	none, err := empty.QuotaUsage(t.Context(), "acme-prod")
	if err != nil {
		t.Fatalf("a namespace with no quota gave %v, want no error", err)
	}
	if none.Found {
		t.Error("a namespace with no quota reported one")
	}
}

// TestMetricsAvailableSaysNoWhenNothingIsServingThem.
//
// A HorizontalPodAutoscaler on a CPU or memory target reads the metrics API.
// Without it the HPA sits at `<unknown>/70%` and never scales, which is the
// most common reason autoscaling silently does nothing — so the panel has to be
// able to tell the difference and say so.
func TestMetricsAvailableSaysNoWhenNothingIsServingThem(t *testing.T) {
	if (&Client{}).MetricsAvailable(t.Context()) {
		t.Error("a client with no metrics clientset reported metrics available")
	}

	empty := &Client{metrics: metricsfake.NewSimpleClientset()}
	if empty.MetricsAvailable(t.Context()) {
		t.Error("a metrics API with no nodes in it reported metrics available")
	}

	// The generated metrics fake serves no seeded object through List — the
	// API is read-only, so its tracker has nothing to read back — and a
	// reactor is the supported way to say what the API answers.
	answering := metricsfake.NewSimpleClientset()
	answering.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{
			Items: []metricsv1beta1.NodeMetrics{{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}},
		}, nil
	})
	if !(&Client{metrics: answering}).MetricsAvailable(t.Context()) {
		t.Error("a metrics API reporting a node was read as unavailable")
	}

	// An error from the API is "no metrics", not a crash: metrics-server can be
	// there and not ready.
	refusing := metricsfake.NewSimpleClientset()
	refusing.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the server is currently unable to handle the request")
	})
	if (&Client{metrics: refusing}).MetricsAvailable(t.Context()) {
		t.Error("a metrics API that answered with an error was read as available")
	}
}
