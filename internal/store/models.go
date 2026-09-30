package store

import "time"

// Role is a team membership role. Roles are ordered: owner > admin > member.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// AtLeast reports whether r grants everything required grants.
func (r Role) AtLeast(required Role) bool { return roleRank[r] >= roleRank[required] }

var roleRank = map[Role]int{RoleMember: 1, RoleAdmin: 2, RoleOwner: 3}

// Valid reports whether r is a role the schema accepts.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

// User is a person who can sign in to the panel.
type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	PasswordHash  string    `json:"-"`
	TOTPSecretEnc string    `json:"-"`
	TOTPEnabled   bool      `json:"totp_enabled"`
	IsAdmin       bool      `json:"is_admin"`
	Disabled      bool      `json:"disabled"`
	Locale        string    `json:"locale"`
	Theme         string    `json:"theme"`
	RecoverySaved bool      `json:"recovery_saved"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastLoginAt   time.Time `json:"last_login_at,omitzero"`
}

// Team owns projects, servers and billing-shaped concerns.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	// Role is filled in when a team is listed for a particular user.
	Role Role `json:"role,omitempty"`
}

// Membership joins a user to a team with a role.
type Membership struct {
	TeamID    string    `json:"team_id"`
	UserID    string    `json:"user_id"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Project groups environments, which group apps and databases.
type Project struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// EnvironmentKind separates long-lived environments from throwaway previews.
type EnvironmentKind string

const (
	EnvStandard EnvironmentKind = "standard"
	EnvPreview  EnvironmentKind = "preview"
)

// Environment maps one-to-one onto a Kubernetes namespace.
type Environment struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Slug      string          `json:"slug"`
	Kind      EnvironmentKind `json:"kind"`
	Namespace string          `json:"namespace"`
	SourceRef string          `json:"source_ref,omitempty"`
	// PodSecurity is the Pod Security Admission level this environment's
	// namespace enforces: "restricted", or "baseline" for an environment that
	// has to run an image starting as root. See kube.PodSecurity.
	PodSecurity string    `json:"pod_security"`
	CreatedAt   time.Time `json:"created_at"`
}

// ServerStatus is the lifecycle of a machine the panel manages.
type ServerStatus string

const (
	ServerPending      ServerStatus = "pending"
	ServerProvisioning ServerStatus = "provisioning"
	ServerReady        ServerStatus = "ready"
	ServerNotReady     ServerStatus = "not_ready"
	ServerFailed       ServerStatus = "failed"
	ServerRemoving     ServerStatus = "removing"
)

