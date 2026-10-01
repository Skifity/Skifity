package kube

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
)

// GPUs: reading them off the servers, what is in use, what an app's pods are
// given, and the device plugin the panel installs. No cluster: the fake
// clientset and rendered objects, as everywhere else here.

func gpuNode(name string, labels map[string]string, capacity map[corev1.ResourceName]string) corev1.Node {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	for name, quantity := range capacity {
		node.Status.Capacity[name] = resource.MustParse(quantity)
		node.Status.Allocatable[name] = resource.MustParse(quantity)
	}
	return node
}

func gpuPod(name, node string, phase corev1.PodPhase, gpus string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop"},
		Spec:       corev1.PodSpec{NodeName: node, Containers: []corev1.Container{{Name: "app"}}},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if gpus != "" {
		pod.Spec.Containers[0].Resources.Limits = corev1.ResourceList{ResourceNVIDIAGPU: resource.MustParse(gpus)}
	}
	return pod
}

func TestAServersGPUsAreReadFromWhatItsPluginAdvertises(t *testing.T) {
	node := gpuNode("gpu-1", map[string]string{
		LabelNVIDIAProduct: "NVIDIA-A10",
		LabelNVIDIAMemory:  "23028",
	}, map[corev1.ResourceName]string{ResourceNVIDIAGPU: "2", ResourceAMDGPU: "1"})
	// One card the plugin found and will not offer.
	node.Status.Allocatable[ResourceNVIDIAGPU] = resource.MustParse("1")

	gpus := NodeGPUs(node)
	if len(gpus) != 2 {
		t.Fatalf("read %d kinds of GPU, want NVIDIA and AMD: %+v", len(gpus), gpus)
	}
	nvidia, amd := gpus[0], gpus[1]
	if nvidia.Vendor != GPUVendorNVIDIA || nvidia.Resource != "nvidia.com/gpu" ||
		nvidia.Capacity != 2 || nvidia.Allocatable != 1 || nvidia.Product != "NVIDIA-A10" || nvidia.MemoryMB != 23028 {
		t.Errorf("the NVIDIA cards read as %+v", nvidia)
	}
	if amd.Vendor != GPUVendorAMD || amd.Allocatable != 1 || amd.Product != "" {
		t.Errorf("the AMD card read as %+v; a product label is NVIDIA's", amd)
	}
	if got := NodeGPUs(gpuNode("plain", nil, nil)); len(got) != 0 {
		t.Errorf("a server with no GPU has %+v", got)
	}
}

func TestAnNVIDIACardWithoutItsPluginIsSeenWhereAnythingCanSeeIt(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
		nfd    bool
	}{
		{"nfd by vendor", map[string]string{LabelNFDNVIDIA: "true"}, GPUSeenByNFD, true},
		{"nfd default, display", map[string]string{"feature.node.kubernetes.io/pci-0300_10de.present": "true"}, GPUSeenByNFD, true},
		{"nfd default, 3d", map[string]string{"feature.node.kubernetes.io/pci-0302_10de.present": "true"}, GPUSeenByNFD, true},
		{"nfd, and no NVIDIA card", map[string]string{"feature.node.kubernetes.io/pci-0300_1234.present": "true"}, "", true},
		{"gpu feature discovery", map[string]string{LabelNVIDIAPresent: "true"}, GPUSeenByDiscovery, false},
		{"marked by an administrator", map[string]string{"skifity.com/gpu": "nvidia"}, GPUSeenByLabel, false},
		{"nothing", map[string]string{"kubernetes.io/hostname": "a"}, "", false},
	}
	for _, c := range cases {
		if got := NVIDIAHardware(c.labels); got != c.want {
			t.Errorf("%s: seen by %q, want %q", c.name, got, c.want)
		}
		if got := HasNFD(c.labels); got != c.nfd {
			t.Errorf("%s: NFD %v, want %v", c.name, got, c.nfd)
		}
	}
}

