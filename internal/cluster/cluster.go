// Package cluster adapts the Kubernetes client to the interfaces the API layer
// declares, and owns installing the optional components.
package cluster

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"skifity/internal/api"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Cluster implements api.Cluster.
type Cluster struct {
	client *kube.Client
	db     *store.DB
	log    *slog.Logger
	// keyring opens the sealed settings a component needs to be installed at
	// all. Only the Cloudflare tunnel has one; it is nil in tests that do not
	// reach it, and the one place that reads it says so when it is.
	keyring *crypto.Keyring

	// installMu serialises component installation, so two apps enabling
	// scale-to-zero at the same moment do not both install KEDA.
	installMu sync.Mutex

	// registryMu keeps the registry sweep and a build apart. Builds hold it
	// for reading, so any number run at once; the sweep holds it for writing,
	// because deleting a blob while a push is in flight is the one thing the
	// registry's own documentation says will corrupt an image.
	registryMu sync.RWMutex
}

// New builds a Cluster.
func New(client *kube.Client, db *store.DB, keyring *crypto.Keyring, log *slog.Logger) *Cluster {
	return &Cluster{client: client, db: db, keyring: keyring, log: log}
}

// Client exposes the underlying Kubernetes client to the orchestrators.
func (c *Cluster) Client() *kube.Client { return c.client }

// Ping reports whether the Kubernetes API is reachable.
func (c *Cluster) Ping(ctx context.Context) error { return c.client.Ping(ctx) }

// Summary describes the cluster for the dashboard.
func (c *Cluster) Summary(ctx context.Context) (api.ClusterSummary, error) {
	raw, err := c.client.Summary(ctx)
	if err != nil {
		if kube.IsUnreachable(err) {
			return api.ClusterSummary{}, errdoc.ClusterUnreachable(err)
		}
		return api.ClusterSummary{}, err
	}
	out := api.ClusterSummary{
		Reachable:        raw.Reachable,
		KubernetesVer:    raw.KubernetesVersion,
		ReadyNodes:       raw.ReadyNodes,
		TotalCPUM:        raw.TotalCPUM,
		TotalMemoryMB:    raw.TotalMemoryMB,
		UsedCPUM:         raw.UsedCPUM,
		UsedMemoryMB:     raw.UsedMemoryMB,
		HighAvailability: raw.HighAvailability,
		Message:          raw.Message,
	}
	for _, n := range raw.Nodes {
		out.Nodes = append(out.Nodes, api.NodeInfo{
			Name: n.Name, Ready: n.Ready, Reason: n.Reason, Roles: n.Roles,
			InternalIP: n.InternalIP, ExternalIP: n.ExternalIP, OS: n.OS,
			Architecture: n.Architecture, KubeletVer: n.KubeletVer,
			CPUCapacityM: n.CPUCapacityM, MemCapacityMB: n.MemCapacityMB,
			CPUUsedM: n.CPUUsedM, MemUsedMB: n.MemUsedMB, PodCount: n.PodCount,
			Labels: n.Labels, Schedulable: n.Schedulable,
		})
	}
	return out, nil
}

// AppStatus describes one app's live state.
func (c *Cluster) AppStatus(ctx context.Context, namespace, appSlug string) (api.AppRuntimeStatus, error) {
	raw, err := c.client.AppStatus(ctx, namespace, appSlug)
	if err != nil {
		if kube.IsUnreachable(err) {
			return api.AppRuntimeStatus{}, errdoc.ClusterUnreachable(err)
		}
		return api.AppRuntimeStatus{}, err
	}
	out := api.AppRuntimeStatus{
		Phase: raw.Phase, Detail: raw.Detail,
		DesiredReplicas: raw.DesiredReplicas, ReadyReplicas: raw.ReadyReplicas,
		Image: raw.Image,
	}
	for _, inst := range raw.Instances {
		out.Instances = append(out.Instances, api.InstanceInfo{
			Name: inst.Name, Status: inst.Status, Ready: inst.Ready,
			Restarts: inst.Restarts, Node: inst.Node, StartedAt: inst.StartedAt,
			Message: inst.Message, CPUM: inst.CPUM, MemoryMB: inst.MemoryMB,
		})
	}
	return out, nil
}

