import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CogIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, AppProcess } from "@/lib/types"

/**
 * An app's other processes: a worker, a clock, the lines of a Procfile beside
 * web. The same build and variables as the app, a command of their own, and
 * no port, so nothing sends them traffic.
 */
export function ProcessesCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const processes = useQuery({
    queryKey: ["processes", app.id],
    queryFn: () => api.get<List<AppProcess>>(`/api/apps/${app.id}/processes`),
  })

  const [name, setName] = useState("")
  const [command, setCommand] = useState("")
  const [instances, setInstances] = useState("1")
  // Edits to a listed process's instance count, on top of what was loaded.
  const [counts, setCounts] = useState<Record<string, string>>({})

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["processes", app.id] })
  const save = useMutation({
    // Instances left out keeps however many of that process already run.
    mutationFn: (process: { name: string; command: string; instances?: number }) =>
      api.put<AppProcess>(`/api/apps/${app.id}/processes/${process.name}`, {
        command: process.command,
        instances: process.instances,
      }),
    onSuccess: (saved) => {
      refresh()
      setCounts((previous) => {
        const next = { ...previous }
        delete next[saved.name]
        return next
      })
      if (saved.name === name.trim()) {
        setName("")
        setCommand("")
        setInstances("1")
      }
    },
  })
  const remove = useMutation({
    mutationFn: (process: string) => api.delete(`/api/apps/${app.id}/processes/${process}`),
    onSuccess: refresh,
  })

  if (processes.isLoading) return <Skeleton className="h-40" />
  if (processes.error) {
    return <ErrorDisplay error={processes.error} onRetry={() => void processes.refetch()} />
  }
  const items = processes.data?.items ?? []
  // A name already listed is that process's command changing, not a new one:
  // its instance count stays what it is.
  const existing = items.find((process) => process.name === name.trim())

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <CogIcon className="size-4" />
          {t("processes.title")}
        </CardTitle>
        <CardDescription>{t("processes.help")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {items.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("processes.name")}</TableHead>
                <TableHead>{t("processes.command")}</TableHead>
                <TableHead className="w-28">{t("processes.instances")}</TableHead>
                <TableHead className="w-20">{t("processes.running")}</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((process) => {
                const count = counts[process.name] ?? String(process.instances)
                const changed = count !== String(process.instances)
                return (
                  <TableRow key={process.name}>
                    <TableCell className="font-medium">{process.name}</TableCell>
                    <TableCell
                      className="max-w-72 truncate font-mono text-xs"
                      title={process.command}
                    >
                      {process.command}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <Input
                          type="number"
                          min={0}
                          max={100}
                          className="h-8 w-16"
                          aria-label={t("processes.instancesOf", { name: process.name })}
                          value={count}
                          onChange={(event) =>
                            setCounts({ ...counts, [process.name]: event.target.value })
                          }
                        />
                        {changed && (
                          <Button
                            size="sm"
                            variant="outline"
                            className="h-8"
                            disabled={save.isPending}
                            onClick={() =>
                              save.mutate({
                                name: process.name,
                                command: process.command,
                                instances: Number(count) || 0,
                              })
                            }
                          >
                            {t("common.save")}
                          </Button>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {process.instances === 0
                        ? t("processes.stopped")
                        : process.ready === undefined
                          ? "—"
                          : `${process.ready} / ${process.instances}`}
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("processes.remove", { name: process.name })}
                        disabled={remove.isPending}
                        onClick={() =>
                          void confirm({
                            title: t("processes.remove", { name: process.name }),
                            description: t("processes.removeConfirm", { name: process.name }),
                            confirmLabel: t("common.delete"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) remove.mutate(process.name)
                          })
                        }
                      >
                        <Trash2Icon className="size-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}

        <form
          className="grid gap-3 sm:grid-cols-[10rem_1fr_6rem_auto] sm:items-end"
          onSubmit={(event) => {
            event.preventDefault()
            save.mutate({
              name: name.trim(),
              command: command.trim(),
              instances: existing ? undefined : Number(instances) || 0,
            })
          }}
        >
          <Field>
            <FieldLabel htmlFor="process-name">{t("processes.name")}</FieldLabel>
            <Input
              id="process-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="worker"
              spellCheck={false}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="process-command">{t("processes.command")}</FieldLabel>
            <Input
              id="process-command"
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              placeholder="bundle exec sidekiq"
              className="font-mono"
              spellCheck={false}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="process-instances">{t("processes.instances")}</FieldLabel>
            <Input
              id="process-instances"
              type="number"
              min={0}
              max={100}
              value={existing ? String(existing.instances) : instances}
              disabled={existing !== undefined}
              onChange={(event) => setInstances(event.target.value)}
            />
          </Field>
          <Button type="submit" disabled={!name.trim() || !command.trim() || save.isPending}>
            {save.isPending ? <Spinner /> : <PlusIcon className="size-4" />}
            {existing ? t("processes.change") : t("processes.add")}
          </Button>
        </form>
        <FieldDescription>{t("processes.formHelp")}</FieldDescription>
        {save.error != null && <ErrorDisplay error={save.error} compact />}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>
    </Card>
  )
}

/**
 * The Procfile's other lines, offered when an app is created from a
 * repository or a folder. Each starts with the first deploy unless it is
 * unticked.
 */
export function DetectedProcesses({
  processes,
  declined,
  onToggle,
}: {
  processes: { name: string; command: string }[]
  declined: string[]
  onToggle: (name: string, run: boolean) => void
}) {
  const { t } = useTranslation()
  if (processes.length === 0) return null
  return (
    <Field>
      <FieldLabel>{t("processes.detected")}</FieldLabel>
      <div className="space-y-2">
        {processes.map((process) => (
          <label key={process.name} className="flex items-start gap-2 text-sm">
            <Checkbox
              checked={!declined.includes(process.name)}
              onCheckedChange={(checked) => onToggle(process.name, checked === true)}
              className="mt-0.5"
            />
            <span>
              <span className="font-medium">{process.name}</span>{" "}
              <code className="font-mono text-xs text-muted-foreground">{process.command}</code>
            </span>
          </label>
        ))}
      </div>
      <FieldDescription>{t("processes.detectedHelp")}</FieldDescription>
    </Field>
  )
}
