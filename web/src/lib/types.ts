/** The shapes the API returns. These mirror the Go types in internal/store. */

export type Role = "owner" | "admin" | "member"

/** A pending offer to join a team. The link that carries its token is shown once. */
export type Invitation = {
  id: string
  team_id: string
  email: string
  role: Role
  expires_at: string
  created_at: string
}

export type User = {
  id: string
  email: string
  name: string
  totp_enabled: boolean
  is_admin: boolean
  disabled: boolean
  locale: string
  theme: string
  recovery_saved: boolean
  created_at: string
  last_login_at?: string
}

export type Team = { id: string; name: string; slug: string; created_at: string; role?: Role }

export type Project = {
  id: string
  team_id: string
  name: string
  slug: string
  description: string
  created_at: string
}

export type Environment = {
  id: string
  project_id: string
  name: string
  slug: string
  kind: "standard" | "preview"
  namespace: string
  source_ref?: string
  /** How strictly this environment's pods are confined. */
  pod_security: PodSecurity
  created_at: string
}

/** The Pod Security Admission level an environment's namespace enforces. */
export type PodSecurity = "restricted" | "baseline"

export type ServerStatus =
  "pending" | "provisioning" | "ready" | "not_ready" | "failed" | "removing"

export type Server = {
  id: string
  team_id: string
  name: string
  host: string
  ssh_port: number
  ssh_user: string
  host_key: string
  role: "control-plane" | "worker"
  status: ServerStatus
  status_detail: string
  node_name: string
  external_ip: string
  internal_ip: string
  os_info: string
  arch: string
  cpu_cores: number
  memory_mb: number
  disk_gb: number
  /** A node the panel found in the cluster rather than one it installed. */
  adopted: boolean
  created_at: string
  last_seen_at?: string
}

export type StepStatus = "pending" | "running" | "succeeded" | "failed" | "skipped"

/** One extra line under a step: the English, its key, and its values. */
export type StepDetail = { text: string; key?: string; args?: string[] }

export type OperationStep = {
  id: string
  seq: number
  key: string
  status: StepStatus
  message: string
  /** Names the sentence in `message` so the panel can show it in its own
   *  language, with `message_args` filling the values. Empty for a step from
   *  before either existed. */
  message_key?: string
  message_args?: string[]
  /** Extra lines the panel wrote itself, each translatable. */
  notes?: StepDetail[]
  /** The one line that is not: a failed step's rendered problem. */
  detail?: string
  started_at?: string
  finished_at?: string
}

export type Operation = {
  id: string
  team_id: string
  kind: string
  target_type: string
  target_id: string
  status: "pending" | "running" | "succeeded" | "failed" | "cancelled"
  error_code?: string
  error_message?: string
  created_at: string
  finished_at?: string
  steps?: OperationStep[]
}

export type App = {
  id: string
  environment_id: string
  name: string
  slug: string
  source_type: "git" | "image" | "upload"
  git_source_id?: string
  repo_url: string
  branch: string
  root_dir: string
  builder: string
  dockerfile_path: string
  image: string
  port: number
  health_path: string
  build_command: string
  static_dir: string
  start_command: string
  release_command: string
  replicas: number
  autoscale: boolean
  min_replicas: number
  max_replicas: number
  cpu_target: number
  memory_target: number
  scale_to_zero: boolean
  cpu_request_m: number
  cpu_limit_m: number
  mem_request_mb: number
  mem_limit_mb: number
  auto_deploy: boolean
  preview_deploys: boolean
  status: string
  created_at: string
  updated_at: string
}

export type Instance = {
  name: string
  status: string
  ready: boolean
  restarts: number
  node: string
  started_at?: string
  message?: string
  cpu_m: number
  memory_mb: number
}

export type AppStatus = {
  phase: string
  detail?: string
  desired_replicas: number
  ready_replicas: number
  instances: Instance[]
  image?: string
  urls?: string[]
}

export type DeploymentStatus =
  "queued" | "building" | "deploying" | "succeeded" | "failed" | "cancelled" | "superseded"

export type Deployment = {
  id: string
  app_id: string
  number: number
  status: DeploymentStatus
  trigger: string
  /** The deployment number a rollback went back to; absent otherwise. */
  rollback_of?: number
  commit_sha: string
  commit_message: string
  commit_author: string
  image: string
  build_fingerprint: string
  error_code?: string
  error_message?: string
  error_hint?: string
  created_by: string
  created_at: string
  started_at?: string
  finished_at?: string
  /**
   * Whether this version's image still exists. The panel keeps far more
   * deployment records than the registry keeps images, so an old record is
   * worth reading and is no longer somewhere to go back to.
   */
  can_rollback?: boolean
}

export type LogLine = { seq: number; stream: string; line: string; at: string }

export type Variable = {
  id: string
  app_id: string
  key: string
  value?: string
  is_secret: boolean
  build_time: boolean
  updated_at: string
}

