import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  BookOpenIcon,
  ExternalLinkIcon,
  GlobeIcon,
  LockIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { ErrorDisplay } from "@/components/error-display"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Spinner } from "@/components/ui/spinner"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, Domain } from "@/lib/types"

export function DomainsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [hostname, setHostname] = useState("")
  const [adding, setAdding] = useState(false)

  const domains = useQuery({
    queryKey: ["domains", app.id],
    queryFn: () => api.get<List<Domain>>(`/api/apps/${app.id}/domains`),
  })

  const add = useMutation({
    mutationFn: () => api.post(`/api/apps/${app.id}/domains`, { hostname: hostname.trim() }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["domains", app.id] })
      setHostname("")
      setAdding(false)
    },
  })

  const remove = useMutation({
    mutationFn: (domainID: string) => api.delete(`/api/apps/${app.id}/domains/${domainID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["domains", app.id] }),
  })

  // One click on a bin icon used to take a live address off the internet. It
  // asks now — not with the name typed out, because adding it back is a
  // minute's work, but the certificate is not free to reissue and a mistake
  // here is visible to everybody who uses the site.
  const confirm = useConfirm()
  const askThenRemove = (domain: Domain) => {
    void confirm({
      title: t("domains.removeDomain"),
      description: t("domains.removeDomainConfirm", { hostname: domain.hostname }),
      confirmLabel: t("common.remove"),
      destructive: true,
    }).then((yes) => {
      if (yes) remove.mutate(domain.id)
    })
  }

  if (domains.isLoading) return <Skeleton className="h-48" />
  if (domains.error)
    return <ErrorDisplay error={domains.error} onRetry={() => void domains.refetch()} />

  const items = domains.data?.items ?? []

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="max-w-xl text-sm text-muted-foreground">
          {t("domains.automaticHelp", { product: "Skifity" })}
        </p>
        <Button size="sm" onClick={() => setAdding(true)}>
          <PlusIcon className="size-4" />
          {t("domains.addDomain")}
        </Button>
      </div>

      {adding && (
        <Card>
          <CardContent className="pt-6">
            <form
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                add.mutate()
              }}
            >
              <Field>
                <FieldLabel htmlFor="hostname">{t("domains.hostname")}</FieldLabel>
                <Input
                  id="hostname"
                  value={hostname}
                  onChange={(event) => setHostname(event.target.value)}
                  placeholder={t("domains.hostnamePlaceholder")}
                  autoFocus
                  required
                />
              </Field>
              {add.error != null && <ErrorDisplay error={add.error} compact />}
              <div className="flex justify-end gap-2">
                <Button type="button" variant="ghost" onClick={() => setAdding(false)}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" disabled={!hostname.trim() || add.isPending}>
                  {add.isPending && <Spinner />}
                  {add.isPending ? t("common.saving") : t("common.add")}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      <div className="space-y-3">
        {items.map((domain) => (
          <Card key={domain.id}>
            <CardContent className="space-y-3 py-4">
              <div className="flex flex-wrap items-center gap-3">
                <GlobeIcon className="size-4 shrink-0 text-muted-foreground" />
                <a
                  href={`${domain.tls ? "https" : "http"}://${domain.hostname}`}
                  target="_blank"
                  rel="noreferrer"
                  className="flex min-w-0 items-center gap-1.5 truncate font-medium hover:text-primary"
                >
                  <span className="truncate">{domain.hostname}</span>
                  <ExternalLinkIcon className="size-3.5 shrink-0" />
                </a>
                {domain.tls && (
                  <Badge variant="outline" className="gap-1 text-[10px]">
                    <LockIcon className="size-2.5" />
                    {t("domains.https")}
                  </Badge>
                )}
                {domain.auto && (
                  <Badge variant="secondary" className="text-[10px]">
                    {t("domains.automatic")}
                  </Badge>
                )}
                <StatusBadge
                  status={domain.status}
                  label={t(`domains.status${statusKey(domain.status)}`, {
                    defaultValue: domain.status,
                  })}
                  className="ml-auto"
                />
                {!domain.auto && (
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("domains.removeDomain")}
                    disabled={remove.isPending}
                    onClick={() => askThenRemove(domain)}
                  >
                    <Trash2Icon className="size-4 text-muted-foreground" />
                  </Button>
                )}
              </div>

              {domain.status === "pending" && !domain.auto && (
                <DNSInstructions
                  domain={domain}
                  rechecking={domains.isFetching}
                  onRecheck={() => void domains.refetch()}
                />
              )}

              {/* A certificate that stopped trying says why, and cert-manager's
                  reason is the only thing that actually explains it. */}
              {domain.status === "failed" && (
                <Alert variant="destructive">
                  <AlertTitle>{t("domains.certificateFailed")}</AlertTitle>
                  <AlertDescription>
                    {domain.status_detail || t("domains.certificateFailedHelp")}
                  </AlertDescription>
                </Alert>
              )}
            </CardContent>
          </Card>
        ))}
      </div>

      {remove.error != null && <ErrorDisplay error={remove.error} compact />}
    </div>
  )
}

