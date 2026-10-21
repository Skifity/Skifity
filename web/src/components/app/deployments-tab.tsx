import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArrowUpRightIcon,
  CircleDotIcon,
  GitCommitHorizontalIcon,
  RocketIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"
import { cn } from "cn"

import { DeployAnywayButton } from "@/components/app/security-tab"
import { useStickToBottom } from "@/components/app/stick-to-bottom"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { StatusBadge } from "@/components/status-badge"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Card, CardContent } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { ApiError, api, type List } from "@/lib/api"
import { formatDuration, formatRelative, shortCommit } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, Deployment, LogLine } from "@/lib/types"

const TERMINAL = new Set(["succeeded", "failed", "cancelled", "superseded"])

export function DeploymentsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const { team } = useSession()
  // null means "nothing chosen yet", which is not the same as "chosen nothing":
  // a running deployment opens itself until the user closes it.
  const [opened, setOpened] = useState<string | null>(null)
  const [touched, setTouched] = useState(false)

  const deployments = useQuery({
    queryKey: ["deployments", app.id],
    queryFn: () => api.get<List<Deployment>>(`/api/apps/${app.id}/deployments`),
    // A member limited to some projects is refused the team's event stream,
    // so a deployment of theirs that is under way is polled until it ends:
    // otherwise a row they were not watching the log of said "building" for
    // ever. Nothing is polled once every deployment has finished.
    refetchInterval: (query) =>
      team?.scoped && query.state.data?.items.some((item) => !TERMINAL.has(item.status))
        ? 5_000
        : false,
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

  // Which deployment a rollback is being considered to, while its plan is on
  // screen. Rolling back used to be one click with nothing said about what it
  // would put back and what it would leave.
  const [rollingBackTo, setRollingBackTo] = useState<Deployment | null>(null)

  // The same app in the project's other environments, which a version that
  // deployed here can be promoted to without building it again.
  const targets = useQuery({
    queryKey: ["promote-targets", app.id],
    queryFn: () => api.get<List<PromoteTarget>>(`/api/apps/${app.id}/promote`),
  })
  const promoteTargets = targets.data?.items ?? []
  const [promoting, setPromoting] = useState<Deployment | null>(null)

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
      {cancel.error && <ErrorDisplay error={cancel.error} />}
      {promoting != null && promoteTargets.length > 0 && (
        <PromoteDialog
          key={promoting.id}
          source={promoting}
          targets={promoteTargets}
          onClose={() => setPromoting(null)}
        />
      )}
      {rollingBackTo != null && (
        <RollbackDialog
          key={rollingBackTo.id}
          app={app}
          target={rollingBackTo}
          onClose={() => setRollingBackTo(null)}
        />
      )}

      {items.map((deployment) => {
        const open = selected === deployment.id
        return (
          <Card key={deployment.id} className={cn(open && "border-primary/40")}>
            <CardContent className="py-4">
              <Collapsible
                open={open}
                onOpenChange={(next) => setSelected(next ? deployment.id : null)}
                className="space-y-4"
              >
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                  <CollapsibleTrigger className="flex min-w-0 flex-1 items-center gap-3 rounded-sm text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
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
                    {deployment.accepted_vulnerabilities && (
                      <Badge
                        variant="outline"
                        className="gap-1 border-destructive/40 text-destructive"
                      >
                        <ShieldAlertIcon className="size-3" />
                        {t("deploy.acceptedVulnerabilities")}
                      </Badge>
                    )}
                    <span className="min-w-0 flex-1 truncate text-sm">
                      {deployment.commit_message || triggerLabel(t, deployment)}
                    </span>
                  </CollapsibleTrigger>

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

                  {/* The version that is serving is the one most worth promoting,
                    so this is offered on it too, unlike a rollback. */}
                  {deployment.status === "succeeded" &&
                    deployment.can_rollback !== false &&
                    promoteTargets.length > 0 && (
                      <Button variant="outline" size="sm" onClick={() => setPromoting(deployment)}>
                        <ArrowUpRightIcon className="size-3.5" />
                        {t("deploy.promote")}
                      </Button>
                    )}

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
                        onClick={() => setRollingBackTo(deployment)}
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
                    {/* The headline in the reader's language, from the error's
                      code; the particulars below are the server's, which
                      carry the names and numbers a translation cannot. */}
                    {deployment.error_code && (
                      <p className="font-medium">
                        {t(`errors.catalogue.${deployment.error_code.replaceAll(".", "_")}.title`, {
                          defaultValue: deployment.error_message,
                        })}
                      </p>
                    )}
                    <p className={deployment.error_code ? "mt-1" : "font-medium"}>
                      {deployment.error_message}
                    </p>
                    {deployment.error_hint && (
                      <p className="mt-1 text-muted-foreground">{deployment.error_hint}</p>
                    )}
                    {/* Only the newest: an older one stopped the same way was
                      followed by something else, and deploying it now would
                      go back past that. */}
                    {deployment.error_code === "deploy.vulnerable" &&
                      deployment.id === items[0]?.id && (
                        <div className="mt-3">
                          <DeployAnywayButton app={app} deployment={deployment} />
                        </div>
                      )}
                  </div>
                )}

                <CollapsibleContent>
                  <BuildLog appId={app.id} deployment={deployment} />
                </CollapsibleContent>
              </Collapsible>
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

type RollbackPlan = {
  target: { number: number; commit_sha?: string; commit_message?: string; image?: string }
  current?: { number: number; commit_sha?: string; commit_message?: string } | null
  changes: { field: string; from: string; to: string }[]
  unchanged: string[]
  can_rollback: boolean
}

/**
 * What going back to a deployment would change, before it is done.
 *
 * A rollback restores that version's image and the settings it ran with, and
 * leaves the variables, the domains and the disks as they are now. That is the
 * difference between it and an undo, and it is said here rather than found out.
 */
function RollbackDialog({
  app,
  target,
  onClose,
}: {
  app: App
  target: Deployment
  onClose: () => void
}) {
  const { t } = useTranslation()
  const plan = useQuery({
    queryKey: ["rollback-plan", app.id, target.id],
    queryFn: () => api.get<RollbackPlan>(`/api/apps/${app.id}/rollback/${target.id}/plan`),
  })
  const rollback = useMutation({
    mutationFn: () => api.post(`/api/apps/${app.id}/rollback/${target.id}`),
    onSuccess: () => {
      // A rollback puts the app's settings back — port, health path, start
      // command, scaling — so what the settings and scaling forms hold is
      // stale: the next Save wrote the pre-rollback values back.
      for (const key of ["deployments", "app", "scaling", "processes"]) {
        void queryClient.invalidateQueries({ queryKey: [key, app.id] })
      }
      onClose()
    },
  })
  const version = (side?: { number: number; commit_sha?: string } | null) =>
    side
      ? `#${side.number}${side.commit_sha ? ` · ${shortCommit(side.commit_sha)}` : ""}`
      : t("deploy.rollbackNothingLive")

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("deploy.rollbackTitle", { number: target.number })}</DialogTitle>
          <DialogDescription>{t("deploy.rollbackHelp")}</DialogDescription>
        </DialogHeader>
        {plan.isLoading ? (
          <Skeleton className="h-32" />
        ) : plan.error ? (
          <ErrorDisplay error={plan.error} compact />
        ) : plan.data ? (
          <div className="space-y-3 text-sm">
            <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
              <span className="text-muted-foreground">{t("deploy.rollbackFrom")}</span>
              <span className="font-mono text-xs">{version(plan.data.current)}</span>
              <span className="text-muted-foreground">{t("deploy.rollbackTo")}</span>
              <span className="font-mono text-xs">{version(plan.data.target)}</span>
            </div>
            {plan.data.changes.length > 0 ? (
              <div className="space-y-1">
                <p className="font-medium">{t("deploy.rollbackChanges")}</p>
                <ul className="space-y-0.5 text-xs">
                  {plan.data.changes.map((change) => (
                    <li key={change.field} className="flex flex-wrap gap-x-2">
                      <span className="text-muted-foreground">
                        {t(`deploy.rollbackField.${change.field}`, { defaultValue: change.field })}
                      </span>
                      <span className="font-mono">
                        {change.from || "—"} → {change.to || "—"}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            ) : (
              <p className="text-muted-foreground">{t("deploy.rollbackNoSettings")}</p>
            )}
            <p className="text-xs text-muted-foreground">{t("deploy.rollbackUnchanged")}</p>
          </div>
        ) : null}
        {rollback.error != null && <ErrorDisplay error={rollback.error} compact />}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={rollback.isPending || !plan.data?.can_rollback}
            onClick={() => rollback.mutate()}
          >
            {rollback.isPending ? <Spinner /> : <RotateCcwIcon />}
            {t("deploy.rollback")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type PromoteTarget = {
  app_id: string
  app_name: string
  environment_id: string
  environment_name: string
}

/**
 * Promoting a version: the exact image this environment runs, deployed to the
 * same app in another, without building it again. When the other app would
 * have built it differently — its own build-time variables — that is said,
 * and running it anyway is a second, deliberate click.
 */
function PromoteDialog({
  source,
  targets,
  onClose,
}: {
  source: Deployment
  targets: PromoteTarget[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [targetID, setTargetID] = useState(targets[0].app_id)
  const target = targets.find((candidate) => candidate.app_id === targetID) ?? targets[0]
  const promote = useMutation({
    // The target travels with the request, so what the answer is about is the
    // environment it was asked of, not whichever one is selected by then.
    mutationFn: ({ to, force }: { to: PromoteTarget; force: boolean }) =>
      api.post(`/api/apps/${to.app_id}/promote`, { deployment_id: source.id, force }),
    onSuccess: (_, { to }) => {
      void queryClient.invalidateQueries({ queryKey: ["deployments", to.app_id] })
      toast.success(
        t("deploy.promoted", { number: source.number, environment: to.environment_name }),
      )
      onClose()
    },
  })
  // The build-time check is per target. A refusal from staging, still on
  // screen after the choice moved to production, turned the button into
  // "Promote anyway" and forced a deploy to an environment nobody had checked.
  // The error is cleared when the target changes, and only ever honoured for
  // the target it came back from.
  const errorIsForTarget = promote.variables?.to.app_id === target.app_id
  const builtDifferently =
    errorIsForTarget &&
    promote.error instanceof ApiError &&
    promote.error.problem.code === "promote.built_differently"

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("deploy.promoteTitle", { number: source.number })}</DialogTitle>
          <DialogDescription>{t("deploy.promoteHelp")}</DialogDescription>
        </DialogHeader>
        {targets.length > 1 && (
          <Select
            value={targetID}
            onValueChange={(next) => {
              promote.reset()
              setTargetID(next)
            }}
          >
            <SelectTrigger aria-label={t("deploy.promoteEnvironment")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {targets.map((candidate) => (
                <SelectItem key={candidate.app_id} value={candidate.app_id}>
                  {candidate.environment_name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <p className="text-sm">
          {t("deploy.promoteWhat", {
            number: source.number,
            commit: source.commit_sha ? shortCommit(source.commit_sha) : "—",
            environment: target.environment_name,
          })}
        </p>
        {promote.error != null && errorIsForTarget && (
          <ErrorDisplay error={promote.error} compact />
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          {builtDifferently ? (
            <Button
              variant="destructive"
              disabled={promote.isPending}
              onClick={() => promote.mutate({ to: target, force: true })}
            >
              {promote.isPending ? <Spinner /> : <ArrowUpRightIcon />}
              {t("deploy.promoteAnyway")}
            </Button>
          ) : (
            <Button
              disabled={promote.isPending}
              onClick={() => promote.mutate({ to: target, force: false })}
            >
              {promote.isPending ? <Spinner /> : <ArrowUpRightIcon />}
              {t("deploy.promoteTo", { environment: target.environment_name })}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
  const known = [
    "manual",
    "create",
    "template",
    "push",
    "tag",
    "preview",
    "rollback",
    "promote",
    "refresh",
  ]
  if (known.includes(deployment.trigger)) return t(`deploy.trigger.${deployment.trigger}`)
  return deployment.trigger
}

/** A line as the event stream sends it: numbered, except from an older panel. */
type StreamedLine = { seq?: number; line: string }

type StoredLog = { lines: LogLine[]; finished: boolean }

/** The most lines the server answers with at once. */
const LOG_PAGE = 5000

/**
 * Every stored line of a deployment's log. The server answers a page at a
 * time, and a build that printed more than one page used to show only its
 * first, with no sign that the rest existed.
 */
async function readBuildLog(appId: string, deploymentId: string): Promise<StoredLog> {
  const path = `/api/apps/${appId}/deployments/${deploymentId}/logs?limit=${LOG_PAGE}`
  const first = await api.get<StoredLog>(path)
  const lines = [...first.lines]
  let page = first.lines
  while (page.length === LOG_PAGE) {
    page = (await api.get<StoredLog>(`${path}&since=${page[page.length - 1].seq}`)).lines
    lines.push(...page)
  }
  return { lines, finished: first.finished }
}

/**
 * The stored lines and the streamed ones as one log, in order and each once.
 *
 * Lines are numbered from 1 without a hole, so fewer numbers than the highest
 * one means some were never seen: sent before the stream was opened, and not
 * yet stored when the log was fetched.
 */
function mergeLog(stored: LogLine[], streamed: StreamedLine[]) {
  const bySeq = new Map<number, string>()
  let highest = 0
  for (const entry of stored) {
    bySeq.set(entry.seq, entry.line)
    highest = Math.max(highest, entry.seq)
  }
  const unnumbered: string[] = []
  for (const entry of streamed) {
    if (entry.seq === undefined) {
      unnumbered.push(entry.line)
      continue
    }
    bySeq.set(entry.seq, entry.line)
    highest = Math.max(highest, entry.seq)
  }
  const ordered = [...bySeq.entries()].sort((a, b) => a[0] - b[0]).map(([, line]) => line)
  return { lines: [...ordered, ...unnumbered], gap: bySeq.size < highest }
}

/**
 * The build log.
 *
 * The stored lines are fetched, and everything after that arrives on the event
 * stream. Reloading the page mid-build therefore shows the whole log, not just
 * what happened after the reload.
 *
 * The two overlap and can leave a hole between them, because a build sends
 * each line as it comes and stores them in batches. Every line carries its
 * number, so one that came both ways is shown once, and a hole is filled by
 * reading the stored log again until the batch holding it is written.
 */
function BuildLog({ appId, deployment }: { appId: string; deployment: Deployment }) {
  const { t } = useTranslation()
  const [streamed, setStreamed] = useState<StreamedLine[]>([])
  const finished = TERMINAL.has(deployment.status)

  const stored = useQuery({
    queryKey: ["build-log", deployment.id],
    queryFn: () => readBuildLog(appId, deployment.id),
    // A finished log never changes. One still being written is read again
    // whenever it is opened, rather than served from a cache that is behind.
    staleTime: (query) => (query.state.data?.finished ? Infinity : 0),
    refetchInterval: (query) =>
      !finished && mergeLog(query.state.data?.lines ?? [], streamed).gap ? 2_000 : false,
  })

  useEvents(
    [`deployment:${deployment.id}`],
    {
      log: (data) => {
        const { seq, line } = data as StreamedLine
        setStreamed((previous) => [...previous, { seq, line }])
      },
      deployment: () => {
        void queryClient.invalidateQueries({ queryKey: ["deployments", appId] })
      },
    },
    !finished,
  )

  // It finished while open. The log that was fetched was still being written,
  // and its last batch is stored only as the build ends, so it is read again,
  // whole, and what was streamed is let go.
  const behind = finished && stored.data !== undefined && !stored.data.finished
  useEffect(() => {
    if (!behind) return
    void queryClient
      .invalidateQueries({ queryKey: ["build-log", deployment.id] })
      .then(() => setStreamed([]))
  }, [behind, deployment.id])

  const lines = useMemo(
    () => mergeLog(stored.data?.lines ?? [], streamed).lines,
    [stored.data, streamed],
  )
  const stick = useStickToBottom(lines)

  if (stored.isLoading) return <Skeleton className="h-40" />
  if (stored.error) return <ErrorDisplay error={stored.error} compact />
  if (lines.length === 0) {
    // No lines is only "nothing was built" once the deployment is over: a
    // queued one, or a build before its first line, has simply not said
    // anything yet.
    return (
      <p className="text-sm text-muted-foreground">
        {finished ? t("deploy.reusedImage") : t("deploy.noLogYet")}
      </p>
    )
  }

  return (
    <div className="space-y-2">
      <p className="text-xs font-medium text-muted-foreground">{t("deploy.buildLog")}</p>
      <ScrollArea className="h-80 rounded-md border bg-muted/30" {...stick}>
        <pre className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {lines.join("\n")}
        </pre>
      </ScrollArea>
    </div>
  )
}

export function DeployButton({ appId, force }: { appId: string; force?: boolean }) {
  const { t } = useTranslation()

  const deploy = useMutation({
    mutationFn: () => api.post<Deployment>(`/api/apps/${appId}/deploy`, { force: force ?? false }),
    onSuccess: (started) => {
      // Said, because from any tab but Deployments nothing else on screen
      // changes when a deployment starts.
      toast.success(t("deploy.started", { number: started.number }))
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
