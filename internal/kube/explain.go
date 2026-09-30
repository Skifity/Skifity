package kube

import "strings"

// Saying why an instance is not running.
//
// Kubernetes already knows. The scheduler writes the reason onto the pod's
// PodScheduled condition, and the ReplicaSet controller writes a quota refusal
// onto the Deployment's ReplicaFailure condition. The panel used to ignore both
// and print one fixed sentence — "waiting for a server with enough free CPU and
// memory" — whatever the actual answer was.
//
// That sentence is right about a third of the time, and the other two thirds it
// sends somebody to look at the wrong thing: they add a server for a problem
// that was a quota, or they wait for a scheduler that is never going to place a
// pod whose volume cannot be bound.
//
// The translations below turn the scheduler's phrasing into the panel's, with
// the fix in it. Anything not recognised falls through with Kubernetes' own
// words, which is still the truth and still better than the fixed sentence.

// ExplainUnschedulable turns a scheduler message into one a person can act on.
//
// The messages look like:
//
//	0/3 nodes are available: 1 Insufficient cpu, 2 node(s) had untolerated taint
//	  {node-role.kubernetes.io/control-plane: }.
//
// so this matches on the fragments rather than the whole, and reports the first
// cause it recognises. One cause is what somebody can act on; a list of three is
// how a status line becomes something nobody reads.
func ExplainUnschedulable(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "insufficient cpu") && strings.Contains(lower, "insufficient memory"):
		return "No server has enough free CPU and memory for this instance. " +
			"Lower what it reserves under Scaling, or add a server."
	case strings.Contains(lower, "insufficient cpu"):
		return "No server has enough free CPU for this instance. " +
			"Lower the CPU it reserves under Scaling, or add a server."
	case strings.Contains(lower, "insufficient memory"):
		return "No server has enough free memory for this instance. " +
			"Lower the memory it reserves under Scaling, or add a server."
	case strings.Contains(lower, "unbound immediate persistentvolumeclaims"),
		strings.Contains(lower, "pod has unbound"):
		return "This app's volume has not been created yet. " +
			"On a single server that is usually a storage class that is still starting; " +
			"across servers it means no server can provide the volume."
	case strings.Contains(lower, "untolerated taint"):
		return "The only servers with room are not accepting apps. " +
			"A control-plane server does not run apps unless you allow it, " +
			"and a server being drained accepts nothing."
	case strings.Contains(lower, "node(s) didn't match pod's node affinity"),
		strings.Contains(lower, "didn't match node selector"):
		return "No server matches where this app is allowed to run."
	case strings.Contains(lower, "didn't match pod anti-affinity"),
		strings.Contains(lower, "didn't satisfy existing pods anti-affinity"):
		return "Every server already runs an instance of this app. " +
			"Add a server, or run fewer instances."
	case strings.Contains(lower, "nodes are available") && strings.Contains(lower, "0/"):
		// Recognised as a scheduling failure without a cause we have words
		// for. The scheduler's own sentence is more use than a guess.
		return "No server can take this instance: " + strings.TrimSpace(message)
	case strings.TrimSpace(message) != "":
		return strings.TrimSpace(message)
	default:
		return "Waiting for a server with room for this instance."
	}
}

// ExplainReplicaFailure turns a ReplicaSet's refusal into a sentence with a fix.
//
// This is where a quota lands, and it is the one failure that never reaches a
// pod at all: there is nothing Pending to look at, because nothing was created.
// A person seeing "0 of 3 instances ready" with no pods and no message has
// nowhere to go.
func ExplainReplicaFailure(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "exceeded quota"):
		return "This environment has reached its limit, so no more instances can start. " +
			"Raise the environment's limits, or give this app less. (" + strings.TrimSpace(message) + ")"
	case strings.Contains(lower, "forbidden") && strings.Contains(lower, "violates podsecurity"):
		return "The instance was refused by the cluster's security rules: " + strings.TrimSpace(message)
	case strings.TrimSpace(message) != "":
		return strings.TrimSpace(message)
	default:
		return "The instances could not be created."
	}
}

// ExplainImagePull turns a pull failure into something actionable.
//
// It is nearly always one of three things, and telling them apart is the whole
// value: a typo, a private registry with no credentials, or an image the
// registry garbage collector removed.
func ExplainImagePull(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication required"):
		return "The registry refused to hand over the image. " +
			"If it is a private registry, set its credentials under Settings, then Registry."
	case strings.Contains(lower, "manifest unknown"), strings.Contains(lower, "not found"):
		return "That image is not in the registry. " +
			"If this is a rollback to an old version, its image has been removed to keep " +
			"the disk free; deploy the commit again instead."
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "timeout"):
		return "The registry could not be reached from the server. " +
			"Check the address, and that the server has a route to it."
	case strings.TrimSpace(message) != "":
		return "The image could not be pulled: " + strings.TrimSpace(message)
	default:
		return "The image could not be pulled."
	}
}

// ExplainImageRunsAsRoot turns the kubelet's refusal into the one sentence that
// says what to do.
//
// The message is `container has runAsNonRoot and image will run as root`, and
// on its own it reads like a fault in the image. It is not: starting as root
// and dropping privileges is what the official WordPress, Nextcloud, MediaWiki
// and phpMyAdmin images all do, and it is what this environment's confinement
// level refuses. The fix is one switch on the environment, and nobody finds it
// from the kubelet's wording.
func ExplainImageRunsAsRoot() string {
	return "This image starts as root and drops privileges itself, which is ordinary for " +
		"an off-the-shelf container and is what this environment refuses. Change the " +
		"environment's confinement to \"baseline\" under the project, then deploy again. " +
		"Baseline still refuses a privileged container, host networking and host paths, " +
		"so the app still cannot reach the server it runs on."
}

// RunsAsRootRefusal reports whether a container's message is that refusal.
func RunsAsRootRefusal(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "runasnonroot") && strings.Contains(lower, "will run as root")
}
