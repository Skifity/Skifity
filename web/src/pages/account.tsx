import { useState } from "react"
import { useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  CopyIcon,
  DownloadIcon,
  FingerprintIcon,
  KeyRoundIcon,
  PencilIcon,
  PlusIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { copyText } from "@/lib/clipboard"
import { downloadText } from "@/lib/download"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
import {
  createPasskey,
  passkeyFailure,
  passkeysSupported,
  type PasskeyCreationOptions,
} from "@/lib/passkeys"
import { queryClient } from "@/lib/query"
import type { APIToken, Passkey, Session } from "@/lib/types"

/** What /api/me/totp answers with when two-factor setup begins. */
type TwoFactorSetup = {
  secret: string
  uri: string
  recovery_codes: string[]
  /** The URI drawn as a QR code, an SVG document. */
  qr_svg?: string
}

export function AccountPage() {
  const { t } = useTranslation()
  const { user, refresh } = useSession()
  const [params] = useSearchParams()

  return (
    <Page width="narrow">
      <PageHeader title={t("nav.account")} description={t("auth.subtitle")} />

      {params.get("recovery") === "1" && user?.is_admin && !user.recovery_saved && (
        <RecoveryReminder onAcknowledged={() => void refresh()} />
      )}

      <ProfileCard />
      <PasswordCard />
      <TwoFactorCard />
      <PasskeysCard />
      <SingleSignOnCard />
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
    if (key) downloadText("skifity-recovery-key.txt", `${key}\n`)
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
                {acknowledge.isPending && <Spinner />}
                {t("auth.recoveryKeyConfirm")}
              </Button>
            </div>
            {/* A failure here was silent, and the reminder simply stayed. */}
            {acknowledge.error != null && <ErrorDisplay error={acknowledge.error} compact />}
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
    onSuccess: () => {
      toast.success(t("auth.profileSaved"))
      void refresh()
    },
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
  const { hasPassword } = useSession()
  // An account that signs in only through the identity provider has no
  // current password to type. It was shown this form anyway, told elsewhere
  // to set a password here, and each try counted as a wrong guess.
  return hasPassword ? <ChangePasswordCard /> : <SetPasswordCard />
}

function ChangePasswordCard() {
  const { t } = useTranslation()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")

  const change = useMutation({
    mutationFn: () =>
      api.post("/api/me/password", { current_password: current, new_password: next }),
    onSuccess: () => {
      setCurrent("")
      setNext("")
      toast.success(t("auth.passwordChanged"))
      // The server signed every other device out; the list should say so.
      void queryClient.invalidateQueries({ queryKey: ["sessions"] })
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

/**
 * A first password, for an account that signs in through single sign-on.
 *
 * It asks for no current one, because there is none. The panel wants a
 * recent sign-in instead, which such an account has for a few minutes after
 * coming back from the provider — the help says so, because the step-up
 * dialog that opens otherwise asks for a password this account does not have.
 */
function SetPasswordCard() {
  const { t } = useTranslation()
  const { refresh } = useSession()
  const [next, setNext] = useState("")
  const [again, setAgain] = useState("")
  const differ = again.length > 0 && again !== next

  const set = useMutation({
    mutationFn: () => api.put("/api/me/password", { new_password: next }),
    onSuccess: () => {
      setNext("")
      setAgain("")
      toast.success(t("auth.passwordSet"))
      void refresh()
      void queryClient.invalidateQueries({ queryKey: ["sessions"] })
      // Unlinking the provider is possible now there is another way in.
      void queryClient.invalidateQueries({ queryKey: ["me-sso"] })
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("auth.setPasswordTitle")}</CardTitle>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            set.mutate()
          }}
        >
          <p className="text-sm text-muted-foreground">{t("auth.setPasswordHelp")}</p>
          <Field>
            <FieldLabel htmlFor="set-password">{t("auth.newPassword")}</FieldLabel>
            <Input
              id="set-password"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(event) => setNext(event.target.value)}
              required
            />
          </Field>
          <Field data-invalid={differ || undefined}>
            <FieldLabel htmlFor="set-password-again">{t("auth.newPasswordAgain")}</FieldLabel>
            <Input
              id="set-password-again"
              type="password"
              autoComplete="new-password"
              value={again}
              aria-invalid={differ || undefined}
              onChange={(event) => setAgain(event.target.value)}
              required
            />
            {differ && <FieldError>{t("auth.passwordsDiffer")}</FieldError>}
          </Field>
          {set.error != null && <ErrorDisplay error={set.error} compact />}
          <div className="flex justify-end">
            <Button type="submit" disabled={set.isPending || !next || next !== again}>
              {set.isPending && <Spinner />}
              {set.isPending ? t("common.saving") : t("auth.setPassword")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

function TwoFactorCard() {
  const { t } = useTranslation()
  const { user, teams, recoveryCodesLeft, refresh } = useSession()
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null)
  const [code, setCode] = useState("")
  // The recovery codes the panel has just made, on screen until the person
  // says they have them. They used to go the moment the first code was
  // confirmed, which was the only copy there would ever be.
  const [codes, setCodes] = useState<string[] | null>(null)
  const confirmReplace = useConfirm()
  const confirmDisable = useConfirm()

  const start = useMutation({
    mutationFn: () => api.post<TwoFactorSetup>("/api/me/totp"),
    onSuccess: (data) => {
      setSetup(data)
      setCodes(data.recovery_codes)
    },
  })

  const turnOn = useMutation({
    mutationFn: () => api.post("/api/me/totp/confirm", { code: code.trim() }),
    onSuccess: () => {
      setSetup(null)
      setCode("")
      void refresh()
      // Turning it on signs every other device out.
      void queryClient.invalidateQueries({ queryKey: ["sessions"] })
    },
  })

  const regenerate = useMutation({
    mutationFn: () => api.post<{ recovery_codes: string[] }>("/api/me/totp/recovery-codes"),
    onSuccess: (data) => {
      setCodes(data.recovery_codes)
      void refresh()
    },
  })

  const disable = useMutation({
    mutationFn: () => api.delete("/api/me/totp"),
    onSuccess: () => {
      setCodes(null)
      void refresh()
    },
  })

  // Teams that would refuse this account the moment it signs in with a
  // password alone. Named, because "you may be locked out of something" is a
  // sentence nobody acts on.
  const strict = teams.filter((candidate) => candidate.require_strong_auth)

  const askDisable = () => {
    void confirmDisable({
      title: t("auth.twoFactorDisableTitle"),
      description: t("auth.twoFactorDisableConfirm"),
      consequence:
        strict.length > 0
          ? t("auth.twoFactorDisableStrongTeams", {
              teams: strict.map((candidate) => candidate.name).join(", "),
            })
          : undefined,
      confirmLabel: t("auth.twoFactorDisable"),
      destructive: true,
    }).then((yes) => {
      if (yes) disable.mutate()
    })
  }

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
        {codes?.length ? <RecoveryCodes codes={codes} onDone={() => setCodes(null)} /> : null}

        {user?.totp_enabled ? (
          <>
            <p className="flex items-center gap-2 text-sm text-success">
              <ShieldCheckIcon className="size-4 shrink-0" />
              {t("auth.twoFactorEnabled")}
            </p>
            {recoveryCodesLeft > 0 ? (
              <p className="text-sm text-muted-foreground">
                {t("auth.recoveryCodesLeft", { count: recoveryCodesLeft })}
              </p>
            ) : (
              <p className="text-sm text-warning">{t("auth.recoveryCodesNone")}</p>
            )}
            {disable.error != null && <ErrorDisplay error={disable.error} compact />}
            {regenerate.error != null && <ErrorDisplay error={regenerate.error} compact />}
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                disabled={regenerate.isPending}
                onClick={() => {
                  void confirmReplace({
                    title: t("auth.recoveryCodesRegenerate"),
                    description: t("auth.recoveryCodesIntro"),
                    consequence: t("auth.recoveryCodesReplaced"),
                    confirmLabel: t("auth.recoveryCodesRegenerate"),
                  }).then((yes) => {
                    if (yes) regenerate.mutate()
                  })
                }}
              >
                {regenerate.isPending && <Spinner />}
                {t("auth.recoveryCodesRegenerate")}
              </Button>
              <Button variant="ghost" disabled={disable.isPending} onClick={askDisable}>
                {disable.isPending && <Spinner />}
                {t("auth.twoFactorDisable")}
              </Button>
            </div>
          </>
        ) : setup ? (
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              turnOn.mutate()
            }}
          >
            <p className="text-sm text-muted-foreground">{t("auth.twoFactorIntro")}</p>
            {setup.qr_svg && (
              <img
                src={`data:image/svg+xml;utf8,${encodeURIComponent(setup.qr_svg)}`}
                alt={t("auth.twoFactorQRAlt")}
                // White whatever the theme: an authenticator reads dark on
                // light, and the drawing carries its own quiet zone.
                className="size-48 rounded-md border bg-white"
              />
            )}
            {/* On a phone there is nothing to scan with: the same URI opens
                the authenticator app directly. */}
            <a
              href={setup.uri}
              className="inline-flex min-h-6 items-center text-sm underline underline-offset-4"
            >
              {t("auth.twoFactorOpenApp")}
            </a>
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
            {turnOn.error != null && <ErrorDisplay error={turnOn.error} compact />}
            <div className="flex gap-2">
              <Button type="submit" disabled={turnOn.isPending || code.length < 6}>
                {turnOn.isPending && <Spinner />}
                {t("auth.twoFactorConfirm")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  // The codes do nothing until two-factor is on, and starting
                  // again makes a new set.
                  setSetup(null)
                  setCodes(null)
                }}
              >
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
 * Passkeys: signing in with a fingerprint, a face or a device PIN.
 *
 * Adding one and removing one ask for the password again; the API client opens
 * that dialog by itself when the panel says so. The list is shown even where
 * passkeys cannot be used at the moment, so one made for an old address can
 * still be removed.
 */
function PasskeysCard() {
  const { t } = useTranslation()
  const { meta } = useSession()
  const confirmRemove = useConfirm()
  const [name, setName] = useState("")
  const [renaming, setRenaming] = useState<Passkey | null>(null)

  const available = meta?.passkeys?.available === true
  const supported = passkeysSupported()

  const passkeys = useQuery({
    queryKey: ["passkeys"],
    queryFn: () => api.get<List<Passkey>>("/api/me/passkeys"),
  })

  const add = useMutation({
    mutationFn: async () => {
      const options = await api.post<PasskeyCreationOptions>("/api/me/passkeys/register")
      const credential = await createPasskey(options)
      return api.post<Passkey>("/api/me/passkeys", { name: name.trim(), credential })
    },
    onSuccess: () => {
      setName("")
      toast.success(t("auth.passkeys.added"))
      void queryClient.invalidateQueries({ queryKey: ["passkeys"] })
    },
  })

  const remove = useMutation({
    mutationFn: (passkeyID: string) => api.delete(`/api/me/passkeys/${passkeyID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["passkeys"] }),
  })

  // When it was the browser that did not make one, it says why in its own
  // terms rather than the panel's.
  const addTrouble = add.error != null ? passkeyFailure(add.error) : null
  const items = passkeys.data?.items ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <FingerprintIcon className="size-4" />
          {t("auth.passkeys.title")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">{t("auth.passkeys.help")}</p>

        {passkeys.isLoading ? (
          <Skeleton className="h-16" />
        ) : passkeys.error ? (
          <ErrorDisplay error={passkeys.error} onRetry={() => void passkeys.refetch()} />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("auth.passkeys.none")}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("auth.passkeys.name")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("common.created")}</TableHead>
                <TableHead className="hidden md:table-cell">
                  {t("auth.passkeys.lastUsed")}
                </TableHead>
                <TableHead className="w-20" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((passkey) => (
                <TableRow key={passkey.id}>
                  <TableCell>
                    <div className="font-medium">{passkey.name}</div>
                    {(passkey.backup_eligible || passkey.elsewhere) && (
                      <div className="mt-1 flex flex-wrap gap-1">
                        {passkey.backup_eligible && (
                          <Badge variant="secondary" className="text-[10px]">
                            {t("auth.passkeys.synced")}
                          </Badge>
                        )}
                        {passkey.elsewhere && (
                          <Badge variant="outline" className="text-[10px]">
                            {t("auth.passkeys.elsewhere")}
                          </Badge>
                        )}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="hidden text-xs sm:table-cell">
                    {formatDateTime(passkey.created_at)}
                  </TableCell>
                  <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                    {passkey.last_used_at
                      ? formatRelative(passkey.last_used_at)
                      : t("common.never")}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("auth.passkeys.rename")}
                        onClick={() => setRenaming(passkey)}
                      >
                        <PencilIcon className="size-4 text-muted-foreground" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("auth.passkeys.remove")}
                        disabled={remove.isPending}
                        onClick={() =>
                          void confirmRemove({
                            title: t("auth.passkeys.remove"),
                            description: t("auth.passkeys.removeConfirm", { name: passkey.name }),
                            consequence: t("auth.passkeys.removeConsequence"),
                            confirmLabel: t("auth.passkeys.remove"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) remove.mutate(passkey.id)
                          })
                        }
                      >
                        <Trash2Icon className="size-4 text-muted-foreground" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        {!available ? (
          <p className="text-sm text-muted-foreground">
            {t("auth.passkeys.unavailable")}{" "}
            <a
              href="/docs/configuration#put-a-domain-on-the-panel"
              className="underline underline-offset-4 hover:text-foreground"
            >
              {t("auth.passkeys.unavailableLink")}
            </a>
          </p>
        ) : !supported ? (
          <p className="text-sm text-muted-foreground">{t("auth.passkeys.unsupported")}</p>
        ) : (
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(event) => {
              event.preventDefault()
              add.mutate()
            }}
          >
            <div className="min-w-48 flex-1 space-y-2">
              <Label htmlFor="passkey-name">{t("auth.passkeys.name")}</Label>
              <Input
                id="passkey-name"
                value={name}
                maxLength={64}
                placeholder={t("auth.passkeys.namePlaceholder")}
                onChange={(event) => setName(event.target.value)}
                required
              />
            </div>
            <Button type="submit" disabled={!name.trim() || add.isPending}>
              {add.isPending ? <Spinner /> : <PlusIcon className="size-4" />}
              {t("auth.passkeys.add")}
            </Button>
          </form>
        )}

        {add.error != null &&
          (addTrouble === null ? (
            <ErrorDisplay error={add.error} compact />
          ) : (
            <p
              className={
                addTrouble === "cancelled"
                  ? "text-sm text-muted-foreground"
                  : "text-sm text-destructive"
              }
            >
              {t(`auth.passkeys.failure.${addTrouble}`)}
            </p>
          ))}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>

      <Dialog
        open={renaming !== null}
        onOpenChange={(open) => {
          if (!open) setRenaming(null)
        }}
      >
        <DialogContent className="sm:max-w-sm">
          {/* Keyed, so the field starts from this passkey's name each time
              rather than being copied into state by an effect. */}
          {renaming && (
            <RenamePasskey key={renaming.id} passkey={renaming} onDone={() => setRenaming(null)} />
          )}
        </DialogContent>
      </Dialog>
    </Card>
  )
}

function RenamePasskey({ passkey, onDone }: { passkey: Passkey; onDone: () => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState(passkey.name)

  const rename = useMutation({
    mutationFn: () => api.patch<Passkey>(`/api/me/passkeys/${passkey.id}`, { name: name.trim() }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["passkeys"] })
      onDone()
    },
  })

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        rename.mutate()
      }}
    >
      <DialogHeader>
        <DialogTitle>{t("auth.passkeys.renameTitle")}</DialogTitle>
        <DialogDescription>{t("auth.passkeys.renameHelp")}</DialogDescription>
      </DialogHeader>
      <Field>
        <FieldLabel htmlFor="passkey-rename">{t("auth.passkeys.name")}</FieldLabel>
        <Input
          id="passkey-rename"
          value={name}
          maxLength={64}
          onChange={(event) => setName(event.target.value)}
          autoFocus
          required
        />
      </Field>
      {rename.error != null && <ErrorDisplay error={rename.error} compact />}
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" disabled={!name.trim() || rename.isPending}>
          {rename.isPending && <Spinner />}
          {t("common.save")}
        </Button>
      </DialogFooter>
    </form>
  )
}

/**
 * The recovery codes, at the one moment they are readable.
 *
 * The panel stores only their hashes, so this screen is the only copy that
 * will ever exist. That is why there is a download button and not just a
 * paragraph telling somebody to write them down.
 */
/** What /api/me/sso answers with. */
type SingleSignOnLink = {
  available: boolean
  label?: string
  linked: Array<{ issuer: string; email: string; created_at: string }>
  can_unlink: boolean
}

/**
 * Linking this account to the identity provider.
 *
 * Single sign-on finds a returning person by who the provider says they are,
 * not by their address, so an account that already has a password is only
 * joined to a provider account by somebody signed into it — here, after typing
 * the password again. The panel sends the browser to the provider and back.
 */
function SingleSignOnCard() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const confirmUnlink = useConfirm()
  const outcome = params.get("sso")

  const sso = useQuery({
    queryKey: ["me-sso"],
    queryFn: () => api.get<SingleSignOnLink>("/api/me/sso"),
  })

  const link = useMutation({
    mutationFn: () => api.post<{ url: string }>("/api/me/sso/link"),
    onSuccess: (data) => window.location.assign(data.url),
  })

  const unlink = useMutation({
    mutationFn: () => api.delete("/api/me/sso"),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["me-sso"] }),
  })

  if (sso.isLoading) return <Skeleton className="h-24" />
  if (sso.error) return <ErrorDisplay error={sso.error} onRetry={() => void sso.refetch()} />
  const current = sso.data
  if (!current || (!current.available && current.linked.length === 0)) return null

  const provider = current.label ?? t("auth.ssoProviderFallback")

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("auth.ssoCardTitle")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {outcome === "linked" && (
          <Alert>
            <AlertDescription>{t("auth.ssoLinkedNotice")}</AlertDescription>
          </Alert>
        )}
        {(outcome === "taken" || outcome === "failed") && (
          <Alert variant="destructive">
            <AlertDescription>
              {t(outcome === "taken" ? "auth.ssoTakenNotice" : "auth.ssoFailedNotice")}
            </AlertDescription>
          </Alert>
        )}

        {current.linked.length > 0 ? (
          <>
            {current.linked.map((identity) => (
              <p key={identity.issuer + identity.email} className="text-sm">
                {t("auth.ssoLinked", { email: identity.email })}
              </p>
            ))}
            {current.can_unlink ? (
              <Button
                variant="outline"
                size="sm"
                disabled={unlink.isPending}
                onClick={async () => {
                  const confirmed = await confirmUnlink({
                    title: t("auth.ssoUnlinkTitle"),
                    description: t("auth.ssoUnlinkDescription"),
                    confirmLabel: t("auth.ssoUnlink"),
                    destructive: true,
                  })
                  if (confirmed) unlink.mutate()
                }}
              >
                {unlink.isPending && <Spinner />}
                {t("auth.ssoUnlink")}
              </Button>
            ) : (
              <p className="text-sm text-muted-foreground">{t("auth.ssoOnlyWayIn")}</p>
            )}
          </>
        ) : (
          <>
            <p className="text-sm text-muted-foreground">{t("auth.ssoCardHelp", { provider })}</p>
            <Button size="sm" disabled={link.isPending} onClick={() => link.mutate()}>
              {link.isPending && <Spinner />}
              {t("auth.ssoLink", { provider })}
            </Button>
          </>
        )}

        {link.error && <ErrorDisplay error={link.error} />}
        {unlink.error && <ErrorDisplay error={unlink.error} />}
      </CardContent>
    </Card>
  )
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const { t } = useTranslation()

  const download = () => downloadText("skifity-recovery-codes.txt", `${codes.join("\n")}\n`)

  const copy = () => {
    void copyText(codes.join("\n")).then((copied) =>
      copied ? toast.success(t("common.copied")) : toast.error(t("common.copyFailed")),
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
          <Button size="sm" onClick={onDone}>
            {t("auth.recoveryCodesConfirm")}
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
                <TableHead className="hidden sm:table-cell">{t("common.ipAddress")}</TableHead>
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

type TokenAccess = "full" | "read"
/** Never, or a number of days. */
const TOKEN_EXPIRY = ["never", "30", "90", "365"] as const
const EXPIRY_LABELS = {
  never: "auth.tokenExpiryNever",
  "30": "auth.tokenExpiry30",
  "90": "auth.tokenExpiry90",
  "365": "auth.tokenExpiry365",
} as const

function TokensCard() {
  const { t } = useTranslation()
  const confirmRevoke = useConfirm()
  // A token cannot grant more than the person creating it has, so it is scoped
  // to a team rather than to the account.
  const { team } = useSession()
  const [name, setName] = useState("")
  // Read only is the "read" scope, and an expiry is ttl_hours: the API took
  // both, and the form offered neither, so every token made here could do
  // everything its owner can, for ever.
  const [access, setAccess] = useState<TokenAccess>("full")
  const [expiry, setExpiry] = useState<(typeof TOKEN_EXPIRY)[number]>("never")
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
        scopes: access === "read" ? "read" : undefined,
        ttl_hours: expiry === "never" ? undefined : Number(expiry) * 24,
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
            <AlertDescription className="space-y-2">
              <pre className="mt-2 w-full rounded-md border bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap">
                {secret}
              </pre>
              {/* "Copy it now", with something to copy it with, and a way
                  to put it away once it has been. */}
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    void copyText(secret).then((copied) =>
                      copied
                        ? toast.success(t("common.copied"))
                        : toast.error(t("common.copyFailed")),
                    )
                  }}
                >
                  <CopyIcon className="size-4" />
                  {t("common.copy")}
                </Button>
                <Button size="sm" onClick={() => setSecret(null)}>
                  {t("common.done")}
                </Button>
              </div>
            </AlertDescription>
          </Alert>
        )}

        {tokens.isLoading ? (
          <Skeleton className="h-16" />
        ) : tokens.error ? (
          <ErrorDisplay error={tokens.error} onRetry={() => void tokens.refetch()} />
        ) : (tokens.data?.items.length ?? 0) === 0 ? (
          <p className="text-sm text-muted-foreground">{t("auth.tokensNone")}</p>
        ) : (
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
                    <div className="font-medium">
                      {token.name}
                      {/* A token limited some other way — a plugin's
                          apps:read — says what it is limited to, in the
                          scopes' own words. */}
                      {token.scopes === "read" ? (
                        <Badge variant="secondary" className="ml-2 text-[10px]">
                          {t("auth.tokenAccessRead")}
                        </Badge>
                      ) : (
                        token.scopes &&
                        token.scopes.split(",").map((scope) => (
                          <Badge
                            key={scope}
                            variant="secondary"
                            className="ml-2 font-mono text-[10px]"
                          >
                            {scope.trim()}
                          </Badge>
                        ))
                      )}
                    </div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {token.prefix}…
                      {token.expires_at &&
                        ` · ${t("auth.tokenExpiresOn", { date: formatDateTime(token.expires_at) })}`}
                    </div>
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
          <div className="space-y-2">
            <Label htmlFor="token-access">{t("auth.tokenAccess")}</Label>
            <Select value={access} onValueChange={(value) => setAccess(value as TokenAccess)}>
              <SelectTrigger id="token-access" className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="full">{t("auth.tokenAccessFull")}</SelectItem>
                <SelectItem value="read">{t("auth.tokenAccessRead")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="token-expiry">{t("auth.tokenExpiry")}</Label>
            <Select
              value={expiry}
              onValueChange={(value) => setExpiry(value as (typeof TOKEN_EXPIRY)[number])}
            >
              <SelectTrigger id="token-expiry" className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TOKEN_EXPIRY.map((option) => (
                  <SelectItem key={option} value={option}>
                    {t(EXPIRY_LABELS[option])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
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
