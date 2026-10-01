package kube

import (
	"fmt"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/version"
)

// This file renders an AppSpec into Kubernetes objects. The defaults encoded
// here are the "safe and correct defaults" the product promises: health checks,
// resource limits, a non-root container, a read-only root filesystem where it is
// possible, HTTPS, and instances spread across servers.

// BuildDeployment renders the Deployment for an app.
func BuildDeployment(s AppSpec) *appsv1.Deployment {
	replicas := s.DesiredReplicas()
	labels := s.Labels()

	confinement := s.Confinement()
	gpus := s.GPUs()

	container := corev1.Container{
		Name:            s.Name,
		Image:           s.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         s.Command,
		Args:            s.Args,
		Resources:       buildResources(s),
		SecurityContext: &corev1.SecurityContext{
			// No new privileges, at every level and for every image: a process
			// gaining more than it started with is what an exploit is for, and
			// nothing legitimate in a container needs it. Dropping privileges,
			// which is what an image starting as root does, is the opposite
			// direction and is unaffected.
			AllowPrivilegeEscalation: ptr(false),
			RunAsNonRoot:             confinement.RunAsNonRoot(),
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	if confinement.DropAllCapabilities() {
		container.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	}

	if s.Port > 0 {
		container.Ports = []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: int32(s.Port),
			Protocol:      corev1.ProtocolTCP,
		}}
		// Readiness, liveness, and a startup probe that gives a slow framework
		// the time the app's settings allow it — or none, when the app asked
		// for none. See health.go.
		setProbes(&container, s)
		// The other half of a zero-downtime deploy.
		//
		// maxUnavailable: 0 keeps the capacity, and on its own it still drops
		// requests. Removing a pod from the Service and telling it to stop
		// happen at the same moment, and the ingress controller finds out
		// through a watch — so for a fraction of a second it is still sending
		// requests to a process that has already begun shutting down. Five
		// seconds of doing nothing before SIGTERM is what closes that window:
		// by then every proxy has seen the endpoint go.
		//
		// A sleep action rather than a command, because it needs no shell in
		// the image — a distroless container has none. It comes out of the same
		// 30-second grace period, which leaves 25 for connections in flight.
		container.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Sleep: &corev1.SleepAction{Seconds: 5},
			},
		}
	}

	container.Ports = append(container.Ports, publicContainerPorts(s)...)

	if s.EnvFromSecret != "" {
		container.EnvFrom = []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: s.EnvFromSecret},
			},
		}}
	}
	container.Env = buildPlainEnv(s)

	for _, v := range s.Volumes {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
		})
	}
	container.VolumeMounts = append(container.VolumeMounts, fileMounts(s)...)

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: confinement.RunAsNonRoot(),
			// The uid is pinned only for an image Skifity built, where 1000 is
			// what the builders produce. Pinning it for anybody else's image
			// overrode the USER that image declares, which is a guess — and it
			// was the wrong one for 120 of the 124 catalogue images whose
			// configuration could be read from their registries.
			RunAsUser:  confinement.RunAsUser(),
			RunAsGroup: confinement.RunAsGroup(),
			// FSGroup stays at 1000 whatever the image runs as. It sets the
			// group on a mounted volume and adds that group to the container's
			// supplementary groups, which is what makes a volume writable by a
			// process whose uid nobody here knows.
			FSGroup: ptr(int64(1000)),
		},
		// An app has no business talking to the Kubernetes API, and a mounted
		// token is the first thing an attacker looks for.
		AutomountServiceAccountToken: ptr(false),
		// Only for an image in somebody's own registry. Without it the kubelet
		// pulls anonymously and a private repository answers 401, which shows
		// up as ImagePullBackOff and says nothing about credentials.
		ImagePullSecrets: pullSecrets(s),
		// Long enough for a web server to finish in-flight requests, short
		// enough that a deploy does not feel stuck.
		TerminationGracePeriodSeconds: ptr(int64(30)),
	}

	if start := confinement.UnprivilegedPortStart(); start != "" {
		podSpec.SecurityContext.Sysctls = []corev1.Sysctl{
			{Name: "net.ipv4.ip_unprivileged_port_start", Value: start},
		}
	}

	for _, v := range s.Volumes {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: v.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ResourceName(s.Name, v.Name),
				},
			},
		})
	}
	if files := filesPodVolume(s); files != nil {
		podSpec.Volumes = append(podSpec.Volumes, *files)
	}

	if s.SpreadAcrossServers {
		// ScheduleAnyway rather than DoNotSchedule: on a one-node cluster,
		// which is where most installs start, DoNotSchedule would leave every
		// instance after the first stuck in Pending.
		podSpec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       "kubernetes.io/hostname",
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector:     &metav1.LabelSelector{MatchLabels: s.SelectorLabels()},
		}}
		podSpec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						TopologyKey:   "kubernetes.io/hostname",
						LabelSelector: &metav1.LabelSelector{MatchLabels: s.SelectorLabels()},
					},
				}},
			},
		}
	}

	// After the spread, which it adds a node preference beside. The build
	// never comes here, and neither does a one-off command: see gpu.go.
	applyGPUs(&podSpec, &podSpec.Containers[0], gpus)

	// A volume means the app keeps state on disk, so two instances writing at
	// once would corrupt it. Recreate stops the old instance before the new one
	// starts; RollingUpdate would run both.
	strategy := appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: intOrString(0),
			MaxSurge:       intOrString(1),
		},
	}
	switch {
	case len(s.Volumes) > 0:
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	case gpus.Count > 0:
		// The new instance needs the card the old one holds. See gpuStrategy.
		strategy = gpuStrategy()
	}

	// Not written at all when an autoscaler owns it: server-side apply removes
	// the field from the panel's ownership, and whatever the HPA or KEDA has
	// set survives the next apply. See AppSpec.ReplicasAreSomebodyElses.
	replicaField := &replicas
	if s.ReplicasAreSomebodyElses() {
		replicaField = nil
	}

	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Labels:      labels,
			Annotations: s.Annotations(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicaField,
			Selector: &metav1.LabelSelector{MatchLabels: s.SelectorLabels()},
			Strategy: strategy,
			// Ten revisions is enough history for rollback without filling
			// etcd with ReplicaSets nobody will ever use.
			RevisionHistoryLimit: ptr(int32(10)),
			// Longer than Kubernetes' ten minutes only for an app allowed
			// nearly that long to start; see progressDeadline.
			ProgressDeadlineSeconds: progressDeadline(s),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: s.Annotations(),
				},
				Spec: podSpec,
			},
		},
	}
}

