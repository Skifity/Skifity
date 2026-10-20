package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// A Job whose name is reused has to be gone before the next one is applied, and
// "gone" is not what Delete returning nil means.
func TestDeleteAndWaitReturnsOnceTheObjectIsGone(t *testing.T) {
	job := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": "sweep", "namespace": "skifity-builds"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), job)
	applier := NewApplier(client)

	if err := applier.DeleteAndWait(context.Background(),
		"batch/v1", "Job", "skifity-builds", "sweep", 5*time.Second); err != nil {
		t.Fatalf("DeleteAndWait: %v", err)
	}
	if _, err := applier.Get(context.Background(), "batch/v1", "Job", "skifity-builds", "sweep"); !IsNotFound(err) {
		t.Fatalf("the job is still there: %v", err)
	}
}

// Deleting something that was never there is how cleanup stays safe to retry.
func TestDeleteAndWaitAcceptsSomethingAlreadyGone(t *testing.T) {
	applier := NewApplier(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	if err := applier.DeleteAndWait(context.Background(),
		"batch/v1", "Job", "skifity-builds", "sweep", time.Second); err != nil {
		t.Fatalf("DeleteAndWait: %v", err)
	}
}

// An object held by a finalizer never goes away, and the answer is to say so
// rather than to apply the new one on top of the dying one.
func TestDeleteAndWaitGivesUpRatherThanApplyingOverACorpse(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client.PrependReactor("get", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "batch/v1", "kind": "Job",
			"metadata": map[string]any{
				"name": "sweep", "namespace": "skifity-builds",
				"deletionTimestamp": metav1.Now().UTC().Format(time.RFC3339),
			},
		}}, nil
	})

	err := NewApplier(client).DeleteAndWait(context.Background(),
		"batch/v1", "Job", "skifity-builds", "sweep", 1500*time.Millisecond)
	if err == nil {
		t.Fatal("DeleteAndWait accepted an object that is still being deleted")
	}
	if !strings.Contains(err.Error(), "still being deleted") {
		t.Fatalf("the error does not say what happened: %v", err)
	}
}

// Switching autoscaling on used to drop a running app to one instance: the
// panel stops writing spec.replicas when an autoscaler owns it, server-side
// apply then removes the field, and Kubernetes defaults it to one. The size is
// claimed by a second owner first, at what it is now, so it survives the panel
// letting go.
func TestAnAutoscalerIsHandedTheSizeTheAppHasNow(t *testing.T) {
	live := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{
			"name": "web", "namespace": "acme-production",
			"managedFields": []any{map[string]any{
				"manager": FieldManager, "operation": "Apply", "apiVersion": "apps/v1",
				"fieldsType": "FieldsV1",
				"fieldsV1":   map[string]any{"f:spec": map[string]any{"f:replicas": map[string]any{}}},
			}},
		},
		"spec": map[string]any{"replicas": int64(3)},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live)
	var patches []k8stesting.PatchActionImpl
	client.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patches = append(patches, action.(k8stesting.PatchActionImpl))
		return true, live, nil
	})

	// What the panel renders for an autoscaled app: no size at all.
	autoscaled := map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": "acme-production"},
		"spec":     map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}}},
	}
	if err := NewApplier(client).Apply(context.Background(), autoscaled); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(patches) != 2 {
		t.Fatalf("%d patches were sent, want the hand-over and then the apply", len(patches))
	}
	handover, apply := patches[0], patches[1]
	if handover.PatchOptions.FieldManager != ReplicasHandoffManager {
		t.Errorf("the size was claimed by %q, want %q", handover.PatchOptions.FieldManager, ReplicasHandoffManager)
	}
	if !strings.Contains(string(handover.Patch), `"replicas":3`) {
		t.Errorf("the hand-over did not claim the size the app has now: %s", handover.Patch)
	}
	if apply.PatchOptions.FieldManager != FieldManager || strings.Contains(string(apply.Patch), "replicas") {
		t.Errorf("the panel's own apply should carry no size: %s by %q", apply.Patch, apply.PatchOptions.FieldManager)
	}
}

// Once the autoscaler owns the size, or when the app is new, there is nothing to
// hand over and only the apply is sent.
func TestNothingIsHandedOverWhenThePanelDoesNotOwnTheSize(t *testing.T) {
	for name, managed := range map[string][]any{
		"the autoscaler owns it": {map[string]any{
			"manager": "kube-controller-manager", "operation": "Update", "apiVersion": "apps/v1",
			"fieldsType": "FieldsV1",
			"fieldsV1":   map[string]any{"f:spec": map[string]any{"f:replicas": map[string]any{}}},
		}},
		"the panel owns other fields only": {map[string]any{
			"manager": FieldManager, "operation": "Apply", "apiVersion": "apps/v1",
			"fieldsType": "FieldsV1",
			"fieldsV1":   map[string]any{"f:spec": map[string]any{"f:template": map[string]any{}}},
		}},
	} {
		live := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "web", "namespace": "acme-production", "managedFields": managed},
			"spec":     map[string]any{"replicas": int64(5)},
		}}
		client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), live)
		var patches int
		client.PrependReactor("patch", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
			patches++
			return true, live, nil
		})
		err := NewApplier(client).Apply(context.Background(), map[string]any{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "web", "namespace": "acme-production"},
		})
		if err != nil {
			t.Fatalf("%s: Apply: %v", name, err)
		}
		if patches != 1 {
			t.Errorf("%s: %d patches were sent, want only the apply", name, patches)
		}
	}
}
