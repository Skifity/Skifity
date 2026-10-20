package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// issuerCluster is a cluster of fakes holding the given objects, which
// remembers what is applied to it.
func issuerCluster(t *testing.T, objects ...runtime.Object) (*Cluster, *store.DB, *collectorCalls) {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	calls := &collectorCalls{}
	dynamic := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}: "ClusterIssuerList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:               "DeploymentList",
	}, objects...)
	dynamic.PrependReactor("patch", "*", calls.react)
	client := kube.NewClientWith(fake.NewSimpleClientset(), dynamic, "")
	return New(client, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil))), db, calls
}

// installersIssuer is the issuer install.sh makes, as deploy/cluster-issuer.yaml
// renders it.
func installersIssuer(server, email string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata":   map[string]any{"name": ClusterIssuerName},
		"spec": map[string]any{"acme": map[string]any{
			"server": server,
			"email":  email,
		}},
	}}
}

// testAuthority is a certificate authority's certificate in PEM, made for the
// test.
func testAuthority(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test ACME authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func appliedIssuer(t *testing.T, calls *collectorCalls) map[string]any {
	t.Helper()
	calls.mu.Lock()
	defer calls.mu.Unlock()
	for _, obj := range calls.applied {
		if obj.GetKind() == "ClusterIssuer" {
			acme, _, _ := unstructured.NestedMap(obj.Object, "spec", "acme")
			return acme
		}
	}
	return nil
}

// An app's certificates are asked of the issuer that is there, whoever made
// it, and of none when there is none: the email setting, which the installer
// never writes, used to decide it.
func TestAppsAskTheIssuerThatIsThere(t *testing.T) {
	c, _, _ := issuerCluster(t)
	if issuer, err := c.CertificateIssuer(t.Context()); err != nil || issuer != "" {
		t.Fatalf("with no issuer, apps were given %q (%v)", issuer, err)
	}

	c, _, _ = issuerCluster(t, installersIssuer(letsEncryptProduction, ""))
	if issuer, err := c.CertificateIssuer(t.Context()); err != nil || issuer != ClusterIssuerName {
		t.Fatalf("with the installer's issuer and no email set, apps were given %q (%v)", issuer, err)
	}
}

// The issuer is made from the settings: no email is needed, the ACME server is
// Let's Encrypt's when none is set, and the authority of a server of one's own
// goes along for cert-manager to trust.
func TestTheIssuerIsMadeFromTheSettings(t *testing.T) {
	c, db, calls := issuerCluster(t)
	authority := testAuthority(t)
	if err := db.SetSetting(t.Context(), settings.KeyACMECABundle, authority, false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureClusterIssuer(t.Context()); err != nil {
		t.Fatalf("an issuer with no email set: %v", err)
	}
	acme := appliedIssuer(t, calls)
	if acme == nil {
		t.Fatal("no issuer was applied")
	}
	if acme["server"] != letsEncryptProduction {
		t.Errorf("the issuer asks %v, want Let's Encrypt's production server", acme["server"])
	}
	// Every environment's quota allows no NodePort, which is what
	// cert-manager makes the challenge's Service unless told otherwise.
	solvers, _ := acme["solvers"].([]any)
	if len(solvers) != 1 {
		t.Fatalf("the issuer has %d solvers, want one", len(solvers))
	}
	if kind, _, _ := unstructured.NestedString(solvers[0].(map[string]any), "http01", "ingress", "serviceType"); kind != "ClusterIP" {
		t.Errorf("the challenge's Service is a %q, which an environment's quota refuses unless it is a ClusterIP", kind)
	}
	if _, ok := acme["email"]; ok {
		t.Errorf("an empty email was written into the issuer: %v", acme["email"])
	}
	decoded, err := base64.StdEncoding.DecodeString(acme["caBundle"].(string))
	if err != nil || string(decoded) != strings.TrimSpace(authority) {
		t.Errorf("the authority the issuer trusts is not the one in Settings (%v)", err)
	}
}

// Saving the settings changes the issuer when there is one, and does not make
// one when there is none: that is what installing HTTPS certificates does.
func TestSavingTheSettingsReachesTheIssuerThatIsThere(t *testing.T) {
	c, db, calls := issuerCluster(t)
	if err := db.SetSetting(t.Context(), settings.KeyACMEServer, "https://acme.example.test/directory", false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshClusterIssuer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if acme := appliedIssuer(t, calls); acme != nil {
		t.Fatalf("an issuer was made by saving a setting: %v", acme)
	}

	c, db, calls = issuerCluster(t, installersIssuer(letsEncryptProduction, ""))
	if err := db.SetSetting(t.Context(), settings.KeyACMEServer, "https://acme.example.test/directory", false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshClusterIssuer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if acme := appliedIssuer(t, calls); acme == nil || acme["server"] != "https://acme.example.test/directory" {
		t.Fatalf("the saved ACME server did not reach the issuer: %v", acme)
	}
}

// What the installer set the issuer up with is what the settings say from
// then on, and cert-manager, which it installed, is listed as installed. A
// setting somebody already chose is left as it is.
func TestTheInstallersIssuerIsAdopted(t *testing.T) {
	const staging = "https://acme-staging-v02.api.letsencrypt.org/directory"
	certManager := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "cert-manager", "namespace": "cert-manager"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "cert-manager-controller", "image": "quay.io/jetstack/cert-manager-controller:v1.21.2"}},
		}}},
	}}
	c, db, calls := issuerCluster(t, installersIssuer(staging, "ops@example.org"), certManager)
	if err := db.SetSetting(t.Context(), settings.KeyACMEEmail, "chosen@example.org", false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.AdoptClusterIssuer(t.Context()); err != nil {
		t.Fatal(err)
	}

	if server, _, _ := db.GetSetting(t.Context(), settings.KeyACMEServer); server != staging {
		t.Errorf("the ACME server setting is %q, want the staging server the installer set", server)
	}
	if email, _, _ := db.GetSetting(t.Context(), settings.KeyACMEEmail); email != "chosen@example.org" {
		t.Errorf("an email somebody chose was replaced with %q", email)
	}
	component, err := db.GetComponent(t.Context(), "cert-manager")
	if err != nil {
		t.Fatal(err)
	}
	if component.Status != "installed" || component.Version != "1.21.2" {
		t.Errorf("cert-manager is recorded as %q at %q, want installed at 1.21.2", component.Status, component.Version)
	}
	// The installer's issuer asked for a NodePort for its challenges; it is
	// written again, with what was recorded, asking for a ClusterIP.
	acme := appliedIssuer(t, calls)
	if acme == nil {
		t.Fatal("an issuer asking for a NodePort was left as it was")
	}
	if acme["server"] != staging || acme["email"] != "chosen@example.org" {
		t.Errorf("the issuer written again asks %v as %v, want the staging server and the chosen email", acme["server"], acme["email"])
	}

	// Production is what an empty setting means already, so it is not
	// written down as if somebody had chosen it.
	c, db, _ = issuerCluster(t, installersIssuer(letsEncryptProduction, ""))
	if err := c.AdoptClusterIssuer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if server, _, _ := db.GetSetting(t.Context(), settings.KeyACMEServer); server != "" {
		t.Errorf("Let's Encrypt's production server was recorded as a choice: %q", server)
	}
}

// An issuer already answering its challenges through a ClusterIP is left as it
// is at start.
func TestAnIssuerWithAClusterIPSolverIsLeftAlone(t *testing.T) {
	issuer := installersIssuer(letsEncryptProduction, "")
	_ = unstructured.SetNestedSlice(issuer.Object, []any{map[string]any{
		"http01": map[string]any{"ingress": map[string]any{"class": "traefik", "serviceType": "ClusterIP"}},
	}}, "spec", "acme", "solvers")
	c, _, calls := issuerCluster(t, issuer)
	if err := c.AdoptClusterIssuer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if acme := appliedIssuer(t, calls); acme != nil {
		t.Errorf("an issuer that needed nothing was written again: %v", acme)
	}
}