func TestGPUsInUseAreWhatThePodsPlacedOnAServerAskedFor(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	initPeak := gpuPod("migrate-then-serve", "gpu-1", corev1.PodRunning, "1")
	initPeak.Spec.InitContainers = []corev1.Container{{
		Name: "warm", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{ResourceNVIDIAGPU: resource.MustParse("2")}},
	}}
	sidecar := gpuPod("with-sidecar", "gpu-2", corev1.PodRunning, "1")
	sidecar.Spec.InitContainers = []corev1.Container{{
		Name: "helper", RestartPolicy: &always,
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{ResourceNVIDIAGPU: resource.MustParse("1")}},
	}}
	pods := []corev1.Pod{
		*gpuPod("serving", "gpu-1", corev1.PodRunning, "1"),
		// Placed and still pulling its image: it holds the card already.
		*gpuPod("pulling", "gpu-1", corev1.PodPending, "1"),
		// Waiting for a server holds nothing anywhere.
		*gpuPod("waiting", "", corev1.PodPending, "1"),
		// Finished, in either direction, holds nothing.
		*gpuPod("done", "gpu-1", corev1.PodSucceeded, "4"),
		*gpuPod("crashed", "gpu-1", corev1.PodFailed, "4"),
		*gpuPod("no-gpu", "gpu-1", corev1.PodRunning, ""),
		*initPeak,
		*sidecar,
	}
	used := GPUsInUse(pods)
	// serving 1 + pulling 1 + max(1, init 2) = 4.
	if got := used["gpu-1"]["nvidia.com/gpu"]; got != 4 {
		t.Errorf("gpu-1 has %d in use, want 4", got)
	}
	// A sidecar runs beside the app and holds its card the whole time.
	if got := used["gpu-2"]["nvidia.com/gpu"]; got != 2 {
		t.Errorf("gpu-2 has %d in use, want 2", got)
	}
	if _, ok := used[""]; ok {
		t.Error("a pod waiting for a server was counted against no server")
	}
}

func TestTheServersPageReadsGPUsInUseAndThePluginsState(t *testing.T) {
	withGPU := gpuNode("gpu-1", map[string]string{LabelNFDNVIDIA: "true"},
		map[corev1.ResourceName]string{ResourceNVIDIAGPU: "2"})
	noPluginYet := gpuNode("gpu-2", map[string]string{"skifity.com/gpu": "nvidia"}, nil)
	plain := gpuNode("cpu-1", map[string]string{"feature.node.kubernetes.io/cpu-model.vendor_id": "Intel"}, nil)

	failing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-x", Namespace: GPUNamespace,
			Labels: map[string]string{"app.kubernetes.io/name": NVIDIADevicePluginName}},
		Spec: corev1.PodSpec{NodeName: "gpu-2"},
		Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "ContainerCreating", Message: `failed to get sandbox runtime: no runtime for "nvidia" is configured`,
			}},
		}}},
	}
	clientset := fake.NewClientset(&withGPU, &noPluginYet, &plain,
		gpuPod("serving", "gpu-1", corev1.PodRunning, "1"), failing)
	summary, err := NewClientWith(clientset, nil, "").Summary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, node := range summary.Nodes {
		nodes[node.Name] = node
	}

	gpu := nodes["gpu-1"]
	if len(gpu.GPUs) != 1 || gpu.GPUs[0].Allocatable != 2 || gpu.GPUs[0].InUse != 1 {
		t.Errorf("gpu-1's GPUs are %+v, want 2 with 1 in use", gpu.GPUs)
	}
	if gpu.GPUHardware != "" {
		t.Errorf("gpu-1's plugin offers its card, and it is still said to be missing (%q)", gpu.GPUHardware)
	}

	waiting := nodes["gpu-2"]
	if waiting.GPUHardware != GPUSeenByLabel || !waiting.GPULabelled {
		t.Errorf("gpu-2 was marked and reads %+v", waiting)
	}
	if waiting.GPUPlugin == nil || waiting.GPUPlugin.Ready || !strings.Contains(waiting.GPUPlugin.Reason, `runtime for "nvidia"`) {
		t.Errorf("gpu-2's plugin state is %+v, want the kubelet's reason", waiting.GPUPlugin)
	}

	cpu := nodes["cpu-1"]
	if len(cpu.GPUs) != 0 || cpu.GPUHardware != "" || !cpu.NFD || cpu.GPUPlugin != nil {
		t.Errorf("a server with no card and NFD reads %+v", cpu)
	}
}

// --- an app's pods ---

func withGPUs(g GPURequest) AppSpec {
	s := baseSpec()
	s.GPU = g
	s.SpreadAcrossServers = true
	return s
}

func gpuLimit(container corev1.Container, name corev1.ResourceName) int64 {
	q, ok := container.Resources.Limits[name]
	if !ok {
		return 0
	}
	return q.Value()
}

