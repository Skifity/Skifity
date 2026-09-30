package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/edgerules"
	"skifity/internal/errdoc"
	"skifity/internal/geoip"
	"skifity/internal/guard"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/version"
)

// The firewall's moving parts, and where each one lives.
//
// The panel owns the rules and writes them into a ConfigMap. The guard reads
// that file and answers Traefik. Traefik asks it because each protected app's
// Ingress names a middleware in its own namespace that points at the guard's
// Service. Nothing in the request path is the panel.

const (
	// GuardComponent is the component name the firewall installs under.
	GuardComponent = "firewall"
	// GuardDeployment and GuardConfigMap are the guard's own objects.
	GuardDeployment = "skifity-guard"
	GuardConfigMap  = "skifity-guard-rules"
	// GuardRulesFile is the key in the ConfigMap and the file it becomes.
	GuardRulesFile = "rules.json"
	// GuardMountPath is where that file is mounted.
	GuardMountPath = "/etc/skifity"
	// GuardDataPath is where the geo databases are kept. An emptyDir: they are
	// downloaded again in a few seconds and are not worth a volume.
	GuardDataPath = "/var/lib/skifity-guard"
	// GuardReplicas is how many run. Two, because Traefik has no way to ignore
	// an authorizer that does not answer, so one replica is one restart away
	// from every protected site being down.
	GuardReplicas = 2
)

// installEdgeGuard runs the guard and points the protected apps at it.
func (c *Cluster) installEdgeGuard(ctx context.Context) error {
	image, err := c.panelImage(ctx)
	if err != nil {
		return err
	}
	namespace := c.client.SystemNamespace()

	// The rules first, so that the pod finds its configuration already there
	// and its readiness probe passes on the first try rather than after the
	// kubelet's next sync.
	config, err := c.guardConfig(ctx)
	if err != nil {
		return err
	}
	objects, err := guardObjects(namespace, image, config)
	if err != nil {
		return err
	}
	if err := c.client.Applier().ApplyAll(ctx, objects...); err != nil {
		return err
	}
	if err := c.client.WaitForDeployment(ctx, namespace, GuardDeployment, 3*time.Minute); err != nil {
		return err
	}
	// A middleware for every namespace that has a protected app in it. Without
	// this the guard runs and nothing asks it anything.
	return c.ensureGuardMiddlewares(ctx)
}

// panelImage is the image the panel itself is running, which is the image the
// guard runs too: it is the same binary in another mode.
//
// Read from the live Deployment rather than from a setting, because the setting
// would be a second place for the version to be written and a second place for
// it to be wrong after an upgrade.
func (c *Cluster) panelImage(ctx context.Context) (string, error) {
	namespace := c.client.SystemNamespace()
	deployment, err := c.client.Clientset().AppsV1().
		Deployments(namespace).Get(ctx, "skifity-panel", metav1.GetOptions{})
	if err != nil {
		return "", errdoc.New("guard.no_panel_image", "The firewall could not be installed").
			WithCause("The panel's own Deployment could not be read, so there is no image to run the guard from: %s", err).
			WithImpact("Nothing was changed in the cluster.").
			WithFix("This works when the panel is running inside the cluster it manages, which is how the installer sets it up. " +
				"A panel running outside the cluster cannot install this component.").
			WithStatus(http.StatusBadRequest)
	}
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return "", fmt.Errorf("the panel's Deployment has no containers")
	}
	return deployment.Spec.Template.Spec.Containers[0].Image, nil
}

// RefreshFirewall writes the current rules into the cluster.
//
// Called whenever a rule set changes or a domain moves. It does nothing when
// the component was never installed, so writing a rule before installing is not
// an install by the back door — and the panel says so on the page rather than
// letting somebody write rules that protect nothing.
func (c *Cluster) RefreshFirewall(ctx context.Context) error {
	current, err := c.db.GetComponent(ctx, GuardComponent)
	if err != nil {
		return err
	}
	if current.Status != "installed" {
		return nil
	}
	config, err := c.guardConfig(ctx)
	if err != nil {
		return err
	}
	raw, err := renderGuardRules(config)
	if err != nil {
		return err
	}
	if err := c.client.Applier().Apply(ctx, guardConfigMap(c.client.SystemNamespace(), raw)); err != nil {
		return err
	}
	return c.ensureGuardMiddlewares(ctx)
}

