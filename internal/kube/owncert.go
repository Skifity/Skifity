package kube

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"skifity/internal/version"
)

// A hostname served with a certificate the team brought, rather than one
// cert-manager issues.
//
// It cannot share the app's Ingress. cert-manager's ingress-shim takes every
// TLS block of an Ingress that carries its annotation as a request for a
// certificate, and writes what it issues into the Secret that block names —
// over a Secret it did not create, too. A hostname with a certificate of its
// own in that Ingress would be sent to Let's Encrypt anyway, and the Secret
// the panel wrote would be replaced by whatever came back, or by nothing.
//
// So those hostnames go in a second Ingress for the app, with no cert-manager
// annotation, the same backend and the same middlewares, and a TLS block per
// certificate naming a kubernetes.io/tls Secret the panel writes from what is
// stored. The first Ingress no longer lists them. Both carry the same
// annotation for their middlewares, built by one function, so a hostname on a
// certificate of the team's own is behind the same firewall, redirects and
// password as the others.
//
// Traefik reads the certificates of every Ingress into one store and picks one
// by the name a browser asks for, an exact name before a wildcard. That is why
// the panel refuses a certificate naming another team's hostname: see
// errdoc.CertificateHostnameTaken.

// certificateLabel marks a Secret as holding one of an app's certificates, so
// the ones nothing uses any more can be found and removed.
var certificateLabel = version.LabelKey("certificate-of")

// certificateIDAnnotation says which of the team's certificates a Secret
// holds, for somebody reading the namespace.
var certificateIDAnnotation = version.LabelKey("certificate-id")

// OwnCertIngressName is the app's second Ingress, for the hostnames served
// with a certificate of the team's own.
func OwnCertIngressName(app string) string { return ResourceName(app, "own-tls") }

// CertificateSecretName is the Secret one of the team's certificates is
// written into, in one app's namespace.
func CertificateSecretName(app, certificateID string) string {
	return ResourceName(app, "cert-"+shortHash(certificateID, 10))
}

// CertificatePair is a certificate chain and its private key, both PEM.
type CertificatePair struct {
	Chain []byte
	Key   []byte
}

// CertificatesUsed are the ids of the team's certificates the app's
// hostnames are served with, each once, in order.
func CertificatesUsed(s AppSpec) []string {
	var out []string
	for _, d := range ownCertDomains(s) {
		if !slices.Contains(out, d.Certificate) {
			out = append(out, d.Certificate)
		}
	}
	slices.Sort(out)
	return out
}

// ownCertDomains are the app's domains that are served with a certificate of
// the team's own.
func ownCertDomains(s AppSpec) []DomainSpec {
	if s.Port <= 0 {
		return nil
	}
	var out []DomainSpec
	for _, d := range s.Domains {
		if d.Certificate != "" {
			out = append(out, d)
		}
	}
	return out
}

// BuildOwnCertIngress renders the app's second Ingress: the hostnames served
// with a certificate of the team's own. Nil when there are none.
func BuildOwnCertIngress(s AppSpec) *networkingv1.Ingress {
	domains := ownCertDomains(s)
	if len(domains) == 0 {
		return nil
	}
	// The middlewares, and nothing from cert-manager: its annotation here is
	// the whole problem this Ingress exists to avoid.
	ingress := buildIngress(s, OwnCertIngressName(s.Name), domains, ingressAnnotations(s))
	for _, id := range CertificatesUsed(s) {
		var hosts []string
		for _, d := range domains {
			if d.Certificate == id {
				hosts = append(hosts, d.Hostname)
			}
		}
		ingress.Spec.TLS = append(ingress.Spec.TLS, networkingv1.IngressTLS{
			Hosts:      hosts,
			SecretName: CertificateSecretName(s.Name, id),
		})
	}
	return ingress
}

// BuildCertificateSecrets renders the Secrets the second Ingress names, one
// per certificate the app uses. A certificate with nothing in pairs is an
// error rather than a Secret left out: an Ingress naming a Secret that is not
// there is served with the ingress controller's own certificate, which every
// browser refuses.
func BuildCertificateSecrets(s AppSpec, pairs map[string]CertificatePair) ([]*corev1.Secret, error) {
	var out []*corev1.Secret
	for _, id := range CertificatesUsed(s) {
		pair, ok := pairs[id]
		if !ok || len(pair.Chain) == 0 || len(pair.Key) == 0 {
			return nil, fmt.Errorf("the certificate %s is used by %s and could not be read", id, s.Name)
		}
		secretLabels := s.Labels()
		secretLabels[certificateLabel] = s.Name
		out = append(out, &corev1.Secret{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{
				Name:        CertificateSecretName(s.Name, id),
				Namespace:   s.Namespace,
				Labels:      secretLabels,
				Annotations: map[string]string{certificateIDAnnotation: id},
			},
			Type: corev1.SecretTypeTLS,
			// data, as everywhere else: stringData cannot be taken away.
			Data: map[string][]byte{
				corev1.TLSCertKey:       pair.Chain,
				corev1.TLSPrivateKeyKey: pair.Key,
			},
		})
	}
	return out, nil
}

// PruneCertificates removes what an app no longer needs of its own
// certificates: the second Ingress when no hostname is on one, and the Secret
// of every certificate it no longer uses — a private key somebody removed from
// the panel is not left in a namespace anybody with access to it can read.
//
// A spec with no domains removes all of it, which is what deleting the app
// does.
func (c *Client) PruneCertificates(ctx context.Context, s AppSpec) error {
	if BuildOwnCertIngress(s) == nil {
		err := c.clientset.NetworkingV1().Ingresses(s.Namespace).
			Delete(ctx, OwnCertIngressName(s.Name), metav1.DeleteOptions{})
		if err != nil && !IsNotFound(err) {
			return fmt.Errorf("remove the ingress for %s's own certificates: %w", s.Name, err)
		}
	}

	wanted := map[string]bool{}
	for _, id := range CertificatesUsed(s) {
		wanted[CertificateSecretName(s.Name, id)] = true
	}
	selector := labels.SelectorFromSet(map[string]string{certificateLabel: s.Name}).String()
	secrets, err := c.clientset.CoreV1().Secrets(s.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("list the certificates of %s: %w", s.Name, err)
	}
	// One at a time, by name, and checked again here: a selector the server
	// ignored is the difference between tidying and emptying the namespace.
	for _, secret := range secrets.Items {
		if secret.Labels[certificateLabel] != s.Name || wanted[secret.Name] {
			continue
		}
		if err := c.clientset.CoreV1().Secrets(s.Namespace).
			Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
			return fmt.Errorf("remove the certificate secret %s: %w", secret.Name, err)
		}
	}
	return nil
}
