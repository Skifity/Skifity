package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	"skifity/internal/version"
)

// Telling a version that crashes from one that is slow.
//
// A new version that stopped as soon as it started used to be waited on for
// the whole rollout window, ten minutes and more, and was then reported as a
// version that "did not start", with a list of the usual causes and the old
// version's "1 instance(s) running" as the reason. The kubelet knew within
// seconds: the container's last termination says it ran out of memory or
// exited with 127, and its log from before the restart says why.

// CrashError is a rollout that cannot finish because the new version's
// instances stop as they start.
type CrashError struct {
	// Pod and Container name the instance and the container that stopped.
	Pod       string
	Container string
	// Stops is how many times it has stopped so far.
	Stops int
	// ExitCode, Reason and Message are its last termination as the kubelet
	// recorded it: 137, OOMKilled, and whatever the container wrote to its
	// termination message.
	ExitCode int32
	Reason   string
	Message  string
}

func (e *CrashError) Error() string {
	return fmt.Sprintf("%s stopped %s as it started: %s", e.Pod, Times(e.Stops), DescribeStop(e.Reason, e.ExitCode))
}

// Kind is what the fix for this crash depends on; see StopKind.
func (e *CrashError) Kind() string { return StopKind(e.Reason, e.ExitCode) }

// The ways a container stops that call for different fixes.
const (
	// StopOutOfMemory is a container the kernel killed for going over its
	// memory limit: the fix is under Scaling.
	StopOutOfMemory = "out_of_memory"
	// StopNotRunnable is a start command that could not be run at all: not
	// there (127), not executable (126), or refused by the runtime.
	StopNotRunnable = "not_runnable"
	// StopExitedCleanly is a process that finished, with 0. A web process
	// has to keep running, so this is a command that daemonises or a script
	// that does its work and returns.
	StopExitedCleanly = "exited_cleanly"
	// StopExited is every other exit, whose reason is in the app's own log.
	StopExited = "exited"
)

// StopKind sorts a container's termination by what fixes it.
func StopKind(reason string, exitCode int32) string {
	switch {
	case reason == "OOMKilled":
		return StopOutOfMemory
	// 126 is a command that is there and cannot be run, 127 one that is not
	// there. A runtime that could not start the process at all says
	// StartError, with 128, and "executable file not found" in its message.
	case exitCode == 126 || exitCode == 127 || reason == "StartError" || reason == "ContainerCannotRun":
		return StopNotRunnable
	case exitCode == 0:
		return StopExitedCleanly
	default:
		return StopExited
	}
}

// DescribeStop says how a container stopped, as the end of a sentence.
func DescribeStop(reason string, exitCode int32) string {
	switch StopKind(reason, exitCode) {
	case StopOutOfMemory:
		return "it ran out of memory"
	case StopNotRunnable:
		return fmt.Sprintf("its start command could not be run (exit code %d)", exitCode)
	default:
		return fmt.Sprintf("it exited with code %d", exitCode)
	}
}

// Times says a count of occurrences the way a sentence does.
func Times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

// RolloutTimeout is a rollout that did not finish: it ran out of the time it
// was given, or Kubernetes gave up on it first (Stalled).
type RolloutTimeout struct {
	Timeout time.Duration
	Stalled bool
	Ready   int32
	Desired int32
	// Instance is the newest of the new version's instances that is not
	// ready, and Waiting what it is waiting for in the panel's words. Both
	// are empty when no such instance could be read.
	Instance string
	Waiting  string
	// Message is what the Deployment itself said: a quota that refused the
	// instances, or the controller's own reason for giving up.
	Message string
}

func (e *RolloutTimeout) Error() string {
	if e.Stalled {
		return "the rollout stopped making progress: " + e.Reason()
	}
	return fmt.Sprintf("the rollout did not finish in %s: %s", e.Timeout, e.Reason())
}

// Reason is why the rollout did not finish, naming the instance that held it
// up. Not the app's summary: during a rolling update that describes the old
// version, which is still ready, and "1 instance(s) running" was the reason
// every timed-out deployment gave.
func (e *RolloutTimeout) Reason() string {
	switch {
	case e.Instance != "":
		return e.Instance + " is not ready: " + e.Waiting
	case e.Message != "":
		return e.Message
	}
	return fmt.Sprintf("%d of %d instances became ready", e.Ready, e.Desired)
}

// revisionAnnotation is where the Deployment controller numbers revisions,
// on the Deployment and on the ReplicaSet each revision runs in.
const revisionAnnotation = "deployment.kubernetes.io/revision"

