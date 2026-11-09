package kube

import (
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"skifity/internal/version"
)

// A rollout of deployment dep_2, under way: the controller has seen it, and
// none of its instances is ready.
func rollingOut(annotations map[string]string) *appsv1.Deployment {
	selector := map[string]string{"app.kubernetes.io/name": "web"}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns", Generation: 2, Annotations: annotations},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{version.LabelKey("deployment-id"): "dep_2"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "img:2"}}},
			},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, ReadyReplicas: 1},
	}
}

// instance is one of the app's pods, of the deployment named, with the
// container status given.
func instance(name, deploymentID string, status corev1.ContainerStatus) *corev1.Pod {
	status.Name = "web"
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "ns",
			Labels:      map[string]string{"app.kubernetes.io/name": "web"},
			Annotations: map[string]string{version.LabelKey("deployment-id"): deploymentID},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{status}},
	}
}

func exited(code int32, reason string) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Reason: reason}}
}

func running() corev1.ContainerState {
	return corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
}

func backingOff() corev1.ContainerState {
	return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
}

// The first thing this exists for: a version that exits as it starts ends the
// wait on the next look, not ten minutes later.
func TestANewVersionThatKeepsStoppingEndsTheWaitAtOnce(t *testing.T) {
	pod := instance("web-2-a", "dep_2", corev1.ContainerStatus{
		RestartCount: 2, State: running(), LastTerminationState: exited(1, "Error"),
	})
	c := &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), pod)}

	started := time.Now()
	err := c.WaitForRollout(t.Context(), "ns", "web", time.Minute)
	var crash *CrashError
	if !errors.As(err, &crash) {
		t.Fatalf("the rollout ended with %v, want a CrashError", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Errorf("it took %s to give up on a version that keeps stopping", time.Since(started))
	}
	if crash.Pod != "web-2-a" || crash.Container != "web" || crash.Stops != 2 || crash.ExitCode != 1 || crash.Kind() != StopExited {
		t.Errorf("the crash is %+v", crash)
	}
}

// Waiting does not give an instance more memory, so one kill is enough.
func TestOneOutOfMemoryKillIsEnough(t *testing.T) {
	pod := instance("web-2-a", "dep_2", corev1.ContainerStatus{
		RestartCount: 1, State: running(), LastTerminationState: exited(137, "OOMKilled"),
	})
	c := &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), pod)}

	var crash *CrashError
	if err := c.WaitForRollout(t.Context(), "ns", "web", time.Minute); !errors.As(err, &crash) {
		t.Fatalf("the rollout ended with %v, want a CrashError", err)
	}
	if crash.Kind() != StopOutOfMemory || crash.Stops != 1 {
		t.Errorf("the crash is %+v, kind %s", crash, crash.Kind())
	}
	if !strings.Contains(crash.Error(), "ran out of memory") {
		t.Errorf("the error says %q", crash.Error())
	}

	// And one stop that is not a crash loop yet is waited on.
	once := instance("web-2-a", "dep_2", corev1.ContainerStatus{
		RestartCount: 1, State: running(), LastTerminationState: exited(1, "Error"),
	})
	c = &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), once)}
	if err := c.WaitForRollout(t.Context(), "ns", "web", time.Nanosecond); errors.As(err, &crash) {
		t.Errorf("one stop was taken for a crash loop: %v", err)
	}
}

// The version being replaced may be crash-looping too — it is often why there
// is a new one — and that is not the new version's doing.
func TestThePreviousVersionsCrashesAreNotTheNewOnes(t *testing.T) {
	old := instance("web-1-a", "dep_1", corev1.ContainerStatus{
		RestartCount: 9, State: backingOff(), LastTerminationState: exited(1, "Error"),
	})
	c := &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), old)}

	err := c.WaitForRollout(t.Context(), "ns", "web", time.Nanosecond)
	var crash *CrashError
	var timeout *RolloutTimeout
	if errors.As(err, &crash) || !errors.As(err, &timeout) {
		t.Fatalf("the rollout ended with %v, want a timeout", err)
	}
}

