import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ConstructionIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, MaintenanceView } from "@/lib/types"

function invalidate(appId: string) {
  void queryClient.invalidateQueries({ queryKey: ["app", appId] })
  void queryClient.invalidateQueries({ queryKey: ["maintenance", appId] })
}

/**
 * Maintenance: visitors get a page with the team's message, with a 503 and a
 * Retry-After, while the addresses listed still reach the app. The app keeps
 * running, so ending it is instant.
 */
export function MaintenanceCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const view = useQuery({
    queryKey: ["maintenance", app.id],
    queryFn: () => api.get<MaintenanceView>(`/api/apps/${app.id}/maintenance`),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("apps.maintenanceTitle")}</CardTitle>
        <CardDescription>{t("apps.maintenanceHelp")}</CardDescription>
      </CardHeader>
      <CardContent>
        {view.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : view.error != null ? (
          <ErrorDisplay error={view.error} compact />
        ) : view.data.hostnames.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("apps.maintenanceNoDomain")}</p>
        ) : (
          // Keyed on what is stored, so the form starts from it and starts
          // again when it changes, without copying it into state by an effect.
          <MaintenanceForm
            key={`${view.data.active}-${view.data.message}-${view.data.allow.join(",")}`}
            app={app}
            view={view.data}
          />
        )}
      </CardContent>
    </Card>
  )
}

function MaintenanceForm({ app, view }: { app: App; view: MaintenanceView }) {
  const { t } = useTranslation()
  const [message, setMessage] = useState(
    view.active ? view.message : t("apps.maintenanceDefaultMessage"),
  )
  const [allow, setAllow] = useState(view.allow.join("\n"))
  const entries = allow
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
  const canAddMine = view.your_address != null && !entries.includes(view.your_address)

  const start = useMutation({
    mutationFn: () =>
      api.put(`/api/apps/${app.id}/maintenance`, { message: message.trim(), allow: entries }),
    onSuccess: () => invalidate(app.id),
  })
  const end = useMutation({
    mutationFn: () => api.delete(`/api/apps/${app.id}/maintenance`),
    onSuccess: () => invalidate(app.id),
  })

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        start.mutate()
      }}
    >
      <Field>
        <FieldLabel htmlFor="maintenance-message">{t("apps.maintenanceMessage")}</FieldLabel>
        <Textarea
          id="maintenance-message"
          value={message}
          maxLength={1000}
          rows={3}
          onChange={(event) => setMessage(event.target.value)}
        />
        <FieldDescription>{t("apps.maintenanceMessageHelp")}</FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor="maintenance-allow">{t("apps.maintenanceAllow")}</FieldLabel>
        <Textarea
          id="maintenance-allow"
          value={allow}
          rows={2}
          placeholder="203.0.113.7"
          className="font-mono"
          onChange={(event) => setAllow(event.target.value)}
        />
        <FieldDescription>{t("apps.maintenanceAllowHelp")}</FieldDescription>
        {canAddMine && (
          <div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setAllow([...entries, view.your_address].join("\n"))}
            >
              {t("apps.maintenanceAddMine", { address: view.your_address })}
            </Button>
          </div>
        )}
      </Field>
      {start.error != null && <ErrorDisplay error={start.error} compact />}
      {end.error != null && <ErrorDisplay error={end.error} compact />}
      <div className="flex flex-wrap justify-end gap-2">
        {view.active && (
          <Button
            type="button"
            variant="outline"
            disabled={end.isPending}
            onClick={() => end.mutate()}
          >
            {end.isPending && <Spinner />}
            {t("apps.maintenanceEnd")}
          </Button>
        )}
        <Button type="submit" disabled={start.isPending || message.trim() === ""}>
          {start.isPending ? <Spinner /> : <ConstructionIcon />}
          {view.active ? t("common.save") : t("apps.maintenanceStart")}
        </Button>
      </div>
    </form>
  )
}

/** Said at the top of the app while visitors see the page instead of it. */
export function MaintenanceNotice({ app }: { app: App }) {
  const { t } = useTranslation()
  const end = useMutation({
    mutationFn: () => api.delete(`/api/apps/${app.id}/maintenance`),
    onSuccess: () => invalidate(app.id),
  })
  if (!app.maintenance) return null
  return (
    <Alert variant="warning">
      <ConstructionIcon />
      <AlertTitle>{t("apps.maintenanceActive")}</AlertTitle>
      <AlertDescription>
        <p>
          {t("apps.maintenanceActiveBy", {
            who: app.maintenance.started_by,
            when: formatRelative(app.maintenance.started_at),
          })}
        </p>
        {end.error != null && <ErrorDisplay error={end.error} compact />}
        <Button
          variant="outline"
          size="sm"
          className="mt-2"
          disabled={end.isPending}
          onClick={() => end.mutate()}
        >
          {end.isPending && <Spinner />}
          {t("apps.maintenanceEnd")}
        </Button>
      </AlertDescription>
    </Alert>
  )
}
