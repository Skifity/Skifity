package kube

import (
	"fmt"
	"strings"
)

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
func ExplainUnschedulable(message string) string { return unschedulable(message).Text }

func unschedulable(message string) Explanation {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "insufficient cpu") && strings.Contains(lower, "insufficient memory"):
		return explained("scheduling_cpu_memory")
	case strings.Contains(lower, "insufficient cpu"):
		return explained("scheduling_cpu")
	case strings.Contains(lower, "insufficient memory"):
		return explained("scheduling_memory")
	case strings.Contains(lower, "unbound immediate persistentvolumeclaims"),
		strings.Contains(lower, "pod has unbound"):
		return explained("scheduling_volume")
	case strings.Contains(lower, "untolerated taint"):
		return explained("scheduling_taint")
	case strings.Contains(lower, "node(s) didn't match pod's node affinity"),
		strings.Contains(lower, "didn't match node selector"):
		return explained("scheduling_node_affinity")
	case strings.Contains(lower, "didn't match pod anti-affinity"),
		strings.Contains(lower, "didn't satisfy existing pods anti-affinity"):
		return explained("scheduling_anti_affinity")
	case strings.Contains(lower, "nodes are available") && strings.Contains(lower, "0/"):
		// Recognised as a scheduling failure without a cause we have words
		// for. The scheduler's own sentence is more use than a guess.
		return explained("scheduling_unexplained", strings.TrimSpace(message))
	case strings.TrimSpace(message) != "":
		return Explanation{Text: strings.TrimSpace(message)}
	default:
		return explained("scheduling_waiting")
	}
}

// ExplainReplicaFailure turns a ReplicaSet's refusal into a sentence with a fix.
//
// This is where a quota lands, and it is the one failure that never reaches a
// pod at all: there is nothing Pending to look at, because nothing was created.
// A person seeing "0 of 3 instances ready" with no pods and no message has
// nowhere to go.
func ExplainReplicaFailure(message string) string { return replicaFailure(message).Text }

func replicaFailure(message string) Explanation {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "exceeded quota"):
		return explained("replicas_quota", strings.TrimSpace(message))
	case strings.Contains(lower, "forbidden") && strings.Contains(lower, "violates podsecurity"):
		return explained("replicas_pod_security", strings.TrimSpace(message))
	case strings.TrimSpace(message) != "":
		return Explanation{Text: strings.TrimSpace(message)}
	default:
		return explained("replicas_failed")
	}
}

// ExplainImagePull turns a pull failure into something actionable.
//
// It is nearly always one of three things, and telling them apart is the whole
// value: a typo, a private registry with no credentials, or an image the
// registry garbage collector removed.
func ExplainImagePull(message string) string { return imagePull(message).Text }

func imagePull(message string) Explanation {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication required"):
		return explained("pull_unauthorized")
	case strings.Contains(lower, "manifest unknown"), strings.Contains(lower, "not found"):
		return explained("pull_missing")
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "timeout"):
		return explained("pull_unreachable")
	case strings.TrimSpace(message) != "":
		return explained("pull_failed", strings.TrimSpace(message))
	default:
		return explained("pull_failed_empty")
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
func ExplainImageRunsAsRoot() string { return explained("runs_as_root").Text }

// ExplainNamedUser is the kubelet's other refusal at the strict level, of an
// image whose USER is a name: `container has runAsNonRoot and image has
// non-numeric user (nobody), cannot verify user is non-root`. The image is
// not root; the kubelet only cannot tell from a name. Giving the number is
// the fix that keeps the environment strict.
func ExplainNamedUser(message string) string {
	return explained("named_user", namedUser(message)).Text
}

// namedUserPrefix and namedUserRest are ExplainNamedUser's sentence around
// the name, so the app's summary can recognise what describePod wrote.
const (
	namedUserPrefix = "This image names its user ("
	namedUserRest   = ") instead of numbering it, so the cluster " +
		"cannot check that it is not root, and this environment refuses what it cannot check. " +
		"Set \"Run as user\" in the app's settings to that user's number — 65534 for nobody — " +
		"and deploy again, or change the environment's confinement to \"baseline\" under the project."
)

