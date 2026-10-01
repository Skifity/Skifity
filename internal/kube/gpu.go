package kube

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	"skifity/internal/version"
)

// GPUs.
//
// Kubernetes knows nothing about graphics cards. A vendor's device plugin, a
// DaemonSet on every server that has one, finds the cards and advertises them
// to the kubelet as an extended resource — nvidia.com/gpu, amd.com/gpu,
// gpu.intel.com/i915 — which then appears in the node's capacity like CPU and
// memory do. A container asks for one in resources.limits, the scheduler
// counts them the way it counts CPU, and at start the plugin tells the
// container runtime which card to hand over. Nothing about the container is
// privileged: the device node is mounted into it by the runtime, and the
// container's own security context — no root, no capabilities, the runtime's
// seccomp profile — is untouched. That is why an app with a GPU stays at the
// strict Pod Security level.
//
// NVIDIA needs one more piece. Its cards reach a container through the NVIDIA
// container runtime, which k3s finds on its own when the NVIDIA container
// toolkit is installed on the server and registers under the RuntimeClass
// "nvidia" (https://docs.k3s.io/advanced#nvidia-container-runtime, and the
// RuntimeClass itself is in k3s's manifests/runtimes.yaml). A pod that does not
// name that RuntimeClass is started by plain runc and sees no card at all,
// whatever it was allocated, so every NVIDIA pod here names it.
//
// What the panel does not do is install the driver or the toolkit on the
// server. Both are kernel- and distribution-specific, the driver usually wants
// a reboot, and a wrong one is a server that does not come back; docs/gpus.md
// has NVIDIA's commands. Coolify and Dokploy draw the same line — Dokploy's
// "GPU support" refuses to start until nvidia-smi and the runtime are there.

// The vendors an app can ask for.
const (
	GPUVendorNVIDIA = "nvidia"
	GPUVendorAMD    = "amd"
	GPUVendorIntel  = "intel"
)

// The extended resource each vendor's device plugin advertises.
const (
	ResourceNVIDIAGPU corev1.ResourceName = "nvidia.com/gpu"
	ResourceAMDGPU    corev1.ResourceName = "amd.com/gpu"
	ResourceIntelGPU  corev1.ResourceName = "gpu.intel.com/i915"
)

// gpuVendors is every vendor, in the order the panel lists them.
var gpuVendors = []struct {
	vendor   string
	resource corev1.ResourceName
}{
	{GPUVendorNVIDIA, ResourceNVIDIAGPU},
	{GPUVendorAMD, ResourceAMDGPU},
	{GPUVendorIntel, ResourceIntelGPU},
}

// GPUVendors lists the vendors an app can ask for.
func GPUVendors() []string {
	out := make([]string, 0, len(gpuVendors))
	for _, v := range gpuVendors {
		out = append(out, v.vendor)
	}
	return out
}

// GPUResource is the extended resource a vendor's cards are counted in.
func GPUResource(vendor string) (corev1.ResourceName, bool) {
	for _, v := range gpuVendors {
		if v.vendor == vendor {
			return v.resource, true
		}
	}
	return "", false
}

// NVIDIARuntimeClass is the RuntimeClass k3s creates for the NVIDIA container
// runtime.
const NVIDIARuntimeClass = "nvidia"

// Node labels that say something about a server's GPUs.
const (
	// LabelNVIDIAProduct and LabelNVIDIAMemory are written by NVIDIA's GPU
	// feature discovery, which the GPU Operator runs: the card's model, such
	// as NVIDIA-A10, and its memory in MiB.
	LabelNVIDIAProduct = "nvidia.com/gpu.product"
	LabelNVIDIAMemory  = "nvidia.com/gpu.memory"
	// LabelNVIDIAPresent is GPU feature discovery's own "there is one here".
	LabelNVIDIAPresent = "nvidia.com/gpu.present"
	// LabelNFDNVIDIA is Node Feature Discovery's label for a PCI device from
	// NVIDIA (vendor 10de), as the GPU Operator configures it: by vendor
	// alone. NFD's own default adds the device class first, pci-0300_10de
	// for a display controller and pci-0302_10de for a 3D one, which is what
	// a data-centre card is; nfdNVIDIAClassed reads those.
	LabelNFDNVIDIA = "feature.node.kubernetes.io/pci-10de.present"
	// nfdPrefix is every label Node Feature Discovery writes.
	nfdPrefix = "feature.node.kubernetes.io/"
)

