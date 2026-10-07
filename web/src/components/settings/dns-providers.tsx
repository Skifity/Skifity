import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BookOpenIcon, GlobeIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useDeleteConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
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
import type { DNSProvider, DNSProviderKind } from "@/lib/types"
import { cardActions, cardHeaderWithActions } from "@/lib/layout"

/** The providers' own names, which are the same in every language. */
export const dnsProviderTitles: Record<DNSProviderKind, string> = {
  cloudflare: "Cloudflare",
  hetzner: "Hetzner",
  digitalocean: "DigitalOcean",
  route53: "Amazon Route 53",
}

const kinds: DNSProviderKind[] = ["cloudflare", "hetzner", "digitalocean", "route53"]

/** The team's DNS providers, as every page that needs them reads them. */
export function useDNSProviders() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["dns-providers", team?.id],
    queryFn: () => api.get<List<DNSProvider>>(`/api/teams/${team?.id}/dns-providers`),
    enabled: Boolean(team),
  })
}

/**
 * The DNS providers the team has connected, where the panel creates its
 * domains' records: Cloudflare, Hetzner, DigitalOcean and Route 53.
 *
 * The credentials go up once and never come back. The provider is asked for
 * its zones before anything is kept, so a token that cannot do the job is
 * refused here rather than at the first domain.
 */
