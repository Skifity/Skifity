package cluster

import (
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/kube"
)

// The installer attached to a release names the panel's image by tag and
// digest. Upgrading from the panel replaces the tag; the digest has to go with
// it, or the colon inside sha256:… was taken for the tag's and the panel was
// pointed at an image reference that does not exist.
func TestUpgradingThePanelReplacesTheTagOfAnImagePinnedByDigest(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct{ current, want string }{
		{"ghcr.io/skifity/skifity:v0.1.1@" + digest, "ghcr.io/skifity/skifity:v0.2.0"},
		{"ghcr.io/skifity/skifity@" + digest, "ghcr.io/skifity/skifity:v0.2.0"},
		{"ghcr.io/skifity/skifity:v0.1.1", "ghcr.io/skifity/skifity:v0.2.0"},
		{"registry.example.test:5000/skifity:0.0.0-test", "registry.example.test:5000/skifity:v0.2.0"},
		{"registry.example.test:5000/skifity", "registry.example.test:5000/skifity:v0.2.0"},
	} {
		t.Run(tc.current, func(t *testing.T) {
			panel := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "skifity-panel", Namespace: "skifity-system"},
				Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "panel", Image: tc.current}},
				}}},
			}
			clientset := fake.NewSimpleClientset(panel)
			c := New(kube.NewClientWith(clientset, nil, ""), nil, nil, slog.New(slog.DiscardHandler))

			previous, err := c.UpgradePanel(t.Context(), "v0.2.0")
			if err != nil {
				t.Fatalf("UpgradePanel: %v", err)
			}
			if previous != tc.current {
				t.Errorf("the image it replaced is %q, want %q", previous, tc.current)
			}
			got, err := clientset.AppsV1().Deployments("skifity-system").Get(t.Context(), "skifity-panel", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if image := got.Spec.Template.Spec.Containers[0].Image; image != tc.want {
				t.Errorf("the panel was pointed at %q, want %q", image, tc.want)
			}
		})
	}
}
