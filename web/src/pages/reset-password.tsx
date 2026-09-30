import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { CheckCircle2Icon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Logo } from "@/components/logo"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { CenteredLayout, PasswordStrength } from "@/pages/setup"

/**
 * Choosing a new password through the link a reset sends.
 *
 * The token is in the address's fragment, which a browser never sends to a
 * server: not to this panel's logs, and not in a Referer to anything the page
 * loads. It is read once, when the page opens.
 */
export function ResetPasswordPage() {
  const { t } = useTranslation()
  const [token] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "",
  )
  const [password, setPassword] = useState("")
  const [again, setAgain] = useState("")

  const reset = useMutation({
    mutationFn: () =>
      api.anonymous("/api/auth/password-reset/confirm", {
        method: "POST",
        body: { token, password },
      }),
  })
  const mismatch = again !== "" && again !== password

  return (
    <CenteredLayout>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <Logo className="mb-2" />
          <CardTitle>{t("auth.resetTitle")}</CardTitle>
          <CardDescription>{t("auth.resetDescription")}</CardDescription>
        </CardHeader>
        <CardContent>
          {reset.isSuccess ? (
            <div className="space-y-4">
              <Alert>
                <CheckCircle2Icon />
                <AlertDescription>{t("auth.resetDone")}</AlertDescription>
              </Alert>
              <Button className="w-full" onClick={() => window.location.assign("/")}>
                {t("auth.backToSignIn")}
              </Button>
            </div>
          ) : !token ? (
            <div className="space-y-4">
              <Alert variant="destructive">
                <AlertDescription>{t("auth.resetMissingToken")}</AlertDescription>
              </Alert>
              <Button
                variant="outline"
                className="w-full"
                onClick={() => window.location.assign("/")}
              >
                {t("auth.backToSignIn")}
              </Button>
            </div>
          ) : (
            <form
              onSubmit={(event) => {
                event.preventDefault()
                reset.mutate()
              }}
            >
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="new-password">{t("auth.newPassword")}</FieldLabel>
                  <Input
                    id="new-password"
                    type="password"
                    value={password}
                    autoComplete="new-password"
                    autoFocus
                    required
                    onChange={(event) => setPassword(event.target.value)}
                  />
                  <PasswordStrength password={password} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="new-password-again">{t("auth.newPasswordAgain")}</FieldLabel>
                  <Input
                    id="new-password-again"
                    type="password"
                    value={again}
                    autoComplete="new-password"
                    required
                    aria-invalid={mismatch}
                    onChange={(event) => setAgain(event.target.value)}
                  />
                  {mismatch && (
                    <p className="text-sm text-destructive">{t("auth.passwordsDiffer")}</p>
                  )}
                </Field>
                {reset.error != null && <ErrorDisplay error={reset.error} compact />}
                <Button
                  type="submit"
                  className="w-full"
                  disabled={reset.isPending || password.length < 12 || password !== again}
                >
                  {reset.isPending && <Spinner />}
                  {t("auth.setNewPassword")}
                </Button>
              </FieldGroup>
            </form>
          )}
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}
