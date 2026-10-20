package cluster

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// ClusterIssuerName is the cert-manager ClusterIssuer apps' certificates come
// from. The installer makes it for a panel installed with a domain, and
// installing HTTPS certificates under Components makes it otherwise; both
// give it this name.
const ClusterIssuerName = "skifity-letsencrypt"

// letsEncryptProduction is where certificates come from when no ACME server
// is set.
const letsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"

// solverServiceType is the kind of Service cert-manager makes for the pod
// that answers an HTTP-01 challenge, in the namespace of the app the
// certificate is for. Left to itself it makes a NodePort, and every
// environment's quota allows no NodePort (kube.BuildResourceQuota), so the
// Service was refused and no app's certificate could ever be asked for: the
// first run with an ACME server to ask showed it. The ingress reaches a
// ClusterIP just as well.
const solverServiceType = "ClusterIP"

// CertificateIssuer is the issuer an app's certificates are asked of: the
// panel's, when it exists, whoever made it. Empty when there is none, because
// cert-manager is not installed or has no issuer yet. A name that is not
// there is never written into an Ingress: cert-manager would keep the
// certificate pending for it forever.
//
// It used to follow the ACME email setting, which the installer never
// writes. On a server installed with a domain the issuer was there, and every
// app's own domain still waited for a certificate nothing had asked for.
func (c *Cluster) CertificateIssuer(ctx context.Context) (string, error) {
	if c.client == nil {
		return "", nil
	}
	_, err := c.client.Applier().Get(ctx, "cert-manager.io/v1", "ClusterIssuer", "", ClusterIssuerName)
	switch {
	case err == nil:
		return ClusterIssuerName, nil
	case kube.IsNotFound(err) || meta.IsNoMatchError(err):
		return "", nil
	case kube.IsUnreachable(err):
		return "", errdoc.ClusterUnreachable(err)
	default:
		return "", err
	}
}

// EnsureClusterIssuer creates or updates the issuer from the settings under
// Domains and HTTPS: the ACME server, Let's Encrypt's production one when none
// is set; the email, when there is one; and the authority that signs the
// server's own address, for an ACME server of one's own.
//
// The email used to be required, and installing HTTPS certificates failed
// without one. Let's Encrypt issues certificates without it, and the installer
// never asked for one.
func (c *Cluster) EnsureClusterIssuer(ctx context.Context) error {
	email, err := c.plainSetting(ctx, settings.KeyACMEEmail)
	if err != nil {
		return err
	}
	server, err := c.plainSetting(ctx, settings.KeyACMEServer)
	if err != nil {
		return err
	}
	if server == "" {
		server = letsEncryptProduction
	}
	authority, err := c.plainSetting(ctx, settings.KeyACMECABundle)
	if err != nil {
		return err
	}

	acme := map[string]any{
		"server": server,
		"privateKeySecretRef": map[string]any{
			"name": ClusterIssuerName + "-account-key",
		},
		"solvers": []any{map[string]any{
			// HTTP-01 works for anyone with a domain pointing at the
			// cluster, with no DNS credentials to configure. The solver's
			// Service is a ClusterIP; see solverServiceType.
			"http01": map[string]any{
				"ingress": map[string]any{"class": "traefik", "serviceType": solverServiceType},
			},
		}},
	}
	if email != "" {
		acme["email"] = email
	}
	if authority != "" {
		acme["caBundle"] = base64.StdEncoding.EncodeToString([]byte(authority))
	}
	return c.client.Applier().Apply(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata": map[string]any{
			"name":   ClusterIssuerName,
			"labels": map[string]any{"app.kubernetes.io/managed-by": version.Binary},
		},
		"spec": map[string]any{"acme": acme},
	}})
}

// RefreshClusterIssuer writes the settings under Domains and HTTPS into the
// issuer, when there is one.
//
// Saving them used to change rows in SQLite and nothing else: the issuer was
// written once, when HTTPS certificates were installed, so pointing the panel
// at Let's Encrypt's staging server to test, and back again, did nothing
// either way. Where there is no issuer yet it does nothing, so saving is not
// an install by the back door: installing HTTPS certificates makes one, from
// these same settings.
func (c *Cluster) RefreshClusterIssuer(ctx context.Context) error {
	issuer, err := c.CertificateIssuer(ctx)
	if err != nil || issuer == "" {
		return err
	}
	return c.EnsureClusterIssuer(ctx)
}