// AppLogs streams an app's logs.
func (c *Cluster) AppLogs(ctx context.Context, namespace, appSlug string, opts api.LogOptions) (io.ReadCloser, error) {
	return c.client.AppLogs(ctx, namespace, appSlug, kube.LogOptions{
		TailLines: opts.TailLines, Follow: opts.Follow, Previous: opts.Previous,
	})
}

// RestartApp triggers a rolling restart.
func (c *Cluster) RestartApp(ctx context.Context, namespace, appSlug string) error {
	return c.client.RestartApp(ctx, namespace, appSlug)
}

// DeleteApp removes an app's Kubernetes objects.
func (c *Cluster) DeleteApp(ctx context.Context, namespace, appSlug string) error {
	return c.client.DeleteApp(ctx, namespace, appSlug)
}

// EnsureNamespace creates an environment's namespace with its guards.
//
// The whole environment rather than its namespace string: the Pod Security
// level is a column on that row, and a signature that takes only the name is a
// signature where a caller can forget it. Forgetting it here would silently put
// a lowered environment back to the strict level on the next deploy.
func (c *Cluster) EnsureNamespace(ctx context.Context, env store.Environment, teamID, projectID string) error {
	if err := c.client.EnsureNamespace(ctx, env.Namespace, teamID, projectID,
		kube.NormalizePodSecurity(env.PodSecurity)); err != nil {
		return err
	}
	// Best effort, and separate from the namespace's own guards: this is a
	// Traefik CRD, and a cluster running a different ingress controller does
	// not have the type at all. An app there still deploys; it just does not
	// get an automatic redirect from plain HTTP.
	if err := c.client.Applier().Apply(ctx, kube.BuildRedirectMiddleware(env.Namespace)); err != nil {
		c.log.Warn("could not create the HTTPS redirect middleware",
			"namespace", env.Namespace, "error", err)
	}
	return nil
}

// DeleteNamespace removes an environment's namespace.
func (c *Cluster) DeleteNamespace(ctx context.Context, namespace string) error {
	return c.client.DeleteNamespace(ctx, namespace)
}

// Manifests renders an app's Kubernetes objects as YAML, for the Advanced view.
//
// This is generated from the same code that applies them, so what a user reads
// here is what is actually running, not a hand-written approximation.
func (c *Cluster) Manifests(ctx context.Context, app store.App, env store.Environment) (string, error) {
	spec, err := c.SpecFor(ctx, app, env, app.Image)
	if err != nil {
		return "", err
	}
	objects := []any{
		kube.BuildDeployment(spec),
		kube.BuildService(spec),
		kube.BuildIngress(spec),
		kube.BuildHPA(spec),
		kube.BuildPDB(spec),
		kube.BuildInterceptorService(spec),
		kube.BuildHTTPScaledObject(spec),
	}
	for _, claim := range kube.BuildPVCs(spec) {
		objects = append(objects, claim)
	}

	// The scheduled commands too: this view says it shows what is running, and
	// a nightly job is as much a part of an app as its Deployment.
	jobs, err := c.db.ListAppJobs(ctx, app.ID)
	if err != nil {
		return "", err
	}
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		cron, err := kube.BuildCronJob(kube.RunSpec{
			App: spec, Name: kube.CronJobName(app.Slug, job.Name),
			Command: job.Command, Kind: kube.RunKindScheduled,
		}, job.Schedule)
		if err != nil {
			// A schedule that cannot be rendered is worth saying so about,
			// rather than quietly leaving out of a view that claims to be
			// complete.
			return "", fmt.Errorf("render the scheduled command %q: %w", job.Name, err)
		}
		objects = append(objects, cron)
	}

	var b strings.Builder
	for _, obj := range objects {
		if obj == nil || isNilPointer(obj) {
			continue
		}
		data, err := yaml.Marshal(obj)
		if err != nil {
			return "", fmt.Errorf("render manifest: %w", err)
		}
		b.WriteString("---\n")
		b.Write(data)
	}
	// The env Secret is shown by name only: printing an app's secrets into a
	// read-only view would undo the point of encrypting them.
	fmt.Fprintf(&b, "---\n# Secret/%s holds this app's environment variables.\n"+
		"# Its values are not shown here: secrets are write-only once set.\n",
		kube.ResourceName(app.Slug, "env"))
	return b.String(), nil
}

