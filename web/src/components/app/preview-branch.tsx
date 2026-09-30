import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { GitBranchIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App } from "@/lib/types"

type StartedPreview = { environment_id: string; app_id: string; deployment_id: string }

/**
 * A preview of any branch, by hand: the design somebody wants to show before
 * opening a pull request, or a host whose webhooks never reach this panel.
 * It is the preview a pull request from that branch would get, so opening
 * one later updates it rather than making a second.
 */
export function PreviewBranchButton({ app }: { app: App }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [branch, setBranch] = useState("")

  const start = useMutation({
    mutationFn: () =>
      api.post<StartedPreview>(`/api/apps/${app.id}/previews`, { branch: branch.trim() }),
    onSuccess: (started) => {
      void queryClient.invalidateQueries({ queryKey: ["environments"] })
      setOpen(false)
      setBranch("")
      if (started.app_id) navigate(`/apps/${started.app_id}`)
    },
  })

  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <GitBranchIcon />
        {t("previews.previewBranch")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("previews.previewBranchTitle", { name: app.name })}</DialogTitle>
            <DialogDescription>{t("previews.previewBranchHelp")}</DialogDescription>
          </DialogHeader>
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              start.mutate()
            }}
          >
            <Field>
              <FieldLabel htmlFor="preview-branch">{t("apps.branch")}</FieldLabel>
              <Input
                id="preview-branch"
                value={branch}
                onChange={(event) => setBranch(event.target.value)}
                placeholder="feature/checkout"
                className="font-mono"
                autoFocus
                required
              />
              <FieldDescription>{t("previews.previewBranchRemove")}</FieldDescription>
            </Field>
            {start.error != null && <ErrorDisplay error={start.error} compact />}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!branch.trim() || start.isPending}>
                {start.isPending && <Spinner />}
                {t("previews.start")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
