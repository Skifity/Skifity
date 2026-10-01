package kube

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

// What the cluster says about its servers' GPUs, for the Servers page.

// GPUPluginState is the panel's device plugin on one server.
type GPUPluginState struct {
	// Ready is true when the plugin is running there and has registered.
	Ready bool
	// Reason is why it is not, in the kubelet's words: a crash loop, or a
	// sandbox the "nvidia" runtime could not create.
	Reason string
}

// advertises reports whether a vendor's cards are among a server's.
func advertises(gpus []NodeGPU, vendor string) bool {
	for _, gpu := range gpus {
		if gpu.Vendor == vendor {
			return true
		}
	}
	return false
}

// describeGPUs fills in what a server's GPUs are being used for, and where
// the panel's device plugin stands on each server that should have it.
//
// Two list calls, both skipped when there is nothing to say: the pods of the
// whole cluster only when some server advertises a card, and the plugin's own
// pods, which are few. A failure of either leaves the numbers out rather than
// failing the page they are on.
func (c *Client) describeGPUs(ctx context.Context, nodes []Node) {
	anyGPU, anyNVIDIA := false, false
	for _, node := range nodes {
		anyGPU = anyGPU || len(node.GPUs) > 0
		anyNVIDIA = anyNVIDIA || node.GPUHardware != "" || advertises(node.GPUs, GPUVendorNVIDIA)
	}
	if anyGPU {
		// Every phase, filtered here: a pod that has been placed and is still
		// pulling its image holds its card as surely as a running one.
		if pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
			used := GPUsInUse(pods.Items)
			for i := range nodes {
				for j := range nodes[i].GPUs {
					nodes[i].GPUs[j].InUse = used[nodes[i].Name][nodes[i].GPUs[j].Resource]
				}
			}
		}
	}
	if anyNVIDIA {
		states := c.devicePluginStates(ctx)
		for i := range nodes {
			if state, ok := states[nodes[i].Name]; ok {
				nodes[i].GPUPlugin = &state
			}
		}
	}
}

// devicePluginStates reads the panel's device plugin pods, by server.
func (c *Client) devicePluginStates(ctx context.Context) map[string]GPUPluginState {
	out := map[string]GPUPluginState{}
	selector := labels.SelectorFromSet(map[string]string{"app.kubernetes.io/name": NVIDIADevicePluginName}).String()
	pods, err := c.clientset.CoreV1().Pods(GPUNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return out
	}
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "" {
			continue
		}
		out[pod.Spec.NodeName] = pluginState(pod)
	}
	return out
}

// pluginState is whether a device plugin pod is up, and why not when it is
// not, in the words that say what to do.
func pluginState(pod corev1.Pod) GPUPluginState {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return GPUPluginState{Ready: true}
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if waiting := status.State.Waiting; waiting != nil && waiting.Reason != "" {
			if waiting.Message != "" {
				return GPUPluginState{Reason: waiting.Reason + ": " + waiting.Message}
			}
			return GPUPluginState{Reason: waiting.Reason}
		}
		if terminated := status.LastTerminationState.Terminated; terminated != nil && terminated.Message != "" {
			return GPUPluginState{Reason: terminated.Message}
		}
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Status != corev1.ConditionTrue && condition.Message != "" {
			return GPUPluginState{Reason: condition.Message}
		}
	}
	return GPUPluginState{Reason: string(pod.Status.Phase)}
}

// SetNodeGPULabel marks a server as having an NVIDIA card, or unmarks it,
// which is what puts the device plugin there on a cluster that has nothing
// else to tell it by. A merge patch of the one label, so a label somebody else
// put on the server is never touched.
func (c *Client) SetNodeGPULabel(ctx context.Context, node string, nvidia bool) error {
	var value any // null removes it
	if nvidia {
		value = GPUVendorNVIDIA
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"labels": map[string]any{LabelGPU: value}},
	})
	if err != nil {
		return err
	}
	if _, err := c.clientset.CoreV1().Nodes().Patch(ctx, node, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("label the server %s: %w", node, err)
	}
	return nil
}

// ForeignNVIDIADevicePlugins names NVIDIA device plugins in the cluster that
// are not the panel's, as namespace/name: one installed by hand from NVIDIA's
// manifest, or the GPU Operator's. Two plugins advertising the same cards to
// one kubelet take turns being the one it listens to.
func (c *Client) ForeignNVIDIADevicePlugins(ctx context.Context) ([]string, error) {
	sets, err := c.clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list the cluster's DaemonSets: %w", err)
	}
	var out []string
	for _, set := range sets.Items {
		if set.Namespace == GPUNamespace && set.Name == NVIDIADevicePluginName {
			continue
		}
		for _, container := range set.Spec.Template.Spec.Containers {
			if IsNVIDIADevicePlugin(container.Image) {
				out = append(out, set.Namespace+"/"+set.Name)
				break
			}
		}
	}
	return out, nil
}
