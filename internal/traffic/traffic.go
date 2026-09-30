// Package traffic counts the requests that reach each app, from the ingress's
// own counters.
//
// k3s runs Traefik as its ingress, and Traefik keeps a Prometheus counter of
// requests and a histogram of how long they took for every service it routes
// to. An app's Ingress backend is one of those services. The watcher reads
// every Traefik pod once a minute (kube.ScrapeTraefik), and this package turns
// two reads into one minute per app: how many requests, how they were
// answered, and the median and 95th percentile of how long they took.
//
// Everything here is a function of its arguments and a little remembered
// state, so the parts that are expensive to get wrong — the parser, which
// service is which app, counters that start again, several pods, the
// percentile — are tested without a cluster. The read itself is not: it needs
// an API server and a Traefik, and has not run against either.
package traffic

import (
	"strconv"
	"strings"
	"unicode"

	"skifity/internal/kube"
)

// The states the ingress can be in, as far as reading it goes. They are what
// the panel says when an app's requests are not drawn.
const (
	// StateNoCluster is a panel with no cluster to read.
	StateNoCluster = "no_cluster"
	// StateStarting is before the second read: the first is only something to
	// compare the next with.
	StateStarting = "starting"
	// StateReading is the last read counted something.
	StateReading = "reading"
	// StateNoTraefik is a cluster with no Traefik pod in kube-system — k3s
	// started with --disable traefik, or another ingress.
	StateNoTraefik = "no_traefik"
	// StateUnreachable is Traefik running and none of its pods answering on
	// the metrics port, or the cluster not answering the listing.
	StateUnreachable = "unreachable"
)

// provider is the suffix Traefik's Kubernetes Ingress provider gives the
// services it creates, as they appear in its metrics.
const provider = "@kubernetes"

// appServicePort is the port kube.BuildIngress sends an app's traffic to on
// its own Service. A test renders the Ingress and holds the two together.
const appServicePort = 80

// ServiceNames are the names Traefik gives the services an app's Ingress can
// route to, as they appear in the service label of its metrics.
//
// Traefik names an Ingress backend <namespace>-<service>-<port>, the port being
// the backend's port name, or its number when it has none — kube.BuildIngress
// always gives a number — passed through its Normalize and followed by
// @kubernetes. That is traefik/traefik pkg/provider/kubernetes/ingress
// (serviceName := provider.Normalize(ingress.Namespace + "-" +
// pa.Backend.Service.Name + "-" + portString)), and the metrics carry the
// qualified name, "whoami@docker" in Traefik's own integration tests.
//
// An app's Ingress points at one of two Services:
//
//   - its own Service, on port 80, normally;
//   - the ExternalName alias for KEDA's interceptor, on 8080, when it can
//     scale to zero (kube.BuildInterceptorService). The interceptor forwards
//     to the app, so what it answers is what the app's visitors got,
//     including a wake that took too long.
//
// Both are returned rather than the one the app's settings point at now: the
// setting can change between deploys, and a counter nothing routes to any more
// does not grow, so counting both costs nothing.
//
// The firewall does not change either name. It is a forwardAuth middleware on
// the Ingress's router, not a backend, and a request it refuses — or answers
// with the maintenance page, or with a password prompt — never reaches the
// service, so it is not counted. What is counted is what reached the app.
func ServiceNames(namespace, slug string) []string {
	return []string{
		traefikServiceName(namespace, slug, appServicePort),
		traefikServiceName(namespace, kube.InterceptorServiceName(slug), kube.KEDAInterceptorPort),
	}
}

func traefikServiceName(namespace, service string, port int) string {
	return normalize(namespace+"-"+service+"-"+strconv.Itoa(port)) + provider
}

// normalize is Traefik's provider.Normalize: every run of characters that is
// neither a letter nor a digit becomes one dash, and there are none at the
// ends. Kubernetes names are already nearly that, except that they may hold two
// dashes in a row, which Traefik folds into one.
func normalize(name string) string {
	return strings.Join(strings.FieldsFunc(name, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	}), "-")
}

// App is what the index needs to know about an app.
type App struct {
	ID, Namespace, Slug string
}

// Index finds the app a Traefik service belongs to.
type Index struct {
	apps map[string]string
}

// NewIndex indexes apps by their Traefik service names. It also returns the
// apps that share a name with another app, which are counted for neither.
//
// Two apps can: namespace "acme-shop" with an app "prod-web" and namespace
// "acme" with an app "shop-prod-web" both come out as acme-shop-prod-web-80.
// Traefik cannot tell them apart either, and one tenant's requests drawn on
// another tenant's page is worse than no numbers.
func NewIndex(apps []App) (Index, map[string]bool) {
	owner := map[string]string{}
	shared := map[string]bool{}
	for _, app := range apps {
		for _, name := range ServiceNames(app.Namespace, app.Slug) {
			if other, taken := owner[name]; taken && other != app.ID {
				shared[other], shared[app.ID] = true, true
				continue
			}
			owner[name] = app.ID
		}
	}
	index := Index{apps: map[string]string{}}
	for name, id := range owner {
		if !shared[id] {
			index.apps[name] = id
		}
	}
	return index, shared
}

// AppFor returns the app a Traefik service belongs to.
func (x Index) AppFor(service string) (string, bool) {
	id, ok := x.apps[service]
	return id, ok
}
