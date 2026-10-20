package cluster

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/version"
)

// Default manifest sources for the components installed from upstream releases.
//
// These are defaults, not pins: each has a matching setting so an operator can
// move to a newer release, or host the file themselves on a network without
// internet access, without waiting for a Skifity release.
const (
	defaultCertManagerURL   = "https://github.com/cert-manager/cert-manager/releases/download/v1.21.2/cert-manager.yaml"
	defaultCloudNativePGURL = "https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.29/releases/cnpg-1.29.0.yaml"
	defaultKEDAURL          = "https://github.com/kedacore/keda/releases/download/v2.20.0/keda-2.20.0.yaml"
	// The add-on's release asset is keda-add-ons-http-<version>.yaml. This said
	// keda-http-add-on-0.15.0.yaml, a file no release has, and scale to zero
	// could never be installed: the fourth run on a real cluster was the first
	// thing to try.
	defaultKEDAHTTPURL = "https://github.com/kedacore/http-add-on/releases/download/v0.15.0/keda-add-ons-http-0.15.0.yaml"
	defaultLonghornURL = "https://raw.githubusercontent.com/longhorn/longhorn/v1.10.0/deploy/longhorn.yaml"
)

// manifestSettingFor maps a component to the setting holding its manifest URL.
func manifestSettingFor(name string) (settingKey, fallback string) {
	switch name {
	case "cert-manager":
		return "components.cert_manager_url", defaultCertManagerURL
	case "cloudnative-pg":
		return "components.cloudnative_pg_url", defaultCloudNativePGURL
	case "keda":
		return "components.keda_url", defaultKEDAURL
	case "longhorn":
		return "components.longhorn_url", defaultLonghornURL
	default:
		return "", ""
	}
}

// installComponent does the work for one component.
func (c *Cluster) installComponent(ctx context.Context, name string) error {
	switch name {
	case "cert-manager":
		return c.installCertManager(ctx)
	case "registry":
		return c.installRegistry(ctx)
	case "cloudnative-pg":
		return c.installFromURL(ctx, name, kube.DatabaseOperatorNamespace, "cnpg-controller-manager")
	case "keda":
		return c.installKEDA(ctx)
	case "longhorn":
		return c.installFromURL(ctx, name, "longhorn-system", "longhorn-driver-deployer")
	case "cloudflare-tunnel":
		return c.installCloudflareTunnel(ctx)
	case UpgradeComponent:
		return c.installUpgradeController(ctx)
	case GuardComponent:
		return c.installEdgeGuard(ctx)
	case NVIDIAComponent:
		return c.installNVIDIADevicePlugin(ctx)
	case "monitoring":
		// Refused earlier, in the API, with a message that says where to look.
		// This is the second lock on the same door.
		return fmt.Errorf("the full monitoring stack is installed with Helm, not by the panel")
	default:
		return fmt.Errorf("%q is not a component Skifity knows how to install", name)
	}
}

// manifestURL reads a component's manifest URL from settings, falling back to
// the version this build was tested against.
func (c *Cluster) manifestURL(ctx context.Context, name string) (string, error) {
	key, fallback := manifestSettingFor(name)
	if key == "" {
		return "", fmt.Errorf("%q has no manifest to install", name)
	}
	value, _, err := c.db.GetSetting(ctx, key)
	if err != nil {
		return "", err
	}
	if value != "" {
		return value, nil
	}
	return fallback, nil
}

// installFromURL applies a component's upstream manifest and waits for it.
func (c *Cluster) installFromURL(ctx context.Context, name, namespace, deployment string) error {
	url, err := c.manifestURL(ctx, name)
	if err != nil {
		return err
	}
	c.log.Info("applying component manifest", "component", name, "url", url)
	if err := c.client.ApplyManifestURL(ctx, url); err != nil {
		return err
	}
	if deployment == "" {
		return nil
	}
	if err := c.client.WaitForDeployment(ctx, namespace, deployment, 5*time.Minute); err != nil {
		return fmt.Errorf("%s was installed but did not start: %w", name, err)
	}
	return nil
}