export type Domain = {
  id: string
  app_id: string
  hostname: string
  path: string
  tls: boolean
  auto: boolean
  status: string
  status_detail?: string
  /** Where this hostname has to point. Worked out per request, not stored. */
  dns_target?: string
  created_at: string
}

export type Volume = {
  id: string
  app_id: string
  name: string
  mount_path: string
  size_gb: number
  created_at: string
}

export type Database = {
  id: string
  environment_id: string
  name: string
  slug: string
  engine: "postgres" | "redis" | "mysql"
  engine_version: string
  status: string
  status_detail?: string
  instances: number
  storage_gb: number
  created_at: string
}

export type DatabaseCredentials = {
  engine: string
  host: string
  port: number
  database: string
  username: string
  password: string
  url: string
}

export type Backup = {
  id: string
  target_type: string
  target_id: string
  status: "running" | "succeeded" | "failed"
  kind: string
  location: string
  size_bytes: number
  error_message?: string
  created_at: string
  finished_at?: string
}

export type BackupPolicy = {
  id?: string
  target_type: string
  target_id: string
  schedule: string
  retention: number
  destination: string
  enabled: boolean
}

export type NodeInfo = {
  name: string
  ready: boolean
  reason?: string
  roles: string[]
  internal_ip: string
  external_ip: string
  os: string
  architecture: string
  kubelet_version: string
  cpu_capacity_m: number
  memory_capacity_mb: number
  cpu_used_m: number
  memory_used_mb: number
  pod_count: number
  schedulable: boolean
}

export type ClusterSummary = {
  reachable: boolean
  kubernetes_version?: string
  nodes: NodeInfo[]
  ready_nodes: number
  total_cpu_m: number
  total_memory_mb: number
  used_cpu_m: number
  used_memory_mb: number
  high_availability: boolean
  message?: string
}

export type ScalingFinding = {
  code: string
  severity: "error" | "warning" | "info"
  title: string
  detail: string
  fix: string
  /** The values interpolated into the three sentences above, in order. */
  args?: { title?: string[]; detail?: string[]; fix?: string[] }
}

export type Scaling = {
  replicas: number
  autoscale: boolean
  min_replicas: number
  max_replicas: number
  cpu_target: number
  memory_target: number
  scale_to_zero: boolean
}

export type AuditEvent = {
  id: string
  team_id: string
  actor_id: string
  actor_label: string
  action: string
  target_type: string
  target_id: string
  target_label: string
  ip: string
  metadata: string
  at: string
}

export type SettingKind = "text" | "bool" | "number" | "choice" | "url" | "email" | "domain"

export type Setting = {
  key: string
  value?: string
  secret: boolean
  configured: boolean
  label: string
  group: string
  help: string
  /** Which control to draw. Without it a yes/no setting becomes a text box. */
  kind: SettingKind
  options?: string[]
  multiline?: boolean
  placeholder?: string
}

export type Component = {
  name: string
  status: string
  version: string
  installed_at?: string
  detail?: string
  title: string
  description: string
  optional: boolean
  beta: boolean
  approximate_memory_mb: number
  /** Installed with Helm rather than by the panel. */
  external: boolean
  docs?: string
}

export type Template = {
  id: string
  name: string
  description: string
  category: string
  website: string
  beta?: boolean
  /** The logo's file name, when the panel has one for this template. */
  icon?: string
  services: { name: string; image: string; port: number; public: boolean }[]
  databases: { name: string; engine: string }[]
  inputs?: {
    key: string
    label: string
    help?: string
    default?: string
    secret?: boolean
    required?: boolean
    generate?: boolean
  }[]
  notes?: string
}

export type APIToken = {
  id: string
  name: string
  prefix: string
  scopes: string
  created_at: string
  last_used_at?: string
  expires_at?: string
}

export type Session = {
  id: string
  ip: string
  user_agent: string
  created_at: string
  last_seen_at: string
  expires_at: string
  current: boolean
}

export type Meta = {
  product: string
  version: string
  commit: string
  tagline: string
  locales: string[]
  dev_mode: boolean
  sso: { enabled: boolean; label?: string }
  server_now: string
  /** What the binary at /api/cli/download runs on, such as linux/amd64. */
  cli_platform: string
  /** Every platform the download serves, this panel's own first. */
  cli_platforms: string[]
}

export type CanvasNode = {
  id: string
  kind: "app" | "database"
  name: string
  status: string
  detail?: string
  urls?: string[]
  environment: string
}

export type CanvasEdge = { from: string; to: string; label: string }

export type DatabaseLink = {
  database_id: string
  app_id: string
  var_name: string
  created_at: string
}

export type GitSource = {
  id: string
  team_id: string
  kind: "github_app" | "github_pat" | "gitlab" | "gitea" | "generic"
  name: string
  base_url: string
  account: string
  created_at: string
}

export type NotificationChannel = {
  id: string
  team_id: string
  /**
   * One of the built-in kinds, or `plugin:<plugin id>/<provider id>` for a
   * channel an installed plugin sends. It is not a closed union any more: the
   * panel no longer knows every way a notification can leave the building.
   */
  kind: string
  name: string
  events: string
  enabled: boolean
  created_at: string
}