func TestAnAppsInstancesAreGivenTheirVendorsGPUs(t *testing.T) {
	cases := []struct {
		vendor   string
		resource corev1.ResourceName
		runtime  bool
	}{
		{GPUVendorNVIDIA, "nvidia.com/gpu", true},
		{GPUVendorAMD, "amd.com/gpu", false},
		{GPUVendorIntel, "gpu.intel.com/i915", false},
	}
	for _, c := range cases {
		deployment := BuildDeployment(withGPUs(GPURequest{Count: 2, Vendor: c.vendor}))
		pod := deployment.Spec.Template.Spec
		container := pod.Containers[0]

		if got := gpuLimit(container, c.resource); got != 2 {
			t.Errorf("%s: the container's limit of %s is %d, want 2", c.vendor, c.resource, got)
		}
		// The memory limit is still there beside it.
		if _, ok := container.Resources.Limits[corev1.ResourceMemory]; !ok {
			t.Errorf("%s: the GPU replaced the memory limit", c.vendor)
		}
		if c.runtime != (pod.RuntimeClassName != nil && *pod.RuntimeClassName == "nvidia") {
			t.Errorf("%s: runtimeClassName is %v", c.vendor, pod.RuntimeClassName)
		}
		if !slices.ContainsFunc(pod.Tolerations, func(tol corev1.Toleration) bool {
			return tol.Key == string(c.resource) && tol.Operator == corev1.TolerationOpExists && tol.Effect == corev1.TaintEffectNoSchedule
		}) {
			t.Errorf("%s: no toleration for the %s taint: %+v", c.vendor, c.resource, pod.Tolerations)
		}
		// The new instance needs the old one's card: down first, then up.
		rolling := deployment.Spec.Strategy.RollingUpdate
		if rolling == nil || rolling.MaxSurge.IntValue() != 0 || rolling.MaxUnavailable.IntValue() != 1 {
			t.Errorf("%s: the rollout surges, and would wait for a card the old instance holds: %+v", c.vendor, deployment.Spec.Strategy)
		}
		for _, env := range container.Env {
			if env.Name == "NVIDIA_VISIBLE_DEVICES" {
				t.Errorf("%s: the container sets NVIDIA_VISIBLE_DEVICES=%s, which is the plugin's to set", c.vendor, env.Value)
			}
		}
	}
}

func TestAGPUAppStaysAtTheStrictLevel(t *testing.T) {
	s := withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA})
	s.ImageBuiltHere = true
	pod := BuildDeployment(s).Spec.Template.Spec
	sc := pod.Containers[0].SecurityContext
	if sc.Privileged != nil && *sc.Privileged {
		t.Fatal("a GPU made the container privileged; the device plugin is what gives it the card")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("a GPU let the container escalate")
	}
	if sc.Capabilities == nil || !slices.Contains(sc.Capabilities.Drop, "ALL") || len(sc.Capabilities.Add) > 0 {
		t.Errorf("a GPU changed the capabilities: %+v", sc.Capabilities)
	}
	if pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Error("a GPU let the app run as root")
	}
	if pod.HostIPC || pod.HostPID || pod.HostNetwork {
		t.Error("a GPU put the app into the host's namespaces")
	}
	for _, volume := range pod.Volumes {
		if volume.HostPath != nil {
			t.Errorf("a GPU mounted the host's %s", volume.HostPath.Path)
		}
	}
}

func TestAPreferredModelIsAPreferenceBesideTheSpread(t *testing.T) {
	pod := BuildDeployment(withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA, Product: "NVIDIA-A10"})).Spec.Template.Spec
	if pod.Affinity == nil || pod.Affinity.PodAntiAffinity == nil {
		t.Fatal("the model preference replaced the spread across servers")
	}
	nodeAffinity := pod.Affinity.NodeAffinity
	if nodeAffinity == nil || nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		t.Fatalf("a model is a preference, and it was written as %+v", nodeAffinity)
	}
	preferred := nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(preferred) != 1 {
		t.Fatalf("preferences: %+v", preferred)
	}
	match := preferred[0].Preference.MatchExpressions[0]
	if match.Key != "nvidia.com/gpu.product" || match.Operator != corev1.NodeSelectorOpIn || !slices.Equal(match.Values, []string{"NVIDIA-A10"}) {
		t.Errorf("the preference is %+v", match)
	}
	// No model, no node affinity at all.
	if pod := BuildDeployment(withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA})).Spec.Template.Spec; pod.Affinity.NodeAffinity != nil {
		t.Errorf("an app preferring no model has %+v", pod.Affinity.NodeAffinity)
	}
}

