package cluster

import (
	"context"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
)

// NVIDIAComponent is NVIDIA's device plugin, which is what "Enable GPUs"
// installs. See kube.BuildNVIDIADevicePlugin for what it runs and why there.
const NVIDIAComponent = "nvidia-device-plugin"

// installNVIDIADevicePlugin applies the device plugin's DaemonSet.
//
// It does not wait for it to start: on a cluster where no server is marked
// as having an NVIDIA card it has nowhere to run, which is not a failure, and
// where it does run, the Servers page says how each server's copy is doing.
//
// It refuses to go in beside somebody else's plugin — one applied by hand
// from NVIDIA's manifest, or the GPU Operator's — because two plugins
// registering the same cards with one kubelet take turns being the one it
// listens to, and the cards come and go from the scheduler's view with them.
func (c *Cluster) installNVIDIADevicePlugin(ctx context.Context) error {
	foreign, err := c.client.ForeignNVIDIADevicePlugins(ctx)
	if err != nil {
		return err
	}
	if len(foreign) > 0 {
		return errdoc.GPUDevicePluginExists(foreign)
	}
	c.log.Info("applying the NVIDIA device plugin", "image", kube.NVIDIADevicePluginImage)
	return c.client.Applier().Apply(ctx, kube.BuildNVIDIADevicePlugin())
}

// SetNodeGPULabel marks a server as having an NVIDIA card, or unmarks it.
func (c *Cluster) SetNodeGPULabel(ctx context.Context, node string, nvidia bool) error {
	if err := c.client.SetNodeGPULabel(ctx, node, nvidia); err != nil {
		if kube.IsUnreachable(err) {
			return errdoc.ClusterUnreachable(err)
		}
		if kube.IsNotFound(err) {
			return errdoc.NotFound("server", node)
		}
		return err
	}
	return nil
}

// nodeGPUs converts a node's GPUs for the API.
func nodeGPUs(in []kube.NodeGPU) []api.NodeGPU {
	out := make([]api.NodeGPU, 0, len(in))
	for _, gpu := range in {
		out = append(out, api.NodeGPU{
			Vendor: gpu.Vendor, Resource: gpu.Resource, Capacity: gpu.Capacity,
			Allocatable: gpu.Allocatable, InUse: gpu.InUse, Product: gpu.Product, MemoryMB: gpu.MemoryMB,
		})
	}
	return out
}

// pluginState converts the device plugin's state on a server for the API.
func pluginState(in *kube.GPUPluginState) *api.GPUPluginState {
	if in == nil {
		return nil
	}
	return &api.GPUPluginState{Ready: in.Ready, Reason: in.Reason}
}