/** One input in a channel form a plugin declared. */
export type NotificationField = {
  key: string
  label: string
  help?: string
  kind?: "text" | "password" | "number" | "bool" | "choice"
  options?: string[]
  secret?: boolean
  required?: boolean
}

/**
 * A way of sending, as the panel offers it.
 *
 * A built-in kind arrives with no fields, because the panel has its form
 * already and has translated it. A kind a plugin provides carries its own
 * form, in the plugin author's English.
 */
export type NotificationKind = {
  kind: string
  name?: string
  description?: string
  provider?: string
  fields?: NotificationField[]
}

/** A command that runs on a schedule, in the app's own image. */
export type AppJob = {
  id: string
  app_id: string
  name: string
  schedule: string
  command: string
  enabled: boolean
  created_at: string
  updated_at: string
}

/** How much of an environment's ceiling is in use. */
export type EnvironmentQuota = {
  found: boolean
  items: EnvironmentQuotaItem[]
}

export type EnvironmentQuotaItem = {
  /** The Kubernetes name, such as requests.memory. */
  resource: string
  /** The quantities as Kubernetes writes them, unit included. */
  used: string
  hard: string
  used_value: number
  hard_value: number
  percent: number
}

/** What the panel worked out about a repository before building it. */
export type Detection = {
  builder: string
  language: string
  framework: string
  port: number
  start_command?: string
  health_path?: string
  dockerfile_path?: string
  static_dir?: string
  build_command?: string
  /** "high" when a marker file is unambiguous, lower when it is a guess. */
  confidence: string
  /** Why it decided that, which is what makes the guess reviewable. */
  notes?: string[]
  /** The repository was larger than the panel read. */
  truncated?: boolean
  /** Services read from a Compose file. An app runs one of them. */
  compose?: ComposeService[]
  /** Parts of the Compose file that do not carry over. */
  compose_warnings?: string[]
  /**
   * What the app will reach for once it runs, read from its own files: a
   * database, data kept in a file the next deploy erases, and the settings its
   * .env.example lists. Each one carries the package and the file it was read
   * from, so the form can say why.
   */
  needs?: AppNeed[]
}

/** One thing an app needs, and the evidence for it. */
export type AppNeed = {
  kind: "database" | "ephemeral" | "variables"
  /** postgres, mysql, redis, mongodb, sqlserver — or sqlite and file. */
  engine?: string
  /** Whether this panel can create it. */
  provided: boolean
  /** The package, provider line or file that says so. */
  evidence?: string
  /** The file the evidence was read from. */
  source?: string
  /** The name the app reads the connection from. */
  variable?: string
  /** For kind "variables": the names the app expects. */
  variables?: string[]
}

/** What happened to a database asked for with a new app. */
export type InitialDatabaseResult = {
  engine: string
  variable: string
  database_id?: string
  name?: string
  error?: string
}

/** One service from a Compose file, as the panel would run it. */
export type ComposeService = {
  name: string
  image?: string
  build?: string
  ports?: number[]
  environment?: Record<string, string>
  volumes?: string[]
  depends_on?: string[]
  /** Compose features with no equivalent here, named rather than dropped. */
  unsupported?: string[]
}

/** A plugin's manifest, mirroring internal/plugins. */
export type PluginManifest = {
  apiVersion: string
  id: string
  name: string
  description: string
  version: string
  homepage?: string
  license: string
  author: { name: string; url?: string; email?: string }
  image: string
  permissions?: string[]
  events?: { event: string; blocking?: boolean; timeoutSeconds?: number }[]
  settings?: {
    key: string
    label: string
    help?: string
    kind?: string
    options?: string[]
    secret?: boolean
    required?: boolean
  }[]
  runtime?: { port?: number; health?: string; memoryMB?: number }
}

export type InstalledPlugin = {
  id: string
  version: string
  source_url?: string
  status: "installing" | "running" | "failed" | "disabled"
  status_detail?: string
  enabled: boolean
  installed_at: string
  decoded: PluginManifest
  settings: { key: string; value?: string; configured: boolean; secret: boolean }[]
}

/** What installing a manifest would mean, before anything is installed. */
export type PluginInspection = {
  manifest: PluginManifest
  permissions: string[]
  blocks_deploys: boolean
  already_installed?: string
}

export type StoreEntry = {
  id: string
  name: string
  description: string
  version: string
  license: string
  author: string
  homepage?: string
  category?: string
  manifest_url: string
  manifest_sha256: string
  paid?: boolean
  purchase_url?: string
}

export type StoreCatalogue = {
  index: { version: number; generated_at?: string; plugins: StoreEntry[] }
  /** True when a key is configured and the index's signature checked out. */
  verified: boolean
  /** True when no key is configured at all, which is not the same thing. */
  unsigned: boolean
  url: string
  installed: Record<string, string>
}

/** What the panel can say about deploy on push, after creating an app. */
export type WebhookStatus = {
  registered: boolean
  url?: string
  reason?: string
}
