import { useRef, useState } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  BookOpenIcon,
  FileUpIcon,
  PlusIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { useDeleteConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { ErrorDisplay } from "@/components/error-display"
import { StatusBadge } from "@/components/status-badge"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatDate, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { SavedCertificate, TeamCertificate } from "@/lib/types"

/**
 * Certificates the team brings for its own hostnames: a company CA's, an EV
 * or OV certificate, a wildcard bought elsewhere, or one for a hostname Let's
 * Encrypt cannot reach. A hostname one of them covers is served with it, on
 * every app in the team, with nothing to choose per domain.
 *
 * The private key goes up once and never comes back: the panel seals it on
 * arrival and no answer carries it, so there is nothing here to show it with.
 */
export function CertificatesCard() {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirmDelete = useDeleteConfirm()
  const canChange = team?.role === "owner" || team?.role === "admin"
  const path = `/api/teams/${team?.id}/certificates`

  const certificates = useQuery({
    queryKey: ["certificates", team?.id],
    queryFn: () => api.get<List<TeamCertificate>>(path),
    enabled: Boolean(team),
  })

  const [adding, setAdding] = useState(false)
  const [name, setName] = useState("")
  const [chain, setChain] = useState("")
  const [key, setKey] = useState("")

  // The key is not kept in the page a moment longer than the form is open.
  const close = () => {
    setAdding(false)
    setName("")
    setChain("")
    setKey("")
  }
  const openFor = (existing: string) => {
    setName(existing)
    setChain("")
    setKey("")
    setAdding(true)
  }

  // Which domains use which certificate is on every app's Domains tab too.
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["certificates", team?.id] })
    void queryClient.invalidateQueries({ queryKey: ["domains"] })
  }

  const save = useMutation({
    mutationFn: () =>
      api.post<SavedCertificate>(path, {
        name: name.trim(),
        certificate: chain,
        private_key: key,
      }),
    onSuccess: (saved) => {
      refresh()
      close()
      const notes = [
        saved.reordered ? t("certificates.reordered") : "",
        saved.updating > 0 ? t("certificates.updating", { count: saved.updating }) : "",
      ].filter(Boolean)
      toast.success(
        saved.replaced
          ? t("certificates.replaced", { name: saved.certificate.name })
          : t("certificates.saved", { name: saved.certificate.name }),
        { description: notes.length > 0 ? notes.join(" ") : undefined },
      )
    },
  })

  const remove = useMutation({
    mutationFn: (id: string) => api.delete<{ updating: number }>(`${path}/${id}`),
    onSuccess: (answer) => {
      refresh()
      if (answer.updating > 0) toast.success(t("certificates.updating", { count: answer.updating }))
    },
  })

  const items = certificates.data?.items ?? []
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldCheckIcon className="size-4" />
            {t("certificates.title")}
          </CardTitle>
          <CardDescription>{t("certificates.help")}</CardDescription>
        </div>
        <div className="flex shrink-0 gap-2">
          <Button variant="ghost" size="sm" asChild>
            <a href="/docs/quick-start#your-own-certificate" target="_blank" rel="noreferrer">
              <BookOpenIcon className="size-3.5" />
              {t("nav.documentation")}
            </a>
          </Button>
          {canChange && !adding && (
            <Button variant="outline" size="sm" onClick={() => openFor("")}>
              <PlusIcon className="size-4" />
              {t("certificates.add")}
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {certificates.isLoading ? (
          <Skeleton className="h-20" />
        ) : certificates.error ? (
          <ErrorDisplay error={certificates.error} onRetry={() => void certificates.refetch()} />
        ) : items.length === 0 && !adding ? (
          <p className="text-sm text-muted-foreground">{t("certificates.empty")}</p>
        ) : (
          items.length > 0 && (
            <ul className="divide-y rounded-md border">
              {items.map((certificate) => (
                <li key={certificate.id} className="space-y-2 px-3 py-3 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{certificate.name}</span>
                    <StatusBadge
                      status={certificate.state}
                      label={t(`certificates.state.${certificate.state}`)}
                    />
                    {certificate.self_signed && (
                      <Badge variant="outline" className="text-[10px]">
                        {t("certificates.selfSigned")}
                      </Badge>
                    )}
                    {canChange && (
                      <span className="ml-auto flex items-center gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={save.isPending}
                          onClick={() => openFor(certificate.name)}
                        >
                          <RefreshCwIcon className="size-3.5" />
                          {t("certificates.replace")}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t("certificates.remove", { name: certificate.name })}
                          disabled={remove.isPending}
                          onClick={() =>
                            void confirmDelete(
                              certificate.name,
                              t("certificates.removeConfirm", {
                                count: certificate.domains.length,
                              }),
                              t("certificates.removeConsequence"),
                            ).then((yes) => {
                              if (yes) remove.mutate(certificate.id)
                            })
                          }
                        >
                          <Trash2Icon className="size-4" />
                        </Button>
                      </span>
                    )}
                  </div>

                  <div className="flex flex-wrap gap-1">
                    {certificate.hostnames.map((hostname) => (
                      <code
                        key={hostname}
                        className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                      >
                        {hostname}
                      </code>
                    ))}
                  </div>

                  <p className="text-xs text-muted-foreground">
                    {t("certificates.details", {
                      issuer: certificate.issuer,
                      date: formatDate(certificate.not_after),
                      relative: formatRelative(certificate.not_after),
                      keyType: certificate.key_type,
                    })}
                  </p>

                  <p className="text-xs text-muted-foreground">
                    {certificate.domains.length === 0 ? (
                      t("certificates.noDomains")
                    ) : (
                      <>
                        {t("certificates.usedBy")}{" "}
                        {certificate.domains.map((domain, index) => (
                          <span key={domain.id}>
                            {index > 0 && ", "}
                            <Link
                              to={`/apps/${domain.app_id}?tab=domains`}
                              className="font-mono hover:text-primary"
                            >
                              {domain.hostname}
                            </Link>{" "}
                            ({domain.app_name})
                          </span>
                        ))}
                      </>
                    )}
                  </p>

                  <p className="flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground">
                    <span className="shrink-0">{t("certificates.fingerprint")}</span>
                    <code className="truncate font-mono">{certificate.fingerprint}</code>
                    <CopyButton
                      value={certificate.fingerprint}
                      label={t("certificates.fingerprint")}
                      className="size-6 shrink-0"
                    />
                  </p>
                </li>
              ))}
            </ul>
          )
        )}

        {adding && (
          <form
            className="space-y-3"
            onSubmit={(event) => {
              event.preventDefault()
              save.mutate()
            }}
          >
            <Field>
              <FieldLabel htmlFor="certificate-name">{t("certificates.name")}</FieldLabel>
              <Input
                id="certificate-name"
                value={name}
                maxLength={100}
                placeholder={t("certificates.namePlaceholder")}
                onChange={(event) => setName(event.target.value)}
              />
              <FieldDescription>{t("certificates.nameHelp")}</FieldDescription>
            </Field>
            <PEMField
              id="certificate-chain"
              label={t("certificates.chain")}
              help={t("certificates.chainHelp")}
              placeholder="-----BEGIN CERTIFICATE-----"
              value={chain}
              onChange={setChain}
            />
            <PEMField
              id="certificate-key"
              label={t("certificates.key")}
              help={t("certificates.keyHelp")}
              placeholder="-----BEGIN PRIVATE KEY-----"
              value={key}
              onChange={setKey}
            />
            {save.error != null && <ErrorDisplay error={save.error} compact />}
            <div className="flex gap-2">
              <Button type="submit" disabled={save.isPending || !name.trim() || !chain.trim()}>
                {save.isPending && <Spinner />}
                {t("certificates.save")}
              </Button>
              <Button type="button" variant="ghost" onClick={close}>
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

/**
 * A box for PEM, pasted or read from a file. The file is read in the browser
 * and goes up with the form, exactly as if it had been pasted.
 */
function PEMField({
  id,
  label,
  help,
  placeholder,
  value,
  onChange,
}: {
  id: string
  label: string
  help: string
  placeholder: string
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const file = useRef<HTMLInputElement>(null)
  return (
    <Field>
      <div className="flex items-center justify-between gap-2">
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <Button type="button" variant="ghost" size="sm" onClick={() => file.current?.click()}>
          <FileUpIcon className="size-3.5" />
          {t("certificates.fromFile")}
        </Button>
      </div>
      <Textarea
        id={id}
        value={value}
        rows={6}
        spellCheck={false}
        autoComplete="off"
        placeholder={placeholder}
        className="max-h-64 font-mono text-xs"
        onChange={(event) => onChange(event.target.value)}
      />
      <Input
        ref={file}
        type="file"
        className="hidden"
        aria-hidden
        tabIndex={-1}
        accept=".pem,.crt,.cer,.key,.txt"
        onChange={(event) => {
          const picked = event.target.files?.[0]
          // Cleared at once, so picking the same file again still reads it.
          event.target.value = ""
          if (picked) void picked.text().then(onChange)
        }}
      />
      <FieldDescription>{help}</FieldDescription>
    </Field>
  )
}