// namedUser is the name in the kubelet's message, or a stand-in.
func namedUser(message string) string {
	name := "a name"
	if start := strings.Index(message, "non-numeric user ("); start >= 0 {
		rest := message[start+len("non-numeric user ("):]
		if end := strings.Index(rest, ")"); end > 0 {
			name = rest[:end]
		}
	}
	return name
}

// NamedUserRefusal reports whether a container's message is that refusal.
func NamedUserRefusal(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "runasnonroot") && strings.Contains(lower, "non-numeric user")
}

// RunsAsRootRefusal reports whether a container's message is that refusal.
func RunsAsRootRefusal(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "runasnonroot") && strings.Contains(lower, "will run as root")
}

// Explanation is one of the sentences above: the English, and the code the
// panel looks its own language's words up by — events.explain.<code> — with
// the values interpolated into it, in order. A message Kubernetes wrote that
// the panel has no words for is passed through with no code.
type Explanation struct {
	Code string
	Text string
	Args []string
}

// explanations are every sentence the panel explains a failure with, by code.
// A %s is a value taken from Kubernetes' own message; the locale writes {{0}}
// for the first. TestEveryExplanationHasItsWords reads this table, so a
// sentence added here without its words in every language fails there.
var explanations = map[string]string{
	"scheduling_cpu_memory": "No server has enough free CPU and memory for this instance. " +
		"Lower what it reserves under Scaling, or add a server.",
	"scheduling_cpu": "No server has enough free CPU for this instance. " +
		"Lower the CPU it reserves under Scaling, or add a server.",
	"scheduling_memory": "No server has enough free memory for this instance. " +
		"Lower the memory it reserves under Scaling, or add a server.",
	"scheduling_volume": "This app's volume has not been created yet. " +
		"On a single server that is usually a storage class that is still starting; " +
		"across servers it means no server can provide the volume.",
	"scheduling_taint": "The only servers with room are not accepting apps. " +
		"A control-plane server does not run apps unless you allow it, " +
		"and a server being drained accepts nothing.",
	"scheduling_node_affinity": "No server matches where this app is allowed to run.",
	"scheduling_anti_affinity": "Every server already runs an instance of this app. " +
		"Add a server, or run fewer instances.",
	"scheduling_unexplained": "No server can take this instance: %s",
	"scheduling_waiting":     "Waiting for a server with room for this instance.",

	"replicas_quota": "This environment has reached its limit, so no more instances can start. " +
		"Raise the environment's limits, or give this app less. (%s)",
	"replicas_pod_security": "The instance was refused by the cluster's security rules: %s",
	"replicas_failed":       "The instances could not be created.",

	"pull_unauthorized": "The registry refused to hand over the image. " +
		"If it is a private registry, set its credentials under Settings, then Registry.",
	"pull_missing": "That image is not in the registry. " +
		"If this is a rollback to an old version, its image has been removed to keep " +
		"the disk free; deploy the commit again instead.",
	"pull_unreachable": "The registry could not be reached from the server. " +
		"Check the address, and that the server has a route to it.",
	"pull_failed":       "The image could not be pulled: %s",
	"pull_failed_empty": "The image could not be pulled.",

	"runs_as_root": "This image starts as root and drops privileges itself, which is ordinary for " +
		"an off-the-shelf container and is what this environment refuses. Change the " +
		"environment's confinement to \"baseline\" under the project, then deploy again. " +
		"Baseline still refuses a privileged container, host networking and host paths, " +
		"so the app still cannot reach the server it runs on.",
	"named_user": namedUserPrefix + "%s" + namedUserRest,

	"crash_backoff": "The app keeps stopping soon after it starts, so Kubernetes waits longer before each restart. " +
		"Why it stopped is in its log from before the restart: open Logs, then Before the restart.",
	"probe_readiness": "The health check failed, so this instance is sent no traffic until it passes. " +
		"Check the health check's path and port under Settings, and that the app answers there in time.",
	"probe_liveness": "The health check kept failing, so the instance is being restarted. " +
		"A check that is slow or points at the wrong path restarts an app that is working: check it under Settings.",
	"probe_startup": "The app did not pass its health check in the time it is given to start. " +
		"If it starts slowly, give it longer under Settings; otherwise its logs say why it is not answering.",
	"mount_config_missing": "The instance cannot start because configuration it reads is missing from the cluster: %s. " +
		"Put the app back under Advanced, or deploy again, and it is written again.",
	"mount_failed": "A volume could not be mounted. A volume is used by one server at a time: " +
		"if an instance on another server still holds it, this one waits until that instance stops.",
	"attach_failed": "The storage behind a volume could not be attached to this server. " +
		"It is usually still attached to another server, and is released once that server lets it go.",
	"sandbox_failed": "The server could not set up this instance's network or container. " +
		"That is a problem on the server rather than in the app: if it goes on, restart k3s on that server, " +
		"or remove the server and add it again.",
	"oom": "The app used more memory than its limit and was stopped. " +
		"Raise the memory limit under Scaling, or find what is using the memory.",
	"evicted": "The server ran short and stopped this instance to protect itself: %s " +
		"It is started again on a server with room.",
}

