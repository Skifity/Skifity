package kube

import (
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// An app with hostnames on Let's Encrypt, on a wildcard of the team's own and
// on an exact certificate of its own, behind every middleware there is.
func ownCertSpec() AppSpec {
	return AppSpec{
		Name: "shop", Namespace: "acme-shop-prod", AppID: "app_1", TeamID: "team_1", Port: 3000,
		ClusterIssuer: "letsencrypt",
		Protected:     true,
		PasswordUsers: "client:$2y$10$notarealhashnotarealhashnotarealhashnotarealhashnotare",
		Domains: []DomainSpec{
			{Hostname: "shop.example.org", Path: "/", TLS: true},
			{Hostname: "shop.example.com", Path: "/", TLS: true, Certificate: "crt_wild"},
			{Hostname: "www.example.com", Path: "/", TLS: true, Certificate: "crt_wild", RedirectTo: "shop.example.com"},
			{Hostname: "intranet.example.net", Path: "/", TLS: true, Certificate: "crt_exact"},
			{Hostname: "plain.example.org", Path: "/", TLS: false},
		},
	}
}

func hostsOf(ingress *networkingv1.Ingress) []string {
	var out []string
	for _, rule := range ingress.Spec.Rules {
		out = append(out, rule.Host)
	}
	return out
}

func TestHostnamesOnTheTeamsOwnCertificatesAreInASecondIngress(t *testing.T) {
	s := ownCertSpec()
	main, own := BuildIngress(s), BuildOwnCertIngress(s)
	if main == nil || own == nil {
		t.Fatalf("main %v, own %v: want both", main != nil, own != nil)
	}

	if got := strings.Join(hostsOf(main), ","); got != "shop.example.org,plain.example.org" {
		t.Errorf("the first Ingress routes %s; it must not list a hostname with a certificate of its own", got)
	}
	if len(main.Spec.TLS) != 1 || strings.Join(main.Spec.TLS[0].Hosts, ",") != "shop.example.org" {
		t.Errorf("the first Ingress's TLS is %+v", main.Spec.TLS)
	}
	if main.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt" {
		t.Error("the first Ingress lost its issuer")
	}

	if own.Name != "shop-own-tls" || own.Namespace != s.Namespace {
		t.Errorf("the second Ingress is %s/%s", own.Namespace, own.Name)
	}
	if got := strings.Join(hostsOf(own), ","); got != "shop.example.com,www.example.com,intranet.example.net" {
		t.Errorf("the second Ingress routes %s", got)
	}
	// Nothing of cert-manager's: its annotation would have it issue for, and
	// write over, the Secrets the panel wrote.
	for key := range own.Annotations {
		if strings.HasPrefix(key, "cert-manager.io/") || strings.HasPrefix(key, "acme.cert-manager.io/") {
			t.Errorf("the second Ingress carries %s", key)
		}
	}
	want := map[string]string{
		CertificateSecretName("shop", "crt_exact"): "intranet.example.net",
		CertificateSecretName("shop", "crt_wild"):  "shop.example.com,www.example.com",
	}
	if len(own.Spec.TLS) != len(want) {
		t.Fatalf("the second Ingress has %d TLS blocks, want one per certificate: %+v", len(own.Spec.TLS), own.Spec.TLS)
	}
	for _, block := range own.Spec.TLS {
		if want[block.SecretName] != strings.Join(block.Hosts, ",") {
			t.Errorf("the TLS block for %s lists %v", block.SecretName, block.Hosts)
		}
	}
	// The same backend as the first.
	if own.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name != main.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name {
		t.Error("the two Ingresses route to different backends")
	}
}

// The firewall, the redirects and the password apply to a hostname whatever
// its certificate: the two Ingresses carry the same middlewares, in the same
// order.
func TestBothIngressesRunTheSameMiddlewaresInTheSameOrder(t *testing.T) {
	s := ownCertSpec()
	const key = "traefik.ingress.kubernetes.io/router.middlewares"
	main, own := BuildIngress(s).Annotations[key], BuildOwnCertIngress(s).Annotations[key]
	if main == "" || main != own {
		t.Fatalf("the middlewares differ:\n  first:  %s\n  second: %s", main, own)
	}
	chain := strings.Split(main, ",")
	wantOrder := []string{
		"acme-shop-prod-firewall@kubernetescrd",
		"acme-shop-prod-" + HostRedirectMiddlewareName("shop", "www.example.com") + "@kubernetescrd",
		"acme-shop-prod-redirect-https@kubernetescrd",
		"acme-shop-prod-shop-password@kubernetescrd",
	}
	if !slices.Equal(chain, wantOrder) {
		t.Errorf("the chain is %v, want %v", chain, wantOrder)
	}

	// And with every hostname on the team's own certificates and no ACME
	// email configured, there is still something on the other side of the
	// redirect to HTTPS.
	s.ClusterIssuer = ""
	s.Domains = []DomainSpec{{Hostname: "shop.example.com", Path: "/", TLS: true, Certificate: "crt_wild"}}
	if BuildIngress(s) != nil {
		t.Error("an app with every hostname on its own certificates still has a first Ingress")
	}
	if got := BuildOwnCertIngress(s).Annotations[key]; !strings.Contains(got, RedirectMiddleware) {
		t.Errorf("HTTP is not sent to HTTPS: %s", got)
	}
}