// Server is a VPS that is, or is becoming, a cluster node.
type Server struct {
	ID           string       `json:"id"`
	TeamID       string       `json:"team_id"`
	Name         string       `json:"name"`
	Host         string       `json:"host"`
	SSHPort      int          `json:"ssh_port"`
	SSHUser      string       `json:"ssh_user"`
	SSHKeyEnc    string       `json:"-"`
	HostKey      string       `json:"host_key"`
	Role         string       `json:"role"`
	Status       ServerStatus `json:"status"`
	StatusDetail string       `json:"status_detail"`
	NodeName     string       `json:"node_name"`
	ExternalIP   string       `json:"external_ip"`
	InternalIP   string       `json:"internal_ip"`
	OSInfo       string       `json:"os_info"`
	Arch         string       `json:"arch"`
	CPUCores     int          `json:"cpu_cores"`
	MemoryMB     int          `json:"memory_mb"`
	DiskGB       int          `json:"disk_gb"`
	Labels       string       `json:"labels"`
	// Adopted marks a node the panel found in the cluster rather than one it
	// installed. The machine Skifity itself runs on is the usual case. There is
	// no key to it and nothing was put on it, so the operations that would need
	// SSH are refused rather than attempted.
	Adopted    bool      `json:"adopted"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastSeenAt time.Time `json:"last_seen_at,omitzero"`
}

// OperationStatus is the state of a long-running, resumable job.
type OperationStatus string

const (
	OpPending   OperationStatus = "pending"
	OpRunning   OperationStatus = "running"
	OpSucceeded OperationStatus = "succeeded"
	OpFailed    OperationStatus = "failed"
	OpCancelled OperationStatus = "cancelled"
)

// Operation is a multi-step job whose progress the UI streams.
type Operation struct {
	ID         string          `json:"id"`
	TeamID     string          `json:"team_id"`
	Kind       string          `json:"kind"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Status     OperationStatus `json:"status"`
	ErrorCode  string          `json:"error_code,omitempty"`
	ErrorMsg   string          `json:"error_message,omitempty"`
	CreatedBy  string          `json:"created_by"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	FinishedAt time.Time       `json:"finished_at,omitzero"`
	Steps      []OperationStep `json:"steps,omitempty"`
}

// StepStatus is the state of one step of an Operation.
type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
)

// OperationStep is one named, idempotent stage of an Operation.
type OperationStep struct {
	ID          string     `json:"id"`
	OperationID string     `json:"operation_id"`
	Seq         int        `json:"seq"`
	Key         string     `json:"key"`
	Status      StepStatus `json:"status"`
	Message     string     `json:"message"`
	// MessageKey names the sentence in Message so the panel can show it in the
	// reader's language, and MessageArgs carries the values that went into it.
	// Both are empty on a step recorded before either existed, and on one whose
	// sentence the panel never wrote itself.
	MessageKey  string   `json:"message_key,omitempty"`
	MessageArgs []string `json:"message_args,omitempty"`
	// Notes are the extra lines the panel wrote itself, each translatable.
	// Detail is the one thing that is not: the rendered text of the problem a
	// failed step hit.
	Notes      []StepDetail `json:"notes,omitempty"`
	Detail     string       `json:"detail,omitempty"`
	StartedAt  time.Time    `json:"started_at,omitzero"`
	FinishedAt time.Time    `json:"finished_at,omitzero"`
}

// StepDetail is one extra line under a step, translated the same way as its
// message: Text is the English, Key names it in the locale, Args fills it.
type StepDetail struct {
	Text string   `json:"text"`
	Key  string   `json:"key,omitempty"`
	Args []string `json:"args,omitempty"`
}

// StepNote is what a step says when it finishes.
//
// Message is the English the panel wrote: the fallback, and what the API and
// the CLI read. Key names the same sentence in the locale, and Args carries the
// values that went into it. A note with no Key is shown as its English, which
// is what a step recorded before this existed has.
type StepNote struct {
	Message string
	Key     string
	Args    []string
	// Details are the extra lines shown under the step — the preflight
	// warnings, the host key, the fingerprint. They used to be one joined
	// English string in a column the interface only read for failures.
	Details []StepDetail
}

// App is what a user deploys. It becomes a Deployment, Service and Ingress.
type App struct {
	ID             string `json:"id"`
	EnvironmentID  string `json:"environment_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	SourceType     string `json:"source_type"`
	GitSourceID    string `json:"git_source_id,omitempty"`
	RepoURL        string `json:"repo_url"`
	Branch         string `json:"branch"`
	RootDir        string `json:"root_dir"`
	Builder        string `json:"builder"`
	DockerfilePath string `json:"dockerfile_path"`
	// BuildCommand is what produces a front end's output, empty when nothing
	// has to run before the files are served.
	BuildCommand string `json:"build_command"`
	// StaticDir is the directory served for a static site, relative to the
	// root being built: "dist" for Vite, "build" for Create React App, "." for
	// a repository that already holds its HTML.
	StaticDir    string `json:"static_dir"`
	Image        string `json:"image"`
	Port         int    `json:"port"`
	HealthPath   string `json:"health_path"`
	StartCommand string `json:"start_command"`
	// ReleaseCommand runs after the build and before any traffic reaches the
	// new version, which is where a migration belongs.
	ReleaseCommand string    `json:"release_command"`
	Replicas       int       `json:"replicas"`
	Autoscale      bool      `json:"autoscale"`
	MinReplicas    int       `json:"min_replicas"`
	MaxReplicas    int       `json:"max_replicas"`
	CPUTarget      int       `json:"cpu_target"`
	MemoryTarget   int       `json:"memory_target"`
	ScaleToZero    bool      `json:"scale_to_zero"`
	CPURequestM    int       `json:"cpu_request_m"`
	CPULimitM      int       `json:"cpu_limit_m"`
	MemRequestMB   int       `json:"mem_request_mb"`
	MemLimitMB     int       `json:"mem_limit_mb"`
	AutoDeploy     bool      `json:"auto_deploy"`
	PreviewDeploys bool      `json:"preview_deploys"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Variable is an environment variable, secret or not. Value is only populated
// when the caller has explicitly asked to decrypt it, which the API never does
// for secrets after they are first created.
type Variable struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	IsSecret  bool      `json:"is_secret"`
	BuildTime bool      `json:"build_time"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SharedVariable is a project-wide variable inherited by every app in it.
type SharedVariable struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	IsSecret  bool      `json:"is_secret"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Domain is a hostname that routes to an app.
type Domain struct {
	ID           string    `json:"id"`
	AppID        string    `json:"app_id"`
	Hostname     string    `json:"hostname"`
	Path         string    `json:"path"`
	TLS          bool      `json:"tls"`
	Auto         bool      `json:"auto"`
	Status       string    `json:"status"`
	StatusDetail string    `json:"status_detail,omitempty"`
	CreatedAt    time.Time `json:"created_at"`

	// DNSTarget is where this hostname has to point, filled in when a list is
	// built rather than stored: it is a property of the cluster, not of the
	// domain, and a stored copy would be wrong the day a server is replaced.
	DNSTarget string `json:"dns_target,omitempty"`
}

// DeploymentStatus is the state of one deploy attempt.
type DeploymentStatus string

const (
	DeployQueued     DeploymentStatus = "queued"
	DeployBuilding   DeploymentStatus = "building"
	DeployDeploying  DeploymentStatus = "deploying"
	DeploySucceeded  DeploymentStatus = "succeeded"
	DeployFailed     DeploymentStatus = "failed"
	DeployCancelled  DeploymentStatus = "cancelled"
	DeploySuperseded DeploymentStatus = "superseded"
)

// Terminal reports whether a deployment will not change state again.
func (s DeploymentStatus) Terminal() bool {
	switch s {
	case DeploySucceeded, DeployFailed, DeployCancelled, DeploySuperseded:
		return true
	}
	return false
}

// Deployment is one attempt to run a particular version of an app.
type Deployment struct {
	ID     string           `json:"id"`
	AppID  string           `json:"app_id"`
	Number int              `json:"number"`
	Status DeploymentStatus `json:"status"`
	// Trigger is one of manual, create, template, push, preview or rollback.
	// It is a token the interface translates, never a sentence.
	Trigger string `json:"trigger"`
	// RollbackOf is the deployment number a rollback went back to, and zero
	// for every other trigger.
	RollbackOf       int       `json:"rollback_of,omitempty"`
	CommitSHA        string    `json:"commit_sha"`
	CommitMessage    string    `json:"commit_message"`
	CommitAuthor     string    `json:"commit_author"`
	Image            string    `json:"image"`
	BuildFingerprint string    `json:"build_fingerprint"`
	RuntimeSpec      string    `json:"runtime_spec"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	ErrorHint        string    `json:"error_hint,omitempty"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	StartedAt        time.Time `json:"started_at,omitzero"`
	FinishedAt       time.Time `json:"finished_at,omitzero"`

	// CanRollback is worked out when a list is built, not stored. The panel
	// keeps far more deployment records than the registry keeps images, so an
	// old record is still worth showing and is no longer something to go back
	// to: offering it would offer a deployment that fails on a pull.
	CanRollback bool `json:"can_rollback"`
}

// LogLine is one line of build output.
type LogLine struct {
	Seq    int       `json:"seq"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
	At     time.Time `json:"at"`
}

