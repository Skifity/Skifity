package kube

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/restmapper"
	k8syaml "sigs.k8s.io/yaml"

	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Applying arbitrary manifests.
//
// The optional components (cert-manager, CloudNativePG, KEDA, Longhorn) publish
// a single YAML file per release. Applying those needs to resolve any kind to
// its API resource, including custom resources that did not exist a moment ago,
// so this path uses discovery rather than the fixed table in apply.go.

// yamlFetchTimeout bounds how long a component manifest download may take.
const yamlFetchTimeout = 2 * time.Minute

// manifestClient refuses to dial an address the panel has no business reaching.
//
// The URL below is a setting, which makes this the third place a value an
// administrator types becomes a request the panel's own process makes — after a
// Git connection's base URL and a notification webhook, both of which already
// go through netguard. This one did not, and what comes back is applied to the
// cluster as Kubernetes objects, so it is the worst of the three to have
// pointed at 169.254.169.254.
var manifestClient = netguard.Client(yamlFetchTimeout)

// ApplyYAML applies every document in a multi-document YAML stream.
//
// CustomResourceDefinitions are applied first and waited for, because the
// resources that use them are in the same file and would otherwise be rejected
// as unknown kinds.
func (c *Client) ApplyYAML(ctx context.Context, manifest []byte) error {
	documents, err := SplitYAML(manifest)
	if err != nil {
		return err
	}

	mapper, err := c.restMapper()
	if err != nil {
		return err
	}

	var crds, rest []*unstructured.Unstructured
	for _, doc := range documents {
		if doc.GetKind() == "CustomResourceDefinition" {
			crds = append(crds, doc)
		} else {
			rest = append(rest, doc)
		}
	}

	for _, crd := range crds {
		if err := c.applyUnstructured(ctx, mapper, crd); err != nil {
			return err
		}
	}
	if len(crds) > 0 {
		// The API server needs a moment to serve the new kinds, and the mapper
		// must be rebuilt or it will not know about them.
		if err := c.waitForCRDs(ctx, crds); err != nil {
			return err
		}
		mapper, err = c.restMapper()
		if err != nil {
			return err
		}
	}

	for _, doc := range rest {
		if err := c.applyUnstructured(ctx, mapper, doc); err != nil {
			return err
		}
	}
	return nil
}

// ApplyManifestURL downloads a manifest and applies it.
//
// The URL comes from configuration rather than being hardcoded, so an operator
// can pin a version or host it themselves on a network with no internet access.
func (c *Client) ApplyManifestURL(ctx context.Context, address string) error {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s is not an http or https address, and a manifest is fetched over one", address)
	}

	ctx, cancel := context.WithTimeout(ctx, yamlFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("build the request for %s: %w", address, err)
	}
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := manifestClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", address, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: the server answered %s", address, resp.Status)
	}

	// 64 MiB is far more than any of these manifests, and stops a wrong URL
	// from streaming something enormous into memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", address, err)
	}
	return c.ApplyYAML(ctx, body)
}

func (c *Client) applyUnstructured(ctx context.Context, mapper meta.RESTMapper, obj *unstructured.Unstructured) error {
	gvk := obj.GroupVersionKind()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("work out how to apply %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return fmt.Errorf("encode %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}

	options := metav1.PatchOptions{FieldManager: FieldManager, Force: ptr(true)}
	client := c.dynamic.Resource(mapping.Resource)

	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		namespace := obj.GetNamespace()
		if namespace == "" {
			namespace = "default"
		}
		_, err = client.Namespace(namespace).Patch(ctx, obj.GetName(), types.ApplyPatchType, data, options)
	} else {
		_, err = client.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, options)
	}
	if err != nil {
		return fmt.Errorf("apply %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}
	return nil
}

// waitForCRDs blocks until the API server is serving the new custom resources.
func (c *Client) waitForCRDs(ctx context.Context, crds []*unstructured.Unstructured) error {
	deadline := time.Now().Add(2 * time.Minute)
	crdGVR := schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}

	for _, crd := range crds {
		for {
			current, err := c.dynamic.Resource(crdGVR).Get(ctx, crd.GetName(), metav1.GetOptions{})
			if err == nil && crdEstablished(current) {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("the custom resource %s did not become available in two minutes", crd.GetName())
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

func crdEstablished(crd *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(crd.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] == "Established" && cond["status"] == "True" {
			return true
		}
	}
	return false
}

// restMapper builds a mapper from the cluster's current API surface.
func (c *Client) restMapper() (meta.RESTMapper, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(c.config)
	if err != nil {
		return nil, fmt.Errorf("build a discovery client: %w", err)
	}
	groups, err := restmapper.GetAPIGroupResources(discoveryClient)
	if err != nil {
		return nil, fmt.Errorf("read the cluster's API groups: %w", err)
	}
	return restmapper.NewDiscoveryRESTMapper(groups), nil
}

// SplitYAML parses a multi-document YAML stream into objects, skipping the empty
// documents that trailing separators and comment-only blocks produce.
func SplitYAML(manifest []byte) ([]*unstructured.Unstructured, error) {
	var out []*unstructured.Unstructured
	scanner := bufio.NewScanner(bytes.NewReader(manifest))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)

	var current bytes.Buffer
	flush := func() error {
		text := strings.TrimSpace(current.String())
		current.Reset()
		if text == "" {
			return nil
		}
		var obj unstructured.Unstructured
		if err := k8syaml.Unmarshal([]byte(text), &obj.Object); err != nil {
			return fmt.Errorf("parse a YAML document: %w", err)
		}
		if obj.GetKind() == "" {
			// A document with no kind is a comment block or an empty value.
			return nil
		}
		out = append(out, &obj)
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		// A separator is "---" at the start of a line, optionally followed by a
		// comment. A "---" inside a block scalar is indented, so this is safe.
		if strings.HasPrefix(line, "---") && strings.TrimSpace(strings.TrimPrefix(line, "---")) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the manifest: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// WaitForDeployment blocks until a Deployment in any namespace has at least one
// ready instance, which is how the panel knows a component is usable.
func (c *Client) WaitForDeployment(ctx context.Context, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && deployment.Status.ReadyReplicas > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("%s/%s did not appear within %s: %w", namespace, name, timeout, err)
			}
			return fmt.Errorf("%s/%s has no ready instances after %s", namespace, name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// WaitForJob blocks until a Job has either succeeded or failed.
//
// A Job is not a Deployment: it is not "ready", it finishes, and the difference
// between finishing and failing is the whole answer the caller wants.
func (c *Client) WaitForJob(ctx context.Context, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		job, err := c.clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil && job.Status.Succeeded > 0:
			return nil
		case err == nil && job.Status.Failed > 0:
			message := "it exited with an error"
			for _, cond := range job.Status.Conditions {
				if cond.Type == batchv1.JobFailed && cond.Message != "" {
					message = cond.Message
				}
			}
			return fmt.Errorf("%s/%s failed: %s", namespace, name, message)
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("%s/%s did not appear within %s: %w", namespace, name, timeout, err)
			}
			return fmt.Errorf("%s/%s had not finished after %s", namespace, name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
