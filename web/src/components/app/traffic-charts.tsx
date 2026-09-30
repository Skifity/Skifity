import { useTranslation } from "react-i18next"
import { GlobeIcon } from "lucide-react"

import { UsageChart } from "@/components/usage-chart"
import { formatNumber } from "@/lib/format"

export type TrafficPoint = {
  at: string
  /** Minutes counted in the bucket; a rate is requests / minutes. */
  minutes: number
  requests: number
  status_2xx: number
  status_3xx: number
  status_4xx: number
  status_5xx: number
  /** Null where nothing in the bucket was timed. */
  p50_ms: number | null
  /** The slowest minute's, so a bad minute is not averaged away. */
  p95_ms: number | null
  /** Some minute could not count all of its traffic. */
  partial: boolean
}

export type Traffic = {
  /** Whether the ingress could be read on the watcher's last pass. */
  source: "no_cluster" | "starting" | "reading" | "no_traefik" | "unreachable"
  /** Whether anything reaches the app through the ingress at all. */
  routed: boolean
  points: TrafficPoint[]
}

/**
 * The requests that reached the app through the ingress: how many, how many
 * failed, and how long the slowest took. Each is a chart of its own on one
 * scale, rather than two measures sharing an axis.
 *
 * When nothing is drawn it says why, because "no data" reads as "no traffic",
 * and those are different things: an app with no domain, a panel that has just
 * started, an ingress that did not answer.
 */
export function TrafficCharts({
  traffic,
  serverErrorsThreshold,
}: {
  traffic: Traffic
  serverErrorsThreshold?: number
}) {
  const { t } = useTranslation()
  const points = traffic.points
  const anyRequests = points.some((point) => point.requests > 0)
  // A worker has minutes of nothing recorded like every app; drawing them
  // would say "no visitors" when the truth is "no way in".
  const reason = !traffic.routed && !anyRequests ? "notRouted" : traffic.source
  const drawn = points.length > 0 && (traffic.routed || anyRequests)

  // A share of no requests is not zero, it is nothing: those buckets are gaps.
  const answered = points.filter((point) => point.requests > 0)
  const share = (part: number, whole: number) => (part * 100) / whole
  const percent = (value: number) =>
    formatNumber(value / 100, { style: "percent", maximumFractionDigits: 1 })
  const duration = (ms: number) =>
    ms >= 1000
      ? formatNumber(ms / 1000, { style: "unit", unit: "second", maximumFractionDigits: 2 })
      : formatNumber(ms, { style: "unit", unit: "millisecond", maximumFractionDigits: 0 })

  return (
    <section className="space-y-3 border-t pt-4">
      <div className="space-y-1">
        <p className="flex items-center gap-2 text-sm font-medium">
          <GlobeIcon className="size-4" />
          {t("apps.traffic")}
        </p>
        <p className="text-sm text-muted-foreground">{t("apps.trafficHelp")}</p>
      </div>
      {!drawn ? (
        <p className="text-sm text-muted-foreground">{t(`apps.trafficWhy.${reason}`)}</p>
      ) : (
        <>
          <div className="grid gap-6 md:grid-cols-2">
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("apps.trafficRequests")}</p>
              <UsageChart
                label={t("apps.trafficRequests")}
                points={points.map((point) => ({
                  at: point.at,
                  value: point.requests / Math.max(point.minutes, 1),
                }))}
                max={0}
                format={(value) => formatNumber(value, { maximumFractionDigits: 1 })}
              />
            </div>
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("apps.trafficServerErrors")}</p>
              <UsageChart
                label={t("apps.trafficServerErrors")}
                points={answered.map((point) => ({
                  at: point.at,
                  value: share(point.status_5xx, point.requests),
                }))}
                max={serverErrorsThreshold ?? 0}
                threshold={serverErrorsThreshold}
                format={percent}
              />
            </div>
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("apps.trafficClientErrors")}</p>
              <UsageChart
                label={t("apps.trafficClientErrors")}
                points={answered.map((point) => ({
                  at: point.at,
                  value: share(point.status_4xx, point.requests),
                }))}
                max={0}
                format={percent}
              />
            </div>
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("apps.trafficLatency")}</p>
              <UsageChart
                label={t("apps.trafficLatency")}
                points={points.flatMap((point) =>
                  point.p95_ms == null ? [] : [{ at: point.at, value: point.p95_ms }],
                )}
                max={0}
                format={duration}
              />
            </div>
          </div>
          {points.some((point) => point.partial) && (
            <p className="text-xs text-muted-foreground">{t("apps.trafficPartial")}</p>
          )}
          {(traffic.source === "no_traefik" || traffic.source === "unreachable") && (
            <p className="text-xs text-muted-foreground">
              {t(`apps.trafficWhy.${traffic.source}`)}
            </p>
          )}
        </>
      )}
    </section>
  )
}
