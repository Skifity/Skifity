package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"skifity/internal/version"
)

// Client talks to the Kubernetes API.
type Client struct {
	clientset kubernetes.Interface
	// streams is the same connection with no request deadline, used for calls
	// whose response body is read for as long as somebody watches it.
	streams kubernetes.Interface
	dynamic dynamic.Interface
	metrics metricsv.Interface
	applier *Applier
	// systemNamespace is where the panel and the components it installs live.
	systemNamespace string
	config          *rest.Config
}

// Options configure a Client.
type Options struct {
	// KubeconfigPath is empty when running inside the cluster, where the
	// ServiceAccount token is used instead.
	KubeconfigPath  string
	SystemNamespace string
}

// NewClient connects to Kubernetes.
func NewClient(opts Options) (*Client, error) {
	config, err := buildConfig(opts.KubeconfigPath)
	if err != nil {
		return nil, err
	}
	// The panel makes many small calls; the client-go defaults (5 QPS) throttle
	// a deploy noticeably.
	config.QPS = 50
	config.Burst = 100
	config.UserAgent = version.UserAgent()

	// Two clients from one connection, because one timeout cannot be right for
	// both kinds of call this panel makes.
	//
	// Everything ordinary is a request that either answers in a moment or has
	// gone wrong, and a deadline on it is what keeps a page from hanging on an
	// API server that stopped replying.
	//
	// A log stream is the opposite: the request succeeds immediately and the
	// body is then read for as long as somebody is watching. rest.Config's
	// Timeout is the HTTP client's, which bounds the whole exchange including
	// that body, so a single deadline here cut every followed log at thirty
	// seconds. The browser reconnected and replayed the last two hundred
	// lines, so live logs repeated themselves every half minute and a build
	// log stopped halfway through a build.
	shortConfig := *config
	shortConfig.Timeout = 30 * time.Second

	clientset, err := kubernetes.NewForConfig(&shortConfig)
	if err != nil {
		return nil, fmt.Errorf("build Kubernetes client: %w", err)
	}
	// No deadline at all: a stream ends when the caller's context does, when
	// the pod stops writing, or when the connection breaks.
	streams, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("build Kubernetes streaming client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("build dynamic Kubernetes client: %w", err)
	}
	// Metrics are optional: a cluster without metrics-server still works, it
	// just cannot show usage or autoscale.
	metricsClient, err := metricsv.NewForConfig(config)
	if err != nil {
		metricsClient = nil
	}

	systemNamespace := opts.SystemNamespace
	if systemNamespace == "" {
		systemNamespace = "skifity-system"
	}
	return &Client{
		clientset:       clientset,
		streams:         streams,
		dynamic:         dyn,
		metrics:         metricsClient,
		applier:         NewApplier(dyn),
		systemNamespace: systemNamespace,
		config:          config,
	}, nil
}

func buildConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("read kubeconfig %s: %w", kubeconfigPath, err)
		}
		return config, nil
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("the panel is not running inside a cluster and no kubeconfig was configured: %w", err)
	}
	return config, nil
}

// Applier exposes the server-side apply helper to the orchestrators.
func (c *Client) Applier() *Applier { return c.applier }

// Clientset exposes the typed client for the few places that need it.
func (c *Client) Clientset() kubernetes.Interface { return c.clientset }

// StreamClientset is the client to read a log with.
//
// It is the same connection with no request deadline, so following a log is
// bounded by the caller's context rather than by a timeout meant for calls that
// answer in a moment. It falls back to the ordinary client, which is what a
// test that builds a Client around a fake clientset has.
func (c *Client) StreamClientset() kubernetes.Interface {
	if c.streams != nil {
		return c.streams
	}
	return c.clientset
}

// SystemNamespace is where the panel and its components run.
func (c *Client) SystemNamespace() string { return c.systemNamespace }

