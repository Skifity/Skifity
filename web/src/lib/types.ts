/** The shapes the API returns. These mirror the Go types in internal/store. */

export type Role = "owner" | "admin" | "member" | "viewer"

/** A pending offer to join a team. The link that carries its token is shown once. */
export type Invitation = {
  id: string
  team_id: string
  email: string
  role: Role
  /** What the membership will be limited to; absent for the whole team. */
  projects?: string[]
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

export type Team = {
  id: string
  name: string
  slug: string
  created_at: string
  role?: Role
  /** Limited to some of the team's projects, and which. */
  scoped?: boolean
  projects?: string[]
  /** Everybody in it has to sign in with a second factor or single sign-on. */
  require_strong_auth?: boolean
}

/** One person in a team, as the members list shows them. */
export type Member = { user: User; role: Role; scoped: boolean; projects?: string[] }

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
  /** A pull request's preview copies every app here, not only its own. */
  preview_stack: boolean
  created_at: string
}

/** What cloning an environment made, and what it left behind. */
export type ClonedEnvironment = {
  environment: Environment
  apps: App[]
  databases: Database[]
  notes: {
    /** The app's or the database's. */
    name: string
    code:
      | "volumes_empty"
      | "domains_kept"
      | "ports_kept"
      | "push_deploy_off"
      | "database_skipped"
      | "deploy_failed"
    detail?: string
  }[]
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
  /** How an instance is checked: ask health_path, connect to the port, or nothing. */
  health_check: HealthCheck
  /** How long a new instance may take to answer, in seconds. */
  health_start_seconds: number
  /** How long one check waits for an answer, in seconds. */
  health_timeout_seconds: number
  /** The uid an image that names its user runs as; 0 when the image decides. */
  run_as_user: number
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
  /** The paths a push has to change to deploy the app, one per line. */
  watch_paths: string
  /** Every push to the branch deploys, or only a pushed tag matching tag_pattern. */
  deploy_trigger: "branch" | "tag"
  tag_pattern: string
  /** Run once in each new preview of the app, after its first deploy. */
  preview_seed: string
  /** When a preview's seed ran; absent until it has. */
  seeded_at?: string
  status: string
  created_at: string
  updated_at: string
  /** Present while deploys are locked: who, why and since when. */
  deploy_lock?: { reason: string; locked_by: string; locked_at: string }
  /** Reached by name from its environment only: no public address. */
  internal: boolean
  /** Present while visitors see the maintenance page instead of the app. */
  maintenance?: Maintenance
  /**
   * What the newest scan of its image counted, per severity. Only on an
   * environment's list of apps, and only for an app that has been scanned.
   */
  vulnerabilities?: ScanCounts
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
  /** How the environment's other apps reach an internal one: name and port. */
  internal_address?: string
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
  /** Deployed past the check that stops critical vulnerabilities with a fix. */
  accepted_vulnerabilities?: boolean
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

/** The severities a scan reports, most severe first. */
export type Severity = "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN"

export type ScanCounts = {
  critical: number
  high: number
  medium: number
  low: number
  unknown: number
}

/** One known vulnerability in one package of an image. */
export type ScanFinding = {
  id: string
  package: string
  installed: string
  /** The version, or versions, that fix it; absent when there is no fix yet. */
  fixed_in?: string
  severity: Severity
  title?: string
  /** The advisory's page, always https. */
  url?: string
  target?: string
}

export type ImageScan = {
  id: string
  app_id: string
  deployment_id?: string
  image: string
  digest?: string
  trigger: "deploy" | "schedule" | "manual"
  status: "queued" | "running" | "succeeded" | "failed"
  counts: ScanCounts
  fixable: number
  fixable_critical: number
  findings: ScanFinding[] | null
  /** How many findings there were beyond those listed. */
  omitted: number
  scanner_version?: string
  os?: string
  error_code?: string
  error_message?: string
  error_hint?: string
  created_at: string
  started_at?: string
  finished_at?: string
}

/** An app's Security tab, as GET /api/apps/{id}/vulnerabilities answers it. */
export type VulnerabilityReport = {
  enabled: boolean
  /** Deploys are stopped by a critical vulnerability that has a fix. */
  blocking: boolean
  /** What the app runs now. */
  image?: string
  /** The newest report, or null when the app has never been scanned. */
  scan: ImageScan | null
  /** The report is of the image the app runs now. */
  current: boolean
  /** The report is of an image that has not gone out: its deploy is under way, or was stopped. */
  undeployed: boolean
  /** The newest scan when it is not the report: queued, running or failed. */
  latest?: ImageScan
}

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
  /** Another of the app's hostnames this one permanently redirects to. */
  redirect_to?: string
  /**
   * The team's own certificate this hostname is served with, when one covers
   * it. Absent means Let's Encrypt. Worked out per request, like dns_target.
   */
  certificate?: DomainCertificate
  created_at: string
}

