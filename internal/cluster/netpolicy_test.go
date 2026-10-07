package cluster

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/crypto"
	"skifity/internal/kube"
	"skifity/internal/store"
	"skifity/internal/version"
)

func nodeWithAddress(name, address string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: address},
		}},
	}
}

func managedNamespace(name, label string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: name,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": version.Binary,
			label:                          "x",
		},
	}}
}

// policyCluster is a cluster of fakes that keeps hold of the clientset, so a
// test can add a node to it.
func policyCluster(t *testing.T, objects ...runtime.Object) (*Cluster, *fake.Clientset, *collectorCalls) {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keyring, err := crypto.InitKeyring(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	calls := &collectorCalls{}
	dynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamic.PrependReactor("patch", "*", calls.react)
	clientset := fake.NewSimpleClientset(objects...)
	client := kube.NewClientWith(clientset, dynamic, "")
	return New(client, db, keyring, slog.New(slog.NewTextHandler(io.Discard, nil))), clientset, calls
}

// appliedPolicies is every NetworkPolicy applied so far, by namespace/name.
func appliedPolicies(calls *collectorCalls) map[string]string {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	out := map[string]string{}
	for _, obj := range calls.applied {
		if obj.GetKind() != "NetworkPolicy" {
			continue
		}
		raw, _ := json.Marshal(obj.Object)
		out[obj.GetNamespace()+"/"+obj.GetName()] = string(raw)
	}
	return out
}

func forgetApplied(calls *collectorCalls) {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.applied = nil
}

// namesTheServer reports whether any policy applied in a namespace excepts the
// server's address from the internet.
func namesTheServer(policies map[string]string, namespace, address string) (found, anyPolicy bool) {
	for key, raw := range policies {
		if !strings.HasPrefix(key, namespace+"/") || strings.HasSuffix(key, "/default-deny") {
			continue
		}
		anyPolicy = true
		if strings.Contains(raw, address+"/32") {
			found = true
		}
	}
	return found, anyPolicy
}

// TestAServerThatJoinsIsFencedOffEverywhere: a policy is written when an
// environment is prepared, so one that has not deployed since a server joined
// would leave that server's address open to its pods for as long as it stayed
// quiet.
func TestAServerThatJoinsIsFencedOffEverywhere(t *testing.T) {
	plugin := kube.PluginNamespace("com.example.backups")
	terminating := managedNamespace("acme-gone-production", version.LabelKey("team-id"))
	terminating.Status.Phase = corev1.NamespaceTerminating

	c, clientset, calls := policyCluster(t,
		nodeWithAddress("one", "203.0.113.10"),
		managedNamespace("acme-shop-production", version.LabelKey("team-id")),
		managedNamespace("acme-shop-staging", version.LabelKey("team-id")),
		managedNamespace(plugin, version.LabelKey("plugin-id")),
		managedNamespace(kube.BuildsNamespace, version.LabelKey("component")),
		// Not the panel's: left alone.
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
		terminating,
	)
	ctx := t.Context()

	c.MaintainNetworkPolicies(ctx)
	first := appliedPolicies(calls)
	for _, namespace := range []string{"acme-shop-production", "acme-shop-staging", plugin, kube.BuildsNamespace} {
		found, any := namesTheServer(first, namespace, "203.0.113.10")
		if !any {
			t.Errorf("%s was given no policy: %v", namespace, slices.Sorted(mapsKeys(first)))
		} else if !found {
			t.Errorf("%s's policy does not fence off the server", namespace)
		}
	}
	for key := range first {
		if strings.HasPrefix(key, "kube-system/") || strings.HasPrefix(key, "acme-gone-production/") {
			t.Errorf("a namespace that is not ours to change was given a policy: %s", key)
		}
	}

	// Nothing changed, so nothing is applied.
	forgetApplied(calls)
	c.MaintainNetworkPolicies(ctx)
	if again := appliedPolicies(calls); len(again) != 0 {
		t.Errorf("an unchanged cluster was applied again: %v", slices.Sorted(mapsKeys(again)))
	}

	// A second server joins, and an environment that has not deployed since
	// is fenced off from it within the minute.
	if _, err := clientset.CoreV1().Nodes().Create(ctx, nodeWithAddress("two", "198.51.100.7"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	c.MaintainNetworkPolicies(ctx)
	after := appliedPolicies(calls)
	if found, _ := namesTheServer(after, "acme-shop-production", "198.51.100.7"); !found {
		t.Error("the new server is reachable from an environment that has not deployed since")
	}
	if found, _ := namesTheServer(after, "acme-shop-production", "203.0.113.10"); !found {
		t.Error("the first server fell out of the policy when the second joined")
	}
}

func mapsKeys(m map[string]string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
