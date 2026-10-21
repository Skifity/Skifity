import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import {
  CheckIcon,
  ClipboardIcon,
  DownloadIcon,
  KeyRoundIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { toast } from "sonner"

import { CenteredLayout } from "@/components/centered-layout"
import { ErrorDisplay } from "@/components/error-display"
import { Logo } from "@/components/logo"
import { PasswordStrength } from "@/components/password-strength"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldSeparator,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { downloadText } from "@/lib/download"
import { currentLanguage } from "@/lib/i18n"
import type { Team, User } from "@/lib/types"

/**
 * First-run setup.
 *
 * Two steps: create the account, then save the recovery key. The second step
 * cannot be skipped by navigating away, because a panel whose master key exists
 * only on one disk is one failed disk away from every secret being unreadable.
 */
/**
 * setupTokenFromLink reads the token the installer put in the link it printed.
 *
 * The installer finishes with a URL and a forty-character token, and the first
 * thing anybody does with a self-hosted panel is copy that token across by
 * hand. The link carries it instead, in the fragment rather than the query
 * string: a fragment is never sent to the server, so it cannot end up in an
 * access log, a proxy, or a Referer header on the way to somewhere else.
 *
 * It is read once, during render, and taken out of the address bar immediately
 * afterwards so a screen share or a bookmark does not keep it.
 */
function setupTokenFromLink(): string {
  if (typeof window === "undefined") return ""
  const fragment = window.location.hash.replace(/^#/, "")
  return new URLSearchParams(fragment).get("token")?.trim() ?? ""
}

export function SetupPage({
  onAccountCreated,
  onComplete,
}: {
  /**
   * The account exists and the session is open, but the recovery key is still
   * on screen. The page that renders this has to keep it there even though
   * the panel no longer needs setting up: a refetch of the session would
   * otherwise replace the key with the shell before it was saved.
   */
  onAccountCreated: () => void
  onComplete: () => void
}) {
  const { t } = useTranslation()
  const [token, setToken] = useState(setupTokenFromLink)
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [teamName, setTeamName] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [recoveryKey, setRecoveryKey] = useState<string | null>(null)

  // Only the address bar is touched here; the value itself was read during
  // render, because state that follows other state is derived, not copied in.
  useEffect(() => {
    if (window.location.hash) {
      window.history.replaceState(null, "", window.location.pathname + window.location.search)
    }
  }, [])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setSubmitting(true)
    setError(null)
    try {
      const result = await api.anonymous<{
        user: User
        team: Team
        recovery_key: string
      }>("/api/setup", {
        method: "POST",
        body: {
          token: token.trim(),
          email: email.trim(),
          name: name.trim(),
          password,
          team_name: teamName.trim(),
          locale: currentLanguage(),
        },
      })
      onAccountCreated()
      setRecoveryKey(result.recovery_key ?? "")
    } catch (caught) {
      setError(caught)
    } finally {
      setSubmitting(false)
    }
  }

  // The account exists from here on, so the form is never shown again: sending
  // it a second time could only fail.
  if (recoveryKey === "") return <RecoveryKeyMissing onDone={onComplete} />
  if (recoveryKey !== null) {
    return <RecoveryKeyStep recoveryKey={recoveryKey} onDone={onComplete} />
  }

  return (
    <CenteredLayout>
      <Card className="w-full max-w-lg">
        <CardHeader className="space-y-3">
          <Logo className="size-9 text-primary" />
          <div>
            <CardTitle className="text-xl">
              {t("auth.setupTitle", { product: "Skifity" })}
            </CardTitle>
            <CardDescription>{t("auth.setupSubtitle")}</CardDescription>
          </div>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit}>
            <FieldGroup>
              {error != null && <ErrorDisplay error={error} />}

              <Field>
                <FieldLabel htmlFor="token">{t("auth.setupToken")}</FieldLabel>
                <Input
                  id="token"
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                  required
                  // The cursor belongs in the first field somebody still has to
                  // fill in, which is this one only when the link did not
                  // carry the token.
                  autoFocus={token === ""}
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono"
                />
                <FieldDescription>{t("auth.setupTokenHelp")}</FieldDescription>
              </Field>

              <FieldSeparator />

              <div className="grid gap-6 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="name">{t("auth.yourName")}</FieldLabel>
                  <Input
                    id="name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    autoFocus={token !== ""}
                    autoComplete="name"
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="email">{t("auth.email")}</FieldLabel>
                  <Input
                    id="email"
                    type="email"
                    value={email}
                    onChange={(event) => setEmail(event.target.value)}
                    required
                    autoComplete="email"
                  />
                </Field>
              </div>

              <Field>
                <FieldLabel htmlFor="password">{t("auth.password")}</FieldLabel>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  required
                  minLength={12}
                  autoComplete="new-password"
                />
                <PasswordStrength password={password} />
              </Field>

              <Field>
                <FieldLabel htmlFor="team">{t("auth.teamName")}</FieldLabel>
                <Input
                  id="team"
                  value={teamName}
                  onChange={(event) => setTeamName(event.target.value)}
                  placeholder={t("auth.teamNameExample")}
                />
                <FieldDescription>{t("auth.teamNameHelp")}</FieldDescription>
              </Field>

              <Button type="submit" className="w-full" disabled={submitting}>
                {submitting && <Spinner />}
                {submitting ? t("auth.creatingAccount") : t("auth.createAccount")}
              </Button>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}

function RecoveryKeyStep({ recoveryKey, onDone }: { recoveryKey: string; onDone: () => void }) {
  const { t } = useTranslation()
  const [acknowledged, setAcknowledged] = useState(false)
  const [copied, setCopied] = useState(false)

  // Ticking the box is the person's word; this is the panel's record of it.
  // Without it the warning dot and "save your recovery key" were waiting in
  // the shell the moment setup finished, and clearing them meant revealing
  // the key again behind a fresh password, audited as viewed.
  const saved = useMutation({
    mutationFn: () => api.post("/api/security/recovery-key/saved"),
    onSuccess: onDone,
  })

  const copy = async () => {
    if (await copyText(recoveryKey)) {
      setCopied(true)
      toast.success(t("common.copied"))
      window.setTimeout(() => setCopied(false), 2000)
    } else {
      toast.error(t("common.copyFailed"))
    }
  }

  const download = () => {
    const title = t("auth.recoveryKeyFileTitle", { product: "Skifity" })
    downloadText(
      "skifity-recovery-key.txt",
      [
        title,
        "=".repeat(title.length),
        "",
        recoveryKey,
        "",
        t("auth.recoveryKeyFileBody"),
        "",
      ].join("\n"),
    )
    setAcknowledged(true)
  }

  return (
    <CenteredLayout>
      <Card className="w-full max-w-lg">
        <CardHeader className="space-y-3">
          <div className="flex size-9 items-center justify-center rounded-lg bg-warning/15">
            <KeyRoundIcon className="size-5 text-warning" />
          </div>
          <div>
            <CardTitle className="text-xl">{t("auth.recoveryKeyTitle")}</CardTitle>
            <CardDescription>{t("auth.recoveryKeyIntro", { product: "Skifity" })}</CardDescription>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="rounded-md border border-warning/40 bg-warning/5 p-3 text-sm">
            {t("auth.recoveryKeyWarning")}
          </div>

          <pre className="log-output overflow-x-auto rounded-md bg-muted p-3 text-xs select-all">
            {recoveryKey}
          </pre>

          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => void copy()}>
              {copied ? <CheckIcon className="size-4" /> : <ClipboardIcon className="size-4" />}
              {t("common.copy")}
            </Button>
            <Button variant="outline" size="sm" onClick={download}>
              <DownloadIcon className="size-4" />
              {t("auth.download")}
            </Button>
          </div>

          <Field orientation="horizontal">
            <Checkbox
              id="recovery-key-saved"
              checked={acknowledged}
              onCheckedChange={(checked) => setAcknowledged(checked === true)}
            />
            <FieldLabel htmlFor="recovery-key-saved" className="font-normal">
              {t("auth.recoveryKeyConfirm")}
            </FieldLabel>
          </Field>

          {saved.error != null && <ErrorDisplay error={saved.error} compact />}

          <Button
            className="w-full"
            disabled={!acknowledged || saved.isPending}
            onClick={() => saved.mutate()}
          >
            {saved.isPending && <Spinner />}
            {t("common.done")}
          </Button>
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}

/**
 * The account was made, and the panel could not show its recovery key. Not a
 * dead end: the key is still there to reveal from Account, and the shell keeps
 * saying so until it has been saved.
 */
function RecoveryKeyMissing({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  return (
    <CenteredLayout>
      <Card className="w-full max-w-lg">
        <CardContent className="space-y-4 pt-6">
          <Alert variant="warning">
            <TriangleAlertIcon />
            <AlertTitle>{t("auth.recoveryKeyMissingTitle")}</AlertTitle>
            <AlertDescription>{t("auth.recoveryKeyMissing")}</AlertDescription>
          </Alert>
          <Button className="w-full" onClick={onDone}>
            {t("common.next")}
          </Button>
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}
