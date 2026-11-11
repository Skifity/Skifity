package cluster

import (
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// settleCluster is a cluster of fakes holding the given workloads, with the
// given components recorded.
func settleCluster(t *testing.T, recorded []store.ClusterComponent, workloads ...runtime.Object) (*Cluster, *store.DB, *fake.Clientset) {
	t.Helper()
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, component := range recorded {
		if err := db.SetComponent(t.Context(), component); err != nil {
			t.Fatal(err)
		}
	}
	clientset := fake.NewSimpleClientset(workloads...)
	client := kube.NewClientWith(clientset, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), "")
	return New(client, db, nil, slog.New(slog.NewTextHandler(io.Discard, nil))), db, clientset
}

func installed(name string) store.ClusterComponent {
	return store.ClusterComponent{Name: name, Status: "installed", Version: "1.0.0", InstalledAt: time.Now()}
}

// A panel database restored onto a new server has components down as
// installed that the new cluster has never had. They are offered again; the
// ones the cluster runs, and the ones not recorded as installed, are left be.
func TestSettleComponentsOffersAgainWhatTheClusterDoesNotRun(t *testing.T) {
	c, db, _ := settleCluster(t,
		[]store.ClusterComponent{
			installed("registry"),
			installed(NVIDIAComponent),
			installed("cloudnative-pg"),
			installed("keda"),
			{Name: "cert-manager", Status: "failed", Detail: "it did not start"},
		},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: RegistryService, Namespace: kube.BuildsNamespace}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: kube.NVIDIADevicePluginName, Namespace: kube.GPUNamespace}},
	)

	gone, err := c.SettleComponents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cloudnative-pg", "keda"}; !slices.Equal(gone, want) {
		t.Fatalf("offered again %v, want %v", gone, want)
	}

	for name, want := range map[string]string{
		"cloudnative-pg": "absent",
		"keda":           "absent",
		"registry":       "installed",
		NVIDIAComponent:  "installed",
		"cert-manager":   "failed",
	} {
		got, err := db.GetComponent(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("%s is %q, want %q", name, got.Status, want)
		}
		if want == "absent" && got.Version != "" {
			t.Errorf("%s still says version %q, of something that is not there", name, got.Version)
		}
	}

	// Once offered, a second start finds nothing more to say.
	again, err := c.SettleComponents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("a second start offered %v again", again)
	}
}

// A cluster that answers with anything but "not there" is no evidence that a
// component is gone, and the record stays as it is.
func TestSettleComponentsChangesNothingWhenTheClusterDoesNotSay(t *testing.T) {
	c, db, clientset := settleCluster(t, []store.ClusterComponent{installed("cloudnative-pg")})
	clientset.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})

	if _, err := c.SettleComponents(t.Context()); err == nil {
		t.Fatal("a cluster that did not answer was taken for an answer")
	}
	got, err := db.GetComponent(t.Context(), "cloudnative-pg")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "installed" {
		t.Errorf("cloudnative-pg is %q after a failed look, want it left installed", got.Status)
	}
}

// Every component the panel installs can be looked for, so one added later
// without a workload to find is caught here rather than after a restore.
func TestEveryComponentThePanelInstallsHasAWorkload(t *testing.T) {
	c, _, _ := settleCluster(t, nil)
	for _, component := range settings.Components {
		if component.External {
			continue
		}
		if _, known := c.componentWorkloadFor(component.Name); !known {
			t.Errorf("%s has no workload to say whether it is in the cluster", component.Name)
		}
	}
}
