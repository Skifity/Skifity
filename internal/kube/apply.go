package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// FieldManager is the name Kubernetes records as the owner of the fields the
// panel sets. Server-side apply uses it to work out what to remove when an
// object stops being rendered, which is how a deleted domain's Ingress rule
// actually disappears.
const FieldManager = "skifity"

// gvrFor maps the kinds the panel renders onto their API resources.
//
// A hardcoded table rather than discovery: the set is small and fixed, and a
// discovery round trip on every apply is a cost with nothing to show for it.
// Custom resources are looked up by their own GroupVersionResource instead.
var gvrFor = map[string]schema.GroupVersionResource{
	"Namespace":               {Version: "v1", Resource: "namespaces"},
	"Secret":                  {Version: "v1", Resource: "secrets"},
	"ConfigMap":               {Version: "v1", Resource: "configmaps"},
	"Service":                 {Version: "v1", Resource: "services"},
	"PersistentVolumeClaim":   {Version: "v1", Resource: "persistentvolumeclaims"},
	"ResourceQuota":           {Version: "v1", Resource: "resourcequotas"},
	"LimitRange":              {Version: "v1", Resource: "limitranges"},
	"ServiceAccount":          {Version: "v1", Resource: "serviceaccounts"},
	"Deployment":              {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet":             {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"Ingress":                 {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	"NetworkPolicy":           {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	"HorizontalPodAutoscaler": {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
	"PodDisruptionBudget":     {Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
	"Job":                     {Group: "batch", Version: "v1", Resource: "jobs"},
	"CronJob":                 {Group: "batch", Version: "v1", Resource: "cronjobs"},
}

// ToUnstructured converts a typed object into the generic form the dynamic
// client works with.
func ToUnstructured(obj any) (*unstructured.Unstructured, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}
	var out unstructured.Unstructured
	if err := json.Unmarshal(data, &out.Object); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	if out.GetKind() == "" {
		return nil, fmt.Errorf("object has no kind, so it cannot be applied")
	}
	return &out, nil
}

// Applier applies objects with server-side apply.
type Applier struct {
	dynamic dynamic.Interface
	// extra holds resources beyond the built-in table, such as custom resources
	// belonging to the operators the panel installs.
	extra map[string]schema.GroupVersionResource
}

// NewApplier builds an Applier over a dynamic client.
func NewApplier(client dynamic.Interface) *Applier {
	return &Applier{
		dynamic: client,
		extra: map[string]schema.GroupVersionResource{
			// cert-manager
			"ClusterIssuer": {Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"},
			"Certificate":   {Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
			// CloudNativePG
			"Cluster":         {Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"},
			"ScheduledBackup": {Group: "postgresql.cnpg.io", Version: "v1", Resource: "scheduledbackups"},
			"Backup":          {Group: "postgresql.cnpg.io", Version: "v1", Resource: "backups"},
			// KEDA
			"ScaledObject":     {Group: "keda.sh", Version: "v1alpha1", Resource: "scaledobjects"},
			"HTTPScaledObject": {Group: "http.keda.sh", Version: "v1alpha1", Resource: "httpscaledobjects"},
			// Traefik
			"Middleware": {Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"},
		},
	}
}

// resourceFor finds the API resource for a kind.
func (a *Applier) resourceFor(u *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	kind := u.GetKind()
	gv, err := schema.ParseGroupVersion(u.GetAPIVersion())
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("object %s has an unreadable apiVersion %q: %w", kind, u.GetAPIVersion(), err)
	}
	if gvr, ok := gvrFor[kind]; ok && gvr.Group == gv.Group {
		return gvr, nil
	}
	if gvr, ok := a.extra[kind]; ok && gvr.Group == gv.Group {
		return gvr, nil
	}
	return schema.GroupVersionResource{}, fmt.Errorf("kind %s in %s is not one Skifity knows how to apply", kind, u.GetAPIVersion())
}

// Apply creates or updates an object.
//
// Force is set because the panel is the authority on the fields it manages: if
// someone edits a Deployment with kubectl, the next deploy puts it back rather
// than failing with a conflict the user cannot see the cause of.
func (a *Applier) Apply(ctx context.Context, obj any) error {
	u, err := ToUnstructured(obj)
	if err != nil {
		return err
	}
	gvr, err := a.resourceFor(u)
	if err != nil {
		return err
	}
	data, err := json.Marshal(u.Object)
	if err != nil {
		return fmt.Errorf("encode %s/%s: %w", u.GetKind(), u.GetName(), err)
	}

	client := a.dynamic.Resource(gvr)
	options := metav1.PatchOptions{FieldManager: FieldManager, Force: ptr(true)}

	if namespace := u.GetNamespace(); namespace != "" {
		_, err = client.Namespace(namespace).Patch(ctx, u.GetName(), types.ApplyPatchType, data, options)
	} else {
		_, err = client.Patch(ctx, u.GetName(), types.ApplyPatchType, data, options)
	}
	if err != nil {
		return fmt.Errorf("apply %s/%s: %w", u.GetKind(), u.GetName(), err)
	}
	return nil
}

// ApplyAll applies objects in order, stopping at the first failure.
//
// Order matters: a Secret must exist before the Deployment that reads it, or the
// pods start, fail and back off before the Secret arrives.
func (a *Applier) ApplyAll(ctx context.Context, objects ...any) error {
	for _, obj := range objects {
		// A nil pointer in the list means "this object does not apply to this
		// app", such as an Ingress for an app with no domains.
		if isNil(obj) {
			continue
		}
		if err := a.Apply(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes an object, treating "already gone" as success so that cleanup
// is safe to retry.
func (a *Applier) Delete(ctx context.Context, apiVersion, kind, namespace, name string) error {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind}}
	gvr, err := a.resourceFor(u)
	if err != nil {
		return err
	}
	policy := metav1.DeletePropagationForeground
	options := metav1.DeleteOptions{PropagationPolicy: &policy}

	if namespace != "" {
		err = a.dynamic.Resource(gvr).Namespace(namespace).Delete(ctx, name, options)
	} else {
		err = a.dynamic.Resource(gvr).Delete(ctx, name, options)
	}
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("delete %s/%s: %w", kind, name, err)
	}
	return nil
}

// DeleteAndWait removes an object and waits until it is actually gone.
//
// Delete returns as soon as the API server has accepted the request, and with
// foreground propagation the object stays — with a deletion timestamp and a
// finalizer — until its dependents are collected. Applying the same name in
// that window patches a corpse: the apply is accepted, the object disappears a
// moment later, and whatever was waiting for it waits for something that is not
// coming. That matters wherever a name is reused, which is every Job the panel
// recreates.
//
// A timeout is not a failure here. The caller wanted the name free; if it is
// still not free, saying so is more useful than applying anyway.
func (a *Applier) DeleteAndWait(ctx context.Context, apiVersion, kind, namespace, name string, timeout time.Duration) error {
	if err := a.Delete(ctx, apiVersion, kind, namespace, name); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, err := a.Get(ctx, apiVersion, kind, namespace, name); err != nil {
			if IsNotFound(err) {
				return nil
			}
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s/%s is still being deleted after %s", kind, name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Get fetches an object in its generic form.
func (a *Applier) Get(ctx context.Context, apiVersion, kind, namespace, name string) (*unstructured.Unstructured, error) {
	probe := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind}}
	gvr, err := a.resourceFor(probe)
	if err != nil {
		return nil, err
	}
	if namespace != "" {
		return a.dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return a.dynamic.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
}

// isNil reports whether an interface holds a nil pointer, which a plain
// `obj == nil` misses.
func isNil(obj any) bool {
	if obj == nil {
		return true
	}
	switch v := obj.(type) {
	case *unstructured.Unstructured:
		return v == nil
	}
	return isNilPointer(obj)
}