// BuildsNamespace is where builds and the things they need live.
//
// Not the panel's own namespace, for two reasons that point the same way. A
// build runs code from a repository, and next to the panel it would share a
// namespace with the master key, the database and the panel's service account.
// And rootless BuildKit needs a Pod Security profile the panel's namespace
// must not have: giving the whole panel namespace that profile to make one
// builder work would be exactly backwards.
//
// It is a fixed name rather than one derived from the panel's namespace,
// because every node's container runtime is configured to reach the registry
// inside it, and that configuration is written before the panel exists.
const BuildsNamespace = "skifity-builds"

// BuildNamespace is where builds run.
func (c *Client) BuildNamespace() string { return BuildsNamespace }

// RESTConfig exposes the connection settings, which exec and port-forward need.
func (c *Client) RESTConfig() *rest.Config { return c.config }

// Ping reports whether the Kubernetes API is reachable.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Listing one namespace is the cheapest call that proves both connectivity
	// and that our credentials still work.
	_, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("reach the Kubernetes API: %w", err)
	}
	return nil
}

// Version reports the cluster's Kubernetes version.
func (c *Client) Version(ctx context.Context) (string, error) {
	info, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return "", fmt.Errorf("read the cluster version: %w", err)
	}
	return info.GitVersion, nil
}

// nodeUsage is the per-node usage read from metrics-server.
type nodeUsage struct {
	cpuMilli int64
	memoryMB int64
}

// Summary describes the cluster for the dashboard.
func (c *Client) Summary(ctx context.Context) (Summary, error) {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Summary{}, fmt.Errorf("list cluster nodes: %w", err)
	}

	usage := c.nodeUsage(ctx)
	podCounts := c.podCountsByNode(ctx)

	out := Summary{Reachable: true}
	if v, err := c.Version(ctx); err == nil {
		out.KubernetesVersion = v
	}

	controlPlanes := 0
	for _, node := range nodes.Items {
		info := describeNode(node)
		if u, ok := usage[node.Name]; ok {
			info.CPUUsedM = u.cpuMilli
			info.MemUsedMB = u.memoryMB
		}
		info.PodCount = podCounts[node.Name]

		out.Nodes = append(out.Nodes, info)
		out.TotalCPUM += info.CPUCapacityM
		out.TotalMemoryMB += info.MemCapacityMB
		out.UsedCPUM += info.CPUUsedM
		out.UsedMemoryMB += info.MemUsedMB
		if info.Ready {
			out.ReadyNodes++
		}
		for _, role := range info.Roles {
			if role == "control-plane" || role == "master" {
				controlPlanes++
			}
		}
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].Name < out.Nodes[j].Name })
	// Embedded etcd needs three members to survive losing one.
	out.HighAvailability = controlPlanes >= 3
	return out, nil
}

// Summary mirrors the API layer's ClusterSummary. It is defined here too so the
// kube package does not import the api package, which would be a cycle.
type Summary struct {
	Reachable         bool
	KubernetesVersion string
	Nodes             []Node
	ReadyNodes        int
	TotalCPUM         int64
	TotalMemoryMB     int64
	UsedCPUM          int64
	UsedMemoryMB      int64
	HighAvailability  bool
	Message           string
}

// Node is one cluster node.
type Node struct {
	Name          string
	Ready         bool
	Reason        string
	Roles         []string
	InternalIP    string
	ExternalIP    string
	OS            string
	Architecture  string
	KubeletVer    string
	CPUCapacityM  int64
	MemCapacityMB int64
	CPUUsedM      int64
	MemUsedMB     int64
	PodCount      int
	Labels        map[string]string
	Schedulable   bool
}

