import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArchiveRestoreIcon,
  CalendarClockIcon,
  DownloadIcon,
  HardDriveIcon,
  InfoIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm, useDeleteConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay, toProblem } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScheduleField } from "@/components/schedule-field"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import { formatRelative } from "@/lib/format"
import type { App, Backup, BackupPolicy, Volume } from "@/lib/types"

export function StorageTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState("data")
  const [mountPath, setMountPath] = useState("/data")
  const [sizeGB, setSizeGB] = useState("5")

  const volumes = useQuery({
    queryKey: ["volumes", app.id],
    queryFn: () => api.get<List<Volume>>(`/api/apps/${app.id}/volumes`),
  })

  const add = useMutation({
    mutationFn: () =>
      api.post(`/api/apps/${app.id}/volumes`, {
        name: name.trim(),
        mount_path: mountPath.trim(),
        size_gb: Number(sizeGB) || 1,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["volumes", app.id] })
      setAdding(false)
    },
  })

  const remove = useMutation({
    mutationFn: (volumeID: string) => api.delete(`/api/apps/${app.id}/volumes/${volumeID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["volumes", app.id] }),
  })

  // The panel makes somebody type the name before it deletes an app, a
  // project, a database or a server. This was one click on a bin icon, and it
  // is the one that destroys data nothing can get back: an app can be deployed
  // again from the same commit, a disk cannot.
  //
  // The consequence names the safety net, or says there is none. Adobe lists
  // what will be gone for good, Laravel Cloud says it will take a final backup
  // first, Render says to move the services out if you want to keep them — the
  // dialog is where somebody finds out whether they can afford to press it, and
  // this screen already knows when the disk was last copied.
  const confirmDelete = useDeleteConfirm()
  const askThenRemove = (volume: Volume, lastBackup?: string) => {
    void confirmDelete(
      volume.name,
      t("apps.deleteVolumeConfirm", { path: volume.mount_path }),
      lastBackup
        ? t("apps.deleteVolumeLastBackup", { when: formatRelative(lastBackup) })
        : t("apps.deleteVolumeNeverBackedUp"),
    ).then((yes) => {
      if (yes) remove.mutate(volume.id)
    })
  }

  if (volumes.isLoading) return <Skeleton className="h-40" />
  if (volumes.error)
    return <ErrorDisplay error={volumes.error} onRetry={() => void volumes.refetch()} />

  const items = volumes.data?.items ?? []

  return (
    <div className="space-y-4">
      {adding && (
        <Card>
          <CardContent className="pt-6">
            <form
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                add.mutate()
              }}
            >
              <div className="grid gap-4 sm:grid-cols-3">
                <Field>
                  <FieldLabel htmlFor="volume-name">{t("common.name")}</FieldLabel>
                  <Input
                    id="volume-name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    className="font-mono"
                    required
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="volume-path">{t("apps.mountPath")}</FieldLabel>
                  <Input
                    id="volume-path"
                    value={mountPath}
                    onChange={(event) => setMountPath(event.target.value)}
                    className="font-mono"
                    placeholder="/data"
                    required
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="volume-size">{t("apps.sizeGB")}</FieldLabel>
                  <Input
                    id="volume-size"
                    type="number"
                    min={1}
                    value={sizeGB}
                    onChange={(event) => setSizeGB(event.target.value)}
                  />
                </Field>
              </div>
              <p className="text-xs text-muted-foreground">{t("apps.volumeHelp")}</p>
              {add.error != null && <ErrorDisplay error={add.error} compact />}
              <div className="flex justify-end gap-2">
                <Button type="button" variant="ghost" onClick={() => setAdding(false)}>
                  {t("common.cancel")}
                </Button>
                <Button type="submit" disabled={add.isPending}>
                  {add.isPending && <Spinner />}
                  {add.isPending ? t("common.saving") : t("common.add")}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      {/*
        The first disk changes how every deploy from then on behaves, and
        nothing said so. An app with a disk cannot do a rolling deploy — one
        disk, one writer — so Skifity stops the old instance before starting
        the new one, and there are a few seconds with nothing answering.
        Finding that out from a deploy is finding it out too late.
      */}
      {items.length > 0 && (
        <Alert>
          <InfoIcon className="size-4" />
          <AlertTitle>{t("apps.volumeStopsRollingDeploys")}</AlertTitle>
          <AlertDescription>{t("apps.volumeStopsRollingDeploysHelp")}</AlertDescription>
        </Alert>
      )}

      {items.length === 0 && !adding ? (
        <EmptyState
          icon={HardDriveIcon}
          title={t("apps.noVolumes")}
          // It used to borrow the scaling tab's sentence about spreading
          // instances across servers, which is about something else entirely.
          description={t("apps.noVolumesHelp")}
          action={
            <Button onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("apps.addVolume")}
            </Button>
          }
        />
      ) : (
        <>
          <div className="flex justify-end">
            <Button size="sm" onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("apps.addVolume")}
            </Button>
          </div>
          {items.length > 0 && (
            <Card>
              <CardContent className="p-0">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("common.name")}</TableHead>
                      <TableHead>{t("apps.mountPath")}</TableHead>
                      <TableHead>{t("common.size")}</TableHead>
                      <TableHead>{t("apps.lastBackup")}</TableHead>
                      <TableHead className="w-24" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {items.map((volume) => (
                      <VolumeRow
                        key={volume.id}
                        appID={app.id}
                        volume={volume}
                        deleting={remove.isPending}
                        onDelete={askThenRemove}
                      />
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          )}
        </>
      )}

      {remove.error != null && <ErrorDisplay error={remove.error} compact />}
    </div>
  )
}

/**
 * One disk, and when it was last copied somewhere safe.
 *
 * The backup query lives here rather than in the cell, because two things need
 * the answer: the column, and the dialog that asks whether to destroy the disk.
 * A volume with no backup says so rather than showing an empty cell — "never"
 * is the thing somebody needs to see, and a blank column reads as a column that
 * has not loaded.
 */
function VolumeRow({
  appID,
  volume,
  deleting,
  onDelete,
}: {
  appID: string
  volume: Volume
  deleting: boolean
  onDelete: (volume: Volume, lastBackup?: string) => void
}) {
  const { t } = useTranslation()
  const backups = useQuery({
    queryKey: ["volume-backups", volume.id],
    queryFn: () => api.get<List<Backup>>(`/api/apps/${appID}/volumes/${volume.id}/backups?limit=1`),
  })

  const [showBackups, setShowBackups] = useState(false)

  const latest = backups.data?.items?.[0]
  // Only a backup that worked is a backup.
  const safe = latest && latest.status !== "failed" ? latest.created_at : undefined

  return (
    <TableRow>
      <TableCell className="font-mono text-xs font-medium">{volume.name}</TableCell>
      <TableCell className="font-mono text-xs">{volume.mount_path}</TableCell>
      <TableCell className="tabular-nums">{volume.size_gb} GB</TableCell>
      <TableCell className="text-xs text-muted-foreground">
        {backups.isLoading ? (
          <Skeleton className="h-4 w-20" />
        ) : latest ? (
          <span>
            {formatRelative(latest.created_at)}
            {latest.status === "failed" && ` · ${t("databases.status.failed")}`}
          </span>
        ) : (
          t("common.never")
        )}
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-1">
          <BackUpNow appID={appID} volumeID={volume.id} />
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("apps.volumeBackups", { name: volume.name })}
            title={t("apps.volumeBackups", { name: volume.name })}
            onClick={() => setShowBackups(true)}
          >
            <CalendarClockIcon className="size-4 text-muted-foreground" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("common.delete")}
            disabled={deleting}
            onClick={() => onDelete(volume, safe)}
          >
            <Trash2Icon className="size-4 text-muted-foreground" />
          </Button>
        </div>
        <VolumeBackupsDialog
          appID={appID}
          volume={volume}
          open={showBackups}
          onOpenChange={setShowBackups}
        />
      </TableCell>
    </TableRow>
  )
}

/**
 * A volume's backups: the schedule, the copies, and putting one back.
 *
 * All three were missing. The engine could already run a volume backup on a
 * schedule — the scheduler reads a policy's target type and a volume is one of
 * them — and there was no way to create the policy, so the answer to "back up
 * my uploads every night" was to press a button every night. And the restore
 * job had existed since volume backups were added, with nothing calling it: a
 * backup you cannot restore is a file you are paying to store.
 */
function VolumeBackupsDialog({
  appID,
  volume,
  open,
  onOpenChange,
}: {
  appID: string
  volume: Volume
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const confirmRestore = useConfirm()

  const policy = useQuery({
    queryKey: ["volume-backup-policy", volume.id],
    queryFn: () => api.get<BackupPolicy>(`/api/apps/${appID}/volumes/${volume.id}/backup-policy`),
    enabled: open,
  })
  const backups = useQuery({
    queryKey: ["volume-backups", volume.id, "all"],
    queryFn: () => api.get<List<Backup>>(`/api/apps/${appID}/volumes/${volume.id}/backups`),
    enabled: open,
  })

  // Derived during render rather than copied in by an effect: the form shows
  // what was typed, falling back to what the panel answered with.
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [schedule, setSchedule] = useState<string | null>(null)
  const [retention, setRetention] = useState<string | null>(null)
  const enabledValue = enabled ?? policy.data?.enabled ?? false
  const scheduleValue = schedule ?? policy.data?.schedule ?? "0 3 * * *"
  const retentionValue = retention ?? String(policy.data?.retention ?? 7)

  const savePolicy = useMutation({
    mutationFn: () =>
      api.put(`/api/apps/${appID}/volumes/${volume.id}/backup-policy`, {
        enabled: enabledValue,
        schedule: scheduleValue,
        retention: Number(retentionValue) || 7,
      }),
    onSuccess: () => {
      toast.success(t("common.saved"))
      void queryClient.invalidateQueries({ queryKey: ["volume-backup-policy", volume.id] })
    },
  })

  const restore = useMutation({
    // overwrite is explicit: the panel refuses without it, because a restore
    // replaces what is on the disk now and stops the app while it runs.
    mutationFn: (backupID: string) =>
      api.post(`/api/apps/${appID}/volumes/${volume.id}/restore/${backupID}?overwrite=true`),
    onSuccess: () => {
      toast.success(t("apps.volumeRestoreStarted"))
      void queryClient.invalidateQueries({ queryKey: ["volume-backups", volume.id] })
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("apps.volumeBackups", { name: volume.name })}</DialogTitle>
          <DialogDescription>{t("apps.volumeBackupsHelp")}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">{t("databases.backupsOn")}</span>
            <Switch checked={enabledValue} onCheckedChange={setEnabled} />
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <ScheduleField
              id={`volume-schedule-${volume.id}`}
              value={scheduleValue}
              onChange={setSchedule}
              description={t("databases.backupScheduleFieldHelp")}
            />
            <Field>
              <FieldLabel htmlFor={`volume-retention-${volume.id}`}>
                {t("databases.retention")}
              </FieldLabel>
              <Input
                id={`volume-retention-${volume.id}`}
                type="number"
                min={1}
                value={retentionValue}
                onChange={(event) => setRetention(event.target.value)}
              />
            </Field>
          </div>
          {savePolicy.error != null && <ErrorDisplay error={savePolicy.error} compact />}
          <div className="flex justify-end">
            <Button disabled={savePolicy.isPending} onClick={() => savePolicy.mutate()}>
              {savePolicy.isPending && <Spinner />}
              {t("common.save")}
            </Button>
          </div>
        </div>

        {restore.error != null && <ErrorDisplay error={restore.error} />}

        <div className="max-h-72 overflow-y-auto rounded-md border">
          {backups.isLoading ? (
            <div className="p-4">
              <Skeleton className="h-16" />
            </div>
          ) : (backups.data?.items?.length ?? 0) === 0 ? (
            <p className="p-4 text-sm text-muted-foreground">{t("apps.noVolumeBackups")}</p>
          ) : (
            <Table>
              <TableBody>
                {backups.data?.items.map((backup) => (
                  <TableRow key={backup.id}>
                    <TableCell className="text-xs">{formatRelative(backup.created_at)}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {t(`databases.backupStatus.${backup.status}`, {
                        defaultValue: backup.status,
                      })}
                    </TableCell>
                    <TableCell className="text-right">
                      {backup.status === "succeeded" && (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={restore.isPending}
                          onClick={() => {
                            void confirmRestore({
                              title: t("databases.restore"),
                              description: t("apps.volumeRestoreWarning", { app: volume.name }),
                              consequence: t("common.cannotBeUndone"),
                              confirmLabel: t("databases.restoreConfirm"),
                              destructive: true,
                              typeToConfirm: t("databases.restore"),
                            }).then((yes) => {
                              if (yes) restore.mutate(backup.id)
                            })
                          }}
                        >
                          <ArchiveRestoreIcon className="size-4" />
                          {t("databases.restore")}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** Copies a volume to storage now. */
function BackUpNow({ appID, volumeID }: { appID: string; volumeID: string }) {
  const { t } = useTranslation()
  const run = useMutation({
    mutationFn: () => api.post(`/api/apps/${appID}/volumes/${volumeID}/backups`, {}),
    onSuccess: () => {
      toast.success(t("databases.backupStarted"))
      void queryClient.invalidateQueries({ queryKey: ["volume-backups", volumeID] })
    },
    onError: (error) => toast.error(toProblem(error)?.title ?? t("errors.somethingWentWrong")),
  })

  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={t("databases.backupNow")}
      title={t("databases.backupNow")}
      disabled={run.isPending}
      onClick={() => run.mutate()}
    >
      {run.isPending ? <Spinner /> : <DownloadIcon className="size-4 text-muted-foreground" />}
    </Button>
  )
}
