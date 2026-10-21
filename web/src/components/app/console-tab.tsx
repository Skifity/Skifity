import { useState } from "react"
import { useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { skipToken, useIsMutating, useMutation, useQuery } from "@tanstack/react-query"
import { ClockIcon, PlayIcon, PlusIcon, TerminalIcon, Trash2Icon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ScheduleField, scheduleWords } from "@/components/schedule-field"
import { useStickToBottom } from "@/components/app/stick-to-bottom"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle,
} from "@/components/ui/item"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ApiError, api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, AppJob } from "@/lib/types"

/** A run's output, and how it ended once it has. */
type RunOutput = { lines: string[]; finished: boolean; exit_code?: number }

/** A run to start: what to show it as, and the run on screen when it was. */
type StartRun = { label: string; start: () => Promise<{ run: string }>; after: string | null }

/** The run this page started last, and the one that was on screen then. */
type StartedRun = { run: string; after: string | null }

/** The search parameter naming the run the console shows; see ConsoleTab. */
export const RUN_PARAM = "run"

/**
 * One-off commands.
 *
 * A migration, a backfill, a look at the data. It runs in the app's own image
 * with the app's own variables, as a job of its own rather than a shell into a
 * running instance — which matters because the moment you most need this is
 * when the app will not start, and then there is nothing to attach to.
 */