func describeNode(node corev1.Node) Node {
	info := Node{
		Name:         node.Name,
		OS:           node.Status.NodeInfo.OSImage,
		Architecture: node.Status.NodeInfo.Architecture,
		KubeletVer:   node.Status.NodeInfo.KubeletVersion,
		Labels:       node.Labels,
		Schedulable:  !node.Spec.Unschedulable,
	}
	if cpu := node.Status.Allocatable.Cpu(); cpu != nil {
		info.CPUCapacityM = cpu.MilliValue()
	}
	if mem := node.Status.Allocatable.Memory(); mem != nil {
		info.MemCapacityMB = mem.Value() / (1024 * 1024)
	}
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case corev1.NodeInternalIP:
			info.InternalIP = addr.Address
		case corev1.NodeExternalIP:
			info.ExternalIP = addr.Address
		}
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			info.Ready = cond.Status == corev1.ConditionTrue
			if !info.Ready {
				// The message is what tells a user why a server went away.
				info.Reason = cond.Message
			}
		}
	}
	for label := range node.Labels {
		if role, ok := strings.CutPrefix(label, "node-role.kubernetes.io/"); ok && role != "" {
			info.Roles = append(info.Roles, role)
		}
	}
	if len(info.Roles) == 0 {
		info.Roles = []string{"worker"}
	}
	sort.Strings(info.Roles)
	return info
}

func (c *Client) nodeUsage(ctx context.Context) map[string]nodeUsage {
	out := map[string]nodeUsage{}
	if c.metrics == nil {
		return out
	}
	list, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		// metrics-server may not be ready yet, which is not an error worth
		// failing the whole dashboard for.
		return out
	}
	for _, item := range list.Items {
		out[item.Name] = nodeUsage{
			cpuMilli: item.Usage.Cpu().MilliValue(),
			memoryMB: item.Usage.Memory().Value() / (1024 * 1024),
		}
	}
	return out
}

// podUsage reads what each of an app's instances is using right now.
//
// A pod's usage is the sum of its containers': the panel shows one number per
// instance, because "which of the two containers in this pod" is a Kubernetes
// question and the product does not ask them.
func (c *Client) podUsage(ctx context.Context, namespace string) map[string]nodeUsage {
	out := map[string]nodeUsage{}
	if c.metrics == nil {
		return out
	}
	// The whole namespace, matched by pod name against the app's own pods.
	// metrics-server's label filtering has never been something to rely on,
	// and a namespace holds a handful of pods.
	list, err := c.metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// metrics-server is optional and may not be ready. A missing number is
		// shown as unknown, which is true, rather than failing the page.
		return out
	}
	for _, item := range list.Items {
		var used nodeUsage
		for _, container := range item.Containers {
			used.cpuMilli += container.Usage.Cpu().MilliValue()
			used.memoryMB += container.Usage.Memory().Value() / (1024 * 1024)
		}
		out[item.Name] = used
	}
	return out
}

// MetricsAvailable reports whether the cluster is serving resource metrics.
//
// A HorizontalPodAutoscaler that scales on CPU or memory reads them from the
// metrics API. Without it the HPA sits at `<unknown>/70%` and never scales, and
// nothing in Kubernetes says so out loud — it is the most common reason
// autoscaling silently does nothing. k3s ships metrics-server by default, so
// this is false when an operator disabled it, brought their own cluster, or it
// is crash-looping on a small node.
func (c *Client) MetricsAvailable(ctx context.Context) bool {
	if c.metrics == nil {
		return false
	}
	list, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	return err == nil && len(list.Items) > 0
}

func (c *Client) podCountsByNode(ctx context.Context) map[string]int {
	out := map[string]int{}
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return out
	}
	for _, pod := range pods.Items {
		out[pod.Spec.NodeName]++
	}
	return out
}

