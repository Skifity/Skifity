import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CloudIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
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
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { CloudProvider, CloudProviderKind } from "@/lib/types"
import { cardHeaderWithActions } from "@/lib/layout"

/** The providers' own names, which are the same in every language. */
export const cloudProviderTitles: Record<CloudProviderKind, string> = {
  hetzner: "Hetzner Cloud",
  digitalocean: "DigitalOcean",
}

const kinds: CloudProviderKind[] = ["hetzner", "digitalocean"]

/** A provider's name as people write it, for a kind this panel may not know. */
export function cloudProviderTitle(kind: string) {
  return cloudProviderTitles[kind as CloudProviderKind] ?? kind
}

/** The query every view of the team's cloud connections shares. */
export function useCloudProviders() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["cloud-providers", team?.id],
    queryFn: () => api.get<List<CloudProvider>>(`/api/teams/${team?.id}/cloud-providers`),
    enabled: Boolean(team),
  })
}

/**
 * Connecting a Hetzner Cloud project or a DigitalOcean account: a name and a
 * token. The panel asks the provider whether the token works, and whether it
 * may create anything, before it keeps it; after that the token is never
 * shown again.
 */
export function CloudProviderForm({
  onSaved,
  onCancel,
}: {
  onSaved: (provider: CloudProvider) => void
  onCancel?: () => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const [kind, setKind] = useState<CloudProviderKind>("hetzner")
  const [name, setName] = useState("")
  const [token, setToken] = useState("")

  const save = useMutation({
    mutationFn: () =>
      api.post<CloudProvider>(`/api/teams/${team?.id}/cloud-providers`, {
        kind,
        name: name.trim() || undefined,
        token: token.trim(),
      }),
    onSuccess: (provider) => {
      // The token only ever existed in this form.
      setToken("")
      setName("")
      void queryClient.invalidateQueries({ queryKey: ["cloud-providers", team?.id] })
      toast.success(t("cloud.saved", { name: provider.name }))
      onSaved(provider)
    },
  })

  return (
    <form
      className="grid gap-3 sm:grid-cols-2"
      onSubmit={(event) => {
        event.preventDefault()
        save.mutate()
      }}
    >
      <Field className="sm:col-span-2">
        <FieldLabel htmlFor="cloud-kind">{t("cloud.kind")}</FieldLabel>
        <Select value={kind} onValueChange={(value) => setKind(value as CloudProviderKind)}>
          <SelectTrigger id="cloud-kind">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {kinds.map((value) => (
              <SelectItem key={value} value={value}>
                {cloudProviderTitles[value]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel htmlFor="cloud-name">{t("cloud.name")}</FieldLabel>
        <Input
          id="cloud-name"
          value={name}
          placeholder={cloudProviderTitles[kind]}
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor="cloud-token">{t("cloud.token")}</FieldLabel>
        <Input
          id="cloud-token"
          type="password"
          value={token}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setToken(event.target.value)}
        />
      </Field>
      <FieldDescription className="sm:col-span-2">{t(`cloud.tokenHelp.${kind}`)}</FieldDescription>
      {save.error != null && (
        <div className="sm:col-span-2">
          <ErrorDisplay error={save.error} compact />
        </div>
      )}
      <div className="flex gap-2 sm:col-span-2">
        <Button type="submit" disabled={save.isPending || !token.trim()}>
          {save.isPending && <Spinner />}
          {t("cloud.save")}
        </Button>
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
        )}
      </div>
    </form>
  )
}

/** The team's cloud connections, under Settings. */
export function CloudProvidersCard() {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirm = useConfirm()
  const providers = useCloudProviders()
  const [adding, setAdding] = useState(false)
  const canEdit = team?.role === "admin" || team?.role === "owner"
  const path = `/api/teams/${team?.id}/cloud-providers`

  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: ["cloud-providers", team?.id] })
  const test = useMutation({
    mutationFn: (id: string) => api.post<CloudProvider>(`${path}/${id}/test`),
    onSuccess: (provider) => {
      refresh()
      toast.success(t("cloud.tested", { name: provider.name }))
    },
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`${path}/${id}`),
    onSuccess: refresh,
  })

  const items = providers.data?.items ?? []
  return (
    <Card>
      <CardHeader className={cardHeaderWithActions}>
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <CloudIcon className="size-4" />
            {t("cloud.title")}
          </CardTitle>
          <CardDescription>{t("cloud.help")}</CardDescription>
        </div>
        {canEdit && !adding && (
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            <PlusIcon className="size-4" />
            {t("cloud.add")}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {providers.isLoading ? (
          <Skeleton className="h-16" />
        ) : providers.error ? (
          <ErrorDisplay error={providers.error} onRetry={() => void providers.refetch()} />
        ) : items.length === 0 && !adding ? (
          <p className="text-sm text-muted-foreground">{t("cloud.empty")}</p>
        ) : (
          items.length > 0 && (
            <ul className="divide-y rounded-md border">
              {items.map((provider) => (
                <li
                  key={provider.id}
                  className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm"
                >
                  <span className="font-medium">{provider.name}</span>
                  <span className="text-muted-foreground">{provider.title}</span>
                  <code className="font-mono text-xs text-muted-foreground">
                    {t("cloud.tokenHint", { hint: provider.token_hint })}
                  </code>
                  <span className="min-w-0 flex-1 text-xs text-muted-foreground">
                    {t("cloud.serverCount", { servers: provider.servers })}
                    {provider.checked_at
                      ? ` · ${t("cloud.checked", { when: formatRelative(provider.checked_at) })}`
                      : ""}
                  </span>
                  {canEdit && (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={test.isPending}
                        onClick={() => test.mutate(provider.id)}
                      >
                        <RefreshCwIcon className="size-4" />
                        {t("cloud.test")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("cloud.remove", { name: provider.name })}
                        disabled={remove.isPending}
                        onClick={() =>
                          void confirm({
                            title: t("cloud.remove", { name: provider.name }),
                            description: t("cloud.removeConfirm"),
                            confirmLabel: t("common.delete"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) remove.mutate(provider.id)
                          })
                        }
                      >
                        <Trash2Icon className="size-4" />
                      </Button>
                    </>
                  )}
                </li>
              ))}
            </ul>
          )
        )}

        {adding && (
          <CloudProviderForm onSaved={() => setAdding(false)} onCancel={() => setAdding(false)} />
        )}
        {test.error != null && <ErrorDisplay error={test.error} compact />}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>
    </Card>
  )
}
