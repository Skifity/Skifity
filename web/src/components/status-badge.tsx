import { useTranslation } from "react-i18next"
import {
  AlertTriangleIcon,
  CheckCircle2Icon,
  CircleDashedIcon,
  CircleIcon,
  Loader2Icon,
  PauseCircleIcon,
  XCircleIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { cn } from "cn"

/**
 * A status is never colour alone: every one carries an icon and a word, so it
 * is readable to someone who cannot distinguish the colours.
 */
type Tone = "success" | "warning" | "danger" | "busy" | "idle" | "neutral"

const TONE_STYLES: Record<Tone, string> = {
  success: "border-success/30 bg-success/10 text-success",
  warning: "border-warning/30 bg-warning/10 text-warning",
  danger: "border-destructive/30 bg-destructive/10 text-destructive",
  busy: "border-info/30 bg-info/10 text-info",
  idle: "border-muted-foreground/25 bg-muted text-muted-foreground",
  neutral: "border-border bg-muted text-muted-foreground",
}

const TONE_ICONS: Record<Tone, typeof CircleIcon> = {
  success: CheckCircle2Icon,
  warning: AlertTriangleIcon,
  danger: XCircleIcon,
  busy: Loader2Icon,
  idle: PauseCircleIcon,
  neutral: CircleDashedIcon,
}

/** Maps a backend status string onto a tone. */
export function toneFor(status: string): Tone {
  switch (status) {
    case "ready":
    case "running":
    case "succeeded":
    case "active":
      return "success"
    case "provisioning":
    case "creating":
    case "starting":
    case "building":
    case "deploying":
    case "updating":
    case "removing":
      return "busy"
    // "degraded" is up, and one failure away from not being. It is not success
    // and it is not a failure, and showing it as either is the whole reason it
    // is here rather than folded into one of them.
    case "pending":
    case "queued":
    case "not_ready":
    case "degraded":
      return "warning"
    case "failed":
    case "crashing":
    case "unhealthy":
      return "danger"
    // Asleep is not down: scale to zero took the last instance away on
    // purpose and the next request brings it back. It shares the idle tone
    // with "stopped" because neither is serving, and the word tells them
    // apart.
    case "sleeping":
    case "stopped":
    case "cancelled":
    case "superseded":
    case "created":
    case "not_deployed":
      return "idle"
    default:
      return "neutral"
  }
}

export function StatusBadge({
  status,
  label,
  className,
}: {
  status: string
  /** An already-translated label; the raw status is used when omitted. */
  label?: string
  className?: string
}) {
  const { t } = useTranslation()
  const tone = toneFor(status)
  const Icon = TONE_ICONS[tone]

  return (
    <Badge
      variant="outline"
      className={cn("gap-1.5 font-medium", TONE_STYLES[tone], className)}
      data-slot="status-badge"
    >
      <Icon className={cn("size-3", tone === "busy" && "animate-spin")} />
      {label ?? t(`apps.phase.${status}`, { defaultValue: status })}
    </Badge>
  )
}