// Raising the memory limit is a new revision under the same deployment: the
// instances still being killed for memory are the old revision's, and the
// ReplicaSet is what tells them apart where the deployment id cannot.
func TestRaisingTheMemoryIsNotFailedByTheInstancesItFixes(t *testing.T) {
	deployment := rollingOut(map[string]string{revisionAnnotation: "3"})
	deployment.UID = "uid-web"
	set := func(name, hash, revision string) *appsv1.ReplicaSet {
		return &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "ns",
			Labels:      map[string]string{"app.kubernetes.io/name": "web", appsv1.DefaultDeploymentUniqueLabelKey: hash},
			Annotations: map[string]string{revisionAnnotation: revision},
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "Deployment", Name: "web", UID: "uid-web",
			}},
		}}
	}
	old := instance("web-old-a", "dep_2", corev1.ContainerStatus{
		RestartCount: 4, State: backingOff(), LastTerminationState: exited(137, "OOMKilled"),
	})
	old.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "old"
	fresh := instance("web-new-a", "dep_2", corev1.ContainerStatus{State: running()})
	fresh.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "new"

	c := &Client{clientset: fake.NewSimpleClientset(deployment, set("web-old", "old", "2"), set("web-new", "new", "3"), old, fresh)}
	err := c.WaitForRollout(t.Context(), "ns", "web", time.Nanosecond)
	var crash *CrashError
	if errors.As(err, &crash) {
		t.Fatalf("the old revision's out-of-memory kills failed the rollout that raises its memory: %v", err)
	}
	var timeout *RolloutTimeout
	if !errors.As(err, &timeout) || timeout.Instance != "web-new-a" {
		t.Fatalf("the timeout names %v, want the new instance", err)
	}
}

// A slow starter is not a crasher. The kubelet stops a container that did not
// answer its health check within its time to start and restarts it like one
// that crashed; calling that a crash sends somebody to a log that says nothing
// is wrong.
func TestAContainerStoppedByItsStartupProbeIsNotACrash(t *testing.T) {
	pod := instance("web-2-a", "dep_2", corev1.ContainerStatus{
		RestartCount: 3, State: backingOff(), LastTerminationState: exited(143, "Error"),
	})
	killing := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "web-2-a.1", Namespace: "ns"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-2-a", FieldPath: "spec.containers{web}"},
		Reason:         "Killing",
		Message:        "Container web failed startup probe, will be restarted",
	}
	c := &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), pod, killing)}
	err := c.WaitForRollout(t.Context(), "ns", "web", time.Nanosecond)
	var crash *CrashError
	if errors.As(err, &crash) {
		t.Fatalf("a container its startup probe stopped was called a crash: %v", err)
	}

	// Without the event, which lasts an hour: it ran for the whole of its
	// time to start before it stopped.
	slow := instance("web-2-a", "dep_2", corev1.ContainerStatus{
		RestartCount: 2, State: running(),
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 0, Reason: "Completed",
			StartedAt:  metav1.NewTime(time.Unix(1000, 0)),
			FinishedAt: metav1.NewTime(time.Unix(1120, 0)),
		}},
	})
	slow.Spec.Containers[0].StartupProbe = &corev1.Probe{PeriodSeconds: 5, FailureThreshold: 24}
	c = &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), slow)}
	if err := c.WaitForRollout(t.Context(), "ns", "web", time.Nanosecond); errors.As(err, &crash) {
		t.Fatalf("a container that ran its whole time to start was called a crash: %v", err)
	}

	// The same exit after two seconds is a crash.
	quick := slow.DeepCopy()
	quick.Status.ContainerStatuses[0].LastTerminationState.Terminated.FinishedAt = metav1.NewTime(time.Unix(1002, 0))
	c = &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), quick)}
	if err := c.WaitForRollout(t.Context(), "ns", "web", time.Minute); !errors.As(err, &crash) || crash.Kind() != StopExitedCleanly {
		t.Fatalf("a web process that ended by itself was not called one: %v", err)
	}
}