// EnsureAutoDomain gives an app the free URL it was promised.
//
// This is the difference between a deploy that ends with an address and one
// that ends with "now go and buy a domain". With a wildcard domain configured
// it is <app>-<env>.<wildcard>; without one it is an sslip.io address built
// from the cluster's public IP, which needs no DNS and no account.
//
// A certificate is only requested under a domain the operator owns. ADR-0015
// explains why the sslip.io address is served over plain HTTP: every Skifity
// install in the world shares sslip.io's Let's Encrypt rate limit.
func (c *Cluster) EnsureAutoDomain(ctx context.Context, app store.App, env store.Environment, teamID string) error {
	wildcard, _, err := c.db.GetSetting(ctx, settings.KeyWildcardDomain)
	if err != nil {
		return err
	}
	address := c.clusterAddress(ctx, teamID)
	tls := wildcard != ""

	domains, err := c.db.ListDomains(ctx, app.ID)
	if err != nil {
		return err
	}
	var current *store.Domain
	for i := range domains {
		if domains[i].Auto {
			current = &domains[i]
			break
		}
	}

	// An address this app already has and could still have been given is kept,
	// whichever of the two shapes it is. Otherwise an app that took the longer
	// name because a sibling held the short one would silently move to the
	// short one the day that sibling was deleted — a URL changing under the
	// user on an unrelated deploy.
	if current != nil && current.TLS == tls {
		for _, candidate := range []string{
			kube.AutoHostname(app.Slug, env.Slug, wildcard, address),
			kube.AutoHostnameFor(app.Slug, env.Slug, wildcard, address, app.ID),
		} {
			if candidate != "" && current.Hostname == candidate {
				return nil
			}
		}
	}

	hostname, err := c.freeAutoHostname(ctx, app, env, wildcard, address)
	if err != nil {
		return err
	}
	if hostname == "" {
		// No wildcard domain and no address to build an sslip.io name from.
		// Saying nothing is right: a hostname that resolves nowhere is worse
		// than no hostname at all.
		c.log.Debug("no automatic domain for this app yet", "app", app.ID)
		return nil
	}

	if current != nil {
		c.log.Info("moving an app's automatic domain",
			"app", app.ID, "from", current.Hostname, "to", hostname)
		return c.db.SetAutoDomain(ctx, current.ID, hostname, tls)
	}

	domain := store.Domain{AppID: app.ID, Hostname: hostname, Path: "/", TLS: tls, Auto: true}
	// Without TLS there is no certificate to wait for, so the address works as
	// soon as the ingress does and "waiting for DNS" would be a lie.
	if !tls {
		domain.Status = "active"
	}
	if err := c.db.CreateDomain(ctx, &domain); err != nil {
		return err
	}
	c.log.Info("gave an app its automatic domain", "app", app.ID, "hostname", hostname)
	return nil
}

