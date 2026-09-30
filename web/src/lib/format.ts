import { currentLanguage } from "@/lib/i18n"

/**
 * Locale-aware formatting.
 *
 * Everything here goes through Intl with the active language, so a Russian user
 * sees "16 сентября" and a Chinese user sees "9月16日", rather than an English
 * date with translated labels around it.
 */

/** Formats a date and time in the user's language. */
export function formatDateTime(value: string | Date | undefined): string {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  if (Number.isNaN(date.getTime())) return "—"
  return new Intl.DateTimeFormat(currentLanguage(), {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date)
}

/** Formats a date without the time. */
export function formatDate(value: string | Date | undefined): string {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  if (Number.isNaN(date.getTime())) return "—"
  return new Intl.DateTimeFormat(currentLanguage(), { dateStyle: "medium" }).format(date)
}

/**
 * Formats a time as "3 minutes ago", in the user's language.
 *
 * Intl.RelativeTimeFormat handles the plural rules, so this needs no
 * translations of its own and is correct in languages with more than two
 * plural categories.
 */
export function formatRelative(value: string | Date | undefined): string {
  if (!value) return "—"
  const date = typeof value === "string" ? new Date(value) : value
  if (Number.isNaN(date.getTime())) return "—"

  const seconds = Math.round((date.getTime() - Date.now()) / 1000)
  const formatter = new Intl.RelativeTimeFormat(currentLanguage(), { numeric: "auto" })

  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 60 * 60 * 24 * 365],
    ["month", 60 * 60 * 24 * 30],
    ["week", 60 * 60 * 24 * 7],
    ["day", 60 * 60 * 24],
    ["hour", 60 * 60],
    ["minute", 60],
  ]
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return formatter.format(Math.round(seconds / size), unit)
    }
  }
  return formatter.format(Math.round(seconds), "second")
}

/** Formats a number in the user's language. */
export function formatNumber(value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(currentLanguage(), options).format(value)
}

/** Formats a percentage. */
export function formatPercent(value: number, total: number): string {
  if (total <= 0) return "—"
  return new Intl.NumberFormat(currentLanguage(), {
    style: "percent",
    maximumFractionDigits: 0,
  }).format(value / total)
}

/**
 * Formats a size in bytes.
 *
 * Binary units, because that is what a disk quota and a container limit mean,
 * and reporting 1 GB where the cluster enforces 1 GiB is a difference someone
 * eventually has to debug.
 */
export function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${formatNumber(value, { maximumFractionDigits: value < 10 && unit > 0 ? 1 : 0 })} ${units[unit]}`
}

/** Formats memory in megabytes, which is how Kubernetes reports it. */
export function formatMemory(megabytes: number): string {
  return formatBytes(megabytes * 1024 * 1024)
}

/** Formats CPU in millicores as a readable core count. */
export function formatCPU(millicores: number): string {
  if (millicores < 1000) return `${formatNumber(millicores)}m`
  return `${formatNumber(millicores / 1000, { maximumFractionDigits: 2 })}`
}

/** Formats a duration between two moments, or from one to now. */
export function formatDuration(start?: string, end?: string): string {
  if (!start) return "—"
  const from = new Date(start).getTime()
  const to = end ? new Date(end).getTime() : Date.now()
  if (Number.isNaN(from) || Number.isNaN(to)) return "—"

  const seconds = Math.max(0, Math.round((to - from) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${minutes % 60}m`
}

/** Shortens a commit hash for display. */
export function shortCommit(sha: string | undefined): string {
  if (!sha) return "—"
  return sha.slice(0, 7)
}

/** Turns a repository URL into "owner/repo". */
export function repoName(url: string): string {
  if (!url) return ""
  try {
    const parsed = new URL(url)
    return parsed.pathname.replace(/^\/|\.git$/g, "")
  } catch {
    return url
  }
}
