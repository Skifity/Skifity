import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ContainerIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { RegistryCredential } from "@/lib/types"
import { cardHeaderWithActions } from "@/lib/layout"

type Saved = { registry: RegistryCredential; checked: boolean; unchecked_because?: string }

/**
 * The private registries the team pulls from — a ghcr.io package, a private
 * Docker Hub repository, a company Harbor. Every app in the team pulls its
 * image with these, and every build its base image.
 */
export function RegistriesCard() {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirm = useConfirm()
  const path = `/api/teams/${team?.id}/registries`
  const registries = useQuery({
    queryKey: ["registries", team?.id],
    queryFn: () => api.get<List<RegistryCredential>>(path),
    enabled: Boolean(team),
  })
  const [adding, setAdding] = useState(false)
  const [host, setHost] = useState("")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["registries", team?.id] })
  const save = useMutation({
    mutationFn: () => api.put<Saved>(path, { host, username, password }),
    onSuccess: (saved) => {
      refresh()
      setAdding(false)
      setHost("")
      setUsername("")
      setPassword("")
      // Saved either way; only one of them was asked of the registry.
      if (saved.checked) toast.success(t("registries.checked", { host: saved.registry.host }))
      else
        toast.warning(t("registries.unchecked", { host: saved.registry.host }), {
          description: saved.unchecked_because,
        })
    },
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`${path}/${id}`),
    onSuccess: refresh,
  })

  const items = registries.data?.items ?? []
  return (
    <Card>
      <CardHeader className={cardHeaderWithActions}>
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <ContainerIcon className="size-4" />
            {t("registries.title")}
          </CardTitle>
          <CardDescription>{t("registries.help")}</CardDescription>
        </div>
        {!adding && (
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            <PlusIcon className="size-4" />
            {t("registries.add")}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {registries.isLoading ? (
          <Skeleton className="h-16" />
        ) : registries.error ? (
          <ErrorDisplay error={registries.error} onRetry={() => void registries.refetch()} />
        ) : items.length === 0 && !adding ? (
          <p className="text-sm text-muted-foreground">{t("registries.empty")}</p>
        ) : (
          items.length > 0 && (
            <ul className="divide-y rounded-md border">
              {items.map((registry) => (
                <li key={registry.id} className="flex items-center gap-3 px-3 py-2 text-sm">
                  <code className="font-mono text-xs">{registry.host}</code>
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">
                    {registry.username}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {formatRelative(registry.updated_at)}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("registries.remove", { host: registry.host })}
                    disabled={remove.isPending}
                    onClick={() =>
                      void confirm({
                        title: t("registries.remove", { host: registry.host }),
                        description: t("registries.removeConfirm", { host: registry.host }),
                        confirmLabel: t("common.delete"),
                        destructive: true,
                      }).then((yes) => {
                        if (yes) remove.mutate(registry.id)
                      })
                    }
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </li>
              ))}
            </ul>
          )
        )}

        {adding && (
          <form
            className="grid gap-3 sm:grid-cols-3"
            onSubmit={(event) => {
              event.preventDefault()
              save.mutate()
            }}
          >
            <Field>
              <FieldLabel htmlFor="registry-host">{t("registries.host")}</FieldLabel>
              <Input
                id="registry-host"
                value={host}
                placeholder="ghcr.io"
                spellCheck={false}
                onChange={(event) => setHost(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="registry-username">{t("registries.username")}</FieldLabel>
              <Input
                id="registry-username"
                value={username}
                autoComplete="off"
                spellCheck={false}
                onChange={(event) => setUsername(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="registry-password">{t("registries.password")}</FieldLabel>
              <Input
                id="registry-password"
                type="password"
                value={password}
                autoComplete="new-password"
                onChange={(event) => setPassword(event.target.value)}
              />
            </Field>
            <FieldDescription className="sm:col-span-3">
              {t("registries.formHelp")}
            </FieldDescription>
            {save.error != null && (
              <div className="sm:col-span-3">
                <ErrorDisplay error={save.error} compact />
              </div>
            )}
            <div className="flex gap-2 sm:col-span-3">
              <Button
                type="submit"
                disabled={save.isPending || !host.trim() || !username.trim() || !password}
              >
                {save.isPending && <Spinner />}
                {t("registries.save")}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setAdding(false)}>
                {t("common.cancel")}
              </Button>
            </div>
          </form>
        )}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>
    </Card>
  )
}
