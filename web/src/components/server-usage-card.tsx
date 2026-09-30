import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ActivityIcon, BellIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
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

type ServerPoint = {
  at: string
  cpu_pct: number
  memory_pct: number
  /** Missing where the disk could not be read. */
  disk_pct?: number
  pods: number
}

type ServerUsage = { range: string; points: ServerPoint[] }

type ServerAlerts = { disk_pct: number; memory_pct: number; cpu_pct: number; firing: string[] }

const RANGES = ["1h", "6h", "24h", "72h"] as const
const FIELDS = ["disk_pct", "memory_pct", "cpu_pct"] as const

/**
 * What a server used over the last hours, its disk included, against the
 * thresholds it is watched against. A full disk is how a self-hosted server
 * usually dies, so it comes first.
 */
export function ServerUsageCard({ serverId, canEdit }: { serverId: string; canEdit: boolean }) {
  const { t } = useTranslation()
  const [range, setRange] = useState<(typeof RANGES)[number]>("6h")

  const usage = useQuery({
    queryKey: ["server-usage", serverId, range],
    queryFn: () => api.get<ServerUsage>(`/api/servers/${serverId}/usage?range=${range}`),
    refetchInterval: 60_000,
  })
  const alerts = useQuery({
    queryKey: ["server-alerts", serverId],
    queryFn: () => api.get<ServerAlerts>(`/api/servers/${serverId}/alerts`),
  })

  const points = usage.data?.points ?? []
  const percent = (value: number) => `${Math.round(value)}%`
  const charts = [
    {
      key: "disk",
      label: t("servers.usageDisk"),
      points: points.flatMap((point) =>
        point.disk_pct == null ? [] : [{ at: point.at, value: point.disk_pct }],
      ),
      threshold: alerts.data?.disk_pct,
    },
    {
      key: "memory",
      label: t("apps.usageMemory"),
      points: points.map((point) => ({ at: point.at, value: point.memory_pct })),
      threshold: alerts.data?.memory_pct,
    },
    {
      key: "cpu",
      label: t("apps.usageCPU"),
      points: points.map((point) => ({ at: point.at, value: point.cpu_pct })),
      threshold: alerts.data?.cpu_pct,
    },
  ]

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3 space-y-0">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2 text-base">
            <ActivityIcon className="size-4" />
            {t("apps.usage")}
          </CardTitle>
          <CardDescription>{t("servers.usageHelp")}</CardDescription>
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
        {usage.isLoading ? (
          <Skeleton className="h-48" />
        ) : usage.error ? (
          <ErrorDisplay error={usage.error} compact />
        ) : points.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("servers.usageNone")}</p>
        ) : (
          <div className="grid gap-6 md:grid-cols-3">
            {charts.map((chart) => (
              <div key={chart.key} className="space-y-1">
                <p className="text-sm font-medium">{chart.label}</p>
                <UsageChart
                  label={chart.label}
                  points={chart.points}
                  max={100}
                  threshold={chart.threshold}
                  format={percent}
                />
              </div>
            ))}
          </div>
        )}
        {alerts.data && (
          <ServerAlertSettings serverId={serverId} alerts={alerts.data} canEdit={canEdit} />
        )}
      </CardContent>
    </Card>
  )
}

/** The thresholds, as three percentages. Zero is off. */
function ServerAlertSettings({
  serverId,
  alerts,
  canEdit,
}: {
  serverId: string
  alerts: ServerAlerts
  canEdit: boolean
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<Record<(typeof FIELDS)[number], string>>({
    disk_pct: String(alerts.disk_pct),
    memory_pct: String(alerts.memory_pct),
    cpu_pct: String(alerts.cpu_pct),
  })
  const save = useMutation({
    mutationFn: () =>
      api.put(`/api/servers/${serverId}/alerts`, {
        disk_pct: Number(draft.disk_pct) || 0,
        memory_pct: Number(draft.memory_pct) || 0,
        cpu_pct: Number(draft.cpu_pct) || 0,
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["server-alerts", serverId] }),
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
      {alerts.firing.length > 0 && (
        <p className="text-xs font-medium text-destructive">
          {t("apps.alertsFiring", {
            list: alerts.firing.map((name) => t(`servers.alertName.${name}`)).join(", "),
          })}
        </p>
      )}
      <div className="grid gap-3 sm:grid-cols-3">
        {FIELDS.map((field) => (
          <div key={field} className="space-y-1">
            <Label htmlFor={`server-alert-${field}`} className="text-xs">
              {t(`servers.alertField.${field}`)}
            </Label>
            <Input
              id={`server-alert-${field}`}
              type="number"
              inputMode="numeric"
              min={0}
              max={100}
              disabled={!canEdit}
              value={draft[field]}
              onChange={(event) => setDraft({ ...draft, [field]: event.target.value })}
            />
          </div>
        ))}
      </div>
      {save.error != null && <ErrorDisplay error={save.error} compact />}
      {canEdit && (
        <div className="flex justify-end">
          <Button type="submit" size="sm" disabled={save.isPending}>
            {save.isPending && <Spinner />}
            {save.isPending ? t("common.saving") : t("common.save")}
          </Button>
        </div>
      )}
    </form>
  )
}
