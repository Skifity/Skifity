package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/runsafe"
)

// Reading the ingress's own counters.
//
// k3s installs Traefik from its HelmChart "traefik" in kube-system, and the
// chart turns on Prometheus metrics on an entry point of their own, "metrics",
// on port 9100, at /metrics. Service labels are on by default in Traefik, so
// every service it routes to — every app's Ingress backend — has its own
// request counter and latency histogram there. Checked against k3s's
// manifests/traefik.yaml (chart 41.4.0, Traefik 3.7) and that chart's
// values.yaml: `ports.metrics.port: 9100`, `metrics.prometheus.entryPoint:
// metrics`.
//
// k3s also annotates the pods `prometheus.io/port: "8082"`, which is not where
// the metrics are, so the port is read from the container's port named
// "metrics" — the chart names each container port after its entry point —
// rather than from the annotation.
//
// None of this has run against a real cluster. The listing is tested against a
// fake clientset and the fetch is a function a test replaces; the request
// through the API server's pod proxy needs an API server and a Traefik.

const (
	// TraefikNamespace is where k3s runs its bundled Traefik.
	TraefikNamespace = "kube-system"
	// TraefikSelector finds its pods: the chart's own selector label.
	TraefikSelector = "app.kubernetes.io/name=traefik"
	// TraefikMetricsPort is the chart's default for the metrics entry point,
	// used when a pod does not name its port.
	TraefikMetricsPort = 9100

	// traefikReadTimeout bounds one pod's answer. A pod that does not answer
	// in ten seconds is not going to within the minute.
	traefikReadTimeout = 10 * time.Second
	// traefikMaxMetrics is the most of one answer that is read. Every service,
	// status code, method and bucket is a line; a cluster with hundreds of apps
	// is a few megabytes, and an answer past this is not the format at all.
	traefikMaxMetrics = 32 << 20
)

// TraefikPod is one Traefik pod and what it answered.
type TraefikPod struct {
	Name string
	// Instance changes whenever the process, and so every counter in it,
	// starts again from zero: a new pod, or its container restarted.
	Instance string
	// Started is when the running container started, zero when not known.
	Started time.Time
	// Metrics is the pod's /metrics answer, in the text exposition format.
	Metrics []byte
	// Err is why there is no answer.
	Err error
}

// traefikFetch reads one pod's metrics. A field so a test can answer for a pod
// without an API server behind the fake clientset.
type traefikFetch func(ctx context.Context, pod string, port int) ([]byte, error)

// ScrapeTraefik lists the running Traefik pods and reads each one's metrics, at
// once. A pod that does not answer is in the result with its error rather than
// failing the rest: several nodes run Traefik, and each counts only the
// requests that arrived on it.
func (c *Client) ScrapeTraefik(ctx context.Context) ([]TraefikPod, error) {
	list, err := c.clientset.CoreV1().Pods(TraefikNamespace).List(ctx, metav1.ListOptions{LabelSelector: TraefikSelector})
	if err != nil {
		return nil, err
	}
	fetch := c.fetchTraefik
	if fetch == nil {
		fetch = c.traefikThroughAPIServer
	}

	out := []TraefikPod{}
	ports := []int{}
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}
		instance, started := traefikInstance(pod)
		out = append(out, TraefikPod{Name: pod.Name, Instance: instance, Started: started})
		ports = append(ports, traefikPort(pod))
	}

	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(pod *TraefikPod, port int) {
			defer wg.Done()
			// A pod whose answer trips something costs that pod's minute, not
			// the panel.
			defer runsafe.Recover(nil, "reading Traefik's metrics", func(err error) { pod.Err = err })
			read, cancel := context.WithTimeout(ctx, traefikReadTimeout)
			defer cancel()
			body, err := fetch(read, pod.Name, port)
			if err != nil {
				pod.Err = fmt.Errorf("the Traefik pod %s did not answer on port %d: %w", pod.Name, port, err)
				return
			}
			pod.Metrics = body
		}(&out[i], ports[i])
	}
	wg.Wait()
	return out, nil
}

// traefikThroughAPIServer asks through the API server's pod proxy rather than
// dialling the pod: the panel reaches it the same way inside the cluster and
// from a development machine with a kubeconfig, and no network policy between
// the panel's namespace and kube-system is involved. It never goes through the
// public ingress, which is the thing being measured.
func (c *Client) traefikThroughAPIServer(ctx context.Context, pod string, port int) ([]byte, error) {
	stream, err := c.clientset.CoreV1().RESTClient().Get().
		AbsPath("/api/v1/namespaces", TraefikNamespace, "pods", pod+":"+strconv.Itoa(port), "proxy", "metrics").
		// The text format, which is what the parser reads; Traefik would
		// otherwise pick it anyway for an Accept it does not recognise.
		SetHeader("Accept", "text/plain;version=0.0.4").
		Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	body, err := io.ReadAll(io.LimitReader(stream, traefikMaxMetrics+1))
	if err != nil {
		return nil, err
	}
	if len(body) > traefikMaxMetrics {
		return nil, errors.New("the answer is larger than any metrics page should be")
	}
	return body, nil
}

// traefikInstance names the process whose counters a pod is serving now.
//
// The pod's UID changes when the pod is replaced, and the container's restart
// count when the process inside it restarts; either one means every counter
// started again at zero.
func traefikInstance(pod corev1.Pod) (string, time.Time) {
	for _, status := range pod.Status.ContainerStatuses {
		if !isTraefikContainer(pod, status.Name) {
			continue
		}
		var started time.Time
		if status.State.Running != nil {
			started = status.State.Running.StartedAt.Time
		}
		return string(pod.UID) + "/" + strconv.Itoa(int(status.RestartCount)), started
	}
	return string(pod.UID) + "/?", time.Time{}
}

// traefikPort is the container port named "metrics", or the chart's default.
func traefikPort(pod corev1.Pod) int {
	for _, container := range pod.Spec.Containers {
		if !isTraefikContainer(pod, container.Name) {
			continue
		}
		for _, port := range container.Ports {
			if port.Name == "metrics" && port.ContainerPort > 0 {
				return int(port.ContainerPort)
			}
		}
	}
	return TraefikMetricsPort
}

// isTraefikContainer picks the container named traefik, or the first when none
// is: the chart has one, and a sidecar somebody added is not what counts.
func isTraefikContainer(pod corev1.Pod, name string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == "traefik" {
			return name == "traefik"
		}
	}
	return len(pod.Spec.Containers) > 0 && pod.Spec.Containers[0].Name == name ||
		len(pod.Spec.Containers) == 0
}
