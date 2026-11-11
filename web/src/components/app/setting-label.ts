import type { TFunction } from "i18next"

/**
 * The words for each of an app's settings, by the name the API gives it: the
 * same label the settings form shows, so "Build command" in a toast or a
 * notice is the field somebody goes looking for.
 */
const LABELS: Record<string, string> = {
  repo_url: "apps.repository",
  branch: "apps.branch",
  root_dir: "apps.rootDirectory",
  builder: "apps.builder",
  dockerfile_path: "apps.dockerfilePath",
  build_command: "apps.buildCommand",
  static_dir: "apps.outputDirectory",
  image: "apps.image",
  release_command: "apps.releaseCommand",
  port: "apps.port",
  start_command: "apps.startCommand",
  health_check: "apps.healthCheck",
  health_path: "apps.healthPath",
  health_start_seconds: "deploy.rollbackField.health_start_seconds",
  health_timeout_seconds: "deploy.rollbackField.health_timeout_seconds",
  cpu_request_m: "deploy.rollbackField.cpu_request_m",
  cpu_limit_m: "deploy.rollbackField.cpu_limit_m",
  mem_request_mb: "deploy.rollbackField.mem_request_mb",
  mem_limit_mb: "deploy.rollbackField.mem_limit_mb",
  run_as_user: "apps.runAsUser",
  internal: "apps.internal",
}

/** settingLabel names a setting in the reader's language; one it does not know, as the API does. */
export function settingLabel(t: TFunction, setting: string): string {
  const key = LABELS[setting]
  return key ? t(key) : setting
}

/** settingLabels is several, as a list in a sentence. */
export function settingLabels(t: TFunction, settings: string[]): string {
  return settings.map((setting) => settingLabel(t, setting)).join(", ")
}