// explained builds the explanation with this code, interpolating args.
func explained(code string, args ...string) Explanation {
	format, ok := explanations[code]
	if !ok {
		// A code with no sentence is a bug here rather than in the cluster;
		// the values are still the truth.
		return Explanation{Text: strings.Join(args, " ")}
	}
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg
	}
	return Explanation{Code: code, Text: fmt.Sprintf(format, values...), Args: args}
}

// ExplainEvent says what a Kubernetes event means for the app, when the panel
// knows: the reasons behind most of the questions somebody opens the events
// with. Anything else has no explanation, and the event's own words stand.
func ExplainEvent(reason, message string) Explanation {
	lower := strings.ToLower(message)
	switch reason {
	case "FailedScheduling":
		return unschedulable(message)
	case "FailedCreate":
		return replicaFailure(message)
	case "ErrImagePull", "ImagePullBackOff", "FailedPull", "ErrImageNeverPull", "InspectFailed":
		return imagePull(message)
	case "BackOff":
		if strings.Contains(lower, "pulling image") {
			return imagePull(message)
		}
		return explained("crash_backoff")
	case "Failed":
		switch {
		case RunsAsRootRefusal(message):
			return explained("runs_as_root")
		case NamedUserRefusal(message):
			return explained("named_user", namedUser(message))
		case strings.Contains(lower, "pull"):
			return imagePull(message)
		}
	case "Unhealthy":
		switch {
		case strings.HasPrefix(lower, "startup probe"):
			return explained("probe_startup")
		case strings.HasPrefix(lower, "liveness probe"):
			return explained("probe_liveness")
		case strings.HasPrefix(lower, "readiness probe"):
			return explained("probe_readiness")
		}
	case "FailedMount":
		if strings.Contains(lower, "not found") &&
			(strings.Contains(lower, "secret") || strings.Contains(lower, "configmap")) {
			return explained("mount_config_missing", missingObject(message))
		}
		return explained("mount_failed")
	case "FailedAttachVolume":
		return explained("attach_failed")
	case "FailedCreatePodSandBox":
		return explained("sandbox_failed")
	case "OOMKilling", "OOMKilled":
		return explained("oom")
	case "Evicted":
		detail := strings.TrimSpace(message)
		if detail == "" {
			detail = "the server was low on resources."
		}
		return explained("evicted", detail)
	}
	return Explanation{}
}

// missingObject is the name in the kubelet's `secret "web-env" not found`, or
// the whole message when it is not in that shape.
func missingObject(message string) string {
	lower := strings.ToLower(message)
	for _, kind := range []string{`secret "`, `configmap "`} {
		start := strings.Index(lower, kind)
		if start < 0 {
			continue
		}
		rest := message[start+len(kind):]
		if end := strings.Index(rest, `"`); end > 0 {
			return rest[:end]
		}
	}
	return strings.TrimSpace(message)
}
