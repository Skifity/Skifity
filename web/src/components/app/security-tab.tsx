import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ExternalLinkIcon,
  RocketIcon,
  ScanSearchIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  ShieldOffIcon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Label } from "@/components/ui/label"
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
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type {
  App,
  Deployment,
  ImageScan,
  ScanCounts,
  Severity,
  VulnerabilityReport,
} from "@/lib/types"

/** The severities, most severe first, as the counts name them. */
const SEVERITIES: (keyof ScanCounts)[] = ["critical", "high", "medium", "low", "unknown"]

/** How loudly each severity is drawn: red for what matters, quieter below. */
const BADGE: Record<keyof ScanCounts, "destructive" | "default" | "secondary" | "outline"> = {
  critical: "destructive",
  high: "default",
  medium: "secondary",
  low: "outline",
  unknown: "outline",
}

function severityKey(severity: Severity): keyof ScanCounts {
  return severity.toLowerCase() as keyof ScanCounts
}

/** Whether a scan is still to come back. */
function pending(scan?: ImageScan | null): boolean {
  return scan?.status === "queued" || scan?.status === "running"
}

export function vulnerabilitiesKey(appId: string) {
  return ["vulnerabilities", appId]
}

/**
 * An app's Security tab: what the image it runs is known to be vulnerable to,
 * from the panel's newest Trivy scan, and a way to look again.
 */
