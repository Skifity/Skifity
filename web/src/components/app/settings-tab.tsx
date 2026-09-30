import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { Trash2Icon } from "lucide-react"

import { useDeleteConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App } from "@/lib/types"
import { Spinner } from "@/components/ui/spinner"

export function SettingsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const confirmDelete = useDeleteConfirm()

  const [name, setName] = useState(app.name)
  const [branch, setBranch] = useState(app.branch)
  const [rootDir, setRootDir] = useState(app.root_dir)
  const [image, setImage] = useState(app.image)
  const [port, setPort] = useState(String(app.port || ""))
  const [healthPath, setHealthPath] = useState(app.health_path)
  const [buildCommand, setBuildCommand] = useState(app.build_command)
  const [staticDir, setStaticDir] = useState(app.static_dir)
  const [startCommand, setStartCommand] = useState(app.start_command)
  const [releaseCommand, setReleaseCommand] = useState(app.release_command)
  const [autoDeploy, setAutoDeploy] = useState(app.auto_deploy)
  const [previewDeploys, setPreviewDeploys] = useState(app.preview_deploys)

  const save = useMutation({
    mutationFn: () =>
      api.patch(`/api/apps/${app.id}`, {
        name: name.trim(),
        branch: branch.trim(),
        root_dir: rootDir.trim(),
        image: image.trim(),
        port: Number(port) || 0,
        health_path: healthPath.trim(),
        build_command: buildCommand.trim(),
        static_dir: staticDir.trim(),
        start_command: startCommand.trim(),
        release_command: releaseCommand.trim(),
        auto_deploy: autoDeploy,
        preview_deploys: previewDeploys,
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["app", app.id] }),
  })

  const remove = useMutation({
    mutationFn: () => api.delete(`/api/apps/${app.id}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["apps", app.environment_id] })
      navigate("/projects")
    },
  })

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("apps.settings")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <Field>
            <FieldLabel htmlFor="settings-name">{t("apps.appName")}</FieldLabel>
            <Input
              id="settings-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>

          {app.source_type === "image" ? (
            <Field>
              <FieldLabel htmlFor="settings-image">{t("apps.image")}</FieldLabel>
              <Input
                id="settings-image"
                value={image}
                onChange={(event) => setImage(event.target.value)}
                className="font-mono"
              />
            </Field>
          ) : (
            <>
              {/* A folder sent with `skifity up` has no branch to follow. */}
              {app.source_type === "git" && (
                <Field>
                  <FieldLabel htmlFor="settings-branch">{t("apps.branch")}</FieldLabel>
                  <Input
                    id="settings-branch"
                    value={branch}
                    onChange={(event) => setBranch(event.target.value)}
                    className="font-mono"
                  />
                </Field>
              )}
              <Field>
                <FieldLabel htmlFor="settings-root">{t("apps.rootDirectory")}</FieldLabel>
                <Input
                  id="settings-root"
                  value={rootDir}
                  onChange={(event) => setRootDir(event.target.value)}
                  className="font-mono"
                />
                <FieldDescription>{t("apps.rootDirectoryHelp")}</FieldDescription>
              </Field>
              {/*
                A front end is built and then served, and the two fields that
                say how were detected and then thrown away. Without them the
                image holds the repository's own source and the page is blank.
              */}
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="settings-build-command">{t("apps.buildCommand")}</FieldLabel>
                  <Input
                    id="settings-build-command"
                    value={buildCommand}
                    onChange={(event) => setBuildCommand(event.target.value)}
                    placeholder="npm run build"
                    className="font-mono"
                  />
                  <FieldDescription>{t("apps.buildCommandHelp")}</FieldDescription>
                </Field>
                <Field>
                  <FieldLabel htmlFor="settings-static-dir">{t("apps.outputDirectory")}</FieldLabel>
                  <Input
                    id="settings-static-dir"
                    value={staticDir}
                    onChange={(event) => setStaticDir(event.target.value)}
                    placeholder="dist"
                    className="font-mono"
                  />
                  <FieldDescription>{t("apps.outputDirectoryHelp")}</FieldDescription>
                </Field>
              </div>
            </>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="settings-port">{t("apps.port")}</FieldLabel>
              <Input
                id="settings-port"
                type="number"
                min={0}
                max={65535}
                value={port}
                onChange={(event) => setPort(event.target.value)}
              />
              <FieldDescription>{t("apps.portHelp", { product: "Skifity" })}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="settings-health">{t("apps.healthPath")}</FieldLabel>
              <Input
                id="settings-health"
                value={healthPath}
                onChange={(event) => setHealthPath(event.target.value)}
                className="font-mono"
              />
            </Field>
          </div>

          <Field>
            <FieldLabel htmlFor="settings-command">{t("apps.startCommand")}</FieldLabel>
            <Input
              id="settings-command"
              value={startCommand}
              onChange={(event) => setStartCommand(event.target.value)}
              className="font-mono"
            />
          </Field>

          <Field>
            <FieldLabel htmlFor="settings-release">{t("apps.releaseCommand")}</FieldLabel>
            <Input
              id="settings-release"
              value={releaseCommand}
              onChange={(event) => setReleaseCommand(event.target.value)}
              placeholder="npm run migrate"
              className="font-mono"
            />
            <FieldDescription>{t("apps.releaseCommandHelp")}</FieldDescription>
          </Field>

          {app.source_type === "git" && (
            <>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldTitle>{t("apps.deployOnPush")}</FieldTitle>
                </FieldContent>
                <Switch checked={autoDeploy} onCheckedChange={setAutoDeploy} />
              </Field>
              <Field orientation="horizontal">
                <FieldContent>
                  <FieldTitle>{t("apps.previewEnvironments")}</FieldTitle>
                  <FieldDescription>{t("apps.previewEnvironmentsHelp")}</FieldDescription>
                </FieldContent>
                <Switch checked={previewDeploys} onCheckedChange={setPreviewDeploys} />
              </Field>
            </>
          )}

          {save.error != null && <ErrorDisplay error={save.error} compact />}

          <div className="flex justify-end">
            <Button disabled={save.isPending} onClick={() => save.mutate()}>
              {save.isPending && <Spinner />}
              {save.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="border-destructive/30">
        <CardHeader>
          <CardTitle className="text-base text-destructive">{t("apps.deleteApp")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">{t("apps.deleteAppWarning")}</p>
          {remove.error != null && <ErrorDisplay error={remove.error} compact />}
          <Button
            variant="destructive"
            disabled={remove.isPending}
            onClick={() => {
              void confirmDelete(app.name, t("apps.deleteAppWarning")).then((yes) => {
                if (yes) remove.mutate()
              })
            }}
          >
            <Trash2Icon className="size-4" />
            {t("apps.deleteApp")}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
