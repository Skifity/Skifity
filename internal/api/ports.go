package api

import (
	"context"
	"io"
	"time"

	"skifity/internal/plugins"
	"skifity/internal/store"
)

// The interfaces in this file are what the HTTP layer needs from the rest of the
// panel. They are declared here, on the consumer side, so that handlers can be
// tested without a Kubernetes cluster, an SSH server or a container registry.

// NodeInfo is one cluster node as the panel presents it.
type NodeInfo struct {
	Name          string            `json:"name"`
	Ready         bool              `json:"ready"`
	Reason        string            `json:"reason,omitempty"`
	Roles         []string          `json:"roles"`
	InternalIP    string            `json:"internal_ip"`
	ExternalIP    string            `json:"external_ip"`
	OS            string            `json:"os"`
	Architecture  string            `json:"architecture"`
	KubeletVer    string            `json:"kubelet_version"`
	CPUCapacityM  int64             `json:"cpu_capacity_m"`
	MemCapacityMB int64             `json:"memory_capacity_mb"`
	CPUUsedM      int64             `json:"cpu_used_m"`
	MemUsedMB     int64             `json:"memory_used_mb"`
	PodCount      int               `json:"pod_count"`
	Labels        map[string]string `json:"labels,omitempty"`
	Schedulable   bool              `json:"schedulable"`
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
	// QuotaUsage reports how much of an environment's limits are in use.
	QuotaUsage(ctx context.Context, namespace string) (EnvironmentQuota, error)
	// ControlPlaneCount is how many nodes actually run the cluster, which is
	// not the same as how many rows the panel has for one team.
	ControlPlaneCount(ctx context.Context) (int, error)
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
	// RemoveServer cordons, drains and removes a node, then cleans the machine.
	RemoveServer(ctx context.Context, serverID string, wipe bool) (store.Operation, error)
	// PromoteServer makes a worker a control plane node.
	PromoteServer(ctx context.Context, serverID string) (store.Operation, error)
	// Cancel stops a running operation.
	Cancel(ctx context.Context, operationID string) error
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

// DeployRequest starts a deployment.
type DeployRequest struct {
	AppID     string
	Trigger   string
	CommitSHA string
	CreatedBy string
	// Force skips the build-fingerprint shortcut and rebuilds regardless.
	Force bool
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
}