// installCertManager installs cert-manager and the ClusterIssuer that makes
// HTTPS automatic.
func (c *Cluster) installCertManager(ctx context.Context) error {
	if err := c.installFromURL(ctx, "cert-manager", "cert-manager", "cert-manager-webhook"); err != nil {
		return err
	}
	// The webhook needs a moment after becoming ready before it will admit a
	// ClusterIssuer; retrying is simpler and more reliable than guessing a sleep.
	// Two minutes of it: the webhook answers only once cert-manager's own
	// injector has given it a certificate, which on a small server can take
	// longer than the half minute this used to wait, and running out left the
	// server with cert-manager and no issuer, and every domain waiting for a
	// certificate nothing would ask for.
	return c.retry(ctx, 40, 3*time.Second, func() error {
		return c.EnsureClusterIssuer(ctx)
	})
}

// RegistryService is where builds push and nodes pull from.
const (
	RegistryService = "skifity-registry"
	RegistryPort    = 5000
)

// RegistryAddress is the in-cluster address images are tagged with.
func (c *Cluster) RegistryAddress() string {
	return kube.RegistryHost()
}

// installRegistry deploys the in-cluster image registry, with its guard in
// front of it.
func (c *Cluster) installRegistry(ctx context.Context) error {
	if err := c.EnsureBuildNamespace(ctx); err != nil {
		return err
	}
	// The key the guard checks tokens against, before the pod that mounts it.
	if _, err := c.RegistryKey(ctx); err != nil {
		return err
	}
	image, err := c.ownImage(ctx)
	if err != nil {
		return errdoc.RegistryNoPanelImage(err.Error())
	}
	namespace := c.client.BuildNamespace()
	if err := c.client.Applier().ApplyAll(ctx, registryObjects(namespace, image)...); err != nil {
		return err
	}
	return c.client.WaitForDeployment(ctx, namespace, RegistryService, 3*time.Minute)
}

// ReconcileRegistry keeps the registry's guard in step with the panel.
//
// The registry is installed once and then left alone, so a panel upgraded to a
// version with a guard would keep pushing to a registry anybody can write to,
// with tokens nothing reads; and a panel upgraded again would keep the guard of
// the version before, which is not the program that signs the tokens. Either
// is put right here, which restarts the registry for a moment: a pull that
// lands in that moment is retried by the server that asked.
//
// A push does not survive the registry restarting under it, so this happens
// only while no build is running, and holds the next ones back until the
// registry is ready again. With builds in flight it is left for a later build:
// an older guard, or none, still lets the build that is waiting push.
//
// It is called before every build, outside BeginBuild, and costs two reads
// when there is nothing to do.
func (c *Cluster) ReconcileRegistry(ctx context.Context) error {
	inStep, err := c.registryInStep(ctx)
	if err != nil || inStep {
		return err
	}
	if !c.registryMu.TryLock() {
		c.log.Info("the registry's guard is out of step with the panel, and builds are running; a later build puts it right")
		return nil
	}
	defer c.registryMu.Unlock()
	c.log.Info("putting the panel's guard in front of the registry")
	return c.installRegistry(ctx)
}

// registryInStep reports whether the registry's guard is the panel's own image.
// A registry that is not there at all, though the panel installed it, is out
// of step too: it is put back rather than pushed to.
func (c *Cluster) registryInStep(ctx context.Context) (bool, error) {
	namespace := c.client.BuildNamespace()
	deployment, err := c.client.Clientset().AppsV1().Deployments(namespace).Get(ctx, RegistryService, metav1.GetOptions{})
	if kube.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the registry: %w", err)
	}
	image, err := c.ownImage(ctx)
	if err != nil {
		return false, errdoc.RegistryNoPanelImage(err.Error())
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == registryGuardContainer && container.Image == image {
			return true, nil
		}
	}
	return false, nil
}

// registryGuardContainer is the guard's container in the registry's pod.
const registryGuardContainer = "guard"