// BuildService renders the Service that sits in front of an app's instances.
func BuildService(s AppSpec) *corev1.Service {
	if s.Port <= 0 {
		return nil
	}
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: s.SelectorLabels(),
			Ports:    servicePorts(s.Port),
		},
	}
}

// servicePorts are 80, which the Ingress and KEDA's interceptor send to, and
// the app's own port as well, so another app in the environment reaches it
// the way it would in Compose: by name and port, db:5432. With 80 alone that
// address — the one every Compose file writes — connected to nothing.
func servicePorts(port int) []corev1.ServicePort {
	ports := []corev1.ServicePort{{
		Name:       "http",
		Port:       80,
		TargetPort: intstr.FromString("http"),
		Protocol:   corev1.ProtocolTCP,
	}}
	if port != 80 {
		ports = append(ports, corev1.ServicePort{
			Name:       "app",
			Port:       int32(port),
			TargetPort: intstr.FromString("http"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return ports
}

// BuildIngress renders the Ingress for an app's domains.
//
// Returns nil when the app has no domains, which is normal for a worker, and
// when every one of them is served with a certificate of the team's own: those
// are in the app's other Ingress, BuildOwnCertIngress, and this one must not
// list them. See owncert.go for why they cannot share it.
func BuildIngress(s AppSpec) *networkingv1.Ingress {
	if s.Port <= 0 {
		return nil
	}
	domains := letsEncryptDomains(s)
	if len(domains) == 0 {
		return nil
	}

	annotations := ingressAnnotations(s)
	var tlsHosts []string
	for _, d := range domains {
		if d.TLS {
			tlsHosts = append(tlsHosts, d.Hostname)
		}
	}
	// cert-manager's ingress-shim issues a certificate for every TLS block of
	// an Ingress carrying this annotation, which is why a hostname with a
	// certificate of its own is never in this one.
	if len(tlsHosts) > 0 && s.ClusterIssuer != "" {
		annotations["cert-manager.io/cluster-issuer"] = s.ClusterIssuer
	}

	ingress := buildIngress(s, s.Name, domains, annotations)
	if len(tlsHosts) > 0 {
		ingress.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      tlsHosts,
			SecretName: ResourceName(s.Name, "tls"),
		}}
	}
	return ingress
}

// ingressAnnotations are what every Ingress of the app carries: the
// middlewares a request goes through before it reaches the app. One function
// for both Ingresses, so a hostname on a certificate of the team's own is
// behind the same firewall, redirects and password as every other.
func ingressAnnotations(s AppSpec) map[string]string {
	annotations := map[string]string{}
	if middlewares := ingressMiddlewares(s); len(middlewares) > 0 {
		annotations["traefik.ingress.kubernetes.io/router.middlewares"] = strings.Join(middlewares, ",")
	}
	return annotations
}

// ingressMiddlewares are the middleware names, in the order Traefik runs them.
//
// Every one of them lives in the app's own namespace. Traefik refuses a
// cross-namespace middleware reference unless allowCrossNamespace is turned
// on, and it is off by default — k3s's bundled Traefik included — so a
// reference to another namespace resolves to nothing and the middleware
// silently never runs. A middleware Traefik will not load is not a middleware
// Traefik complains about, which is why that was invisible for as long as it
// was.
func ingressMiddlewares(s AppSpec) []string {
	var middlewares []string

	// The firewall comes first: a request nobody is allowed to make should not
	// be answered with a redirect that tells them where to make it instead.
	if s.Protected {
		middlewares = append(middlewares, s.Namespace+"-"+GuardMiddleware+"@kubernetescrd")
	}
	// A hostname that redirects, before the redirect to HTTPS: it goes to the
	// target's own scheme, so http://www.example.com is one hop from
	// https://example.com rather than two. Each matches only its own host.
	for _, d := range hostRedirects(s) {
		middlewares = append(middlewares, s.Namespace+"-"+HostRedirectMiddlewareName(s.Name, d.Hostname)+"@kubernetescrd")
	}
	if redirectsToHTTPS(s) {
		middlewares = append(middlewares, s.Namespace+"-"+RedirectMiddleware+"@kubernetescrd")
	}
	// The password after the redirect, never before it. A browser answers a
	// password prompt with the password, so asking on plain HTTP when HTTPS
	// exists would send it in the clear one request before the redirect.
	if s.PasswordUsers != "" {
		middlewares = append(middlewares, s.Namespace+"-"+PasswordMiddlewareName(s.Name)+"@kubernetescrd")
	}
	return middlewares
}

// redirectsToHTTPS reports whether plain HTTP is sent to HTTPS: when one of
// the app's hostnames has a certificate coming, from cert-manager or from the
// team's own. Without either there is nothing on the other side of the
// redirect.
func redirectsToHTTPS(s AppSpec) bool {
	for _, d := range s.Domains {
		if d.TLS && (d.Certificate != "" || s.ClusterIssuer != "") {
			return true
		}
	}
	return false
}

// letsEncryptDomains are the app's domains that are not served with a
// certificate of the team's own.
func letsEncryptDomains(s AppSpec) []DomainSpec {
	var out []DomainSpec
	for _, d := range s.Domains {
		if d.Certificate == "" {
			out = append(out, d)
		}
	}
	return out
}

// buildIngress is an Ingress routing these domains to the app, with no TLS
// block yet: which certificate serves them is the caller's to say.
func buildIngress(s AppSpec, name string, domains []DomainSpec, annotations map[string]string) *networkingv1.Ingress {
	// An app that can scale to zero is reached through KEDA's interceptor,
	// which is what wakes it. Pointing the Ingress straight at the app's own
	// Service would mean a request to a sleeping app got a 503 and nothing
	// ever started it.
	backend := s.Name
	backendPort := int32(80)
	if ScaleToZeroEnabled(s) {
		backend = InterceptorServiceName(s.Name)
		// Not 80. That Service is an alias for KEDA's interceptor, which
		// listens on 8080; see BuildInterceptorService for why the number has
		// to be the same on both sides of the alias.
		backendPort = KEDAInterceptorPort
	}

	pathType := networkingv1.PathTypePrefix
	rules := make([]networkingv1.IngressRule, 0, len(domains))
	for _, d := range domains {
		path := d.Path
		if path == "" {
			path = "/"
		}
		rules = append(rules, networkingv1.IngressRule{
			Host: d.Hostname,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     path,
						PathType: &pathType,
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: backend,
								Port: networkingv1.ServiceBackendPort{Number: backendPort},
							},
						},
					}},
				},
			},
		})
	}

	return &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   s.Namespace,
			Labels:      s.Labels(),
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{Rules: rules},
	}
}

