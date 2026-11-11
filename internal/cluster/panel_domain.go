package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/manifests"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Moving the panel to a domain of its own: the cluster's half. The API's half,
// and why it is done this way, is in internal/api/panel_domain_handlers.go.

const (
	// panelDeployment is the panel's own Deployment, whose address is changed
	// at the end of a move.
	panelDeployment = "skifity-panel"
	// publicURLVariable is the one thing about it a move changes.
	publicURLVariable = "SKIFITY_PUBLIC_URL"
	// issuerAnnotation is how the panel's route asks cert-manager for its
	// certificate.
	issuerAnnotation = "cert-manager.io/cluster-issuer"

	// panelCertificateWait is how long a move waits for the domain's
	// certificate. Let's Encrypt takes under a minute once DNS is right; the
	// installer gives it two.
	panelCertificateWait = 5 * time.Minute
	panelCertificatePoll = 3 * time.Second
)

var _ api.PanelMover = (*Cluster)(nil)

// PanelRoute reads the panel's own Ingress and the certificate of its first
// host.
func (c *Cluster) PanelRoute(ctx context.Context) (api.PanelRoute, error) {
	if c.client == nil {
		return api.PanelRoute{}, errdoc.ClusterUnreachable(nil)
	}
	ingress, err := c.panelIngress(ctx)
	if err != nil {
		return api.PanelRoute{}, err
	}
	route := api.PanelRoute{Secrets: map[string]string{}}
	for _, rule := range ingress.Spec.Rules {
		if rule.Host != "" && !slices.Contains(route.Hosts, rule.Host) {
			route.Hosts = append(route.Hosts, rule.Host)
		}
	}
	for _, entry := range ingress.Spec.TLS {
		for _, host := range entry.Hosts {
			route.Secrets[host] = entry.SecretName
		}
	}
	route.Certificate = api.PanelCertificate{State: api.PanelCertificateNone}
	if len(route.Hosts) > 0 && route.Secrets[route.Hosts[0]] != "" {
		route.Certificate, err = c.panelCertificate(ctx, route.Hosts[0])
		if err != nil {
			return api.PanelRoute{}, err
		}
	}
	return route, nil
}

// panelIngress is the panel's route as it is, or why the panel cannot move
// itself.
func (c *Cluster) panelIngress(ctx context.Context) (*networkingv1.Ingress, error) {
	ingress, err := c.client.Clientset().NetworkingV1().Ingresses(c.client.SystemNamespace()).
		Get(ctx, manifests.PanelRouteName, metav1.GetOptions{})
	switch {
	case err == nil:
		return ingress, nil
	case kube.IsNotFound(err):
		return nil, errdoc.PanelDomainUnsupported(fmt.Sprintf("The panel's own route, the Ingress %s in %s, is not there: "+
			"this panel was not installed by install.sh, or its route was renamed.", manifests.PanelRouteName, c.client.SystemNamespace()))
	case kube.IsUnreachable(err):
		return nil, errdoc.ClusterUnreachable(err)
	default:
		return nil, err
	}
}

// panelCertificate is the panel's certificate as cert-manager has it, for a
// host. A certificate for another host is one still being changed to this
// one: cert-manager updates it from the route a moment after the route
// changes, and until then it is the last domain's, ready or not.
func (c *Cluster) panelCertificate(ctx context.Context, host string) (api.PanelCertificate, error) {
	none := api.PanelCertificate{State: api.PanelCertificateNone}
	certificate, err := c.client.Applier().Get(ctx, "cert-manager.io/v1", "Certificate",
		c.client.SystemNamespace(), manifests.PanelCertificateSecret)
	switch {
	case kube.IsNotFound(err) || meta.IsNoMatchError(err):
		return none, nil
	case kube.IsUnreachable(err):
		return none, errdoc.ClusterUnreachable(err)
	case err != nil:
		return none, err
	}
	return certificateFor(certificate, host), nil
}