func TestAnAppWithoutGPUsIsRenderedAsBefore(t *testing.T) {
	pod := BuildDeployment(baseSpec()).Spec.Template.Spec
	if pod.RuntimeClassName != nil || len(pod.Tolerations) != 0 {
		t.Errorf("an app with no GPU has runtimeClassName %v and tolerations %+v", pod.RuntimeClassName, pod.Tolerations)
	}
	for _, name := range []corev1.ResourceName{ResourceNVIDIAGPU, ResourceAMDGPU, ResourceIntelGPU} {
		if gpuLimit(pod.Containers[0], name) != 0 {
			t.Errorf("an app with no GPU asks for %s", name)
		}
	}
	if s := BuildDeployment(baseSpec()).Spec.Strategy.RollingUpdate; s.MaxSurge.IntValue() != 1 {
		t.Error("an app with no GPU lost its zero-downtime surge")
	}
}

func TestWhichWorkloadsGetTheGPUs(t *testing.T) {
	appOnly := withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA})
	if gpuLimit(BuildDeployment(appOnly).Spec.Template.Spec.Containers[0], ResourceNVIDIAGPU) != 1 {
		t.Error("the app itself was not given its GPU")
	}
	worker := BuildProcessDeployment(appOnly, "worker", "python work.py", 1)
	if gpuLimit(worker.Spec.Template.Spec.Containers[0], ResourceNVIDIAGPU) != 0 || worker.Spec.Template.Spec.RuntimeClassName != nil {
		t.Error("a process was given the app's GPU without being named")
	}

	workerOnly := withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA, Workloads: []string{"worker"}})
	if gpuLimit(BuildDeployment(workerOnly).Spec.Template.Spec.Containers[0], ResourceNVIDIAGPU) != 0 {
		t.Error("the app was given a GPU meant for its worker")
	}
	worker = BuildProcessDeployment(workerOnly, "worker", "python work.py", 3)
	pod := worker.Spec.Template.Spec
	if gpuLimit(pod.Containers[0], ResourceNVIDIAGPU) != 1 || pod.RuntimeClassName == nil {
		t.Error("the worker named was not given the GPU")
	}
	if other := BuildProcessDeployment(workerOnly, "clock", "python clock.py", 1); gpuLimit(other.Spec.Template.Spec.Containers[0], ResourceNVIDIAGPU) != 0 {
		t.Error("a process not named was given the GPU")
	}
}