// BuildHPA renders the HorizontalPodAutoscaler, or nil when autoscaling is off.
//
// An app that can scale to zero never gets one, even when it also asked for
// autoscaling. KEDA creates its own HorizontalPodAutoscaler for the
// HTTPScaledObject, and two of them pointed at one Deployment do not divide the
// work: each reconciles the replica count towards its own answer and overwrites
// the other's, so the app oscillates for as long as both exist. The floor is
// what scale to zero is for, and KEDA owns it; the ceiling is carried into the
// HTTPScaledObject so the maximum the person chose still applies.
func BuildHPA(s AppSpec) *autoscalingv2.HorizontalPodAutoscaler {
	if !s.Autoscale || ScaleToZeroEnabled(s) {
		return nil
	}
	metrics := []autoscalingv2.MetricSpec{}
	if s.CPUTarget > 0 {
		metrics = append(metrics, utilizationMetric(corev1.ResourceCPU, s.CPUTarget))
	}
	if s.MemoryTarget > 0 {
		metrics = append(metrics, utilizationMetric(corev1.ResourceMemory, s.MemoryTarget))
	}

	minReplicas := int32(max(s.MinReplicas, 1))
	return &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(s.Name, "hpa"),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: s.Name,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: int32(s.MaxReplicas),
			Metrics:     metrics,
			Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
				ScaleUp: &autoscalingv2.HPAScalingRules{
					// React to a traffic spike quickly.
					StabilizationWindowSeconds: ptr(int32(30)),
				},
				ScaleDown: &autoscalingv2.HPAScalingRules{
					// Come down slowly, so a brief dip does not drop instances
					// that are about to be needed again.
					StabilizationWindowSeconds: ptr(int32(300)),
				},
			},
		},
	}
}

