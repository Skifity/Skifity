package cluster

import (
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

const (
	freeHost    = "203-0-113-10.sslip.io"
	panelDomain = "panel.example.test"
)

var certificateResource = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}

// movingCluster is a panel as the installer leaves it with no domain: its
// route over plain HTTP on the sslip.io address, its Deployment started
// there, and cert-manager with its issuer, plus whatever certificate is given.
type movingCluster struct {
	*Cluster
	db        *store.DB
	clientset *fake.Clientset
	dynamic   *dynamicfake.FakeDynamicClient
}

func newMovingCluster(t *testing.T, route *networkingv1.Ingress, objects ...runtime.Object) *movingCluster {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetComponent(t.Context(), store.ClusterComponent{Name: "cert-manager", Status: "installed", InstalledAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	objects = append(objects, installersIssuer(letsEncryptProduction, ""))
	dynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}: "ClusterIssuerList",
		certificateResource: "CertificateList",
	}, objects...)
	clientset := fake.NewSimpleClientset(route, panelDeploymentAt("http://"+freeHost))
	client := kube.NewClientWith(clientset, dynamic, "")
	c := New(client, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &movingCluster{Cluster: c, db: db, clientset: clientset, dynamic: dynamic}
}

// plainRoute is ingress.yaml as the installer applies it, with the
// annotation kubectl leaves.
func plainRoute(host string) *networkingv1.Ingress {
	prefix := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "skifity-panel", Namespace: "skifity-system",
			Labels:      map[string]string{"app.kubernetes.io/name": "skifity"},
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{}"},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &prefix, Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{Name: "skifity-panel", Port: networkingv1.ServiceBackendPort{Name: "http"}},
				}}},
			}},
		}}},
	}
}

// tlsRoute is ingress-tls.yaml as the installer applies it.
func tlsRoute(host string) *networkingv1.Ingress {
	route := plainRoute(host)
	route.Annotations[issuerAnnotation] = ClusterIssuerName
	route.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: "skifity-panel-tls"}}
	return route
}

func panelDeploymentAt(url string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "skifity-panel", Namespace: "skifity-system"},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "panel", Image: "ghcr.io/example/skifity:0.0.0-test",
				Env: []corev1.EnvVar{
					{Name: "SKIFITY_LISTEN", Value: ":8080"},
					{Name: "SKIFITY_PUBLIC_URL", Value: url},
					{Name: "SKIFITY_LOG_FORMAT", Value: "json"},
				},
			}}}},
		},
	}
}

// panelCertificateObject is the Certificate cert-manager makes for the
// route's certificate Secret.
func panelCertificateObject(ready bool, names ...string) *unstructured.Unstructured {
	status := "False"
	if ready {
		status = "True"
	}
	dnsNames := make([]any, 0, len(names))
	for _, name := range names {
		dnsNames = append(dnsNames, name)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]any{"name": "skifity-panel-tls", "namespace": "skifity-system"},
		"spec":       map[string]any{"secretName": "skifity-panel-tls", "dnsNames": dnsNames},
		"status": map[string]any{"conditions": []any{map[string]any{
			"type": "Ready", "status": status, "reason": "Pending",
			"message": "Issuing certificate as Secret does not exist",
		}}},
	}}
}