// LabelGPU is the label an administrator puts on a server from the Servers
// page to say it has an NVIDIA GPU, for a cluster without Node Feature
// Discovery, where nothing else can tell before the device plugin runs.
var LabelGPU = version.LabelKey("gpu")

// nfdNVIDIAClassed are NFD's default labels for an NVIDIA display or 3D
// controller.
var nfdNVIDIAClassed = []string{
	"feature.node.kubernetes.io/pci-0300_10de.present",
	"feature.node.kubernetes.io/pci-0302_10de.present",
}

// NodeGPU is one kind of GPU a server advertises, and how much of it is taken.
type NodeGPU struct {
	Vendor   string
	Resource string
	// Capacity is how many the device plugin found; Allocatable how many it
	// offers, which is fewer when one is unhealthy.
	Capacity    int64
	Allocatable int64
	// InUse is the sum of what the pods placed on the server asked for,
	// which is what the scheduler counts against Allocatable.
	InUse int64
	// Product and MemoryMB come from GPU feature discovery's labels, when it
	// runs; NVIDIA only.
	Product  string
	MemoryMB int64
}

// How an NVIDIA GPU is known to be in a server whose device plugin does not
// advertise one. Empty is not known: without the plugin or a discovery tool,
// nothing in Kubernetes can see a PCI card.
const (
	GPUSeenByNFD       = "nfd"
	GPUSeenByDiscovery = "gpu-feature-discovery"
	GPUSeenByLabel     = "label"
)

// NodeGPUs reads the GPUs a node advertises, in vendor order. InUse is left
// for the caller, which has the pods.
func NodeGPUs(node corev1.Node) []NodeGPU {
	var out []NodeGPU
	for _, v := range gpuVendors {
		capacity := quantityOf(node.Status.Capacity, v.resource)
		allocatable := quantityOf(node.Status.Allocatable, v.resource)
		if capacity <= 0 && allocatable <= 0 {
			continue
		}
		gpu := NodeGPU{Vendor: v.vendor, Resource: string(v.resource), Capacity: capacity, Allocatable: allocatable}
		if v.vendor == GPUVendorNVIDIA {
			gpu.Product = node.Labels[LabelNVIDIAProduct]
			if mib, err := strconv.ParseInt(node.Labels[LabelNVIDIAMemory], 10, 64); err == nil && mib > 0 {
				gpu.MemoryMB = mib
			}
		}
		out = append(out, gpu)
	}
	return out
}

// NVIDIAHardware says how an NVIDIA GPU is known to be in a server, from its
// labels: Node Feature Discovery saw the PCI device, GPU feature discovery
// said so, or an administrator marked it. Empty when none of them did.
func NVIDIAHardware(labels map[string]string) string {
	switch {
	case labels[LabelNFDNVIDIA] == "true" || labels[nfdNVIDIAClassed[0]] == "true" || labels[nfdNVIDIAClassed[1]] == "true":
		return GPUSeenByNFD
	case labels[LabelNVIDIAPresent] == "true":
		return GPUSeenByDiscovery
	case labels[LabelGPU] == GPUVendorNVIDIA:
		return GPUSeenByLabel
	}
	return ""
}

// HasNFD reports whether Node Feature Discovery has labelled a server, which
// is what makes "it found no NVIDIA card" mean something.
func HasNFD(labels map[string]string) bool {
	for key := range labels {
		if strings.HasPrefix(key, nfdPrefix) {
			return true
		}
	}
	return false
}

func quantityOf(list corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := list[name]; ok {
		return q.Value()
	}
	return 0
}

// GPUsInUse sums what the pods on each server hold, by resource: the number
// the scheduler compares with a server's allocatable before it places
// another. A pod that has finished holds nothing; one waiting for a server
// holds nothing on any.
func GPUsInUse(pods []corev1.Pod) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	for _, pod := range pods {
		if pod.Spec.NodeName == "" || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		for _, v := range gpuVendors {
			n := podRequest(pod, v.resource)
			if n <= 0 {
				continue
			}
			if out[pod.Spec.NodeName] == nil {
				out[pod.Spec.NodeName] = map[string]int64{}
			}
			out[pod.Spec.NodeName][string(v.resource)] += n
		}
	}
	return out
}

