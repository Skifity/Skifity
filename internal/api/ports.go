package api

import (
	"context"
	"io"
	"time"

	"skifity/internal/kube"
	"skifity/internal/logdrain"
	"skifity/internal/plugins"
	"skifity/internal/store"
)

// The interfaces in this file are what the HTTP layer needs from the rest of the
// panel. They are declared here, on the consumer side, so that handlers can be
// tested without a Kubernetes cluster, an SSH server or a container registry.

// NodeInfo is one cluster node as the panel presents it.
type NodeInfo struct {
	Name          string   `json:"name"`
	Ready         bool     `json:"ready"`
	Reason        string   `json:"reason,omitempty"`
	Roles         []string `json:"roles"`
	InternalIP    string   `json:"internal_ip"`
	ExternalIP    string   `json:"external_ip"`
	OS            string   `json:"os"`
	Architecture  string   `json:"architecture"`
	KubeletVer    string   `json:"kubelet_version"`
	CPUCapacityM  int64    `json:"cpu_capacity_m"`
	MemCapacityMB int64    `json:"memory_capacity_mb"`
	CPUUsedM      int64    `json:"cpu_used_m"`
	MemUsedMB     int64    `json:"memory_used_mb"`
	// UsageKnown is false when metrics-server had nothing for the node: the
	// two above are then not zero, they are not known.
	UsageKnown  bool              `json:"usage_known"`
	PodCount    int               `json:"pod_count"`
	Labels      map[string]string `json:"labels,omitempty"`
	Schedulable bool              `json:"schedulable"`
	// DiskUsedMB and DiskCapacityMB come from the watcher's last reading,
	// at most a minute old: the kubelet's summary is too heavy to ask for on
	// every page. Zero capacity is not known yet.
	DiskUsedMB     int64 `json:"disk_used_mb,omitempty"`
	DiskCapacityMB int64 `json:"disk_capacity_mb,omitempty"`

	// GPUs are the cards the server's device plugins advertise, one entry a
	// vendor, with how many the pods placed there hold.
	GPUs []NodeGPU `json:"gpus"`
	// GPUHardware is how an NVIDIA card is known to be in the server while no
	// device plugin advertises one: "nfd" (Node Feature Discovery saw it),
	// "gpu-feature-discovery", "label" (an administrator marked it), or empty
	// when nothing can tell.
	GPUHardware string `json:"gpu_hardware,omitempty"`
	// NFD is true when Node Feature Discovery labels the server, which is
	// what makes "it found no NVIDIA card" mean something.
	NFD bool `json:"nfd"`
	// GPULabelled is true when an administrator marked it as having an NVIDIA
	// card, which is what puts the panel's device plugin there without NFD.
	GPULabelled bool `json:"gpu_labelled"`
	// GPUPlugin is the panel's NVIDIA device plugin on this server, when it
	// has been placed here.
	GPUPlugin *GPUPluginState `json:"gpu_plugin,omitempty"`
}

// NodeGPU is one vendor's cards on a server.
type NodeGPU struct {
	Vendor      string `json:"vendor"`
	Resource    string `json:"resource"`
	Capacity    int64  `json:"capacity"`
	Allocatable int64  `json:"allocatable"`
	InUse       int64  `json:"in_use"`
	// Product and MemoryMB are GPU feature discovery's, when it runs.
	Product  string `json:"product,omitempty"`
	MemoryMB int64  `json:"memory_mb,omitempty"`
}

// GPUPluginState is whether the device plugin is up on a server, and why
// not in the kubelet's words when it is not.
type GPUPluginState struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
}

// K3sRelease is the latest k3s release of one minor version.
type K3sRelease struct {
	Channel string `json:"channel"`
	Version string `json:"version"`
	// Stable is the release k3s recommends.
	Stable bool `json:"stable,omitempty"`
}

// NodeDisk is how full a node's disk is, as the kubelet measures it.
type NodeDisk struct {
	UsedMB     int64
	CapacityMB int64
}