/** Whether a certificate is still good: expiring is within 21 days. */
export type CertificateState = "valid" | "expiring" | "expired"

/** Which of the team's own certificates a domain is served with. */
export type DomainCertificate = {
  id: string
  name: string
  not_after: string
  state: CertificateState
}

/**
 * A certificate the team brought for its own hostnames. Its private key is
 * never part of anything the panel sends.
 */
export type TeamCertificate = {
  id: string
  team_id: string
  name: string
  /** The names it is for, wildcards as written, such as *.example.com. */
  hostnames: string[]
  subject: string
  issuer: string
  not_before: string
  not_after: string
  /** SHA-256, as `openssl x509 -noout -fingerprint -sha256` prints it. */
  fingerprint: string
  key_type: string
  self_signed: boolean
  chain_length: number
  created_at: string
  updated_at: string
  state: CertificateState
  /** The team's domains served with it. */
  domains: { id: string; app_id: string; app_name: string; hostname: string }[]
}

/** What uploading a certificate answers. */
export type SavedCertificate = {
  certificate: TeamCertificate
  replaced: boolean
  reordered: boolean
  /** How many apps are being re-applied to use it, in the background. */
  updating: number
}

export type HealthCheck = "http" | "tcp" | "none"

/** What a domain's DNS said when it was last asked, against where it has to point. */
export type DNSCheck = {
  hostname: string
  status: "here" | "partly" | "elsewhere" | "missing" | "unknown"
  points_here: boolean
  found: { type: "A" | "AAAA" | "CNAME"; value: string; here: boolean }[]
  /** Every address that counts as here, the one to use first. */
  expected: string[]
  checked_at: string
}

/** Adding a domain answers with the domain and, when it could be looked up, its DNS. */
export type AddedDomain = Domain & { dns?: DNSCheck }

export type Volume = {
  id: string
  app_id: string
  name: string
  mount_path: string
  size_gb: number
  created_at: string
}

/** The engines a database can be, as internal/dbsvc/engine names them. */
export type DatabaseEngineName =
  | "postgres"
  | "mysql"
  | "mariadb"
  | "mongodb"
  | "redis"
  | "valkey"
  | "dragonfly"
  | "clickhouse"
  | "memcached"

/** One engine, from GET /api/database-engines. */
export type DatabaseEngine = {
  name: DatabaseEngineName
  /** The product's own name, which is not translated. */
  title: string
  port: number
  default_version: string
  /** Newest first. */
  versions: string[]
  /** False for an engine the panel does not back up. */
  backups: boolean
  /** Only PostgreSQL runs more than one instance. */
  replicated: boolean
  /** False for a cache, which keeps nothing on a disk. */
  storage: boolean
  /** False for an engine with no authentication. */
  password: boolean
  /** What a linked app reads the connection string from by default. */
  variable: string
  storage_gb: number
}

export type Database = {
  id: string
  environment_id: string
  name: string
  slug: string
  engine: DatabaseEngineName
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
  /** Empty for memcached, which has no databases, users or passwords. */
  database: string
  username: string
  password: string
  url: string
  /** ClickHouse only: its native protocol, beside the HTTP one url names. */
  native_url?: string
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
  /** Sealed with the backup passphrase. */
  encrypted: boolean
  /** When it was last downloaded, opened and read through. */
  verified_at?: string
  /** Why that did not work, when it did not. */
  verify_error?: string
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
  /** From the watcher's last reading, at most a minute old; absent until then. */
  disk_used_mb?: number
  disk_capacity_mb?: number
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
  /** What this panel would install now. */
  wanted_version?: string
  upgrade_available: boolean
}

/** A reason a k3s upgrade cannot start, or something worth knowing about one. */
export type UpgradeReason = {
  code: string
  params?: Record<string, string>
  text: string
}

