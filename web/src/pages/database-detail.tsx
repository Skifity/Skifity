import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArchiveRestoreIcon,
  ArrowLeftIcon,
  DatabaseBackupIcon,
  DatabaseIcon,
  EyeIcon,
  EyeOffIcon,
  LinkIcon,
  Trash2Icon,
  UnlinkIcon,
} from "lucide-react"
import { toast } from "sonner"

import { CopyButton } from "@/components/copy-button"
import { EmptyState } from "@/components/empty-state"
import { useDeleteConfirm } from "@/components/confirm-dialog"
import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { ScheduleField } from "@/components/schedule-field"
import { Page, PageHeader } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type {
  App,
  Backup,
  BackupPolicy,
  Database,
  DatabaseCredentials,
  DatabaseLink,
} from "@/lib/types"

export function DatabaseDetailPage() {
  const { t } = useTranslation()
  const { databaseId = "" } = useParams()
  const navigate = useNavigate()
  const confirmDelete = useDeleteConfirm()
  const { team } = useSession()

  // The poll below stops once the database is running, which is right; these
  // are what make the page move before that, and what catches a backup
  // finishing without waiting for the next tick.
  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      database: () => void queryClient.invalidateQueries({ queryKey: ["database", databaseId] }),
      backups: () => void queryClient.invalidateQueries({ queryKey: ["backups", databaseId] }),
      operation: () => void queryClient.invalidateQueries({ queryKey: ["database", databaseId] }),
    },
    Boolean(team),
  )

  const database = useQuery({
    queryKey: ["database", databaseId],
    queryFn: () =>
      api.get<{ database: Database; links: DatabaseLink[] }>(`/api/databases/${databaseId}`),
    refetchInterval: (query) => (query.state.data?.database.status === "running" ? false : 5_000),
  })

  const remove = useMutation({
    mutationFn: () => api.delete(`/api/databases/${databaseId}`),
    onSuccess: () => navigate("/databases"),
  })

  if (database.error) {
    return <ErrorDisplay error={database.error} onRetry={() => void database.refetch()} />
  }
  if (database.isLoading || !database.data) return <Skeleton className="h-96" />

  const record = database.data.database
  const links = database.data.links ?? []

  return (
    <Page>
      <PageHeader
        icon={DatabaseIcon}
        title={record.name}
        description={record.status_detail}
        back={
          <Button variant="ghost" size="sm" asChild className="-ml-2">
            <Link to="/databases">
              <ArrowLeftIcon />
              {t("databases.title")}
            </Link>
          </Button>
        }
        badge={
          <>
            <StatusBadge
              status={record.status}
              label={t(`databases.status.${record.status}`, { defaultValue: record.status })}
            />
            <Badge variant="secondary" className="font-mono text-[10px]">
              {record.engine} {record.engine_version} · {record.storage_gb} GB
            </Badge>
          </>
        }
      />

      <Tabs defaultValue="connection">
        <TabsList>
          <TabsTrigger value="connection">{t("databases.connectionDetails")}</TabsTrigger>
          <TabsTrigger value="apps">{t("databases.linkedApps")}</TabsTrigger>
          <TabsTrigger value="backups">{t("databases.backups")}</TabsTrigger>
        </TabsList>

        <TabsContent value="connection" className="pt-4">
          <ConnectionPanel databaseId={databaseId} />
        </TabsContent>

        <TabsContent value="apps" className="pt-4">
          <LinkedApps database={record} links={links} />
        </TabsContent>

        <TabsContent value="backups" className="pt-4">
          <BackupsPanel databaseId={databaseId} />
        </TabsContent>
      </Tabs>

      <Card className="border-destructive/30">
        <CardHeader>
          <CardTitle className="text-base text-destructive">{t("common.delete")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">{t("databases.restoreWarning")}</p>
          {remove.error != null && <ErrorDisplay error={remove.error} compact />}
          <Button
            variant="destructive"
            disabled={remove.isPending}
            onClick={() => {
              void confirmDelete(
                record.name,
                t("databases.deleteWarning"),
                t("common.cannotBeUndone"),
              ).then((yes) => {
                if (yes) remove.mutate()
              })
            }}
          >
            <Trash2Icon className="size-4" />
            {t("common.delete")}
          </Button>
        </CardContent>
      </Card>
    </Page>
  )
}

