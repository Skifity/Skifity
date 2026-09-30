import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CircleCheckIcon, GitCompareArrowsIcon, Undo2Icon } from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, DriftItem, DriftReport } from "@/lib/types"

/**
 * Changes made to an app outside the panel.
 *
 * The watcher compares each app's objects in the cluster with what the panel
 * applies every few minutes; this page reads the cluster itself when it is
 * opened, so what it shows is now rather than up to five minutes ago. The
 * objects and fields are Kubernetes' own, which is why the list lives under
 * Advanced and the notice at the top of the app only says that something
 * changed.
 */

function useDrift(appId: string) {
  return useQuery({
    queryKey: ["drift", appId],
    queryFn: () => api.get<DriftReport>(`/api/apps/${appId}/drift`),
    // A few requests to the cluster each time; the notice and the card share
    // this one, and a page left open is refreshed by the watcher's event.
    staleTime: 30_000,
    retry: false,
  })
}

function isDrifted(
  report: DriftReport | undefined,
): report is DriftReport & { status: "drifted" | "missing" } {
  return report?.status === "drifted" || report?.status === "missing"
}

/** Said at the top of the app while its objects are not what the panel applied. */
export function DriftNotice({ app, onOpen }: { app: App; onOpen: () => void }) {
  const { t } = useTranslation()
  const drift = useDrift(app.id)
  // A cluster that cannot be read is said elsewhere on the page; this notice
  // is only ever about a change somebody made.
  if (!isDrifted(drift.data)) return null
  return (
    <Alert variant="warning">
      <GitCompareArrowsIcon />
      <AlertTitle>{t("drift.noticeTitle", { product: "Skifity" })}</AlertTitle>
      <AlertDescription>
        <p>{t("drift.noticeBody")}</p>
        <Button variant="outline" size="sm" className="mt-2" onClick={onOpen}>
          {t("drift.noticeOpen")}
        </Button>
      </AlertDescription>
    </Alert>
  )
}