// Only when a hostname needs it, and the first Ingress is untouched when none
// does.
func TestTheSecondIngressExistsOnlyWhenAHostnameNeedsIt(t *testing.T) {
	s := ownCertSpec()
	for i := range s.Domains {
		s.Domains[i].Certificate = ""
	}
	if BuildOwnCertIngress(s) != nil {
		t.Error("a second Ingress was rendered with no hostname on the team's own certificate")
	}
	if CertificatesUsed(s) != nil {
		t.Errorf("certificates are used: %v", CertificatesUsed(s))
	}
	if got := len(hostsOf(BuildIngress(s))); got != len(s.Domains) {
		t.Errorf("the first Ingress routes %d hostnames, want all %d", got, len(s.Domains))
	}

	// An app with no port has no Ingress of either kind.
	s = ownCertSpec()
	s.Port = 0
	if BuildOwnCertIngress(s) != nil || BuildIngress(s) != nil {
		t.Error("an app with no port has an Ingress")
	}
}

func TestACertificatesSecretHoldsTheChainAndTheKey(t *testing.T) {
	s := ownCertSpec()
	pairs := map[string]CertificatePair{
		"crt_wild":  {Chain: []byte("-----BEGIN CERTIFICATE-----\nwild\n"), Key: []byte("-----BEGIN PRIVATE KEY-----\nwild\n")},
		"crt_exact": {Chain: []byte("-----BEGIN CERTIFICATE-----\nexact\n"), Key: []byte("-----BEGIN PRIVATE KEY-----\nexact\n")},
	}
	secrets, err := BuildCertificateSecrets(s, pairs)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 {
		t.Fatalf("got %d secrets, want one per certificate", len(secrets))
	}
	for _, secret := range secrets {
		id := secret.Annotations[certificateIDAnnotation]
		if secret.Name != CertificateSecretName("shop", id) || secret.Namespace != s.Namespace {
			t.Errorf("secret %s/%s holds %q", secret.Namespace, secret.Name, id)
		}
		if secret.Type != corev1.SecretTypeTLS {
			t.Errorf("%s is %s, want kubernetes.io/tls", secret.Name, secret.Type)
		}
		if string(secret.Data["tls.crt"]) != string(pairs[id].Chain) || string(secret.Data["tls.key"]) != string(pairs[id].Key) {
			t.Errorf("%s holds the wrong certificate", secret.Name)
		}
		if secret.Labels[certificateLabel] != "shop" || secret.Labels["app.kubernetes.io/name"] != "shop" {
			t.Errorf("%s is not labelled as the app's: %v", secret.Name, secret.Labels)
		}
		if len(secret.StringData) != 0 {
			t.Errorf("%s uses stringData, which cannot be taken away", secret.Name)
		}
	}

	// A certificate the app uses and nobody could read is an error, not an
	// Ingress naming a Secret that is not there.
	delete(pairs, "crt_exact")
	if _, err := BuildCertificateSecrets(s, pairs); err == nil {
		t.Error("a certificate that could not be read was left out quietly")
	}
}

func TestACertificateIsOnlyForAnHTTPSDomain(t *testing.T) {
	s := ownCertSpec()
	s.Image = "registry.example.test/shop:1"
	if err := s.Validate(); err != nil {
		t.Fatalf("a good spec was refused: %v", err)
	}
	s.Domains[1].TLS = false
	if err := s.Validate(); err == nil {
		t.Error("a certificate on a domain served over plain HTTP was accepted")
	}
}

// What the app no longer needs goes: the second Ingress once no hostname is
// on a certificate of the team's own, and each Secret of a certificate it
// stopped using. Nothing of another app's, and nothing that is not a
// certificate, is touched.
func TestCertificatesAnAppNoLongerUsesAreRemoved(t *testing.T) {
	s := ownCertSpec()
	secret := func(name, of string) *corev1.Secret {
		labels := map[string]string{"app.kubernetes.io/name": of}
		if of != "" {
			labels[certificateLabel] = of
		}
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace, Labels: labels}}
	}
	clientset := fake.NewSimpleClientset(
		secret(CertificateSecretName("shop", "crt_wild"), "shop"),
		secret(CertificateSecretName("shop", "crt_exact"), "shop"),
		secret(CertificateSecretName("shop", "crt_gone"), "shop"),
		secret(CertificateSecretName("blog", "crt_gone"), "blog"),
		secret(ResourceName("shop", "env"), ""),
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: OwnCertIngressName("shop"), Namespace: s.Namespace}},
	)
	c := &Client{clientset: clientset}

	remaining := func() []string {
		list, err := clientset.CoreV1().Secrets(s.Namespace).List(t.Context(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		slices.Sort(names)
		return names
	}
	ingressThere := func() bool {
		_, err := clientset.NetworkingV1().Ingresses(s.Namespace).Get(t.Context(), OwnCertIngressName("shop"), metav1.GetOptions{})
		return err == nil
	}

	// Still in use: only the certificate nothing uses goes.
	if err := c.PruneCertificates(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	want := []string{
		CertificateSecretName("blog", "crt_gone"), CertificateSecretName("shop", "crt_exact"),
		CertificateSecretName("shop", "crt_wild"), ResourceName("shop", "env"),
	}
	slices.Sort(want)
	if got := remaining(); !slices.Equal(got, want) {
		t.Errorf("after the first prune: %v, want %v", got, want)
	}
	if !ingressThere() {
		t.Error("the second Ingress was removed while hostnames are on it")
	}

	// Every certificate removed from the panel: the Ingress and every Secret
	// of this app's go; the other app's and the variables stay.
	for i := range s.Domains {
		s.Domains[i].Certificate = ""
	}
	if err := c.PruneCertificates(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	want = []string{CertificateSecretName("blog", "crt_gone"), ResourceName("shop", "env")}
	slices.Sort(want)
	if got := remaining(); !slices.Equal(got, want) {
		t.Errorf("after the last certificate went: %v, want %v", got, want)
	}
	if ingressThere() {
		t.Error("the second Ingress outlived the last hostname on it")
	}
}