// BuildPDB renders a PodDisruptionBudget so that draining a node for an upgrade
// takes an app's instances one at a time instead of all at once.
//
// Returns nil for a single-instance app: a budget has nothing to protect when
// there is one instance, and one that cannot be satisfied blocks a drain
// forever, which is worse than the brief outage.
//
// maxUnavailable rather than minAvailable, which is not the same shape of
// promise. minAvailable: 1 says "leave one running", so on a three-instance app
// it permits two to go at once, and on an autoscaled app that has come down to
// its minimum of one it permits none at all — the drain then waits for an
// eviction that can never be allowed, which is the deadlock this comment used
// to say it avoided. maxUnavailable: 1 says "take one at a time", which is the
// actual intention, holds at every instance count, and leaves a single
// remaining instance evictable so a node can always be emptied.
func BuildPDB(s AppSpec) *policyv1.PodDisruptionBudget {
	if s.DesiredReplicas() < 2 && !s.Autoscale {
		return nil
	}
	maxUnavailable := intstr.FromInt32(1)
	return &policyv1.PodDisruptionBudget{
		TypeMeta: metav1.TypeMeta{APIVersion: "policy/v1", Kind: "PodDisruptionBudget"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(s.Name, "pdb"),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector:       &metav1.LabelSelector{MatchLabels: s.SelectorLabels()},
		},
	}
}

// BuildPVCs renders the persistent volume claims for an app's volumes.
func BuildPVCs(s AppSpec) []*corev1.PersistentVolumeClaim {
	out := make([]*corev1.PersistentVolumeClaim, 0, len(s.Volumes))
	for _, v := range s.Volumes {
		size := v.SizeGB
		if size < 1 {
			size = 1
		}
		claim := &corev1.PersistentVolumeClaim{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      ResourceName(s.Name, v.Name),
				Namespace: s.Namespace,
				Labels:    s.Labels(),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(strconv.Itoa(size) + "Gi"),
					},
				},
			},
		}
		if v.StorageClass != "" {
			claim.Spec.StorageClassName = &v.StorageClass
		}
		out = append(out, claim)
	}
	return out
}

