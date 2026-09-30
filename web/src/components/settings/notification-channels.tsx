import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BellIcon, PencilIcon, PlusIcon, SendIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { EmptyState } from "@/components/empty-state"
import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { useTeamProjects } from "@/components/settings/member-access"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
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
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type {
  NotificationChannel,
  NotificationChannelDetail,
  NotificationField,
  NotificationKind,
} from "@/lib/types"

/** The events a channel can subscribe to, matching internal/notify. */
const EVENTS = [
  "deploy.succeeded",
  "deploy.failed",
  "app.unhealthy",
  "server.added",
  "server.lost",
  "backup.failed",
  "backup.missed",
  "backup.succeeded",
  "certificate.failed",
  "app.alert",
  "server.alert",
] as const

/**
 * Whether a plugin sends this kind. The panel has its own words for every
 * other kind, and a plugin's are the plugin author's.
 */
function isProvided(kind: string): boolean {
  return kind.startsWith("plugin:")
}

/**
 * The form for a kind, from the API: the server says what each kind asks for
 * and which of it is secret, because it is the server that must never send a
 * secret back. A built-in kind's words are the panel's own, translated; a
 * plugin's are the plugin author's.
 */
function useFieldsFor() {
  const { t } = useTranslation()
  return (kind: NotificationKind | undefined): NotificationField[] => {
    if (!kind) return []
    const fields = kind.fields ?? []
    if (isProvided(kind.kind)) return fields
    return fields.map((field) => ({
      ...field,
      label: t(`notifications.field.${kind.kind}.${field.key}`),
      help: t(`notifications.field.${kind.kind}.${field.key}Help`, { defaultValue: "" }),
    }))
  }
}

/** What to call a kind: a translated name for a built-in, the plugin's own otherwise. */
function useKindLabel() {
  const { t } = useTranslation()
  return (kind: string, name?: string) =>
    isProvided(kind) ? (name ?? t("notifications.kindFromPlugin")) : t(`notifications.kind.${kind}`)
}

/**
 * The ways of sending that exist right now.
 *
 * Read from the API rather than written into the frontend, because a plugin is
 * installed and removed while somebody has this page open.
 */
function useChannelKinds() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["notification-kinds", team?.id],
    queryFn: () => api.get<List<NotificationKind>>(`/api/teams/${team!.id}/notifications/kinds`),
    enabled: Boolean(team),
  })
}