// ensureGuardMiddlewares puts a forwardAuth middleware in every namespace that
// holds a protected app.
//
// One per namespace rather than one shared, because Traefik will not load a
// middleware from another namespace; see kube.BuildRedirectMiddleware.
func (c *Cluster) ensureGuardMiddlewares(ctx context.Context) error {
	namespaces, err := c.db.ProtectedNamespaces(ctx)
	if err != nil {
		return err
	}
	system := c.client.SystemNamespace()
	for _, namespace := range namespaces {
		if err := c.client.Applier().Apply(ctx, kube.BuildGuardMiddleware(namespace, system)); err != nil {
			// Best effort, the same as the redirect: a cluster running a
			// different ingress controller has no Traefik CRDs at all, and the
			// panel says on the page that the firewall needs Traefik.
			c.log.Warn("could not create the firewall middleware",
				"namespace", namespace, "error", err)
		}
	}
	return nil
}

// guardConfig reads everything the guard needs out of the database.
func (c *Cluster) guardConfig(ctx context.Context) (guard.Config, error) {
	protected, err := c.db.ProtectedHostnames(ctx)
	if err != nil {
		return guard.Config{}, err
	}

	config := guard.Config{Sets: map[string]guard.Protected{}}
	for _, row := range protected {
		var set edgerules.RuleSet
		if err := json.Unmarshal([]byte(row.Rules), &set); err != nil {
			// One app's unreadable rules must not take the firewall off every
			// other app. It is logged and that app is left unprotected, which
			// is visible on its page as a firewall that is on with no rules.
			c.log.Error("an app's firewall rules could not be read",
				"app", row.AppID, "hostname", row.Hostname, "error", err)
			continue
		}
		config.Sets[row.Hostname] = guard.Protected{AppID: row.AppID, RuleSet: set}
	}

	if value, _, err := c.db.GetSetting(ctx, settings.KeyTrustedProxies); err == nil && value != "" {
		config.TrustedProxies = []string{value}
	}
	// The tunnel's headers are the best country data there is and cost nothing,
	// but they may only be believed when the tunnel is actually in front.
	if tunnel, err := c.db.GetComponent(ctx, TunnelComponent); err == nil {
		config.Cloudflare = tunnel.Status == "installed"
	}

	config.CountryURL, _, _ = c.settingOr(ctx, settings.KeyGeoCountryURL, geoip.DefaultCountryURL)
	config.ASNURL, _, _ = c.settingOr(ctx, settings.KeyGeoASNURL, geoip.DefaultASNURL)
	return config, nil
}

// MaxGuardRulesBytes is what will be written into a ConfigMap.
//
// Kubernetes refuses an object above about a megabyte, and a refusal at apply
// time would be a rule somebody saved that never reached the cluster. Refusing
// in the panel, where they are looking at the form, is the difference.
const MaxGuardRulesBytes = 768 << 10

func renderGuardRules(config guard.Config) (string, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("render the firewall rules: %w", err)
	}
	if len(raw) > MaxGuardRulesBytes {
		return "", errdoc.New("guard.rules_too_large", "There are too many firewall rules to apply").
			WithCause("The rules for every protected app together come to %d KB, and Kubernetes will not store more than %d KB in one object.",
				len(raw)>>10, MaxGuardRulesBytes>>10).
			WithImpact("Nothing was changed in the cluster; the rules already there are still in force.").
			WithFix("Shorten the longest lists — a list of addresses is usually what grows — or turn the firewall off for an app that no longer needs it.").
			WithStatus(http.StatusRequestEntityTooLarge)
	}
	return string(raw), nil
}

func guardLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       GuardDeployment,
		"app.kubernetes.io/managed-by": version.Binary,
	}
}

