import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { LockIcon, LockOpenIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App } from "@/lib/types"

/**
 * Locking an app's deploys, the way `kamal lock` does.
 *
 * During an incident, a migration somebody is running by hand, or a freeze
 * before a launch, a push to the branch should not ship. A lock stops every
 * deploy and rollback — from here, the CLI, an assistant or a webhook — and
 * says who locked it and why. Variables and scaling still apply.
 */
export function DeployLockButton({ app }: { app: App }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState("")
  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["app", app.id] })

  const lock = useMutation({
    mutationFn: () => api.put(`/api/apps/${app.id}/lock`, { reason: reason.trim() }),
    onSuccess: () => {
      invalidate()
      setOpen(false)
      setReason("")
    },
  })
  const unlock = useMutation({
    mutationFn: () => api.delete(`/api/apps/${app.id}/lock`),
    onSuccess: invalidate,
  })

  if (app.deploy_lock) {
    return (
      <Button
        variant="outline"
        aria-label={t("apps.unlockDeploys")}
        disabled={unlock.isPending}
        onClick={() => unlock.mutate()}
      >
        {unlock.isPending ? <Spinner /> : <LockOpenIcon />}
        {/* The header holds three buttons, and on a phone the words of this
            one are what pushed the row off the screen. The icon stays, and
            the notice below says what it is for. */}
        <span className="hidden sm:inline">{t("apps.unlockDeploys")}</span>
      </Button>
    )
  }
  return (
    <>
      <Button variant="outline" aria-label={t("apps.lockDeploys")} onClick={() => setOpen(true)}>
        <LockIcon />
        <span className="hidden sm:inline">{t("apps.lockDeploys")}</span>
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("apps.lockDeploys")}</DialogTitle>
            <DialogDescription>{t("apps.lockDeploysHelp")}</DialogDescription>
          </DialogHeader>
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              lock.mutate()
            }}
          >
            <div className="space-y-2">
              <Label htmlFor="lock-reason">{t("apps.lockReason")}</Label>
              <Input
                id="lock-reason"
                value={reason}
                maxLength={200}
                placeholder={t("apps.lockReasonPlaceholder")}
                onChange={(event) => setReason(event.target.value)}
              />
            </div>
            {lock.error != null && <ErrorDisplay error={lock.error} compact />}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={lock.isPending || reason.trim() === ""}>
                {lock.isPending ? <Spinner /> : <LockIcon />}
                {t("apps.lockDeploys")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

/** Said at the top of the app while it holds: who, why and since when. */
export function DeployLockNotice({ app }: { app: App }) {
  const { t } = useTranslation()
  if (!app.deploy_lock) return null
  return (
    <Alert variant="warning">
      <LockIcon />
      <AlertTitle>{t("apps.deploysLocked")}</AlertTitle>
      <AlertDescription>
        {t("apps.deploysLockedBy", {
          who: app.deploy_lock.locked_by,
          when: formatRelative(app.deploy_lock.locked_at),
          reason: app.deploy_lock.reason,
        })}
      </AlertDescription>
    </Alert>
  )
}
