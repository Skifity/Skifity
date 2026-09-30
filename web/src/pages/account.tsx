import { useState } from "react"
import { useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  CopyIcon,
  DownloadIcon,
  KeyRoundIcon,
  PlusIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
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
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatDateTime, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { APIToken, Session } from "@/lib/types"

/** What /api/me/totp answers with when two-factor setup begins. */
type TwoFactorSetup = {
  secret: string
  uri: string
  recovery_codes: string[]
}

export function AccountPage() {
  const { t } = useTranslation()
  const { user, refresh } = useSession()
  const [params] = useSearchParams()

  return (
    <Page width="narrow">
      <PageHeader title={t("nav.account")} description={t("auth.subtitle")} />

      {params.get("recovery") === "1" && !user?.recovery_saved && (
        <RecoveryReminder onAcknowledged={() => void refresh()} />
      )}

      <ProfileCard />
      <PasswordCard />
      <TwoFactorCard />
      <SessionsCard />
      <TokensCard />
    </Page>
  )
}

function RecoveryReminder({ onAcknowledged }: { onAcknowledged: () => void }) {
  const { t } = useTranslation()
  const { meta } = useSession()
  const [key, setKey] = useState<string | null>(null)

  const reveal = useMutation({
    mutationFn: () => api.get<{ recovery_key: string }>("/api/security/recovery-key"),
    onSuccess: (data) => setKey(data.recovery_key),
  })

  const acknowledge = useMutation({
    mutationFn: () => api.post("/api/security/recovery-key/saved"),
    onSuccess: onAcknowledged,
  })

  const download = () => {
    if (!key) return
    const blob = new Blob([`${key}\n`], { type: "text/plain" })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = "skifity-recovery-key.txt"
    anchor.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Alert>
      <AlertTitle>{t("auth.recoveryKeyTitle")}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t("auth.recoveryKeyIntro", { product: meta?.product ?? "Skifity" })}</p>
        {key ? (
          <>
            <pre className="w-full rounded-md border bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap">
              {key}
            </pre>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" onClick={download}>
                <DownloadIcon className="size-4" />
                {t("auth.download")}
              </Button>
              <Button
                size="sm"
                disabled={acknowledge.isPending}
                onClick={() => acknowledge.mutate()}
              >
                {t("auth.recoveryKeyConfirm")}
              </Button>
            </div>
          </>
        ) : (
          <Button
            variant="outline"
            size="sm"
            disabled={reveal.isPending}
            onClick={() => reveal.mutate()}
          >
            <KeyRoundIcon className="size-4" />
            {t("settings.recoveryKey")}
          </Button>
        )}
        {reveal.error != null && <ErrorDisplay error={reveal.error} compact />}
      </AlertDescription>
    </Alert>
  )
}