// InstanceInfo is one running instance of an app, in human terms.
type InstanceInfo struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Ready     bool      `json:"ready"`
	Restarts  int       `json:"restarts"`
	Node      string    `json:"node"`
	StartedAt time.Time `json:"started_at,omitzero"`
	Message   string    `json:"message,omitempty"`
	CPUM      int64     `json:"cpu_m"`
	MemoryMB  int64     `json:"memory_mb"`
	// UsageKnown is false when metrics-server had nothing for the instance.
	UsageKnown bool `json:"usage_known"`
}

// AppRuntimeStatus is what the app page shows at the top.
type AppRuntimeStatus struct {
	Phase           string         `json:"phase"`
	Detail          string         `json:"detail,omitempty"`
	DesiredReplicas int            `json:"desired_replicas"`
	ReadyReplicas   int            `json:"ready_replicas"`
	Instances       []InstanceInfo `json:"instances"`
	Image           string         `json:"image,omitempty"`
	URLs            []string       `json:"urls,omitempty"`
	// InternalAddress is how the environment's other apps reach an internal
	// one: its name and port, as in Compose.
	InternalAddress string `json:"internal_address,omitempty"`
}

// LogOptions is what to read from an app's logs, and from where.
type LogOptions struct {
	// TailLines bounds how far back to read.
	TailLines int64
	// Follow keeps the stream open.
	Follow bool
	// Previous reads the container that ran before the current one, which is
	// the only copy of why a crash-looping app crashed.
	Previous bool
}

// ClusterSummary is the cluster overview for a team.
type ClusterSummary struct {
	Reachable        bool       `json:"reachable"`
	KubernetesVer    string     `json:"kubernetes_version,omitempty"`
	Nodes            []NodeInfo `json:"nodes"`
	ReadyNodes       int        `json:"ready_nodes"`
	TotalCPUM        int64      `json:"total_cpu_m"`
	TotalMemoryMB    int64      `json:"total_memory_mb"`
	UsedCPUM         int64      `json:"used_cpu_m"`
	UsedMemoryMB     int64      `json:"used_memory_mb"`
	HighAvailability bool       `json:"high_availability"`
	Message          string     `json:"message,omitempty"`
}

