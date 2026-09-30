import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"

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
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { ClonedEnvironment, Environment } from "@/lib/types"

/**
 * Copying an environment: staging made from production. The apps, their
 * settings, variables, files, processes and schedules come along, with a new,
 * empty database of each kind; what does not is listed once it is done, so
 * nothing left behind is a surprise later.
 */
export function CloneEnvironmentDialog({
  environment,
  open,
  onOpenChange,
  onCloned,
}: {
  environment: Environment | undefined
  open: boolean
  onOpenChange: (open: boolean) => void
  onCloned: (environment: Environment) => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState("")
  const [deploy, setDeploy] = useState(false)

  const clone = useMutation({
    mutationFn: () =>
      api.post<ClonedEnvironment>(`/api/environments/${environment?.id}/clone`, {
        name: name.trim(),
        deploy,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["environments", environment?.project_id] })
      setName("")
    },
  })

  // The page moves to the copy once its notes have been read, not before:
  // moving sooner would retitle this dialog with the copy's name.
  const close = (next: boolean) => {
    if (!next) {
      if (clone.data) onCloned(clone.data.environment)
      clone.reset()
    }
    onOpenChange(next)
  }
  const result = clone.data

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("environments.cloneTitle", { name: environment?.name ?? "" })}
          </DialogTitle>
          <DialogDescription>{t("environments.cloneHelp")}</DialogDescription>
        </DialogHeader>
        {result ? (
          <div className="space-y-4">
            <Alert>
              <AlertTitle>
                {t("environments.cloned", {
                  name: result.environment.name,
                  apps: result.apps.length,
                  databases: result.databases.length,
                })}
              </AlertTitle>
              {result.notes.length > 0 && (
                <AlertDescription>
                  <ul className="list-disc space-y-1 pl-4">
                    {result.notes.map((note, index) => (
                      <li key={index}>
                        {t(`environments.cloneNote.${note.code}`, {
                          name: note.name,
                          detail: note.detail ?? "",
                        })}
                      </li>
                    ))}
                  </ul>
                </AlertDescription>
              )}
            </Alert>
            <DialogFooter>
              <Button onClick={() => close(false)}>{t("common.done")}</Button>
            </DialogFooter>
          </div>
        ) : (
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              clone.mutate()
            }}
          >
            <Field>
              <FieldLabel htmlFor="clone-name">{t("projects.environmentName")}</FieldLabel>
              <Input
                id="clone-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="staging"
                autoFocus
                required
              />
            </Field>
            <Field orientation="horizontal">
              <Switch id="clone-deploy" checked={deploy} onCheckedChange={setDeploy} />
              <div>
                <FieldLabel htmlFor="clone-deploy">{t("environments.cloneDeploy")}</FieldLabel>
                <FieldDescription>{t("environments.cloneDeployHelp")}</FieldDescription>
              </div>
            </Field>
            {clone.error && <ErrorDisplay error={clone.error} compact />}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => close(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={!name.trim() || clone.isPending || !environment}>
                {clone.isPending && <Spinner />}
                {t("environments.clone")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
