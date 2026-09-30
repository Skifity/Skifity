import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ClockIcon, PlayIcon, PlusIcon, TerminalIcon, Trash2Icon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ScheduleField, scheduleWords } from "@/components/schedule-field"
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
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, AppJob } from "@/lib/types"

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
  const [output, setOutput] = useState<string[] | null>(null)
  const [ran, setRan] = useState("")

  const run = useMutation({
    mutationFn: async () => {
      const started = await api.post<{ run: string }>(`/api/apps/${app.id}/run`, {
        command: command.trim(),
      })
      return api.get<{ lines: string[] }>(`/api/apps/${app.id}/runs/${started.run}/logs`)
    },
    onSuccess: (result) => {
      setOutput(result.lines)
      setRan(command.trim())
    },
  })

  return (
    <div className="space-y-6">
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          run.mutate()
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
              disabled={run.isPending}
            />
            <Button type="submit" disabled={!command.trim() || run.isPending}>
              {run.isPending ? <Spinner /> : <PlayIcon />}
              {run.isPending ? t("apps.running") : t("apps.run")}
            </Button>
          </div>
          <FieldDescription>{t("apps.runCommandHelp")}</FieldDescription>
        </Field>
      </form>

      {run.error != null && <ErrorDisplay error={run.error} />}

      {run.isPending && (
        <Alert>
          <Spinner />
          <AlertDescription>{t("apps.runningHelp")}</AlertDescription>
        </Alert>
      )}

      {output === null ? (
        !run.isPending && (
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
          <p className="font-mono text-xs text-muted-foreground">$ {ran}</p>
          <ScrollArea className="h-80 rounded-md border bg-muted/30">
            <pre className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
              {output.length > 0 ? output.join("\n") : t("apps.runNoOutput")}
            </pre>
          </ScrollArea>
        </div>
      )}

      <ScheduledCommands app={app} />
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
function ScheduledCommands({ app }: { app: App }) {
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
                <Button type="button" variant="ghost" onClick={() => setAdding(false)}>
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

        {items.length === 0 && !adding ? (
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
                  <Switch
                    checked={job.enabled}
                    disabled={toggle.isPending}
                    onCheckedChange={() => toggle.mutate(job)}
                    aria-label={t("apps.scheduledCommands")}
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("common.remove")}
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