// Cluster is the read side of Kubernetes plus the few direct actions the panel
// takes that are not part of a larger orchestration.
type Cluster interface {
	// Ping reports whether the Kubernetes API is reachable.
	Ping(ctx context.Context) error
	// Summary describes the cluster for the dashboard.
	Summary(ctx context.Context) (ClusterSummary, error)
	// AppStatus describes one app's live state.
	AppStatus(ctx context.Context, namespace, appSlug string) (AppRuntimeStatus, error)
	// PublicAddress is where a domain of somebody's own should point. Empty
	// when the panel cannot work it out and nobody has set it.
	PublicAddress(ctx context.Context, teamID string) string
	// AppLogs streams an app's logs.
	AppLogs(ctx context.Context, namespace, appSlug string, opts LogOptions) (io.ReadCloser, error)
	// RestartApp triggers a rolling restart without changing anything else.
	RestartApp(ctx context.Context, namespace, appSlug string) error
	// DeleteApp removes an app's Kubernetes objects.
	DeleteApp(ctx context.Context, namespace, appSlug string) error
	// EnsureNamespace creates a namespace with its quota, limits and policies.
	EnsureNamespace(ctx context.Context, env store.Environment, teamID, projectID string) error
	// DeleteNamespace removes an environment's namespace and everything in it.
	DeleteNamespace(ctx context.Context, namespace string) error
	// Manifests renders the Kubernetes objects for an app, for the Advanced tab.
	Manifests(ctx context.Context, app store.App, env store.Environment) (string, error)
	// AppEvents lists what Kubernetes said about an app's objects, newest
	// first, with the app's secrets kept out of every message.
	AppEvents(ctx context.Context, app store.App, env store.Environment) ([]ObjectEvent, error)
	// DatabaseEvents does the same for a managed database's objects.
	DatabaseEvents(ctx context.Context, database store.Database, env store.Environment) ([]ObjectEvent, error)
	// InstallComponent installs an optional add-on on first use.
	InstallComponent(ctx context.Context, name string) error
	// InstallPlugin issues a plugin's credentials and starts its container.
	InstallPlugin(ctx context.Context, record store.Plugin, manifest plugins.Manifest, token string) error
	// RestartPlugin rolls a plugin, which is what a settings change needs: a
	// Secret read into the environment is read once when the process starts.
	RestartPlugin(ctx context.Context, id string) error
	// StartPlugin and StopPlugin switch a plugin on and off without losing its
	// settings or its token.
	StartPlugin(ctx context.Context, id string) error
	StopPlugin(ctx context.Context, id string) error
	// RemovePlugin takes a plugin's namespace away, and everything in it.
	RemovePlugin(ctx context.Context, id string) error
	// NewPluginSigningSecret makes the sealed key a plugin verifies events with.
	NewPluginSigningSecret(id string) (string, error)
	// RefreshFirewall writes the current rules into the cluster and puts the
	// guard's middleware in front of every protected app. Saving a rule that
	// never reaches the cluster is a firewall that exists only in SQLite.
	RefreshFirewall(ctx context.Context) error
	// RefreshCloudflareTunnel re-applies the tunnel token from Settings, so
	// that changing it in the panel changes what the connectors are using.
	RefreshCloudflareTunnel(ctx context.Context) error
	// ComponentStatus reports whether an add-on is present.
	ComponentStatus(ctx context.Context, name string) (store.ClusterComponent, error)
	// ComponentVersion is the version of an add-on this panel installs now,
	// or "" for one that moves with the panel.
	ComponentVersion(ctx context.Context, name string) string
	// UpgradeInstalledComponent applies that version over an installed one.
	UpgradeInstalledComponent(ctx context.Context, name string) error
	// K3sUpgradeNodes is every node's role, readiness and k3s version.
	K3sUpgradeNodes(ctx context.Context) ([]kube.UpgradeNode, error)
	// StartK3sUpgrade hands an upgrade to the system-upgrade-controller.
	StartK3sUpgrade(ctx context.Context, target kube.K3sVersion) error
	// ForeignUpgradePlans names upgrade Plans the panel did not write.
	ForeignUpgradePlans(ctx context.Context) ([]string, error)
	// K3sReleases is the latest k3s release of each minor version.
	K3sReleases(ctx context.Context) ([]K3sRelease, error)
	// QuotaUsage reports how much of an environment's limits are in use.
	QuotaUsage(ctx context.Context, namespace string) (EnvironmentQuota, error)
	// ControlPlaneCount is how many nodes actually run the cluster, which is
	// not the same as how many rows the panel has for one team.
	ControlPlaneCount(ctx context.Context) (int, error)
	// SetNodeGPULabel marks a server as having an NVIDIA card, or unmarks
	// it, which decides whether the device plugin runs there.
	SetNodeGPULabel(ctx context.Context, node string, nvidia bool) error
}

// LogCollector is the collector that ships the teams' logs to their drains:
// one on every server, rendered from every team's drains at once.
type LogCollector interface {
	// RefreshLogDrains renders every enabled drain into the collector and
	// applies it, or takes the collector away when nothing is to be sent.
	RefreshLogDrains(ctx context.Context) error
	// LogCollectorStatus is how the collector is doing, from the DaemonSet,
	// its pods and its events.
	LogCollectorStatus(ctx context.Context) (logdrain.CollectorStatus, error)
}

// EnvironmentQuota is how much of an environment's ceiling is in use.
//
// Every environment has had a ResourceQuota since the first release and nothing
// showed it, so the first sign of reaching one was a deployment that failed with
// a message about a resource nobody had heard of.
type EnvironmentQuota struct {
	Found bool                   `json:"found"`
	Items []EnvironmentQuotaItem `json:"items"`
}

// EnvironmentQuotaItem is one limit and what has been used against it.
type EnvironmentQuotaItem struct {
	Resource  string `json:"resource"`
	Used      string `json:"used"`
	Hard      string `json:"hard"`
	UsedValue int64  `json:"used_value"`
	HardValue int64  `json:"hard_value"`
	Percent   int    `json:"percent"`
}

