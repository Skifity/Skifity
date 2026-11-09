import { useEffect, useEffectEvent, useRef, useState } from "react"
import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ArrowLeftIcon, FingerprintIcon, KeyRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { CenteredLayout } from "@/components/centered-layout"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Logo } from "@/components/logo"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp"
import { Spinner } from "@/components/ui/spinner"
import { Separator } from "@/components/ui/separator"
import { api } from "@/lib/api"
import {
  autofillAvailable,
  getPasskey,
  passkeyFailure,
  passkeysSupported,
  type PasskeyFailure,
  type PasskeyRequestOptions,
} from "@/lib/passkeys"
import type { Meta } from "@/lib/types"

/**
 * Signing in with a passkey, start to finish: a challenge from the panel, the
 * browser's answer to it, and the panel checking the answer. The session
 * cookie comes back on the last request, exactly as it does for a password.
 */
async function signInWithPasskey(how: {
  mediation?: CredentialMediationRequirement
  signal?: AbortSignal
}) {
  const options = await api.anonymous<PasskeyRequestOptions>("/api/auth/passkey/begin", {
    method: "POST",
    body: {},
    signal: how.signal,
  })
  const answer = await getPasskey(options, how)
  await api.anonymous("/api/auth/passkey/finish", { method: "POST", body: answer })
}

/**
 * Asking for a reset link, in place under "Lost your password?". The answer
 * is the same whether or not the address has an account, so it says what
 * happens if it does.
 */
function ResetLinkRequest({ email }: { email: string }) {
  const { t } = useTranslation()
  const request = useMutation({
    mutationFn: () =>
      api.anonymous("/api/auth/password-reset", { method: "POST", body: { email: email.trim() } }),
  })
  if (request.isSuccess) {
    return <p className="pt-2 text-left">{t("auth.resetLinkSent", { email: email.trim() })}</p>
  }
  return (
    <div className="space-y-2 pt-2 text-left">
      <p>{t("auth.resetLinkHelp")}</p>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="w-full"
        disabled={!email.trim() || request.isPending}
        onClick={() => request.mutate()}
      >
        {request.isPending && <Spinner />}
        {t("auth.sendResetLink")}
      </Button>
      {request.error != null && <ErrorDisplay error={request.error} compact />}
    </div>
  )
}

/**
 * Where single sign-on should bring somebody back to: the page they asked for,
 * as password and passkey sign-in already do, without the failed sign-on's
 * own reason riding along. The panel checks it is a path on itself.
 */
function ssoStartLink(): string {
  const search = new URLSearchParams(window.location.search)
  search.delete("sso_error")
  const query = search.toString()
  const next = window.location.pathname + (query ? `?${query}` : "")
  return next === "/"
    ? "/api/auth/sso/start"
    : `/api/auth/sso/start?next=${encodeURIComponent(next)}`
}

/** What the sign-in answers: the session, or a request for the second factor. */
type SignInAnswer = {
  totp_required?: boolean
  used_recovery_code?: boolean
  recovery_codes_left?: number
}

/**
 * Sign-in.
 *
 * Two-factor is asked for only after the password is accepted, which keeps the
 * first screen simple and tells nobody whether an account has it enabled.
 */
