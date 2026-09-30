package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"skifity/internal/errdoc"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/version"
)

// A front door that needs no public IP.
//
// Every server runs the ingress, so an app answers on all of them — but DNS
// names one, and Kubernetes cannot move a DNS record. The usual answers are a
// floating IP or a provider's load balancer, both of which cost money and tie
// the cluster to one provider.
//
// cloudflared is the other answer. It makes **outbound** connections to
// Cloudflare and traffic arrives through them, so:
//
//   - no public IP is needed, and no inbound port is opened. A server behind
//     NAT works. A machine at home works.
//   - two or three replicas are ordinary pods, which Kubernetes already spreads
//     across servers, so one server going down is handled by the same machinery
//     that handles everything else. There is no health check to configure and
//     no failover to design.
//
// What makes this a small amount of code rather than a large one is that
// nothing per-app is needed. cloudflared is pointed at the cluster's ingress
// controller and forwards the Host header untouched, which is what the ingress
// routes on. Every app that has a domain already works; a new one works the
// moment its Ingress exists.
//
// The trades, which belong in the documentation and do too: traffic passes
// through Cloudflare, the domain has to be on their DNS, replicas are steered
// by geography rather than round-robin, and the free plan caps an upload at
// 100 MB.

const (
	// TunnelDeployment is the name of the connector's Deployment.
	TunnelDeployment = "cloudflared"
	// TunnelReplicas is how many connectors run.
	//
	// Two, not one: one is a connector on a server, and the point of this is to
	// survive a server. Not more than two by default because each is a small
	// idle process and Cloudflare already opens four connections per replica to
	// at least two of its data centres.
	TunnelReplicas = 2
	// TunnelSecret holds the token, which is the credential for the whole
	// tunnel and is why it is a Secret rather than an argument.
	TunnelSecret = "cloudflared-token"
	// TunnelImage is the connector. A digest rather than a tag would be better
	// and cannot be one: Cloudflare expects the connector to be recent, and a
	// pinned digest is a thing nobody updates.
	TunnelImage = "cloudflare/cloudflared:2026.9.1"
	// TunnelMetricsPort is where cloudflared answers /ready, which is what
	// makes a replica that has lost its connections stop being routed to.
	TunnelMetricsPort = 2000
)

// TunnelComponent is the component name the tunnel installs under.
const TunnelComponent = "cloudflare-tunnel"

// RefreshCloudflareTunnel applies the token that is in Settings now.
//
// A token pasted into a box is a token that is going to be pasted again: the
// tunnel is deleted and made afresh, or it is moved to another Cloudflare
// account. Without this, saving the new one changes a row in SQLite and nothing
// else — the connectors keep running on the old credential until somebody
// restarts them, and the panel shows the component as installed the whole time.
//
// Clearing the token is how this integration is disconnected, the same as every
// other one in Settings, so it takes the connectors away rather than leaving
// them running on a credential the panel can no longer see.
//
// It does nothing when the component was never installed, so saving a token
// before installing is not an install by the back door.
func (c *Cluster) RefreshCloudflareTunnel(ctx context.Context) error {
	current, err := c.db.GetComponent(ctx, TunnelComponent)
	if err != nil {
		return err
	}
	if current.Status != "installed" {
		return nil
	}
	token, err := c.tunnelToken(ctx)
	if err != nil {
		return err
	}
	if token == "" {
		return c.removeCloudflareTunnel(ctx)
	}
	// Applied, not waited for. This runs inside a settings save, and a rollout
	// that cannot take a connector away before its replacement is connected is
	// a rollout nobody needs to watch.
	return c.applyTunnel(ctx, token)
}

// installCloudflareTunnel runs the connector, with the token the operator
// pasted into Settings.
func (c *Cluster) installCloudflareTunnel(ctx context.Context) error {
	token, err := c.tunnelToken(ctx)
	if err != nil {
		return err
	}
	if token == "" {
		return errNoTunnelToken()
	}
	if err := c.applyTunnel(ctx, token); err != nil {
		return err
	}
	return c.client.WaitForDeployment(ctx, c.client.SystemNamespace(), TunnelDeployment, 3*time.Minute)
}

// applyTunnel writes the Secret and the Deployment.
func (c *Cluster) applyTunnel(ctx context.Context, token string) error {
	namespace := c.client.SystemNamespace()
	return c.client.Applier().ApplyAll(ctx, tunnelObjects(namespace, token)...)
}

// removeCloudflareTunnel takes the connectors and the token out of the cluster.
func (c *Cluster) removeCloudflareTunnel(ctx context.Context) error {
	namespace := c.client.SystemNamespace()
	apps := c.client.Clientset().AppsV1().Deployments(namespace)
	if err := apps.Delete(ctx, TunnelDeployment, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("remove the Cloudflare connectors: %w", err)
	}
	secrets := c.client.Clientset().CoreV1().Secrets(namespace)
	if err := secrets.Delete(ctx, TunnelSecret, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("remove the stored tunnel token: %w", err)
	}
	c.log.Info("cloudflare tunnel removed", "reason", "the token was cleared in Settings")
	return c.db.SetComponent(ctx, store.ClusterComponent{
		Name:   TunnelComponent,
		Status: "removed",
		Detail: "The tunnel token was cleared, so the connectors were stopped. Paste a token and install it again to bring them back.",
	})
}

