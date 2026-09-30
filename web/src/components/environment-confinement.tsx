import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ShieldCheckIcon, ShieldAlertIcon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import type { Environment, PodSecurity } from "@/lib/types"

/**
 * How strictly this environment confines its pods, and the one switch.
 *
 * It is on the project page rather than in Settings because it belongs to one
 * environment, not to the panel: a team can keep production strict and lower a
 * staging environment, and the page where they choose between environments is
 * the page where they should see which is which.
 *
 * The strict level is shown as a quiet line and the lowered one as a warning
 * badge. An environment nobody has touched should not look like a decision
 * anybody has to make; one that has been lowered should never be forgettable.
 */
export function EnvironmentConfinement({ environment }: { environment: Environment }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const [error, setError] = useState<unknown>(null)

  const change = useMutation({
    mutationFn: (level: PodSecurity) =>
      api.patch<Environment>(`/api/environments/${environment.id}`, { pod_security: level }),
    onSuccess: () => {
      setError(null)
      void queryClient.invalidateQueries({ queryKey: ["environments", environment.project_id] })
    },
    onError: (err) => setError(err),
  })

  const lowered = environment.pod_security === "baseline"

  async function onToggle() {
    if (lowered) {
      change.mutate("restricted")
      return
    }
    // Lowering is the direction that gives something up, so it is the direction
    // that asks. Raising it back is not a question: the worst it does is stop
    // an app that was already running, which the confirmation would not change.
    const ok = await confirm({
      title: t("environments.confinementLowerTitle"),
      description: t("environments.confinementLowerConfirm"),
      confirmLabel: t("environments.confinementLower"),
      destructive: true,
    })
    if (ok) change.mutate("baseline")
  }

  return (
    <Card>
      <CardContent className="flex flex-wrap items-center gap-4 py-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {lowered ? (
              <ShieldAlertIcon className="size-4 shrink-0 text-warning" />
            ) : (
              <ShieldCheckIcon className="size-4 shrink-0 text-muted-foreground" />
            )}
            <span className="text-sm font-medium">{t("environments.confinement")}</span>
            <Badge variant="outline" className="text-[10px]">
              {t(`environments.podSecurity.${environment.pod_security}`)}
            </Badge>
          </div>
          <p className="mt-0.5 text-sm text-muted-foreground">
            {lowered
              ? t("environments.confinementBaselineHelp")
              : t("environments.confinementRestrictedHelp")}
          </p>
        </div>

        {/* Shown to everyone, as every other admin-only control in the panel
            is: the API refuses a member, with a message saying so. A button
            that is missing teaches nobody why. */}
        <Button
          variant="outline"
          size="sm"
          disabled={change.isPending}
          onClick={() => void onToggle()}
        >
          {change.isPending && <Spinner className="size-4" />}
          {lowered ? t("environments.confinementRaise") : t("environments.confinementLower")}
        </Button>
      </CardContent>
      {error != null && (
        <CardContent className="pt-0">
          <ErrorDisplay error={error} />
        </CardContent>
      )}
    </Card>
  )
}