export function NotificationChannels() {
  const { t } = useTranslation()
  const confirmRemove = useConfirm()
  const { team } = useSession()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)
  const kinds = useChannelKinds()
  const projects = useTeamProjects()
  const label = useKindLabel()

  const channels = useQuery({
    queryKey: ["notifications", team?.id],
    queryFn: () => api.get<List<NotificationChannel>>(`/api/teams/${team!.id}/notifications`),
    enabled: Boolean(team),
  })

  const remove = useMutation({
    mutationFn: (channelID: string) =>
      api.delete(`/api/teams/${team!.id}/notifications/${channelID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["notifications", team?.id] }),
  })

  const sendTest = useMutation({
    mutationFn: (channelID: string) =>
      api.post(`/api/teams/${team!.id}/notifications/${channelID}/test`),
    onSuccess: () => toast.success(t("notifications.testSent")),
  })

  if (channels.isLoading) return <Skeleton className="h-48" />
  if (channels.error) {
    return <ErrorDisplay error={channels.error} onRetry={() => void channels.refetch()} />
  }

  const items = channels.data?.items ?? []

  /** Where a channel's events come from, in words. */
  const reach = (channel: NotificationChannel) => {
    if (!channel.scoped) return null
    const names = (channel.projects ?? [])
      .map((id) => projects.data?.items.find((project) => project.id === id)?.name)
      .filter(Boolean)
    return names.length > 0
      ? t("notifications.onlyProjects", { projects: names.join(", ") })
      : t("notifications.noProjectsLeft")
  }

  return (
    <div className="space-y-4">
      <p className="max-w-2xl text-sm text-muted-foreground">{t("notifications.help")}</p>

      {items.length === 0 && !adding ? (
        <EmptyState
          icon={BellIcon}
          title={t("notifications.empty")}
          description={t("notifications.help")}
          action={
            <Button onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("notifications.add")}
            </Button>
          }
        />
      ) : (
        <div className="space-y-3">
          {items.map((channel) =>
            editing === channel.id ? (
              <EditChannel key={channel.id} channel={channel} onDone={() => setEditing(null)} />
            ) : (
              <Card key={channel.id}>
                <CardContent className="flex flex-wrap items-center gap-3 py-4">
                  <BellIcon className="size-4 shrink-0 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate font-medium">{channel.name}</span>
                      <Badge variant="secondary" className="text-[10px]">
                        {label(
                          channel.kind,
                          kinds.data?.items.find((candidate) => candidate.kind === channel.kind)
                            ?.name,
                        )}
                      </Badge>
                      {!channel.enabled && (
                        <Badge variant="outline" className="text-[10px]">
                          {t("notifications.paused")}
                        </Badge>
                      )}
                    </div>
                    <p className="truncate text-xs text-muted-foreground">
                      {channel.events
                        .split(",")
                        .filter(Boolean)
                        .map((event) => t(`notifications.event.${event}`, { defaultValue: event }))
                        // No list is every event but the opt-in ones, which
                        // is what the panel sends it; "None" said the opposite.
                        .join(" · ") || t("notifications.everyEvent")}
                    </p>
                    {channel.scoped && (
                      <p className="truncate text-xs text-muted-foreground">{reach(channel)}</p>
                    )}
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={sendTest.isPending}
                    onClick={() => sendTest.mutate(channel.id)}
                  >
                    <SendIcon className="size-4" />
                    {t("notifications.test")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("common.edit")}
                    onClick={() => setEditing(channel.id)}
                  >
                    <PencilIcon className="size-4 text-muted-foreground" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("common.remove")}
                    disabled={remove.isPending}
                    onClick={() =>
                      void confirmRemove({
                        title: t("common.remove"),
                        description: t("notifications.removeConfirm", { name: channel.name }),
                        confirmLabel: t("common.remove"),
                        destructive: true,
                      }).then((yes) => {
                        if (yes) remove.mutate(channel.id)
                      })
                    }
                  >
                    <Trash2Icon className="size-4 text-muted-foreground" />
                  </Button>
                </CardContent>
              </Card>
            ),
          )}

          {!adding && (
            <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("notifications.add")}
            </Button>
          )}
        </div>
      )}

      {adding && <ChannelForm onDone={() => setAdding(false)} />}
      {remove.error != null && <ErrorDisplay error={remove.error} />}
      {sendTest.error != null && <ErrorDisplay error={sendTest.error} />}
    </div>
  )
}

/**
 * Changing a channel: its settings are read first, then the form that adds a
 * channel is drawn with them.
 *
 * The form is only drawn once they are here, so it starts from them rather
 * than from empty and copying them in afterwards.
 */
function EditChannel({ channel, onDone }: { channel: NotificationChannel; onDone: () => void }) {
  const { team } = useSession()
  const detail = useQuery({
    queryKey: ["notification", team?.id, channel.id],
    queryFn: () =>
      api.get<NotificationChannelDetail>(`/api/teams/${team!.id}/notifications/${channel.id}`),
    enabled: Boolean(team),
    // What the form shows is what is stored now, not what was stored when
    // the page was first opened.
    gcTime: 0,
  })
  if (detail.isLoading) return <Skeleton className="h-48" />
  if (detail.error || !detail.data) {
    return <ErrorDisplay error={detail.error} onRetry={() => void detail.refetch()} />
  }
  return <ChannelForm existing={detail.data} onDone={onDone} />
}

/**
 * Adding a channel, or changing one.
 *
 * A secret is never shown again once stored. Changing a channel shows that it
 * is there, and a secret box left empty keeps it: the server merges, so a
 * token nobody retyped is not lost.
 */
function ChannelForm({
  existing,
  onDone,
}: {
  existing?: NotificationChannelDetail
  onDone: () => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const kinds = useChannelKinds()
  const projects = useTeamProjects()
  const label = useKindLabel()
  const fieldsFor = useFieldsFor()
  const [kind, setKind] = useState(existing?.kind ?? "telegram")
  const [name, setName] = useState(existing?.name ?? "")
  const [config, setConfig] = useState<Record<string, string>>(existing?.config ?? {})
  // Failures are what people actually want to be told about; successes are
  // opt-in so the channel does not become noise nobody reads.
  // backup.failed among them: docs/backups.md said it was on by default,
  // and it was not.
  const [events, setEvents] = useState<string[]>(
    existing
      ? existing.events.split(",").filter(Boolean)
      : ["deploy.failed", "app.unhealthy", "server.lost", "backup.failed", "backup.missed"],
  )
  const [enabled, setEnabled] = useState(existing?.enabled ?? true)
  const [limited, setLimited] = useState(existing?.scoped ?? false)
  const [chosen, setChosen] = useState<string[]>(existing?.projects ?? [])

  // Derived during render rather than copied into state by an effect: the
  // chosen kind and the list from the API are the only facts, and the form is
  // a function of them.
  const available = kinds.data?.items ?? []
  const chosenKind = available.find((candidate) => candidate.kind === kind)
  const fields = fieldsFor(chosenKind)
  const stored = new Set(existing?.secrets ?? [])
  const complete =
    fields.every(
      (field) =>
        field.required !== true || (config[field.key] ?? "").trim() !== "" || stored.has(field.key),
    ) &&
    (!limited || chosen.length > 0)

  const save = useMutation({
    mutationFn: () => {
      // Every field the form has, empty ones included: an emptied setting is
      // cleared, and an empty secret is kept by the server.
      const settings = Object.fromEntries(
        fields.map((field) => [field.key, config[field.key] ?? ""]),
      )
      const common = {
        name: name.trim() || (existing ? existing.name : kind),
        events,
        projects: limited ? chosen : undefined,
        config: settings,
      }
      return existing
        ? api.put(`/api/teams/${team!.id}/notifications/${existing.id}`, { ...common, enabled })
        : api.post(`/api/teams/${team!.id}/notifications`, { ...common, kind })
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["notifications", team?.id] })
      if (existing) {
        void queryClient.invalidateQueries({ queryKey: ["notification", team?.id, existing.id] })
      }
      onDone()
    },
  })

  const formID = existing ? `channel-${existing.id}` : "channel-new"

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
            <FieldLabel htmlFor={`${formID}-kind`}>{t("notifications.channelKind")}</FieldLabel>
            <Select
              value={kind}
              disabled={existing != null}
              onValueChange={(next) => {
                setKind(next)
                // The fields are different per kind; keeping the old values
                // would send a Discord URL as a Telegram token.
                setConfig({})
              }}
            >
              <SelectTrigger id={`${formID}-kind`}>
                <SelectValue>{label(kind, chosenKind?.name)}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                {available.map((candidate) => (
                  <SelectItem key={candidate.kind} value={candidate.kind}>
                    {label(candidate.kind, candidate.name)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {existing != null && (
              <FieldDescription>{t("notifications.kindFixed")}</FieldDescription>
            )}
            {chosenKind?.provider != null && (
              <FieldDescription>
                {chosenKind.description != null && chosenKind.description !== ""
                  ? `${chosenKind.description} — `
                  : ""}
                {t("notifications.providedBy", { plugin: chosenKind.provider })}
              </FieldDescription>
            )}
          </Field>

          <Field>
            <FieldLabel htmlFor={`${formID}-name`}>{t("notifications.channelName")}</FieldLabel>
            <Input
              id={`${formID}-name`}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={label(kind, chosenKind?.name)}
            />
          </Field>

          {fields.map((field) => (
            <ChannelField
              key={field.key}
              id={`${formID}-${field.key}`}
              field={field}
              stored={stored.has(field.key)}
              value={config[field.key] ?? ""}
              onChange={(value) => setConfig({ ...config, [field.key]: value })}
            />
          ))}

          <FieldSet>
            <FieldLegend variant="label">{t("notifications.events")}</FieldLegend>
            <FieldDescription>
              {events.length === 0
                ? t("notifications.noEventsChosen")
                : t("notifications.eventsHelp")}
            </FieldDescription>
            <FieldGroup className="grid gap-2 pt-1 sm:grid-cols-2">
              {EVENTS.map((event) => (
                <FieldLabel
                  key={event}
                  htmlFor={`${formID}-event-${event}`}
                  className="font-normal"
                >
                  <Checkbox
                    id={`${formID}-event-${event}`}
                    checked={events.includes(event)}
                    onCheckedChange={(checked) =>
                      setEvents(
                        checked === true
                          ? [...events, event]
                          : events.filter((candidate) => candidate !== event),
                      )
                    }
                  />
                  {t(`notifications.event.${event}`)}
                </FieldLabel>
              ))}
            </FieldGroup>
          </FieldSet>

          <div className="space-y-3 rounded-lg border p-3">
            <Field orientation="horizontal">
              <Switch id={`${formID}-limited`} checked={limited} onCheckedChange={setLimited} />
              <FieldContent>
                <FieldLabel htmlFor={`${formID}-limited`} className="font-normal">
                  {t("notifications.limitToProjects")}
                </FieldLabel>
                <FieldDescription>{t("notifications.limitToProjectsHelp")}</FieldDescription>
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
                        onCheckedChange={(value) =>
                          setChosen(
                            value === true
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
                  <p className="text-xs text-muted-foreground">
                    {t("notifications.chooseProject")}
                  </p>
                )}
              </div>
            )}
          </div>

          {existing != null && (
            <Field orientation="horizontal">
              <Switch id={`${formID}-enabled`} checked={enabled} onCheckedChange={setEnabled} />
              <FieldContent>
                <FieldLabel htmlFor={`${formID}-enabled`} className="font-normal">
                  {t("notifications.enabled")}
                </FieldLabel>
                <FieldDescription>{t("notifications.enabledHelp")}</FieldDescription>
              </FieldContent>
            </Field>
          )}

          {save.error != null && <ErrorDisplay error={save.error} compact />}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onDone}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!complete || kinds.isLoading || save.isPending}>
              {save.isPending && <Spinner />}
              {save.isPending
                ? t("common.saving")
                : existing != null
                  ? t("common.save")
                  : t("common.add")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

/**
 * One input in a channel's form.
 *
 * A built-in kind only ever needs text and password. A plugin may declare a
 * number, a switch or a choice, and a field the panel cannot draw is a field
 * somebody cannot fill in — so all five kinds are drawn here rather than
 * falling back to a text box that stores "true" as a word.
 *
 * A secret that is stored is never shown; the box says it is there, and left
 * empty it keeps it.
 */
function ChannelField({
  id,
  field,
  stored,
  value,
  onChange,
}: {
  id: string
  field: NotificationField
  stored: boolean
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const help =
    field.help != null && field.help !== "" ? (
      <FieldDescription>{field.help}</FieldDescription>
    ) : null

  if (field.kind === "bool") {
    return (
      <Field orientation="horizontal">
        <Checkbox
          id={id}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked === true ? "true" : "false")}
        />
        <div>
          <FieldLabel htmlFor={id} className="font-normal">
            {field.label}
          </FieldLabel>
          {help}
        </div>
      </Field>
    )
  }

  if (field.kind === "choice") {
    return (
      <Field>
        <FieldLabel htmlFor={id}>{field.label}</FieldLabel>
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(field.options ?? []).map((option) => (
              <SelectItem key={option} value={option}>
                {option}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {help}
      </Field>
    )
  }

  return (
    <Field>
      <FieldLabel htmlFor={id}>{field.label}</FieldLabel>
      <Input
        id={id}
        type={
          field.secret === true || field.kind === "password"
            ? "password"
            : field.kind === "number"
              ? "number"
              : "text"
        }
        autoComplete="off"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={stored ? t("notifications.secretStored") : undefined}
        required={field.required === true && !stored}
      />
      {help}
    </Field>
  )
}
