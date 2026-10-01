import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  BookOpenIcon,
  PencilIcon,
  PlusIcon,
  ScrollTextIcon,
  SendIcon,
  ServerIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { useTeamProjects } from "@/components/settings/member-access"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { LogDrain, LogDrainKind, LogDrainKindName, LogDrainList } from "@/lib/types"

/** The tone a drain's status is shown in, through the shared badge. */
const DRAIN_TONE: Record<LogDrain["status"], string> = {
  applied: "running",
  pending: "pending",
  failed: "failed",
  paused: "stopped",
  idle: "stopped",
  no_cluster: "unknown",
  unreadable: "failed",
}

/** And the collector's. */
const COLLECTOR_TONE: Record<string, string> = {
  running: "running",
  starting: "starting",
  failing: "failed",
  absent: "unknown",
}

function useLogDrains() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["log-drains", team?.id],
    queryFn: () => api.get<LogDrainList>(`/api/teams/${team!.id}/log-drains`),
    enabled: Boolean(team),
    // The collector's state changes on its own while the page is open.
    refetchInterval: 30_000,
  })
}

function invalidateDrains(teamID: string | undefined) {
  void queryClient.invalidateQueries({ queryKey: ["log-drains", teamID] })
}

/**
 * Settings → Log drains: where the team's apps' logs are shipped, and how the
 * collector that ships them is doing.
 */
export function LogDrains() {
  const { t } = useTranslation()
  const { team } = useSession()
  const drains = useLogDrains()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)
  const admin = team?.role === "admin" || team?.role === "owner"

  if (drains.isLoading) return <Skeleton className="h-48" />
  if (drains.error || !drains.data) {
    return <ErrorDisplay error={drains.error} onRetry={() => void drains.refetch()} />
  }
  const { items, kinds, limit } = drains.data
  const canAdd = admin && items.length < limit

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="max-w-2xl text-sm text-muted-foreground">{t("logDrains.help")}</p>
        <Button variant="ghost" size="sm" asChild>
          <a href="/docs/log-drains" target="_blank" rel="noreferrer">
            <BookOpenIcon className="size-4" />
            {t("logDrains.docs")}
          </a>
        </Button>
      </div>

      {items.length > 0 && <CollectorCard list={drains.data} />}

      {items.length === 0 && !adding ? (
        <EmptyState
          icon={ScrollTextIcon}
          title={t("logDrains.empty")}
          description={t("logDrains.emptyHelp")}
          action={
            admin ? (
              <Button onClick={() => setAdding(true)}>
                <PlusIcon className="size-4" />
                {t("logDrains.add")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="space-y-3">
          {items.map((drain) =>
            editing === drain.id ? (
              <DrainForm
                key={drain.id}
                kinds={kinds}
                existing={drain}
                onDone={() => setEditing(null)}
              />
            ) : (
              <DrainCard
                key={drain.id}
                drain={drain}
                admin={admin}
                onEdit={() => setEditing(drain.id)}
              />
            ),
          )}
          {canAdd && !adding && (
            <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("logDrains.add")}
            </Button>
          )}
          {admin && items.length >= limit && (
            <p className="text-xs text-muted-foreground">
              {t("logDrains.limitReached", { limit })}
            </p>
          )}
        </div>
      )}

      {adding && <DrainForm kinds={kinds} onDone={() => setAdding(false)} />}
    </div>
  )
}

