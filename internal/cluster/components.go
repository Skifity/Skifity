package cluster

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/kube"
	"skifity/internal/settings"
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
	defaultKEDAHTTPURL      = "https://github.com/kedacore/http-add-on/releases/download/v0.15.0/keda-http-add-on-0.15.0.yaml"
	defaultLonghornURL      = "https://raw.githubusercontent.com/longhorn/longhorn/v1.10.0/deploy/longhorn.yaml"
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
	case "buildkit":
		return c.installBuildKit(ctx)
	case "cloudnative-pg":
		return c.installFromURL(ctx, name, "cnpg-system", "cnpg-controller-manager")
	case "keda":
		return c.installKEDA(ctx)
	case "longhorn":
		return c.installFromURL(ctx, name, "longhorn-system", "longhorn-driver-deployer")
	case "cloudflare-tunnel":
		return c.installCloudflareTunnel(ctx)
	case GuardComponent:
		return c.installEdgeGuard(ctx)
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
	return c.retry(ctx, 10, 3*time.Second, func() error {
		return c.EnsureClusterIssuer(ctx)
	})
}

// EnsureClusterIssuer creates or updates the Let's Encrypt issuer.
//
// It also creates the Traefik middleware that redirects HTTP to HTTPS, because
// an issued certificate that nothing redirects to is only half the promise.
func (c *Cluster) EnsureClusterIssuer(ctx context.Context) error {
	email, _, err := c.db.GetSetting(ctx, settings.KeyACMEEmail)
	if err != nil {
		return err
	}
	if email == "" {
		return fmt.Errorf("no Let's Encrypt email is configured; set one under Settings, then Domains")
	}
	server, _, err := c.db.GetSetting(ctx, settings.KeyACMEServer)
	if err != nil {
		return err
	}
	if server == "" {
		server = "https://acme-v02.api.letsencrypt.org/directory"
	}

	issuer := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata": map[string]any{
			"name":   ClusterIssuerName,
			"labels": map[string]any{"app.kubernetes.io/managed-by": version.Binary},
		},
		"spec": map[string]any{
			"acme": map[string]any{
				"email":  email,
				"server": server,
				"privateKeySecretRef": map[string]any{
					"name": ClusterIssuerName + "-account-key",
				},
				"solvers": []any{map[string]any{
					// HTTP-01 works for anyone with a domain pointing at the
					// cluster, with no DNS credentials to configure.
					"http01": map[string]any{
						"ingress": map[string]any{"class": "traefik"},
					},
				}},
			},
		},
	}}
	if err := c.client.Applier().Apply(ctx, issuer); err != nil {
		return err
	}

	// The HTTPS redirect middleware used to be created here, once, in the
	// panel's own namespace, and referenced by every app's Ingress. Traefik
	// refuses a cross-namespace middleware reference unless allowCrossNamespace
	// is turned on, and it is off by default — so the reference resolved to
	// nothing and no app was ever redirected. It is rendered into each app's
	// own namespace now, by EnsureNamespace.
	return nil
}

// RegistryService is where builds push and nodes pull from.
const (
	RegistryService = "skifity-registry"
	RegistryPort    = 5000
	BuildKitService = "skifity-buildkit"
	BuildKitPort    = 1234
)

// RegistryAddress is the in-cluster address images are tagged with.
func (c *Cluster) RegistryAddress() string {
	return kube.RegistryHost()
}

// installRegistry deploys the in-cluster image registry.
func (c *Cluster) installRegistry(ctx context.Context) error {
	if err := c.EnsureBuildNamespace(ctx); err != nil {
		return err
	}
	namespace := c.client.BuildNamespace()
	if err := c.client.Applier().ApplyAll(ctx, registryObjects(namespace)...); err != nil {
		return err
	}
	return c.client.WaitForDeployment(ctx, namespace, RegistryService, 3*time.Minute)
}

// registryObjects renders the registry, separately from applying it, so a test
// can read what would be created.
func registryObjects(namespace string) []any {
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
					Containers: []corev1.Container{{
						Name:  "registry",
						Image: "registry:3",
						Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: RegistryPort}},
						Env: []corev1.EnvVar{
							{Name: "REGISTRY_STORAGE_DELETE_ENABLED", Value: "true"},
							{Name: "REGISTRY_HTTP_ADDR", Value: fmt.Sprintf("0.0.0.0:%d", RegistryPort)},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("20m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/var/lib/registry"}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{Path: "/v2/", Port: intstr.FromString("http")},
							},
							PeriodSeconds: 5,
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: RegistryService},
						},
					}},
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

// installBuildKit deploys the rootless builder.
func (c *Cluster) installBuildKit(ctx context.Context) error {
	if err := c.EnsureBuildNamespace(ctx); err != nil {
		return err
	}
	namespace := c.client.BuildNamespace()
	if err := c.client.Applier().ApplyAll(ctx, buildKitObjects(namespace)...); err != nil {
		return err
	}
	return c.client.WaitForDeployment(ctx, namespace, BuildKitService, 5*time.Minute)
}

// buildKitObjects renders the builder, separately from applying it.
func buildKitObjects(namespace string) []any {
	labels := map[string]string{
		"app.kubernetes.io/name":       BuildKitService,
		"app.kubernetes.io/managed-by": version.Binary,
	}

	replicas := int32(1)
	runAsUser := int64(1000)
	deployment := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: BuildKitService, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					// Rootless BuildKit needs these two profiles relaxed to use
					// user namespaces. This is the whole reason for choosing
					// rootless: the alternative is a privileged container with
					// the host's container runtime socket.
					Annotations: map[string]string{
						"container.apparmor.security.beta.kubernetes.io/buildkitd": "unconfined",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "buildkitd",
						// Pinned: "master" is whatever was built this morning,
						// and a builder that changes under an operator is a
						// build that breaks for no reason they can see.
						Image: buildKitImage,
						Args: []string{
							"--addr", fmt.Sprintf("tcp://0.0.0.0:%d", BuildKitPort),
							"--oci-worker-no-process-sandbox",
						},
						Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: BuildKitPort}},
						SecurityContext: &corev1.SecurityContext{
							RunAsUser:      &runAsUser,
							SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								Exec: &corev1.ExecAction{Command: []string{
									// The address has to be given. buildkitd is
									// started with --addr, which replaces the
									// default socket rather than adding to it,
									// so a bare `buildctl debug workers` looks
									// for a socket that does not exist and the
									// builder never becomes ready.
									"buildctl", "--addr", buildKitLocalAddress,
									"debug", "workers",
								}},
							},
							InitialDelaySeconds: 10,
							PeriodSeconds:       10,
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "cache", MountPath: "/home/user/.local/share/buildkit"}},
					}},
					Volumes: []corev1.Volume{{
						Name: "cache",
						// An emptyDir rather than a volume: the build cache is
						// worth keeping between builds on one node but is not
						// worth a PVC that pins the builder to one server.
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					}},
				},
			},
		},
	}

	service := &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: BuildKitService, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "grpc", Port: BuildKitPort, TargetPort: intstr.FromString("grpc"),
			}},
		},
	}

	return []any{deployment, service}
}

// buildKitImage is the rootless builder, pinned.
const buildKitImage = "moby/buildkit:v0.18.2-rootless"

// buildKitLocalAddress is how the readiness probe reaches buildkitd from
// inside its own container.
var buildKitLocalAddress = fmt.Sprintf("tcp://127.0.0.1:%d", BuildKitPort)

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
