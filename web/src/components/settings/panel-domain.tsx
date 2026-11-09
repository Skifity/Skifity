import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  CircleCheckIcon,
  CircleDashedIcon,
  ExternalLinkIcon,
  GlobeIcon,
  InfoIcon,
  LockIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ApiError, api } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { DNSCheck, PanelDomain } from "@/lib/types"

/** Where Settings → General shows this card, for the links that point at it. */
export const PANEL_ADDRESS_LINK = "/settings?tab=panel#panel-address"

/** What taking the domain off answers: the panel restarting somewhere else. */
type Returned = { url: string; restarting: true }

/** A move under way, which the card asks about again until it is over. */
const underWay = (view: PanelDomain | undefined) =>
  view?.state === "moving" || view?.state === "restarting"

/** What somebody typed, as the panel will read it. */
const cleaned = (typed: string) =>
  typed
    .trim()
    .toLowerCase()
    .replace(/^https?:\/\//, "")
    .split("/")[0]
    .replace(/\.$/, "")

/**
 * Where the panel answers, and putting it on a domain of its own.
 *
 * A default install answers on an sslip.io address over plain HTTP, on
 * purpose (ADR-0015): the password crosses in clear, and the __Host- cookies
 * and passkeys need HTTPS. This is the way out from inside the panel: name a
 * domain whose record points here, and the panel asks for a certificate,
 * keeps answering here until it has one, then restarts at the domain. The
 * page follows it there, because the API client opens the new address as
 * soon as the old one says the panel has moved.
 *
 * The installer's way is said too: running it again with --domain does the
 * same from the server, and it is the only way when the panel cannot change
 * its own address.
 */
export function PanelDomainCard() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [typed, setTyped] = useState("")
  const domain = cleaned(typed)

  const view = useQuery({
    queryKey: ["panel-domain"],
    queryFn: () => api.get<PanelDomain>("/api/panel/domain"),
    refetchInterval: (query) => (underWay(query.state.data) ? 3000 : false),
  })
  const check = useMutation({
    mutationFn: (name: string) => api.post<DNSCheck>("/api/panel/domain/check", { domain: name }),
  })
  const move = useMutation({
    mutationFn: (body: { domain: string; ignore_dns?: boolean }) =>
      api.post<PanelDomain>("/api/panel/domain", body),
    onSuccess: (answer) => queryClient.setQueryData(["panel-domain"], answer),
  })
  const remove = useMutation({
    mutationFn: () => api.delete<PanelDomain | Returned>("/api/panel/domain"),
    onSuccess: (answer) => {
      if ("restarting" in answer) return
      queryClient.setQueryData(["panel-domain"], answer)
    },
  })

  const data = view.data
  if (view.isLoading) return <Skeleton className="h-40" />
  if (!data) {
    return <ErrorDisplay error={view.error} onRetry={() => void view.refetch()} />
  }

  const secure = data.scheme === "https"
  const returned = remove.data && "restarting" in remove.data ? remove.data : undefined
  const busy = underWay(data) || returned !== undefined
  const target = data.dns_target
  // A colon is IPv6, digits and dots are IPv4; anything else is a name.
  const recordType = target.includes(":") ? "AAAA" : /^[\d.]+$/.test(target) ? "A" : "CNAME"
  const looksLikeADomain = /^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(domain)
  const result = check.data?.hostname === domain ? check.data : undefined

  const askToMove = (name: string, ignoreDNS: boolean) =>
    void confirm({
      title: t("panelDomain.moveTitle", { domain: name }),
      description: t("panelDomain.moveConfirm", { domain: name }),
      confirmLabel: t("panelDomain.move"),
    }).then((yes) => {
      if (!yes) return
      check.reset()
      move.mutate({ domain: name, ...(ignoreDNS ? { ignore_dns: true } : {}) })
    })

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (looksLikeADomain) askToMove(domain, false)
  }

  return (
    <Card id="panel-address">
      <CardHeader>
        <CardTitle className="text-base">{t("panelDomain.title")}</CardTitle>
        <CardDescription>{t("panelDomain.help")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-muted-foreground">{t("panelDomain.answersAt")}</span>
          <span className="font-mono break-all">{data.url || "—"}</span>
          {secure ? (
            <Badge variant="secondary">
              <LockIcon />
              {t("panelDomain.https")}
            </Badge>
          ) : (
            <Badge variant="outline" className="border-warning/40 text-warning">
              {t("panelDomain.plainHttp")}
            </Badge>
          )}
        </div>

        {data.certificate?.state === "ready" && data.certificate.not_after && (
          <p className="text-xs text-muted-foreground">
            {t("panelDomain.certificateReady", { date: formatDate(data.certificate.not_after) })}
          </p>
        )}
        {data.certificate?.state === "issuing" && !underWay(data) && (
          <p className="text-xs text-muted-foreground">
            {t("panelDomain.certificateIssuing")}
            {data.certificate.message ? ` ${data.certificate.message}` : ""}
          </p>
        )}
        {!secure && !busy && (
          <p className="text-xs text-muted-foreground">{t("panelDomain.plainHelp")}</p>
        )}
        {data.previous.length > 0 && (
          <p className="text-xs text-muted-foreground">
            {t("panelDomain.previous", { hosts: data.previous.join(", ") })}
          </p>
        )}

        {data.state === "moving" && (
          <Alert variant="info">
            <Spinner />
            <AlertTitle>{t("panelDomain.moving", { domain: data.domain })}</AlertTitle>
            <AlertDescription>
              {t("panelDomain.movingHelp", { domain: data.domain })}
            </AlertDescription>
          </Alert>
        )}
        {data.state === "restarting" && (
          <Alert variant="info">
            <Spinner />
            <AlertTitle>{t("panelDomain.restarting", { domain: data.domain })}</AlertTitle>
            <AlertDescription className="space-y-2">
              <p>{t("panelDomain.restartingHelp", { domain: data.domain })}</p>
              <Button size="sm" variant="outline" asChild>
                <a href={`https://${data.domain}/settings?tab=panel`}>
                  <ExternalLinkIcon />
                  {t("panelDomain.open", { url: `https://${data.domain}` })}
                </a>
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {returned && (
          <Alert variant="info">
            <Spinner />
            <AlertDescription className="space-y-2">
              <p>{t("panelDomain.goingBack", { url: returned.url })}</p>
              <Button size="sm" variant="outline" asChild>
                <a href={`${returned.url}/settings?tab=panel`}>
                  <ExternalLinkIcon />
                  {t("panelDomain.open", { url: returned.url })}
                </a>
              </Button>
            </AlertDescription>
          </Alert>
        )}

        {data.state === "failed" && data.problem && !busy && (
          <div className="space-y-2">
            <p className="font-medium">{t("panelDomain.failed", { domain: data.domain })}</p>
            <ErrorDisplay error={new ApiError(data.problem, 409)} />
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                disabled={move.isPending || remove.isPending}
                onClick={() => askToMove(data.domain ?? "", false)}
              >
                {move.isPending && <Spinner />}
                {t("panelDomain.tryAgain")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={move.isPending || remove.isPending}
                onClick={() =>
                  void confirm({
                    title: t("panelDomain.cancelTitle", { domain: data.domain }),
                    description: t("panelDomain.cancelConfirm", { host: data.host }),
                    confirmLabel: t("panelDomain.cancel"),
                  }).then((yes) => {
                    if (yes) remove.mutate()
                  })
                }
              >
                {t("panelDomain.cancel")}
              </Button>
            </div>
          </div>
        )}

        {!data.movable && data.reason && (
          <ErrorDisplay error={new ApiError(data.reason, 400)} compact />
        )}

        {data.movable && !busy && (
          <form className="space-y-3" onSubmit={submit}>
            <Field>
              <FieldLabel htmlFor="panel-domain">
                {secure ? t("panelDomain.anotherDomain") : t("panelDomain.domain")}
              </FieldLabel>
              <Input
                id="panel-domain"
                value={typed}
                onChange={(event) => setTyped(event.target.value)}
                placeholder="panel.example.com"
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
              <FieldDescription>{t("panelDomain.domainHelp")}</FieldDescription>
            </Field>

            {looksLikeADomain && target !== "" && (
              <div className="space-y-2">
                <p className="text-xs text-muted-foreground">{t("domains.dnsIntro")}</p>
                <div className="overflow-x-auto rounded-md border bg-background">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="h-8 text-xs">{t("domains.recordType")}</TableHead>
                        <TableHead className="h-8 text-xs">{t("domains.recordName")}</TableHead>
                        <TableHead className="h-8 text-xs">{t("domains.recordValue")}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      <RecordRow type={recordType} name={domain} value={target} />
                      {data.dns_target_ipv6 && (
                        <RecordRow type="AAAA" name={domain} value={data.dns_target_ipv6} />
                      )}
                    </TableBody>
                  </Table>
                </div>
                <p className="text-xs text-muted-foreground">
                  {t("domains.dnsNameVaries")} {t("domains.dnsPropagation")}
                </p>
              </div>
            )}

            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!looksLikeADomain || check.isPending}
                onClick={() => check.mutate(domain)}
              >
                {check.isPending ? <Spinner /> : <RefreshCwIcon className="size-3.5" />}
                {t("domains.checkDns")}
              </Button>
              <Button type="submit" size="sm" disabled={!looksLikeADomain || move.isPending}>
                {move.isPending ? <Spinner /> : <GlobeIcon className="size-3.5" />}
                {t("panelDomain.move")}
              </Button>
            </div>

            {check.error != null && <ErrorDisplay error={check.error} compact />}
            {result && !check.isPending && <CheckResult check={result} />}
            {move.error != null && (
              <div className="space-y-2">
                <ErrorDisplay error={move.error} />
                {move.error instanceof ApiError &&
                  move.error.problem.code === "panel.domain_dns_mismatch" && (
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={() => askToMove(move.variables?.domain ?? domain, true)}
                    >
                      {t("panelDomain.moveAnyway")}
                    </Button>
                  )}
              </div>
            )}
          </form>
        )}

        {data.movable && secure && !busy && data.free_address && (
          <div className="flex flex-wrap items-center gap-2 border-t pt-4">
            <Button
              size="sm"
              variant="outline"
              disabled={remove.isPending}
              onClick={() =>
                void confirm({
                  title: t("panelDomain.goBackTitle"),
                  description: t("panelDomain.goBackConfirm", {
                    url: `http://${data.free_address}`,
                  }),
                  consequence: t("panelDomain.goBackConsequence", { domain: data.host }),
                  confirmLabel: t("panelDomain.goBack", { url: `http://${data.free_address}` }),
                  destructive: true,
                }).then((yes) => {
                  if (yes) remove.mutate()
                })
              }
            >
              {remove.isPending && <Spinner />}
              {t("panelDomain.goBack", { url: `http://${data.free_address}` })}
            </Button>
          </div>
        )}
        {remove.error != null && <ErrorDisplay error={remove.error} />}

        <p className="text-xs text-muted-foreground">{t("panelDomain.installer")}</p>
      </CardContent>
    </Card>
  )
}

/** One record to create, with the copy control the registrar's form wants. */
function RecordRow({ type, name, value }: { type: string; name: string; value: string }) {
  const { t } = useTranslation()
  return (
    <TableRow>
      <TableCell className="py-2 font-mono text-xs font-medium">{type}</TableCell>
      {[
        [name, t("domains.recordName")],
        [value, t("domains.recordValue")],
      ].map(([cell, label]) => (
        <TableCell key={label} className="py-2">
          <span className="flex items-center gap-1">
            <code className="truncate font-mono text-xs">{cell}</code>
            <CopyButton value={cell} label={label} className="size-7 shrink-0" />
          </span>
        </TableCell>
      ))}
    </TableRow>
  )
}

/**
 * What DNS said about the domain, in a sentence, with the records behind it.
 * Let's Encrypt asks DNS the same question before it issues the certificate,
 * so this is the answer to whether moving will work.
 */
function CheckResult({ check }: { check: DNSCheck }) {
  const { t } = useTranslation()
  const elsewhere = check.found
    .filter((record) => record.type !== "CNAME" && !record.here)
    .map((record) => record.value)
    .join(", ")
  const found = check.found.map((record) => record.value).join(", ")
  const expected = check.expected[0] ?? ""

  const shown = {
    here: {
      variant: "success" as const,
      icon: <CircleCheckIcon />,
      title: t("domains.dnsStatusHere"),
      help: t("panelDomain.dnsHere"),
    },
    partly: {
      variant: "warning" as const,
      icon: <TriangleAlertIcon />,
      title: t("domains.dnsStatusPartly"),
      help: t("panelDomain.dnsPartly", { addresses: elsewhere, expected }),
    },
    elsewhere: {
      variant: "warning" as const,
      icon: <TriangleAlertIcon />,
      title: t("domains.dnsStatusElsewhere", { addresses: elsewhere }),
      help: t("panelDomain.dnsElsewhere", { expected }),
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
      help: t("panelDomain.dnsUnknown"),
    },
    proxied: {
      variant: "info" as const,
      icon: <InfoIcon />,
      title: t("domains.dnsStatusProxied"),
      help: t("panelDomain.dnsProxied", { expected }),
    },
  }[check.status]

  return (
    <Alert variant={shown.variant}>
      {shown.icon}
      <AlertTitle>{shown.title}</AlertTitle>
      <AlertDescription className="space-y-1">
        <p>{shown.help}</p>
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
