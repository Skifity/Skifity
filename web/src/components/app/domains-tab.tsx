import { useState, type ReactNode } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArrowRightIcon,
  BookOpenIcon,
  CircleCheckIcon,
  CircleDashedIcon,
  ExternalLinkIcon,
  GlobeIcon,
  InfoIcon,
  LockIcon,
  PlusIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { ErrorDisplay } from "@/components/error-display"
import { dnsProviderTitles, useDNSProviders } from "@/components/settings/dns-providers"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { ApiError, api, type List } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { AddedDomain, App, DNSCheck, DNSProvider, Domain } from "@/lib/types"

export function DomainsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [hostname, setHostname] = useState("")
  const [adding, setAdding] = useState(false)
  // What each domain's DNS said when it was added. Adding one answers with
  // its DNS, so the first thing somebody sees after adding a domain is
  // whether it already points here, without pressing anything.
  const [dnsWhenAdded, setDNSWhenAdded] = useState<Record<string, DNSCheck>>({})
  // The switch for creating the record, on by default. Whether it is shown
  // follows from the hostname typed and the zones the team connected.
  const [manageDNS, setManageDNS] = useState(true)

  const domains = useQuery({
    queryKey: ["domains", app.id],
    queryFn: () => api.get<List<Domain>>(`/api/apps/${app.id}/domains`),
  })
  const providers = useDNSProviders()
  const zone = app.internal ? null : zoneFor(hostname, providers.data?.items ?? [])

  const add = useMutation({
    mutationFn: () =>
      api.post<AddedDomain>(`/api/apps/${app.id}/domains`, {
        hostname: hostname.trim(),
        ...(zone ? { manage_dns: manageDNS } : {}),
      }),
    onSuccess: (added) => {
      void queryClient.invalidateQueries({ queryKey: ["domains", app.id] })
      const dns = added.dns
      if (dns) setDNSWhenAdded((current) => ({ ...current, [added.id]: dns }))
      setHostname("")
      setManageDNS(true)
      setAdding(false)
    },
  })

  const remove = useMutation({
    mutationFn: (domainID: string) => api.delete(`/api/apps/${app.id}/domains/${domainID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["domains", app.id] }),
  })

  // One hostname sending its visitors to another of the app's: www to the
  // bare domain, or an old name to the new one.
  const redirect = useMutation({
    mutationFn: ({ domainID, target }: { domainID: string; target: string }) =>
      api.patch<Domain>(`/api/apps/${app.id}/domains/${domainID}`, { redirect_to: target }),
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
              {zone && (
                <Field orientation="horizontal">
                  <Switch id="manage-dns" checked={manageDNS} onCheckedChange={setManageDNS} />
                  <div className="space-y-1">
                    <FieldLabel htmlFor="manage-dns">
                      {t("domains.manageDNS", { provider: zone.provider.name })}
                    </FieldLabel>
                    <FieldDescription>
                      {t("domains.manageDNSHelp", {
                        zone: zone.zone,
                        provider: dnsProviderTitles[zone.provider.kind],
                      })}
                    </FieldDescription>
                  </div>
                </Field>
              )}
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
                {domain.redirect_to && (
                  <Badge variant="outline" className="gap-1 text-[10px]">
                    <ArrowRightIcon className="size-2.5" />
                    {domain.redirect_to}
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

              {items.length > 1 && (
                <div className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="text-muted-foreground">{t("domains.visitors")}</span>
                  <Select
                    value={domain.redirect_to || "serve"}
                    disabled={redirect.isPending}
                    onValueChange={(value) =>
                      redirect.mutate({
                        domainID: domain.id,
                        target: value === "serve" ? "" : value,
                      })
                    }
                  >
                    <SelectTrigger
                      className="h-8 w-auto min-w-56"
                      aria-label={t("domains.visitors")}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="serve">{t("domains.servesTheApp")}</SelectItem>
                      {items
                        .filter((other) => other.id !== domain.id && !other.redirect_to)
                        .map((other) => (
                          <SelectItem key={other.id} value={other.hostname}>
                            {t("domains.redirectTo", { hostname: other.hostname })}
                          </SelectItem>
                        ))}
                    </SelectContent>
                  </Select>
                </div>
              )}

              {/* A redirecting hostname is answered here too, so its DNS has
                  to point here like any other's. */}
              {domain.managed_dns && <ManagedRecord app={app} domain={domain} />}
              {!domain.auto && (
                <DomainDNS app={app} domain={domain} whenAdded={dnsWhenAdded[domain.id]} />
              )}
              {domain.redirect_to && (
                <p className="text-sm text-muted-foreground">
                  {t("domains.redirectHelp", {
                    hostname: domain.hostname,
                    target: domain.redirect_to,
                  })}
                </p>
              )}

              {domain.certificate && <OwnCertificate domain={domain} />}

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
      {redirect.error != null && <ErrorDisplay error={redirect.error} compact />}
    </div>
  )
}

/**
 * Whether a domain's DNS points here, on demand.
 *
 * A certificate that stays "waiting" looked the same whether the record was
 * right and a minute old or pointed at the old host, and only one of those
 * fixes itself. The panel asks DNS and says which, with the address it found.
 * The answer shown is the latest one: this button's, or the one that came
 * back with adding the domain.
 */
function DomainDNS({ app, domain, whenAdded }: { app: App; domain: Domain; whenAdded?: DNSCheck }) {
  const { t } = useTranslation()
  const check = useMutation({
    mutationFn: () => api.post<DNSCheck>(`/api/apps/${app.id}/domains/${domain.id}/check`),
    // The certificate's state may have moved on as well, and it is on the
    // same card.
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ["domains", app.id] }),
  })
  const result = check.data ?? whenAdded

  const button = (
    <Button variant="outline" size="sm" disabled={check.isPending} onClick={() => check.mutate()}>
      {check.isPending ? <Spinner /> : <RefreshCwIcon className="size-3.5" />}
      {result ? t("domains.checkAgain") : t("domains.checkDns")}
    </Button>
  )

  // A record the panel keeps, or one that already points here, is not one
  // to create by hand: the card above says where it stands.
  const kept = ["created", "elsewhere", "pending"].includes(domain.managed_dns?.state ?? "")

  return (
    <>
      {domain.status === "pending" && !kept ? (
        <DNSInstructions domain={domain} action={button} />
      ) : (
        <div>{button}</div>
      )}
      {check.error != null && <ErrorDisplay error={check.error} compact />}
      {result && !check.isPending && <DNSResult check={result} domain={domain} />}
    </>
  )
}

/**
 * What DNS said, in one sentence and the records behind it.
 *
 * The certificate is mentioned where it is still to come, because the two are
 * one wait to the person watching: cert-manager cannot prove a domain it is
 * not reached at, so the certificate follows the record, and saying so turns
 * "waiting" into something with a cause.
 */
function DNSResult({ check, domain }: { check: DNSCheck; domain: Domain }) {
  const { t } = useTranslation()
  const addresses = check.found
    .filter((record) => record.type !== "CNAME" && !record.here)
    .map((record) => record.value)
    .join(", ")
  const found = check.found.map((record) => record.value).join(", ")
  const expected = check.expected[0] ?? ""
  // Still waiting for its certificate: soon once DNS is right. One that has
  // stopped trying says so above this, with what to do about it. A hostname
  // on one of the team's own certificates has nothing to wait for.
  const certificateToCome = domain.tls && !domain.certificate && domain.status !== "active"
  const certificateSoon = domain.tls && domain.status === "pending"

  const shown = {
    here: {
      variant: "success" as const,
      icon: <CircleCheckIcon />,
      title: t("domains.dnsStatusHere"),
      help: certificateSoon ? t("domains.dnsCertificateSoon") : t("domains.dnsHereHelp"),
    },
    partly: {
      variant: "warning" as const,
      icon: <TriangleAlertIcon />,
      title: t("domains.dnsStatusPartly"),
      help: t("domains.dnsPartlyHelp", { addresses, expected }),
    },
    elsewhere: {
      variant: "warning" as const,
      icon: <TriangleAlertIcon />,
      title: t("domains.dnsStatusElsewhere", { addresses }),
      help: t("domains.dnsElsewhereHelp", { expected }),
    },
    missing: {
      variant: "info" as const,
      icon: <CircleDashedIcon />,
      title: t("domains.dnsStatusMissing"),
      help: t("domains.dnsMissingHelp"),
    },
    unknown: {
      variant: "default" as const,
      icon: <InfoIcon />,
      title: t("domains.dnsStatusUnknown", { addresses: found }),
      help: t("domains.dnsUnknownHelp"),
    },
    // Cloudflare's addresses, and none of the cluster's: behind its proxy
    // or a tunnel. Where it sends visitors is in the record at Cloudflare,
    // which the panel can vouch for only when it keeps that record.
    proxied: {
      variant: check.origin_confirmed ? ("success" as const) : ("info" as const),
      icon: check.origin_confirmed ? <CircleCheckIcon /> : <InfoIcon />,
      title: t("domains.dnsStatusProxied"),
      help: check.origin_confirmed
        ? t("domains.dnsProxiedConfirmed")
        : t("domains.dnsProxiedHelp", { expected }),
    },
  }[check.status]

  return (
    <Alert variant={shown.variant}>
      {shown.icon}
      <AlertTitle>{shown.title}</AlertTitle>
      <AlertDescription className="space-y-1">
        <p>{shown.help}</p>
        {certificateToCome && !check.points_here && check.status !== "unknown" && (
          <p>{t("domains.dnsCertificateAfter")}</p>
        )}
        {check.found.length > 0 && (
          <p className="text-xs">
            {t("domains.dnsRecordsFound")}{" "}
            <span className="font-mono">
              {check.found.map((record) => `${record.type} ${record.value}`).join(" · ")}
            </span>
          </p>
        )}
      </AlertDescription>
    </Alert>
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
function DNSInstructions({ domain, action }: { domain: Domain; action: ReactNode }) {
  const { t } = useTranslation()
  const target = domain.dns_target ?? ""

  if (target === "") {
    return (
      <Alert>
        <AlertTitle>{t("domains.dnsInstructions")}</AlertTitle>
        <AlertDescription className="space-y-3">
          <p>{t("domains.dnsUnknown")}</p>
          <div>{action}</div>
        </AlertDescription>
      </Alert>
    )
  }

  // A colon is IPv6, digits and dots are IPv4; anything else is a name.
  const type = target.includes(":") ? "AAAA" : /^[\d.]+$/.test(target) ? "A" : "CNAME"

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
              {domain.dns_target_ipv6 && (
                <TableRow>
                  <TableCell className="py-2 font-mono text-xs font-medium">AAAA</TableCell>
                  <DNSCell value={domain.hostname} label={t("domains.recordName")} />
                  <DNSCell value={domain.dns_target_ipv6} label={t("domains.recordValue")} />
                </TableRow>
              )}
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
          {action}
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

/**
 * Which of the team's own certificates serves this hostname, and until when.
 *
 * Matching is automatic — a hostname a certificate covers uses it — so this is
 * where somebody finds out that it does, and it says so loudly once the date
 * is close, because nothing renews a certificate somebody brought.
 */
function OwnCertificate({ domain }: { domain: Domain }) {
  const { t } = useTranslation()
  const certificate = domain.certificate
  if (!certificate) return null
  const name = certificate.name
  const date = formatDate(certificate.not_after)
  const settings = (
    <Link
      to="/settings?tab=certificates"
      className="underline underline-offset-2 hover:text-primary"
    >
      {t("domains.manageCertificates")}
    </Link>
  )

  if (certificate.state === "valid") {
    return (
      <p className="flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground">
        <ShieldCheckIcon className="size-3.5 shrink-0" />
        {t("domains.ownCertificate", { name, date })}
        <span aria-hidden>·</span>
        {settings}
      </p>
    )
  }
  const expired = certificate.state === "expired"
  return (
    <Alert variant={expired ? "destructive" : "warning"}>
      <TriangleAlertIcon />
      <AlertTitle>
        {expired
          ? t("domains.ownCertificateExpired", { name, date })
          : t("domains.ownCertificateExpiring", { name, date })}
      </AlertTitle>
      <AlertDescription>
        <p>
          {t("domains.ownCertificateRenew")} {settings}
        </p>
      </AlertDescription>
    </Alert>
  )
}

/** The connected zone a hostname is in: the longest, as the panel picks it. */
function zoneFor(
  hostname: string,
  providers: DNSProvider[],
): { provider: DNSProvider; zone: string } | null {
  const name = hostname.trim().toLowerCase().replace(/\.$/, "")
  let best: { provider: DNSProvider; zone: string } | null = null
  if (!name.includes(".")) return null
  for (const provider of providers) {
    for (const zone of provider.zones) {
      const covers = name === zone.name || name.endsWith(`.${zone.name}`)
      if (covers && (!best || zone.name.length > best.zone.length)) {
        best = { provider, zone: zone.name }
      }
    }
  }
  return best
}

/**
 * The domain's record at the team's DNS provider: created and kept, somebody
 * else's that already points here, refused — with the record in the way and
 * what to do — or left to the person. The switch is here too.
 */
function ManagedRecord({ app, domain }: { app: App; domain: Domain }) {
  const { t } = useTranslation()
  const managed = domain.managed_dns
  const change = useMutation({
    mutationFn: (manage: boolean) =>
      api.patch<Domain>(`/api/apps/${app.id}/domains/${domain.id}`, { manage_dns: manage }),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ["domains", app.id] }),
  })
  if (!managed) return null
  const provider = managed.provider_name
  const values = managed.records.map((record) => `${record.type} ${record.content}`).join(" · ")

  const stop = (
    <Button
      variant="ghost"
      size="sm"
      disabled={change.isPending}
      onClick={() => change.mutate(false)}
    >
      {t("domains.stopKeeping")}
    </Button>
  )
  const keep = (label: string) => (
    <Button
      variant="outline"
      size="sm"
      disabled={change.isPending}
      onClick={() => change.mutate(true)}
    >
      {change.isPending ? <Spinner /> : <RefreshCwIcon className="size-3.5" />}
      {label}
    </Button>
  )

  let body: ReactNode
  switch (managed.state) {
    case "created":
      body = (
        <Alert variant="success">
          <CircleCheckIcon />
          <AlertTitle>{t("domains.dnsCreated", { provider })}</AlertTitle>
          <AlertDescription className="space-y-2">
            <p>{t("domains.dnsCreatedHelp")}</p>
            <p className="flex flex-wrap items-center gap-2 font-mono text-xs">
              {values}
              {managed.records.some((record) => record.proxied) && (
                <Badge variant="outline" className="text-[10px]">
                  {t("domains.dnsProxied")}
                </Badge>
              )}
            </p>
            <div>{stop}</div>
          </AlertDescription>
        </Alert>
      )
      break
    case "elsewhere":
      body = (
        <Alert>
          <InfoIcon />
          <AlertTitle>{t("domains.dnsManagedElsewhere")}</AlertTitle>
          <AlertDescription className="space-y-2">
            <p>{t("domains.dnsManagedElsewhereHelp", { provider, zone: managed.zone })}</p>
            <div>{stop}</div>
          </AlertDescription>
        </Alert>
      )
      break
    case "pending":
      body = (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner />
          {t("domains.dnsCreating", { provider })}
        </p>
      )
      break
    case "refused":
    case "failed":
      body = (
        <div className="space-y-2">
          <p className="text-sm font-medium">{t("domains.dnsNotCreated", { provider })}</p>
          {managed.problem && <ErrorDisplay error={problemError(managed.problem)} />}
          <div className="flex flex-wrap gap-2">
            {keep(t("domains.tryAgain"))}
            {stop}
          </div>
        </div>
      )
      break
    default:
      body = (
        <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
          <span>
            {managed.state === "off"
              ? t("domains.dnsLeftToYou", { provider, zone: managed.zone })
              : t("domains.dnsNotAsked", { provider, zone: managed.zone })}
          </span>
          {keep(t("domains.keepRecord", { provider }))}
        </div>
      )
  }

  return (
    <div className="space-y-2">
      {body}
      {change.error != null && <ErrorDisplay error={change.error} compact />}
    </div>
  )
}

/** A stored problem, shown the way any failure is. */
function problemError(problem: NonNullable<NonNullable<Domain["managed_dns"]>["problem"]>) {
  return new ApiError(problem, 409)
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
    case "expiring":
      return "Expiring"
    case "expired":
      return "Expired"
    default:
      return "Pending"
  }
}
