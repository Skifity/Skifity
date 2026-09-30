import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CircleAlertIcon, CircleCheckIcon, ShieldAlertIcon, ShieldCheckIcon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { HardeningFinding, HardeningReport } from "@/lib/types"

/**
 * How a server stands up to the internet: SSH passwords, root, fail2ban,
 * automatic updates, the firewall, a pending reboot. Checked when asked
 * rather than on every visit, because it signs in to the machine over SSH.
 */
export function ServerHardening({ serverId }: { serverId: string }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [asked, setAsked] = useState(false)
  const key = ["server-hardening", serverId]

  const report = useQuery({
    queryKey: key,
    queryFn: () => api.get<HardeningReport>(`/api/servers/${serverId}/hardening`),
    enabled: asked,
    retry: false,
    staleTime: Infinity,
  })

  const passwordsOff = useMutation({
    mutationFn: () =>
      api.post<HardeningReport>(`/api/servers/${serverId}/hardening/ssh-passwords-off`),
    onSuccess: (next) => queryClient.setQueryData(key, next),
  })

  const risks = report.data?.findings.filter((f) => f.level === "risk").length ?? 0

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            {risks > 0 ? (
              <ShieldAlertIcon className="size-4 text-destructive" />
            ) : (
              <ShieldCheckIcon className="size-4 text-muted-foreground" />
            )}
            {t("servers.hardening.title")}
          </CardTitle>
          <CardDescription>{t("servers.hardening.description")}</CardDescription>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={report.isFetching}
          onClick={() => (asked ? void report.refetch() : setAsked(true))}
        >
          {report.isFetching && <Spinner />}
          {report.data ? t("servers.hardening.checkAgain") : t("servers.hardening.check")}
        </Button>
      </CardHeader>
      {(report.data || report.error || passwordsOff.error) && (
        <CardContent className="space-y-4">
          {report.error != null && <ErrorDisplay error={report.error} compact />}
          {report.data && (
            <>
              <ul className="divide-y rounded-md border">
                {report.data.findings.map((finding) => (
                  <Finding key={finding.code} finding={finding} />
                ))}
              </ul>
              <p className="text-xs text-muted-foreground">
                {t("servers.hardening.checkedAt", {
                  when: formatRelative(report.data.checked_at),
                })}
              </p>
              {report.data.can_turn_off_passwords && (
                <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-destructive/30 p-3">
                  <p className="min-w-0 flex-1 text-sm">
                    {t("servers.hardening.passwordsOffHelp")}
                  </p>
                  <Button
                    size="sm"
                    disabled={passwordsOff.isPending}
                    onClick={() => {
                      void confirm({
                        title: t("servers.hardening.passwordsOffTitle"),
                        description: t("servers.hardening.passwordsOffConfirm"),
                        confirmLabel: t("servers.hardening.passwordsOff"),
                        destructive: true,
                      }).then((yes) => {
                        if (yes) passwordsOff.mutate()
                      })
                    }}
                  >
                    {passwordsOff.isPending && <Spinner />}
                    {t("servers.hardening.passwordsOff")}
                  </Button>
                </div>
              )}
            </>
          )}
          {passwordsOff.error != null && <ErrorDisplay error={passwordsOff.error} compact />}
        </CardContent>
      )}
    </Card>
  )
}

function Finding({ finding }: { finding: HardeningFinding }) {
  const { t } = useTranslation()
  const base = `servers.hardening.checks.${finding.code}`
  const detail = t(`${base}.${finding.state}.detail`, {
    firewall: finding.firewall ?? "",
    defaultValue: finding.detail,
  })
  const fix =
    finding.level === "ok"
      ? ""
      : t(`${base}.${finding.state}.fix`, { defaultValue: finding.fix ?? "" })

  return (
    <li className="flex gap-3 p-3">
      {finding.level === "ok" ? (
        <CircleCheckIcon className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
      ) : (
        <CircleAlertIcon
          className={
            finding.level === "risk"
              ? "mt-0.5 size-4 shrink-0 text-destructive"
              : "mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
          }
        />
      )}
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">{t(`${base}.title`)}</span>
          {finding.level !== "ok" && (
            <Badge variant={finding.level === "risk" ? "destructive" : "outline"}>
              {t(`servers.hardening.level.${finding.level}`)}
            </Badge>
          )}
        </div>
        <p className="text-sm text-muted-foreground">{detail}</p>
        {fix && <p className="text-sm">{fix}</p>}
      </div>
    </li>
  )
}
