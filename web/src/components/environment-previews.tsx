import { useTranslation } from "react-i18next"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { GitPullRequestIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Card, CardContent } from "@/components/ui/card"
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api"
import type { Environment } from "@/lib/types"

/**
 * Whether a pull request's preview copies this whole environment, or only the
 * apps its repository builds. Beside the confinement level, because it is the
 * other thing that belongs to one environment rather than to one app.
 */
export function EnvironmentPreviews({ environment }: { environment: Environment }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const change = useMutation({
    mutationFn: (on: boolean) =>
      api.patch<Environment>(`/api/environments/${environment.id}`, { preview_stack: on }),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ["environments", environment.project_id] }),
  })

  // A preview has no previews of its own.
  if (environment.kind === "preview") return null

  return (
    <Card>
      <CardContent className="flex flex-wrap items-center gap-4 py-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <GitPullRequestIcon className="size-4 shrink-0 text-muted-foreground" />
            <span className="text-sm font-medium">{t("environments.previewStack")}</span>
          </div>
          <p className="mt-0.5 text-sm text-muted-foreground">
            {t("environments.previewStackHelp")}
          </p>
        </div>
        <Switch
          aria-label={t("environments.previewStack")}
          checked={change.isPending ? change.variables : environment.preview_stack}
          disabled={change.isPending}
          onCheckedChange={(on) => change.mutate(on)}
        />
      </CardContent>
      {change.error != null && (
        <CardContent className="pt-0">
          <ErrorDisplay error={change.error} />
        </CardContent>
      )}
    </Card>
  )
}