export function ConsoleTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [command, setCommand] = useState("")
  const [params, setParams] = useSearchParams()

  // What to run: a command typed here, or a scheduled one run now. Both are
  // one-off runs whose output is followed the same way.
  //
  // Keyed, so a tab opened again while a run is being started knows it is:
  // the new tab's own mutation was idle, Run was enabled, and the second press
  // started the command twice. What is done here goes on after the tab is
  // closed, so it only fills the cache; the address is changed by the tab
  // that is open (see run below), because a stale copy of it, applied after
  // the console was left, put the user back on the console.
  const start = useMutation({
    mutationKey: ["run-start", app.id],
    mutationFn: (what: StartRun) => what.start(),
    onSuccess: (started, what) => {
      queryClient.setQueryData(["run", app.id, started.run, "command"], what.label)
      queryClient.setQueryData<StartedRun>(["run-started", app.id], {
        run: started.run,
        after: what.after,
      })
    },
  })
  const starting = useIsMutating({ mutationKey: ["run-start", app.id] }) > 0
  const recorded = useQuery<StartedRun>({
    queryKey: ["run-started", app.id],
    queryFn: skipToken,
    staleTime: Infinity,
    gcTime: Infinity,
  })

  // Which run is on screen, in the address rather than in this component. A
  // tab is unmounted when another is opened, and a run only this component
  // knew about was forgotten with it: the output gone, Run enabled again, and
  // the migration still going on the server started a second time. In the
  // address it outlives the tab, a reload, and a proxy that cut the request.
  //
  // A run started from this page replaces the one that was on screen when it
  // was started, whether or not the address heard about it: the tab may have
  // been left while it was being started, and then only the cache did. An
  // address naming any other run is one somebody opened on purpose.
  const inAddress = params.get(RUN_PARAM)
  const latest = recorded.data
  const runName =
    latest && (inAddress === null || inAddress === latest.after) ? latest.run : inAddress

  const run = (label: string, post: () => Promise<{ run: string }>) =>
    start.mutate(
      { label, start: post, after: runName },
      {
        // Called only while this tab is still open, which is when the address
        // is still the console's.
        onSuccess: (started) =>
          setParams(
            (previous) => {
              const next = new URLSearchParams(previous)
              next.set(RUN_PARAM, started.run)
              return next
            },
            { replace: true },
          ),
      },
    )

  // Followed to the end. Without it the panel answered with whatever the
  // container had printed when its pod was first seen running — usually
  // nothing — and this said the command had finished while it ran on. A query,
  // so a tab opened again joins the request still waiting rather than losing it.
  const followed = useQuery({
    queryKey: ["run", app.id, runName, "followed"],
    queryFn: runName
      ? () => api.get<RunOutput>(`/api/apps/${app.id}/runs/${runName}/logs?follow=true`)
      : skipToken,
    staleTime: Infinity,
    retry: false,
  })
  const following = runName !== null && followed.isFetching
  // A followed request that failed — a proxy that cuts long requests — says
  // nothing about the command, which is most likely still going.
  const cut = runName !== null && followed.isError
  // What it has printed so far, while the followed request waits for the end,
  // and whether it has ended, once that request is gone.
  const soFar = useQuery({
    queryKey: ["run", app.id, runName, "so-far"],
    queryFn:
      runName && (following || cut)
        ? () => api.get<RunOutput>(`/api/apps/${app.id}/runs/${runName}/logs`)
        : skipToken,
    refetchInterval: (query) => (query.state.data?.finished ? false : 5_000),
    retry: false,
  })
  // Ended, or gone: a run the panel no longer has is not one still going.
  const ended =
    soFar.data?.finished === true || (soFar.error instanceof ApiError && soFar.error.status === 404)
  // The command as it was typed, for as long as the page is open. Not in the
  // address: a command can carry a password, and the address is kept in the
  // browser's history.
  const ran = useQuery<string>({
    queryKey: ["run", app.id, runName, "command"],
    queryFn: skipToken,
    staleTime: Infinity,
    gcTime: Infinity,
  })

  const result = followed.data ?? soFar.data
  // Not while a run is being started or is going, as far as anything here can
  // tell: enabling Run when the followed request was cut was how a migration
  // came to run twice.
  const busy = starting || following || (cut && !ended)
  const stick = useStickToBottom(result?.lines)

  return (
    <div className="space-y-6">
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (busy) return
          run(command.trim(), () =>
            api.post<{ run: string }>(`/api/apps/${app.id}/run`, { command: command.trim() }),
          )
        }}
      >
        <Field>
          <FieldLabel htmlFor="console-command">{t("apps.runCommand")}</FieldLabel>
          <div className="flex flex-wrap gap-2">
            <Input
              id="console-command"
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              placeholder="npm run migrate"
              className="min-w-64 flex-1 font-mono"
              autoComplete="off"
              disabled={busy}
            />
            {/* Not while a run is going: the second press was how a migration
                came to run twice. */}
            <Button type="submit" disabled={!command.trim() || busy}>
              {busy ? <Spinner /> : <PlayIcon />}
              {busy ? t("apps.running") : t("apps.run")}
            </Button>
          </div>
          <FieldDescription>{t("apps.runCommandHelp")}</FieldDescription>
        </Field>
      </form>

      {start.error != null && <ErrorDisplay error={start.error} />}

      {busy && (
        <Alert>
          <Spinner />
          <AlertDescription>{t("apps.runningHelp")}</AlertDescription>
        </Alert>
      )}

      {runName === null ? (
        !starting && (
          // What this panel is for is output, so that is what its empty state
          // says. It used to repeat the field's label and its help text word
          // for word, sixty pixels below them.
          <EmptyState
            icon={TerminalIcon}
            title={t("apps.noOutputYet")}
            description={t("apps.noOutputYetHelp")}
          />
        )
      ) : (
        <div className="space-y-2">
          {/* The run's own name as well as the command: it is what the CLI
              and the API read the output by, if this page loses it. */}
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-xs text-muted-foreground">
            {ran.data && <span className="font-mono">$ {ran.data}</span>}
            <span>{t("apps.runName", { name: runName })}</span>
          </div>
          {followed.error != null && (
            <ErrorDisplay error={followed.error} compact onRetry={() => void followed.refetch()} />
          )}
          {result?.finished && result.exit_code !== 0 && (
            <Alert variant="destructive">
              <AlertDescription>
                {t("apps.runExitStatus", { code: result.exit_code })}
              </AlertDescription>
            </Alert>
          )}
          {followed.data && !followed.data.finished && (
            <Alert>
              <AlertDescription>{t("apps.runOutcomeUnknown")}</AlertDescription>
            </Alert>
          )}
          {result ? (
            <ScrollArea className="h-80 rounded-md border bg-muted/30" {...stick}>
              <pre className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
                {result.lines.length > 0
                  ? result.lines.join("\n")
                  : (followed.data || result.finished) && t("apps.runNoOutput")}
              </pre>
            </ScrollArea>
          ) : (
            busy && <Skeleton className="h-80" />
          )}
        </div>
      )}

      <ScheduledCommands
        app={app}
        running={busy}
        onRunNow={(job) =>
          run(job.command, () =>
            api.post<{ run: string }>(`/api/apps/${app.id}/jobs/${job.id}/run`, {}),
          )
        }
      />
    </div>
  )
}

/**
 * Commands that run on a schedule.
 *
 * Kubernetes does the scheduling, not the panel: a panel that is restarting at
 * three in the morning should not be why a nightly job did not run. They are
 * applied with the app, so a schedule always runs the version that is deployed.
 */