func TestACommandNeverHasAGPU(t *testing.T) {
	// A release command runs while the version before it holds the card; one
	// that asked for it would wait for it, and the deploy with it.
	app := withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA, Workloads: []string{GPUWeb, "worker"}})
	for _, kind := range []string{RunKindRelease, RunKindOneOff, RunKindScheduled, RunKindSeed} {
		job, err := BuildRunJob(RunSpec{App: app, Name: "run-1", Command: "migrate", Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		pod := job.Spec.Template.Spec
		if gpuLimit(pod.Containers[0], ResourceNVIDIAGPU) != 0 || pod.RuntimeClassName != nil {
			t.Errorf("a %s command was given a GPU", kind)
		}
	}
}

func TestAGPURequestIsRefusedWhatCouldNeverWork(t *testing.T) {
	good := []GPURequest{
		{},
		{Count: 1, Vendor: GPUVendorNVIDIA},
		{Count: 16, Vendor: GPUVendorAMD},
		{Count: 1, Vendor: GPUVendorNVIDIA, Product: "Tesla-T4", Workloads: []string{GPUWeb, "celery-worker"}},
	}
	for _, g := range good {
		if err := ValidateGPURequest(g); err != nil {
			t.Errorf("%+v was refused: %v", g, err)
		}
	}
	bad := map[string]GPURequest{
		"negative":              {Count: -1, Vendor: GPUVendorNVIDIA},
		"too many":              {Count: 17, Vendor: GPUVendorNVIDIA},
		"no such vendor":        {Count: 1, Vendor: "voodoo"},
		"product for AMD":       {Count: 1, Vendor: GPUVendorAMD, Product: "MI300X"},
		"product not label":     {Count: 1, Vendor: GPUVendorNVIDIA, Product: "A10 with spaces"},
		"workload not named":    {Count: 1, Vendor: GPUVendorNVIDIA, Workloads: []string{"Not A Process"}},
		"release is no process": {Count: 1, Vendor: GPUVendorNVIDIA, Workloads: []string{"release"}},
	}
	for name, g := range bad {
		if err := ValidateGPURequest(g); err == nil {
			t.Errorf("%s: %+v was accepted", name, g)
		}
	}

	s := withGPUs(GPURequest{Count: 1, Vendor: GPUVendorNVIDIA})
	s.ScaleToZero = true
	s.Domains = []DomainSpec{{Hostname: "shop.example.com", TLS: true}}
	if err := s.Validate(); err == nil {
		t.Error("an app with a GPU that scales to zero was rendered")
	}
	s.GPU.Workloads = []string{"worker"}
	if err := s.Validate(); err != nil {
		t.Errorf("an app scaling to zero whose worker has the GPU was refused: %v", err)
	}
}

func TestTheDeviceVariableNeverReachesAnApp(t *testing.T) {
	for _, key := range []string{"NVIDIA_VISIBLE_DEVICES", "nvidia_visible_devices"} {
		if _, err := SanitiseEnvKey(key); err == nil {
			t.Errorf("%s was accepted as a variable", key)
		}
	}
	if _, err := SanitiseEnvKey("NVIDIA_DRIVER_CAPABILITIES"); err != nil {
		t.Errorf("a harmless NVIDIA variable was refused: %v", err)
	}
	// One stored before it was refused is left out of what the app reads.
	secret := BuildEnvSecret(baseSpec(), map[string]string{"NVIDIA_VISIBLE_DEVICES": "all", "LOG_LEVEL": "info"})
	if _, ok := secret.Data["NVIDIA_VISIBLE_DEVICES"]; ok {
		t.Error("the app's Secret hands it every card on the server")
	}
	if string(secret.Data["LOG_LEVEL"]) != "info" {
		t.Error("the other variables went with it")
	}
}

func TestWhyAGPUInstanceIsWaitingIsSaid(t *testing.T) {
	explanation := unschedulable("0/3 nodes are available: 1 Insufficient cpu, 2 Insufficient nvidia.com/gpu.")
	if explanation.Code != "scheduling_gpu" || !slices.Equal(explanation.Args, []string{"nvidia.com/gpu"}) {
		t.Errorf("explained as %+v", explanation)
	}
	if got := unschedulable("0/1 nodes are available: 1 Insufficient amd.com/gpu.").Code; got != "scheduling_gpu" {
		t.Errorf("an AMD shortage explained as %s", got)
	}
	sandbox := ExplainEvent("FailedCreatePodSandBox",
		`Failed to create pod sandbox: rpc error: code = Unknown desc = failed to get sandbox runtime: no runtime for "nvidia" is configured`)
	if sandbox.Code != "gpu_runtime_missing" {
		t.Errorf("a server without the NVIDIA runtime explained as %+v", sandbox)
	}
	if other := ExplainEvent("FailedCreatePodSandBox", "failed to setup network for sandbox"); other.Code != "sandbox_failed" {
		t.Errorf("an ordinary sandbox failure explained as %s", other.Code)
	}
}

// --- the device plugin ---

func TestTheDevicePluginIsPinnedAndConfinedToGPUServers(t *testing.T) {
	ds := BuildNVIDIADevicePlugin()
	if ds.Namespace != "kube-system" || ds.Name != NVIDIADevicePluginName {
		t.Errorf("the plugin is %s/%s", ds.Namespace, ds.Name)
	}
	pod := ds.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("%d containers", len(pod.Containers))
	}
	container := pod.Containers[0]

	// Pinned by version and by digest, like every image the panel runs.
	image := container.Image
	if !strings.HasPrefix(image, "nvcr.io/nvidia/k8s-device-plugin:"+NVIDIADevicePluginVersion+"@sha256:") ||
		len(image[strings.Index(image, "@sha256:")+len("@sha256:"):]) != 64 {
		t.Errorf("the image %q is not pinned by version and digest", image)
	}

	if pod.RuntimeClassName == nil || *pod.RuntimeClassName != "nvidia" {
		t.Error("the plugin does not run under the nvidia RuntimeClass, so it cannot read the cards")
	}

	// Only where there is a card: any of the labels that say so.
	terms := pod.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	keys := map[string]string{}
	for _, term := range terms {
		if len(term.MatchExpressions) != 1 {
			t.Errorf("a term with %d conditions is not an alternative: %+v", len(term.MatchExpressions), term)
			continue
		}
		keys[term.MatchExpressions[0].Key] = term.MatchExpressions[0].Values[0]
	}
	for key, value := range map[string]string{
		"feature.node.kubernetes.io/pci-10de.present":      "true",
		"feature.node.kubernetes.io/pci-0300_10de.present": "true",
		"feature.node.kubernetes.io/pci-0302_10de.present": "true",
		"nvidia.com/gpu.present":                           "true",
		"skifity.com/gpu":                                  "nvidia",
	} {
		if keys[key] != value {
			t.Errorf("the plugin is not placed on a server labelled %s=%s", key, value)
		}
	}

	// What it may do: NVIDIA's own manifest, and not a thing more.
	sc := container.SecurityContext
	if sc == nil || (sc.Privileged != nil && *sc.Privileged) {
		t.Error("the plugin is privileged")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("the plugin can escalate")
	}
	if sc.Capabilities == nil || !slices.Equal(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) || len(sc.Capabilities.Add) > 0 {
		t.Errorf("the plugin's capabilities are %+v", sc.Capabilities)
	}
	if pod.HostNetwork || pod.HostPID || pod.HostIPC {
		t.Error("the plugin shares a host namespace")
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("the plugin is given a token to the Kubernetes API it never calls")
	}
	var hostPaths []string
	for _, volume := range pod.Volumes {
		if volume.HostPath != nil {
			hostPaths = append(hostPaths, volume.HostPath.Path)
		}
	}
	if !slices.Equal(hostPaths, []string{"/var/lib/kubelet/device-plugins"}) {
		t.Errorf("the plugin mounts %v from the host; it needs the kubelet's device-plugin directory alone", hostPaths)
	}
	if !slices.ContainsFunc(pod.Tolerations, func(tol corev1.Toleration) bool { return tol.Key == "nvidia.com/gpu" }) {
		t.Error("the plugin does not tolerate the taint a GPU server carries, which is where it has to run")
	}

	// And it applies: the applier knows the kind.
	if _, err := Prepare(ds); err != nil {
		t.Fatal(err)
	}
	if _, err := NewApplier(nil).resourceFor(mustUnstructured(t, ds)); err != nil {
		t.Errorf("the applier cannot apply a DaemonSet: %v", err)
	}
}