/** The collector: whether it runs on every server, and what is wrong if not. */
function CollectorCard({ list }: { list: LogDrainList }) {
  const { t } = useTranslation()
  const { collector } = list
  const live = collector.live
  const state = collector.error ? "failing" : (live?.state ?? "absent")
  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
          <ServerIcon className="size-4 text-muted-foreground" />
          {t("logDrains.collector.title")}
          <StatusBadge
            status={COLLECTOR_TONE[state] ?? "unknown"}
            label={t(`logDrains.collector.state.${state}`)}
          />
        </CardTitle>
        <CardDescription>
          {collector.configuration === "no_cluster"
            ? t("logDrains.collector.noCluster")
            : live
              ? t("logDrains.collector.servers", {
                  ready: live.ready,
                  desired: live.desired,
                  version: live.version ?? "",
                })
              : t("logDrains.collector.notRead")}
        </CardDescription>
      </CardHeader>
      {(collector.error || (live && live.problems.length > 0)) && (
        <CardContent className="space-y-2">
          {collector.error && (
            <Alert variant="destructive">
              <AlertTitle>{t("logDrains.collector.updateFailed")}</AlertTitle>
              <AlertDescription className="break-words">{collector.error}</AlertDescription>
            </Alert>
          )}
          {live?.problems.map((problem, index) => (
            <p key={index} className="text-xs break-words text-muted-foreground">
              <span className="font-medium text-foreground">
                {problem.reason === "ConfigurationRefused"
                  ? t("logDrains.collector.refused")
                  : problem.reason}
              </span>
              {problem.server ? ` · ${problem.server}` : ""}
              {problem.message ? ` — ${problem.message}` : ""}
            </p>
          ))}
        </CardContent>
      )}
    </Card>
  )
}

function DrainCard({
  drain,
  admin,
  onEdit,
}: {
  drain: LogDrain
  admin: boolean
  onEdit: () => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirm = useConfirm()
  const projects = useTeamProjects()
  const path = `/api/teams/${team?.id}/log-drains/${drain.id}`

  const sendTest = useMutation({
    mutationFn: () => api.post(`${path}/test`),
    onSuccess: () => toast.success(t("logDrains.testSent", { name: drain.name })),
    // A failed test is recorded on the drain, so the list is read either way.
    onSettled: () => invalidateDrains(team?.id),
  })
  const remove = useMutation({
    mutationFn: () => api.delete(path),
    onSuccess: () => invalidateDrains(team?.id),
  })

  // Derived from the drain and the team's projects; nothing to keep in step.
  const names = drain.projects
    .map((id) => projects.data?.items.find((project) => project.id === id)?.name)
    .filter(Boolean)
  const reach = !drain.scoped
    ? t("logDrains.everyProject")
    : names.length > 0
      ? t("logDrains.onlyProjects", { projects: names.join(", ") })
      : t("logDrains.noProjectsLeft")

  return (
    <Card>
      <CardContent className="space-y-2 py-4">
        <div className="flex flex-wrap items-center gap-3">
          <ScrollTextIcon className="size-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate font-medium">{drain.name}</span>
              <Badge variant="secondary" className="text-[10px]">
                {t(`logDrains.kind.${drain.kind}`)}
              </Badge>
              <StatusBadge
                status={DRAIN_TONE[drain.status]}
                label={t(`logDrains.status.${drain.status}`)}
              />
            </div>
            <p className="truncate font-mono text-xs text-muted-foreground">{drain.destination}</p>
            <p className="truncate text-xs text-muted-foreground">
              {reach}
              {drain.include_builds ? ` · ${t("logDrains.withBuilds")}` : ""}
              {drain.tested_at
                ? ` · ${t("logDrains.testedAt", { when: formatRelative(drain.tested_at) })}`
                : ""}
            </p>
          </div>
          {admin && (
            <div className="flex items-center gap-1">
              <Button
                variant="outline"
                size="sm"
                disabled={sendTest.isPending}
                onClick={() => sendTest.mutate()}
              >
                {sendTest.isPending ? <Spinner /> : <SendIcon className="size-4" />}
                {t("logDrains.test")}
              </Button>
              <Button variant="ghost" size="icon" aria-label={t("common.edit")} onClick={onEdit}>
                <PencilIcon className="size-4 text-muted-foreground" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t("common.remove")}
                disabled={remove.isPending}
                onClick={() =>
                  void confirm({
                    title: t("logDrains.removeTitle", { name: drain.name }),
                    description: t("logDrains.removeConfirm"),
                    confirmLabel: t("common.remove"),
                    destructive: true,
                  }).then((yes) => {
                    if (yes) remove.mutate()
                  })
                }
              >
                <Trash2Icon className="size-4 text-muted-foreground" />
              </Button>
            </div>
          )}
        </div>
        {drain.test_error && (
          <p className="text-xs break-words text-destructive">
            {t("logDrains.lastTestFailed", { reason: drain.test_error })}
          </p>
        )}
        {sendTest.error != null && <ErrorDisplay error={sendTest.error} compact />}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>
    </Card>
  )
}

