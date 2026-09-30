import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BellIcon, PlusIcon, SendIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { EmptyState } from "@/components/empty-state"
import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
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
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { NotificationChannel, NotificationField, NotificationKind } from "@/lib/types"

/** The events a channel can subscribe to, matching internal/notify. */
const EVENTS = [
  "deploy.succeeded",
  "deploy.failed",
  "app.unhealthy",
  "server.added",
  "server.lost",
  "backup.failed",
  "certificate.failed",
] as const

/**
 * What each built-in kind needs, so the form asks for exactly that.
 *
 * Only the built-in ones. The list of kinds itself comes from the API now,
 * because a plugin can provide a way of sending and a form that is written in
 * here could never offer it: the panel would accept such a channel through the
 * API and never let a person pick it.
 */
const BUILT_IN: Record<string, NotificationField[]> = {
  telegram: [
    { key: "bot_token", label: "Bot token", secret: true, required: true },
    { key: "chat_id", label: "Chat id", required: true },
  ],
  discord: [{ key: "webhook_url", label: "Webhook URL", secret: true, required: true }],
  webhook: [{ key: "url", label: "URL", required: true }],
  email: [{ key: "to", label: "To", required: true }],
}

/** The form for a kind: the panel's own for a built-in, the plugin's otherwise. */
function fieldsFor(kind: NotificationKind | undefined): NotificationField[] {
  if (!kind) return []
  return BUILT_IN[kind.kind] ?? kind.fields ?? []
}

/** What to call a kind: a translated name for a built-in, the plugin's own otherwise. */
function useKindLabel() {
  const { t } = useTranslation()
  return (kind: string, name?: string) =>
    BUILT_IN[kind] !== undefined
      ? t(`notifications.kind.${kind}`)
      : (name ?? t("notifications.kindFromPlugin"))
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
  const kinds = useChannelKinds()
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
          {items.map((channel) => (
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
                  </div>
                  <p className="truncate text-xs text-muted-foreground">
                    {channel.events
                      .split(",")
                      .filter(Boolean)
                      .map((event) => t(`notifications.event.${event}`, { defaultValue: event }))
                      .join(" · ") || t("common.none")}
                  </p>
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
          ))}

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

function ChannelForm({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const kinds = useChannelKinds()
  const label = useKindLabel()
  const [kind, setKind] = useState("telegram")
  const [name, setName] = useState("")
  const [config, setConfig] = useState<Record<string, string>>({})
  // Failures are what people actually want to be told about; successes are
  // opt-in so the channel does not become noise nobody reads.
  const [events, setEvents] = useState<string[]>(["deploy.failed", "app.unhealthy", "server.lost"])

  const create = useMutation({
    mutationFn: () =>
      api.post(`/api/teams/${team!.id}/notifications`, {
        kind,
        name: name.trim() || kind,
        config,
        events,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["notifications", team?.id] })
      onDone()
    },
  })

  // Derived during render rather than copied into state by an effect: the
  // chosen kind and the list from the API are the only facts, and the form is
  // a function of them.
  const available = kinds.data?.items ?? []
  const chosen = available.find((candidate) => candidate.kind === kind)
  const fields = fieldsFor(chosen)
  const complete = fields.every(
    (field) => field.required !== true || (config[field.key] ?? "").trim() !== "",
  )

  return (
    <Card>
      <CardContent className="pt-6">
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="channel-kind">{t("notifications.channelKind")}</FieldLabel>
            <Select
              value={kind}
              onValueChange={(next) => {
                setKind(next)
                // The fields are different per kind; keeping the old values
                // would send a Discord URL as a Telegram token.
                setConfig({})
              }}
            >
              <SelectTrigger id="channel-kind">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {available.map((candidate) => (
                  <SelectItem key={candidate.kind} value={candidate.kind}>
                    {label(candidate.kind, candidate.name)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {chosen?.provider != null && (
              <FieldDescription>
                {chosen.description != null && chosen.description !== ""
                  ? `${chosen.description} — `
                  : ""}
                {t("notifications.providedBy", { plugin: chosen.provider })}
              </FieldDescription>
            )}
          </Field>

          <Field>
            <FieldLabel htmlFor="channel-name">{t("notifications.channelName")}</FieldLabel>
            <Input
              id="channel-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={label(kind, chosen?.name)}
            />
          </Field>

          {fields.map((field) => (
            <ChannelField
              key={field.key}
              field={field}
              value={config[field.key] ?? ""}
              onChange={(value) => setConfig({ ...config, [field.key]: value })}
            />
          ))}

          <FieldSet>
            <FieldLegend variant="label">{t("notifications.events")}</FieldLegend>
            <FieldDescription>{t("notifications.eventsHelp")}</FieldDescription>
            <FieldGroup className="grid gap-2 pt-1 sm:grid-cols-2">
              {EVENTS.map((event) => (
                <FieldLabel key={event} htmlFor={`event-${event}`} className="font-normal">
                  <Checkbox
                    id={`event-${event}`}
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

          {create.error != null && <ErrorDisplay error={create.error} compact />}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onDone}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!complete || kinds.isLoading || create.isPending}>
              {create.isPending && <Spinner />}
              {create.isPending ? t("common.saving") : t("common.add")}
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
 */
function ChannelField({
  field,
  value,
  onChange,
}: {
  field: NotificationField
  value: string
  onChange: (value: string) => void
}) {
  const id = `channel-${field.key}`
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
        required={field.required === true}
      />
      {help}
    </Field>
  )
}