function ProfileCard() {
  const { t } = useTranslation()
  const { user, refresh } = useSession()
  const [name, setName] = useState(user?.name ?? "")

  const save = useMutation({
    mutationFn: () => api.patch("/api/me", { name: name.trim() }),
    onSuccess: () => void refresh(),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("nav.account")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <Field>
          <FieldLabel htmlFor="account-email">{t("auth.email")}</FieldLabel>
          <Input id="account-email" value={user?.email ?? ""} readOnly disabled />
        </Field>
        <Field>
          <FieldLabel htmlFor="account-name">{t("auth.yourName")}</FieldLabel>
          <Input id="account-name" value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        {save.error != null && <ErrorDisplay error={save.error} compact />}
        <div className="flex justify-end">
          <Button disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending && <Spinner />}
            {save.isPending ? t("common.saving") : t("common.save")}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function PasswordCard() {
  const { t } = useTranslation()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")

  const change = useMutation({
    mutationFn: () =>
      api.post("/api/me/password", { current_password: current, new_password: next }),
    onSuccess: () => {
      setCurrent("")
      setNext("")
      toast.success(t("common.save"))
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("auth.newPassword")}</CardTitle>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            change.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="current-password">{t("auth.currentPassword")}</FieldLabel>
            <Input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={current}
              onChange={(event) => setCurrent(event.target.value)}
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="new-password">{t("auth.newPassword")}</FieldLabel>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(event) => setNext(event.target.value)}
              required
            />
          </Field>
          {change.error != null && <ErrorDisplay error={change.error} compact />}
          <div className="flex justify-end">
            <Button type="submit" disabled={change.isPending || !current || !next}>
              {change.isPending && <Spinner />}
              {change.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

function TwoFactorCard() {
  const { t } = useTranslation()
  const { user, recoveryCodesLeft, refresh } = useSession()
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null)
  const [code, setCode] = useState("")
  const confirmReplace = useConfirm()

  const start = useMutation({
    mutationFn: () => api.post<TwoFactorSetup>("/api/me/totp"),
    onSuccess: (data) => setSetup(data),
  })

  const confirm = useMutation({
    mutationFn: () => api.post("/api/me/totp/confirm", { code: code.trim() }),
    onSuccess: () => {
      setSetup(null)
      setCode("")
      void refresh()
    },
  })

  const disable = useMutation({
    mutationFn: () => api.delete("/api/me/totp"),
    onSuccess: () => void refresh(),
  })

  const codesLeft = recoveryCodesLeft

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ShieldCheckIcon className="size-4" />
          {t("auth.twoFactorTitle")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* Codes are shown whenever the panel has just generated them, on or
            off: this is the only moment they exist in readable form. */}
        {setup?.recovery_codes?.length ? <RecoveryCodes codes={setup.recovery_codes} /> : null}

        {user?.totp_enabled ? (
          <>
            <p className="flex items-center gap-2 text-sm text-success">
              <ShieldCheckIcon className="size-4 shrink-0" />
              {t("auth.twoFactorEnabled")}
            </p>
            {codesLeft > 0 ? (
              <p className="text-sm text-muted-foreground">
                {t("auth.recoveryCodesLeft", { count: codesLeft })}
              </p>
            ) : (
              <p className="text-sm text-warning">{t("auth.recoveryCodesNone")}</p>
            )}
            {disable.error != null && <ErrorDisplay error={disable.error} compact />}
            {start.error != null && <ErrorDisplay error={start.error} compact />}
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                disabled={start.isPending}
                onClick={() => {
                  void confirmReplace({
                    title: t("auth.recoveryCodesRegenerate"),
                    description: t("auth.recoveryCodesIntro"),
                    consequence: t("auth.recoveryCodesReplaced"),
                    confirmLabel: t("auth.recoveryCodesRegenerate"),
                  }).then((yes) => {
                    if (yes) start.mutate()
                  })
                }}
              >
                {start.isPending && <Spinner />}
                {t("auth.recoveryCodesRegenerate")}
              </Button>
              <Button variant="ghost" disabled={disable.isPending} onClick={() => disable.mutate()}>
                {t("auth.twoFactorDisable")}
              </Button>
            </div>
          </>
        ) : setup ? (
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              confirm.mutate()
            }}
          >
            <p className="text-sm text-muted-foreground">{t("auth.twoFactorIntro")}</p>
            <div className="space-y-2">
              <Label>{t("auth.twoFactorSecret")}</Label>
              <pre className="rounded-md border bg-muted p-3 font-mono text-xs break-all">
                {setup.secret}
              </pre>
            </div>
            <Field>
              <FieldLabel htmlFor="totp-code">{t("auth.twoFactorCode")}</FieldLabel>
              <Input
                id="totp-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(event) => setCode(event.target.value)}
                className="max-w-32 font-mono tracking-widest"
                required
              />
            </Field>
            {confirm.error != null && <ErrorDisplay error={confirm.error} compact />}
            <div className="flex gap-2">
              <Button type="submit" disabled={confirm.isPending || code.length < 6}>
                {confirm.isPending && <Spinner />}
                {t("auth.twoFactorConfirm")}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setSetup(null)}>
                {t("common.cancel")}
              </Button>
            </div>
          </form>
        ) : (
          <>
            <p className="flex items-center gap-2 text-sm text-warning">
              <ShieldAlertIcon className="size-4 shrink-0" />
              {t("auth.twoFactorOff")}
            </p>
            {start.error != null && <ErrorDisplay error={start.error} compact />}
            <Button variant="outline" disabled={start.isPending} onClick={() => start.mutate()}>
              {start.isPending && <Spinner />}
              {t("auth.enableTwoFactor")}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * The recovery codes, at the one moment they are readable.
 *
 * The panel stores only their hashes, so this screen is the only copy that
 * will ever exist. That is why there is a download button and not just a
 * paragraph telling somebody to write them down.
 */
function RecoveryCodes({ codes }: { codes: string[] }) {
  const { t } = useTranslation()

  const download = () => {
    const blob = new Blob([`${codes.join("\n")}\n`], { type: "text/plain" })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = "skifity-recovery-codes.txt"
    anchor.click()
    URL.revokeObjectURL(url)
  }

  const copy = () => {
    void navigator.clipboard.writeText(codes.join("\n")).then(
      () => toast.success(t("common.copied")),
      () => toast.error(t("errors.somethingWentWrong")),
    )
  }

  return (
    <Alert variant="warning">
      <KeyRoundIcon />
      <AlertTitle>{t("auth.recoveryCodesTitle")}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t("auth.recoveryCodesIntro")}</p>
        <ul className="grid w-full grid-cols-2 gap-x-6 gap-y-1 rounded-md border bg-background p-3 font-mono text-xs sm:grid-cols-4">
          {codes.map((recoveryCode) => (
            <li key={recoveryCode} className="tabular-nums">
              {recoveryCode}
            </li>
          ))}
        </ul>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={download}>
            <DownloadIcon className="size-4" />
            {t("auth.download")}
          </Button>
          <Button variant="outline" size="sm" onClick={copy}>
            <CopyIcon className="size-4" />
            {t("common.copy")}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  )
}

function SessionsCard() {
  const { t } = useTranslation()
  const confirmRevoke = useConfirm()

  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api.get<List<Session>>("/api/me/sessions"),
  })

  const revoke = useMutation({
    mutationFn: (sessionID: string) => api.delete(`/api/me/sessions/${sessionID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["sessions"] }),
  })

  // One button, because the moment somebody wants this — a laptop left on a
  // train — is not the moment to work through a list deciding which row is
  // which device.
  const revokeOthers = useMutation({
    mutationFn: () => api.delete("/api/me/sessions"),
    onSuccess: () => {
      toast.success(t("auth.signedOutEverywhereDone"))
      void queryClient.invalidateQueries({ queryKey: ["sessions"] })
    },
  })

  const others = (sessions.data?.items ?? []).filter((session) => !session.current).length

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between gap-2 space-y-0">
        <CardTitle className="text-base">{t("auth.sessions")}</CardTitle>
        {others > 0 && (
          <Button
            variant="outline"
            size="sm"
            disabled={revokeOthers.isPending}
            onClick={() =>
              void confirmRevoke({
                title: t("auth.signOutEverywhere"),
                description: t("auth.signOutEverywhereConfirm"),
                confirmLabel: t("auth.signOutEverywhere"),
                destructive: true,
              }).then((yes) => {
                if (yes) revokeOthers.mutate()
              })
            }
          >
            {revokeOthers.isPending && <Spinner />}
            {t("auth.signOutEverywhere")}
          </Button>
        )}
      </CardHeader>
      <CardContent className="p-0">
        {sessions.isLoading ? (
          <div className="px-6 pb-6">
            <Skeleton className="h-24" />
          </div>
        ) : sessions.error ? (
          <div className="px-6 pb-6">
            <ErrorDisplay error={sessions.error} onRetry={() => void sessions.refetch()} />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.type")}</TableHead>
                <TableHead className="hidden sm:table-cell">IP</TableHead>
                <TableHead>{t("servers.lastSeen")}</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sessions.data?.items.map((session) => (
                <TableRow key={session.id}>
                  <TableCell className="max-w-xs truncate text-xs">
                    {session.user_agent || t("common.unknown")}
                    {session.current && (
                      <Badge variant="secondary" className="ml-2 text-[10px]">
                        {t("auth.currentSession")}
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="hidden font-mono text-xs sm:table-cell">
                    {session.ip}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatRelative(session.last_seen_at)}
                  </TableCell>
                  <TableCell>
                    {!session.current && (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("auth.revokeSession")}
                        disabled={revoke.isPending}
                        onClick={() =>
                          void confirmRevoke({
                            title: t("auth.revokeSession"),
                            description: t("auth.revokeSessionConfirm", {
                              device: session.user_agent || t("common.unknown"),
                            }),
                            confirmLabel: t("auth.revokeSession"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) revoke.mutate(session.id)
                          })
                        }
                      >
                        <Trash2Icon className="size-4 text-muted-foreground" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}

function TokensCard() {
  const { t } = useTranslation()
  const confirmRevoke = useConfirm()
  // A token cannot grant more than the person creating it has, so it is scoped
  // to a team rather than to the account.
  const { team } = useSession()
  const [name, setName] = useState("")
  const [secret, setSecret] = useState<string | null>(null)

  const tokens = useQuery({
    queryKey: ["tokens"],
    queryFn: () => api.get<List<APIToken>>("/api/me/tokens"),
  })

  const create = useMutation({
    mutationFn: () =>
      api.post<{ secret: string }>("/api/me/tokens", {
        name: name.trim(),
        team_id: team?.id,
      }),
    onSuccess: (data) => {
      setSecret(data.secret)
      setName("")
      void queryClient.invalidateQueries({ queryKey: ["tokens"] })
    },
  })

  const revoke = useMutation({
    mutationFn: (tokenID: string) => api.delete(`/api/me/tokens/${tokenID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["tokens"] }),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("auth.apiTokens")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">{t("auth.apiTokensHelp")}</p>

        {secret && (
          <Alert>
            <AlertTitle>{t("auth.tokenShownOnce")}</AlertTitle>
            <AlertDescription>
              <pre className="mt-2 w-full rounded-md border bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap">
                {secret}
              </pre>
            </AlertDescription>
          </Alert>
        )}

        {(tokens.data?.items.length ?? 0) > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.name")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("common.created")}</TableHead>
                <TableHead className="hidden md:table-cell">{t("servers.lastSeen")}</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.data?.items.map((token) => (
                <TableRow key={token.id}>
                  <TableCell>
                    <div className="font-medium">{token.name}</div>
                    <div className="font-mono text-xs text-muted-foreground">{token.prefix}…</div>
                  </TableCell>
                  <TableCell className="hidden text-xs sm:table-cell">
                    {formatDateTime(token.created_at)}
                  </TableCell>
                  <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                    {token.last_used_at ? formatRelative(token.last_used_at) : t("common.never")}
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("auth.revokeToken")}
                      disabled={revoke.isPending}
                      // A token cannot be got back, and whatever was using it
                      // stops working the moment this lands. The bin icon next
                      // to it was the whole of the question.
                      onClick={() =>
                        void confirmRevoke({
                          title: t("auth.revokeToken"),
                          description: t("auth.revokeTokenConfirm", { name: token.name }),
                          consequence: t("auth.revokeTokenConsequence"),
                          confirmLabel: t("auth.revokeToken"),
                          destructive: true,
                        }).then((yes) => {
                          if (yes) revoke.mutate(token.id)
                        })
                      }
                    >
                      <Trash2Icon className="size-4 text-muted-foreground" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <div className="min-w-48 flex-1 space-y-2">
            <Label htmlFor="token-name">{t("auth.tokenName")}</Label>
            <Input
              id="token-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
            />
          </div>
          <Button type="submit" disabled={!name.trim() || !team || create.isPending}>
            <PlusIcon className="size-4" />
            {t("auth.newToken")}
          </Button>
        </form>

        {create.error != null && <ErrorDisplay error={create.error} compact />}
        {revoke.error != null && <ErrorDisplay error={revoke.error} compact />}
      </CardContent>
    </Card>
  )
}
