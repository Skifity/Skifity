import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ActivityIcon, BellIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { TrafficCharts, type Traffic } from "@/components/app/traffic-charts"
import { UsageChart } from "@/components/usage-chart"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App } from "@/lib/types"

type MetricPoint = {
  at: string
  cpu_m: number
  memory_mb: number
  cpu_peak_pct: number
  memory_peak_pct: number
  ready: number
  desired: number
  restarts: number
  /** False where no minute of the bucket had its CPU and memory measured. */
  usage_known: boolean
}

type Metrics = { range: string; points: MetricPoint[]; traffic: Traffic }

type Alerts = {
  memory_pct: number
  cpu_pct: number
  restarts: number
  server_errors_pct: number
  firing: string[]
}

const ALERT_FIELDS = ["memory_pct", "cpu_pct", "restarts", "server_errors_pct"] as const

const RANGES = ["1h", "6h", "24h", "72h"] as const

/**
 * What the app used over the last hours, and the requests it answered, against
 * the thresholds it is watched against. The watcher reads every app once a
 * minute and keeps three days; the usage lines are the busiest instance as a
 * share of its limit, which is what gets an instance throttled or killed.
 */
export function UsageCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const [range, setRange] = useState<(typeof RANGES)[number]>("6h")

  const metrics = useQuery({
    queryKey: ["metrics", app.id, range],
    queryFn: () => api.get<Metrics>(`/api/apps/${app.id}/metrics?range=${range}`),
    refetchInterval: 60_000,
  })
  const alerts = useQuery({
    queryKey: ["alerts", app.id],
    queryFn: () => api.get<Alerts>(`/api/apps/${app.id}/alerts`),
  })

  const points = metrics.data?.points ?? []
  // A bucket nobody measured is a gap in the line, not a dip to zero.
  const measured = points.filter((point) => point.usage_known)
  const percent = (value: number) => `${Math.round(value)}%`

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3 space-y-0">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2 text-base">
            <ActivityIcon className="size-4" />
            {t("apps.usage")}
          </CardTitle>
          <CardDescription>{t("apps.usageHelp")}</CardDescription>
        </div>
        <Tabs value={range} onValueChange={(next) => setRange(next as (typeof RANGES)[number])}>
          <TabsList>
            {RANGES.map((name) => (
              <TabsTrigger key={name} value={name}>
                {t(`apps.range.${name}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent className="space-y-4">
        {metrics.isLoading ? (
          <Skeleton className="h-48" />
        ) : metrics.error ? (
          <ErrorDisplay error={metrics.error} compact />
        ) : points.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("apps.usageNone")}</p>
        ) : (
          <div className="grid gap-6 md:grid-cols-2">
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("apps.usageMemory")}</p>
              <UsageChart
                label={t("apps.usageMemory")}
                points={measured.map((point) => ({ at: point.at, value: point.memory_peak_pct }))}
                max={100}
                threshold={alerts.data?.memory_pct}
                format={percent}
              />
            </div>
            <div className="space-y-1">
              <p className="text-sm font-medium">{t("apps.usageCPU")}</p>
              <UsageChart
                label={t("apps.usageCPU")}
                points={measured.map((point) => ({ at: point.at, value: point.cpu_peak_pct }))}
                max={100}
                threshold={alerts.data?.cpu_pct}
                format={percent}
              />
            </div>
          </div>
        )}
        {metrics.data?.traffic && (
          <TrafficCharts
            traffic={metrics.data.traffic}
            serverErrorsThreshold={alerts.data?.server_errors_pct}
          />
        )}
        {alerts.data && <AlertSettings app={app} alerts={alerts.data} />}
      </CardContent>
    </Card>
  )
}

/**
 * The thresholds, as four numbers. Zero is off. Crossing one is said once to
 * the team's notification channels, and so is coming back under it.
 */
function AlertSettings({ app, alerts }: { app: App; alerts: Alerts }) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<Record<(typeof ALERT_FIELDS)[number], string>>({
    memory_pct: String(alerts.memory_pct),
    cpu_pct: String(alerts.cpu_pct),
    restarts: String(alerts.restarts),
    server_errors_pct: String(alerts.server_errors_pct),
  })
  const save = useMutation({
    mutationFn: () =>
      api.put(`/api/apps/${app.id}/alerts`, {
        memory_pct: Number(draft.memory_pct) || 0,
        cpu_pct: Number(draft.cpu_pct) || 0,
        restarts: Number(draft.restarts) || 0,
        server_errors_pct: Number(draft.server_errors_pct) || 0,
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["alerts", app.id] }),
  })

  return (
    <form
      className="space-y-3 rounded-lg border p-3"
      onSubmit={(event) => {
        event.preventDefault()
        save.mutate()
      }}
    >
      <div className="flex items-center gap-2 text-sm font-medium">
        <BellIcon className="size-4" />
        {t("apps.alertsTitle")}
      </div>
      <p className="text-xs text-muted-foreground">{t("apps.alertsHelp")}</p>
      <p className="text-xs text-muted-foreground">{t("apps.alertsErrorsHelp")}</p>
      {alerts.firing.length > 0 && (
        <p className="text-xs font-medium text-destructive">
          {t("apps.alertsFiring", {
            list: alerts.firing.map((name) => t(`apps.alertName.${name}`)).join(", "),
          })}
        </p>
      )}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {ALERT_FIELDS.map((field) => (
          <div key={field} className="space-y-1">
            <Label htmlFor={`alert-${field}`} className="text-xs">
              {t(`apps.alertField.${field}`)}
            </Label>
            <Input
              id={`alert-${field}`}
              type="number"
              inputMode="numeric"
              min={0}
              max={100}
              value={draft[field]}
              onChange={(event) => setDraft({ ...draft, [field]: event.target.value })}
            />
          </div>
        ))}
      </div>
      {save.error != null && <ErrorDisplay error={save.error} compact />}
      <div className="flex justify-end">
        <Button type="submit" size="sm" disabled={save.isPending}>
          {save.isPending && <Spinner />}
          {save.isPending ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </form>
  )
}