// podRequest is what a pod asks for of one resource, the way the scheduler
// adds it up: its containers together, or its largest init container if that
// is more — an init container runs before the rest and holds what it asked for
// only while it does — with a sidecar counted with both, and the runtime's
// overhead on top.
func podRequest(pod corev1.Pod, name corev1.ResourceName) int64 {
	var containers, sidecars, initPeak int64
	for _, c := range pod.Spec.Containers {
		containers += containerRequest(c, name)
	}
	for _, c := range pod.Spec.InitContainers {
		asked := containerRequest(c, name)
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			sidecars += asked
			initPeak = max(initPeak, sidecars)
			continue
		}
		initPeak = max(initPeak, sidecars+asked)
	}
	total := max(containers+sidecars, initPeak)
	total += quantityOf(pod.Spec.Overhead, name)
	return total
}

// containerRequest is a container's request, or its limit when it gave only
// that: for an extended resource the API server makes them equal, and an
// object that has not been through it has only the limit.
func containerRequest(c corev1.Container, name corev1.ResourceName) int64 {
	if q, ok := c.Resources.Requests[name]; ok {
		return q.Value()
	}
	return quantityOf(c.Resources.Limits, name)
}

// --- an app's GPUs ---

// GPUWeb is the app itself among the workloads that can be given GPUs; any
// other name is one of its processes.
const GPUWeb = "web"

// MaxGPUs is the most one instance may ask for. The largest servers have
// eight or sixteen; a number past that is a typo.
const MaxGPUs = 16

// GPURequest is what each instance of an app's workloads is given.
type GPURequest struct {
	Count  int
	Vendor string
	// Product is a model to prefer, as GPU feature discovery writes it —
	// NVIDIA-A10, Tesla-T4. A preference, not a requirement: with none of
	// them free the app goes to another card of the vendor's rather than
	// waiting.
	Product string
	// Workloads names what gets them: GPUWeb for the app itself, or a
	// process's name. Empty is the app itself.
	Workloads []string
}

// Covers reports whether a workload is given the GPUs.
func (g GPURequest) Covers(workload string) bool {
	if g.Count <= 0 {
		return false
	}
	if len(g.Workloads) == 0 {
		return workload == GPUWeb
	}
	return slices.Contains(g.Workloads, workload)
}

// GPUs is what each of this Deployment's instances is given: the app's
// request when it covers this workload, and nothing otherwise.
func (s AppSpec) GPUs() GPURequest {
	workload := GPUWeb
	if s.Process != "" {
		workload = s.Process
	}
	if !s.GPU.Covers(workload) {
		return GPURequest{}
	}
	return s.GPU
}

// ValidateGPURequest reports a request the cluster could never honour for
// reasons that have nothing to do with its servers.
func ValidateGPURequest(g GPURequest) error {
	if g.Count < 0 || g.Count > MaxGPUs {
		return fmt.Errorf("an instance can be given from 0 to %d GPUs, not %d", MaxGPUs, g.Count)
	}
	if g.Count == 0 {
		return nil
	}
	if _, ok := GPUResource(g.Vendor); !ok {
		return fmt.Errorf("%q is not a GPU vendor; use one of %s", g.Vendor, strings.Join(GPUVendors(), ", "))
	}
	if g.Product != "" {
		if g.Vendor != GPUVendorNVIDIA {
			return fmt.Errorf("a GPU model can be preferred for NVIDIA cards only, which are the ones labelled with theirs")
		}
		if problems := validation.IsValidLabelValue(g.Product); len(problems) > 0 {
			return fmt.Errorf("%q is not a GPU model as servers are labelled with one, such as NVIDIA-A10: %s",
				g.Product, strings.Join(problems, "; "))
		}
	}
	for _, workload := range g.Workloads {
		if workload != GPUWeb && !ValidProcessName(workload) {
			return fmt.Errorf("%q is neither the app (%s) nor a name a process can have", workload, GPUWeb)
		}
	}
	return nil
}

