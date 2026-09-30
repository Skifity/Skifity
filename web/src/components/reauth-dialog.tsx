import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { LockIcon } from "lucide-react"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api, ApiError, setReauthHandler } from "@/lib/api"

/**
 * Proving who you are, again.
 *
 * Holding a signed-in session is not the same as being at the keyboard. Three
 * actions turn a session somebody borrowed into access they keep — switching
 * the second factor off, reading the recovery codes, and minting an API token
 * that outlives the session — and the panel refuses each of them until the
 * password has been given inside the last few minutes.
 *
 * Nothing at the call site knows about this. The API client sees the panel's
 * answer, opens this, and repeats the request once it is satisfied, so an
 * action added later cannot forget to ask.
 */

type Resolver = (proved: boolean) => void

export function ReauthProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [needsCode, setNeedsCode] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const resolver = useRef<Resolver | null>(null)

  const settle = useCallback((proved: boolean) => {
    resolver.current?.(proved)
    resolver.current = null
    setOpen(false)
    setPassword("")
    setCode("")
    setNeedsCode(false)
    setBusy(false)
    setError(null)
  }, [])

  useEffect(() => {
    setReauthHandler(
      () =>
        new Promise<boolean>((resolve) => {
          resolver.current = resolve
          setPassword("")
          setCode("")
          setNeedsCode(false)
          setError(null)
          setOpen(true)
        }),
    )
  }, [])

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      const answer = await api.post<{ totp_required?: boolean }>("/api/me/reauth", {
        password,
        totp_code: code,
      })
      // The account has a second factor and has not given one yet: the same
      // request again, with the field now shown.
      if (answer?.totp_required) {
        setNeedsCode(true)
        setBusy(false)
        return
      }
      settle(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.title : t("errors.unknown"))
      setPassword("")
      setBusy(false)
    }
  }

  return (
    <>
      {children}
      <Dialog
        open={open}
        onOpenChange={(next) => {
          // Dismissing is a no. The action that asked is abandoned, not run.
          if (!next && !busy) settle(false)
        }}
      >
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <LockIcon className="size-4 shrink-0" />
              {t("auth.reauth.title")}
            </DialogTitle>
            <DialogDescription>{t("auth.reauth.description")}</DialogDescription>
          </DialogHeader>

          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              void submit()
            }}
          >
            <Field>
              <FieldLabel htmlFor="reauth-password">{t("auth.password")}</FieldLabel>
              <Input
                id="reauth-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="current-password"
                autoFocus
              />
            </Field>

            {needsCode && (
              <Field>
                <FieldLabel htmlFor="reauth-code">{t("auth.twoFactorCode")}</FieldLabel>
                <Input
                  id="reauth-code"
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  autoFocus
                />
              </Field>
            )}

            {error && <p className="text-sm text-destructive">{error}</p>}

            <DialogFooter>
              <Button type="button" variant="ghost" disabled={busy} onClick={() => settle(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={busy || password.length === 0}>
                {busy && <Spinner />}
                {t("auth.reauth.confirm")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
