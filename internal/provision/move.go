package provision

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// movedAnnotation is the pod template annotation a rollout is asked for with,
// the one `kubectl rollout restart` writes, so anything that knows that
// command reads this as what it is.
const movedAnnotation = "kubectl.kubernetes.io/restartedAt"

// moveWait is how long a server's removal waits for the apps it moves
// first to come up elsewhere, in all, before it evicts what is left.
const moveWait = 3 * time.Minute

// evictRetry is how long an eviction a disruption budget deferred waits
// before it is asked again. A variable so a test need not wait it out.
var evictRetry = 5 * time.Second

// moveSoleInstances rolls out, onto the other servers, every Deployment whose
// ready instances are all on the node being emptied, and waits for each to
// finish, for at most wait in all.
//
// Eviction stops an instance first and only then does its Deployment start
// another, so an app whose one instance, or every instance, was on the
// server went down while its replacement started: the two-server crosscheck
// counted the requests that failed while a server was taken out. A rollout
// starts the new instance first and stops the old one once the new one is
// ready, which is how every deploy of these apps already goes, and with the
// server cordoned the new one starts on another. A Deployment that replaces
// its instances by stopping them first, an app with a volume, is left to the
// eviction: its instance cannot be anywhere else first.
//
// What it returns is how many of the node's instances belonged to the apps it
// moved. Whatever it could not move is still on the node, and the eviction
// after it is what moves that.
func moveSoleInstances(ctx context.Context, clientset kubernetes.Interface, log *slog.Logger,
	pods []corev1.Pod, node string, wait time.Duration) int {
	type key struct{ namespace, name string }
	seen := map[key]bool{}
	var moving []key
	instances := map[key]int{}
	for _, pod := range pods {
		owner := metav1.GetControllerOf(&pod)
		if owner == nil || owner.Kind != "ReplicaSet" {
			continue
		}
		replicaSet, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		parent := metav1.GetControllerOf(replicaSet)
		if parent == nil || parent.Kind != "Deployment" {
			continue
		}
		k := key{pod.Namespace, parent.Name}
		instances[k]++
		if seen[k] {
			continue
		}
		seen[k] = true

		deployment, err := clientset.AppsV1().Deployments(pod.Namespace).Get(ctx, parent.Name, metav1.GetOptions{})
		if err != nil || deployment.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
			continue
		}
		elsewhere, err := readyElsewhere(ctx, clientset, deployment, node)
		if err != nil || elsewhere {
			continue
		}
		patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
			movedAnnotation, time.Now().UTC().Format(time.RFC3339))
		if _, err := clientset.AppsV1().Deployments(pod.Namespace).Patch(ctx, parent.Name,
			types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: "skifity-move"}); err != nil {
			log.Warn("could not start an app elsewhere before emptying its server",
				"namespace", pod.Namespace, "deployment", parent.Name, "error", err)
			continue
		}
		moving = append(moving, k)
	}

	moved := 0
	for _, k := range moving {
		moved += instances[k]
	}
	deadline := time.Now().Add(wait)
	for _, k := range moving {
		for {
			deployment, err := clientset.AppsV1().Deployments(k.namespace).Get(ctx, k.name, metav1.GetOptions{})
			if err != nil || rolledOut(deployment) {
				break
			}
			if time.Now().After(deadline) {
				log.Warn("an app moved off a server being emptied is not ready elsewhere yet; evicting what is left",
					"namespace", k.namespace, "deployment", k.name)
				break
			}
			select {
			case <-ctx.Done():
				return moved
			case <-time.After(3 * time.Second):
			}
		}
	}
	return moved
}

// readyElsewhere reports whether a Deployment has a ready instance on a node
// other than this one, which keeps it answering while its instances here are
// evicted. Its disruption budget makes sure of the rest.
func readyElsewhere(ctx context.Context, clientset kubernetes.Interface, deployment *appsv1.Deployment, node string) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return false, err
	}
	pods, err := clientset.CoreV1().Pods(deployment.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return false, err
	}
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != node && pod.DeletionTimestamp == nil && podReady(pod) {
			return true, nil
		}
	}
	return false, nil
}

// rolledOut reports whether a Deployment's newest instances are all there and
// ready, and the old ones gone.
func rolledOut(d *appsv1.Deployment) bool {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	return d.Status.ObservedGeneration >= d.Generation &&
		d.Status.UpdatedReplicas == want &&
		d.Status.AvailableReplicas == want &&
		d.Status.Replicas == want
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// evict asks for one pod to go, waiting as long as its disruption budget says
// not yet, up to deadline.
//
// A budget answers "not now" while an app is one instance short, and the
// instance being replaced is usually seconds from ready: an app with two
// instances both on the server being emptied had the second eviction refused
// that way, and the removal stopped with "an app would go down", which it
// would not have. A refusal that outlasts the deadline is that, still.
func evict(ctx context.Context, clientset kubernetes.Interface, pod corev1.Pod, deadline time.Time) error {
	eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
	for {
		err := clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
		if err == nil || apierrors.IsNotFound(err) {
			return nil
		}
		if !apierrors.IsTooManyRequests(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(evictRetry):
		}
	}
}
