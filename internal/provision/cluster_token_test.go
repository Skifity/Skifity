package provision

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// noCluster is the answer when nothing exists to join yet.
func noCluster() (bool, error) { return false, nil }

func tokenProvisioner(t *testing.T, tokenPath string) (*Provisioner, *store.DB) {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return New(Options{
		DB: db, Keyring: keyring, ClusterTokenPath: tokenPath,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}), db
}

// TestTheClustersOwnTokenWins: a cluster the installer created has a token the
// panel cannot invent, and a server joining with any other token is refused.
func TestTheClustersOwnTokenWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster-token")
	if err := os.WriteFile(path, []byte("K10theRealTokenFromK3s\n"), 0o600); err != nil {
		t.Fatalf("write the token: %v", err)
	}
	p, db := tokenProvisioner(t, path)

	// Even with a token already stored, the cluster's own is the truth.
	sealed, err := p.keyring.Seal([]byte("a token the panel made up"), settings.Context(clusterTokenKey))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := db.SetSetting(t.Context(), clusterTokenKey, sealed, true, "test"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	token, err := p.joinToken(t.Context(), noCluster)
	if err != nil {
		t.Fatalf("clusterToken: %v", err)
	}
	if token != "K10theRealTokenFromK3s" {
		t.Fatalf("token is %q, want the one k3s wrote", token)
	}

	// And it is kept, so the file is not needed on every call.
	stored, encrypted, err := db.GetSetting(t.Context(), clusterTokenKey)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if !encrypted || stored == "" {
		t.Fatal("the cluster's token was not stored, encrypted")
	}
	plaintext, err := p.keyring.Open(stored, settings.Context(clusterTokenKey))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(plaintext) != "K10theRealTokenFromK3s" {
		t.Fatalf("stored token is %q", plaintext)
	}
}

// TestAStoredTokenIsReused covers a cluster the panel built itself.
func TestAStoredTokenIsReused(t *testing.T) {
	p, _ := tokenProvisioner(t, filepath.Join(t.TempDir(), "absent"))

	first, err := p.joinToken(t.Context(), noCluster)
	if err != nil {
		t.Fatalf("clusterToken: %v", err)
	}
	if first == "" {
		t.Fatal("no token was generated for a cluster that does not exist yet")
	}
	second, err := p.joinToken(t.Context(), noCluster)
	if err != nil {
		t.Fatalf("clusterToken: %v", err)
	}
	if second != first {
		t.Fatalf("a second call generated a different token: %q then %q", first, second)
	}
}

// TestAMissingTokenIsAnErrorNotAGuess: inventing one for a cluster that
// already exists is how a second, separate cluster gets built by accident.
func TestAMissingTokenIsAnErrorNotAGuess(t *testing.T) {
	p, _ := tokenProvisioner(t, filepath.Join(t.TempDir(), "absent"))
	// The panel is running in a cluster somebody else created.
	_, err := p.joinToken(t.Context(), func() (bool, error) { return true, nil })
	if err == nil {
		t.Fatal("a token was invented for a cluster that already exists")
	}
	if errdoc.From(err).Code != "cluster.token_missing" {
		t.Fatalf("error is %v, want cluster.token_missing", err)
	}
}

// TestControlPlaneAddressPrefersSomethingReachable: a node's external address
// is the one another machine can use.
func TestControlPlaneAddressPrefersSomethingReachable(t *testing.T) {
	nodes := []corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "no-addresses"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "internal-only"},
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "web-1"},
				{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			}},
		},
	}
	if got := firstControlPlaneAddress(nodes); got != "10.0.0.5" {
		t.Fatalf("address is %q, want the internal IP when there is no external one", got)
	}

	nodes[1].Status.Addresses = append(nodes[1].Status.Addresses,
		corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "203.0.113.10"})
	if got := firstControlPlaneAddress(nodes); got != "203.0.113.10" {
		t.Fatalf("address is %q, want the external IP", got)
	}

	if got := firstControlPlaneAddress(nil); got != "" {
		t.Fatalf("an empty cluster answered %q, which would be joined", got)
	}
}