// newRevisionPods are the instances of the revision a Deployment is rolling
// out, and not of the one it is replacing.
//
// Read by the pod-template-hash of the ReplicaSet the controller made for the
// current template. The deployment id on the template is not enough on its
// own: a sync that raises the memory limit — the fix for an instance that ran
// out — rolls out a new revision under the same deployment, and the old
// revision's instances, still being killed for want of memory, are not the
// new one's. Where that ReplicaSet cannot be read, the deployment id narrows
// the list as far as it can.
//
// Nothing until the controller has seen the current generation: before that,
// its revision and its ReplicaSets are the previous template's.
func (c *Client) newRevisionPods(ctx context.Context, deployment *appsv1.Deployment) []corev1.Pod {
	if deployment.Status.ObservedGeneration < deployment.Generation || deployment.Spec.Selector == nil {
		return nil
	}
	selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels).String()
	pods, err := c.clientset.CoreV1().Pods(deployment.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil || pods == nil {
		return nil
	}
	hash := c.currentTemplateHash(ctx, deployment, selector)
	id := deployment.Spec.Template.Annotations[version.LabelKey("deployment-id")]
	var out []corev1.Pod
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		switch {
		case hash != "":
			if pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey] != hash {
				continue
			}
		case id != "":
			if pod.Annotations[version.LabelKey("deployment-id")] != id {
				continue
			}
		}
		out = append(out, pod)
	}
	return out
}

// currentTemplateHash is the pod-template-hash of the ReplicaSet running the
// Deployment's current revision, or empty when there is none to read.
func (c *Client) currentTemplateHash(ctx context.Context, deployment *appsv1.Deployment, selector string) string {
	revision := deployment.Annotations[revisionAnnotation]
	if revision == "" {
		return ""
	}
	sets, err := c.clientset.AppsV1().ReplicaSets(deployment.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil || sets == nil {
		return ""
	}
	for _, set := range sets.Items {
		if set.Annotations[revisionAnnotation] != revision || !ownedByDeployment(set.OwnerReferences, deployment) {
			continue
		}
		return set.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
	}
	return ""
}

func ownedByDeployment(owners []metav1.OwnerReference, deployment *appsv1.Deployment) bool {
	for _, owner := range owners {
		if owner.Kind == "Deployment" && owner.Name == deployment.Name &&
			(owner.UID == "" || deployment.UID == "" || owner.UID == deployment.UID) {
			return true
		}
	}
	return false
}

// crashed reads the new revision's instances for one that keeps stopping as
// it starts.
//
// Three things count: a container that has stopped and been started again
// twice, one the kubelet is backing off from restarting (which it does from
// the second stop), and one the kernel killed for its memory, once — waiting
// will not give it more. A container the kubelet stopped because it did not
// answer its health check is not one of them: it is slow, or listening in the
// wrong place, and calling it a crash would send somebody to read a log that
// says nothing was wrong.
func (c *Client) crashed(ctx context.Context, deployment *appsv1.Deployment) *CrashError {
	for _, pod := range c.newRevisionPods(ctx, deployment) {
		for _, status := range pod.Status.ContainerStatuses {
			current := status.State.Terminated
			last := current
			if last == nil {
				last = status.LastTerminationState.Terminated
			}
			if last == nil {
				continue
			}
			backingOff := status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff"
			outOfMemory := last.Reason == "OOMKilled" ||
				(status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.Reason == "OOMKilled")
			repeated := status.LastTerminationState.Terminated != nil && status.RestartCount >= 2
			if !backingOff && !outOfMemory && !repeated {
				continue
			}
			if outOfMemory && last.Reason != "OOMKilled" {
				last = status.LastTerminationState.Terminated
			}
			if !outOfMemory && c.killedByProbe(ctx, pod, status.Name, last) {
				continue
			}
			stops := int(status.RestartCount)
			if current != nil || backingOff {
				stops++
			}
			return &CrashError{
				Pod: pod.Name, Container: status.Name, Stops: max(stops, 1),
				ExitCode: last.ExitCode, Reason: last.Reason, Message: strings.TrimSpace(last.Message),
			}
		}
	}
	return nil
}

// killedByProbe reports whether the kubelet stopped this container because
// its health check failed, rather than the container stopping by itself.
//
// Its termination alone cannot tell: it is restarted like a crash, and exits
// with whatever SIGTERM made of it — 143, or 0 for an app that shuts down
// cleanly. The kubelet's own event can, while it lasts. After that, a
// container that ran for its whole time to start before it stopped was
// stopped for not answering within it.
func (c *Client) killedByProbe(ctx context.Context, pod corev1.Pod, container string, last *corev1.ContainerStateTerminated) bool {
	for _, event := range c.podEvents(ctx, pod) {
		if event.Reason != "Killing" || !forContainer(event, container) {
			continue
		}
		message := strings.ToLower(event.Message)
		if strings.Contains(message, "failed startup probe") || strings.Contains(message, "failed liveness probe") {
			return true
		}
	}
	probe := startupProbe(pod, container)
	if probe == nil || last.StartedAt.IsZero() || last.FinishedAt.IsZero() {
		return false
	}
	ran := last.FinishedAt.Sub(last.StartedAt.Time)
	return ran >= startBudget(probe)-time.Duration(probePeriod(probe))*time.Second
}

// podEvents is what Kubernetes said about one pod. A list that fails is no
// events, which is what an hour-old pod has anyway.
func (c *Client) podEvents(ctx context.Context, pod corev1.Pod) []corev1.Event {
	list, err := c.clientset.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("involvedObject.name", pod.Name).String(),
	})
	if err != nil || list == nil {
		return nil
	}
	out := make([]corev1.Event, 0, len(list.Items))
	for _, event := range list.Items {
		// The field selector is the API server's to apply; this keeps the
		// answer right where it was not applied.
		if event.InvolvedObject.Kind == "Pod" && event.InvolvedObject.Name == pod.Name {
			out = append(out, event)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		_, _, a := eventSpan(&out[i])
		_, _, b := eventSpan(&out[j])
		return a.After(b)
	})
	return out
}

