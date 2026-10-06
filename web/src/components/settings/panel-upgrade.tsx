import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { CheckCircle2Icon, ExternalLinkIcon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { useSession } from "@/hooks/use-session"
import type { UpgradeCheck, UpgradeStarted } from "@/lib/types"

/**
 * The panel's own version, and upgrading it.
 *
 * Nothing here asks anybody anything until the button is pressed: the panel
 * never checks for updates on its own, which is a promise this card keeps by
 * having no query that runs when it is shown. The check is a POST for the same
 * reason — a GET could be prefetched.
 *
 * What follows a check is the answer and nothing more: a newer release gets a
 * button to upgrade to it, with what the upgrade will do said first, and the
 * way back printed once it has started, because the panel is gone for a moment
 * and cannot be asked again.
 */
export function PanelUpgradeCard() {
  const { t } = useTranslation()
  const { meta } = useSession()
  const confirm = useConfirm()

  const check = useMutation({
    mutationFn: () => api.post<UpgradeCheck>("/api/upgrade/check"),
  })
  const upgrade = useMutation({
    mutationFn: (version: string) => api.post<UpgradeStarted>("/api/upgrade", { version }),
  })

  const found = check.data
  const latest = found?.latest_version ?? ""

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("settings.upgrade")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <div className="flex items-center justify-between">
          <span className="text-muted-foreground">{t("settings.currentVersion")}</span>
          <span className="font-mono">{meta?.version ?? "—"}</span>
        </div>
        <p className="text-xs text-muted-foreground">
          {t("panelUpgrade.manualNote", { product: meta?.product ?? "Skifity" })}
        </p>

        <Button
          variant="outline"
          size="sm"
          disabled={check.isPending || upgrade.isPending || upgrade.isSuccess}
          onClick={() => {
            upgrade.reset()
            check.mutate()
          }}
        >
          {check.isPending && <Spinner />}
          {t("panelUpgrade.check")}
        </Button>
        {check.error != null && <ErrorDisplay error={check.error} compact />}

        {found && !upgrade.isSuccess && (
          <div className="space-y-2">
            {found.state === "available" ? (
              <>
                <p className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="font-medium">
                    {t("panelUpgrade.available", { version: latest })}
                  </span>
                  {found.release_url && (
                    <a
                      href={found.release_url}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="inline-flex items-center gap-1 text-xs text-primary underline-offset-4 hover:underline"
                    >
                      {t("panelUpgrade.releaseNotes")}
                      <ExternalLinkIcon className="size-3" />
                    </a>
                  )}
                </p>
                <Button
                  size="sm"
                  disabled={upgrade.isPending}
                  onClick={() =>
                    void confirm({
                      title: t("panelUpgrade.confirmTitle", { version: latest }),
                      description: t("panelUpgrade.confirm"),
                      confirmLabel: t("panelUpgrade.upgradeTo", { version: latest }),
                    }).then((yes) => {
                      if (yes) upgrade.mutate(latest)
                    })
                  }
                >
                  {upgrade.isPending && <Spinner />}
                  {t("panelUpgrade.upgradeTo", { version: latest })}
                </Button>
              </>
            ) : found.state === "up_to_date" ? (
              <p className="flex items-center gap-1.5 text-success">
                <CheckCircle2Icon className="size-4" />
                {t("panelUpgrade.upToDate")}
              </p>
            ) : (
              <p className="text-muted-foreground">
                {t(`panelUpgrade.${found.state === "no_release" ? "noRelease" : found.state}`, {
                  version: latest,
                })}
              </p>
            )}
          </div>
        )}
        {upgrade.error != null && <ErrorDisplay error={upgrade.error} />}

        {upgrade.data && (
          <Alert variant="info">
            <AlertDescription className="space-y-2">
              <p>{t("panelUpgrade.started")}</p>
              <p className="text-xs">
                {t("panelUpgrade.copiedTo", { path: upgrade.data.snapshot })}
              </p>
              <p className="text-xs">{t("panelUpgrade.goBack")}</p>
              <pre className="overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
                {upgrade.data.rollback.join("\n")}
              </pre>
            </AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  )
}
