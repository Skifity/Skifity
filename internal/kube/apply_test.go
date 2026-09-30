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