// Volume is persistent storage attached to an app.
type Volume struct {
	ID           string    `json:"id"`
	AppID        string    `json:"app_id"`
	Name         string    `json:"name"`
	MountPath    string    `json:"mount_path"`
	SizeGB       int       `json:"size_gb"`
	StorageClass string    `json:"storage_class"`
	CreatedAt    time.Time `json:"created_at"`
}

// Database is a managed data store.
type Database struct {
	ID             string    `json:"id"`
	EnvironmentID  string    `json:"environment_id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Engine         string    `json:"engine"`
	EngineVersion  string    `json:"engine_version"`
	Status         string    `json:"status"`
	StatusDetail   string    `json:"status_detail,omitempty"`
	Instances      int       `json:"instances"`
	StorageGB      int       `json:"storage_gb"`
	CPURequestM    int       `json:"cpu_request_m"`
	MemRequestMB   int       `json:"mem_request_mb"`
	CredentialsEnc string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// DatabaseLink injects a database's connection string into an app.
type DatabaseLink struct {
	DatabaseID string    `json:"database_id"`
	AppID      string    `json:"app_id"`
	VarName    string    `json:"var_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// BackupPolicy schedules backups for a database or a volume.
type BackupPolicy struct {
	ID          string    `json:"id"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	Schedule    string    `json:"schedule"`
	Retention   int       `json:"retention"`
	Destination string    `json:"destination"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Backup is one stored copy of a database or volume.
type Backup struct {
	ID           string    `json:"id"`
	TargetType   string    `json:"target_type"`
	TargetID     string    `json:"target_id"`
	Status       string    `json:"status"`
	Kind         string    `json:"kind"`
	Location     string    `json:"location"`
	SizeBytes    int64     `json:"size_bytes"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	FinishedAt   time.Time `json:"finished_at,omitzero"`
}

// GitSource is a connection to a Git host.
type GitSource struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"team_id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	Account   string    `json:"account"`
	ConfigEnc string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Session is a browser login.
type Session struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	TokenHash string `json:"-"`
	// CSRFHash is the hash of this session's CSRF token. The check is against
	// the session rather than against a second cookie, because a cookie can be
	// written by a page on a sibling subdomain and this panel hosts those.
	CSRFHash   string    `json:"-"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	// ReauthAt is when the person last proved who they are with a password or
	// a second factor, rather than by holding this cookie. Zero means never.
	ReauthAt time.Time `json:"-"`
}

// APIToken authenticates the CLI and the MCP server.
type APIToken struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	TeamID     string    `json:"team_id"`
	Name       string    `json:"name"`
	TokenHash  string    `json:"-"`
	Prefix     string    `json:"prefix"`
	Scopes     string    `json:"scopes"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"`
}

// AuditEvent records who did what, when and from where.
type AuditEvent struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	ActorID     string    `json:"actor_id"`
	ActorLabel  string    `json:"actor_label"`
	Action      string    `json:"action"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	TargetLabel string    `json:"target_label"`
	IP          string    `json:"ip"`
	UserAgent   string    `json:"user_agent"`
	Metadata    string    `json:"metadata"`
	At          time.Time `json:"at"`
}

// NotificationChannel delivers events to email, Telegram, Discord or a webhook.
type NotificationChannel struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"team_id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	ConfigEnc string    `json:"-"`
	Events    string    `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ClusterComponent tracks an optional add-on that is installed on first use.
type ClusterComponent struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installed_at,omitzero"`
	Detail      string    `json:"detail,omitempty"`
}
