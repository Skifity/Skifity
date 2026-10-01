package cluster

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// gpuCluster is a Cluster over fakes, with these objects already there, and
// the objects it applies kept, as the API server would keep them.
func gpuCluster(t *testing.T, existing ...runtime.Object) (*Cluster, *[]*unstructured.Unstructured) {
	t.Helper()
	_, db, _, _, _ := autoDomainFixture(t)
	dynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	applied := &[]*unstructured.Unstructured{}
	dynamic.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(k8stesting.PatchAction)
		if !ok || patch.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patch.GetPatch()); err != nil {
			return true, nil, err
		}
		*applied = append(*applied, obj)
		return true, obj, nil
	})
	c := New(kube.NewClientWith(fake.NewClientset(existing...), dynamic, ""), db, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return c, applied
}

func TestEnablingGPUsAppliesThePinnedDevicePlugin(t *testing.T) {
	if _, ok := settings.LookupComponent(NVIDIAComponent); !ok {
		t.Fatalf("%s is not a component the panel lists", NVIDIAComponent)
	}
	c, objects := gpuCluster(t)
	if err := c.InstallComponent(t.Context(), NVIDIAComponent); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(*objects) != 1 {
		t.Fatalf("%d objects were applied, want the DaemonSet", len(*objects))
	}
	applied := (*objects)[0]
	if applied.GetKind() != "DaemonSet" || applied.GetNamespace() != "kube-system" || applied.GetName() != kube.NVIDIADevicePluginName {
		t.Errorf("applied %s %s/%s", applied.GetKind(), applied.GetNamespace(), applied.GetName())
	}
	containers, _, _ := unstructured.NestedSlice(applied.Object, "spec", "template", "spec", "containers")
	if len(containers) != 1 || containers[0].(map[string]any)["image"] != kube.NVIDIADevicePluginImage {
		t.Errorf("the plugin runs %v", containers)
	}
	record, err := c.db.GetComponent(t.Context(), NVIDIAComponent)
	if err != nil || record.Status != "installed" || record.Version != "0.20.1" {
		t.Errorf("recorded as %+v (%v); the version is what an upgrade compares", record, err)
	}
}

func TestGPUsAreNotEnabledBesideSomebodyElsesPlugin(t *testing.T) {
	theirs := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin-daemonset", Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "nvidia-device-plugin-ctr", Image: "nvcr.io/nvidia/k8s-device-plugin:v0.17.0",
		}}}}},
	}
	c, objects := gpuCluster(t, theirs)
	err := c.InstallComponent(t.Context(), NVIDIAComponent)
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "gpu.device_plugin_exists" {
		t.Fatalf("installing beside another plugin answered %v", err)
	}
	if len(*objects) != 0 {
		t.Error("a second plugin was applied anyway")
	}
}

func TestAnAppsSpecCarriesItsGPUs(t *testing.T) {
	c, db, app, env, _ := autoDomainFixture(t)
	if err := db.SetAppGPU(t.Context(), store.AppGPU{
		AppID: app.ID, Count: 1, Vendor: "nvidia", Product: "NVIDIA-A10", Workloads: []string{"worker"},
	}); err != nil {
		t.Fatal(err)
	}
	spec, err := c.SpecFor(t.Context(), app, env, "registry/acme/web:1")
	if err != nil {
		t.Fatal(err)
	}
	if spec.GPU.Count != 1 || spec.GPU.Vendor != "nvidia" || spec.GPU.Product != "NVIDIA-A10" || len(spec.GPU.Workloads) != 1 {
		t.Errorf("the spec carries %+v", spec.GPU)
	}
	// The worker has it, the app itself does not.
	if spec.GPUs().Count != 0 {
		t.Error("the app was given its worker's GPU")
	}
	if kube.ProcessSpec(spec, "worker", "work", 1).GPUs().Count != 1 {
		t.Error("the worker was not given its GPU")
	}
}
