import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { FileCodeIcon, LockIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
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
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { api, type List } from "@/lib/api"
import { formatBytes } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, AppFile } from "@/lib/types"

/** What the dialog is editing: a new file, or one that is already there. */
type Editing = { file?: AppFile }

/**
 * An app's files: configuration its containers read at a path — an
 * nginx.conf, a Caddyfile, a script run at start. Saving one restarts the app,
 * like a variable, and never rebuilds it.
 */
export function FilesCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const files = useQuery({
    queryKey: ["files", app.id],
    queryFn: () => api.get<List<AppFile>>(`/api/apps/${app.id}/files`),
  })
  const [editing, setEditing] = useState<Editing | null>(null)

  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/api/apps/${app.id}/files/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["files", app.id] }),
  })

  if (files.isLoading) return <Skeleton className="h-32" />
  if (files.error) return <ErrorDisplay error={files.error} onRetry={() => void files.refetch()} />
  const items = files.data?.items ?? []

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <FileCodeIcon className="size-4" />
            {t("files.title")}
          </CardTitle>
          <CardDescription>{t("files.help")}</CardDescription>
        </div>
        <Button variant="outline" size="sm" onClick={() => setEditing({})}>
          <PlusIcon className="size-4" />
          {t("files.add")}
        </Button>
      </CardHeader>
      <CardContent className="space-y-2">
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("files.empty")}</p>
        ) : (
          <ul className="divide-y rounded-md border">
            {items.map((file) => (
              <li key={file.id} className="flex items-center gap-3 px-3 py-2">
                <code className="min-w-0 flex-1 truncate font-mono text-xs" title={file.path}>
                  {file.path}
                </code>
                {file.is_secret && (
                  <Badge variant="secondary" className="gap-1">
                    <LockIcon className="size-3" />
                    {t("files.secret")}
                  </Badge>
                )}
                {file.executable && <Badge variant="outline">{t("files.executable")}</Badge>}
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatBytes(file.size)}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("files.edit", { path: file.path })}
                  onClick={() => setEditing({ file })}
                >
                  <PencilIcon className="size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("files.remove", { path: file.path })}
                  disabled={remove.isPending}
                  onClick={() =>
                    void confirm({
                      title: t("files.remove", { path: file.path }),
                      description: t("files.removeConfirm", { path: file.path }),
                      confirmLabel: t("common.delete"),
                      destructive: true,
                    }).then((yes) => {
                      if (yes) remove.mutate(file.id)
                    })
                  }
                >
                  <Trash2Icon className="size-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>
      {editing && (
        <FileDialog
          // A fresh form for each file opened, rather than one that has to
          // be reset to it.
          key={editing.file?.id ?? "new"}
          app={app}
          file={editing.file}
          onClose={() => setEditing(null)}
        />
      )}
    </Card>
  )
}

function FileDialog({ app, file, onClose }: { app: App; file?: AppFile; onClose: () => void }) {
  const { t } = useTranslation()
  const [path, setPath] = useState(file?.path ?? "")
  // A secret file's content is never sent back, so its editor starts empty
  // and saving it empty would wipe it: the button waits for new content.
  const [content, setContent] = useState(file?.content ?? "")
  const [secret, setSecret] = useState(file?.is_secret ?? false)
  const [executable, setExecutable] = useState(file?.executable ?? false)
  const keepsSecret = file?.is_secret === true && content === ""

  const save = useMutation({
    mutationFn: () =>
      api.put<{ file: AppFile }>(`/api/apps/${app.id}/files`, {
        path: path.trim(),
        content,
        is_secret: secret,
        executable,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["files", app.id] })
      onClose()
    },
  })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{file ? t("files.edit", { path: file.path }) : t("files.add")}</DialogTitle>
          <DialogDescription>{t("files.restartNote")}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            save.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="file-path">{t("files.path")}</FieldLabel>
            <Input
              id="file-path"
              value={path}
              // The path is what the file is; a different one is a new file.
              disabled={file !== undefined}
              placeholder="/etc/nginx/conf.d/default.conf"
              className="font-mono"
              spellCheck={false}
              onChange={(event) => setPath(event.target.value)}
            />
            <FieldDescription>{t("files.pathHelp")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="file-content">{t("files.content")}</FieldLabel>
            <Textarea
              id="file-content"
              value={content}
              rows={14}
              className="max-h-[50vh] font-mono text-xs"
              spellCheck={false}
              placeholder={file?.is_secret ? t("files.secretStored") : undefined}
              onChange={(event) => setContent(event.target.value)}
            />
          </Field>
          <div className="space-y-3">
            <label className="flex items-start gap-2 text-sm">
              <Checkbox
                checked={secret}
                onCheckedChange={(checked) => setSecret(checked === true)}
                className="mt-0.5"
              />
              <span>
                <span className="font-medium">{t("files.secret")}</span>
                <span className="block text-muted-foreground">{t("files.secretHelp")}</span>
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm">
              <Checkbox
                checked={executable}
                onCheckedChange={(checked) => setExecutable(checked === true)}
                className="mt-0.5"
              />
              <span>
                <span className="font-medium">{t("files.executable")}</span>
                <span className="block text-muted-foreground">{t("files.executableHelp")}</span>
              </span>
            </label>
          </div>
          {save.error != null && <ErrorDisplay error={save.error} compact />}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={save.isPending || path.trim() === "" || keepsSecret}>
              {save.isPending && <Spinner />}
              {t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