export type K3sUpgradePlan = {
  target: string
  steps: {
    node: string
    role: "control-plane" | "worker"
    from: string
    to: string
    upgrade: boolean
  }[]
  blockers: UpgradeReason[]
  warnings: UpgradeReason[]
  nothing: boolean
}

export type K3sUpgrade = {
  nodes: { name: string; control_plane: boolean; ready: boolean; version: string }[]
  releases: { channel: string; version: string; stable?: boolean }[]
  releases_error?: string
  plan?: K3sUpgradePlan
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
  /** The team catalogue it came from; absent for a built-in template. */
  catalogue?: { id: string; name: string }
}

/** A template in a team's catalogue that cannot be installed, and why. */
export type TemplateCatalogueProblem = {
  /** The file in an archive, or the place in an index's list. */
  file: string
  id?: string
  name?: string
  errors: string[]
}

/** One of a team's own template catalogues. Never the header's value. */
export type TemplateCatalogue = {
  id: string
  team_id: string
  name: string
  url: string
  auth_header_name?: string
  format?: "index" | "tar.gz" | "zip"
  fetched_at: string
  attempted_at: string
  last_error?: string
  /** How many of its templates can be installed. */
  templates: number
  problems: TemplateCatalogueProblem[]
  created_at: string
  updated_at: string
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

/** One of the signed-in person's passkeys, as the account page lists it. */
export type Passkey = {
  id: string
  name: string
  /** The hostname the passkey was made for. */
  rp_id: string
  /** A synced passkey: the platform or password manager copies it to other devices. */
  backup_eligible: boolean
  backup_state: boolean
  created_at: string
  last_used_at?: string
  /** Made for an address the panel no longer answers on. */
  elsewhere: boolean
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
  /** Whether "Lost your password?" can send a reset link by email. */
  password_reset: boolean
  /** Whether a browser would offer passkeys at this panel's address. */
  passkeys: { available: boolean }
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
  kind: "github_app" | "github_pat" | "gitlab" | "gitea" | "bitbucket" | "generic"
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
  /**
   * Limited to `projects`. Events about an app, a database or a backup reach
   * it only from those; events about a server reach every channel.
   */
  scoped: boolean
  projects?: string[]
  created_at: string
}

/**
 * A channel as the form that changes it reads it: the settings that are not
 * secret, and the names of the secrets that are stored — never their values.
 */
export type NotificationChannelDetail = NotificationChannel & {
  config: Record<string, string>
  secrets: string[]
}

/** One input in a channel's form. */
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
 * A built-in kind's fields arrive without words, because the panel has them
 * translated; the server says which are secret, because it is the server that
 * never sends those back. A kind a plugin provides carries its own form, in
 * the plugin author's English.
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
  /** Critical vulnerabilities in the framework versions the source installs. */
  advisories?: Advisory[]
  /** The notes again, as codes the panel translates, in the same order. */
  note_codes?: { code: string; params?: Record<string, string> }[]
  /** app.json's postdeploy script: run once in each new preview. */
  preview_seed?: string
  /** The Procfile's release line. */
  release_command?: string
  /** The Procfile's other lines, which run beside the app as its processes. */
  processes?: { name: string; command: string }[]
  /** app.json's settings as a .env, defaults filled in and secrets generated. */
  env_template?: string
}

/** A known critical vulnerability in a version the source installs. */
export type Advisory = {
  package: string
  version: string
  /** The CVE. */
  id: string
  /** The first version on the same line that is not affected. */
  fixed_in: string
  url: string
}