/**
 * Adding a drain, or changing one. Saving sends a test line first, and
 * nothing is kept unless the service takes it. A secret is never shown again:
 * left empty it keeps the stored one, unless the address changes.
 */
function DrainForm({
  kinds,
  existing,
  onDone,
}: {
  kinds: LogDrainKind[]
  existing?: LogDrain
  onDone: () => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const projects = useTeamProjects()
  const [kind, setKind] = useState<LogDrainKindName>(existing?.kind ?? "http")
  const [name, setName] = useState(existing?.name ?? "")
  const [values, setValues] = useState<Record<string, string>>(existing?.settings ?? {})
  const [forget, setForget] = useState<string[]>([])
  const [limited, setLimited] = useState(existing?.scoped ?? false)
  const [chosen, setChosen] = useState<string[]>(existing?.projects ?? [])
  const [builds, setBuilds] = useState(existing?.include_builds ?? false)
  const [enabled, setEnabled] = useState(existing?.enabled ?? true)

  // Derived during render: the kind and the list from the API are the facts.
  const spec = kinds.find((candidate) => candidate.kind === kind)
  const fields = spec?.fields ?? []
  const stored = new Set(existing?.secrets ?? [])
  const complete =
    fields.every(
      (field) =>
        field.required !== true ||
        (values[field.key] ?? "").trim() !== "" ||
        (field.default ?? "") !== "" ||
        (stored.has(field.key) && !forget.includes(field.key)),
    ) &&
    (!limited || chosen.length > 0)

  const save = useMutation({
    mutationFn: () => {
      // Every setting the form has; a secret only when one was typed, since
      // an empty one keeps what is stored.
      const settings = Object.fromEntries(
        fields
          .filter((field) => field.secret !== true || (values[field.key] ?? "") !== "")
          .map((field) => [field.key, values[field.key] ?? ""]),
      )
      const path = `/api/teams/${team!.id}/log-drains`
      if (existing) {
        return api.patch(`${path}/${existing.id}`, {
          name: name.trim() || existing.name,
          settings,
          clear: forget,
          enabled,
          include_builds: builds,
          ...(limited ? { projects: chosen } : { scoped: false }),
        })
      }
      return api.post(path, {
        name: name.trim() || t(`logDrains.kind.${kind}`),
        kind,
        settings,
        include_builds: builds,
        ...(limited ? { projects: chosen } : {}),
      })
    },
    onSuccess: () => {
      toast.success(t("logDrains.saved"))
      invalidateDrains(team?.id)
      onDone()
    },
  })

  const formID = existing ? `drain-${existing.id}` : "drain-new"

  return (
    <Card>
      <CardContent className="pt-6">
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            save.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor={`${formID}-kind`}>{t("logDrains.kindLabel")}</FieldLabel>
            <Select
              value={kind}
              disabled={existing != null}
              onValueChange={(next) => {
                setKind(next as LogDrainKindName)
                // Each kind asks for different things; an Axiom token is not
                // a Loki password.
                setValues({})
              }}
            >
              <SelectTrigger id={`${formID}-kind`}>
                <SelectValue>{t(`logDrains.kind.${kind}`)}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                {kinds.map((candidate) => (
                  <SelectItem key={candidate.kind} value={candidate.kind}>
                    {t(`logDrains.kind.${candidate.kind}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldDescription>{t(`logDrains.kindHelp.${kind}`)}</FieldDescription>
          </Field>

          <Field>
            <FieldLabel htmlFor={`${formID}-name`}>{t("logDrains.name")}</FieldLabel>
            <Input
              id={`${formID}-name`}
              value={name}
              maxLength={60}
              onChange={(event) => setName(event.target.value)}
              placeholder={t(`logDrains.kind.${kind}`)}
            />
          </Field>

          {fields.map((field) => {
            const id = `${formID}-${field.key}`
            const label = t(`logDrains.field.${kind}.${field.key}`)
            const help = t(`logDrains.field.${kind}.${field.key}Help`)
            const value = values[field.key] ?? ""
            const set = (next: string) => setValues({ ...values, [field.key]: next })
            if (field.options && field.options.length > 0) {
              return (
                <Field key={field.key}>
                  <FieldLabel htmlFor={id}>{label}</FieldLabel>
                  <Select value={value || field.default || ""} onValueChange={set}>
                    <SelectTrigger id={id}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {field.options.map((option) => (
                        <SelectItem key={option} value={option}>
                          {option}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FieldDescription>{help}</FieldDescription>
                </Field>
              )
            }
            const isStored = stored.has(field.key) && !forget.includes(field.key)
            return (
              <Field key={field.key}>
                <FieldLabel htmlFor={id}>{label}</FieldLabel>
                <Input
                  id={id}
                  type={field.secret === true ? "password" : "text"}
                  autoComplete="off"
                  value={value}
                  onChange={(event) => set(event.target.value)}
                  placeholder={
                    isStored ? t("logDrains.secretStored") : (field.placeholder ?? field.default)
                  }
                />
                <FieldDescription>{help}</FieldDescription>
                {field.secret === true && field.required !== true && stored.has(field.key) && (
                  <FieldLabel htmlFor={`${id}-forget`} className="font-normal">
                    <Checkbox
                      id={`${id}-forget`}
                      checked={forget.includes(field.key)}
                      onCheckedChange={(checked) =>
                        setForget(
                          checked === true
                            ? [...forget, field.key]
                            : forget.filter((key) => key !== field.key),
                        )
                      }
                    />
                    {t("logDrains.forgetSecret")}
                  </FieldLabel>
                )}
              </Field>
            )
          })}

          <FieldSet className="space-y-3 rounded-lg border p-3">
            <FieldLegend variant="label">{t("logDrains.whichLogs")}</FieldLegend>
            <Field orientation="horizontal">
              <Switch id={`${formID}-limited`} checked={limited} onCheckedChange={setLimited} />
              <FieldContent>
                <FieldLabel htmlFor={`${formID}-limited`} className="font-normal">
                  {t("logDrains.limitToProjects")}
                </FieldLabel>
                <FieldDescription>{t("logDrains.limitToProjectsHelp")}</FieldDescription>
              </FieldContent>
            </Field>
            {limited && (
              <div className="space-y-2 pl-1">
                {(projects.data?.items ?? []).map((project) => {
                  const box = `${formID}-project-${project.id}`
                  return (
                    <Field key={project.id} orientation="horizontal">
                      <Checkbox
                        id={box}
                        checked={chosen.includes(project.id)}
                        onCheckedChange={(checked) =>
                          setChosen(
                            checked === true
                              ? [...chosen, project.id]
                              : chosen.filter((other) => other !== project.id),
                          )
                        }
                      />
                      <FieldLabel htmlFor={box} className="font-normal">
                        {project.name}
                      </FieldLabel>
                    </Field>
                  )
                })}
                {chosen.length === 0 && (
                  <p className="text-xs text-muted-foreground">{t("logDrains.chooseProject")}</p>
                )}
              </div>
            )}
            <Field orientation="horizontal">
              <Switch id={`${formID}-builds`} checked={builds} onCheckedChange={setBuilds} />
              <FieldContent>
                <FieldLabel htmlFor={`${formID}-builds`} className="font-normal">
                  {t("logDrains.includeBuilds")}
                </FieldLabel>
                <FieldDescription>{t("logDrains.includeBuildsHelp")}</FieldDescription>
              </FieldContent>
            </Field>
          </FieldSet>

          {existing != null && (
            <Field orientation="horizontal">
              <Switch id={`${formID}-enabled`} checked={enabled} onCheckedChange={setEnabled} />
              <FieldContent>
                <FieldLabel htmlFor={`${formID}-enabled`} className="font-normal">
                  {t("logDrains.enabled")}
                </FieldLabel>
                <FieldDescription>{t("logDrains.enabledHelp")}</FieldDescription>
              </FieldContent>
            </Field>
          )}

          <p className="text-xs text-muted-foreground">{t("logDrains.testFirst")}</p>
          {save.error != null && <ErrorDisplay error={save.error} compact />}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onDone}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!complete || save.isPending}>
              {save.isPending ? <Spinner /> : <SendIcon className="size-4" />}
              {save.isPending ? t("logDrains.testing") : t("logDrains.testAndSave")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
