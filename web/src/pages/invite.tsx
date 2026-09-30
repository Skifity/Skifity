import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useParams } from "react-router-dom"
import { useMutation, useQuery } from "@tanstack/react-query"

import { ErrorDisplay } from "@/components/error-display"
import { Logo } from "@/components/logo"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { CenteredLayout, PasswordStrength } from "@/pages/setup"

type Lookup = { email: string; team_name: string; role: string }

/**
 * Accepting an invitation: the one page somebody sees before they have an
 * account.
 *
 * The address is not editable and is not sent: it comes from the invitation, so
 * a link meant for one person cannot make an account for another. The panel
 * says which team and which address before asking for a password, because
 * typing a password into a page that has not said what it is for is a habit
 * worth not teaching.
 */
export function InvitePage({ onSignedIn }: { onSignedIn: () => void }) {
  const { t } = useTranslation()
  const { token = "" } = useParams()
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")

  const lookup = useQuery({
    queryKey: ["invitation", token],
    queryFn: () => api.get<Lookup>(`/api/invitations/${token}`),
    retry: false,
  })

  const accept = useMutation({
    mutationFn: () =>
      api.post(`/api/invitations/${token}/accept`, { name: name.trim(), password }),
    onSuccess: () => {
      // Signed in already: the panel issued a session with the answer, so
      // sending somebody to a sign-in form now would be asking for the password
      // they typed one second ago.
      window.history.replaceState({}, "", "/")
      onSignedIn()
    },
  })

  if (lookup.isLoading) {
    return (
      <CenteredLayout>
        <Skeleton className="h-96 w-full" />
      </CenteredLayout>
    )
  }

  if (lookup.error != null) {
    return (
      <CenteredLayout>
        <Card>
          <CardHeader>
            <Logo className="size-8" />
            <CardTitle>{t("invite.notValidTitle")}</CardTitle>
            <CardDescription>{t("invite.notValidHelp")}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button variant="outline" className="w-full" asChild>
              <a href="/">{t("invite.goToSignIn")}</a>
            </Button>
          </CardContent>
        </Card>
      </CenteredLayout>
    )
  }

  return (
    <CenteredLayout>
      <Card>
        <CardHeader>
          <Logo className="size-8" />
          <CardTitle>
            {t("invite.title", { team: lookup.data?.team_name ?? "" })}
          </CardTitle>
          <CardDescription>
            {t("invite.subtitle", {
              email: lookup.data?.email ?? "",
              role: t(`settings.role${(lookup.data?.role ?? "member").replace(/^./, (c) => c.toUpperCase())}`, {
                defaultValue: lookup.data?.role ?? "",
              }),
            })}
          </CardDescription>
        </CardHeader>

        <CardContent>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              accept.mutate()
            }}
          >
            <FieldGroup>
              {accept.error != null && <ErrorDisplay error={accept.error} compact />}

              <Field>
                <FieldLabel htmlFor="invite-email">{t("auth.email")}</FieldLabel>
                {/*
                  Shown, not asked for. The account is created with the address
                  the invitation names, whatever a request says, so an editable
                  field here would be a promise the server does not keep.
                */}
                <Input id="invite-email" value={lookup.data?.email ?? ""} readOnly disabled />
                <FieldDescription>{t("invite.emailFixed")}</FieldDescription>
              </Field>

              <Field>
                <FieldLabel htmlFor="invite-name">{t("auth.yourName")}</FieldLabel>
                <Input
                  id="invite-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  autoComplete="name"
                  autoFocus
                  required
                />
              </Field>

              <Field>
                <FieldLabel htmlFor="invite-password">{t("auth.password")}</FieldLabel>
                <Input
                  id="invite-password"
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete="new-password"
                  required
                  minLength={12}
                />
                {/*
                  The same meter as first-run setup, not a sentence of its own:
                  a second description of the password rule is a second thing
                  to keep in step with the policy the server actually enforces.
                */}
                <PasswordStrength password={password} />
              </Field>

              <Field>
                <Button
                  type="submit"
                  className="w-full"
                  disabled={!name.trim() || !password || accept.isPending}
                >
                  {accept.isPending && <Spinner />}
                  {accept.isPending ? t("common.saving") : t("invite.join")}
                </Button>
              </Field>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </CenteredLayout>
  )
}