// AppStatus describes one app's live state in the panel's own words.
func (c *Client) AppStatus(ctx context.Context, namespace, appSlug string) (AppStatus, error) {
	deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, appSlug, metav1.GetOptions{})
	if err != nil {
		if IsNotFound(err) {
			return AppStatus{Phase: "not_deployed", Detail: "This app has not been deployed yet."}, nil
		}
		return AppStatus{}, fmt.Errorf("read the app's Deployment: %w", err)
	}

	status := AppStatus{
		DesiredReplicas: int(deployment.Status.Replicas),
		ReadyReplicas:   int(deployment.Status.ReadyReplicas),
	}
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		status.Image = deployment.Spec.Template.Spec.Containers[0].Image
	}

	selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels).String()
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return status, fmt.Errorf("list the app's instances: %w", err)
	}
	// What each instance is actually using. The fields existed from the start
	// and nothing ever filled them, so the panel showed an em-dash where the
	// number that decides whether to scale should be.
	usage := c.podUsage(ctx, namespace)
	for _, pod := range pods.Items {
		instance := describePod(pod)
		if used, ok := usage[pod.Name]; ok {
			instance.CPUM, instance.MemoryMB = used.cpuMilli, used.memoryMB
		}
		status.Instances = append(status.Instances, instance)
	}
	sort.Slice(status.Instances, func(i, j int) bool { return status.Instances[i].Name < status.Instances[j].Name })

	status.Phase, status.Detail = summarisePhase(deployment, status)
	return status, nil
}

// AppStatus mirrors the API layer's AppRuntimeStatus.
type AppStatus struct {
	Phase           string
	Detail          string
	DesiredReplicas int
	ReadyReplicas   int
	Instances       []Instance
	Image           string
}

// Instance is one running pod, described the way the UI shows it.
type Instance struct {
	Name      string
	Status    string
	Ready     bool
	Restarts  int
	Node      string
	StartedAt time.Time
	Message   string
	CPUM      int64
	MemoryMB  int64
}

func describePod(pod corev1.Pod) Instance {
	inst := Instance{Name: pod.Name, Node: pod.Spec.NodeName, Status: string(pod.Status.Phase)}
	if pod.Status.StartTime != nil {
		inst.StartedAt = pod.Status.StartTime.Time
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			inst.Ready = cond.Status == corev1.ConditionTrue
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		inst.Restarts += int(cs.RestartCount)
		switch {
		case cs.State.Waiting != nil:
			// The waiting reason is the single most useful thing on this
			// screen: CrashLoopBackOff, ImagePullBackOff, CreateContainerError.
			inst.Status = cs.State.Waiting.Reason
			inst.Message = cs.State.Waiting.Message
			if RunsAsRootRefusal(inst.Message) {
				inst.Message = ExplainImageRunsAsRoot()
			}
		case cs.State.Terminated != nil:
			inst.Status = cs.State.Terminated.Reason
			inst.Message = cs.State.Terminated.Message
		}
	}
	// A pod nobody could place carries the reason on its own condition, and it
	// is the one the panel used to throw away and replace with a guess.
	if pod.Status.Phase == corev1.PodPending {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				inst.Status = "Unschedulable"
				inst.Message = ExplainUnschedulable(cond.Message)
			}
		}
	}

	// A pod that is being deleted shows as Running, which is confusing while a
	// deploy is finishing.
	if pod.DeletionTimestamp != nil {
		inst.Status = "Terminating"
		inst.Ready = false
	}
	return inst
}