// applyGPUs gives a pod the GPUs its app asked for.
//
// The count goes in limits alone, which is how Kubernetes documents an
// extended resource: the API server copies it into requests, and a request
// that differs from the limit is refused.
//
// The toleration is for the taint GPU servers commonly carry — the cloud
// providers put nvidia.com/gpu:NoSchedule on theirs so nothing else fills
// them — keyed on the resource's name, which is exactly what Kubernetes'
// own ExtendedResourceToleration admission plugin adds when it is switched
// on. It lets a pod onto such a server; it keeps nobody else off one.
func applyGPUs(podSpec *corev1.PodSpec, container *corev1.Container, g GPURequest) {
	if g.Count <= 0 {
		return
	}
	name, ok := GPUResource(g.Vendor)
	if !ok {
		return
	}
	if container.Resources.Limits == nil {
		container.Resources.Limits = corev1.ResourceList{}
	}
	container.Resources.Limits[name] = *resource.NewQuantity(int64(g.Count), resource.DecimalSI)

	podSpec.Tolerations = append(podSpec.Tolerations, corev1.Toleration{
		Key: string(name), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
	})
	if g.Vendor == GPUVendorNVIDIA {
		// Without it the card is allocated and never reaches the container:
		// runc knows nothing about NVIDIA's devices. Restricted Pod Security
		// has nothing to say about a RuntimeClass.
		podSpec.RuntimeClassName = ptr(NVIDIARuntimeClass)
		// Deliberately no NVIDIA_VISIBLE_DEVICES here. The device plugin sets
		// it to the cards it allocated, and the value k3s's example sets,
		// "all", would hand the container every card on the server whatever
		// it was counted for. See SanitiseEnvKey.
	}
	if g.Product != "" {
		if podSpec.Affinity == nil {
			podSpec.Affinity = &corev1.Affinity{}
		}
		podSpec.Affinity.NodeAffinity = &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
				Weight: 100,
				Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: LabelNVIDIAProduct, Operator: corev1.NodeSelectorOpIn, Values: []string{g.Product},
				}}},
			}},
		}
	}
}

// gpuStrategy is how an app with GPUs is rolled out: one instance at a time,
// the old one stopping before the new one starts.
//
// The usual surge — a new instance up before an old one goes — asks for a
// card while the old instance still holds it. On a server with one card, and
// on a cluster whose cards are all in use, which for GPUs is the normal case
// rather than the unlucky one, the new instance waits for a card that is only
// freed once it is ready, and the deploy never finishes. Going down first
// costs a single-instance app a few seconds of its address answering 503
// while the new version starts; the alternative was a deploy that hung.
func gpuStrategy() appsv1.DeploymentStrategy {
	maxSurge := intstr.FromInt32(0)
	maxUnavailable := intstr.FromInt32(1)
	return appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxSurge: &maxSurge, MaxUnavailable: &maxUnavailable,
		},
	}
}

// --- the NVIDIA device plugin ---

// The device plugin, pinned by version and by digest. The digest is the
// multi-architecture index of the tag (linux/amd64 and linux/arm64), read
// from nvcr.io on 2026-09-30; to move it, read the new tag's index digest the
// same way and change the two together, as builder/images.go says.
const (
	NVIDIADevicePluginVersion = "v0.20.1"
	NVIDIADevicePluginImage   = "nvcr.io/nvidia/k8s-device-plugin:" + NVIDIADevicePluginVersion +
		"@sha256:27c1b2553a690ca3d29889dcd61df97525d46f58272c0be8dc63c2b18f120075"
)

// NVIDIADevicePluginName is the DaemonSet's name. Not NVIDIA's own
// "nvidia-device-plugin-daemonset", so one somebody installed by hand is never
// taken over by the panel's; ForeignNVIDIADevicePlugins refuses to install
// beside one instead.
const NVIDIADevicePluginName = "skifity-nvidia-device-plugin"

// GPUNamespace is where the device plugin runs: kube-system, beside k3s's own
// node agents, as NVIDIA's static manifest has it.
//
// Not the panel's namespace. The plugin talks to the kubelet through a socket
// under /var/lib/kubelet/device-plugins on the host, which is a hostPath
// volume, and the baseline Pod Security level the panel's namespace enforces
// refuses every hostPath. kube-system carries no Pod Security label.
const GPUNamespace = "kube-system"