// registryObjects renders the registry, separately from applying it, so a test
// can read what would be created.
//
// Two containers in one pod. The registry listens on the pod's loopback only;
// the guard, the panel's own image running `skifity registry-guard`, listens on
// the registry's port and is the only way to it. Reading passes straight
// through, which is what every server's container runtime does to pull an
// app; writing needs a token for the repository written to.
func registryObjects(namespace, guardImage string) []any {
	labels := map[string]string{
		"app.kubernetes.io/name":       RegistryService,
		"app.kubernetes.io/managed-by": version.Binary,
	}

	claim := &corev1.PersistentVolumeClaim{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{Name: RegistryService, Namespace: namespace, Labels: labels},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
			},
		},
	}

	keyPath := "/etc/skifity-registry"
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: RegistryService, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			// The registry keeps images on a ReadWriteOnce volume, so two
			// instances must never run at once.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// The guard reads a file and forwards HTTP; it has no use for
					// the Kubernetes API.
					AutomountServiceAccountToken: ptrTo(false),
					// On a server that runs the control plane, when there is one
					// to choose. Its disk holds every image the cluster has built,
					// and its volume keeps it on whichever server it first started
					// on; a worker is the kind of server that is added and taken
					// away, and one that fails took every image with it. Preferred
					// rather than required, so a cluster whose control plane takes
					// no work still has a registry.
					Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
							Weight: 100,
							Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{
								Key: "node-role.kubernetes.io/control-plane", Operator: corev1.NodeSelectorOpExists,
							}}},
						}},
					}},
					Containers: []corev1.Container{
						{
							Name:  "registry",
							Image: registryImage,
							Env: []corev1.EnvVar{
								{Name: "REGISTRY_STORAGE_DELETE_ENABLED", Value: "true"},
								// Loopback: the guard is the only thing that can reach it.
								{Name: "REGISTRY_HTTP_ADDR", Value: fmt.Sprintf("127.0.0.1:%d", RegistryUpstreamPort)},
								// The address an upload is continued at is written
								// relative, so it is the guard's, whichever name
								// the client used.
								{Name: "REGISTRY_HTTP_RELATIVEURLS", Value: "true"},
								{Name: "REGISTRY_HTTP_DEBUG_ADDR", Value: registryDebugAddress},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("20m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
							},
							VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/var/lib/registry"}},
						},
						{
							Name:    registryGuardContainer,
							Image:   guardImage,
							Command: []string{"/usr/local/bin/skifity", "registry-guard"},
							Env: []corev1.EnvVar{
								{Name: "SKIFITY_REGISTRY_ADDRESS", Value: fmt.Sprintf(":%d", RegistryPort)},
								{Name: "SKIFITY_REGISTRY_UPSTREAM", Value: fmt.Sprintf("http://127.0.0.1:%d", RegistryUpstreamPort)},
								{Name: "SKIFITY_REGISTRY_KEY_FILE", Value: keyPath + "/" + registryKeyField},
								{Name: "SKIFITY_LOG_FORMAT", Value: "json"},
							},
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: RegistryPort}},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("16Mi"),
								},
								Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptrTo(false),
								ReadOnlyRootFilesystem:   ptrTo(true),
								RunAsNonRoot:             ptrTo(true),
								RunAsUser:                ptrTo(int64(65532)),
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							},
							// Through the guard to the registry, so a pod is ready
							// only when both halves answer.
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{Path: "/v2/", Port: intstr.FromString("http")},
								},
								PeriodSeconds: 5,
							},
							VolumeMounts: []corev1.VolumeMount{{Name: "key", MountPath: keyPath, ReadOnly: true}},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: RegistryService},
							},
						},
						{
							// Readable by every user: the guard runs as nobody,
							// and a Secret's files belong to root. Only the
							// guard's container mounts it.
							Name: "key",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{SecretName: RegistryKeySecret, DefaultMode: ptrTo(int32(0o444))},
							},
						},
					},
				},
			},
		},
	}

	service := &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: RegistryService, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			// A NodePort, because the thing that pulls these images is every
			// node's container runtime, which is on the host and cannot reach
			// a ClusterIP by name. See kube.RegistriesYAML.
			Type:     corev1.ServiceTypeNodePort,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "http", Port: RegistryPort, TargetPort: intstr.FromString("http"),
				NodePort: kube.RegistryNodePort,
			}},
		},
	}

	return []any{claim, deployment, service}
}

// legacyBuilder is the one BuildKit every team's builds used to share, before
// each build had its own.
const legacyBuilder = "skifity-buildkit"