// A rollout that runs out of time says which new instance it was waiting for
// and why, not how the old version is doing.
func TestATimeoutNamesTheInstanceItWasWaitingFor(t *testing.T) {
	ready := instance("web-2-a", "dep_2", corev1.ContainerStatus{State: running(), Ready: true})
	ready.CreationTimestamp = metav1.NewTime(time.Unix(1000, 0))
	ready.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	waiting := instance("web-2-b", "dep_2", corev1.ContainerStatus{State: running()})
	waiting.CreationTimestamp = metav1.NewTime(time.Unix(1060, 0))
	unhealthy := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "web-2-b.1", Namespace: "ns"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-2-b"},
		Reason:         "Unhealthy",
		Message:        "Readiness probe failed: HTTP probe failed with statuscode: 404",
	}
	c := &Client{clientset: fake.NewSimpleClientset(rollingOut(nil), ready, waiting, unhealthy)}

	err := c.WaitForRollout(t.Context(), "ns", "web", time.Nanosecond)
	var timeout *RolloutTimeout
	if !errors.As(err, &timeout) {
		t.Fatalf("the rollout ended with %v, want a timeout", err)
	}
	if timeout.Instance != "web-2-b" || !strings.Contains(timeout.Reason(), "statuscode: 404") {
		t.Errorf("the reason is %q", timeout.Reason())
	}
	if timeout.Ready != 1 || timeout.Desired != 1 {
		t.Errorf("%d of %d new instances are counted ready, want 1 of 1", timeout.Ready, timeout.Desired)
	}
	if strings.Contains(err.Error(), "running") {
		t.Errorf("the reason describes what is running: %v", err)
	}
}

func TestAnInstanceSaysHowItLastStopped(t *testing.T) {
	deployment := rollingOut(nil)
	deployment.Status = appsv1.DeploymentStatus{Replicas: 1, ObservedGeneration: 2}
	for _, c := range []struct {
		last     corev1.ContainerState
		reason   string
		code     int32
		contains string
	}{
		{exited(137, "OOMKilled"), "OOMKilled", 137, "out of memory"},
		{exited(127, "Error"), "Error", 127, "exit code 127"},
		{exited(0, "Completed"), "Completed", 0, "keep running"},
		{exited(3, "Error"), "Error", 3, "code 3"},
	} {
		pod := instance("web-2-a", "dep_2", corev1.ContainerStatus{
			RestartCount: 4, State: backingOff(), LastTerminationState: c.last,
		})
		inst := describePod(*pod)
		if inst.LastReason != c.reason || inst.LastExitCode != c.code {
			t.Errorf("the instance says it last stopped with %s %d, want %s %d",
				inst.LastReason, inst.LastExitCode, c.reason, c.code)
		}
		phase, detail := summarisePhase(deployment, AppStatus{Instances: []Instance{inst}})
		if phase != "crashing" || !strings.Contains(detail, c.contains) {
			t.Errorf("%s %d: %s, %q does not say %q", c.reason, c.code, phase, detail, c.contains)
		}
	}

	// One that has never stopped says nothing about stopping.
	if inst := describePod(*instance("web-2-a", "dep_2", corev1.ContainerStatus{State: running()})); inst.LastReason != "" {
		t.Errorf("an instance that never stopped says it stopped with %q", inst.LastReason)
	}
}

// The lines are the crashed container's, from before its restart.
func TestPreviousLogTailReadsTheContainerThatStopped(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	var asked *corev1.PodLogOptions
	clientset.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		generic, ok := action.(ktesting.GenericAction)
		if !ok || action.GetSubresource() != "log" {
			return false, nil, nil
		}
		asked, _ = generic.GetValue().(*corev1.PodLogOptions)
		return true, &runtime.Unknown{Raw: []byte("listening\nError: DATABASE_URL is not set\n")}, nil
	})
	c := &Client{clientset: clientset}

	tail, err := c.PreviousLogTail(t.Context(), "ns", "web-2-a", "web", 40)
	if err != nil {
		t.Fatal(err)
	}
	if tail != "listening\nError: DATABASE_URL is not set" {
		t.Errorf("the tail is %q", tail)
	}
	if asked == nil || !asked.Previous || asked.Container != "web" || asked.TailLines == nil || *asked.TailLines != 40 {
		t.Errorf("the log was asked for with %+v", asked)
	}
}
