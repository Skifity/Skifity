package kube

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// What Kubernetes said about an app's objects.
//
// The scheduler, the kubelet and the controllers write down everything they do
// and everything that goes wrong as Events in the namespace: a pod that could
// not be placed, a probe that failed, a volume that would not attach. They
// last an hour, they are the first thing anybody who knows Kubernetes reads,
// and the panel showed none of them.

// MaxEvents bounds a feed. An hour of a crash-looping app is a few dozen
// distinct events once repeats are folded; a thousand is somebody's loop.
const MaxEvents = 200

// ObjectEvent is one thing Kubernetes said about one object, with its repeats
// folded into a count.
type ObjectEvent struct {
	// Type is Normal or Warning.
	Type    string
	Reason  string
	Kind    string
	Name    string
	Message string
	Count   int
	// FirstSeen and LastSeen span the repeats.
	FirstSeen time.Time
	LastSeen  time.Time
	// Explanation is what the panel makes of it, when it knows. Its Code is
	// empty otherwise.
	Explanation Explanation
}

// EventScope says which objects in a namespace something owns.
type EventScope struct {
	// Owns reports whether an object belongs, by kind and name.
	Owns func(kind, name string) bool
	// Selector finds the pods that exist now, whatever they are called: a
	// one-off run's, whose name nothing else predicts.
	Selector string
}

// Events lists what Kubernetes said about the objects in scope, newest first,
// with repeats folded together.
func (c *Client) Events(ctx context.Context, namespace string, scope EventScope) ([]ObjectEvent, error) {
	pods := map[string]bool{}
	if scope.Selector != "" {
		list, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: scope.Selector})
		if err != nil && !IsNotFound(err) {
			return nil, fmt.Errorf("list the pods in %s: %w", namespace, err)
		}
		if list != nil {
			for _, pod := range list.Items {
				pods[pod.Name] = true
			}
		}
	}

	list, err := c.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if IsNotFound(err) {
			return []ObjectEvent{}, nil
		}
		return nil, fmt.Errorf("list the events in %s: %w", namespace, err)
	}

	type key struct{ kind, name, kindOf, reason, message string }
	folded := map[key]*ObjectEvent{}
	for i := range list.Items {
		event := &list.Items[i]
		kind, name := event.InvolvedObject.Kind, event.InvolvedObject.Name
		if !(kind == "Pod" && pods[name]) && (scope.Owns == nil || !scope.Owns(kind, name)) {
			continue
		}
		count, first, last := eventSpan(event)
		k := key{kind, name, event.Type, event.Reason, event.Message}
		if existing, ok := folded[k]; ok {
			existing.Count += count
			if first.Before(existing.FirstSeen) {
				existing.FirstSeen = first
			}
			if last.After(existing.LastSeen) {
				existing.LastSeen = last
			}
			continue
		}
		folded[k] = &ObjectEvent{
			Type: event.Type, Reason: event.Reason, Kind: kind, Name: name,
			Message: event.Message, Count: count, FirstSeen: first, LastSeen: last,
			Explanation: ExplainEvent(event.Reason, event.Message),
		}
	}

	out := make([]ObjectEvent, 0, len(folded))
	for _, event := range folded {
		out = append(out, *event)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		// Warnings first among events of the same moment, then a stable
		// order, so the table does not reshuffle on every refresh.
		if out[i].Type != out[j].Type {
			return out[i].Type == corev1.EventTypeWarning
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Reason < out[j].Reason
	})
	if len(out) > MaxEvents {
		out = out[:MaxEvents]
	}
	return out, nil
}

// eventSpan reads how often an event happened and when, from whichever of the
// two Event APIs wrote it: core/v1's count and timestamps, or events.k8s.io's
// series and eventTime, which the kubelet has used since 1.19.
func eventSpan(event *corev1.Event) (count int, first, last time.Time) {
	count = int(event.Count)
	first = event.FirstTimestamp.Time
	last = event.LastTimestamp.Time
	if event.Series != nil {
		count = max(count, int(event.Series.Count))
		if event.Series.LastObservedTime.After(last) {
			last = event.Series.LastObservedTime.Time
		}
	}
	if !event.EventTime.IsZero() {
		if first.IsZero() {
			first = event.EventTime.Time
		}
		if event.EventTime.After(last) {
			last = event.EventTime.Time
		}
	}
	if first.IsZero() {
		first = event.CreationTimestamp.Time
	}
	if last.IsZero() {
		last = first
	}
	return max(count, 1), first.UTC(), last.UTC()
}

// WorkloadPods matches what a Deployment, a StatefulSet or a Job's pods and
// ReplicaSets are called, for events about ones that are already gone: the
// instance that was evicted, the one the out-of-memory killer took.
//
// A Deployment's ReplicaSet is <name>-<hash>, and its pods <name>-<hash>-<5>;
// a StatefulSet's pods are <name>-<ordinal>; a Job's are <name>-<5>. The hash
// never holds a hyphen, which is what keeps web's pods from matching web-api's.
type WorkloadPods struct {
	pods, sets *regexp.Regexp
}

// NewWorkloadPods matches the pods and ReplicaSets of these workloads.
func NewWorkloadPods(names ...string) WorkloadPods {
	if len(names) == 0 {
		return WorkloadPods{}
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	alternatives := "(?:" + strings.Join(quoted, "|") + ")"
	return WorkloadPods{
		pods: regexp.MustCompile(`^` + alternatives + `-(?:[a-z0-9]{1,10}-[a-z0-9]{5}|[a-z0-9]{5}|[0-9]+)$`),
		sets: regexp.MustCompile(`^` + alternatives + `-[a-z0-9]{1,10}$`),
	}
}

// Match reports whether an object is one of those pods or ReplicaSets.
func (w WorkloadPods) Match(kind, name string) bool {
	switch {
	case w.pods == nil:
		return false
	case kind == "Pod":
		return w.pods.MatchString(name)
	case kind == "ReplicaSet":
		return w.sets.MatchString(name)
	}
	return false
}