export function SecurityTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [onlyFixable, setOnlyFixable] = useState(false)

  const report = useQuery({
    queryKey: vulnerabilitiesKey(app.id),
    queryFn: () => api.get<VulnerabilityReport>(`/api/apps/${app.id}/vulnerabilities`),
    // Asked again while a scan is under way, and not otherwise: the event
    // stream says when one finishes, and a member limited to projects has no
    // stream, which is what the poll is also for.
    refetchInterval: (query) => (pending(query.state.data?.latest) ? 5_000 : false),
  })

  const scan = useMutation({
    mutationFn: () => api.post<ImageScan>(`/api/apps/${app.id}/vulnerabilities/scan`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: vulnerabilitiesKey(app.id) }),
  })

  if (report.isLoading) return <Skeleton className="h-64" />
  if (report.error || !report.data) {
    return <ErrorDisplay error={report.error} onRetry={() => void report.refetch()} />
  }

  const data = report.data
  const result = data.scan
  const latest = data.latest
  const busy = pending(latest) || scan.isPending
  // Derived here, from the report and the switch: nothing to keep in step.
  const findings = (result?.findings ?? []).filter((finding) => !onlyFixable || finding.fixed_in)

  return (
    <div className="space-y-4">
      {!data.enabled && (
        <Alert variant="warning">
          <ShieldOffIcon />
          <AlertDescription>{t("security.disabled")}</AlertDescription>
        </Alert>
      )}

      {data.blocking &&
        result &&
        (data.current || data.undeployed) &&
        result.fixable_critical > 0 && (
          <Alert variant="destructive">
            <ShieldAlertIcon />
            <AlertTitle>{t("security.blocked")}</AlertTitle>
            <AlertDescription>
              {t("security.blockedHelp", { number: result.fixable_critical })}
            </AlertDescription>
          </Alert>
        )}

      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0">
          <div className="space-y-1.5">
            <CardTitle className="flex items-center gap-2 text-base">
              <ScanSearchIcon className="size-4" />
              {t("security.title")}
            </CardTitle>
            <CardDescription>{t("security.help")}</CardDescription>
          </div>
          <Button
            variant="outline"
            size="sm"
            disabled={busy || !data.enabled || !data.image}
            onClick={() => scan.mutate()}
          >
            {busy ? <Spinner /> : <ScanSearchIcon className="size-4" />}
            {t("security.scanNow")}
          </Button>
        </CardHeader>
        <CardContent className="space-y-4">
          {scan.error != null && <ErrorDisplay error={scan.error} compact />}

          {pending(latest) && (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Spinner />
              {latest?.status === "queued" ? t("security.queued") : t("security.running")}
            </p>
          )}

          {latest?.status === "failed" && (
            <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
              <p className="font-medium">
                {latest.error_code
                  ? t(`errors.catalogue.${latest.error_code.replaceAll(".", "_")}.title`, {
                      defaultValue: t("security.failed"),
                    })
                  : t("security.failed")}
              </p>
              {latest.error_message && <p className="mt-1">{latest.error_message}</p>}
              {latest.error_hint && (
                <p className="mt-1 text-muted-foreground">{latest.error_hint}</p>
              )}
            </div>
          )}

          {!result ? (
            <p className="text-sm text-muted-foreground">
              {data.image ? t("security.never") : t("security.notDeployed")}
            </p>
          ) : (
            <>
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  {SEVERITIES.map((severity) => (
                    <Badge
                      key={severity}
                      variant={result.counts[severity] > 0 ? BADGE[severity] : "outline"}
                      className="tabular-nums"
                    >
                      {t("security.countBadge", {
                        severity: t(`security.severity.${severity}`),
                        number: result.counts[severity],
                      })}
                    </Badge>
                  ))}
                  <span className="text-sm text-muted-foreground">
                    {t("security.fixable", { number: result.fixable })}
                  </span>
                </div>
                <p className="text-xs text-muted-foreground">
                  {t("security.scannedAt", { when: formatRelative(result.finished_at) })}
                  {" · "}
                  {t(`security.trigger.${result.trigger}`)}
                  {result.scanner_version && (
                    <> · {t("security.scanner", { version: result.scanner_version })}</>
                  )}
                  {result.os && <> · {result.os}</>}
                </p>
                <p
                  className="truncate font-mono text-xs text-muted-foreground"
                  title={result.image}
                >
                  {result.image}
                </p>
              </div>

              {!data.current && data.image && (
                <Alert variant="warning">
                  <ShieldAlertIcon />
                  <AlertDescription>
                    {data.undeployed ? t("security.undeployed") : t("security.notCurrent")}
                  </AlertDescription>
                </Alert>
              )}

              {(result.findings ?? []).length === 0 ? (
                <p className="flex items-center gap-2 text-sm">
                  <ShieldCheckIcon className="size-4 text-success" />
                  {t("security.clean")}
                </p>
              ) : (
                <div className="space-y-3">
                  <div className="flex items-center gap-2">
                    <Switch
                      id={`only-fixable-${app.id}`}
                      checked={onlyFixable}
                      onCheckedChange={setOnlyFixable}
                    />
                    <Label htmlFor={`only-fixable-${app.id}`}>{t("security.onlyFixable")}</Label>
                  </div>
                  {findings.length === 0 ? (
                    <p className="text-sm text-muted-foreground">{t("security.noneFixable")}</p>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead className="w-28">{t("security.severityHeading")}</TableHead>
                          <TableHead>{t("security.package")}</TableHead>
                          <TableHead>{t("security.installed")}</TableHead>
                          <TableHead>{t("security.fixedIn")}</TableHead>
                          <TableHead>{t("security.advisory")}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {findings.map((finding) => {
                          const severity = severityKey(finding.severity)
                          return (
                            <TableRow key={`${finding.id}:${finding.package}:${finding.installed}`}>
                              <TableCell>
                                <Badge variant={BADGE[severity]}>
                                  {t(`security.severity.${severity}`)}
                                </Badge>
                              </TableCell>
                              <TableCell
                                className="max-w-56 truncate font-mono text-xs"
                                title={
                                  finding.target
                                    ? `${finding.package} · ${finding.target}`
                                    : finding.package
                                }
                              >
                                {finding.package}
                              </TableCell>
                              <TableCell
                                className="max-w-40 truncate font-mono text-xs"
                                title={finding.installed}
                              >
                                {finding.installed}
                              </TableCell>
                              <TableCell
                                className="max-w-40 truncate font-mono text-xs"
                                title={finding.fixed_in}
                              >
                                {finding.fixed_in ?? (
                                  <span className="text-muted-foreground">
                                    {t("security.noFix")}
                                  </span>
                                )}
                              </TableCell>
                              <TableCell className="max-w-64 text-xs">
                                {finding.url ? (
                                  <a
                                    href={finding.url}
                                    target="_blank"
                                    rel="noreferrer"
                                    className="inline-flex min-h-6 items-center gap-1 font-mono underline-offset-4 hover:underline"
                                    title={finding.title}
                                  >
                                    {finding.id}
                                    <ExternalLinkIcon className="size-3" />
                                  </a>
                                ) : (
                                  <span className="font-mono" title={finding.title}>
                                    {finding.id}
                                  </span>
                                )}
                              </TableCell>
                            </TableRow>
                          )
                        })}
                      </TableBody>
                    </Table>
                  )}
                  {result.omitted > 0 && (
                    <p className="text-xs text-muted-foreground">
                      {t("security.more", { number: result.omitted })}
                    </p>
                  )}
                </div>
              )}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

/**
 * Deploys a version the panel stopped for a critical vulnerability with a fix,
 * past that check. Asked first, and recorded in the activity log with whoever
 * pressed it.
 */
export function DeployAnywayButton({ app, deployment }: { app: App; deployment: Deployment }) {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const deploy = useMutation({
    mutationFn: () =>
      api.post<Deployment>(`/api/apps/${app.id}/deploy`, {
        commit_sha: deployment.commit_sha || undefined,
        accept_vulnerabilities: true,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["deployments", app.id] })
      void queryClient.invalidateQueries({ queryKey: ["app-status", app.id] })
    },
  })

  return (
    <div className="space-y-2">
      <Button
        variant="outline"
        size="sm"
        disabled={deploy.isPending}
        onClick={() => {
          void confirm({
            title: t("deploy.deployAnyway"),
            description: t("deploy.deployAnywayConfirm"),
            confirmLabel: t("deploy.deployAnyway"),
            destructive: true,
          }).then((yes) => {
            if (yes) deploy.mutate()
          })
        }}
      >
        {deploy.isPending ? <Spinner /> : <RocketIcon className="size-3.5" />}
        {t("deploy.deployAnyway")}
      </Button>
      {deploy.error != null && <ErrorDisplay error={deploy.error} compact />}
    </div>
  )
}

/** The count of critical vulnerabilities on an app in a list, when it has any. */
export function CriticalBadge({ counts }: { counts?: ScanCounts }) {
  const { t } = useTranslation()
  if (!counts || counts.critical === 0) return null
  return (
    <Badge variant="destructive" className="gap-1 tabular-nums">
      <ShieldAlertIcon className="size-3" />
      {t("apps.criticalBadge", { number: counts.critical })}
    </Badge>
  )
}