// certificateFor reads a cert-manager Certificate for one host.
func certificateFor(certificate *unstructured.Unstructured, host string) api.PanelCertificate {
	out := api.PanelCertificate{State: api.PanelCertificateIssuing}
	names, _, _ := unstructured.NestedStringSlice(certificate.Object, "spec", "dnsNames")
	conditions, _, _ := unstructured.NestedSlice(certificate.Object, "status", "conditions")
	ready, current := false, true
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		ready = condition["status"] == "True"
		out.Message, _ = condition["message"].(string)
		// A Ready from before the route changed is the last domain's.
		if observed := generation(condition["observedGeneration"]); observed != 0 && observed != certificate.GetGeneration() {
			current = false
		}
	}
	if !slices.Contains(names, host) {
		out.Message = ""
		return out
	}
	if ready && current {
		out.State, out.Message = api.PanelCertificateReady, ""
		if expiry, _, _ := unstructured.NestedString(certificate.Object, "status", "notAfter"); expiry != "" {
			if at, err := time.Parse(time.RFC3339, expiry); err == nil {
				out.NotAfter = &at
			}
		}
	}
	return out
}

// generation reads a generation however the JSON it came from was decoded.
func generation(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case float64:
		return int64(number)
	}
	return 0
}

// SetPanelDomain moves the panel to a domain: cert-manager and its issuer,
// the route, the certificate, then the panel's address, which restarts it.
//
// Until the certificate is there nothing about the panel itself changes, and
// the address it answered on stays on the route throughout: a move that
// stops leaves the panel answering where it did.
func (c *Cluster) SetPanelDomain(ctx context.Context, move api.PanelMove) error {
	if c.client == nil {
		return errdoc.ClusterUnreachable(nil)
	}
	if _, err := c.panelIngress(ctx); err != nil {
		return err
	}
	if err := c.EnsureComponent(ctx, "cert-manager"); err != nil {
		return errdoc.PanelDomainMoveFailed(move.Domain, fmt.Errorf("install cert-manager: %w", err))
	}
	if err := c.ensurePanelIssuer(ctx); err != nil {
		return errdoc.PanelDomainMoveFailed(move.Domain, fmt.Errorf("make the certificate issuer: %w", err))
	}
	if err := c.WritePanelRoute(ctx, move.Domain, true, move.Previous); err != nil {
		return errdoc.PanelDomainMoveFailed(move.Domain, fmt.Errorf("write the panel's route: %w", err))
	}
	c.log.Info("the panel's route answers at its domain; waiting for the certificate", "domain", move.Domain)
	if err := c.waitForPanelCertificate(ctx, move.Domain); err != nil {
		return err
	}

	// The certificate is there. Everything from here is the panel changing
	// its own address, and the last step restarts it.
	url := "https://" + move.Domain
	restoreURL, err := c.setPanelURL(ctx, url)
	if err != nil {
		return errdoc.PanelDomainMoveFailed(move.Domain, err)
	}
	if err := c.db.SetPanelDomainState(ctx, store.PanelDomainRestarting, nil); err != nil && !errors.Is(err, store.ErrNotFound) {
		restoreURL()
		return errdoc.PanelDomainMoveFailed(move.Domain, err)
	}
	if err := c.setPanelPublicURL(ctx, url); err != nil {
		restoreURL()
		return errdoc.PanelDomainMoveFailed(move.Domain, fmt.Errorf("restart the panel at %s: %w", url, err))
	}
	c.log.Info("the panel's certificate is ready, and the panel is restarting at its domain", "url", url)
	return nil
}