// Provisioner turns a bare VPS into a cluster node and back.
type Provisioner interface {
	// AddServer starts provisioning and returns the operation tracking it.
	AddServer(ctx context.Context, req AddServerRequest) (store.Operation, error)
	// RetryServer resumes a failed provision from the step that failed.
	RetryServer(ctx context.Context, serverID string) (store.Operation, error)
	// CreateCloudServer orders a machine from a cloud provider and joins it,
	// as one operation.
	CreateCloudServer(ctx context.Context, req CreateCloudServerRequest) (store.Operation, error)
	// RemoveServer cordons, drains and removes a node, then cleans the machine
	// or, for one the panel created, deletes it at the provider.
	RemoveServer(ctx context.Context, serverID string, opts RemoveServerOptions) (store.Operation, error)
	// PromoteServer makes a worker a control plane node.
	PromoteServer(ctx context.Context, serverID string) (store.Operation, error)
	// Cancel stops a running operation.
	Cancel(ctx context.Context, operationID string) error
	// AuditServer reads how a server stands up to the internet, changing
	// nothing on it.
	AuditServer(ctx context.Context, serverID string) (HardeningReport, error)
	// TurnOffSSHPasswords makes a server's SSH take keys only.
	TurnOffSSHPasswords(ctx context.Context, serverID string) (HardeningReport, error)
}