// summarisePhase turns the Deployment's conditions into one sentence.
func summarisePhase(deployment *appsv1.Deployment, status AppStatus) (string, string) {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}

	for _, cond := range deployment.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse {
			return "failed", "The rollout did not finish: " + cond.Message
		}
		// A quota refusal lands here and nowhere else: the pod was never
		// created, so there is nothing Pending to look at and the instance
		// list is simply empty.
		if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue {
			return "failed", ExplainReplicaFailure(cond.Message)
		}
	}

	switch {
	case desired == 0:
		return "stopped", "This app is scaled to zero instances."
	case status.ReadyReplicas == 0 && len(status.Instances) > 0:
		for _, inst := range status.Instances {
			if strings.Contains(inst.Status, "CrashLoop") {
				return "crashing", "The app starts and then exits. The last log lines usually say why."
			}
			if strings.Contains(inst.Status, "ImagePull") || strings.Contains(inst.Status, "ErrImage") {
				return "failed", ExplainImagePull(inst.Message)
			}
			// describePod has already replaced the kubelet's wording, so this
			// matches what the panel will show rather than what it was sent.
			if inst.Message == ExplainImageRunsAsRoot() {
				return "failed", inst.Message
			}
			// describePod has already turned the scheduler's own message into
			// a sentence with the fix in it. Replacing that with a guess about
			// CPU and memory, which is what used to happen, sent people to add
			// a server for a problem that was a quota or a volume.
			if inst.Status == "Unschedulable" {
				return "pending", inst.Message
			}
			if inst.Status == "Pending" {
				return "pending", "The instance has been placed and is starting."
			}
		}
		return "starting", "The instances are starting."
	case status.ReadyReplicas == 0:
		return "pending", "No instances are running yet."
	case int32(status.ReadyReplicas) < desired:
		return "updating", fmt.Sprintf("%d of %d instances are ready.", status.ReadyReplicas, desired)
	default:
		return "running", fmt.Sprintf("%d instance(s) running.", status.ReadyReplicas)
	}
}

// LogOptions is what to read, and from where.
type LogOptions struct {
	// TailLines bounds how far back to read. Zero means everything the node
	// still has.
	TailLines int64
	// Follow keeps the stream open.
	Follow bool
	// Previous reads the container that ran before the current one.
	//
	// This is the only copy of why a crash-looping app crashed: the container
	// that printed the reason has already been replaced, and its log is gone
	// from the live stream the moment the new one starts.
	Previous bool
}

// AppLogs streams the logs of an app's instances.
func (c *Client) AppLogs(ctx context.Context, namespace, appSlug string, opts LogOptions) (io.ReadCloser, error) {
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{"app.kubernetes.io/name": appSlug}).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("find the app's instances: %w", err)
	}
	if len(pods.Items) == 0 {
		return io.NopCloser(strings.NewReader("")), nil
	}

	target := pickLogPod(pods.Items, opts.Previous)

	options := &corev1.PodLogOptions{
		Follow:     opts.Follow && !opts.Previous,
		Previous:   opts.Previous,
		Timestamps: false,
	}
	if opts.TailLines > 0 {
		options.TailLines = &opts.TailLines
	}
	stream, err := c.StreamClientset().CoreV1().Pods(namespace).GetLogs(target.Name, options).Stream(ctx)
	if err != nil {
		if opts.Previous {
			// The API server answers 400 when there is no earlier container,
			// which is the ordinary case for an app that has not crashed.
			return nil, fmt.Errorf("%s has not restarted, so there is no earlier log to read", target.Name)
		}
		return nil, fmt.Errorf("read logs from %s: %w", target.Name, err)
	}
	return stream, nil
}

// pickLogPod chooses the instance whose log is worth reading.
//
// Normally that is a ready one: a crash-looping pod's output is what somebody
// wants when nothing is serving, and noise when something is. Asking for the
// earlier container inverts it — the pod that restarted is the whole point.
func pickLogPod(pods []corev1.Pod, previous bool) corev1.Pod {
	target := pods[0]
	if previous {
		mostRestarts := restartCount(target)
		for _, pod := range pods {
			if n := restartCount(pod); n > mostRestarts {
				target, mostRestarts = pod, n
			}
		}
		return target
	}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return pod
			}
		}
	}
	return target
}

func restartCount(pod corev1.Pod) int32 {
	var n int32
	for _, cs := range pod.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}