// ReturnPanel puts the panel back on a host over plain HTTP, and restarts it
// there. The domain goes off the route at once; if the panel cannot be told
// its address, current goes back on.
func (c *Cluster) ReturnPanel(ctx context.Context, host string, current api.PanelMove) error {
	if c.client == nil {
		return errdoc.ClusterUnreachable(nil)
	}
	if err := c.WritePanelRoute(ctx, host, false, nil); err != nil {
		return err
	}
	url := "http://" + host
	restoreURL, err := c.setPanelURL(ctx, url)
	if err == nil {
		if err = c.setPanelPublicURL(ctx, url); err != nil {
			restoreURL()
		}
	}
	if err != nil {
		if again := c.WritePanelRoute(ctx, current.Domain, true, current.Previous); again != nil {
			c.log.Warn("could not put the panel's route back after failing to move it", "error", again)
		}
		return fmt.Errorf("restart the panel at %s: %w", url, err)
	}
	c.log.Info("the panel's domain was taken off, and the panel is restarting at its free address", "url", url)
	return nil
}

// KeepPanelRoute puts the hosts the panel left back on its route, when the
// route still starts with its domain and lost any of them. Running the
// installer again is what takes them off: it applies its route for the
// domain alone.
func (c *Cluster) KeepPanelRoute(ctx context.Context, move api.PanelMove) (bool, error) {
	if c.client == nil {
		return false, nil
	}
	route, err := c.PanelRoute(ctx)
	if err != nil {
		return false, err
	}
	if len(route.Hosts) == 0 || route.Hosts[0] != move.Domain {
		return false, nil
	}
	missing := false
	for _, host := range move.Previous {
		if !slices.Contains(route.Hosts, host) {
			missing = true
		}
	}
	if !missing {
		return false, nil
	}
	return true, c.WritePanelRoute(ctx, move.Domain, true, move.Previous)
}

// WritePanelRoute writes the panel's Ingress for a host: from ingress-tls.yaml
// with a certificate, or ingress.yaml without, and a rule for each of the
// hosts the panel answered on before.
//
// The object is replaced rather than applied. The installer applies its route
// with kubectl, which owns what it wrote, and a server-side apply leaves what
// another owner set: the certificate of a domain taken off would have stayed.
func (c *Cluster) WritePanelRoute(ctx context.Context, host string, tls bool, previous []string) error {
	namespace := c.client.SystemNamespace()
	var rendered map[string]any
	var err error
	if tls {
		issuer, issuerErr := c.CertificateIssuer(ctx)
		if issuerErr != nil {
			return issuerErr
		}
		if issuer == "" {
			issuer = ClusterIssuerName
		}
		rendered, err = manifests.PanelRoute(namespace, host, issuer, panelHosts(previous)...)
	} else {
		rendered, err = manifests.PlainPanelRoute(namespace, host)
	}
	if err != nil {
		return err
	}
	data, err := json.Marshal(rendered)
	if err != nil {
		return err
	}
	var want networkingv1.Ingress
	if err := json.Unmarshal(data, &want); err != nil {
		return fmt.Errorf("read the panel's route: %w", err)
	}

	ingresses := c.client.Clientset().NetworkingV1().Ingresses(namespace)
	live, err := ingresses.Get(ctx, manifests.PanelRouteName, metav1.GetOptions{})
	if kube.IsNotFound(err) {
		_, err = ingresses.Create(ctx, &want, metav1.CreateOptions{FieldManager: kube.FieldManager})
		return err
	}
	if err != nil {
		return err
	}
	live.Spec = want.Spec
	if live.Labels == nil {
		live.Labels = map[string]string{}
	}
	for key, value := range want.Labels {
		live.Labels[key] = value
	}
	if live.Annotations == nil {
		live.Annotations = map[string]string{}
	}
	delete(live.Annotations, issuerAnnotation)
	for key, value := range want.Annotations {
		live.Annotations[key] = value
	}
	_, err = ingresses.Update(ctx, live, metav1.UpdateOptions{FieldManager: kube.FieldManager})
	return err
}