export function LoginPage({ onSignedIn }: { onSignedIn: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [needsCode, setNeedsCode] = useState(false)
  // A recovery code is fourteen characters of letters and digits, which six
  // boxes for digits cut short — and the cut-off code was sent on its own, as
  // a wrong guess. It gets a plain field that waits for the button instead.
  const [recovery, setRecovery] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<unknown>(null)

  const meta = useQuery({ queryKey: ["meta"], queryFn: () => api.get<Meta>("/api/meta") })

  // A sign-on that failed comes back as a redirect with a reason in the query,
  // because the callback is a place a browser lands rather than a request the
  // panel made.
  //
  // Read during the first render rather than copied in by an effect: the value
  // is already there when this mounts, and setting state from an effect is the
  // cascading render this codebase does not do. The effect only takes it out of
  // the address bar, which is a side effect and nothing else's input.
  const [ssoError] = useState(() => new URLSearchParams(window.location.search).get("sso_error"))
  useEffect(() => {
    if (ssoError) window.history.replaceState({}, "", window.location.pathname)
  }, [ssoError])

  // Passkeys are offered when the panel's address can have them and this
  // browser can use them. The panel's half is in /api/meta; the browser's is
  // asked here, and neither is copied into state.
  const passkeysOffered = meta.data?.passkeys?.available === true && passkeysSupported()
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const [passkeyTrouble, setPasskeyTrouble] = useState<PasskeyFailure | null>(null)
  // The autofill request is started again after the button's one ends, since
  // starting the button's stopped it.
  const [autofillRound, setAutofillRound] = useState(0)
  const autofill = useRef<AbortController | null>(null)
  const signedInWithAutofill = useEffectEvent(() => onSignedIn())

  // Offer passkeys in the email field's suggestions, the way password
  // managers offer passwords: the browser waits, and a passkey picked there
  // signs in without the button. Aborted when the page goes, or when the
  // button starts a request of its own — the browser handles one at a time.
  useEffect(() => {
    if (!passkeysOffered || needsCode) return
    const controller = new AbortController()
    autofill.current = controller
    void (async () => {
      if (!(await autofillAvailable()) || controller.signal.aborted) return
      try {
        await signInWithPasskey({ mediation: "conditional", signal: controller.signal })
        signedInWithAutofill()
      } catch (caught) {
        // Stopping it is how the page stops waiting, and the browser's own
        // refusals while it waits are nothing the person did.
        if (controller.signal.aborted || passkeyFailure(caught) !== null) return
        setError(caught)
      }
    })()
    return () => controller.abort()
  }, [passkeysOffered, needsCode, autofillRound])

  const startPasskey = async () => {
    autofill.current?.abort()
    setPasskeyBusy(true)
    setError(null)
    setPasskeyTrouble(null)
    try {
      await signInWithPasskey({})
      onSignedIn()
    } catch (caught) {
      const trouble = passkeyFailure(caught)
      if (trouble === null) setError(caught)
      // Closing the browser's dialog is an answer, not a failure.
      else if (trouble !== "cancelled") setPasskeyTrouble(trouble)
      setAutofillRound((round) => round + 1)
    } finally {
      setPasskeyBusy(false)
    }
  }

  // A recovery code is spent by signing in with it, and this is the one moment
  // the panel can warn somebody before they run out. A toast, because this
  // page is gone as soon as the session exists.
  const sayRecoveryCodeUsed = (left: number) => {
    const action = { label: t("auth.openAccount"), onClick: () => navigate("/account") }
    if (left > 0) {
      toast.info(t("auth.recoveryCodeUsed", { count: left }), {
        description: t("auth.recoveryCodeUsedHelp"),
        action,
        duration: 15000,
      })
    } else {
      toast.warning(t("auth.recoveryCodesUsedUp"), {
        description: t("auth.recoveryCodesUsedUpHelp"),
        action,
        duration: 30000,
      })
    }
  }

  const signIn = async (totpCode: string) => {
    setSubmitting(true)
    setError(null)
    try {
      const result = await api.anonymous<SignInAnswer>("/api/auth/login", {
        method: "POST",
        body: { email: email.trim(), password, totp_code: totpCode.trim() || undefined },
      })
      if (result?.totp_required) {
        setNeedsCode(true)
        return
      }
      if (result?.used_recovery_code) sayRecoveryCodeUsed(result.recovery_codes_left ?? 0)
      onSignedIn()
    } catch (caught) {
      setError(caught)
      // Six digits are cleared, because the boxes send themselves when full
      // and a wrong code is retyped from the app anyway. A recovery code is
      // kept: it was copied off paper, and a typo in it is one character to
      // fix rather than fourteen to type again.
      if (!recovery) setCode("")
    } finally {
      setSubmitting(false)
    }
  }

  // Changing how the second factor is given starts that answer afresh: half a
  // code typed into one is not the beginning of the other.
  const chooseRecovery = (next: boolean) => {
    setRecovery(next)
    setCode("")
    setError(null)
  }

  return (
    <CenteredLayout>
      <Card className="w-full max-w-sm">
        <CardHeader className="space-y-3">
          <Logo className="size-9 text-primary" />
          <div className="space-y-1">
            <CardTitle className="text-xl">
              {needsCode
                ? recovery
                  ? t("auth.recoveryCode")
                  : t("auth.twoFactorCode")
                : t("auth.signInTitle", { product: "Skifity" })}
            </CardTitle>
            <CardDescription>
              {needsCode
                ? recovery
                  ? t("auth.recoveryCodePrompt")
                  : t("auth.twoFactorPrompt")
                : t("auth.signInSubtitle")}
            </CardDescription>
          </div>
        </CardHeader>

        <CardContent>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              void signIn(code)
            }}
          >
            <FieldGroup>
              {error != null && <ErrorDisplay error={error} compact />}
              {passkeyTrouble != null && (
                <Alert variant="destructive">
                  <AlertDescription>
                    {t(`auth.passkeys.failure.${passkeyTrouble}`)}
                  </AlertDescription>
                </Alert>
              )}
              {ssoError != null && (
                <Alert variant="destructive">
                  <AlertDescription>
                    {/*
                      Five reasons, because more would be telling an anonymous
                      browser things about accounts it does not have. Everything
                      else is one sentence and a line in the panel's log.
                    */}
                    {t(
                      `auth.ssoError.${["no_account", "state", "expired", "disabled", "link_required"].includes(ssoError) ? ssoError : "refused"}`,
                    )}
                  </AlertDescription>
                </Alert>
              )}

              {needsCode ? (
                <>
                  {recovery ? (
                    <>
                      <Field>
                        <FieldLabel htmlFor="recovery-code">{t("auth.recoveryCode")}</FieldLabel>
                        <Input
                          id="recovery-code"
                          value={code}
                          onChange={(event) => setCode(event.target.value)}
                          // Letters and digits, so no number pad, and nothing
                          // a browser should remember or offer again: each
                          // code works once.
                          autoComplete="off"
                          autoCapitalize="characters"
                          spellCheck={false}
                          className="font-mono"
                          required
                          autoFocus
                        />
                      </Field>
                      <Button type="submit" className="w-full" disabled={submitting || !code.trim()}>
                        {submitting && <Spinner />}
                        {submitting ? t("auth.signingIn") : t("auth.signIn")}
                      </Button>
                    </>
                  ) : (
                    <>
                      <Field className="items-center">
                        <InputOTP
                          maxLength={6}
                          value={code}
                          autoFocus
                          onChange={(value) => {
                            setCode(value)
                            // Six digits is the whole answer, so pressing a
                            // button afterwards is a step with no decision in it.
                            if (value.length === 6 && !submitting) void signIn(value)
                          }}
                        >
                          <InputOTPGroup>
                            {[0, 1, 2, 3, 4, 5].map((slot) => (
                              <InputOTPSlot key={slot} index={slot} />
                            ))}
                          </InputOTPGroup>
                        </InputOTP>
                      </Field>

                      {submitting && (
                        <p className="flex items-center justify-center gap-2 text-sm text-muted-foreground">
                          <Spinner />
                          {t("auth.signingIn")}
                        </p>
                      )}
                    </>
                  )}

                  <Button
                    type="button"
                    variant="link"
                    size="sm"
                    disabled={submitting}
                    onClick={() => chooseRecovery(!recovery)}
                  >
                    {recovery ? t("auth.useAuthenticator") : t("auth.useRecoveryCode")}
                  </Button>

                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setNeedsCode(false)
                      chooseRecovery(false)
                    }}
                  >
                    <ArrowLeftIcon />
                    {t("common.back")}
                  </Button>
                </>
              ) : (
                <>
                  <Field>
                    <FieldLabel htmlFor="email">{t("auth.email")}</FieldLabel>
                    <Input
                      id="email"
                      type="email"
                      value={email}
                      onChange={(event) => setEmail(event.target.value)}
                      required
                      autoFocus
                      // "webauthn" is what lets the browser offer passkeys
                      // among this field's suggestions.
                      autoComplete={passkeysOffered ? "username webauthn" : "username"}
                    />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="password">{t("auth.password")}</FieldLabel>
                    <Input
                      id="password"
                      type="password"
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      required
                      autoComplete="current-password"
                    />
                  </Field>

                  <Field>
                    <Button type="submit" className="w-full" disabled={submitting}>
                      {submitting && <Spinner />}
                      {submitting ? t("auth.signingIn") : t("auth.signIn")}
                    </Button>

                    {(passkeysOffered || meta.data?.sso.enabled) && (
                      <div className="flex items-center gap-3 py-1">
                        <Separator className="flex-1" />
                        <span className="text-xs text-muted-foreground">{t("auth.or")}</span>
                        <Separator className="flex-1" />
                      </div>
                    )}

                    {/*
                      No address first: the passkey says whose it is. Only
                      offered where it can work — see passkeysOffered.
                    */}
                    {passkeysOffered && (
                      <Button
                        type="button"
                        variant="outline"
                        className="w-full"
                        disabled={passkeyBusy || submitting}
                        onClick={() => void startPasskey()}
                      >
                        {passkeyBusy ? <Spinner /> : <FingerprintIcon />}
                        {passkeyBusy ? t("auth.passkeys.waiting") : t("auth.passkeys.signIn")}
                      </Button>
                    )}

                    {/*
                      The button is a link rather than a fetch: the provider
                      answers with a redirect to its own page, and following
                      that is the browser's job, not the API client's.
                    */}
                    {meta.data?.sso.enabled && (
                      <Button variant="outline" className="w-full" asChild>
                        <a href={ssoStartLink()}>
                          <KeyRoundIcon />
                          {meta.data.sso.label ?? t("auth.signInWithSSO")}
                        </a>
                      </Button>
                    )}
                    <FieldDescription className="text-center">
                      <Collapsible>
                        <CollapsibleTrigger className="hover:text-foreground">
                          {t("auth.forgotPassword")}
                        </CollapsibleTrigger>
                        <CollapsibleContent>
                          {meta.data?.password_reset ? (
                            <ResetLinkRequest email={email} />
                          ) : (
                            <p className="pt-2 text-left">{t("auth.forgotPasswordHelp")}</p>
                          )}
                        </CollapsibleContent>
                      </Collapsible>
                    </FieldDescription>
                  </Field>
                </>
              )}
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}