func guardConfigMap(namespace, rules string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: GuardConfigMap, Namespace: namespace, Labels: guardLabels()},
		Data:       map[string]string{GuardRulesFile: rules},
	}
}

// guardObjects renders the guard, separately from applying it, so a test can
// read what would be created.
func guardObjects(namespace, image string, config guard.Config) ([]any, error) {
	rules, err := renderGuardRules(config)
	if err != nil {
		return nil, err
	}
	labels := guardLabels()
	replicas := int32(GuardReplicas)

	probe := func(delay, period int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz", Port: intstr.FromString("guard"),
			}},
			InitialDelaySeconds: delay,
			PeriodSeconds:       period,
			FailureThreshold:    3,
		}
	}

	deployment := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: GuardDeployment, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					// Traefik has no way to ignore an authorizer that does not
					// answer, so a rollout that takes a replica away before its
					// replacement is ready is a rollout that returns 500 to
					// every protected site for a moment.
					MaxUnavailable: ptrTo(intstr.FromInt32(0)),
					MaxSurge:       ptrTo(intstr.FromInt32(1)),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// Both replicas on one server is one server away from every
					// protected site being down. ScheduleAnyway, because on the
					// single-node cluster most installs start as the second
					// would otherwise be Pending for ever.
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
						MaxSkew:           1,
						TopologyKey:       "kubernetes.io/hostname",
						WhenUnsatisfiable: corev1.ScheduleAnyway,
						LabelSelector:     &metav1.LabelSelector{MatchLabels: labels},
					}},
					Containers: []corev1.Container{{
						Name:            "guard",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Args:            []string{"edge-guard"},
						Env: []corev1.EnvVar{
							{Name: "SKIFITY_GUARD_RULES", Value: GuardMountPath + "/" + GuardRulesFile},
							{Name: "SKIFITY_GUARD_DATA", Value: GuardDataPath},
							{Name: "SKIFITY_GUARD_ADDRESS", Value: fmt.Sprintf(":%d", kube.GuardPort)},
							{Name: "SKIFITY_POD_CIDR", Value: kube.PodCIDR},
							{Name: "SKIFITY_SERVICE_CIDR", Value: kube.ServiceCIDR},
						},
						Ports: []corev1.ContainerPort{{
							Name: "guard", ContainerPort: int32(kube.GuardPort),
						}},
						// A replica with no rules loaded reports itself not
						// ready, so a rollout never puts one in front of
						// traffic it cannot judge.
						ReadinessProbe: probe(2, 5),
						LivenessProbe:  probe(30, 30),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "rules", MountPath: GuardMountPath, ReadOnly: true},
							{Name: "data", MountPath: GuardDataPath},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("20m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							// The geo databases are memory-mapped and together
							// come to about 20 MB, so the ceiling is the file
							// sizes plus room to answer requests.
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptrTo(false),
							RunAsNonRoot:             ptrTo(true),
							// Not read-only: the geo databases are downloaded
							// into the emptyDir below, which is the only thing
							// this process writes.
							ReadOnlyRootFilesystem: ptrTo(true),
							Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:         &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
					}},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptrTo(true),
						RunAsUser:    ptrTo(int64(1000)),
						RunAsGroup:   ptrTo(int64(1000)),
						FSGroup:      ptrTo(int64(1000)),
					},
					// It reads a file and answers HTTP. It has no business with
					// the Kubernetes API, and a token in an outward-facing pod
					// is the first thing an attacker looks for.
					AutomountServiceAccountToken: ptrTo(false),
					Volumes: []corev1.Volume{
						{Name: "rules", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: GuardConfigMap},
							},
						}},
						{Name: "data", VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{
								SizeLimit: ptrTo(resource.MustParse("256Mi")),
							},
						}},
					},
				},
			},
		},
	}

	service := &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: kube.GuardService, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "guard", Port: int32(kube.GuardPort), TargetPort: intstr.FromString("guard"),
			}},
		},
	}

	return []any{guardConfigMap(namespace, rules), deployment, service}, nil
}