// freeAutoHostname is the address this app should have: the readable one when
// nobody else holds it, and one with a short suffix when somebody does.
//
// Two projects both calling an app "web" is the usual case rather than a rare
// one, and it used to mean the second app had no address at all: the unique
// constraint refused the row and the deployer logged a warning nobody reads.
//
// The suffix comes from the app's id, so it is the same on every deploy. An
// address that changed under the user each time they deployed would be worse
// than not having one.
func (c *Cluster) freeAutoHostname(ctx context.Context, app store.App, env store.Environment, wildcard, address string) (string, error) {
	preferred := kube.AutoHostname(app.Slug, env.Slug, wildcard, address)
	if preferred == "" {
		return "", nil
	}
	owner, err := c.db.DomainOwner(ctx, preferred)
	if err != nil {
		return "", err
	}
	if owner == "" || owner == app.ID {
		return preferred, nil
	}

	fallback := kube.AutoHostnameFor(app.Slug, env.Slug, wildcard, address, app.ID)
	owner, err = c.db.DomainOwner(ctx, fallback)
	if err != nil {
		return "", err
	}
	if owner == "" || owner == app.ID {
		c.log.Info("an app's preferred address was taken, so it gets a longer one",
			"app", app.ID, "preferred", preferred, "hostname", fallback)
		return fallback, nil
	}
	// Both taken means the suffix collided with another app's suffix, which is
	// a 1-in-a-million sha256 prefix clash. Saying so beats looping.
	return "", errdoc.New("domain.no_free_address", "This app could not be given an address").
		WithCause("Both %s and %s are already used by other apps.", preferred, fallback).
		WithImpact("The app is deployed and has no automatic URL. A domain of your own still works.").
		WithFix("Rename the app, or add a domain of your own under the app's Domains tab.")
}

// PublicAddress is where a domain of somebody's own should point.
//
// It is the same address the panel gives an app's automatic subdomain, which
// is the point: an operator reading it off the Domains tab and an app reaching
// its own URL must not disagree. It reads the panel's own records only, so it
// answers while the cluster is unreachable — which is exactly when somebody is
// looking at DNS.
func (c *Cluster) PublicAddress(ctx context.Context, teamID string) string {
	return c.clusterAddress(ctx, teamID)
}

// clusterAddress is the public IP apps are reached on.
//
// The setting wins, because an operator behind a load balancer knows something
// the panel cannot see. Otherwise a control-plane server's own external
// address is used, so a single-server install works with nothing configured.
func (c *Cluster) clusterAddress(ctx context.Context, teamID string) string {
	if ip, _, err := c.db.GetSetting(ctx, settings.KeyClusterIP); err == nil && ip != "" {
		return strings.TrimSpace(ip)
	}
	servers, err := c.db.ListServers(ctx, teamID)
	if err != nil {
		return ""
	}
	fallback := ""
	for _, server := range servers {
		if server.Status != store.ServerReady || server.ExternalIP == "" {
			continue
		}
		if server.Role == "control-plane" {
			return server.ExternalIP
		}
		if fallback == "" {
			fallback = server.ExternalIP
		}
	}
	return fallback
}

