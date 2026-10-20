package kube

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// pinnedVolume is a volume only one node can mount, as the storage k3s ships
// makes every one, bound to a claim.
func pinnedVolume(name, node, namespace, claim string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{Namespace: namespace, Name: claim},
			NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: "kubernetes.io/hostname", Operator: corev1.NodeSelectorOpIn, Values: []string{node},
				}}}},
			}},
		},
	}
}

// What goes with a server is what somebody would miss: an app's volume, a
// database's, the registry's images. The vulnerability scanner's cache is
// not: the next scan makes it again, wherever it runs, and counted with the
// rest it kept a server from being taken out until somebody agreed to lose
// data that was nobody's.
func TestTheScannersCacheIsNotDataAServerTakesWithIt(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		pinnedVolume("pvc-1", "second", "acme-shop-production", "shop-uploads"),
		pinnedVolume("pvc-2", "second", BuildsNamespace, RegistryService),
		pinnedVolume("pvc-3", "second", BuildsNamespace, ScanCacheClaim),
		pinnedVolume("pvc-4", "first", "acme-shop-production", "data-orders-0"),
	)
	client := NewClientWith(clientset, nil, "")

	got, err := client.VolumesPinnedToNode(t.Context(), "second")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme-shop-production/shop-uploads", BuildsNamespace + "/" + RegistryService}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the second server takes %v with it, want %v", got, want)
	}
}