// AdoptClusterIssuer records the issuer the installer made as the panel's own
// settings: its ACME server, its email and the authority its server's address
// is signed by, each only where the setting is still empty, so an operator's
// own answer stands, the same as the address the installer found.
//
// Read from settings alone, the page said Let's Encrypt's production server
// for a panel the installer had pointed at the staging one, and the first
// change saved there would have moved it to production with nobody choosing
// that.
//
// The installer installs cert-manager itself, too, and the list of components
// did not know: it offered to install what was already running. That is
// recorded here as well, at the version it runs.
//
// And an issuer made before the challenge's Service was a ClusterIP, by the
// installer or by the panel, still asks for a NodePort, which no environment
// allows (solverServiceType). It is written again from the settings just
// recorded, so a server upgraded by any means gets certificates for its apps.
func (c *Cluster) AdoptClusterIssuer(ctx context.Context) error {
	if c.client == nil {
		return nil
	}
	live, err := c.client.Applier().Get(ctx, "cert-manager.io/v1", "ClusterIssuer", "", ClusterIssuerName)
	if kube.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return err
	}

	server, _, _ := unstructured.NestedString(live.Object, "spec", "acme", "server")
	email, _, _ := unstructured.NestedString(live.Object, "spec", "acme", "email")
	authority := ""
	if encoded, _, _ := unstructured.NestedString(live.Object, "spec", "acme", "caBundle"); encoded != "" {
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			authority = string(decoded)
		}
	}
	if server == letsEncryptProduction {
		// What an empty setting means already.
		server = ""
	}
	for _, adopted := range []struct{ key, value string }{
		{settings.KeyACMEServer, server},
		{settings.KeyACMEEmail, email},
		{settings.KeyACMECABundle, authority},
	} {
		if adopted.value == "" {
			continue
		}
		current, err := c.plainSetting(ctx, adopted.key)
		if err != nil {
			return err
		}
		if current != "" {
			continue
		}
		if err := c.db.SetSetting(ctx, adopted.key, adopted.value, false, "system"); err != nil {
			return err
		}
		c.log.Info("recorded what the certificate issuer the installer made says", "setting", adopted.key)
	}
	if !solverIsClusterIP(live) {
		if err := c.EnsureClusterIssuer(ctx); err != nil {
			return err
		}
		c.log.Info("the certificate issuer's challenges are answered through a ClusterIP now, which every environment allows")
	}

	recorded, err := c.db.GetComponent(ctx, "cert-manager")
	if err != nil {
		return err
	}
	if recorded.Status == "installed" {
		return nil
	}
	deployment, err := c.client.Applier().Get(ctx, "apps/v1", "Deployment", "cert-manager", "cert-manager")
	if kube.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	running := ""
	containers, _, _ := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	if len(containers) > 0 {
		if container, ok := containers[0].(map[string]any); ok {
			image, _ := container["image"].(string)
			running = strings.TrimPrefix(imageTag(image), "v")
		}
	}
	return c.db.SetComponent(ctx, store.ClusterComponent{
		Name: "cert-manager", Status: "installed", InstalledAt: time.Now(), Version: running,
	})
}

// solverIsClusterIP reports whether every HTTP-01 solver of an issuer asks
// for a ClusterIP Service.
func solverIsClusterIP(issuer *unstructured.Unstructured) bool {
	solvers, _, _ := unstructured.NestedSlice(issuer.Object, "spec", "acme", "solvers")
	if len(solvers) == 0 {
		return false
	}
	for _, raw := range solvers {
		solver, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if _, http := solver["http01"]; !http {
			continue
		}
		if kind, _, _ := unstructured.NestedString(solver, "http01", "ingress", "serviceType"); kind != solverServiceType {
			return false
		}
	}
	return true
}

// plainSetting reads a setting that is not sealed.
func (c *Cluster) plainSetting(ctx context.Context, key string) (string, error) {
	value, _, err := c.db.GetSetting(ctx, key)
	return strings.TrimSpace(value), err
}
