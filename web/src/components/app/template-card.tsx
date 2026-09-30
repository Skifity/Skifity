import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ArrowUpCircleIcon, LayoutTemplateIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { ApiError, api } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App } from "@/lib/types"

type TemplateView = {
  from_template: boolean
  template_id?: string
  service?: string
  installed_image?: string
  update_status?: "" | "backing_up" | "failed"
  update_to?: string
  update_error?: string
  name?: string
  current_image?: string
  latest_image?: string
  update_available: boolean
  changed_by_hand: boolean
}

/**
 * Where the app came from, and the template's newer version when an upgrade of
 * the panel brought one. The update backs up the app's disks and databases
 * first and deploys only when every backup succeeded.
 */
export function TemplateCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const view = useQuery({
    queryKey: ["app-template", app.id],
    queryFn: () => api.get<TemplateView>(`/api/apps/${app.id}/template`),
    // While backups run, the answer changes without anybody doing anything.
    refetchInterval: (query) => (query.state.data?.update_status === "backing_up" ? 5000 : false),
  })
  const update = useMutation({
    mutationFn: (options: { force?: boolean; skip_backup?: boolean }) =>
      api.post(`/api/apps/${app.id}/template/update`, options),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["app-template", app.id] })
      void queryClient.invalidateQueries({ queryKey: ["app", app.id] })
      void queryClient.invalidateQueries({ queryKey: ["deployments", app.id] })
    },
  })

  const data = view.data
  if (!data?.from_template) return null
  const backingUp = data.update_status === "backing_up"
  // The one refusal that has a button of its own: nowhere to back up to.
  const needsBackups =
    update.error instanceof ApiError && update.error.problem.code === "template.needs_backups"

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <LayoutTemplateIcon className="size-4" />
          {t("apps.template.title")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <p>
          {t("apps.template.from", { name: data.name ?? data.template_id, service: data.service })}{" "}
          <span className="text-muted-foreground">
            {t("apps.template.version", { image: data.current_image })}
          </span>
        </p>

        {backingUp && (
          <p className="flex items-center gap-2 text-muted-foreground">
            <Spinner />
            {t("apps.template.backingUp", { image: data.update_to })}
          </p>
        )}
        {data.update_status === "failed" && (
          <Alert variant="destructive">
            <AlertDescription>
              {t("apps.template.failed", { image: data.update_to, reason: data.update_error })}
            </AlertDescription>
          </Alert>
        )}

        {data.update_available && !backingUp ? (
          <div className="space-y-2 rounded-lg border p-3">
            <p className="font-medium">
              {t("apps.template.available", { image: data.latest_image })}
            </p>
            <p className="text-xs text-muted-foreground">{t("apps.template.backupFirst")}</p>
            {data.changed_by_hand && (
              <p className="text-xs text-muted-foreground">
                {t("apps.template.changedByHand", { image: data.installed_image })}
              </p>
            )}
            {update.error != null && <ErrorDisplay error={update.error} compact />}
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                disabled={update.isPending}
                onClick={() => update.mutate({ force: data.changed_by_hand })}
              >
                {update.isPending ? <Spinner /> : <ArrowUpCircleIcon />}
                {data.changed_by_hand ? t("apps.template.updateAnyway") : t("apps.template.update")}
              </Button>
              {needsBackups && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={update.isPending}
                  onClick={() => update.mutate({ force: data.changed_by_hand, skip_backup: true })}
                >
                  {t("apps.template.updateWithoutBackup")}
                </Button>
              )}
            </div>
            {update.isSuccess && (
              <p className="text-xs text-muted-foreground">{t("apps.template.started")}</p>
            )}
          </div>
        ) : (
          !backingUp && (
            <p className="text-xs text-muted-foreground">{t("apps.template.upToDate")}</p>
          )
        )}
      </CardContent>
    </Card>
  )
}
