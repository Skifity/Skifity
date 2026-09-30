import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { CheckIcon, ClipboardIcon, DownloadIcon, KeyRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { ErrorDisplay } from "@/components/error-display"
import { LanguageSwitcher } from "@/components/language-switcher"
import { Logo } from "@/components/logo"
import { ThemeToggle } from "@/components/theme-toggle"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
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

export function SetupPage({ onComplete }: { onComplete: () => void }) {
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
      setRecoveryKey(result.recovery_key)
    } catch (caught) {
      setError(caught)
    } finally {
      setSubmitting(false)
    }
  }

  if (recoveryKey) {
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

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(recoveryKey)
      setCopied(true)
      toast.success(t("common.copied"))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error(t("errors.somethingWentWrong"))
    }
  }

  const download = () => {
    const blob = new Blob(
      [
        "Skifity recovery key\n",
        "====================\n\n",
        `${recoveryKey}\n\n`,
        "Anyone holding this key can decrypt every secret this panel stores.\n",
        "Keep it somewhere safe and offline. Without it, a restored backup is unreadable.\n",
      ],
      { type: "text/plain" },
    )
    const url = URL.createObjectURL(blob)
    const link = document.createElement("a")
    link.href = url
    link.download = "skifity-recovery-key.txt"
    link.click()
    URL.revokeObjectURL(url)
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
            <Button variant="outline" size="sm" onClick={copy}>
              {copied ? <CheckIcon className="size-4" /> : <ClipboardIcon className="size-4" />}
              {t("common.copy")}
            </Button>
            <Button variant="outline" size="sm" onClick={download}>
              <DownloadIcon className="size-4" />
              {t("auth.download")}
            </Button>
          </div>

          <label className="flex cursor-pointer items-start gap-2.5 text-sm">
            <input
              type="checkbox"
              checked={acknowledged}
              onChange={(event) => setAcknowledged(event.target.checked)}
              className="mt-0.5 size-4 accent-primary"
            />
            <span>{t("auth.recoveryKeyConfirm")}</span>
          </label>

          <Button className="w-full" disabled={!acknowledged} onClick={onDone}>
            {t("common.done")}
          </Button>
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}

/** A quiet strength hint, so the 12-character minimum is not a surprise. */
export function PasswordStrength({ password }: { password: string }) {
  const { t } = useTranslation()
  if (!password) return null

  const tooShort = password.length < 12
  return (
    <p className={tooShort ? "text-xs text-warning" : "text-xs text-success"}>
      {tooShort ? t("common.required") + ": 12+" : `${password.length} ${t("common.of")} 12+`}
    </p>
  )
}

export function CenteredLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-svh flex-col bg-muted/30">
      <div className="flex justify-end gap-1 p-4">
        <LanguageSwitcher />
        <ThemeToggle />
      </div>
      <div className="flex flex-1 items-center justify-center px-4 pb-16">{children}</div>
    </div>
  )
}
