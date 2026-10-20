package deploy

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// A build of a repository on a Git host builds are kept off is refused before
// it starts, unless an administrator listed the host; then that build alone
// may reach it, on the repository's port.
//
// The clone used to wait two minutes for a connection the build namespace's
// policy dropped, and fail with a timeout that named nothing: a Gitea at home
// or a GitLab on the office network was a repository no build could ever
// clone.
func TestABuildReachesAListedGitHostAndNoOther(t *testing.T) {
	d, db, _, _ := testDeployer(t)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "first"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			{Type: corev1.NodeExternalIP, Address: "203.0.113.10"},
		}},
	}
	applied := &appliedObjects{}
	dynamic := syncableDynamic()
	dynamic.PrependReactor("patch", "*", applied.react)
	withCluster(t, d, db, fake.NewSimpleClientset(node), dynamic)

	resolves := map[string][]string{
		"github.com":             {"140.82.112.3"},
		"git.home.arpa":          {"192.168.1.10"},
		"gitea.example.com":      {"203.0.113.10"},
		"split.example.com":      {"192.168.1.20", "198.51.100.7"},
		"metadata.example.com":   {"169.254.169.254"},
		"tailnet.ts.example.com": {"100.101.102.103"},
	}
	previous := lookupGitHost
	lookupGitHost = func(_ context.Context, host string) ([]string, error) { return resolves[host], nil }
	t.Cleanup(func() { lookupGitHost = previous })

	open := func(t *testing.T, listed, repo string) (*networkingv1.NetworkPolicy, error) {
		t.Helper()
		if err := db.SetSetting(t.Context(), settings.KeyGitPrivateHosts, listed, false, "test"); err != nil {
			t.Fatal(err)
		}
		spec := builder.JobSpec{Name: "build-shop-abc", Namespace: "skifity-builds", DeploymentID: "dep_1", RepoURL: repo}
		return d.openPathToSource(t.Context(), &store.Deployment{ID: "dep_1"}, spec)
	}
	refused := func(t *testing.T, err error) {
		t.Helper()
		var problem *errdoc.Problem
		if !errors.As(err, &problem) || problem.Code != "build.git_host_private" {
			t.Fatalf("got %v, want the build refused for a host nobody listed", err)
		}
	}

	t.Run("a host on the internet needs nothing", func(t *testing.T) {
		policy, err := open(t, "", "https://github.com/acme/shop")
		if err != nil || policy != nil {
			t.Fatalf("got %v, %v; a public host needs no policy", policy, err)
		}
	})
	t.Run("a host at home that nobody listed is refused", func(t *testing.T) {
		_, err := open(t, "", "https://git.home.arpa/acme/shop")
		refused(t, err)
	})
	t.Run("on a VPN as well", func(t *testing.T) {
		_, err := open(t, "", "https://tailnet.ts.example.com/acme/shop")
		refused(t, err)
	})
	t.Run("a listed host is opened to this build alone, on its port", func(t *testing.T) {
		policy, err := open(t, "git.home.arpa\nother.lan:3443", "https://git.home.arpa/acme/shop")
		if err != nil || policy == nil {
			t.Fatalf("got %v, %v; want the listed host opened", policy, err)
		}
		applied.mu.Lock()
		sent := applied.objects
		applied.mu.Unlock()
		if len(sent) != 1 || sent[0].GetKind() != "NetworkPolicy" || sent[0].GetName() != policy.Name {
			t.Fatalf("applied %d objects, want the one policy", len(sent))
		}
		var live networkingv1.NetworkPolicy
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(sent[0].Object, &live); err != nil {
			t.Fatal(err)
		}
		selector := live.Spec.PodSelector.MatchLabels
		if selector[version.LabelKey("deployment-id")] != "dep_1" || selector["app.kubernetes.io/component"] != "build" {
			t.Errorf("the policy selects %v, want this build's pods and no other's", selector)
		}
		rules := live.Spec.Egress
		if len(rules) != 1 || len(rules[0].To) != 1 || rules[0].To[0].IPBlock.CIDR != "192.168.1.10/32" ||
			len(rules[0].Ports) != 1 || rules[0].Ports[0].Port.IntValue() != 443 {
			t.Errorf("the policy opens %+v, want 192.168.1.10/32 on 443 alone", rules)
		}
	})
	t.Run("a listed host on another port is not this one", func(t *testing.T) {
		_, err := open(t, "git.home.arpa:443", "https://git.home.arpa:3443/acme/shop")
		refused(t, err)
	})
	t.Run("a server's own address on HTTPS is open to every build already", func(t *testing.T) {
		policy, err := open(t, "", "https://gitea.example.com/acme/shop")
		if err != nil || policy != nil {
			t.Fatalf("got %v, %v; a Git host that is an app here needs nothing", policy, err)
		}
	})
	t.Run("but not on another port", func(t *testing.T) {
		_, err := open(t, "", "https://gitea.example.com:2222/acme/shop")
		refused(t, err)
	})
	t.Run("a name that also has a public address is cloned from that", func(t *testing.T) {
		policy, err := open(t, "", "https://split.example.com/acme/shop")
		if err != nil || policy != nil {
			t.Fatalf("got %v, %v; the public address is reachable as it was", policy, err)
		}
	})
	t.Run("the metadata service is never opened, listed or not", func(t *testing.T) {
		policy, err := open(t, "metadata.example.com", "https://metadata.example.com/acme/shop")
		if err != nil || policy != nil {
			t.Fatalf("got %v, %v; the metadata service is no Git host", policy, err)
		}
	})
	t.Run("an upload is not cloned at all", func(t *testing.T) {
		spec := builder.JobSpec{Name: "build-shop-abc", Namespace: "skifity-builds", DeploymentID: "dep_1", SourceUpload: true}
		policy, err := d.openPathToSource(t.Context(), &store.Deployment{ID: "dep_1"}, spec)
		if err != nil || policy != nil {
			t.Fatalf("got %v, %v; an upload clones nothing", policy, err)
		}
	})
}
