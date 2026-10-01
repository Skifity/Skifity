package logdrain

import (
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/version"
)

// How the collector is doing, from what Kubernetes already knows: the
// DaemonSet's counts, each pod's state, and the warnings Kubernetes wrote
// about them. That is cheap and it is honest about its reach — it says
// whether the collector runs, not whether a service took every line. Vector's
// own account of each delivery is in its log, and reading every server's log
// to find it is not a thing to do each time somebody opens a page.

// Collector states.
const (
	CollectorRunning  = "running"
	CollectorStarting = "starting"
	CollectorFailing  = "failing"
	CollectorAbsent   = "absent"
)

// CollectorStatus is the collector as the panel shows it.
type CollectorStatus struct {
	State string `json:"state"`
	// Version is the Vector the DaemonSet runs.
	Version string `json:"version,omitempty"`
	// Desired and Ready count servers: one collector each.
	Desired  int                `json:"desired"`
	Ready    int                `json:"ready"`
	Problems []CollectorProblem `json:"problems"`
}

// CollectorProblem is one thing wrong with one collector, or with all of them.
type CollectorProblem struct {
	// Server is the server it is about; empty for the DaemonSet as a whole.
	Server string `json:"server,omitempty"`
	// Reason is Kubernetes' word for it, or ConfigurationRefused for a
	// collector that stopped because it could not read its configuration.
	Reason  string    `json:"reason"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at,omitzero"`
}

// ReasonConfigurationRefused is Vector exiting with 78, EX_CONFIG: it would
// not start with the configuration it was given.
const ReasonConfigurationRefused = "ConfigurationRefused"

// maxProblems is how many problems are shown. The first few say what is
// wrong; the rest say it again.
const maxProblems = 8

// waitingTrouble are the reasons a container waits that are not a start
// taking its time.
var waitingTrouble = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true,
	"InvalidImageName": true, "CreateContainerConfigError": true, "CreateContainerError": true,
	"RunContainerError": true,
}

// Summarize reads the collector's state. A nil DaemonSet is a collector that
// is not there.
func Summarize(set *appsv1.DaemonSet, pods []corev1.Pod, events []kube.ObjectEvent) CollectorStatus {
	if set == nil {
		return CollectorStatus{State: CollectorAbsent, Problems: []CollectorProblem{}}
	}
	status := CollectorStatus{
		Version:  set.Annotations[version.LabelKey("collector-version")],
		Desired:  int(set.Status.DesiredNumberScheduled),
		Ready:    int(set.Status.NumberReady),
		Problems: []CollectorProblem{},
	}
	failing := false

	sorted := append([]corev1.Pod(nil), pods...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Spec.NodeName < sorted[j].Spec.NodeName })
	for _, pod := range sorted {
		server := pod.Spec.NodeName
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				status.Problems = append(status.Problems, CollectorProblem{
					Server: server, Reason: condition.Reason, Message: scrub(condition.Message),
					At: condition.LastTransitionTime.UTC(),
				})
			}
		}
		for _, container := range pod.Status.ContainerStatuses {
			if container.State.Waiting != nil && waitingTrouble[container.State.Waiting.Reason] {
				failing = true
				problem := CollectorProblem{
					Server: server, Reason: container.State.Waiting.Reason,
					Message: scrub(container.State.Waiting.Message),
				}
				if last := container.LastTerminationState.Terminated; last != nil {
					problem.At = last.FinishedAt.UTC()
					if last.ExitCode == 78 {
						problem.Reason = ReasonConfigurationRefused
					}
					if problem.Message == "" {
						problem.Message = fmt.Sprintf("exit code %d", last.ExitCode)
					}
				}
				status.Problems = append(status.Problems, problem)
			}
		}
	}
	for _, event := range events {
		if event.Type != corev1.EventTypeWarning {
			continue
		}
		if event.Kind == "DaemonSet" && event.Reason == "FailedCreate" {
			failing = true
		}
		status.Problems = append(status.Problems, CollectorProblem{
			Reason: event.Reason, Message: scrub(event.Message), At: event.LastSeen,
		})
	}
	if len(status.Problems) > maxProblems {
		status.Problems = status.Problems[:maxProblems]
	}

	switch {
	case failing:
		status.State = CollectorFailing
	case status.Desired > 0 && status.Ready == status.Desired:
		status.State = CollectorRunning
	default:
		status.State = CollectorStarting
	}
	return status
}

func scrub(message string) string {
	if len(message) > 500 {
		message = message[:500] + "…"
	}
	return logging.Scrub(message)
}
