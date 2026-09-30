import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CircleDotIcon, GitCommitHorizontalIcon, RocketIcon, RotateCcwIcon, XIcon } from "lucide-react"
import { cn } from "cn"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { StatusBadge } from "@/components/status-badge"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Card, CardContent } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { useEvents } from "@/hooks/use-events"
import { api, type List } from "@/lib/api"
import { formatDuration, formatRelative, shortCommit } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, Deployment, LogLine } from "@/lib/types"

const TERMINAL = new Set(["succeeded", "failed", "cancelled", "superseded"])

export function DeploymentsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  // null means "nothing chosen yet", which is not the same as "chosen nothing":
  // a running deployment opens itself until the user closes it.
  const [opened, setOpened] = useState<string | null>(null)
  const [touched, setTouched] = useState(false)

  const deployments = useQuery({
    queryKey: ["deployments", app.id],
    queryFn: () => api.get<List<Deployment>>(`/api/apps/${app.id}/deployments`),
  })

  const items = deployments.data?.items ?? []
  const running = items.find((deployment) => !TERMINAL.has(deployment.status))
  // Which version is actually serving. A rollback is a new deployment carrying
  // an old image, so the newest succeeded one is always the live one — but
  // nothing on this list said so, and after a rollback the row people assume
  // is live (the highest number) is a superseded build. Derived during render
  // from the list itself: there is no second source to fall out of step with.
  const live = items.find((deployment) => deployment.status === "succeeded")?.id

  // A running deployment opens itself: that is what the user came to look at.
  const selected = touched ? opened : (running?.id ?? null)
  const setSelected = (id: string | null) => {
    setTouched(true)
    setOpened(id)
  }

  const rollback = useMutation({
    mutationFn: (deploymentID: string) => api.post(`/api/apps/${app.id}/rollback/${deploymentID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["deployments", app.id] }),
  })

  const cancel = useMutation({
    mutationFn: (deploymentID: string) =>
      api.post(`/api/apps/${app.id}/deployments/${deploymentID}/cancel`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["deployments", app.id] }),
  })

  if (deployments.isLoading) return <Skeleton className="h-64" />
  if (deployments.error) {
    return <ErrorDisplay error={deployments.error} onRetry={() => void deployments.refetch()} />
  }
  if (items.length === 0) {
    return (
      <EmptyState
        icon={RocketIcon}
        title={t("deploy.noDeployments")}
        description={t("deploy.noDeploymentsHelp")}
        action={<DeployButton appId={app.id} />}
      />
    )
  }

  return (
    <div className="space-y-3">
      {rollback.error && <ErrorDisplay error={rollback.error} />}
      {cancel.error && <ErrorDisplay error={cancel.error} />}

      {items.map((deployment) => {
        const open = selected === deployment.id
        return (
          <Card key={deployment.id} className={cn(open && "border-primary/40")}>
            <CardContent className="space-y-4 py-4">
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                <button
                  type="button"
                  className="flex min-w-0 flex-1 items-center gap-3 text-left"
                  onClick={() => setSelected(open ? null : deployment.id)}
                  aria-expanded={open}
                >
                  <span className="font-mono text-sm text-muted-foreground">
                    #{deployment.number}
                  </span>
                  <StatusBadge
                    status={deployment.status}
                    label={t(`deploy.status.${deployment.status}`, {
                      defaultValue: deployment.status,
                    })}
                  />
                  {deployment.id === live && (
                    <Badge className="gap-1 border-success/30 bg-success/10 text-success">
                      <CircleDotIcon className="size-3" />
                      {t("deploy.live")}
                    </Badge>
                  )}
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {deployment.commit_message || triggerLabel(t, deployment)}
                  </span>
                </button>

                <div className="flex items-center gap-3 text-xs text-muted-foreground">
                  {deployment.commit_sha && (
                    <span className="flex items-center gap-1 font-mono">
                      <GitCommitHorizontalIcon className="size-3.5" />
                      {shortCommit(deployment.commit_sha)}
                    </span>
                  )}
                  <span>{formatRelative(deployment.created_at)}</span>
                  {deployment.started_at && (
                    <span>{formatDuration(deployment.started_at, deployment.finished_at)}</span>
                  )}
                </div>

                {TERMINAL.has(deployment.status) ? (
                  // Not on the version that is already serving: rolling back to
                  // what is running is a deployment that changes nothing, and a
                  // button offering it invites the question of what it would do.
                  deployment.status === "succeeded" &&
                  deployment.id !== live &&
                  (deployment.can_rollback === false ? (
                    // Said rather than hidden. A button that is simply absent
                    // on old versions reads as a bug; this says why, and the
                    // answer — deploy that commit again — is a real one.
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span className="text-xs text-muted-foreground">
                          {t("deploy.imageCollected")}
                        </span>
                      </TooltipTrigger>
                      <TooltipContent>{t("deploy.imageCollectedHelp")}</TooltipContent>
                    </Tooltip>
                  ) : (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={rollback.isPending}
                      onClick={() => rollback.mutate(deployment.id)}
                    >
                      <RotateCcwIcon className="size-3.5" />
                      {t("deploy.rollback")}
                    </Button>
                  ))
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={cancel.isPending}
                    onClick={() => cancel.mutate(deployment.id)}
                  >
                    <XIcon className="size-3.5" />
                    {t("common.cancel")}
                  </Button>
                )}
              </div>

              {deployment.error_message && (
                <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
                  <p className="font-medium">{deployment.error_message}</p>
                  {deployment.error_hint && (
                    <p className="mt-1 text-muted-foreground">{deployment.error_hint}</p>
                  )}
                </div>
              )}

              {open && <BuildLog appId={app.id} deployment={deployment} />}
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

/**
 * What started this deployment, in the reader's language.
 *
 * Every trigger the panel stores has a key here. The list used to fold four of
 * them — create, template, preview and rollback — into "a person", because it
 * matched three strings and sent everything else to the default, and a rollback
 * did not match any of them: its trigger was the English sentence
 * "rollback to #3", which no language but English ever showed.
 */
function triggerLabel(t: TFunction, deployment: Deployment): string {
  if (deployment.trigger === "rollback" && deployment.rollback_of) {
    return t("deploy.triggerRollbackTo", { number: deployment.rollback_of })
  }
  const known = ["manual", "create", "template", "push", "preview", "rollback"]
  if (known.includes(deployment.trigger)) return t(`deploy.trigger.${deployment.trigger}`)
  return deployment.trigger
}

/**
 * The build log.
 *
 * Lines already stored are fetched once, and everything after that arrives on
 * the event stream. Reloading the page mid-build therefore shows the whole log,
 * not just what happened after the reload.
 */
function BuildLog({ appId, deployment }: { appId: string; deployment: Deployment }) {
  const { t } = useTranslation()
  const [live, setLive] = useState<string[]>([])
  const bottom = useRef<HTMLDivElement>(null)
  const finished = TERMINAL.has(deployment.status)

  const stored = useQuery({
    queryKey: ["build-log", deployment.id],
    queryFn: () =>
      api.get<{ lines: LogLine[]; finished: boolean }>(
        `/api/apps/${appId}/deployments/${deployment.id}/logs`,
      ),
  })

  useEvents(
    [`deployment:${deployment.id}`],
    {
      log: (data) => setLive((previous) => [...previous, (data as { line: string }).line]),
      deployment: () => {
        void queryClient.invalidateQueries({ queryKey: ["deployments", appId] })
      },
    },
    !finished,
  )

  const lines = [...(stored.data?.lines ?? []).map((entry) => entry.line), ...live]

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "nearest" })
  }, [lines.length])

  if (stored.isLoading) return <Skeleton className="h-40" />
  if (lines.length === 0) {
    return <p className="text-sm text-muted-foreground">{t("deploy.reusedImage")}</p>
  }

  return (
    <div className="space-y-2">
      <p className="text-xs font-medium text-muted-foreground">{t("deploy.buildLog")}</p>
      <ScrollArea className="h-80 rounded-md border bg-muted/30">
        <pre className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {lines.join("\n")}
          <div ref={bottom} />
        </pre>
      </ScrollArea>
    </div>
  )
}

export function DeployButton({ appId, force }: { appId: string; force?: boolean }) {
  const { t } = useTranslation()

  const deploy = useMutation({
    mutationFn: () => api.post<Deployment>(`/api/apps/${appId}/deploy`, { force: force ?? false }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["deployments", appId] })
      void queryClient.invalidateQueries({ queryKey: ["app-status", appId] })
    },
  })

  return (
    <div className="space-y-2">
      <Button onClick={() => deploy.mutate()} disabled={deploy.isPending}>
        <RocketIcon className="size-4" />
        {deploy.isPending && <Spinner />}
        {deploy.isPending ? t("apps.deploying") : t("deploy.deployNow")}
      </Button>
      {deploy.error != null && <ErrorDisplay error={deploy.error} compact />}
    </div>
  )
}