/**
 * The record to create, as a record.
 *
 * Written as a sentence first — "Create an A record for blog.example.com
 * pointing to 203.0.113.10" — which is how somebody who already knows DNS
 * would say it, and not how anybody types it in. Every registrar's form has
 * three boxes: type, name, value. Tally, Okta, Klaviyo, AutoSend and Loops all
 * lay this out as those three columns with a copy control on each cell, and
 * that is the shape somebody is copying into, so that is the shape here.
 *
 * An address that is a name rather than a number is a CNAME, which is what an
 * install behind a load balancer has, so the type follows the value.
 */
function DNSInstructions({ domain, onRecheck, rechecking }: {
  domain: Domain
  onRecheck: () => void
  rechecking: boolean
}) {
  const { t } = useTranslation()
  const target = domain.dns_target ?? ""

  if (target === "") {
    return (
      <Alert>
        <AlertTitle>{t("domains.dnsInstructions")}</AlertTitle>
        <AlertDescription>{t("domains.dnsUnknown")}</AlertDescription>
      </Alert>
    )
  }

  // A colon is IPv6, digits and dots are IPv4; anything else is a name.
  const type = /^[\d.]+$|:/.test(target) ? "A" : "CNAME"

  return (
    <Alert>
      <AlertTitle>{t("domains.dnsInstructions")}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t("domains.dnsIntro")}</p>

        <div className="overflow-hidden rounded-md border bg-background">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="h-8 text-xs">{t("domains.recordType")}</TableHead>
                <TableHead className="h-8 text-xs">{t("domains.recordName")}</TableHead>
                <TableHead className="h-8 text-xs">{t("domains.recordValue")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow>
                <TableCell className="py-2 font-mono text-xs font-medium">{type}</TableCell>
                <DNSCell value={domain.hostname} label={t("domains.recordName")} />
                <DNSCell value={target} label={t("domains.recordValue")} />
              </TableRow>
            </TableBody>
          </Table>
        </div>

        {/* Okta says "the host format may vary by registrar" on this screen,
            and it is the mistake people actually make: half the registrars want
            the whole hostname in the Name box and half want only the label in
            front of the domain. The panel cannot tell which, because working
            out where a hostname's zone ends needs the public suffix list and
            gets .co.uk wrong. So it says so. */}
        <p className="text-xs text-muted-foreground">{t("domains.dnsNameVaries")}</p>

        <div className="flex flex-wrap items-center gap-3">
          {/* Waiting is the normal case and reads as a failure without this.
              Every product that asks for a DNS record says it on the same
              screen, because the alternative is somebody deciding after two
              minutes that the panel is broken. */}
          <p className="text-xs text-muted-foreground">{t("domains.dnsPropagation")}</p>
          <Button variant="outline" size="sm" disabled={rechecking} onClick={onRecheck}>
            {rechecking ? <Spinner /> : <RefreshCwIcon className="size-3.5" />}
            {t("domains.checkAgain")}
          </Button>
          <Button variant="ghost" size="sm" asChild>
            <a href="/docs/quick-start#4-add-your-own-domain" target="_blank" rel="noreferrer">
              <BookOpenIcon className="size-3.5" />
              {t("nav.documentation")}
            </a>
          </Button>
        </div>

        {/* cert-manager's own reason, when there is one: it is the only thing
            that explains a certificate that is still waiting. */}
        {domain.status_detail && (
          <p className="text-xs text-muted-foreground">{domain.status_detail}</p>
        )}
      </AlertDescription>
    </Alert>
  )
}

/** One cell of the record, with the copy control the registrar's form wants. */
function DNSCell({ value, label }: { value: string; label: string }) {
  return (
    <TableCell className="py-2">
      <span className="flex items-center gap-1">
        <code className="truncate font-mono text-xs">{value}</code>
        <CopyButton value={value} label={label} className="size-7 shrink-0" />
      </span>
    </TableCell>
  )
}

function statusKey(status: string): string {
  switch (status) {
    case "active":
      return "Active"
    case "failed":
      return "Failed"
    default:
      return "Pending"
  }
}