// SpecFor builds the AppSpec for an app, reading the settings that affect it.
func (c *Cluster) SpecFor(ctx context.Context, app store.App, env store.Environment, image string) (kube.AppSpec, error) {
	project, err := c.db.GetProject(ctx, env.ProjectID)
	if err != nil {
		return kube.AppSpec{}, err
	}

	spec := kube.AppSpec{
		Name:          app.Slug,
		Namespace:     env.Namespace,
		AppID:         app.ID,
		ProjectID:     project.ID,
		TeamID:        project.TeamID,
		Environment:   env.Slug,
		DisplayName:   app.Name,
		Image:         image,
		Port:          app.Port,
		HealthPath:    app.HealthPath,
		Replicas:      app.Replicas,
		CPURequestM:   app.CPURequestM,
		CPULimitM:     app.CPULimitM,
		MemRequestMB:  app.MemRequestMB,
		MemLimitMB:    app.MemLimitMB,
		Autoscale:     app.Autoscale,
		MinReplicas:   app.MinReplicas,
		MaxReplicas:   app.MaxReplicas,
		CPUTarget:     app.CPUTarget,
		MemoryTarget:  app.MemoryTarget,
		ScaleToZero:   app.ScaleToZero,
		EnvFromSecret: kube.ResourceName(app.Slug, "env"),
		// Spreading matters as soon as there is more than one instance, and
		// costs nothing when there is one.
		SpreadAcrossServers: true,
		PodSecurity:         kube.NormalizePodSecurity(env.PodSecurity),
		// An app whose source is a Git repository or an uploaded folder runs
		// an image this panel's own builder produced, so what is inside it is
		// known. Anything else is
		// an image reference somebody typed or a template chose, and the only
		// honest thing to say about its user is what the image itself says.
		ImageBuiltHere: app.SourceType == "git" || app.SourceType == "upload",
	}

	// An image in somebody's own registry needs a credential to pull, and the
	// same Secret the build pushed with is the one to read. Named only when an
	// external registry is configured: a pull secret that does not exist stops
	// every pod on a panel that never needed one.
	external, _, err := c.db.GetSetting(ctx, settings.KeyRegistryURL)
	if err != nil {
		return kube.AppSpec{}, err
	}
	if strings.TrimSpace(external) != "" {
		spec.ImagePullSecret = kube.RegistrySecretName
	}
	if app.StartCommand != "" {
		// A start command is a shell line, so it runs through a shell rather
		// than being split here and getting quoting subtly wrong.
		spec.Command = []string{"/bin/sh", "-c"}
		spec.Args = []string{app.StartCommand}
	}

	volumes, err := c.db.ListVolumes(ctx, app.ID)
	if err != nil {
		return spec, err
	}
	for _, v := range volumes {
		spec.Volumes = append(spec.Volumes, kube.VolumeSpec{
			Name: v.Name, MountPath: v.MountPath, SizeGB: v.SizeGB, StorageClass: v.StorageClass,
		})
	}

	domains, err := c.db.ListDomains(ctx, app.ID)
	if err != nil {
		return spec, err
	}
	for _, d := range domains {
		spec.Domains = append(spec.Domains, kube.DomainSpec{Hostname: d.Hostname, Path: d.Path, TLS: d.TLS})
	}

	// An app with the firewall switched on gets the guard's middleware in
	// front of its Ingress. Read here rather than passed in, because every
	// caller that renders an app's objects has to get this right and one that
	// forgot would be an app whose rules quietly stopped being applied.
	protected, err := c.db.AppHasFirewall(ctx, app.ID)
	if err != nil {
		return spec, err
	}
	spec.Protected = protected

	// The issuer only exists once an ACME email has been configured, and
	// referencing a missing issuer leaves certificates stuck forever.
	if email, _, err := c.db.GetSetting(ctx, settings.KeyACMEEmail); err == nil && email != "" {
		spec.ClusterIssuer = ClusterIssuerName
	}
	return spec, nil
}

// ClusterIssuerName is the cert-manager ClusterIssuer the panel creates.
const ClusterIssuerName = "skifity-letsencrypt"

// ControlPlaneCount is how many nodes actually run the cluster.
//
// Read from the cluster rather than counted in the panel's own table, because
// those are two different numbers. A panel installed by install.sh runs in a
// cluster it has no server row for, and on a panel with more than one team the
// rows are split between them — so counting rows undercounts by at least one
// and is scoped to the wrong thing. Both directions matter here: too low
// refuses a removal that is safe, too high permits one that is not.
func (c *Cluster) ControlPlaneCount(ctx context.Context) (int, error) {
	summary, err := c.client.Summary(ctx)
	if err != nil {
		if kube.IsUnreachable(err) {
			return 0, errdoc.ClusterUnreachable(err)
		}
		return 0, err
	}
	count := 0
	for _, node := range summary.Nodes {
		for _, role := range node.Roles {
			// k3s labels its own; "master" is what an older cluster called it.
			if role == "control-plane" || role == "master" {
				count++
				break
			}
		}
	}
	return count, nil
}