/** One thing an app needs, and the evidence for it. */
export type AppNeed = {
  kind: "database" | "ephemeral" | "variables"
  /** A database engine the panel runs, or sqlserver; or sqlite and file. */
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
  /** The build's own Dockerfile, relative to its context. */
  dockerfile?: string
  /** Replaces the image's command, as a line a shell runs. */
  command?: string
  /** Published to the host, which is what makes a service public. */
  ports?: number[]
  /** Reached only by the other services. */
  expose?: number[]
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

/** One repository a Git connection can read, as the create form offers it. */
export type GitRepository = {
  full_name: string
  url: string
  default_branch: string
  private: boolean
}

/** A capped list from a Git host: truncated says there was more. */
export type GitListing<T> = {
  items: T[]
  truncated: boolean
}

/** What the panel can say about deploy on push, after creating an app. */
export type WebhookStatus = {
  registered: boolean
  url?: string
  reason?: string
}

/** An app answering visitors with a page instead of itself. */
export type Maintenance = {
  message: string
  /** Addresses and ranges that still reach the app. */
  allow: string[]
  started_by: string
  started_at: string
}

/** What the maintenance card reads: the state, where it shows, and who is asking. */
export type MaintenanceView = Maintenance & {
  active: boolean
  hostnames: string[]
  /** The caller's public address, the one to let through. */
  your_address?: string
}

/** Something about a Compose service that did not carry over as written. */
export type StackNote = {
  service: string
  code: "renamed" | "bind_mount" | "interpolation"
  value?: string
}

/** One of an app's other processes: its image, its own command, no port. */
/** A port an app takes connections on that is not HTTP, open on every server. */
/** One check of a server's hardening. */
export type HardeningFinding = {
  code: "ssh_passwords" | "ssh_root" | "fail2ban" | "auto_updates" | "firewall" | "reboot"
  /** Which answer the check gave, and what its sentence is looked up by. */
  state: string
  level: "risk" | "warn" | "ok"
  detail: string
  fix?: string
  firewall?: string
}

/** A server's hardening, worst first. */
export type HardeningReport = {
  server_id: string
  checked_at: string
  findings: HardeningFinding[]
  can_turn_off_passwords: boolean
}

export type AppPort = {
  id: string
  app_id: string
  port: number
  protocol: "tcp" | "udp"
  public_port: number
  created_at: string
  /** The team's servers' addresses at this port; empty when none are known. */
  addresses: string[]
}

/** Credentials a team pulls private images with. The password never comes back. */
export type RegistryCredential = {
  id: string
  team_id: string
  name: string
  host: string
  username: string
  created_at: string
  updated_at: string
}

/** The kinds of secret manager a variable can be read from. */
export type SecretManagerKind = "vault" | "infisical" | "doppler" | "aws"

/**
 * A connection to a secret manager the team already runs. What it signs in
 * with is never sent back: `credentials` names them and nothing more.
 */
export type SecretManager = {
  id: string
  team_id: string
  name: string
  kind: SecretManagerKind
  settings: Record<string, string>
  credentials: string[]
  used_by: number
  refresh_minutes: number
  next_refresh_at?: string
  last_refresh_at?: string
  refresh_failures: number
  last_error?: string
  created_at: string
  updated_at: string
}

/** Where a variable's value is read from, for one not stored in the panel. */
export type SecretReference = {
  connection_id: string
  path: string
  key?: string
  connection?: string
  kind?: SecretManagerKind
}

/** What refreshing an app's variables from their secret managers did. */
export type VariablesRefresh = {
  references: number
  changed: string[]
  build_time_changed: string[]
  rolled_out: boolean
  deployment?: Deployment
  not_deployed?: boolean
}

/** A file an app's containers read, mounted read-only at its path. */
export type AppFile = {
  id: string
  app_id: string
  path: string
  /** Absent for a secret file, whose content is never sent back. */
  content?: string
  size: number
  is_secret: boolean
  executable: boolean
  created_at: string
  updated_at: string
}

export type AppProcess = {
  app_id: string
  name: string
  command: string
  instances: number
  created_at: string
  updated_at: string
  /** How many are running, when the cluster could say. */
  ready?: number
  phase?: string
}

/** How an app's objects in the cluster compare with what the panel applies. */
export type DriftStatus = "in_sync" | "drifted" | "missing" | "not_deployed" | "applying"

/**
 * One difference: an object deleted, or one field of one changed. Kinds and
 * paths are Kubernetes' own, which is why this is only shown under Advanced.
 */
export type DriftItem = {
  kind: string
  name: string
  path?: string
  change: "changed" | "removed" | "deleted"
  panel?: string
  live?: string
  /** A Secret's value differs; neither value is ever sent. */
  hidden?: boolean
  changed_by?: string
  changed_at?: string
}

export type DriftReport = {
  status: DriftStatus
  items: DriftItem[]
  checked_at: string
  since?: string
  auto_repair: boolean
}

/** One thing Kubernetes said about one object, with its repeats counted. */
export type ObjectEvent = {
  type: "Normal" | "Warning"
  reason: string
  kind: string
  name: string
  message: string
  count: number
  first_seen: string
  last_seen: string
  explanation?: string
  explanation_code?: string
  explanation_args?: string[]
}