// RestartApp triggers a rolling restart without changing anything else.
func (c *Client) RestartApp(ctx context.Context, namespace, appSlug string) error {
	// Changing an annotation on the pod template is how kubectl rollout restart
	// works, and it keeps the rollout ordinary: the same surge and readiness
	// rules apply, so a restart is still zero downtime.
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"%s":%q}}}}}`,
		version.LabelKey("restarted-at"), time.Now().UTC().Format(time.RFC3339))
	_, err := c.clientset.AppsV1().Deployments(namespace).Patch(
		ctx, appSlug, "application/strategic-merge-patch+json", []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("restart %s: %w", appSlug, err)
	}
	return nil
}

// ScaleDeployment sets a Deployment's replica count and reports what it was.
//
// Used by a volume restore, which has to stop the app first: a ReadWriteOnce
// volume being unpacked underneath a process that has files open on it is how
// a restore turns one bad day into two.
func (c *Client) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) (was int32, err error) {
	deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}
	was = 1
	if deployment.Spec.Replicas != nil {
		was = *deployment.Spec.Replicas
	}
	if was == replicas {
		return was, nil
	}
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	if _, err := c.clientset.AppsV1().Deployments(namespace).Patch(
		ctx, name, "application/strategic-merge-patch+json", []byte(patch), metav1.PatchOptions{}); err != nil {
		return was, fmt.Errorf("scale %s to %d: %w", name, replicas, err)
	}
	return was, nil
}

// WaitForNoPods blocks until a Deployment has no pods left running.
//
// Scaling to zero returns as soon as the API server has the new number. The
// pod is still there, still holding the volume, for as long as it takes to
// shut down.
func (c *Client) WaitForNoPods(ctx context.Context, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if deployment.Status.Replicas == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s still has %d instance(s) running after %s",
				name, deployment.Status.Replicas, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitForRollout blocks until a Deployment's new revision is fully available.
func (c *Client) WaitForRollout(ctx context.Context, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastMessage string
	for {
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("read the rollout status: %w", err)
		}
		desired := int32(1)
		if deployment.Spec.Replicas != nil {
			desired = *deployment.Spec.Replicas
		}
		// Comparing the observed generation stops us reporting success from
		// the previous revision's status before the controller has caught up.
		if deployment.Status.ObservedGeneration >= deployment.Generation &&
			deployment.Status.UpdatedReplicas == desired &&
			deployment.Status.ReadyReplicas == desired &&
			deployment.Status.UnavailableReplicas == 0 {
			return nil
		}
		for _, cond := range deployment.Status.Conditions {
			if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse {
				return fmt.Errorf("the rollout stopped making progress: %s", cond.Message)
			}
			if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue {
				lastMessage = cond.Message
			}
		}

		if time.Now().After(deadline) {
			if lastMessage == "" {
				lastMessage = fmt.Sprintf("%d of %d instances became ready",
					deployment.Status.ReadyReplicas, desired)
			}
			return fmt.Errorf("the rollout did not finish in %s: %s", timeout, lastMessage)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// DeleteApp removes everything an app owns in its namespace.
func (c *Client) DeleteApp(ctx context.Context, namespace, appSlug string) error {
	// Ordered so that traffic stops before the workload does.
	for _, target := range []struct {
		apiVersion, kind, name string
		// optional marks a kind the cluster may not have. KEDA is installed
		// only when somebody asks for scale to zero, and a cluster without it
		// answers "no such resource" — which must not be the reason deleting
		// an app fails.
		optional bool
	}{
		{apiVersion: "networking.k8s.io/v1", kind: "Ingress", name: appSlug},
		// The wake Service and the scaled object go with the ingress: they are
		// the path traffic took to a sleeping app, and an HTTPScaledObject left
		// behind keeps KEDA reconciling a Deployment that is no longer there.
		{apiVersion: "v1", kind: "Service", name: InterceptorServiceName(appSlug)},
		{apiVersion: "http.keda.sh/v1alpha1", kind: "HTTPScaledObject", name: appSlug, optional: true},
		{apiVersion: "autoscaling/v2", kind: "HorizontalPodAutoscaler", name: ResourceName(appSlug, "hpa")},
		{apiVersion: "policy/v1", kind: "PodDisruptionBudget", name: ResourceName(appSlug, "pdb")},
		{apiVersion: "v1", kind: "Service", name: appSlug},
		{apiVersion: "apps/v1", kind: "Deployment", name: appSlug},
		{apiVersion: "v1", kind: "Secret", name: ResourceName(appSlug, "env")},
	} {
		err := c.applier.Delete(ctx, target.apiVersion, target.kind, namespace, target.name)
		if err != nil && !target.optional {
			return err
		}
	}

	// The scheduled commands, which are the part of an app that keeps going on
	// its own. Left behind, a deleted app's nightly job fires every night
	// forever against an image nothing will pull and a Secret that no longer
	// exists, and the only sign of it is failed pods accumulating in a
	// namespace nobody is looking at.
	if err := c.deleteRuns(ctx, namespace, appSlug); err != nil {
		return err
	}

	// PersistentVolumeClaims are deliberately left behind: deleting an app
	// should not silently destroy its data. They are removed with the
	// environment, or by hand.
	return nil
}

// deleteRuns removes an app's scheduled commands and the Jobs from its runs.
func (c *Client) deleteRuns(ctx context.Context, namespace, appSlug string) error {
	selector := labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/name":      appSlug,
		"app.kubernetes.io/component": "run",
	}).String()
	// Background, because the default for a Job is to orphan its pods, and a
	// migration pod outliving the app that owns it is the thing being removed.
	background := metav1.DeletePropagationBackground
	options := metav1.DeleteOptions{PropagationPolicy: &background}
	listing := metav1.ListOptions{LabelSelector: selector}

	// Listed and then deleted one at a time rather than as a collection. A
	// DeleteCollection would be one call, but it is one call whose selector
	// nothing here can check: if it were ever sent without one it would empty
	// the namespace of every app's scheduled commands, and that is not a
	// mistake worth being one typo away from.
	crons, err := c.clientset.BatchV1().CronJobs(namespace).List(ctx, listing)
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("find %s's scheduled commands: %w", appSlug, err)
	}
	if crons != nil {
		for _, cron := range crons.Items {
			if err := c.clientset.BatchV1().CronJobs(namespace).
				Delete(ctx, cron.Name, options); err != nil && !IsNotFound(err) {
				return fmt.Errorf("remove the scheduled command %s: %w", cron.Name, err)
			}
		}
	}

	jobs, err := c.clientset.BatchV1().Jobs(namespace).List(ctx, listing)
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("find %s's runs: %w", appSlug, err)
	}
	if jobs != nil {
		for _, job := range jobs.Items {
			if err := c.clientset.BatchV1().Jobs(namespace).
				Delete(ctx, job.Name, options); err != nil && !IsNotFound(err) {
				return fmt.Errorf("remove the run %s: %w", job.Name, err)
			}
		}
	}
	return nil
}

// EnsureNamespace creates an environment's namespace with its guards.
func (c *Client) EnsureNamespace(ctx context.Context, namespace, teamID, projectID string, level PodSecurity) error {
	objects := []any{
		BuildNamespace(namespace, teamID, projectID, level),
		BuildLimitRange(namespace),
		BuildResourceQuota(namespace, DefaultQuota()),
	}
	for _, policy := range BuildNetworkPolicies(namespace, c.systemNamespace) {
		objects = append(objects, policy)
	}
	if err := c.applier.ApplyAll(ctx, objects...); err != nil {
		return fmt.Errorf("prepare the namespace %s: %w", namespace, err)
	}
	return nil
}

// DeleteNamespace removes an environment and everything in it.
func (c *Client) DeleteNamespace(ctx context.Context, namespace string) error {
	// Refusing to delete a namespace we do not own stops a bug or a crafted id
	// from removing kube-system.
	ns, err := c.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read the namespace %s: %w", namespace, err)
	}
	if ns.Labels["app.kubernetes.io/managed-by"] != version.Binary {
		return fmt.Errorf("refusing to delete the namespace %s: it was not created by %s", namespace, version.Name)
	}
	if err := c.clientset.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil && !IsNotFound(err) {
		return fmt.Errorf("delete the namespace %s: %w", namespace, err)
	}
	return nil
}
