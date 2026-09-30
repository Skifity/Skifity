import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { LockIcon, TriangleAlertIcon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"

type AppPassword = {
  enabled: boolean
  username?: string
  updated_at?: string
  hostnames: string[]
  plain_http: string[]
}

/**
 * A password in front of the app.
 *
 * For the cases a firewall rule is the wrong tool: a client on a phone, a
 * reviewer whose address changes, a staging site that just should not be open
 * to whoever finds its URL. The form never shows the password back — there is
 * nothing to show, only a hash is kept — so "change" is the same form as
 * "set", and the username is the one thing it can say.
 */
export function AppPasswordProtection({ appId }: { appId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()

  const password = useQuery({
    queryKey: ["app-password", appId],
    queryFn: () => api.get<AppPassword>(`/api/apps/${appId}/password`),
  })

  const [editing, setEditing] = useState(false)
  const [username, setUsername] = useState("")
  const [secret, setSecret] = useState("")

  const save = useMutation({
    mutationFn: () =>
      api.put<AppPassword>(`/api/apps/${appId}/password`, { username, password: secret }),
    onSuccess: (saved) => {
      queryClient.setQueryData(["app-password", appId], saved)
      setEditing(false)
      setSecret("")
    },
  })

  const remove = useMutation({
    mutationFn: () => api.delete<AppPassword>(`/api/apps/${appId}/password`),
    onSuccess: (saved) => queryClient.setQueryData(["app-password", appId], saved),
  })

  if (password.isLoading) return <Skeleton className="h-24" />
  if (password.error) {
    return <ErrorDisplay error={password.error} onRetry={() => void password.refetch()} />
  }
  const current = password.data
  if (!current) return null

  const showForm = editing || !current.enabled

  function startEditing() {
    setUsername(current?.username ?? "")
    setSecret("")
    setEditing(true)
  }

  async function removePassword() {
    const confirmed = await confirm({
      title: t("appPassword.removeTitle"),
      description: t("appPassword.removeDescription"),
      confirmLabel: t("appPassword.remove"),
      destructive: true,
    })
    if (confirmed) remove.mutate()
  }

  return (
    <Card>
      <CardContent className="space-y-4 py-4">
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <LockIcon className="size-4 shrink-0 text-muted-foreground" />
              <span className="text-sm font-medium">{t("appPassword.title")}</span>
              {current.enabled && <Badge variant="secondary">{t("appPassword.on")}</Badge>}
            </div>
            <p className="mt-0.5 text-sm text-muted-foreground">{t("appPassword.help")}</p>
            {current.enabled && !editing && (
              <p className="mt-2 text-sm">
                {t("appPassword.current", {
                  username: current.username,
                  when: formatRelative(current.updated_at),
                })}
              </p>
            )}
          </div>
          {current.enabled && !editing && (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" onClick={startEditing}>
                {t("appPassword.change")}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => void removePassword()}
                disabled={remove.isPending}
              >
                {remove.isPending && <Spinner />}
                {t("appPassword.remove")}
              </Button>
            </div>
          )}
        </div>

        {current.plain_http.length > 0 && (current.enabled || showForm) && (
          <Alert variant="warning">
            <TriangleAlertIcon />
            <AlertTitle>{t("appPassword.plainHttp")}</AlertTitle>
            <AlertDescription>
              {t("appPassword.plainHttpHelp", { hostnames: current.plain_http.join(", ") })}
            </AlertDescription>
          </Alert>
        )}

        {showForm && (
          <form
            className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
            onSubmit={(event) => {
              event.preventDefault()
              save.mutate()
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="app-password-username">{t("appPassword.username")}</Label>
              <Input
                id="app-password-username"
                autoComplete="off"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="app-password-secret">{t("appPassword.password")}</Label>
              <Input
                id="app-password-secret"
                type="password"
                autoComplete="new-password"
                value={secret}
                onChange={(event) => setSecret(event.target.value)}
              />
            </div>
            <div className="flex gap-2">
              <Button type="submit" disabled={save.isPending || !username.trim() || !secret}>
                {save.isPending && <Spinner />}
                {current.enabled ? t("appPassword.save") : t("appPassword.turnOn")}
              </Button>
              {editing && (
                <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
                  {t("common.cancel")}
                </Button>
              )}
            </div>
          </form>
        )}

        {save.error && <ErrorDisplay error={save.error} />}
        {remove.error && <ErrorDisplay error={remove.error} />}

        <p className="text-xs text-muted-foreground">{t("appPassword.previews")}</p>
      </CardContent>
    </Card>
  )
}