// forContainer reports whether an event is about this container, or about
// the pod as a whole.
func forContainer(event corev1.Event, container string) bool {
	path := event.InvolvedObject.FieldPath
	return path == "" || strings.Contains(path, "{"+container+"}")
}

func startupProbe(pod corev1.Pod, container string) *corev1.Probe {
	for _, spec := range pod.Spec.Containers {
		if spec.Name == container {
			return spec.StartupProbe
		}
	}
	return nil
}

// probePeriod is a probe's period, with Kubernetes' default for none.
func probePeriod(probe *corev1.Probe) int32 {
	if probe.PeriodSeconds > 0 {
		return probe.PeriodSeconds
	}
	return 10
}

// startBudget is how long a startup probe lets a container go unanswered
// before the kubelet stops it.
func startBudget(probe *corev1.Probe) time.Duration {
	threshold := probe.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	return time.Duration(probe.InitialDelaySeconds+probePeriod(probe)*threshold) * time.Second
}

// stalledInstance is the newest of the new revision's instances that is not
// ready, and what it is waiting for.
func (c *Client) stalledInstance(ctx context.Context, pods []corev1.Pod) (string, string) {
	pods = append([]corev1.Pod(nil), pods...)
	sort.SliceStable(pods, func(i, j int) bool {
		return pods[i].CreationTimestamp.After(pods[j].CreationTimestamp.Time)
	})
	for _, pod := range pods {
		if podReady(pod) {
			continue
		}
		return pod.Name, c.notReadyBecause(ctx, pod)
	}
	return "", ""
}

func podReady(pod corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// notReadyBecause says what an instance that is not ready is waiting for.
//
// What the instance list says, where it says something: a container waiting
// to be pulled or created, a scheduler's refusal, a crash. An instance that is
// running and not ready is waiting on its health check, and the kubelet's
// event says which one and what it got back.
func (c *Client) notReadyBecause(ctx context.Context, pod corev1.Pod) string {
	inst := describePod(pod)
	if inst.Message != "" {
		return inst.Message
	}
	if inst.LastReason != "" && inst.Status == "CrashLoopBackOff" {
		return "it keeps stopping: " + DescribeStop(inst.LastReason, inst.LastExitCode) + "."
	}
	if pod.Status.Phase != corev1.PodRunning {
		return waitingFor(pod)
	}
	for _, event := range c.podEvents(ctx, pod) {
		message := strings.TrimSpace(event.Message)
		lower := strings.ToLower(message)
		switch {
		case event.Reason == "Killing" && strings.Contains(lower, "failed startup probe"),
			event.Reason == "Unhealthy" && strings.HasPrefix(lower, "startup probe"):
			return "it did not answer its health check within its time to start" + budgetOf(pod) + ": " + message
		case event.Reason == "Unhealthy" && strings.HasPrefix(lower, "readiness probe"):
			return "its health check fails: " + message
		}
	}
	return "it is running and has not passed its health check yet."
}

// budgetOf is the app's time to start, as the startup probe has it, in
// brackets, or nothing when it has none.
func budgetOf(pod corev1.Pod) string {
	for _, spec := range pod.Spec.Containers {
		if spec.StartupProbe != nil {
			return " (" + startBudget(spec.StartupProbe).String() + ")"
		}
	}
	return ""
}

// PreviousLogTail reads the last lines a container printed before it last
// stopped: the container that crashed, not the one started in its place.
//
// A container that has stopped and not been started again yet has no earlier
// one, and its own log, which is the same lines, is read instead.
func (c *Client) PreviousLogTail(ctx context.Context, namespace, pod, container string, lines int64) (string, error) {
	// A minified stack trace is one line; this keeps one from being the
	// whole of a deployment's log.
	limit := int64(16 << 10)
	read := func(previous bool) (string, error) {
		stream, err := c.StreamClientset().CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
			Container: container, Previous: previous, TailLines: &lines, LimitBytes: &limit,
		}).Stream(ctx)
		if err != nil {
			return "", err
		}
		defer stream.Close()
		body, err := io.ReadAll(io.LimitReader(stream, limit))
		return string(body), err
	}
	tail, err := read(true)
	if err != nil {
		if tail, err = read(false); err != nil {
			return "", fmt.Errorf("read the log of %s: %w", pod, err)
		}
	}
	return strings.TrimRight(tail, "\n"), nil
}