// A HardeningFinding is one check of a server. The interface translates it
// as servers.hardening.checks.<code>.<state>; Detail and Fix are the English
// for the CLI and the API.
type HardeningFinding struct {
	Code string `json:"code"`
	// State is which of the check's answers this is — on, off, absent — and
	// what the interface's sentence is looked up by.
	State  string `json:"state"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	// Firewall names the firewall found, for the sentence that says so.
	Firewall string `json:"firewall,omitempty"`
}

// HardeningReport is a server's findings, worst first, and whether the one
// change the panel can make for it is on offer.
type HardeningReport struct {
	ServerID  string             `json:"server_id"`
	CheckedAt time.Time          `json:"checked_at"`
	Findings  []HardeningFinding `json:"findings"`
	// CanTurnOffPasswords is true when SSH still takes a password and the
	// panel can safely stop it: it signs in with a key, and the daemon reads
	// the drop-in directory the change is written to.
	CanTurnOffPasswords bool `json:"can_turn_off_passwords"`
}

// AddServerRequest is what the Add Server form submits.
type AddServerRequest struct {
	TeamID     string `json:"-"`
	CreatedBy  string `json:"-"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	SSHPort    int    `json:"ssh_port"`
	SSHUser    string `json:"ssh_user"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	// Location and Size become node labels so apps can be steered.
	Location string `json:"location,omitempty"`
	Size     string `json:"size,omitempty"`
	// ControlPlane joins the node as a control plane member for HA.
	ControlPlane bool `json:"control_plane,omitempty"`
}

// CreateCloudServerRequest orders a server from one of the team's cloud
// connections. Everything after the order is the add-server join.
type CreateCloudServerRequest struct {
	TeamID    string `json:"-"`
	CreatedBy string `json:"-"`
	// ProviderID is the team's connection to order with.
	ProviderID string `json:"provider_id"`
	// Name is the machine's name at the provider, its hostname, and the
	// node's name in the cluster.
	Name       string `json:"name"`
	Location   string `json:"location"`
	ServerType string `json:"server_type"`
	// Image is the operating system; empty is Ubuntu 24.04.
	Image string `json:"image,omitempty"`
	// SSHAccess is anywhere, the default, or cluster.
	SSHAccess    string `json:"ssh_access,omitempty"`
	ControlPlane bool   `json:"control_plane,omitempty"`
	// Arch is the server type's, amd64 or arm64, which the API reads from
	// the provider's catalogue.
	Arch string `json:"-"`
}

// RemoveServerOptions say what happens to the machine once it has left the
// cluster.
type RemoveServerOptions struct {
	// Wipe uninstalls k3s from a machine that stays.
	Wipe bool
	// DeleteMachine deletes the machine at the provider it was created at.
	// Only a server the panel created can have it; see handleRemoveServer.
	DeleteMachine bool
}

// DeployRequest starts a deployment.
type DeployRequest struct {
	AppID     string
	Trigger   string
	CommitSHA string
	CreatedBy string
	// Force skips the build-fingerprint shortcut and rebuilds regardless. For
	// a promotion, it deploys the image even though this app would have built
	// it differently.
	Force bool
	// Image, for a promotion, is another environment's image to run as it is,
	// with the commit it was built from and the fingerprint of its build.
	// Nothing is built.
	Image         string
	CommitMessage string
	CommitAuthor  string
	Fingerprint   string
	// AcceptVulnerabilities lets the deploy go ahead when its image has a
	// critical vulnerability with a fix and the panel is set to stop those.
	// The handler that sets it records it in the activity log.
	AcceptVulnerabilities bool
}

// RunHandle identifies a one-off command that has been started.
type RunHandle struct {
	Name      string `json:"name"`
	Namespace string `json:"-"`
}

// Deployer builds and rolls out apps.
type Deployer interface {
	// Deploy queues a deployment and returns its record.
	Deploy(ctx context.Context, req DeployRequest) (store.Deployment, error)
	// Rollback re-applies a previous deployment's image and runtime spec.
	Rollback(ctx context.Context, appID, deploymentID, actorID string) (store.Deployment, error)
	// Cancel stops an in-flight build or rollout.
	Cancel(ctx context.Context, deploymentID string) error
	// Sync applies an app's current configuration without building.
	Sync(ctx context.Context, appID string) error
	// ScalingReadiness looks for patterns that break with several instances.
	ScalingReadiness(ctx context.Context, appID string) ([]ScalingFinding, error)
	// RunOnce starts a command in the app's own image with the app's own
	// variables, which is where a migration runs.
	RunOnce(ctx context.Context, appID, command string) (RunHandle, error)
	// RunLogs reads a run's output.
	RunLogs(ctx context.Context, appID, name string, follow bool) (io.ReadCloser, error)
	// RunResult says how a run ended, waiting a little for its container to
	// stop once its output has.
	RunResult(ctx context.Context, appID, name string) (RunResult, error)
	// RefreshReferences reads an app's variables from their secret managers
	// again, and rolls the app out when any of them changed: a rollout for
	// runtime values, a build of the running version for build-time ones.
	RefreshReferences(ctx context.Context, appID, actorID string) (ReferenceRefresh, error)
}

// ReferenceRefresh is what refreshing an app's variables from their secret
// managers found and did. It names variables and never holds a value.
type ReferenceRefresh struct {
	// References is how many of the app's variables are read from a secret
	// manager.
	References int `json:"references"`
	// Changed are the ones whose values changed since they last reached the
	// app.
	Changed []string `json:"changed"`
	// BuildTimeChanged are those of them the build reads, which is why the
	// app is being rebuilt.
	BuildTimeChanged []string `json:"build_time_changed"`
	// RolledOut is a rollout with the new values and the image the app has.
	RolledOut bool `json:"rolled_out"`
	// Deployment is the build started because a build-time value changed.
	Deployment *store.Deployment `json:"deployment,omitempty"`
	// NotDeployed is an app with nothing running yet, so nothing to roll out.
	NotDeployed bool `json:"not_deployed,omitempty"`
}

// Scanner looks for known vulnerabilities in the images apps run.
type Scanner interface {
	// Scan queues a scan of the image an app runs now, or answers the one
	// already queued or running.
	Scan(ctx context.Context, appID, requestedBy string) (store.ImageScan, error)
}

// ScalingFinding is one reason an app may not survive being scaled out.
type ScalingFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
	// Args are the values interpolated into the three sentences above, so the
	// panel can show the same values in a sentence from its own locale. The
	// English stays: it is the fallback, and what the CLI and an assistant read.
	Args ScalingArgs `json:"args,omitzero"`
}

// ScalingArgs carries one finding's interpolated values, in the order the Go
// format string used them. The locale writes {{0}} where the English writes %s.
type ScalingArgs struct {
	Title  []string `json:"title,omitempty"`
	Detail []string `json:"detail,omitempty"`
	Fix    []string `json:"fix,omitempty"`
}

// DatabaseManager provisions managed data stores.
type DatabaseManager interface {
	// Create provisions a database, installing its operator on first use.
	Create(ctx context.Context, env store.Environment, req CreateDatabaseRequest) (store.Database, error)
	// Delete removes a database and its storage.
	Delete(ctx context.Context, databaseID string) error
	// Credentials returns the connection details, decrypted.
	Credentials(ctx context.Context, databaseID string) (DatabaseCredentials, error)
	// Link injects a connection string into an app as a variable.
	Link(ctx context.Context, databaseID, appID, varName string) error
	// Unlink removes the injected variable.
	Unlink(ctx context.Context, databaseID, appID string) error
	// Status refreshes a database's state from the cluster.
	Status(ctx context.Context, databaseID string) (string, string, error)
	// Stop scales a database to nothing and keeps its disk.
	Stop(ctx context.Context, databaseID string) (store.Database, error)
	// Start brings a stopped database back, and waits for it in the
	// background.
	Start(ctx context.Context, databaseID string) (store.Database, error)
	// Resize changes what a database reserves and may use, and grows its
	// disk. The manager checks the request against the engine, the
	// environment's quota and the storage class.
	Resize(ctx context.Context, databaseID string, req ResizeDatabaseRequest) (store.Database, error)
	// ChangePassword gives a database a new password, generated when
	// password is empty, and every linked app the new connection string.
	ChangePassword(ctx context.Context, databaseID, password, userID string) (store.Operation, error)
}

// ResizeDatabaseRequest is what PATCH /api/databases/{id} changes. A field
// left out is left as it is.
type ResizeDatabaseRequest struct {
	CPURequestM  *int `json:"cpu_request_m,omitempty"`
	CPULimitM    *int `json:"cpu_limit_m,omitempty"`
	MemRequestMB *int `json:"mem_request_mb,omitempty"`
	MemLimitMB   *int `json:"mem_limit_mb,omitempty"`
	StorageGB    *int `json:"storage_gb,omitempty"`
}

// ImportRequest is a dump on its way into a database.
type ImportRequest struct {
	DatabaseID string
	// Dump is the file, read once and to the end, and never more than Limit
	// bytes of it.
	Dump  io.Reader
	Limit int64
	// Format is the one somebody named, or empty to detect it.
	Format string
	UserID string
}

// CreateDatabaseRequest is the Create Database form.
type CreateDatabaseRequest struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Version   string `json:"version,omitempty"`
	StorageGB int    `json:"storage_gb,omitempty"`
	Instances int    `json:"instances,omitempty"`
}

// DatabaseCredentials is what an app needs to connect.
type DatabaseCredentials struct {
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	URL      string `json:"url"`
	// NativeURL is ClickHouse's native-protocol address, for the clients
	// that speak it rather than the HTTP one URL names. Empty for every
	// other engine.
	NativeURL string `json:"native_url,omitempty"`
}

// BackupManager runs and restores backups.
type BackupManager interface {
	// Run takes a backup now.
	Run(ctx context.Context, targetType, targetID, kind string) (store.Backup, error)
	// Restore puts a database backup back. overwrite must be explicit.
	Restore(ctx context.Context, backupID string, overwrite bool) (store.Operation, error)
	// RestoreVolume puts a volume backup back, stopping the app while it does.
	// A separate method rather than a branch inside Restore, because the two
	// share nothing but the word: one runs an engine's own restore tool
	// against a live database, the other stops a process and unpacks a tar.
	RestoreVolume(ctx context.Context, backupID string, overwrite bool) (store.Operation, error)
	// Verify checks that configured storage is reachable and writable.
	Verify(ctx context.Context) error
	// BackupPanel copies the panel's own database to backup storage.
	BackupPanel(ctx context.Context, kind string) (store.Backup, error)
	// VerifyBackup downloads a backup, opens it and reads it through, in the
	// background, and records the outcome on it.
	VerifyBackup(ctx context.Context, backupID string) error
	// Import loads a dump into a database, after a backup of what it holds.
	// The dump is read and staged before this returns; the rest is the
	// operation's.
	Import(ctx context.Context, req ImportRequest) (store.Operation, error)
}

// RunResult is how a one-off command ended: its exit status, once it has.
type RunResult struct {
	Finished bool
	ExitCode int
}