/**
 * Credentials are fetched only when asked for.
 *
 * The panel audits every read of a database password, so showing them on page
 * load would fill the audit log with entries nobody meant to create.
 */
function ConnectionPanel({ databaseId }: { databaseId: string }) {
  const { t } = useTranslation()
  const [revealed, setRevealed] = useState(false)

  const credentials = useQuery({
    queryKey: ["credentials", databaseId],
    queryFn: () => api.get<DatabaseCredentials>(`/api/databases/${databaseId}/credentials`),
    enabled: revealed,
    gcTime: 0,
  })

  if (!revealed) {
    return (
      <Card>
        <CardContent className="space-y-4 pt-6">
          <Alert>
            <AlertTitle>{t("databases.showCredentials")}</AlertTitle>
            <AlertDescription>{t("databases.credentialsWarning")}</AlertDescription>
          </Alert>
          <Button variant="outline" onClick={() => setRevealed(true)}>
            <EyeIcon className="size-4" />
            {t("databases.showCredentials")}
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (credentials.isLoading) return <Skeleton className="h-64" />
  if (credentials.error) {
    return <ErrorDisplay error={credentials.error} onRetry={() => void credentials.refetch()} />
  }

  const data = credentials.data!

  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <div className="grid gap-4 sm:grid-cols-2">
          <CredentialRow label={t("databases.host")} value={data.host} />
          <CredentialRow label={t("databases.port")} value={String(data.port)} />
          <CredentialRow label={t("databases.databaseName")} value={data.database} />
          <CredentialRow label={t("databases.user")} value={data.username} />
        </div>
        <CredentialRow label={t("auth.password")} value={data.password} secret />
        {/* The connection string carries the password inside it. */}
        <CredentialRow label={t("databases.connectionString")} value={data.url} secret />
        <p className="text-xs text-muted-foreground">{t("databases.credentialsWarning")}</p>
      </CardContent>
    </Card>
  )
}

function CredentialRow({ label, value, secret }: { label: string; value: string; secret?: boolean }) {
  const { t } = useTranslation()
  const [shown, setShown] = useState(false)

  return (
    <Field>
      <FieldLabel className="text-xs text-muted-foreground">{label}</FieldLabel>
      <div className="flex items-center gap-2">
        {/* Masked even here, and the eye is per field.
            This row carried a `secret` prop that chose between type="text" and
            type="text" — it had never masked anything. The first fix was to
            delete the prop: the card is already behind "Show credentials", so
            why hide twice? Because that is not what the gate is for. PlanetScale
            will not show a password again at all, Cloudflare and Retool keep an
            eye toggle on the field, Laravel Cloud masks the whole block behind
            one. The gate is consent to fetch the secret; the mask is so that
            fetching the host does not put the password on a screen somebody is
            sharing. The copy button hands over the value without showing it. */}
        <Input
          readOnly
          value={value}
          type={secret && !shown ? "password" : "text"}
          className="font-mono text-xs"
        />
        {secret && (
          <Button
            variant="outline"
            size="icon"
            aria-label={shown ? t("common.hide") : t("common.show")}
            onClick={() => setShown(!shown)}
          >
            {shown ? <EyeOffIcon className="size-4" /> : <EyeIcon className="size-4" />}
          </Button>
        )}
        <CopyButton value={value} label={label} variant="outline" />
      </div>
    </Field>
  )
}

function LinkedApps({ database, links }: { database: Database; links: DatabaseLink[] }) {
  const { t } = useTranslation()
  const confirmUnlink = useConfirm()
  const [appId, setAppId] = useState("")
  const [varName, setVarName] = useState("")

  const apps = useQuery({
    queryKey: ["apps", database.environment_id],
    queryFn: () => api.get<List<App>>(`/api/environments/${database.environment_id}/apps`),
  })

  const link = useMutation({
    mutationFn: () =>
      api.post(`/api/databases/${database.id}/link`, {
        app_id: appId,
        var_name: varName.trim(),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["database", database.id] })
      setAppId("")
      setVarName("")
    },
  })

  const unlink = useMutation({
    mutationFn: (target: string) => api.delete(`/api/databases/${database.id}/link/${target}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["database", database.id] }),
  })

  const byId = new Map((apps.data?.items ?? []).map((app) => [app.id, app]))
  const available = (apps.data?.items ?? []).filter(
    (app) => !links.some((entry) => entry.app_id === app.id),
  )

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("databases.linkAppHelp")}</p>

      {links.length === 0 ? (
        <EmptyState
          icon={LinkIcon}
          title={t("databases.linkedApps")}
          description={t("databases.linkAppHelp")}
        />
      ) : (
        <Card>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("nav.apps")}</TableHead>
                  <TableHead>{t("databases.variableName")}</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {links.map((entry) => (
                  <TableRow key={entry.app_id}>
                    <TableCell className="font-medium">
                      <Link to={`/apps/${entry.app_id}`} className="hover:text-primary">
                        {byId.get(entry.app_id)?.name ?? entry.app_id}
                      </Link>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{entry.var_name}</TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("databases.unlink")}
                        disabled={unlink.isPending}
                        // The variable goes with it, so the app stops being
                        // able to reach this database on its next deployment.
                        onClick={() =>
                          void confirmUnlink({
                            title: t("databases.unlink"),
                            description: t("databases.unlinkConfirm", {
                              app: byId.get(entry.app_id)?.name ?? entry.app_id,
                              variable: entry.var_name,
                            }),
                            confirmLabel: t("databases.unlink"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) unlink.mutate(entry.app_id)
                          })
                        }
                      >
                        <UnlinkIcon className="size-4 text-muted-foreground" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {available.length > 0 && (
        <Card>
          <CardContent className="pt-6">
            <form
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                link.mutate()
              }}
            >
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="link-app">{t("databases.linkApp")}</FieldLabel>
                  <Select value={appId} onValueChange={setAppId}>
                    <SelectTrigger id="link-app">
                      <SelectValue placeholder={t("databases.linkApp")} />
                    </SelectTrigger>
                    <SelectContent>
                      {available.map((app) => (
                        <SelectItem key={app.id} value={app.id}>
                          {app.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                <Field>
                  <FieldLabel htmlFor="link-var">
                    {t("databases.variableName")}{" "}
                    <span className="text-muted-foreground">({t("common.optional")})</span>
                  </FieldLabel>
                  <Input
                    id="link-var"
                    value={varName}
                    onChange={(event) => setVarName(event.target.value)}
                    placeholder="DATABASE_URL"
                    className="font-mono"
                  />
                </Field>
              </div>
              {link.error != null && <ErrorDisplay error={link.error} compact />}
              <div className="flex justify-end">
                <Button type="submit" disabled={!appId || link.isPending}>
                  <LinkIcon className="size-4" />
                  {link.isPending && <Spinner />}
                  {link.isPending ? t("common.saving") : t("databases.linkApp")}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      {unlink.error != null && <ErrorDisplay error={unlink.error} compact />}
    </div>
  )
}

function BackupsPanel({ databaseId }: { databaseId: string }) {
  const { t } = useTranslation()
  const confirmRestore = useConfirm()

  const backups = useQuery({
    queryKey: ["backups", databaseId],
    queryFn: () => api.get<List<Backup>>(`/api/databases/${databaseId}/backups`),
    refetchInterval: (query) =>
      (query.state.data?.items ?? []).some((backup) => backup.status === "running") ? 5_000 : false,
  })

  const policy = useQuery({
    queryKey: ["backup-policy", databaseId],
    queryFn: () => api.get<BackupPolicy>(`/api/databases/${databaseId}/backup-policy`),
  })

  const [schedule, setSchedule] = useState<string | null>(null)
  const [retention, setRetention] = useState<string | null>(null)
  const [enabled, setEnabled] = useState<boolean | null>(null)

  const current = policy.data
  const scheduleValue = schedule ?? current?.schedule ?? "0 3 * * *"
  const retentionValue = retention ?? String(current?.retention ?? 7)
  const enabledValue = enabled ?? current?.enabled ?? false

  const savePolicy = useMutation({
    mutationFn: () =>
      api.put(`/api/databases/${databaseId}/backup-policy`, {
        schedule: scheduleValue,
        retention: Number(retentionValue) || 7,
        enabled: enabledValue,
      }),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ["backup-policy", databaseId] }),
  })

  const backupNow = useMutation({
    mutationFn: () => api.post(`/api/databases/${databaseId}/backups`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["backups", databaseId] }),
  })

  const restore = useMutation({
    mutationFn: (backupID: string) => api.post(`/api/databases/${databaseId}/restore/${backupID}`),
    onSuccess: () => toast.success(t("databases.restoreStarted")),
  })

  const items = backups.data?.items ?? []

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("databases.backupSchedule")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">{t("databases.backupScheduleHelp")}</p>
          {/* Three controls used to be labelled "Automatic backups": the card,
              the switch and the schedule. Each says what it is now. */}
          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">{t("databases.backupsOn")}</span>
            <Switch checked={enabledValue} onCheckedChange={setEnabled} />
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <ScheduleField
              id="backup-schedule"
              value={scheduleValue}
              onChange={setSchedule}
              description={t("databases.backupScheduleFieldHelp")}
            />
            <Field>
              <FieldLabel htmlFor="backup-retention">{t("databases.retention")}</FieldLabel>
              <Input
                id="backup-retention"
                type="number"
                min={1}
                value={retentionValue}
                onChange={(event) => setRetention(event.target.value)}
              />
              <FieldDescription>{t("databases.retentionHelp")}</FieldDescription>
            </Field>
          </div>
          {savePolicy.error != null && <ErrorDisplay error={savePolicy.error} compact />}
          <div className="flex justify-end">
            <Button disabled={savePolicy.isPending} onClick={() => savePolicy.mutate()}>
              {savePolicy.isPending && <Spinner />}
              {savePolicy.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-base font-medium">{t("databases.backups")}</h2>
        <Button size="sm" disabled={backupNow.isPending} onClick={() => backupNow.mutate()}>
          <DatabaseBackupIcon className="size-4" />
          {t("databases.backupNow")}
        </Button>
      </div>

      {backupNow.error != null && <ErrorDisplay error={backupNow.error} />}
      {restore.error != null && <ErrorDisplay error={restore.error} />}

      {backups.isLoading ? (
        <Skeleton className="h-40" />
      ) : items.length === 0 ? (
        <EmptyState
          icon={DatabaseBackupIcon}
          title={t("databases.noBackups")}
          description={t("databases.backupScheduleHelp")}
          action={<Button onClick={() => backupNow.mutate()}>{t("databases.backupNow")}</Button>}
        />
      ) : (
        <Card>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("common.created")}</TableHead>
                  <TableHead>{t("common.status")}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t("common.size")}</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((backup) => (
                  <TableRow key={backup.id}>
                    <TableCell>
                      <div className="text-sm">{formatDateTime(backup.created_at)}</div>
                      <div className="text-xs text-muted-foreground">
                        {formatRelative(backup.created_at)}
                      </div>
                    </TableCell>
                    <TableCell>
                      <StatusBadge
                        status={backup.status}
                        label={t(`databases.backupStatus.${backup.status}`, {
                          defaultValue: backup.status,
                        })}
                      />
                      {backup.error_message && (
                        <p className="mt-1 text-xs text-destructive">{backup.error_message}</p>
                      )}
                    </TableCell>
                    <TableCell className="hidden tabular-nums sm:table-cell">
                      {backup.size_bytes ? formatBytes(backup.size_bytes) : "—"}
                    </TableCell>
                    <TableCell>
                      {backup.status === "succeeded" && (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={restore.isPending}
                          onClick={() => {
                            void confirmRestore({
                              title: t("databases.restore"),
                              description: t("databases.restoreWarning"),
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
          </CardContent>
        </Card>
      )}
    </div>
  )
}