// panelHosts says, for each host the panel answered on before, whether it
// keeps a certificate: an sslip.io address never had one (ADR-0015), and a
// domain the installer put the panel on did, and keeps it, in a Secret of its
// own, so a webhook registered at its https:// address still arrives.
func panelHosts(previous []string) []manifests.PanelHost {
	hosts := make([]manifests.PanelHost, 0, len(previous))
	for _, host := range previous {
		entry := manifests.PanelHost{Host: host}
		if !manifests.FreeAddress(host) {
			entry.Secret = manifests.PreviousCertificateSecret(host)
		}
		hosts = append(hosts, entry)
	}
	return hosts
}

// ensurePanelIssuer makes the certificate issuer when cert-manager is there
// and the issuer is not: installed by hand, or the issuer removed. cert-manager's
// webhook admits an issuer a moment after it says it is ready.
func (c *Cluster) ensurePanelIssuer(ctx context.Context) error {
	issuer, err := c.CertificateIssuer(ctx)
	if err != nil || issuer != "" {
		return err
	}
	return c.retry(ctx, 40, 3*time.Second, func() error { return c.EnsureClusterIssuer(ctx) })
}

// waitForPanelCertificate waits for the certificate of the panel's domain, or
// says what cert-manager said last.
func (c *Cluster) waitForPanelCertificate(ctx context.Context, domain string) error {
	wait, poll := panelCertificateWait, panelCertificatePoll
	if c.panelWait > 0 {
		wait, poll = c.panelWait, c.panelWait/20
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	said := "nothing yet"
	for {
		certificate, err := c.panelCertificate(ctx, domain)
		if err == nil && certificate.State == api.PanelCertificateReady {
			return nil
		}
		if err == nil && certificate.Message != "" {
			said = certificate.Message
		}
		select {
		case <-ctx.Done():
			return errdoc.PanelCertificatePending(domain, said)
		case <-deadline.C:
			return errdoc.PanelCertificatePending(domain, said)
		case <-time.After(poll):
		}
	}
}

// setPanelURL writes the Panel URL setting, which links, webhooks and passkeys
// read, and hands back what puts the one before it back.
func (c *Cluster) setPanelURL(ctx context.Context, url string) (func(), error) {
	before, _, err := c.db.GetSetting(ctx, settings.KeyPanelURL)
	if err != nil {
		return nil, err
	}
	if err := c.db.SetSetting(ctx, settings.KeyPanelURL, url, false, "system"); err != nil {
		return nil, err
	}
	return func() {
		ctx := context.WithoutCancel(ctx)
		var err error
		if before == "" {
			err = c.db.DeleteSetting(ctx, settings.KeyPanelURL)
		} else {
			err = c.db.SetSetting(ctx, settings.KeyPanelURL, before, false, "system")
		}
		if err != nil {
			c.log.Warn("could not put the Panel URL setting back", "error", err)
		}
	}, nil
}

// setPanelPublicURL changes SKIFITY_PUBLIC_URL on the panel's Deployment, and
// nothing else, which restarts the panel: its cookies and its links follow
// that address, and are read once, at start-up.
//
// A strategic merge patch of that one variable rather than an update of the
// whole Deployment: the installer owns the rest of it, and its next run reads
// the route to work out the same address.
func (c *Cluster) setPanelPublicURL(ctx context.Context, url string) error {
	deployments := c.client.Clientset().AppsV1().Deployments(c.client.SystemNamespace())
	deployment, err := deployments.Get(ctx, panelDeployment, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("find the panel's own Deployment: %w", err)
	}
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("the panel's Deployment has no containers")
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{
				"name": deployment.Spec.Template.Spec.Containers[0].Name,
				"env":  []any{map[string]any{"name": publicURLVariable, "value": url}},
			}},
		}}},
	})
	if err != nil {
		return err
	}
	_, err = deployments.Patch(ctx, panelDeployment, types.StrategicMergePatchType, patch,
		metav1.PatchOptions{FieldManager: kube.FieldManager})
	return err
}
