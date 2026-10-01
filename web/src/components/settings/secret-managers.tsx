import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  KeyRoundIcon,
  PlusIcon,
  ShieldCheckIcon,
  SlidersHorizontalIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { useTeamProjects } from "@/components/settings/member-access"
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
import { Field, FieldContent, FieldDescription, FieldLabel } from "@/components/ui/field"
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
import { Textarea } from "@/components/ui/textarea"
import { useSession } from "@/hooks/use-session"
import { api, ApiError, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { Project, SecretManager, SecretManagerKind } from "@/lib/types"

export const SECRET_MANAGER_KINDS: SecretManagerKind[] = ["vault", "infisical", "doppler", "aws"]

/** How often the apps that read a connection are refreshed on their own. */
const REFRESH_MINUTES = [0, 15, 60, 360, 1440]

type SettingField = { name: string; placeholder?: string; optional?: boolean }

/** What each kind's form asks for that is not secret. */
const SETTINGS: Record<SecretManagerKind, SettingField[]> = {
  vault: [
    { name: "address", placeholder: "https://vault.example.com:8200" },
    { name: "mount", placeholder: "secret", optional: true },
    { name: "namespace", optional: true },
  ],
  infisical: [
    { name: "site_url", placeholder: "https://app.infisical.com", optional: true },
    { name: "project_id" },
    { name: "environment", placeholder: "prod" },
  ],
  doppler: [],
  aws: [
    { name: "region", placeholder: "eu-central-1" },
    { name: "endpoint", optional: true },
  ],
}

/** What each kind signs in with. A Vault connection with AppRole takes two. */
function credentialsFor(kind: SecretManagerKind, auth: string): string[] {
  switch (kind) {
    case "vault":
      return auth === "approle" ? ["role_id", "secret_id"] : ["token"]
    case "infisical":
      return ["client_id", "client_secret"]
    case "doppler":
      return ["token"]
    case "aws":
      return ["access_key_id", "secret_access_key", "session_token"]
  }
}

const OPTIONAL_CREDENTIALS = new Set(["session_token"])

/**
 * What a path limit looks like for each kind: the way a reference to it
 * writes the path. Examples of paths, not words, so they are not translated.
 */
const PATH_EXAMPLES: Record<SecretManagerKind, string> = {
  vault: "shop\nshared/smtp",
  infisical: "/shop",
  doppler: "SHOP",
  aws: "prod/shop",
}

/** The prefixes typed into the box, one a line. */
function pathsFrom(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

/**
 * The projects a connection may be used by, by name. A project deleted since
 * stays on the connection's list, so the connection stays limited; it has no
 * name to show.
 */
function projectNames(manager: SecretManager, projects: Project[]): string[] {
  return manager.allowed_project_ids
    .map((id) => projects.find((project) => project.id === id)?.name)
    .filter((name): name is string => Boolean(name))
}

/** The one setting that tells two connections of a kind apart. */
function where(manager: SecretManager): string | null {
  const s = manager.settings
  switch (manager.kind) {
    case "vault":
      return `${s.address} · ${s.mount}`
    case "infisical":
      return `${s.project_id} · ${s.environment}`
    case "aws":
      return s.endpoint || s.region
    case "doppler":
      return null
  }
}

/**
 * The secret managers a team reads variables from: the Vault, Infisical,
 * Doppler or AWS Secrets Manager it already runs. An administrator connects
 * and removes them; everybody sees which there are, because a member picks
 * one when a variable is read from it.
 */
export function SecretManagersCard() {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirm = useConfirm()
  const isAdmin = team?.role === "admin" || team?.role === "owner"
  const path = `/api/teams/${team?.id}/secret-managers`
  const managers = useQuery({
    queryKey: ["secret-managers", team?.id],
    queryFn: () => api.get<List<SecretManager>>(path),
    enabled: Boolean(team),
  })
  const [adding, setAdding] = useState(false)
  const [limiting, setLimiting] = useState<SecretManager | null>(null)
  const projects = useTeamProjects()
  const teamProjects = projects.data?.items ?? []

  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: ["secret-managers", team?.id] })
  const test = useMutation({
    mutationFn: (manager: SecretManager) => api.post(`${path}/${manager.id}/test`),
    onSuccess: (_, manager) => toast.success(t("secretManagers.tested", { name: manager.name })),
  })
  const schedule = useMutation({
    mutationFn: ({ id, minutes }: { id: string; minutes: number }) =>
      api.patch<SecretManager>(`${path}/${id}`, { refresh_minutes: minutes }),
    onSuccess: refresh,
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`${path}/${id}`),
    onSuccess: refresh,
  })

  const items = managers.data?.items ?? []
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <KeyRoundIcon className="size-4" />
            {t("secretManagers.title")}
          </CardTitle>
          <CardDescription>{t("secretManagers.help")}</CardDescription>
        </div>
        {isAdmin && (
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            <PlusIcon className="size-4" />
            {t("secretManagers.add")}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {managers.isLoading ? (
          <Skeleton className="h-16" />
        ) : managers.error ? (
          <ErrorDisplay error={managers.error} onRetry={() => void managers.refetch()} />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {isAdmin ? t("secretManagers.empty") : t("secretManagers.emptyMember")}
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {items.map((manager) => (
              <li key={manager.id} className="space-y-2 px-3 py-3 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <code className="font-mono text-xs font-medium">{manager.name}</code>
                  <Badge variant="outline">{t(`secretManagers.kind.${manager.kind}`)}</Badge>
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">
                    {where(manager) ?? t("secretManagers.dopplerWhere")}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {t("secretManagers.usedBy", { count: manager.used_by })}
                  </span>
                  {isAdmin && (
                    <>
                      <Button variant="ghost" size="sm" onClick={() => setLimiting(manager)}>
                        <SlidersHorizontalIcon className="size-4" />
                        {t("secretManagers.limits")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={test.isPending}
                        onClick={() => test.mutate(manager)}
                      >
                        <ShieldCheckIcon className="size-4" />
                        {t("secretManagers.test")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("secretManagers.remove", { name: manager.name })}
                        disabled={remove.isPending}
                        onClick={() =>
                          void confirm({
                            title: t("secretManagers.remove", { name: manager.name }),
                            description: t("secretManagers.removeConfirm", { name: manager.name }),
                            confirmLabel: t("common.delete"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) remove.mutate(manager.id)
                          })
                        }
                      >
                        <Trash2Icon className="size-4" />
                      </Button>
                    </>
                  )}
                </div>
                <LimitsSummary manager={manager} projects={teamProjects} />
                <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  {isAdmin ? (
                    <Select
                      value={String(manager.refresh_minutes)}
                      onValueChange={(value) =>
                        schedule.mutate({ id: manager.id, minutes: Number(value) })
                      }
                    >
                      <SelectTrigger
                        size="sm"
                        className="h-7 w-auto"
                        aria-label={t("secretManagers.refresh")}
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {REFRESH_MINUTES.map((minutes) => (
                          <SelectItem key={minutes} value={String(minutes)}>
                            {t(`secretManagers.refreshOption.${minutes}`)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <span>{t(`secretManagers.refreshOption.${manager.refresh_minutes}`)}</span>
                  )}
                  {manager.last_refresh_at && (
                    <span>
                      {t("secretManagers.lastRefresh", {
                        when: formatRelative(manager.last_refresh_at),
                      })}
                    </span>
                  )}
                </div>
                {manager.last_error && (
                  <p className="text-xs text-destructive">
                    {t("secretManagers.lastError", { error: manager.last_error })}
                  </p>
                )}
              </li>
            ))}
          </ul>
        )}
        {test.error != null && <ErrorDisplay error={test.error} compact />}
        {schedule.error != null && <ErrorDisplay error={schedule.error} compact />}
        {remove.error != null && <ErrorDisplay error={remove.error} compact />}
      </CardContent>
      {adding && (
        <NewSecretManagerDialog path={path} onClose={() => setAdding(false)} onSaved={refresh} />
      )}
      {limiting && (
        <LimitsDialog
          key={limiting.id}
          path={`${path}/${limiting.id}`}
          manager={limiting}
          onClose={() => setLimiting(null)}
          onSaved={refresh}
        />
      )}
    </Card>
  )
}

/**
 * What a connection may be used for, under its name. One nobody limited to
 * some paths says so as a warning: anybody who can set a variable in any
 * project can read whatever its credentials can.
 */
function LimitsSummary({ manager, projects }: { manager: SecretManager; projects: Project[] }) {
  const { t } = useTranslation()
  const names = projectNames(manager, projects)
  return (
    <div className="space-y-1 text-xs">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
        {manager.allowed_paths.length === 0 ? (
          <Badge variant="outline" className="border-warning/30 text-warning">
            {t("secretManagers.anyPath")}
          </Badge>
        ) : (
          <span className="font-mono">
            {t("secretManagers.onlyPaths", { paths: manager.allowed_paths.join(", ") })}
          </span>
        )}
        <span>
          {manager.allowed_project_ids.length === 0
            ? t("secretManagers.everyProject")
            : names.length > 0
              ? t("secretManagers.onlyProjects", { projects: names.join(", ") })
              : t("secretManagers.projectsGone")}
        </span>
      </div>
      {manager.allowed_paths.length === 0 && (
        <p className="flex items-start gap-1.5 text-warning">
          <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
          {t("secretManagers.anyPathWarning")}
        </p>
      )}
    </div>
  )
}

/**
 * The two limits, as the new-connection form and the limits dialog both ask
 * for them. The paths are a box of lines; the projects a switch and a list,
 * because an empty list is every project and "none chosen yet" must not be
 * sent as that.
 */
function LimitsFields({
  idPrefix,
  kind,
  paths,
  onPaths,
  limited,
  onLimited,
  chosen,
  onChosen,
}: {
  idPrefix: string
  kind: SecretManagerKind
  paths: string
  onPaths: (paths: string) => void
  limited: boolean
  onLimited: (limited: boolean) => void
  chosen: string[]
  onChosen: (chosen: string[]) => void
}) {
  const { t } = useTranslation()
  const projects = useTeamProjects()
  const items = projects.data?.items ?? []
  const noPaths = pathsFrom(paths).length === 0
  return (
    <>
      <Field>
        <FieldLabel htmlFor={`${idPrefix}-paths`}>{t("secretManagers.allowedPaths")}</FieldLabel>
        <Textarea
          id={`${idPrefix}-paths`}
          value={paths}
          rows={3}
          className="font-mono"
          spellCheck={false}
          autoComplete="off"
          placeholder={PATH_EXAMPLES[kind]}
          onChange={(event) => onPaths(event.target.value)}
        />
        <FieldDescription>
          {t("secretManagers.allowedPathsHelp")} {t(`secretManagers.pathsLook.${kind}`)}
        </FieldDescription>
        {noPaths && (
          <p className="flex items-start gap-1.5 text-xs text-warning">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {t("secretManagers.allowedPathsSuggest")}
          </p>
        )}
      </Field>

      <div className="space-y-3 rounded-lg border p-3">
        <Field orientation="horizontal">
          <Switch id={`${idPrefix}-limited`} checked={limited} onCheckedChange={onLimited} />
          <FieldContent>
            <FieldLabel htmlFor={`${idPrefix}-limited`} className="font-normal">
              {t("secretManagers.onlySomeProjects")}
            </FieldLabel>
            <FieldDescription>{t("secretManagers.onlySomeProjectsHelp")}</FieldDescription>
          </FieldContent>
        </Field>
        {limited && (
          <div className="space-y-2 pl-1">
            {items.map((project) => {
              const box = `${idPrefix}-project-${project.id}`
              return (
                <Field key={project.id} orientation="horizontal">
                  <Checkbox
                    id={box}
                    checked={chosen.includes(project.id)}
                    onCheckedChange={(value) =>
                      onChosen(
                        value === true
                          ? [...chosen, project.id]
                          : chosen.filter((other) => other !== project.id),
                      )
                    }
                  />
                  <FieldLabel htmlFor={box} className="font-normal">
                    {project.name}
                  </FieldLabel>
                </Field>
              )
            })}
            {chosen.every((id) => !items.some((project) => project.id === id)) && (
              <p className="text-xs text-muted-foreground">{t("secretManagers.chooseProject")}</p>
            )}
          </div>
        )}
      </div>
    </>
  )
}

/**
 * The projects to send: the ones chosen that still exist. A switch that is on
 * with none of them is not sent at all — the form will not save it — because
 * an empty list is every project.
 */
function chosenProjects(limited: boolean, chosen: string[], projects: Project[]): string[] {
  if (!limited) return []
  return chosen.filter((id) => projects.some((project) => project.id === id))
}

/**
 * Changing what a connection may be used for. Narrowing it so that variables
 * which read it now would be outside it is refused by the panel, naming them;
 * the dialog then offers to save anyway, since cutting a project off is
 * sometimes the point.
 */
function LimitsDialog({
  path,
  manager,
  onClose,
  onSaved,
}: {
  path: string
  manager: SecretManager
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const projects = useTeamProjects()
  const [paths, setPaths] = useState(manager.allowed_paths.join("\n"))
  const [limited, setLimited] = useState(manager.allowed_project_ids.length > 0)
  const [chosen, setChosen] = useState<string[]>(manager.allowed_project_ids)

  const sent = chosenProjects(limited, chosen, projects.data?.items ?? [])
  const incomplete = limited && sent.length === 0

  const save = useMutation({
    mutationFn: (force: boolean) =>
      api.patch<SecretManager>(path, {
        allowed_paths: pathsFrom(paths),
        allowed_project_ids: sent,
        force: force || undefined,
      }),
    onSuccess: (saved) => {
      const cut = saved.stopped_resolving ?? []
      if (cut.length > 0) {
        toast.warning(
          t("secretManagers.limitsSavedCutOff", { name: saved.name, variables: cut.join(", ") }),
        )
      } else {
        toast.success(t("secretManagers.limitsSaved", { name: saved.name }))
      }
      onSaved()
      onClose()
    },
  })
  const breaks =
    save.error instanceof ApiError && save.error.problem.code === "secrets.limits_break_references"
  // "Save anyway" is an answer to the variables the panel named for these
  // limits; limits changed after that are asked about afresh.
  const edited =
    <T,>(set: (value: T) => void) =>
    (value: T) => {
      save.reset()
      set(value)
    }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("secretManagers.limitsTitle", { name: manager.name })}</DialogTitle>
          <DialogDescription>{t("secretManagers.limitsHelp")}</DialogDescription>
        </DialogHeader>
        <form
          id="secret-manager-limits"
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            if (!incomplete) save.mutate(false)
          }}
        >
          <LimitsFields
            idPrefix="secret-manager-limits"
            kind={manager.kind}
            paths={paths}
            onPaths={edited(setPaths)}
            limited={limited}
            onLimited={edited(setLimited)}
            chosen={chosen}
            onChosen={edited(setChosen)}
          />
          {save.error != null && <ErrorDisplay error={save.error} compact />}
        </form>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          {breaks ? (
            <Button
              variant="destructive"
              disabled={save.isPending || incomplete}
              onClick={() => save.mutate(true)}
            >
              {save.isPending && <Spinner />}
              {t("secretManagers.saveAnyway")}
            </Button>
          ) : (
            <Button
              type="submit"
              form="secret-manager-limits"
              disabled={save.isPending || incomplete}
            >
              {save.isPending && <Spinner />}
              {t("secretManagers.saveLimits")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Connecting one. The kind decides the fields; the panel signs in with them
 * before anything is saved, and reads nothing.
 */
function NewSecretManagerDialog({
  path,
  onClose,
  onSaved,
}: {
  path: string
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState("")
  const [kind, setKind] = useState<SecretManagerKind>("vault")
  const [auth, setAuth] = useState("token")
  const [settings, setSettings] = useState<Record<string, string>>({})
  const [credentials, setCredentials] = useState<Record<string, string>>({})
  const [minutes, setMinutes] = useState("0")
  const [paths, setPaths] = useState("")
  const [limited, setLimited] = useState(false)
  const [chosen, setChosen] = useState<string[]>([])
  const projects = useTeamProjects()

  // Only what the chosen kind takes is sent: a field filled in for another
  // kind before switching is not a setting of this one.
  const settingFields = SETTINGS[kind]
  const credentialFields = credentialsFor(kind, auth)
  const sentProjects = chosenProjects(limited, chosen, projects.data?.items ?? [])
  const missing =
    !/^[a-z0-9][a-z0-9_-]*$/.test(name) ||
    settingFields.some((field) => !field.optional && !settings[field.name]?.trim()) ||
    credentialFields.some((field) => !OPTIONAL_CREDENTIALS.has(field) && !credentials[field]) ||
    (limited && sentProjects.length === 0)

  const save = useMutation({
    mutationFn: () => {
      const sentSettings: Record<string, string> = {}
      for (const field of settingFields) {
        if (settings[field.name]?.trim()) sentSettings[field.name] = settings[field.name].trim()
      }
      if (kind === "vault") sentSettings.auth = auth
      const sentCredentials: Record<string, string> = {}
      for (const field of credentialFields) {
        if (credentials[field]) sentCredentials[field] = credentials[field]
      }
      return api.post<SecretManager>(path, {
        name,
        kind,
        settings: sentSettings,
        credentials: sentCredentials,
        refresh_minutes: Number(minutes),
        allowed_paths: pathsFrom(paths),
        allowed_project_ids: sentProjects,
      })
    },
    onSuccess: (saved) => {
      toast.success(t("secretManagers.connected", { name: saved.name }))
      onSaved()
      onClose()
    },
  })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("secretManagers.add")}</DialogTitle>
          <DialogDescription>{t("secretManagers.formHelp")}</DialogDescription>
        </DialogHeader>
        <form
          id="new-secret-manager"
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            if (!missing) save.mutate()
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="secret-manager-kind">{t("secretManagers.kindLabel")}</FieldLabel>
              <Select value={kind} onValueChange={(value) => setKind(value as SecretManagerKind)}>
                <SelectTrigger id="secret-manager-kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SECRET_MANAGER_KINDS.map((k) => (
                    <SelectItem key={k} value={k}>
                      {t(`secretManagers.kind.${k}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="secret-manager-name">{t("secretManagers.name")}</FieldLabel>
              <Input
                id="secret-manager-name"
                value={name}
                placeholder="company-vault"
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                onChange={(event) => setName(event.target.value.toLowerCase())}
              />
              <FieldDescription>{t("secretManagers.nameHelp")}</FieldDescription>
            </Field>
          </div>

          {kind === "vault" && (
            <Field>
              <FieldLabel htmlFor="secret-manager-auth">{t("secretManagers.auth")}</FieldLabel>
              <Select value={auth} onValueChange={setAuth}>
                <SelectTrigger id="secret-manager-auth">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="token">{t("secretManagers.authToken")}</SelectItem>
                  <SelectItem value="approle">{t("secretManagers.authAppRole")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}
          {kind === "doppler" && (
            <p className="text-sm text-muted-foreground">{t("secretManagers.dopplerHelp")}</p>
          )}

          {settingFields.map((field) => (
            <Field key={`${kind}-${field.name}`}>
              <FieldLabel htmlFor={`secret-manager-${field.name}`}>
                {t(`secretManagers.field.${field.name}`)}
                {field.optional && (
                  <span className="font-normal text-muted-foreground">
                    {" "}
                    {t("secretManagers.optional")}
                  </span>
                )}
              </FieldLabel>
              <Input
                id={`secret-manager-${field.name}`}
                value={settings[field.name] ?? ""}
                placeholder={field.placeholder}
                spellCheck={false}
                autoComplete="off"
                onChange={(event) => setSettings({ ...settings, [field.name]: event.target.value })}
              />
            </Field>
          ))}

          {credentialFields.map((field) => (
            <Field key={`${kind}-${field}`}>
              <FieldLabel htmlFor={`secret-manager-${field}`}>
                {t(`secretManagers.field.${field}`)}
                {OPTIONAL_CREDENTIALS.has(field) && (
                  <span className="font-normal text-muted-foreground">
                    {" "}
                    {t("secretManagers.optional")}
                  </span>
                )}
              </FieldLabel>
              <Input
                id={`secret-manager-${field}`}
                type="password"
                value={credentials[field] ?? ""}
                autoComplete="new-password"
                spellCheck={false}
                onChange={(event) =>
                  setCredentials({ ...credentials, [field]: event.target.value })
                }
              />
            </Field>
          ))}
          <FieldDescription>{t("secretManagers.credentialsHelp")}</FieldDescription>

          <LimitsFields
            idPrefix="new-secret-manager"
            kind={kind}
            paths={paths}
            onPaths={setPaths}
            limited={limited}
            onLimited={setLimited}
            chosen={chosen}
            onChosen={setChosen}
          />

          <Field>
            <FieldLabel htmlFor="secret-manager-refresh">{t("secretManagers.refresh")}</FieldLabel>
            <Select value={minutes} onValueChange={setMinutes}>
              <SelectTrigger id="secret-manager-refresh">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {REFRESH_MINUTES.map((value) => (
                  <SelectItem key={value} value={String(value)}>
                    {t(`secretManagers.refreshOption.${value}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldDescription>{t("secretManagers.refreshHelp")}</FieldDescription>
          </Field>

          {save.error != null && <ErrorDisplay error={save.error} compact />}
        </form>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="new-secret-manager" disabled={save.isPending || missing}>
            {save.isPending && <Spinner />}
            {t("secretManagers.connect")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