// QuotaUsage reports how much of an environment's ceiling is in use.
func (c *Cluster) QuotaUsage(ctx context.Context, namespace string) (api.EnvironmentQuota, error) {
	raw, err := c.client.QuotaUsage(ctx, namespace)
	if err != nil {
		if kube.IsUnreachable(err) {
			return api.EnvironmentQuota{}, errdoc.ClusterUnreachable(err)
		}
		return api.EnvironmentQuota{}, err
	}
	out := api.EnvironmentQuota{Found: raw.Found}
	for _, item := range raw.Items {
		out.Items = append(out.Items, api.EnvironmentQuotaItem{
			Resource: item.Resource, Used: item.Used, Hard: item.Hard,
			UsedValue: item.UsedValue, HardValue: item.HardValue, Percent: item.Percent(),
		})
	}
	return out, nil
}

// ComponentStatus reports whether an optional add-on is installed.
func (c *Cluster) ComponentStatus(ctx context.Context, name string) (store.ClusterComponent, error) {
	return c.db.GetComponent(ctx, name)
}

// InstallComponent installs an optional add-on, once.
//
// Components are installed on first use rather than at install time, which is
// what keeps a fresh install small enough for a 2 GB VPS.
func (c *Cluster) InstallComponent(ctx context.Context, name string) error {
	def, ok := settings.LookupComponent(name)
	if !ok {
		return fmt.Errorf("%q is not a component Skifity installs", name)
	}

	c.installMu.Lock()
	defer c.installMu.Unlock()

	current, err := c.db.GetComponent(ctx, name)
	if err != nil {
		return err
	}
	if current.Status == "installed" {
		return nil
	}

	if err := c.db.SetComponent(ctx, store.ClusterComponent{
		Name: name, Status: "installing", Detail: "",
	}); err != nil {
		return err
	}

	c.log.Info("installing cluster component", "component", name, "title", def.Title)
	// Installation can take minutes; it must not be bound to a request that the
	// browser may have already abandoned.
	installCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()

	if err := c.installComponent(installCtx, name); err != nil {
		_ = c.db.SetComponent(ctx, store.ClusterComponent{
			Name: name, Status: "failed", Detail: err.Error(),
		})
		return fmt.Errorf("install %s: %w", def.Title, err)
	}

	return c.db.SetComponent(ctx, store.ClusterComponent{
		Name: name, Status: "installed", InstalledAt: time.Now(), Detail: "",
	})
}

// isNilPointer reports whether a non-nil interface holds a nil pointer, which
// the builders return for objects an app does not need, such as an Ingress for
// an app with no domains.
func isNilPointer(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// CertificateState is what cert-manager says about one app's certificate.
type CertificateState struct {
	// Found is false when no Certificate exists, which is the normal state for
	// an app with no TLS domain and for a cluster with no cert-manager.
	Found  bool
	Ready  bool
	Reason string
}

// CertificateStatus reads the Certificate cert-manager created for an app.
//
// The ingress asks for TLS with an annotation, so the Certificate is named
// after the secret the ingress references; one object covers every TLS
// hostname on the app.
func (c *Cluster) CertificateStatus(ctx context.Context, namespace, name string) (CertificateState, error) {
	obj, err := c.client.Applier().Get(ctx, "cert-manager.io/v1", "Certificate", namespace, name)
	if err != nil {
		// No cert-manager, or no certificate yet. Neither is a failure to
		// report: an app without a TLS domain never has one.
		if kube.IsNotFound(err) || meta.IsNoMatchError(err) {
			return CertificateState{}, nil
		}
		if kube.IsUnreachable(err) {
			return CertificateState{}, errdoc.ClusterUnreachable(err)
		}
		return CertificateState{}, err
	}

	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if condition["type"] != "Ready" {
			continue
		}
		reason, _ := condition["message"].(string)
		if reason == "" {
			reason, _ = condition["reason"].(string)
		}
		return CertificateState{Found: true, Ready: condition["status"] == "True", Reason: reason}, nil
	}
	// A Certificate with no Ready condition has only just been created.
	return CertificateState{Found: true, Ready: false, Reason: "The certificate has been requested."}, nil
}