function ScheduledCommands({
  app,
  running,
  onRunNow,
}: {
  app: App
  running: boolean
  onRunNow: (job: AppJob) => void
}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState("")
  const [schedule, setSchedule] = useState("0 3 * * *")
  const [command, setCommand] = useState("")

  const jobs = useQuery({
    queryKey: ["app-jobs", app.id],
    queryFn: () => api.get<List<AppJob>>(`/api/apps/${app.id}/jobs`),
  })

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["app-jobs", app.id] })

  const create = useMutation({
    mutationFn: () =>
      api.post(`/api/apps/${app.id}/jobs`, {
        name: name.trim(),
        schedule: schedule.trim(),
        command: command.trim(),
      }),
    onSuccess: () => {
      refresh()
      setAdding(false)
      setName("")
      setCommand("")
    },
  })

  const toggle = useMutation({
    mutationFn: (job: AppJob) =>
      api.patch(`/api/apps/${app.id}/jobs/${job.id}`, {
        name: job.name,
        schedule: job.schedule,
        command: job.command,
        enabled: !job.enabled,
      }),
    onSuccess: refresh,
  })

  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/api/apps/${app.id}/jobs/${id}`),
    onSuccess: refresh,
  })

  const items = jobs.data?.items ?? []

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ClockIcon className="size-4" />
          {t("apps.scheduledCommands")}
        </CardTitle>
        {!adding && (
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            <PlusIcon />
            {t("common.add")}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {adding && (
          <form
            onSubmit={(event) => {
              event.preventDefault()
              create.mutate()
            }}
          >
            <FieldGroup>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="job-name">{t("common.name")}</FieldLabel>
                  <Input
                    id="job-name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder={t("apps.scheduledNamePlaceholder")}
                    required
                    autoFocus
                  />
                </Field>
                <ScheduleField
                  id="job-schedule"
                  value={schedule}
                  onChange={setSchedule}
                  description={t("apps.scheduleHelp")}
                />
              </div>
              <Field>
                <FieldLabel htmlFor="job-command">{t("apps.runCommand")}</FieldLabel>
                <Input
                  id="job-command"
                  value={command}
                  onChange={(event) => setCommand(event.target.value)}
                  placeholder="npm run send-digest"
                  className="font-mono"
                  required
                />
              </Field>
              {create.error != null && <ErrorDisplay error={create.error} compact />}
              <div className="flex justify-end gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    // The next Add starts clean, not with this one's refusal.
                    create.reset()
                    setAdding(false)
                  }}
                >
                  {t("common.cancel")}
                </Button>
                <Button type="submit" disabled={create.isPending}>
                  {create.isPending && <Spinner />}
                  {t("common.add")}
                </Button>
              </div>
            </FieldGroup>
          </form>
        )}

        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
        {/* A switch the panel refused snaps back, and without this it said
            nothing about why: switching one needs an admin. */}
        {toggle.error != null && <ErrorDisplay error={toggle.error} compact />}

        {/* Loading and failing are not "nothing scheduled", which is what
            both used to say, and which invites adding the job a second time. */}
        {jobs.isLoading ? (
          <Skeleton className="h-16" />
        ) : jobs.error != null ? (
          <ErrorDisplay error={jobs.error} compact onRetry={() => void jobs.refetch()} />
        ) : items.length === 0 && !adding ? (
          <EmptyState
            bordered={false}
            icon={ClockIcon}
            title={t("apps.nothingScheduled")}
            description={t("apps.scheduledCommandsHelp")}
          />
        ) : (
          <ItemGroup>
            {items.map((job) => (
              <Item key={job.id} variant="outline" className="mb-2">
                <ItemContent>
                  <ItemTitle>{job.name}</ItemTitle>
                  <ItemDescription>
                    {/* The words when it is one of the four, the expression
                        when it is not: a row reading "0 3 * * *" asks the
                        reader to parse cron to find out when their job runs. */}
                    <span>{scheduleWords(t, job.schedule)}</span>
                    <span className="mx-1">·</span>
                    <span className="font-mono">{job.command}</span>
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={running}
                    onClick={() => onRunNow(job)}
                    aria-label={t("apps.runNowNamed", { name: job.name })}
                  >
                    <PlayIcon className="size-4" />
                    <span className="hidden sm:inline">{t("apps.runNow")}</span>
                  </Button>
                  <Switch
                    checked={job.enabled}
                    disabled={toggle.isPending}
                    onCheckedChange={() => toggle.mutate(job)}
                    aria-label={t("apps.scheduledEnabledNamed", { name: job.name })}
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("common.removeNamed", { name: job.name })}
                    onClick={() => {
                      // No typing the name for this one: a schedule can be
                      // added back in a few seconds, unlike an app or a volume.
                      void confirm({
                        title: t("common.deleteNamed", { name: job.name }),
                        description: t("apps.scheduledCommandsHelp"),
                        confirmLabel: t("common.delete"),
                        destructive: true,
                      }).then((yes) => {
                        if (yes) remove.mutate(job.id)
                      })
                    }}
                  >
                    <Trash2Icon className="size-4 text-muted-foreground" />
                  </Button>
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
      </CardContent>
    </Card>
  )
}