// RetireSharedBuilder removes the shared BuildKit from a cluster that has one.
// Nothing talks to it any more, and it held a node's memory and its cache held
// the node's disk for nobody. Called before a build; it costs a read when the
// builder is long gone.
func (c *Cluster) RetireSharedBuilder(ctx context.Context) error {
	namespace := c.client.BuildNamespace()
	if _, err := c.client.Clientset().AppsV1().Deployments(namespace).Get(ctx, legacyBuilder, metav1.GetOptions{}); err != nil {
		if kube.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read the shared builder: %w", err)
	}
	c.log.Info("removing the shared builder: every build runs its own now")
	applier := c.client.Applier()
	if err := applier.Delete(ctx, "apps/v1", "Deployment", namespace, legacyBuilder); err != nil {
		return err
	}
	return applier.Delete(ctx, "v1", "Service", namespace, legacyBuilder)
}

// registryImage is the in-cluster registry, and the garbage collection that
// sweeps it. It was registry:3, a tag that moves, so the registry holding
// every image the panel built could change version under a restart with
// nothing changed here. Pinned by version and index digest, read from Docker
// Hub on 2026-10-02; move the two together.
const registryImage = "registry:3.1.2@sha256:ddf754342cfc8acc51a56d5d0ab6af06826461864460636d8bd5c546dab2a7b8"

// installKEDA installs KEDA and its HTTP add-on, which together provide
// scale-to-zero.
func (c *Cluster) installKEDA(ctx context.Context) error {
	if err := c.installFromURL(ctx, "keda", "keda", "keda-operator"); err != nil {
		return err
	}
	url, _, err := c.settingOr(ctx, "components.keda_http_url", defaultKEDAHTTPURL)
	if err != nil {
		return err
	}
	if err := c.client.ApplyManifestURL(ctx, url); err != nil {
		return fmt.Errorf("install the KEDA HTTP add-on: %w", err)
	}
	return c.client.WaitForDeployment(ctx, "keda", "keda-add-ons-http-interceptor", 5*time.Minute)
}

func (c *Cluster) settingOr(ctx context.Context, key, fallback string) (string, bool, error) {
	value, _, err := c.db.GetSetting(ctx, key)
	if err != nil {
		return "", false, err
	}
	if value == "" {
		return fallback, false, nil
	}
	return value, true, nil
}

// retry runs fn until it succeeds or the attempts run out.
func (c *Cluster) retry(ctx context.Context, attempts int, wait time.Duration, fn func() error) error {
	var err error
	for i := range attempts {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}

// EnsureComponent installs a component if it is not already there. Callers use
// it as a precondition rather than checking first themselves.
func (c *Cluster) EnsureComponent(ctx context.Context, name string) error {
	current, err := c.db.GetComponent(ctx, name)
	if err != nil {
		return err
	}
	if current.Status == "installed" {
		return nil
	}
	return c.InstallComponent(ctx, name)
}

// UpgradePanel changes the panel's own image and lets Kubernetes roll it out,
// returning the image it replaced.
//
// The caller needs that image. The panel's Deployment uses the Recreate
// strategy — one copy at a time, because the database is a file on one node's
// disk — so Kubernetes stops the running panel before it starts the new one.
// If the new image does not come up, there is no panel left to notice, and
// Kubernetes does not roll anything back by itself. The way out is a command
// on the server, and somebody has to be told it before they need it.
func (c *Cluster) UpgradePanel(ctx context.Context, target string) (string, error) {
	namespace := c.client.SystemNamespace()
	deployment, err := c.client.Clientset().AppsV1().Deployments(namespace).Get(ctx, "skifity-panel", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("find the panel's own Deployment: %w", err)
	}
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return "", fmt.Errorf("the panel's Deployment has no containers")
	}

	current := deployment.Spec.Template.Spec.Containers[0].Image
	repository := current
	if idx := lastIndexByte(current, ':'); idx > 0 {
		repository = current[:idx]
	}
	deployment.Spec.Template.Spec.Containers[0].Image = repository + ":" + target

	if _, err := c.client.Clientset().AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("start the upgrade: %w", err)
	}
	c.log.Info("panel upgrade started", "from", current, "to", repository+":"+target)
	return current, nil
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
		if s[i] == '/' {
			// A colon before the last slash is a registry port, not a tag.
			return -1
		}
	}
	return -1
}
