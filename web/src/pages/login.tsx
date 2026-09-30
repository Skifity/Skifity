import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { ArrowLeftIcon, KeyRoundIcon } from "lucide-react"

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
import type { Meta } from "@/lib/types"
import { CenteredLayout } from "@/pages/setup"

/**
 * Sign-in.
 *
 * Two-factor is asked for only after the password is accepted, which keeps the
 * first screen simple and tells nobody whether an account has it enabled.
 */
export function LoginPage({ onSignedIn }: { onSignedIn: () => void }) {
  const { t } = useTranslation()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [needsCode, setNeedsCode] = useState(false)
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

  const signIn = async (totpCode: string) => {
    setSubmitting(true)
    setError(null)
    try {
      const result = await api.anonymous<{ totp_required?: boolean }>("/api/auth/login", {
        method: "POST",
        body: { email: email.trim(), password, totp_code: totpCode.trim() || undefined },
      })
      if (result?.totp_required) {
        setNeedsCode(true)
        return
      }
      onSignedIn()
    } catch (caught) {
      setError(caught)
      // A wrong code is almost always a typo or a clock drift, and leaving the
      // digits in place means fixing one of them rather than retyping six.
      setCode("")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <CenteredLayout>
      <Card className="w-full max-w-sm">
        <CardHeader className="space-y-3">
          <Logo className="size-9 text-primary" />
          <div className="space-y-1">
            <CardTitle className="text-xl">
              {needsCode ? t("auth.twoFactorCode") : t("auth.signInTitle", { product: "Skifity" })}
            </CardTitle>
            <CardDescription>
              {needsCode ? t("auth.twoFactorPrompt") : t("auth.signInSubtitle")}
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
              {ssoError != null && (
                <Alert variant="destructive">
                  <AlertDescription>
                    {/*
                      Five reasons, because more would be telling an anonymous
                      browser things about accounts it does not have. Everything
                      else is one sentence and a line in the panel's log.
                    */}
                    {t(`auth.ssoError.${["no_account", "state", "expired", "disabled"].includes(ssoError) ? ssoError : "refused"}`)}
                  </AlertDescription>
                </Alert>
              )}

              {needsCode ? (
                <>
                  <Field className="items-center">
                    <InputOTP
                      maxLength={6}
                      value={code}
                      autoFocus
                      onChange={(value) => {
                        setCode(value)
                        // Six digits is the whole answer, so pressing a button
                        // afterwards is a step with no decision in it.
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

                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setNeedsCode(false)
                      setCode("")
                      setError(null)
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
                      autoComplete="username"
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

                    {/*
                      The button is a link rather than a fetch: the provider
                      answers with a redirect to its own page, and following
                      that is the browser's job, not the API client's.
                    */}
                    {meta.data?.sso.enabled && (
                      <>
                        <div className="flex items-center gap-3 py-1">
                          <Separator className="flex-1" />
                          <span className="text-xs text-muted-foreground">{t("auth.or")}</span>
                          <Separator className="flex-1" />
                        </div>
                        <Button variant="outline" className="w-full" asChild>
                          <a href="/api/auth/sso/start">
                            <KeyRoundIcon />
                            {meta.data.sso.label ?? t("auth.signInWithSSO")}
                          </a>
                        </Button>
                      </>
                    )}
                    <FieldDescription className="text-center">
                      <Collapsible>
                        <CollapsibleTrigger className="hover:text-foreground">
                          {t("auth.forgotPassword")}
                        </CollapsibleTrigger>
                        <CollapsibleContent>
                          <p className="pt-2 text-left">{t("auth.forgotPasswordHelp")}</p>
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
