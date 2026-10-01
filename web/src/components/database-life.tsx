import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  FileUpIcon,
  HistoryIcon,
  KeyRoundIcon,
  PlayIcon,
  RulerIcon,
  SquareIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { OperationProgress } from "@/components/operation-progress"
import { StatusBadge } from "@/components/status-badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
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
import { useDatabaseEngines } from "@/hooks/use-database-engines"
import { api, type List } from "@/lib/api"
import { formatDateTime, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, Database, DatabaseEngine, DatabaseLink, Operation } from "@/lib/types"

/** The largest dump an import takes, as the panel says in its API. */
const IMPORT_LIMIT_BYTES = 5 * 1024 ** 3

function refresh(databaseId: string) {
  void queryClient.invalidateQueries({ queryKey: ["database", databaseId] })
  void queryClient.invalidateQueries({ queryKey: ["database-operations", databaseId] })
}

/**
 * A database's life after it is created: stopping and starting it, its size,
 * its password and importing a dump. Each asks first, and says what happens to
 * the apps that use it, because each of them reaches those apps.
 */
export function DatabaseLife({ database, links }: { database: Database; links: DatabaseLink[] }) {
  const engines = useDatabaseEngines()

  if (engines.error) {
    return <ErrorDisplay error={engines.error} onRetry={() => void engines.refetch()} />
  }
  if (!engines.data) return <Skeleton className="h-64" />
  const kind = engines.data.find((item) => item.name === database.engine)
  if (!kind) return null

  return (
    <div className="space-y-6">
      <PowerCard database={database} links={links} />
      <SizeCard database={database} kind={kind} />
      {kind.password && <PasswordCard database={database} kind={kind} links={links} />}
      {kind.imports.length > 0 && <ImportCard database={database} kind={kind} />}
    </div>
  )
}

function PowerCard({ database, links }: { database: Database; links: DatabaseLink[] }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const apps = useQuery({
    queryKey: ["apps", database.environment_id],
    queryFn: () => api.get<List<App>>(`/api/environments/${database.environment_id}/apps`),
  })
  const names = links
    .map((link) => apps.data?.items.find((app) => app.id === link.app_id)?.name ?? link.app_id)
    .join(", ")

  const stop = useMutation({
    // Asked with the linked apps named, so the refusal the panel would
    // otherwise give is already answered.
    mutationFn: () => api.post<Database>(`/api/databases/${database.id}/stop?force=true`),
    onSuccess: () => {
      toast.success(t("databases.life.stopped", { name: database.name }))
      refresh(database.id)
    },
  })
  const start = useMutation({
    mutationFn: () => api.post<Database>(`/api/databases/${database.id}/start`),
    onSuccess: () => {
      toast.success(t("databases.life.started", { name: database.name }))
      refresh(database.id)
    },
  })
  const stopped = database.status === "stopped"

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("databases.life.power")}</CardTitle>
        <CardDescription>{t("databases.life.powerHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {stop.error != null && <ErrorDisplay error={stop.error} compact />}
        {start.error != null && <ErrorDisplay error={start.error} compact />}
        {stopped ? (
          <Button disabled={start.isPending} onClick={() => start.mutate()}>
            {start.isPending ? <Spinner /> : <PlayIcon className="size-4" />}
            {t("databases.life.start")}
          </Button>
        ) : (
          <Button
            variant="outline"
            disabled={stop.isPending || database.status === "creating"}
            onClick={() => {
              void confirm({
                title: t("databases.life.stopTitle", { name: database.name }),
                description:
                  links.length > 0
                    ? t("databases.life.stopLinked", { apps: names })
                    : t("databases.life.stopUnlinked"),
                consequence: t("databases.life.stopConsequence"),
                confirmLabel: t("databases.life.stop"),
                destructive: links.length > 0,
              }).then((yes) => {
                if (yes) stop.mutate()
              })
            }}
          >
            {stop.isPending ? <Spinner /> : <SquareIcon className="size-4" />}
            {t("databases.life.stop")}
          </Button>
        )}
      </CardContent>
    </Card>
  )
}

/** The numbers a size form edits, as the text in each field. */
type SizeFields = {
  cpu_request_m: string
  cpu_limit_m: string
  mem_request_mb: string
  mem_limit_mb: string
  storage_gb: string
}

