import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { useQuery } from "@tanstack/react-query"
import { CircleAlertIcon, LoaderCircleIcon, ScrollTextIcon } from "lucide-react"
import { toast } from "sonner"

import { deploymentLink, TERMINAL, triggerLabel } from "@/components/app/deployments-tab"
import { recordedTitle } from "@/components/app/recorded-title"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatRelative, shortCommit } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { AppStatus, Deployment, Member } from "@/lib/types"

/**
 * Who a deployment is said to be by: the member who started it, or else the
 * author of the commit it built. A push is started by the panel's webhook,
 * which is nobody, and the commit's author is who sent it.
 */
function useDeployedBy(deployment?: Deployment): string {
  const { team } = useSession()
  const byPerson = Boolean(deployment?.created_by) && deployment?.created_by !== "webhook"
  // The settings page asks for the same list under the same key, so a panel
  // that has been there has it already.
  const members = useQuery({
    queryKey: ["members", team?.id],
    queryFn: () => api.get<List<Member>>(`/api/teams/${team!.id}/members`),
    enabled: Boolean(team) && byPerson,
  })
  const member = members.data?.items.find((item) => item.user.id === deployment?.created_by)
  return member?.user.name || member?.user.email || deployment?.commit_author || ""
}

/**
 * The version that is serving, on the app's Overview: its number, its commit,
 * how it came to be deployed, by whom and when. The cards beside it said how
 * many instances were up and which image they ran, and not which deployment
 * that was — which, after a rollback or during the next build, is the
 * question.
 */
export function LiveVersionCard({ live, loading }: { live?: Deployment; loading: boolean }) {
  const { t } = useTranslation()
  const who = useDeployedBy(live)
  const when = live ? formatRelative(live.finished_at ?? live.created_at) : ""

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-xs font-medium text-muted-foreground">
          {t("apps.liveVersion")}
        </CardTitle>
      </CardHeader>
      <CardContent className="min-w-0 space-y-0.5 text-sm">
        {live ? (
          <Link
            to={deploymentLink(live.id)}
            className="block min-w-0 rounded-sm outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
          >
            <span className="block truncate font-mono">
              #{live.number}
              {live.commit_sha && (
                <span className="text-muted-foreground"> · {shortCommit(live.commit_sha)}</span>
              )}
            </span>
            <span className="block truncate">{live.commit_message || triggerLabel(t, live)}</span>
            <span className="block truncate text-xs text-muted-foreground">
              {who ? t("apps.wentLiveBy", { when, who }) : t("apps.wentLive", { when })}
            </span>
          </Link>
        ) : (
          <span className="text-muted-foreground">{loading ? "…" : t("apps.liveNone")}</span>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * What the newest deployment is doing, when that is the thing to know: one
 * under way, or one that failed.
 *
 * During an app's first build its Overview said nothing was running and to
 * press Deploy now — while a build was running, a tab away. A deployment that
 * failed before anything ever went live was only on that other tab too.
 */
export function DeploymentNotice({ newest, live }: { newest?: Deployment; live?: Deployment }) {
  const { t } = useTranslation()
  if (!newest) return null

  const view = (
    <Button variant="outline" size="sm" asChild>
      <Link to={deploymentLink(newest.id)}>
        <ScrollTextIcon />
        {t("apps.viewBuildLog")}
      </Link>
    </Button>
  )

  if (!TERMINAL.has(newest.status)) {
    return (
      <Alert variant="info">
        <LoaderCircleIcon className="animate-spin" />
        <AlertTitle>
          {t(`apps.deploymentUnderway.${newest.status}`, { number: newest.number })}
        </AlertTitle>
        <AlertDescription className="space-y-3">
          <p>
            {live
              ? t("apps.deploymentUnderwayLive", { number: live.number })
              : t("apps.deploymentUnderwayFirst")}
          </p>
          {view}
        </AlertDescription>
      </Alert>
    )
  }

  if (newest.status !== "failed") return null
  return (
    // A failure with an older version still serving is a warning: the app is
    // up, only the change did not reach it.
    <Alert variant={live ? "warning" : "destructive"}>
      <CircleAlertIcon />
      <AlertTitle>{t("apps.deploymentFailed", { number: newest.number })}</AlertTitle>
      <AlertDescription className="space-y-3">
        {(newest.error_code || newest.error_message) && (
          <p className="font-medium">
            {newest.error_code
              ? recordedTitle(t, newest.error_code, newest.error_message ?? "")
              : newest.error_message}
          </p>
        )}
        {newest.error_hint && <p>{newest.error_hint}</p>}
        {live && <p>{t("apps.deploymentFailedLive", { number: live.number })}</p>}
        {view}
      </AlertDescription>
    </Alert>
  )
}

/**
 * Says so when the first version of an app goes live while somebody is
 * watching it build, with its address to open.
 *
 * Called from the deployment event, with what the page knew before it: the
 * moment is the event, not anything the page holds, so nothing is copied into
 * state to notice it. `announced` keeps one deployment from being said twice,
 * since a deployment is published again as its last steps finish.
 */
export function announceFirstLive(
  t: TFunction,
  appId: string,
  appName: string,
  changed: Deployment,
  announced: Set<string>,
) {
  if (changed.app_id !== appId || changed.status !== "succeeded") return
  if (announced.has(changed.id)) return
  const before = queryClient.getQueryData<List<Deployment>>(["deployments", appId])
  // Not knowing what came before is not knowing it is the first.
  if (!before) return
  if (before.items.some((item) => item.status === "succeeded" && item.id !== changed.id)) return
  announced.add(changed.id)

  // The address is worked out as the deployment is applied, so the status
  // read before it may not have one yet.
  void queryClient.invalidateQueries({ queryKey: ["app-status", appId] }).then(() => {
    const url = queryClient.getQueryData<AppStatus>(["app-status", appId])?.urls?.[0]
    toast.success(t("apps.nowLive", { name: appName }), {
      id: `live-${changed.id}`,
      description: url
        ? url.replace(/^https?:\/\//, "")
        : t("apps.nowLiveNumber", { number: changed.number }),
      duration: 15000,
      action: url
        ? {
            label: t("apps.openApp"),
            onClick: () => window.open(url, "_blank", "noopener,noreferrer"),
          }
        : undefined,
    })
  })
}