export function DNSProvidersCard() {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirmDelete = useDeleteConfirm()
  const canChange = team?.role === "owner" || team?.role === "admin"
  const path = `/api/teams/${team?.id}/dns-providers`
  const providers = useDNSProviders()

  const [adding, setAdding] = useState(false)
  const [kind, setKind] = useState<DNSProviderKind>("cloudflare")
  const [name, setName] = useState("")
  const [token, setToken] = useState("")
  const [accessKeyID, setAccessKeyID] = useState("")
  const [secret, setSecret] = useState("")

  // The credentials are not kept in the page a moment longer than the form.
  const close = () => {
    setAdding(false)
    setName("")
    setToken("")
    setAccessKeyID("")
    setSecret("")
  }
  // A domain's record follows the zones, so the Domains tabs read again too.
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["dns-providers", team?.id] })
    void queryClient.invalidateQueries({ queryKey: ["domains"] })
  }

  const connect = useMutation({
    mutationFn: () =>
      api.post<DNSProvider>(
        path,
        kind === "route53"
          ? { kind, name: name.trim(), access_key_id: accessKeyID, secret_access_key: secret }
          : { kind, name: name.trim(), token },
      ),
    onSuccess: (connected) => {
      refresh()
      close()
      toast.success(
        t("dnsProviders.connected", {
          name: connected.name,
          zones: connected.zones.map((zone) => zone.name).join(", "),
        }),
      )
    },
  })

  const test = useMutation({
    mutationFn: (id: string) => api.post<DNSProvider>(`${path}/${id}/test`),
    onSuccess: (tested) => {
      refresh()
      toast.success(
        t("dnsProviders.tested", {
          name: tested.name,
          zones: tested.zones.map((zone) => zone.name).join(", "),
        }),
      )
    },
  })

  const remove = useMutation({
    mutationFn: (id: string) => api.delete<{ left: number }>(`${path}/${id}`),
    onSuccess: refresh,
  })

  const complete = kind === "route53" ? accessKeyID.trim() && secret.trim() : token.trim()
  const items = providers.data?.items ?? []
  return (
    <Card>
      <CardHeader className={cardHeaderWithActions}>
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <GlobeIcon className="size-4" />
            {t("dnsProviders.title")}
          </CardTitle>
          <CardDescription>{t("dnsProviders.help")}</CardDescription>
        </div>
        <div className={cardActions}>
          <Button variant="ghost" size="sm" asChild>
            <a href="/docs/concepts#connecting-a-dns-provider" target="_blank" rel="noreferrer">
              <BookOpenIcon className="size-3.5" />
              {t("nav.documentation")}
            </a>
          </Button>
          {canChange && !adding && (
            <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("dnsProviders.add")}
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {providers.isLoading ? (
          <Skeleton className="h-20" />
        ) : providers.error ? (
          <ErrorDisplay error={providers.error} onRetry={() => void providers.refetch()} />
        ) : items.length === 0 && !adding ? (
          <p className="text-sm text-muted-foreground">{t("dnsProviders.empty")}</p>
        ) : (
          items.length > 0 && (
            <ul className="divide-y rounded-md border">
              {items.map((provider) => (
                <li key={provider.id} className="space-y-2 px-3 py-3 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{provider.name}</span>
                    <Badge variant="secondary" className="text-[10px]">
                      {dnsProviderTitles[provider.kind]}
                    </Badge>
                    <span className="text-xs text-muted-foreground">
                      {t("dnsProviders.recordsKept", { number: provider.records })}
                    </span>
                    {canChange && (
                      <span className="ml-auto flex items-center gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={test.isPending}
                          onClick={() => test.mutate(provider.id)}
                        >
                          {test.isPending && test.variables === provider.id ? (
                            <Spinner />
                          ) : (
                            <RefreshCwIcon className="size-3.5" />
                          )}
                          {t("dnsProviders.test")}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t("dnsProviders.remove", { name: provider.name })}
                          disabled={remove.isPending}
                          onClick={() =>
                            void confirmDelete(
                              provider.name,
                              t("dnsProviders.removeConfirm", { name: provider.name }),
                              t("dnsProviders.removeConsequence", { number: provider.records }),
                            ).then((yes) => {
                              if (yes) remove.mutate(provider.id)
                            })
                          }
                        >
                          <Trash2Icon className="size-4" />
                        </Button>
                      </span>
                    )}
                  </div>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {provider.zones.map((zone) => (
                      <Badge key={zone.id} variant="outline" className="font-mono text-[11px]">
                        {zone.name}
                      </Badge>
                    ))}
                    {provider.zones_listed_at && (
                      <span className="text-xs text-muted-foreground">
                        {t("dnsProviders.zonesListed", {
                          when: formatRelative(provider.zones_listed_at),
                        })}
                      </span>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )
        )}

        {test.error != null && <ErrorDisplay error={test.error} compact />}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}

        {adding && (
          <form
            className="grid gap-3 sm:grid-cols-2"
            onSubmit={(event) => {
              event.preventDefault()
              connect.mutate()
            }}
          >
            <Field>
              <FieldLabel htmlFor="dns-kind">{t("dnsProviders.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(value) => setKind(value as DNSProviderKind)}>
                <SelectTrigger id="dns-kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {kinds.map((value) => (
                    <SelectItem key={value} value={value}>
                      {dnsProviderTitles[value]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="dns-name">{t("dnsProviders.name")}</FieldLabel>
              <Input
                id="dns-name"
                value={name}
                placeholder={dnsProviderTitles[kind]}
                maxLength={64}
                onChange={(event) => setName(event.target.value)}
              />
            </Field>
            {kind === "route53" ? (
              <>
                <Field>
                  <FieldLabel htmlFor="dns-access-key">{t("dnsProviders.accessKeyID")}</FieldLabel>
                  <Input
                    id="dns-access-key"
                    value={accessKeyID}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(event) => setAccessKeyID(event.target.value)}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="dns-secret">{t("dnsProviders.secretAccessKey")}</FieldLabel>
                  <Input
                    id="dns-secret"
                    type="password"
                    value={secret}
                    autoComplete="new-password"
                    onChange={(event) => setSecret(event.target.value)}
                  />
                </Field>
              </>
            ) : (
              <Field className="sm:col-span-2">
                <FieldLabel htmlFor="dns-token">{t("dnsProviders.token")}</FieldLabel>
                <Input
                  id="dns-token"
                  type="password"
                  value={token}
                  autoComplete="new-password"
                  onChange={(event) => setToken(event.target.value)}
                />
              </Field>
            )}
            <FieldDescription className="sm:col-span-2">
              {t(`dnsProviders.formHelp.${kind}`)}
            </FieldDescription>
            {kind === "cloudflare" && (
              <FieldDescription className="sm:col-span-2">
                {t("dnsProviders.proxiedHelp")}
              </FieldDescription>
            )}
            {connect.error != null && (
              <div className="sm:col-span-2">
                <ErrorDisplay error={connect.error} compact />
              </div>
            )}
            <div className="flex gap-2 sm:col-span-2">
              <Button type="submit" disabled={connect.isPending || !complete}>
                {connect.isPending && <Spinner />}
                {t("dnsProviders.connect")}
              </Button>
              <Button type="button" variant="ghost" onClick={close}>
                {t("common.cancel")}
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  )
}