// errNoTunnelToken is what installing without a token answers.
//
// Refusing rather than installing something that cannot work is the whole
// difference between a component and a settings page nobody reads: a cloudflared
// with no token starts, fails to authenticate, and restarts for ever while the
// panel says it is installed.
func errNoTunnelToken() error {
	return errdoc.New("tunnel.no_token", "There is no Cloudflare tunnel token").
		WithCause("Settings has no tunnel token, and the connector cannot authenticate without one.").
		WithImpact("The tunnel was not installed. Nothing was changed in the cluster.").
		WithFix("Create a tunnel in the Cloudflare dashboard under Zero Trust, then Networks, then Tunnels. "+
			"Choose the Cloudflared connector, copy the token it shows, and paste it into "+
			"Settings, then Domains and HTTPS, then Cloudflare tunnel token. On the same tunnel, "+
			"add a public hostname — a wildcard such as *.apps.example.com covers every app at once — "+
			"with the service set to HTTP and the address %s.", settings.IngressServiceAddress).
		WithDocs("/docs/adding-servers#cloudflare-tunnel--automatic-free-and-needs-no-public-ip-at-all").
		WithStatus(http.StatusBadRequest)
}

// tunnelToken reads the sealed token. An empty string means there is none.
func (c *Cluster) tunnelToken(ctx context.Context) (string, error) {
	value, encrypted, err := c.db.GetSetting(ctx, settings.KeyCloudflareTunnelToken)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", nil
	}
	if !encrypted {
		return value, nil
	}
	if c.keyring == nil {
		return "", fmt.Errorf("the tunnel token is sealed and this panel has no keyring to open it")
	}
	plaintext, err := c.keyring.Open(value, settings.Context(settings.KeyCloudflareTunnelToken))
	if err != nil {
		return "", fmt.Errorf("read the Cloudflare tunnel token: %w", err)
	}
	return string(plaintext), nil
}

// tunnelObjects renders the connector, separately from applying it, so a test
// can read what would be created.
func tunnelObjects(namespace, token string) []any {
	labels := map[string]string{
		"app.kubernetes.io/name":       TunnelDeployment,
		"app.kubernetes.io/managed-by": version.Binary,
	}

	secret := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: TunnelSecret, Namespace: namespace, Labels: labels},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"token": token},
	}

	replicas := int32(TunnelReplicas)
	deployment := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: TunnelDeployment, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					// This is the way in. A rollout that takes a connector away
					// before its replacement is connected is a rollout with a
					// gap in the front door.
					MaxUnavailable: ptrTo(intstr.FromInt32(0)),
					MaxSurge:       ptrTo(intstr.FromInt32(1)),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					// The token reaches the container as an environment variable,
					// and an environment variable is read once when the process
					// starts: changing the Secret underneath a running pod changes
					// nothing at all. Naming the token's fingerprint here makes a
					// new token a new pod template, so applying it rolls the
					// connectors the ordinary way.
					//
					// It is a hash, of a value with far more entropy than anything
					// a hash can be walked back through.
					Annotations: map[string]string{
						version.LabelKey("token-fingerprint"): tokenFingerprint(token),
					},
				},
				Spec: corev1.PodSpec{
					// The whole point is surviving a server, so the replicas
					// must not land on the same one. ScheduleAnyway rather than
					// DoNotSchedule: on the single-node cluster most installs
					// start as, the second would otherwise be Pending for ever
					// and the tunnel would run at half strength with no sign.
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
						MaxSkew:           1,
						TopologyKey:       "kubernetes.io/hostname",
						WhenUnsatisfiable: corev1.ScheduleAnyway,
						LabelSelector:     &metav1.LabelSelector{MatchLabels: labels},
					}},
					Containers: []corev1.Container{{
						Name:  TunnelDeployment,
						Image: TunnelImage,
						// --no-autoupdate because a connector that replaces its
						// own binary inside a container is a pod that changed
						// without a deploy, and the image is how it is updated.
						Args: []string{
							"tunnel", "--no-autoupdate",
							"--metrics", fmt.Sprintf("0.0.0.0:%d", TunnelMetricsPort),
							"run",
						},
						Env: []corev1.EnvVar{{
							Name: "TUNNEL_TOKEN",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: TunnelSecret},
									Key:                  "token",
								},
							},
						}},
						Ports: []corev1.ContainerPort{{
							Name: "metrics", ContainerPort: TunnelMetricsPort,
						}},
						// /ready is cloudflared's own answer to "am I connected
						// to Cloudflare", which is the only thing that matters
						// about a connector and is not the same as "the process
						// is running".
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/ready", Port: intstr.FromString("metrics"),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       10,
							FailureThreshold:    3,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/ready", Port: intstr.FromString("metrics"),
								},
							},
							InitialDelaySeconds: 30,
							PeriodSeconds:       30,
							FailureThreshold:    5,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptrTo(false),
							RunAsNonRoot:             ptrTo(true),
							ReadOnlyRootFilesystem:   ptrTo(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
					}},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptrTo(true),
						RunAsUser:    ptrTo(int64(65532)),
					},
					// It talks to Cloudflare and to the ingress. It has no
					// business with the Kubernetes API.
					AutomountServiceAccountToken: ptrTo(false),
				},
			},
		},
	}

	return []any{secret, deployment}
}

// tokenFingerprint identifies a token without carrying it.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte("skifity-cloudflare-tunnel\x00" + token))
	return hex.EncodeToString(sum[:8])
}