// BuildEnvSecret renders the Secret holding an app's environment variables.
func BuildEnvSecret(s AppSpec, values map[string]string) *corev1.Secret {
	// data, never stringData. stringData is write-only: the API server folds
	// it into data and forgets it, so server-side apply owns nothing it can
	// take away, and a variable deleted in the panel stayed in data — and in
	// every pod started after — for good.
	data := make(map[string][]byte, len(values))
	for k, v := range values {
		if IsGPUDeviceVariable(k) {
			continue
		}
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceName(s.Name, "env"),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

func buildResources(s AppSpec) corev1.ResourceRequirements {
	requests := corev1.ResourceList{}
	limits := corev1.ResourceList{}

	if s.CPURequestM > 0 {
		requests[corev1.ResourceCPU] = resource.MustParse(strconv.Itoa(s.CPURequestM) + "m")
	}
	if s.MemRequestMB > 0 {
		requests[corev1.ResourceMemory] = resource.MustParse(strconv.Itoa(s.MemRequestMB) + "Mi")
	}
	if s.MemLimitMB > 0 {
		limits[corev1.ResourceMemory] = resource.MustParse(strconv.Itoa(s.MemLimitMB) + "Mi")
	}
	// A CPU limit is deliberately optional. Capping CPU throttles an app that
	// briefly needs more, which shows up as mysterious latency; the memory
	// limit is what actually protects the node, because memory cannot be
	// reclaimed by slowing the app down.
	if s.CPULimitM > 0 {
		limits[corev1.ResourceCPU] = resource.MustParse(strconv.Itoa(s.CPULimitM) + "m")
	}

	out := corev1.ResourceRequirements{}
	if len(requests) > 0 {
		out.Requests = requests
	}
	if len(limits) > 0 {
		out.Limits = limits
	}
	return out
}

// pullSecrets is the one secret an external registry needs, or nothing.
func pullSecrets(s AppSpec) []corev1.LocalObjectReference {
	var out []corev1.LocalObjectReference
	for _, name := range []string{s.ImagePullSecret, s.TeamPullSecret} {
		if name != "" {
			out = append(out, corev1.LocalObjectReference{Name: name})
		}
	}
	return out
}

func buildPlainEnv(s AppSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{}
	if s.Port > 0 {
		// Most frameworks read PORT; setting it means an app usually works
		// without the user configuring anything.
		env = append(env, corev1.EnvVar{Name: "PORT", Value: strconv.Itoa(s.Port)})
	}
	// What an app can know about itself without asking anybody. These were
	// Skifity_APP and Skifity_ENVIRONMENT — the product's display name glued
	// to a suffix — which no documentation mentioned and no program expects.
	// The prefix is reserved: these take precedence over a variable of the
	// same name, which Kubernetes does for env over envFrom.
	prefix := strings.ToUpper(version.Binary)
	appName := s.Name
	if s.ProcessOf != "" {
		appName = s.ProcessOf
	}
	env = append(env,
		corev1.EnvVar{Name: prefix + "_APP", Value: appName},
		corev1.EnvVar{Name: prefix + "_ENVIRONMENT", Value: s.Environment},
	)
	if s.CommitSHA != "" {
		env = append(env, corev1.EnvVar{Name: prefix + "_COMMIT_SHA", Value: s.CommitSHA})
	}
	if s.URL != "" {
		env = append(env, corev1.EnvVar{Name: prefix + "_URL", Value: s.URL})
	}
	if s.Preview {
		env = append(env, corev1.EnvVar{Name: prefix + "_PREVIEW", Value: "true"})
		if s.PullRequest > 0 {
			env = append(env, corev1.EnvVar{Name: prefix + "_PULL_REQUEST", Value: strconv.Itoa(s.PullRequest)})
		}
	}
	// The downward API gives an app its own identity without a service account.
	env = append(env, corev1.EnvVar{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
		FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
	}})
	for k, v := range sortedPairs(s.PlainEnv) {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	return env
}

func utilizationMetric(name corev1.ResourceName, target int) autoscalingv2.MetricSpec {
	value := int32(target)
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{
			Name: name,
			Target: autoscalingv2.MetricTarget{
				Type:               autoscalingv2.UtilizationMetricType,
				AverageUtilization: &value,
			},
		},
	}
}

// sortedPairs iterates a map in key order, so generated manifests are stable and
// a diff between two deploys shows real changes only.
func sortedPairs(m map[string]string) func(func(string, string) bool) {
	return func(yield func(string, string) bool) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			if !yield(k, m[k]) {
				return
			}
		}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func ptr[T any](v T) *T { return &v }

func intOrString(v int32) *intstr.IntOrString {
	out := intstr.FromInt32(v)
	return &out
}

// ObjectSummary is a short description of a rendered object, for logs and the
// Advanced view's list.
func ObjectSummary(kind, name string) string { return fmt.Sprintf("%s/%s", kind, name) }
