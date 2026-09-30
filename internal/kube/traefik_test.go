package kube

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// Which Traefik pods are asked, on which port, and what says their counters
// started again. The request itself goes through the API server's pod proxy
// and needs a real cluster; here the fetch is answered by the test.
func TestTraefikIsAskedOnItsMetricsPortAndRestartsAreSeen(t *testing.T) {
	started := time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)
	traefik := func(name, uid string, phase corev1.PodPhase, restarts int32, ports ...corev1.ContainerPort) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: TraefikNamespace, UID: types.UID(uid),
				Labels: map[string]string{"app.kubernetes.io/name": "traefik"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "traefik", Ports: ports}}},
			Status: corev1.PodStatus{Phase: phase, PodIP: "10.42.0.9", ContainerStatuses: []corev1.ContainerStatus{{
				Name: "traefik", RestartCount: restarts,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(started)}},
			}}},
		}
	}
	notTraefik := traefik("coredns-1", "uid-dns", corev1.PodRunning, 0)
	notTraefik.Labels = map[string]string{"k8s-app": "kube-dns"}

	c := &Client{clientset: fake.NewSimpleClientset(
		// The chart's ports: the metrics entry point is named, and 8082 in
		// k3s's annotation is not where it is.
		traefik("traefik-a", "uid-a", corev1.PodRunning, 2,
			corev1.ContainerPort{Name: "web", ContainerPort: 8000},
			corev1.ContainerPort{Name: "metrics", ContainerPort: 9100}),
		// An operator moved the metrics entry point.
		traefik("traefik-b", "uid-b", corev1.PodRunning, 0, corev1.ContainerPort{Name: "metrics", ContainerPort: 9200}),
		// No named port: the chart's default.
		traefik("traefik-c", "uid-c", corev1.PodRunning, 0),
		traefik("traefik-pending", "uid-p", corev1.PodPending, 0),
		notTraefik,
	)}
	var mu sync.Mutex
	asked := map[string]int{}
	c.fetchTraefik = func(_ context.Context, pod string, port int) ([]byte, error) {
		mu.Lock()
		asked[pod] = port
		mu.Unlock()
		if pod == "traefik-c" {
			return nil, errors.New("connection refused")
		}
		return []byte("traefik_service_requests_total 1\n"), nil
	}

	pods, err := c.ScrapeTraefik(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 3 || asked["traefik-a"] != 9100 || asked["traefik-b"] != 9200 || asked["traefik-c"] != TraefikMetricsPort {
		t.Fatalf("asked %v", asked)
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	if len(pods) != 3 {
		t.Fatalf("%d pods", len(pods))
	}
	a := pods[0]
	if a.Instance != "uid-a/2" || !a.Started.Equal(started) || string(a.Metrics) == "" || a.Err != nil {
		t.Fatalf("traefik-a is %+v", a)
	}
	// The same pod after another restart is another process.
	if next, _ := traefikInstance(*traefik("traefik-a", "uid-a", corev1.PodRunning, 3)); next == a.Instance {
		t.Fatal("a restarted container reads as the same process")
	}
	if c := pods[2]; c.Err == nil || c.Metrics != nil {
		t.Fatalf("a pod that did not answer is %+v", c)
	}
}