/** The list of what changed, under Advanced, with the way to put it back. */
export function DriftCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirm = useConfirm()
  const drift = useDrift(app.id)
  // Viewers see what changed and cannot change it back; the panel refuses
  // them anyway, and a button that only ever answers "no" is not offered.
  const canChange = team?.role !== undefined && team.role !== "viewer"

  const repair = useMutation({
    mutationFn: () => api.post<DriftReport>(`/api/apps/${app.id}/drift/repair`),
    onSuccess: (report) => {
      queryClient.setQueryData(["drift", app.id], report)
      void queryClient.invalidateQueries({ queryKey: ["app-status", app.id] })
      toast.success(t("drift.repaired"))
    },
  })
  const autoRepair = useMutation({
    mutationFn: (on: boolean) =>
      api.put<{ auto_repair: boolean }>(`/api/apps/${app.id}/drift`, { auto_repair: on }),
    onSuccess: (answer) => {
      queryClient.setQueryData<DriftReport>(["drift", app.id], (current) =>
        current ? { ...current, auto_repair: answer.auto_repair } : current,
      )
    },
  })

  const report = drift.data
  const drifted = isDrifted(report)

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1.5">
            <CardTitle className="flex items-center gap-2 text-base">
              {/* A heading for the section whatever it holds; the icon is the
                  answer, and only once there is one. */}
              {drifted ? (
                <GitCompareArrowsIcon className="size-4 text-warning" />
              ) : (
                report?.status === "in_sync" && <CircleCheckIcon className="size-4 text-success" />
              )}
              {t("drift.title", { product: "Skifity" })}
            </CardTitle>
            <CardDescription>{t("drift.help")}</CardDescription>
          </div>
          {canChange && drifted && (
            <Button
              disabled={repair.isPending}
              onClick={() => {
                void confirm({
                  title: t("drift.repairConfirmTitle"),
                  description: t("drift.repairConfirm", { product: "Skifity" }),
                  confirmLabel: t("drift.repair"),
                }).then((yes) => {
                  if (yes) repair.mutate()
                })
              }}
            >
              {repair.isPending ? <Spinner /> : <Undo2Icon />}
              {t("drift.repair")}
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {drift.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : drift.error != null ? (
          <ErrorDisplay error={drift.error} compact onRetry={() => void drift.refetch()} />
        ) : report?.status === "not_deployed" ? (
          <p className="text-sm text-muted-foreground">{t("drift.notDeployed")}</p>
        ) : report?.status === "applying" ? (
          <p className="text-sm text-muted-foreground">
            {t("drift.applying", { product: "Skifity" })}
          </p>
        ) : !drifted ? (
          <p className="text-sm text-muted-foreground">
            {t("drift.inSync", {
              product: "Skifity",
              when: formatRelative(report?.checked_at),
            })}
          </p>
        ) : (
          <>
            <p className="text-sm text-muted-foreground">
              {report.since
                ? t("drift.since", { when: formatRelative(report.since) })
                : t("drift.checked", { when: formatRelative(report.checked_at) })}
            </p>
            <DriftTable items={report.items} />
          </>
        )}
        {repair.error != null && <ErrorDisplay error={repair.error} compact />}

        {canChange && report && report.status !== "not_deployed" && (
          <label className="flex items-start justify-between gap-4 rounded-md border p-3">
            <span className="space-y-1">
              <span className="block text-sm font-medium">{t("drift.autoRepair")}</span>
              <span className="block text-xs text-muted-foreground">
                {t("drift.autoRepairHelp")}
              </span>
            </span>
            <Switch
              checked={report.auto_repair}
              disabled={autoRepair.isPending}
              onCheckedChange={(on) => autoRepair.mutate(on)}
              aria-label={t("drift.autoRepair")}
            />
          </label>
        )}
        {autoRepair.error != null && <ErrorDisplay error={autoRepair.error} compact />}
      </CardContent>
    </Card>
  )
}

function DriftTable({ items }: { items: DriftItem[] }) {
  const { t } = useTranslation()
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("drift.object")}</TableHead>
            <TableHead>{t("drift.field")}</TableHead>
            <TableHead>{t("drift.panelValue", { product: "Skifity" })}</TableHead>
            <TableHead>{t("drift.liveValue")}</TableHead>
            <TableHead className="hidden md:table-cell">{t("drift.changedBy")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={`${item.kind}/${item.name}/${item.path ?? ""}`}>
              <TableCell className="font-mono text-xs whitespace-nowrap">
                {item.kind}/{item.name}
              </TableCell>
              {item.change === "deleted" ? (
                <TableCell colSpan={3}>
                  <Badge variant="destructive">{t("drift.deleted")}</Badge>
                </TableCell>
              ) : (
                <>
                  <TableCell className="max-w-64 font-mono text-xs break-all">
                    {item.path}
                  </TableCell>
                  {item.hidden ? (
                    <TableCell colSpan={2} className="text-xs text-muted-foreground">
                      {t("drift.hidden")}
                    </TableCell>
                  ) : (
                    <>
                      <TableCell className="max-w-56 font-mono text-xs break-all">
                        {item.panel}
                      </TableCell>
                      <TableCell className="max-w-56 font-mono text-xs break-all">
                        {item.change === "removed" ? (
                          <Badge variant="outline">{t("drift.removed")}</Badge>
                        ) : (
                          item.live
                        )}
                      </TableCell>
                    </>
                  )}
                </>
              )}
              <TableCell className="hidden text-xs md:table-cell">
                {item.changed_by ? (
                  <span className="font-mono">{item.changed_by}</span>
                ) : (
                  <span className="text-muted-foreground">{t("drift.unknownWho")}</span>
                )}
                {item.changed_at && (
                  <span className="block text-muted-foreground">
                    {formatRelative(item.changed_at)}
                  </span>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
