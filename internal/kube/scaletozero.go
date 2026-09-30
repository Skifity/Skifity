package kube

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/version"
)

// Scale to zero.
//
// KEDA's HTTP add-on puts an interceptor in front of an app: a request for a
// hostname it knows arrives at the interceptor, which wakes the app, holds the
// request until an instance is ready, and forwards it. After a quiet period the
// app goes back to zero instances.
//
// Two objects make that work, and the toggle used to render neither, so
// "scale to zero" installed KEDA and then changed nothing at all.
//
//   - An HTTPScaledObject, which tells the add-on which hostnames belong to
//     which Deployment and how low it may go.
//   - An ExternalName Service in the app's own namespace, because an Ingress
//     cannot name a backend in another namespace and the interceptor lives in
//     KEDA's.

const (
	// KEDANamespace is where the HTTP add-on's interceptor runs.
	KEDANamespace = "keda"
	// KEDAInterceptorService is the add-on's proxy.
	KEDAInterceptorService = "keda-add-ons-http-interceptor-proxy"
	// KEDAInterceptorPort is the port that proxy listens on.
	KEDAInterceptorPort = 8080
	// ScaleDownAfterSeconds is how long an app stays up with no traffic.
	//
	// Five minutes: long enough that a person clicking around does not pay the
	// cold start twice, short enough that an idle app is genuinely free.
	ScaleDownAfterSeconds = 300
)

// InterceptorServiceName is the name of the ExternalName Service an app's
// Ingress points at when the app can scale to zero.
func InterceptorServiceName(appSlug string) string { return ResourceName(appSlug, "wake") }

// ScaleToZeroEnabled reports whether an app both asked for scale to zero and
// can have it.
//
// It needs a hostname: the interceptor routes by Host header, so an app nobody
// can reach by name has nothing to be woken by.
func ScaleToZeroEnabled(s AppSpec) bool {
	return s.ScaleToZero && s.Port > 0 && len(s.Domains) > 0
}

// BuildHTTPScaledObject renders the KEDA object, or nil when the app does not
// scale to zero.
func BuildHTTPScaledObject(s AppSpec) *unstructured.Unstructured {
	if !ScaleToZeroEnabled(s) {
		return nil
	}

	hosts := make([]any, 0, len(s.Domains))
	for _, domain := range s.Domains {
		hosts = append(hosts, domain.Hostname)
	}

	// The ceiling still applies: scale to zero decides the floor, not the roof.
	maxReplicas := s.MaxReplicas
	if !s.Autoscale || maxReplicas < 1 {
		maxReplicas = max(s.Replicas, 1)
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "http.keda.sh/v1alpha1",
		"kind":       "HTTPScaledObject",
		"metadata": map[string]any{
			"name":      s.Name,
			"namespace": s.Namespace,
			"labels":    toAnyMap(s.Labels()),
		},
		"spec": map[string]any{
			"hosts": hosts,
			"scaleTargetRef": map[string]any{
				"name":       s.Name,
				"kind":       "Deployment",
				"apiVersion": "apps/v1",
				"service":    s.Name,
				"port":       int64(80),
			},
			"replicas": map[string]any{
				"min": int64(0),
				"max": int64(maxReplicas),
			},
			"scaledownPeriod": int64(ScaleDownAfterSeconds),
		},
	}}
}

// BuildInterceptorService renders the ExternalName Service the Ingress points
// at, or nil when the app does not scale to zero.
//
// An Ingress backend has to be a Service in the same namespace. This one is an
// alias for KEDA's interceptor, which is the only way to send traffic across a
// namespace boundary without a second ingress controller.
func BuildInterceptorService(s AppSpec) *unstructured.Unstructured {
	if !ScaleToZeroEnabled(s) {
		return nil
	}
	labels := s.Labels()
	labels[version.LabelKey("role")] = "wake"

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      InterceptorServiceName(s.Name),
			"namespace": s.Namespace,
			"labels":    toAnyMap(labels),
		},
		"spec": map[string]any{
			"type":         "ExternalName",
			"externalName": KEDAInterceptorService + "." + KEDANamespace + ".svc.cluster.local",
			// The port has to be the interceptor's own, on both sides.
			//
			// An ExternalName Service is a DNS alias and nothing else: no
			// kube-proxy rule is created for it, so `targetPort` is never
			// applied and the ingress controller connects to whatever number
			// it settles on. Traefik takes the Service's `port`, nginx takes
			// the number in the Ingress backend. This used to say port 80 with
			// targetPort 8080, so every one of them dialled port 80 of the
			// interceptor, which listens on 8080 and nothing else — a sleeping
			// app answered 502 and was never woken. 8080 everywhere is the
			// only value that is right under all three readings.
			"ports": []any{map[string]any{
				"name":       "http",
				"port":       int64(KEDAInterceptorPort),
				"targetPort": int64(KEDAInterceptorPort),
				"protocol":   "TCP",
			}},
		},
	}}
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