// kubeletDevicePlugins is where the kubelet listens for device plugins. k3s
// keeps its kubelet's root at /var/lib/kubelet, like every other distribution.
const kubeletDevicePlugins = "/var/lib/kubelet/device-plugins"

// NVIDIADevicePluginLabels select the plugin's pods.
func NVIDIADevicePluginLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       NVIDIADevicePluginName,
		"app.kubernetes.io/managed-by": version.Binary,
	}
}

// NVIDIANodeAffinity keeps the device plugin to the servers that have an
// NVIDIA card: any one of Node Feature Discovery's labels for one, GPU feature
// discovery's, or the one an administrator sets from the Servers page. The
// terms are alternatives.
//
// This matters more than tidiness. The plugin runs under the "nvidia"
// RuntimeClass, and a server without the NVIDIA container runtime has no
// handler of that name: the pod would sit in ContainerCreating there for
// ever, on every server that has no card.
func NVIDIANodeAffinity() *corev1.Affinity {
	is := func(key, value string) corev1.NodeSelectorTerm {
		return corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
			Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value},
		}}}
	}
	return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				is(LabelNFDNVIDIA, "true"),
				is(nfdNVIDIAClassed[0], "true"),
				is(nfdNVIDIAClassed[1], "true"),
				is(LabelNVIDIAPresent, "true"),
				is(LabelGPU, GPUVendorNVIDIA),
			},
		},
	}}
}

// BuildNVIDIADevicePlugin renders NVIDIA's device plugin as the panel runs it.
//
// It is NVIDIA's static manifest for the same version with four changes: a
// pinned image, the RuntimeClass k3s needs, the affinity above, and a name of
// its own. Its privileges are the upstream ones, and they are small: the
// container is not privileged, drops every capability, cannot escalate, and
// runs under the runtime's seccomp profile. It needs one thing from the host,
// the kubelet's device-plugin directory, to register with the kubelet and
// serve it over a socket there. The NVIDIA runtime then mounts the driver's
// libraries into it, which is how it reads the cards through NVML.
//
// It runs as root inside the container because the socket directory is
// root's on the host. The upstream manifest does too.
func BuildNVIDIADevicePlugin() *appsv1.DaemonSet {
	labels := NVIDIADevicePluginLabels()
	directory := corev1.HostPathDirectory
	return &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name: NVIDIADevicePluginName, Namespace: GPUNamespace, Labels: labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": NVIDIADevicePluginName}},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{Type: appsv1.RollingUpdateDaemonSetStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RuntimeClassName: ptr(NVIDIARuntimeClass),
					Affinity:         NVIDIANodeAffinity(),
					// A server whose cards are kept for GPU work is tainted so;
					// the plugin is the one thing that has to run there.
					Tolerations: []corev1.Toleration{{
						Key: string(ResourceNVIDIAGPU), Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
					}},
					// Evicted last when the server is short: without it, every
					// GPU on the server disappears from the scheduler's view.
					PriorityClassName: "system-node-critical",
					// It talks to the kubelet and nothing else.
					AutomountServiceAccountToken: ptr(false),
					Containers: []corev1.Container{{
						Name:            "nvidia-device-plugin",
						Image:           NVIDIADevicePluginImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name: "device-plugins", MountPath: kubeletDevicePlugins,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "device-plugins",
						VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
							Path: kubeletDevicePlugins, Type: &directory,
						}},
					}},
				},
			},
		},
	}
}

// IsNVIDIADevicePlugin reports whether a container image is NVIDIA's device
// plugin, whoever installed it.
func IsNVIDIADevicePlugin(image string) bool {
	return strings.Contains(image, "k8s-device-plugin") &&
		(strings.Contains(image, "nvidia") || strings.Contains(image, "nvcr.io"))
}

// SortGPUProducts sorts and de-duplicates model names, for a list to pick from.
func SortGPUProducts(products []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, product := range products {
		if product != "" && !seen[product] {
			seen[product] = true
			out = append(out, product)
		}
	}
	sort.Strings(out)
	return out
}