func (m *movingCluster) route(t *testing.T) *networkingv1.Ingress {
	t.Helper()
	route, err := m.clientset.NetworkingV1().Ingresses("skifity-system").Get(t.Context(), "skifity-panel", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func (m *movingCluster) deployment(t *testing.T) *appsv1.Deployment {
	t.Helper()
	deployment, err := m.clientset.AppsV1().Deployments("skifity-system").Get(t.Context(), "skifity-panel", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return deployment
}

func hostsOf(route *networkingv1.Ingress) []string {
	var hosts []string
	for _, rule := range route.Spec.Rules {
		hosts = append(hosts, rule.Host)
	}
	return hosts
}

func publicURLOf(deployment *appsv1.Deployment) string {
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "SKIFITY_PUBLIC_URL" {
			return env.Value
		}
	}
	return ""
}

// Moving the panel keeps the sslip.io address on its route, touches nothing
// of the panel itself until the certificate is ready, and then changes the
// one variable that restarts the panel at its domain.
func TestThePanelMovesOnlyOnceItsCertificateIsReady(t *testing.T) {
	m := newMovingCluster(t, plainRoute(freeHost), panelCertificateObject(false, panelDomain))
	m.panelWait = 5 * time.Second
	if err := m.db.StartPanelDomain(t.Context(), store.PanelDomain{
		Domain: panelDomain, Previous: []string{freeHost}, State: store.PanelDomainMoving,
	}); err != nil {
		t.Fatal(err)
	}
	before := m.deployment(t)

	done := make(chan error, 1)
	go func() {
		done <- m.SetPanelDomain(t.Context(), api.PanelMove{Domain: panelDomain, Previous: []string{freeHost}})
	}()

	// The route answers at the domain, and still at the free address.
	deadline := time.Now().Add(3 * time.Second)
	for {
		route := m.route(t)
		if len(route.Spec.Rules) > 0 && route.Spec.Rules[0].Host == panelDomain {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the route was never written for the domain: %v", hostsOf(route))
		}
		time.Sleep(10 * time.Millisecond)
	}
	route := m.route(t)
	if !reflect.DeepEqual(hostsOf(route), []string{panelDomain, freeHost}) {
		t.Errorf("while the certificate is on its way the route answers on %v", hostsOf(route))
	}
	if len(route.Spec.TLS) != 1 || route.Spec.TLS[0].SecretName != "skifity-panel-tls" || route.Spec.TLS[0].Hosts[0] != panelDomain {
		t.Errorf("the route's certificate is %+v", route.Spec.TLS)
	}
	if route.Annotations[issuerAnnotation] != ClusterIssuerName || route.Annotations["kubectl.kubernetes.io/last-applied-configuration"] == "" {
		t.Errorf("the route's annotations are %v", route.Annotations)
	}

	// Not ready: the panel is where it was, whatever else has happened.
	time.Sleep(400 * time.Millisecond)
	if publicURLOf(m.deployment(t)) != "http://"+freeHost {
		t.Fatal("the panel was told its new address before its certificate was ready")
	}
	if url, _, _ := m.db.GetSetting(t.Context(), settings.KeyPanelURL); url != "" {
		t.Fatalf("the Panel URL was set to %q before the certificate was ready", url)
	}
	select {
	case err := <-done:
		t.Fatalf("the move ended before the certificate was ready: %v", err)
	default:
	}

	if _, err := m.dynamic.Resource(certificateResource).Namespace("skifity-system").Update(t.Context(),
		panelCertificateObject(true, panelDomain), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the move failed once the certificate was ready: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the move did not finish once the certificate was ready")
	}

	after := m.deployment(t)
	if got := publicURLOf(after); got != "https://"+panelDomain {
		t.Errorf("the panel was told %q", got)
	}
	// Nothing else about the panel changed: put the old address back and
	// the two are the same.
	for i := range after.Spec.Template.Spec.Containers[0].Env {
		if after.Spec.Template.Spec.Containers[0].Env[i].Name == "SKIFITY_PUBLIC_URL" {
			after.Spec.Template.Spec.Containers[0].Env[i].Value = "http://" + freeHost
		}
	}
	if !reflect.DeepEqual(before.Spec, after.Spec) {
		t.Errorf("more than the panel's address changed:\nbefore %+v\nafter  %+v", before.Spec, after.Spec)
	}
	if !reflect.DeepEqual(hostsOf(m.route(t)), []string{panelDomain, freeHost}) {
		t.Errorf("after the move the route answers on %v", hostsOf(m.route(t)))
	}
	if url, _, _ := m.db.GetSetting(t.Context(), settings.KeyPanelURL); url != "https://"+panelDomain {
		t.Errorf("the Panel URL is %q", url)
	}
	if row, _ := m.db.GetPanelDomain(t.Context()); row.State != store.PanelDomainRestarting {
		t.Errorf("the move is recorded as %q, not restarting", row.State)
	}
}

// A certificate that never comes stops the move with what cert-manager said,
// and the panel is left exactly where it was.
func TestAMoveWhoseCertificateNeverComesLeavesThePanelAlone(t *testing.T) {
	m := newMovingCluster(t, plainRoute(freeHost), panelCertificateObject(false, panelDomain))
	m.panelWait = 200 * time.Millisecond
	before := m.deployment(t)

	err := m.SetPanelDomain(t.Context(), api.PanelMove{Domain: panelDomain, Previous: []string{freeHost}})
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "panel.domain_certificate_pending" {
		t.Fatalf("a certificate that never came answered %v", err)
	}
	if problem.Args.Cause[1] != "Issuing certificate as Secret does not exist" {
		t.Errorf("the problem does not say what cert-manager said: %q", problem.Cause)
	}
	if after := m.deployment(t); !reflect.DeepEqual(before.Spec, after.Spec) {
		t.Error("the panel's Deployment was changed by a move that did not get its certificate")
	}
	if url, _, _ := m.db.GetSetting(t.Context(), settings.KeyPanelURL); url != "" {
		t.Errorf("the Panel URL was set to %q", url)
	}
	// The route keeps asking, and keeps answering where the panel is.
	if !reflect.DeepEqual(hostsOf(m.route(t)), []string{panelDomain, freeHost}) {
		t.Errorf("the route answers on %v", hostsOf(m.route(t)))
	}
}

// A Ready certificate for the domain before it is a stale one: the last
// domain's, which cert-manager has not caught up with yet.
func TestALastDomainsCertificateIsNotTheNewOnes(t *testing.T) {
	stale := panelCertificateObject(true, "old.example.test")
	if got := certificateFor(stale, panelDomain); got.State != api.PanelCertificateIssuing {
		t.Errorf("the last domain's certificate read as %q", got.State)
	}
	behind := panelCertificateObject(true, panelDomain)
	behind.SetGeneration(3)
	conditions, _, _ := unstructured.NestedSlice(behind.Object, "status", "conditions")
	conditions[0].(map[string]any)["observedGeneration"] = int64(2)
	_ = unstructured.SetNestedSlice(behind.Object, conditions, "status", "conditions")
	if got := certificateFor(behind, panelDomain); got.State != api.PanelCertificateIssuing {
		t.Errorf("a Ready from before the certificate changed read as %q", got.State)
	}
	ready := panelCertificateObject(true, panelDomain)
	_ = unstructured.SetNestedField(ready.Object, "2027-02-01T00:00:00Z", "status", "notAfter")
	if got := certificateFor(ready, panelDomain); got.State != api.PanelCertificateReady || got.NotAfter == nil {
		t.Errorf("a ready certificate read as %+v", got)
	}
}

// Going back to the free address replaces the route, the certificate and
// cert-manager's annotation included, which a server-side apply over the
// installer's kubectl would have left, and restarts the panel there.
func TestTakingTheDomainOffPutsThePanelBackOnItsFreeAddress(t *testing.T) {
	m := newMovingCluster(t, tlsRoute(panelDomain))
	err := m.ReturnPanel(t.Context(), freeHost, api.PanelMove{Domain: panelDomain})
	if err != nil {
		t.Fatal(err)
	}
	route := m.route(t)
	if !reflect.DeepEqual(hostsOf(route), []string{freeHost}) || len(route.Spec.TLS) != 0 {
		t.Errorf("the route answers on %v with certificates %+v", hostsOf(route), route.Spec.TLS)
	}
	if _, ok := route.Annotations[issuerAnnotation]; ok {
		t.Error("the route still asks cert-manager for a certificate")
	}
	if got := publicURLOf(m.deployment(t)); got != "http://"+freeHost {
		t.Errorf("the panel was told %q", got)
	}
	if url, _, _ := m.db.GetSetting(t.Context(), settings.KeyPanelURL); url != "http://"+freeHost {
		t.Errorf("the Panel URL is %q", url)
	}
}

// Running the installer again writes the route for the domain alone; the
// panel puts the addresses it keeps back, and leaves a route that is not its
// domain's alone.
func TestTheAddressesThePanelKeepsGoBackOnItsRoute(t *testing.T) {
	m := newMovingCluster(t, tlsRoute(panelDomain))
	move := api.PanelMove{Domain: panelDomain, Previous: []string{freeHost, "old.example.test"}}
	restored, err := m.KeepPanelRoute(t.Context(), move)
	if err != nil || !restored {
		t.Fatalf("the lost addresses were not put back: %v %v", restored, err)
	}
	route := m.route(t)
	if !reflect.DeepEqual(hostsOf(route), []string{panelDomain, freeHost, "old.example.test"}) {
		t.Errorf("the route answers on %v", hostsOf(route))
	}
	secrets := map[string]string{}
	for _, entry := range route.Spec.TLS {
		secrets[entry.Hosts[0]] = entry.SecretName
	}
	if secrets[panelDomain] != "skifity-panel-tls" || secrets["old.example.test"] != "skifity-panel-tls-old-example-test" || secrets[freeHost] != "" {
		t.Errorf("the route's certificates are %v", secrets)
	}
	if restored, err := m.KeepPanelRoute(t.Context(), move); err != nil || restored {
		t.Errorf("a route with everything on it was written again: %v %v", restored, err)
	}

	elsewhere := newMovingCluster(t, tlsRoute("other.example.test"))
	if restored, err := elsewhere.KeepPanelRoute(t.Context(), move); err != nil || restored {
		t.Errorf("a route for another domain was changed: %v %v", restored, err)
	}
	if hosts := hostsOf(elsewhere.route(t)); !reflect.DeepEqual(hosts, []string{"other.example.test"}) {
		t.Errorf("the other domain's route answers on %v", hosts)
	}
}

// A panel with no route of the installer's cannot move itself, and says so.
func TestAPanelWithoutTheInstallersRouteCannotMove(t *testing.T) {
	m := newMovingCluster(t, plainRoute(freeHost))
	if err := m.clientset.NetworkingV1().Ingresses("skifity-system").Delete(t.Context(), "skifity-panel", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := m.PanelRoute(t.Context())
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "panel.domain_unsupported" {
		t.Errorf("a panel with no route answered %v", err)
	}
	if err := m.SetPanelDomain(t.Context(), api.PanelMove{Domain: panelDomain}); !errors.As(err, &problem) || problem.Code != "panel.domain_unsupported" {
		t.Errorf("moving a panel with no route answered %v", err)
	}
}
