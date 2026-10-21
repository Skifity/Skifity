import { useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQueries, useQuery } from "@tanstack/react-query"
import { GitBranchIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
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
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle,
} from "@/components/ui/item"
import { Spinner } from "@/components/ui/spinner"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, Environment } from "@/lib/types"

type StartedPreview = { environment_id: string; app_id: string; deployment_id: string }

/**
 * A preview of any branch, by hand: the design somebody wants to show before
 * opening a pull request, or a host whose webhooks never reach this panel.
 * It is the preview a pull request from that branch would get, so opening
 * one later updates it rather than making a second.
 *
 * The previews of branches already open are listed beside it, each with the
 * way to close it. Starting one was a button; closing one was only ever the
 * CLI, so a preview nobody remembered to close ran until its branch went.
 */
export function PreviewBranchButton({ app, projectId }: { app: App; projectId: string }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const { team } = useSession()
  const [open, setOpen] = useState(false)
  const [branch, setBranch] = useState("")
  // The panel lets a member or above start or close one, and refuses a viewer.
  const canClose = team?.role !== undefined && team.role !== "viewer"

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

  const close = useMutation({
    mutationFn: (previewed: string) =>
      api.post(`/api/apps/${app.id}/previews/close`, { branch: previewed }),
    onSuccess: (_, previewed) => {
      toast.success(t("previews.closed", { branch: previewed }))
      void queryClient.invalidateQueries({ queryKey: ["environments", projectId] })
    },
  })

  // A preview of a branch is a preview environment whose ref says "branch"
  // rather than a pull request's number, holding a copy of this app; the copy
  // is built from the branch, so it is what names it. Read only while the
  // dialog is open: a header button is not worth a request per environment.
  const environments = useQuery({
    queryKey: ["environments", projectId],
    queryFn: () => api.get<List<Environment>>(`/api/projects/${projectId}/environments`),
    enabled: open,
  })
  const branchEnvironments = (environments.data?.items ?? []).filter(
    (environment) =>
      environment.kind === "preview" && environment.source_ref?.startsWith("branch-"),
  )
  const copies = useQueries({
    queries: branchEnvironments.map((environment) => ({
      queryKey: ["apps", environment.id],
      queryFn: () => api.get<List<App>>(`/api/environments/${environment.id}/apps`),
      enabled: open,
    })),
  })
  const previews = branchEnvironments.flatMap((environment, index) => {
    const copy = copies[index]?.data?.items.find(
      (candidate) => candidate.slug === app.slug && candidate.repo_url === app.repo_url,
    )
    return copy?.branch ? [{ environment, copy }] : []
  })

  const askThenClose = (environment: Environment, previewed: string) => {
    void confirm({
      title: t("previews.closeTitle", { branch: previewed }),
      description: t("previews.closeConfirm", { environment: environment.name }),
      consequence: t("common.cannotBeUndone"),
      confirmLabel: t("common.close"),
      destructive: true,
    }).then((yes) => {
      if (yes) close.mutate(previewed)
    })
  }

  const setDialogOpen = (next: boolean) => {
    if (!next) {
      start.reset()
      close.reset()
    }
    setOpen(next)
  }

  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <GitBranchIcon />
        {t("previews.previewBranch")}
      </Button>
      <Dialog open={open} onOpenChange={setDialogOpen}>
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
              <Button type="button" variant="ghost" onClick={() => setDialogOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!branch.trim() || start.isPending}>
                {start.isPending && <Spinner />}
                {t("previews.start")}
              </Button>
            </DialogFooter>
          </form>

          {previews.length > 0 && (
            <div className="space-y-2 border-t pt-4">
              <p className="text-sm font-medium">{t("previews.openPreviews")}</p>
              {close.error != null && <ErrorDisplay error={close.error} compact />}
              <ItemGroup className="gap-2">
                {previews.map(({ environment, copy }) => (
                  <Item key={environment.id} variant="outline" size="sm">
                    <ItemContent>
                      <ItemTitle className="font-mono">{copy.branch}</ItemTitle>
                      <ItemDescription>{environment.name}</ItemDescription>
                    </ItemContent>
                    <ItemActions>
                      <Button variant="ghost" size="sm" asChild>
                        <Link to={`/apps/${copy.id}`} onClick={() => setDialogOpen(false)}>
                          {t("previews.openPreview")}
                        </Link>
                      </Button>
                      {canClose && (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={close.isPending}
                          aria-label={t("previews.closeNamed", { branch: copy.branch })}
                          onClick={() => askThenClose(environment, copy.branch)}
                        >
                          {close.isPending && close.variables === copy.branch ? (
                            <Spinner />
                          ) : (
                            <XIcon />
                          )}
                          {t("common.close")}
                        </Button>
                      )}
                    </ItemActions>
                  </Item>
                ))}
              </ItemGroup>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