function SizeCard({ database, kind }: { database: Database; kind: DatabaseEngine }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  // What was typed, over what the database has: nothing is copied from the
  // record into state, so a resize that lands shows through at once.
  const [edits, setEdits] = useState<Partial<SizeFields>>({})
  const current: SizeFields = {
    cpu_request_m: String(database.cpu_request_m),
    cpu_limit_m: String(database.cpu_limit_m),
    mem_request_mb: String(database.mem_request_mb),
    mem_limit_mb: String(database.mem_limit_mb),
    storage_gb: String(database.storage_gb),
  }
  const value = (field: keyof SizeFields) => edits[field] ?? current[field]
  const changed = (Object.keys(current) as (keyof SizeFields)[]).filter(
    (field) => edits[field] !== undefined && edits[field] !== current[field],
  )
  const grows = Number(value("storage_gb")) > database.storage_gb

  const resize = useMutation({
    mutationFn: () =>
      api.patch<Database>(
        `/api/databases/${database.id}`,
        Object.fromEntries(changed.map((field) => [field, Number(value(field))])),
      ),
    onSuccess: () => {
      toast.success(t("databases.life.resized", { name: database.name }))
      setEdits({})
      refresh(database.id)
    },
  })

  const field = (name: keyof SizeFields, label: string, help?: string, min?: number) => (
    <Field>
      <FieldLabel htmlFor={`size-${name}`}>{label}</FieldLabel>
      <Input
        id={`size-${name}`}
        type="number"
        inputMode="numeric"
        min={min ?? 0}
        value={value(name)}
        onChange={(event) => setEdits({ ...edits, [name]: event.target.value })}
      />
      {help && <FieldDescription>{help}</FieldDescription>}
    </Field>
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("databases.life.size")}</CardTitle>
        <CardDescription>{t("databases.life.sizeHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          {field("cpu_request_m", t("databases.life.cpuRequest"), undefined, 10)}
          {field("cpu_limit_m", t("databases.life.cpuLimit"), t("databases.life.cpuLimitHelp"))}
          {field("mem_request_mb", t("databases.life.memRequest"), undefined, 32)}
          {field(
            "mem_limit_mb",
            t("databases.life.memLimit"),
            t("databases.life.memLimitHelp", { min: kind.min_memory_mb, engine: kind.title }),
            kind.min_memory_mb,
          )}
          {kind.storage &&
            field(
              "storage_gb",
              t("databases.life.storage"),
              t("databases.life.storageHelp"),
              database.storage_gb,
            )}
        </div>
        {resize.error != null && <ErrorDisplay error={resize.error} compact />}
        <div className="flex justify-end">
          <Button
            disabled={changed.length === 0 || resize.isPending}
            onClick={() => {
              void confirm({
                title: t("databases.life.resizeTitle", { name: database.name }),
                description: t("databases.life.resizeRestarts"),
                consequence: grows ? t("databases.life.resizeGrowForever") : undefined,
                confirmLabel: t("databases.life.resize"),
                destructive: grows,
              }).then((yes) => {
                if (yes) resize.mutate()
              })
            }}
          >
            {resize.isPending ? <Spinner /> : <RulerIcon className="size-4" />}
            {t("databases.life.resize")}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function PasswordCard({
  database,
  kind,
  links,
}: {
  database: Database
  kind: DatabaseEngine
  links: DatabaseLink[]
}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [password, setPassword] = useState("")

  const change = useMutation({
    mutationFn: () =>
      api.post<Operation>(`/api/databases/${database.id}/password`, password ? { password } : {}),
    onSuccess: () => {
      toast.success(t("databases.life.passwordStarted"))
      setPassword("")
      refresh(database.id)
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("databases.life.password")}</CardTitle>
        <CardDescription>{t("databases.life.passwordHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <Field>
          <FieldLabel htmlFor="new-password">
            {t("databases.life.newPassword")}{" "}
            <span className="text-muted-foreground">({t("common.optional")})</span>
          </FieldLabel>
          <Input
            id="new-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            className="font-mono"
          />
          <FieldDescription>{t("databases.life.newPasswordHelp")}</FieldDescription>
        </Field>
        {change.error != null && <ErrorDisplay error={change.error} compact />}
        <div className="flex justify-end">
          <Button
            variant="outline"
            disabled={change.isPending || database.status !== "running"}
            onClick={() => {
              void confirm({
                title: t("databases.life.passwordTitle", { name: database.name }),
                description: t("databases.life.passwordConfirm", { apps: links.length }),
                consequence: kind.password_restarts
                  ? t("databases.life.passwordRestarts", { engine: kind.title })
                  : undefined,
                confirmLabel: t("databases.life.changePassword"),
                destructive: true,
              }).then((yes) => {
                if (yes) change.mutate()
              })
            }}
          >
            {change.isPending ? <Spinner /> : <KeyRoundIcon className="size-4" />}
            {t("databases.life.changePassword")}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function ImportCard({ database, kind }: { database: Database; kind: DatabaseEngine }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [file, setFile] = useState<File | null>(null)
  const [format, setFormat] = useState("auto")
  const tooLarge = file != null && file.size > IMPORT_LIMIT_BYTES

  const upload = useMutation({
    mutationFn: (dump: File) =>
      api.upload<Operation>(
        `/api/databases/${database.id}/import${format === "auto" ? "" : `?format=${format}`}`,
        dump,
        "POST",
      ),
    onSuccess: () => {
      toast.success(t("databases.life.importStarted"))
      setFile(null)
      refresh(database.id)
    },
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("databases.life.import")}</CardTitle>
        <CardDescription>{t("databases.life.importHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="import-file">{t("databases.life.importFile")}</FieldLabel>
            <Input
              id="import-file"
              type="file"
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            />
            <FieldDescription>
              {t("databases.life.importFileHelp", {
                formats: kind.imports.map((name) => t(`databases.life.format.${name}`)).join(", "),
              })}
            </FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="import-format">{t("databases.life.importFormat")}</FieldLabel>
            <Select value={format} onValueChange={setFormat}>
              <SelectTrigger id="import-format">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">{t("databases.life.importFormatAuto")}</SelectItem>
                {kind.imports.map((name) => (
                  <SelectItem key={name} value={name}>
                    {t(`databases.life.format.${name}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </div>
        {tooLarge && (
          <p className="text-sm text-destructive">{t("databases.life.importTooLarge")}</p>
        )}
        {upload.error != null && <ErrorDisplay error={upload.error} compact />}
        <div className="flex justify-end">
          <Button
            disabled={!file || tooLarge || upload.isPending || database.status !== "running"}
            onClick={() => {
              if (!file) return
              void confirm({
                title: t("databases.life.importTitle", { name: database.name }),
                description: t("databases.life.importConfirm"),
                consequence: t("databases.life.importBackupFirst"),
                confirmLabel: t("databases.life.importButton"),
                destructive: true,
                typeToConfirm: database.name,
              }).then((yes) => {
                if (yes) upload.mutate(file)
              })
            }}
          >
            {upload.isPending ? <Spinner /> : <FileUpIcon className="size-4" />}
            {upload.isPending ? t("databases.life.importing") : t("databases.life.importButton")}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * What has been done to a database: its restores, imports and password
 * changes, each step by step. Polled while one is under way, since a member
 * limited to projects hears no live events.
 */
export function DatabaseHistory({ databaseId }: { databaseId: string }) {
  const { t } = useTranslation()
  const operations = useQuery({
    queryKey: ["database-operations", databaseId],
    queryFn: () => api.get<List<Operation>>(`/api/databases/${databaseId}/operations`),
    refetchInterval: (query) =>
      (query.state.data?.items ?? []).some(
        (op) => op.status === "pending" || op.status === "running",
      )
        ? 3_000
        : false,
  })

  if (operations.error) {
    return <ErrorDisplay error={operations.error} onRetry={() => void operations.refetch()} />
  }
  if (operations.isLoading) return <Skeleton className="h-40" />
  const items = operations.data?.items ?? []
  if (items.length === 0) {
    return (
      <EmptyState
        icon={HistoryIcon}
        title={t("databases.life.historyEmpty")}
        description={t("databases.life.historyHelp")}
      />
    )
  }

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("databases.life.historyHelp")}</p>
      {items.map((op) => (
        <Card key={op.id}>
          <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
            <div>
              <CardTitle className="text-sm">
                {t(`databases.life.kind.${op.kind.replace(/^database\./, "")}`, {
                  defaultValue: op.kind,
                })}
              </CardTitle>
              <CardDescription>
                {formatDateTime(op.created_at)} · {formatRelative(op.created_at)}
              </CardDescription>
            </div>
            <StatusBadge
              status={op.status}
              label={t(`databases.life.opStatus.${op.status}`, { defaultValue: op.status })}
            />
          </CardHeader>
          <CardContent>
            <OperationProgress operation={op} />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