func TestSomebodyElsesDevicePluginIsFound(t *testing.T) {
	theirs := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin-daemonset", Namespace: "gpu-operator"},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "plugin", Image: "nvcr.io/nvidia/k8s-device-plugin:v0.17.1",
		}}}}},
	}
	ours := BuildNVIDIADevicePlugin()
	unrelated := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "svclb-traefik", Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "lb", Image: "rancher/klipper-lb:v0.4.13",
		}}}}},
	}
	client := NewClientWith(fake.NewClientset(theirs, ours, unrelated), nil, "")
	foreign, err := client.ForeignNVIDIADevicePlugins(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(foreign, []string{"gpu-operator/nvidia-device-plugin-daemonset"}) {
		t.Errorf("found %v", foreign)
	}
}

func TestMarkingAServerTouchesOneLabel(t *testing.T) {
	node := gpuNode("gpu-1", map[string]string{"skifity.com/location": "fra1"}, nil)
	clientset := fake.NewClientset(&node)
	client := NewClientWith(clientset, nil, "")

	if err := client.SetNodeGPULabel(t.Context(), "gpu-1", true); err != nil {
		t.Fatal(err)
	}
	got, _ := clientset.CoreV1().Nodes().Get(t.Context(), "gpu-1", metav1.GetOptions{})
	if got.Labels["skifity.com/gpu"] != "nvidia" || got.Labels["skifity.com/location"] != "fra1" {
		t.Errorf("marked, the labels are %v", got.Labels)
	}
	if err := client.SetNodeGPULabel(t.Context(), "gpu-1", false); err != nil {
		t.Fatal(err)
	}
	got, _ = clientset.CoreV1().Nodes().Get(t.Context(), "gpu-1", metav1.GetOptions{})
	if _, ok := got.Labels["skifity.com/gpu"]; ok || got.Labels["skifity.com/location"] != "fra1" {
		t.Errorf("unmarked, the labels are %v", got.Labels)
	}
	if err := client.SetNodeGPULabel(t.Context(), "nowhere", true); err == nil || !IsNotFound(err) {
		t.Errorf("marking a server that is not there answered %v", err)
	}
}

func mustUnstructured(t *testing.T, obj any) *unstructured.Unstructured {
	t.Helper()
	u, err := ToUnstructured(obj)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
