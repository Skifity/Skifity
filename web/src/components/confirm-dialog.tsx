import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangleIcon } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { buttonVariants } from "@/components/ui/button"
import { cn } from "cn"

/**
 * Asking "are you sure?" properly.
 *
 * The browser's own confirm() cannot be styled, cannot be read by anything, and
 * blocks the whole tab. It also treats "delete one variable" and "delete every
 * app in this project" as the same question. This asks once, in the panel's own
 * voice, and for the irreversible cases makes the person type the name of the
 * thing they are about to lose.
 */

export type ConfirmOptions = {
  title: string
  description: string
  /** The wording on the button that goes ahead. */
  confirmLabel?: string
  /** Destructive styling, for anything that removes something. */
  destructive?: boolean
  /**
   * When set, the person has to type this exactly. Reserved for the cases where
   * being wrong cannot be undone: deleting a project, purging data, restoring
   * over a live database.
   */
  typeToConfirm?: string
  /** Extra consequence spelled out under the description. */
  consequence?: string
}

type Resolver = (confirmed: boolean) => void

const ConfirmContext = createContext<((options: ConfirmOptions) => Promise<boolean>) | null>(null)

export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const [options, setOptions] = useState<ConfirmOptions | null>(null)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const resolver = useRef<Resolver | null>(null)

  const confirm = useCallback((next: ConfirmOptions) => {
    setOptions(next)
    setTyped("")
    setBusy(false)
    return new Promise<boolean>((resolve) => {
      resolver.current = resolve
    })
  }, [])

  const settle = useCallback((confirmed: boolean) => {
    resolver.current?.(confirmed)
    resolver.current = null
    setOptions(null)
    setTyped("")
    setBusy(false)
  }, [])

  const ready = !options?.typeToConfirm || typed.trim() === options.typeToConfirm

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <AlertDialog
        open={options !== null}
        onOpenChange={(open) => {
          // Dismissing by any means is a "no". An unanswered question must
          // never be read as consent.
          if (!open && !busy) settle(false)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              {options?.destructive && (
                <AlertTriangleIcon className="size-4 shrink-0 text-destructive" />
              )}
              {options?.title}
            </AlertDialogTitle>
            <AlertDialogDescription>{options?.description}</AlertDialogDescription>
          </AlertDialogHeader>

          {options?.consequence && (
            <p className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              {options.consequence}
            </p>
          )}

          {options?.typeToConfirm && (
            <Field>
              <FieldLabel htmlFor="confirm-phrase">
                {t("common.typeToConfirm", { phrase: options.typeToConfirm })}
              </FieldLabel>
              <Input
                id="confirm-phrase"
                value={typed}
                onChange={(event) => setTyped(event.target.value)}
                autoComplete="off"
                autoFocus
                className="font-mono"
              />
            </Field>
          )}

          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={!ready || busy}
              className={cn(options?.destructive && buttonVariants({ variant: "destructive" }))}
              onClick={(event) => {
                // Radix closes on click; the caller decides when it is done.
                event.preventDefault()
                setBusy(true)
                settle(true)
              }}
            >
              {busy && <Spinner />}
              {options?.confirmLabel ?? t("common.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ConfirmContext.Provider>
  )
}

/**
 * Returns an async confirm(). Awaiting it gives true only if the person said
 * yes, which reads the same as the browser's confirm() at the call site while
 * being a real dialog.
 */
export function useConfirm() {
  const context = useContext(ConfirmContext)
  if (!context) throw new Error("useConfirm must be used inside a ConfirmProvider")
  return context
}

/** Convenience for the common "type the name to delete it" shape. */
export function useDeleteConfirm() {
  const confirm = useConfirm()
  const { t } = useTranslation()
  return useMemo(
    () => (name: string, description: string, consequence?: string) =>
      confirm({
        title: t("common.deleteNamed", { name }),
        description,
        consequence,
        confirmLabel: t("common.delete"),
        destructive: true,
        typeToConfirm: name,
      }),
    [confirm, t],
  )
}
