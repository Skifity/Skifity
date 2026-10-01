import { useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArrowUpCircleIcon,
  BookOpenIcon,
  CheckCircle2Icon,
  CopyIcon,
  DatabaseBackupIcon,
  DownloadIcon,
  KeyRoundIcon,
  LinkIcon,
  PackagePlusIcon,
  PlusIcon,
  ShieldAlertIcon,
  Trash2Icon,
  UserCogIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { K3sUpgradeCard } from "@/components/settings/k3s-upgrade"
import { BackupVerification } from "@/components/backup-verification"
import { CertificatesCard } from "@/components/settings/certificates"
import { DNSProvidersCard } from "@/components/settings/dns-providers"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { GitSources } from "@/components/settings/git-sources"
import { RegistriesCard } from "@/components/settings/registries"
import {
  AccessSummary,
  ChangeAccessDialog,
  ProjectLimitField,
  RoleSelect,
  canLimit,
  limitFor,
} from "@/components/settings/member-access"
import { NotificationChannels } from "@/components/settings/notification-channels"
import { SecretManagersCard } from "@/components/settings/secret-managers"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useSession } from "@/hooks/use-session"
import { ApiError, api, type List } from "@/lib/api"
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type {
  AuditEvent,
  Backup,
  Component,
  Invitation,
  Member,
  Role,
  Setting,
  StoreCatalogue,
} from "@/lib/types"

/**
 * The order groups are shown in, and what each is called.
 *
 * It is an ordering and not the list: the list comes from the settings the
 * server sends. A hand-kept copy of the server's groups is a group that renders
 * as nothing at all the day somebody adds one — which is what happened to
 * single sign-on and to plugins, both of which existed, were documented, and
 * had nowhere to be set.
 */
const GROUPS: { key: string; label: string }[] = [
  { key: "general", label: "settings.general" },
  { key: "cluster", label: "settings.cluster" },
  { key: "scanning", label: "settings.scanning" },
  { key: "domains", label: "settings.domains" },
  { key: "git", label: "settings.git" },
  { key: "storage", label: "settings.storage" },
  { key: "email", label: "settings.email" },
  { key: "notifications", label: "settings.notifications" },
  { key: "registry", label: "settings.registry" },
  { key: "signin", label: "settings.signin" },
  { key: "plugins", label: "settings.plugins" },
]

/**
 * Every group present in the settings, in the order above, with anything the
 * server knows about and this build does not put at the end under its own key.
 */
function groupsOf(items: Setting[]): { key: string; label: string }[] {
  const present = new Set(items.map((setting) => setting.group))
  const ordered = GROUPS.filter((group) => present.has(group.key))
  const known = new Set(GROUPS.map((group) => group.key))
  for (const key of present) {
    // No translation for a group this build has never heard of, so the key is
    // shown as it came. Untranslated is a great deal better than invisible.
    if (!known.has(key)) ordered.push({ key, label: key })
  }
  return ordered
}

export function SettingsPage() {
  const { t } = useTranslation()
  // The tab is in the address, so a notification can link to the one it is
  // about: a certificate running out links to the certificates.
  const [params, setParams] = useSearchParams()
  const tab = params.get("tab") ?? "panel"

  return (
    <Page>
      <PageHeader title={t("settings.title")} description={t("settings.subtitle")} />

      <Tabs value={tab} onValueChange={(value) => setParams({ tab: value }, { replace: true })}>
        <TabsList className="flex-wrap">
          <TabsTrigger value="panel">{t("settings.general")}</TabsTrigger>
          <TabsTrigger value="git">{t("settings.git")}</TabsTrigger>
          <TabsTrigger value="certificates">{t("settings.certificates")}</TabsTrigger>
          <TabsTrigger value="dns">{t("settings.dnsProviders")}</TabsTrigger>
          <TabsTrigger value="notifications">{t("settings.notifications")}</TabsTrigger>
          <TabsTrigger value="secrets">{t("settings.secretManagers")}</TabsTrigger>
          <TabsTrigger value="components">{t("settings.components")}</TabsTrigger>
          <TabsTrigger value="plugins">{t("settings.plugins")}</TabsTrigger>
          <TabsTrigger value="members">{t("settings.members")}</TabsTrigger>
          <TabsTrigger value="security">{t("settings.security")}</TabsTrigger>
          <TabsTrigger value="audit">{t("settings.auditLog")}</TabsTrigger>
        </TabsList>

        <TabsContent value="panel" className="space-y-6 pt-4">
          {/* Git and plugins have tabs of their own, below. */}
          <SettingGroups except={["git", "plugins"]} />
          <PanelBackupsCard />
          <ExportCard />
          <VersionCard />
        </TabsContent>
        <TabsContent value="git" className="space-y-6 pt-4">
          <GitSources />
          <RegistriesCard />
          <SettingGroups only={["git"]} />
        </TabsContent>
        <TabsContent value="certificates" className="pt-4">
          <CertificatesCard />
        </TabsContent>
        <TabsContent value="dns" className="pt-4">
          <DNSProvidersCard />
        </TabsContent>
        <TabsContent value="notifications" className="pt-4">
          <NotificationChannels />
        </TabsContent>
        <TabsContent value="secrets" className="pt-4">
          <SecretManagersCard />
        </TabsContent>
        <TabsContent value="components" className="pt-4">
          <ComponentsPanel />
        </TabsContent>
        <TabsContent value="plugins" className="space-y-6 pt-4">
          <SettingGroups only={["plugins"]} />
          <StoreCheckCard />
        </TabsContent>
        <TabsContent value="members" className="pt-4">
          <MembersPanel />
        </TabsContent>
        <TabsContent value="security" className="pt-4">
          <SecurityPanel />
        </TabsContent>
        <TabsContent value="audit" className="pt-4">
          <AuditPanel />
        </TabsContent>
      </Tabs>
    </Page>
  )
}

function SettingGroups({ only, except }: { only?: string[]; except?: string[] }) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<Record<string, string>>({})

  const settings = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<List<Setting>>("/api/settings"),
  })

  const save = useMutation({
    mutationFn: () => api.put("/api/settings", { values: draft }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["settings"] })
      setDraft({})
      toast.success(t("settings.saved"))
    },
  })

  if (settings.isLoading) return <Skeleton className="h-96" />
  if (settings.error) {
    return <ErrorDisplay error={settings.error} onRetry={() => void settings.refetch()} />
  }

  const items = settings.data?.items ?? []
  const dirty = Object.keys(draft).length > 0
  const shown = groupsOf(items).filter(
    (group) => (!only || only.includes(group.key)) && (!except || !except.includes(group.key)),
  )

  /** The value a control should show: the edit in progress, else what is stored. */
  const valueOf = (setting: Setting) =>
    draft[setting.key] ?? (setting.secret ? "" : (setting.value ?? ""))

  const set = (key: string, value: string) => setDraft({ ...draft, [key]: value })

  return (
    <div className="space-y-6">
      {shown.map((group) => {
        const groupItems = items.filter((setting) => setting.group === group.key)
        if (groupItems.length === 0) return null
        return (
          <Card key={group.key}>
            <CardHeader>
              <CardTitle className="text-base">
                {group.label.includes(".") ? t(group.label) : group.label}
              </CardTitle>
            </CardHeader>
            <CardContent>
              <FieldGroup>
                {groupItems.map((setting) => (
                  <SettingField
                    key={setting.key}
                    setting={setting}
                    value={valueOf(setting)}
                    edited={setting.key in draft}
                    onChange={(value) => set(setting.key, value)}
                  />
                ))}
              </FieldGroup>
            </CardContent>
          </Card>
        )
      })}

      {save.error != null && <ErrorDisplay error={save.error} />}

      {/* The bar only appears once something has changed, so it is never a
          button that does nothing. */}
      {dirty && (
        <div className="sticky bottom-4 z-10 flex items-center justify-between gap-3 rounded-lg border bg-card/95 px-4 py-3 shadow-lg backdrop-blur">
          <span className="text-sm text-muted-foreground">
            {t("settings.unsaved", { count: Object.keys(draft).length })}
          </span>
          <div className="flex gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setDraft({})}
              disabled={save.isPending}
            >
              {t("common.cancel")}
            </Button>
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              {save.isPending && <Spinner />}
              {save.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * One setting, drawn as the kind of thing it actually is.
 *
 * Every setting used to be a text box, so "Use STARTTLS" was a field where you
 * had to guess whether it wanted "true", "yes" or "on" — and the server only
 * accepted some of those.
 */
function SettingField({
  setting,
  value,
  edited,
  onChange,
}: {
  setting: Setting
  value: string
  edited: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const id = `setting-${setting.key}`
  const placeholder =
    setting.secret && setting.configured ? t("settings.secretStored") : setting.placeholder

  // The server's label and help are English, because the server has one
  // language and the API, the CLI and an assistant all read them. The panel has
  // five, and this page was the largest English surface left in it: every label
  // and every paragraph, in Indonesian, Hindi, Russian and Chinese alike. The
  // translation is looked up by the setting's own key, with the server's text
  // as the fallback for a setting added before anybody has translated it.
  const field = `settings.field.${setting.key.replace(".", "_")}`
  const label = t(`${field}.label`, { defaultValue: setting.label })
  const help = setting.help ? t(`${field}.help`, { defaultValue: setting.help }) : ""

  if (setting.kind === "bool") {
    return (
      <Field orientation="horizontal">
        <FieldContent>
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
          <FieldDescription>{help}</FieldDescription>
        </FieldContent>
        <Switch
          id={id}
          checked={value === "true"}
          onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
        />
      </Field>
    )
  }

  return (
    <Field data-edited={edited || undefined}>
      <FieldLabel htmlFor={id}>
        {label}
        {setting.secret && setting.configured && !edited && (
          <Badge variant="outline" className="ml-2 text-[10px]">
            {t("settings.configured")}
          </Badge>
        )}
        {edited && (
          <Badge variant="secondary" className="ml-2 text-[10px]">
            {t("settings.edited")}
          </Badge>
        )}
      </FieldLabel>

      {setting.kind === "choice" ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id}>
            <SelectValue placeholder={setting.placeholder ?? t("common.none")} />
          </SelectTrigger>
          <SelectContent>
            {(setting.options ?? []).map((option) => (
              <SelectItem key={option} value={option}>
                {option}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : setting.multiline ? (
        <Textarea
          id={id}
          value={value}
          rows={6}
          placeholder={placeholder}
          onChange={(event) => onChange(event.target.value)}
          className="font-mono text-xs"
        />
      ) : (
        <Input
          id={id}
          value={value}
          placeholder={placeholder}
          onChange={(event) => onChange(event.target.value)}
          type={inputType(setting)}
          inputMode={setting.kind === "number" ? "numeric" : undefined}
          autoComplete={setting.secret ? "new-password" : "off"}
          spellCheck={false}
        />
      )}

      <FieldDescription>{help}</FieldDescription>
    </Field>
  )
}

function inputType(setting: Setting): string {
  if (setting.secret) return "password"
  switch (setting.kind) {
    case "number":
      return "number"
    case "email":
      return "email"
    case "url":
      return "url"
    default:
      return "text"
  }
}

function VersionCard() {
  const { t } = useTranslation()
  const { meta } = useSession()

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("settings.upgrade")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        <div className="flex items-center justify-between">
          <span className="text-muted-foreground">{t("settings.currentVersion")}</span>
          <span className="font-mono">{meta?.version ?? "—"}</span>
        </div>
        <p className="text-xs text-muted-foreground">
          {t("settings.noUpdateCheck", { product: meta?.product ?? "Skifity" })}
        </p>
      </CardContent>
    </Card>
  )
}

/**
 * Whether everybody in the team has to sign in with more than a password.
 *
 * Only an admin or an owner can change it, and the server refuses to turn it
 * on from a sign-in that would not pass it — which would lock out the person
 * doing it — so the error that comes back is the whole explanation.
 */
function StrongAuthCard() {
  const { t } = useTranslation()
  const { team, refresh } = useSession()

  const update = useMutation({
    mutationFn: (required: boolean) =>
      api.patch(`/api/teams/${team!.id}`, { require_strong_auth: required }),
    onSuccess: () => void refresh(),
  })

  if (!team || (team.role !== "admin" && team.role !== "owner")) return null

  return (
    <Card>
      <CardContent className="space-y-3 pt-6">
        <Field orientation="horizontal">
          <Switch
            id="require-strong-auth"
            checked={team.require_strong_auth ?? false}
            disabled={update.isPending}
            onCheckedChange={(checked) => update.mutate(checked)}
          />
          <FieldContent>
            <FieldLabel htmlFor="require-strong-auth">{t("settings.requireStrongAuth")}</FieldLabel>
            <FieldDescription>{t("settings.requireStrongAuthHelp")}</FieldDescription>
          </FieldContent>
        </Field>
        {update.error != null && <ErrorDisplay error={update.error} compact />}
      </CardContent>
    </Card>
  )
}

/**
 * The panel's own database, copied to the bucket.
 *
 * Everything else here is about what the panel looks after; this is the panel
 * looking after itself. The schedule and how many to keep are ordinary
 * settings in the Backup storage card above. This lists what is in the bucket
 * and takes a copy on demand, which is what you want right before an upgrade.
 * Putting one back happens with the panel stopped, so it is a command and not
 * a button.
 */
function PanelBackupsCard() {
  const { t } = useTranslation()

  const backups = useQuery({
    queryKey: ["panel-backups"],
    queryFn: () => api.get<List<Backup>>("/api/panel/backups"),
  })

  const backupNow = useMutation({
    mutationFn: () => api.post<Backup>("/api/panel/backups"),
    onSuccess: () => toast.success(t("settings.panelBackupTaken")),
    // A failure is recorded too, so the list is refreshed either way.
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ["panel-backups"] }),
  })

  const items = backups.data?.items ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("settings.panelBackups")}</CardTitle>
        <CardDescription>{t("settings.panelBackupsHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            disabled={backupNow.isPending}
            onClick={() => backupNow.mutate()}
          >
            {backupNow.isPending ? <Spinner /> : <DatabaseBackupIcon />}
            {t("databases.backupNow")}
          </Button>
        </div>

        {backupNow.error != null && <ErrorDisplay error={backupNow.error} />}

        {backups.isLoading ? (
          <Skeleton className="h-24" />
        ) : backups.error ? (
          <ErrorDisplay error={backups.error} onRetry={() => void backups.refetch()} />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("settings.panelBackupsNone")}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.created")}</TableHead>
                <TableHead>{t("common.status")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("common.size")}</TableHead>
                <TableHead className="hidden md:table-cell">
                  {t("settings.panelBackupObject")}
                </TableHead>
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
                      <p className="mt-1 max-w-xs text-xs break-words text-destructive">
                        {backup.error_message}
                      </p>
                    )}
                    <BackupVerification
                      backup={backup}
                      verifyPath={`/api/panel/backups/${backup.id}/verify`}
                      listKey={["panel-backups"]}
                    />
                  </TableCell>
                  <TableCell className="hidden tabular-nums sm:table-cell">
                    {backup.size_bytes ? formatBytes(backup.size_bytes) : "—"}
                  </TableCell>
                  <TableCell className="hidden font-mono text-xs break-all md:table-cell">
                    {backup.location || "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        <p className="text-xs text-muted-foreground">{t("settings.panelBackupsRestore")}</p>
        <code className="block rounded bg-muted px-2 py-1 font-mono text-xs break-all">
          skifity admin restore-db ./panel.db.gz
        </code>
      </CardContent>
    </Card>
  )
}

/**
 * Taking everything out again.
 *
 * A self-hosted product that is hard to leave is a trap with good onboarding.
 * The download is the panel's whole answer for this team; the CLI writes the
 * same thing as a directory with the Kubernetes objects split out per app,
 * which is the form somebody actually migrates with.
 */
function ExportCard() {
  const { t } = useTranslation()
  const { team } = useSession()
  if (!team) return null

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("settings.export")}</CardTitle>
        <CardDescription>{t("settings.exportHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <p className="text-xs text-muted-foreground">{t("settings.exportSecrets")}</p>
        <div className="flex flex-wrap items-center gap-2">
          {/*
            A plain link rather than a fetch: the browser saves the file with
            the session cookie it already has, and nothing has to be held in
            memory on the way.
          */}
          <Button asChild variant="outline">
            <a href={`/api/teams/${team.id}/export`} download={`${team.slug}-export.json`}>
              <DownloadIcon />
              {t("settings.exportDownload")}
            </a>
          </Button>
          <code className="rounded bg-muted px-2 py-1 font-mono text-xs">skifity export</code>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Reading the store, on purpose, to find out whether these two settings work.
 *
 * An address and a key are a pair you otherwise find out is wrong on the day
 * you wanted a plugin. This reads the catalogue the same way the Plugins page
 * does and says which of the three answers came back: signed by the key set
 * here, readable but vouched for by nobody, or a failure that names itself.
 */
function StoreCheckCard() {
  const { t } = useTranslation()

  // Not a useQuery on mount: an address an operator is halfway through typing
  // should not be fetched, and a store that is down should not make the
  // settings page look broken. It runs when somebody asks for it.
  const check = useMutation({
    mutationFn: () => api.get<StoreCatalogue>("/api/plugins/store"),
  })

  const catalogue = check.data

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("settings.pluginStoreCheck")}</CardTitle>
        <CardDescription>{t("settings.pluginStoreCheckHelp")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <Button variant="outline" disabled={check.isPending} onClick={() => check.mutate()}>
          {check.isPending && <Spinner />}
          {t("settings.pluginStoreCheckAction")}
        </Button>

        {check.error != null && <ErrorDisplay error={check.error} />}

        {/* Unsigned is a warning and not a fault: the catalogue was read, and
            nobody vouched for it. Red here would be red for something that
            works, which teaches people to ignore red. */}
        {catalogue != null && (
          <Alert variant={catalogue.verified ? "success" : "warning"}>
            {catalogue.verified ? <CheckCircle2Icon /> : <ShieldAlertIcon />}
            <AlertTitle>
              {catalogue.verified ? t("settings.pluginStoreVerified") : t("plugins.storeUnsigned")}
            </AlertTitle>
            <AlertDescription>
              <span>
                {t("settings.pluginStoreCount", { count: catalogue.index.plugins.length })}
              </span>
              <span className="font-mono text-xs break-all">{catalogue.url}</span>
              {!catalogue.verified && <span>{t("plugins.storeUnsignedHelp")}</span>}
            </AlertDescription>
          </Alert>
        )}

        <p className="text-xs text-muted-foreground">
          <Link className="underline underline-offset-4" to="/plugins">
            {t("settings.pluginStoreGoToPlugins")}
          </Link>
        </p>
      </CardContent>
    </Card>
  )
}

function ComponentsPanel() {
  const { t } = useTranslation()

  const components = useQuery({
    queryKey: ["components"],
    queryFn: () => api.get<List<Component>>("/api/components"),
  })

  const install = useMutation({
    mutationFn: (name: string) => api.post(`/api/components/${name}/install`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["components"] }),
  })
  const upgrade = useMutation({
    mutationFn: (name: string) => api.post(`/api/components/${name}/upgrade`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["components"] }),
  })
  const confirmUpgrade = useConfirm()

  if (components.isLoading) return <Skeleton className="h-64" />
  if (components.error) {
    return <ErrorDisplay error={components.error} onRetry={() => void components.refetch()} />
  }

  return (
    <div className="space-y-4">
      <K3sUpgradeCard />
      <p className="text-sm text-muted-foreground">{t("settings.componentsHelp")}</p>
      <div className="space-y-3">
        {components.data?.items.map((component) => (
          <Card key={component.name}>
            <CardContent className="flex flex-wrap items-center gap-4 py-4">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">
                    {t(`components.${component.name}.title`, { defaultValue: component.title })}
                  </span>
                  {component.beta && (
                    <Badge variant="outline" className="text-[10px]">
                      {t("common.beta")}
                    </Badge>
                  )}
                  {component.approximate_memory_mb > 0 && (
                    <span className="text-xs text-muted-foreground">
                      {t("settings.componentMemory", { mb: component.approximate_memory_mb })}
                    </span>
                  )}
                  {component.status === "installed" && component.version && (
                    <Badge variant="outline" className="font-mono text-[10px]">
                      {component.version}
                    </Badge>
                  )}
                </div>
                <p className="mt-0.5 text-sm text-muted-foreground">
                  {t(`components.${component.name}.description`, {
                    defaultValue: component.description,
                  })}
                </p>
                {component.detail && (
                  <p className="mt-0.5 text-xs text-muted-foreground">{component.detail}</p>
                )}
              </div>

              {component.external ? (
                // An Install button that always answers "use Helm instead" is
                // worse than no button: it looks like a failure.
                <Button variant="outline" size="sm" asChild>
                  <Link to={component.docs ?? "/docs"}>
                    <BookOpenIcon className="size-4" />
                    {t("settings.componentExternal")}
                  </Link>
                </Button>
              ) : component.upgrade_available ? (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={upgrade.isPending}
                  onClick={() =>
                    void confirmUpgrade({
                      title: t("settings.componentUpgradeTitle", {
                        name: t(`components.${component.name}.title`, {
                          defaultValue: component.title,
                        }),
                      }),
                      description: t("settings.componentUpgradeConfirm", {
                        from: component.version || t("settings.componentVersionUnknown"),
                        to: component.wanted_version,
                      }),
                      confirmLabel: t("settings.componentUpgrade", {
                        version: component.wanted_version,
                      }),
                    }).then((yes) => {
                      if (yes) upgrade.mutate(component.name)
                    })
                  }
                >
                  {upgrade.isPending && upgrade.variables === component.name ? (
                    <Spinner />
                  ) : (
                    <ArrowUpCircleIcon className="size-4" />
                  )}
                  {t("settings.componentUpgrade", { version: component.wanted_version })}
                </Button>
              ) : component.status === "installed" ? (
                <span className="flex items-center gap-1.5 text-sm text-success">
                  <CheckCircle2Icon className="size-4" />
                  {t("settings.componentInstalled")}
                </span>
              ) : (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={install.isPending}
                  onClick={() => install.mutate(component.name)}
                >
                  <PackagePlusIcon className="size-4" />
                  {t("settings.componentInstall")}
                </Button>
              )}
            </CardContent>
          </Card>
        ))}
      </div>
      {install.error != null && <ErrorDisplay error={install.error} />}
      {upgrade.error != null && <ErrorDisplay error={upgrade.error} />}
    </div>
  )
}

function MembersPanel() {
  const { t } = useTranslation()
  const confirmMember = useConfirm()
  const { team } = useSession()
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<Role>("member")
  const [limited, setLimited] = useState(false)
  const [chosen, setChosen] = useState<string[]>([])
  const [changing, setChanging] = useState<Member | null>(null)

  const members = useQuery({
    queryKey: ["members", team?.id],
    queryFn: () => api.get<List<Member>>(`/api/teams/${team!.id}/members`),
    enabled: Boolean(team),
  })

  const invitations = useQuery({
    queryKey: ["invitations", team?.id],
    queryFn: () => api.get<List<Invitation>>(`/api/teams/${team!.id}/invitations`),
    enabled: Boolean(team),
  })

  // The link is shown once, the same way the recovery key and the setup token
  // are: the token behind it is stored hashed, so the panel cannot show it
  // again even if somebody asks.
  const [link, setLink] = useState<string | null>(null)

  const invite = useMutation({
    // Somebody who already has an account here needs a membership, not a
    // link. The server says so rather than guessing, and the form then does
    // what its help text promises: adds them directly.
    mutationFn: async (): Promise<{ url?: string }> => {
      const body = { email: email.trim(), role, projects: limitFor(role, limited, chosen) }
      try {
        return await api.post<{ url: string }>(`/api/teams/${team!.id}/invitations`, body)
      } catch (error) {
        if (error instanceof ApiError && error.problem.code === "team.account_exists") {
          await api.post(`/api/teams/${team!.id}/members`, body)
          return {}
        }
        throw error
      }
    },
    onSuccess: (result) => {
      if (result.url) {
        setLink(result.url)
      } else {
        toast.success(t("settings.memberAdded"))
      }
      setEmail("")
      void queryClient.invalidateQueries({ queryKey: ["invitations", team?.id] })
      void queryClient.invalidateQueries({ queryKey: ["members", team?.id] })
    },
  })

  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/api/teams/${team!.id}/invitations/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["invitations", team?.id] }),
  })

  const remove = useMutation({
    mutationFn: (userID: string) => api.delete(`/api/teams/${team!.id}/members/${userID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["members", team?.id] }),
  })

  if (members.isLoading) return <Skeleton className="h-64" />
  if (members.error)
    return <ErrorDisplay error={members.error} onRetry={() => void members.refetch()} />

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("settings.roleHelp")}</p>

      <StrongAuthCard />

      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.name")}</TableHead>
                <TableHead>{t("settings.memberRole")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("settings.access")}</TableHead>
                <TableHead className="w-20" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.data?.items.map((member) => (
                <TableRow key={member.user.id}>
                  <TableCell>
                    <div className="font-medium">{member.user.name || member.user.email}</div>
                    <div className="text-xs text-muted-foreground">{member.user.email}</div>
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">
                      {t(
                        `settings.role${member.role.charAt(0).toUpperCase()}${member.role.slice(1)}`,
                        {
                          defaultValue: member.role,
                        },
                      )}
                    </Badge>
                  </TableCell>
                  <TableCell className="hidden text-sm text-muted-foreground sm:table-cell">
                    <AccessSummary scoped={member.scoped} projects={member.projects} />
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("settings.changeAccess")}
                      onClick={() => setChanging(member)}
                    >
                      <UserCogIcon className="size-4 text-muted-foreground" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("settings.removeMember")}
                      disabled={remove.isPending}
                      // Taking somebody's access away, on one click of a bin
                      // the same size as the one beside a variable.
                      onClick={() =>
                        void confirmMember({
                          title: t("settings.removeMember"),
                          description: t("settings.removeMemberConfirm", {
                            name: member.user.name || member.user.email,
                          }),
                          confirmLabel: t("common.remove"),
                          destructive: true,
                        }).then((yes) => {
                          if (yes) remove.mutate(member.user.id)
                        })
                      }
                    >
                      <Trash2Icon className="size-4 text-muted-foreground" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {changing != null && (
        <ChangeAccessDialog
          // Keyed by the person, so opening it for somebody else starts from
          // their access rather than the last one's.
          key={changing.user.id}
          member={changing}
          open
          onOpenChange={(open) => !open && setChanging(null)}
        />
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("settings.inviteMember")}</CardTitle>
          <CardDescription>{t("settings.inviteMemberHelp")}</CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              invite.mutate()
            }}
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="member-email">{t("settings.memberEmail")}</Label>
                <Input
                  id="member-email"
                  type="email"
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="member-role">{t("settings.memberRole")}</Label>
                <RoleSelect id="member-role" value={role} onChange={setRole} />
              </div>
            </div>
            <ProjectLimitField
              id="invite"
              role={role}
              limited={limited}
              onLimitedChange={setLimited}
              chosen={chosen}
              onChosenChange={setChosen}
            />
            {invite.error != null && <ErrorDisplay error={invite.error} compact />}
            <div className="flex justify-end">
              <Button
                type="submit"
                disabled={
                  !email.trim() ||
                  invite.isPending ||
                  (canLimit(role) && limited && chosen.length === 0)
                }
              >
                <PlusIcon className="size-4" />
                {invite.isPending && <Spinner />}
                {invite.isPending ? t("common.saving") : t("settings.inviteMember")}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      {link != null && (
        <Alert>
          <LinkIcon />
          <AlertTitle>{t("settings.inviteLinkTitle")}</AlertTitle>
          <AlertDescription className="space-y-2">
            <p>{t("settings.inviteLinkHelp")}</p>
            <code className="block w-full rounded-md border bg-muted p-2 font-mono text-xs break-all">
              {link}
            </code>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                navigator.clipboard
                  .writeText(link)
                  .then(() => toast.success(t("common.copied")))
                  .catch(() => toast.error(t("errors.somethingWentWrong")))
              }}
            >
              <CopyIcon className="size-4" />
              {t("common.copy")}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {(invitations.data?.items.length ?? 0) > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("settings.pendingInvitations")}</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableBody>
                {invitations.data?.items.map((invitation) => (
                  <TableRow key={invitation.id}>
                    <TableCell>
                      <div className="font-medium">{invitation.email}</div>
                      <div className="text-xs text-muted-foreground">
                        {t("settings.invitationExpires", {
                          when: formatDateTime(invitation.expires_at),
                        })}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">
                        {t(
                          `settings.role${invitation.role.charAt(0).toUpperCase()}${invitation.role.slice(1)}`,
                          { defaultValue: invitation.role },
                        )}
                      </Badge>
                    </TableCell>
                    <TableCell className="hidden text-sm text-muted-foreground sm:table-cell">
                      <AccessSummary
                        scoped={invitation.projects != null}
                        projects={invitation.projects}
                      />
                    </TableCell>
                    <TableCell className="w-10">
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("settings.revokeInvitation")}
                        disabled={revoke.isPending}
                        onClick={() =>
                          void confirmMember({
                            title: t("settings.revokeInvitation"),
                            description: t("settings.revokeInvitationConfirm", {
                              email: invitation.email,
                            }),
                            confirmLabel: t("common.remove"),
                            destructive: true,
                          }).then((yes) => {
                            if (yes) revoke.mutate(invitation.id)
                          })
                        }
                      >
                        <Trash2Icon className="size-4 text-muted-foreground" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {remove.error != null && <ErrorDisplay error={remove.error} />}
      {revoke.error != null && <ErrorDisplay error={revoke.error} />}
    </div>
  )
}

function SecurityPanel() {
  const { t } = useTranslation()
  const { meta } = useSession()
  const [recoveryKey, setRecoveryKey] = useState<string | null>(null)
  const confirm = useConfirm()

  const reveal = useMutation({
    mutationFn: () => api.get<{ recovery_key: string }>("/api/security/recovery-key"),
    onSuccess: (data) => setRecoveryKey(data.recovery_key),
  })

  const rotate = useMutation({
    mutationFn: () =>
      api.post<{ rewrapped: number; failed: number; recovery_key: string }>(
        "/api/security/rotate-key",
      ),
    onSuccess: (data) => {
      setRecoveryKey(data.recovery_key)
      // A partial rotation is not a failure: the old key is kept so the
      // secrets that did not rewrap stay readable. Say which one happened.
      if (data.failed > 0) {
        toast.warning(t("settings.rotateKeyPartial"))
      } else {
        toast.success(t("settings.rotateKeyDone"))
      }
    },
  })

  const download = () => {
    if (!recoveryKey) return
    const blob = new Blob([`${recoveryKey}\n`], { type: "text/plain" })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement("a")
    anchor.href = url
    anchor.download = "skifity-recovery-key.txt"
    anchor.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("settings.recoveryKey")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            {t("auth.recoveryKeyIntro", { product: meta?.product ?? "Skifity" })}
          </p>
          {recoveryKey ? (
            <div className="space-y-3">
              <pre className="rounded-md border bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap">
                {recoveryKey}
              </pre>
              <Alert>
                <AlertTitle>{t("auth.recoveryKeyWarning")}</AlertTitle>
                <AlertDescription>{t("databases.credentialsWarning")}</AlertDescription>
              </Alert>
              <Button variant="outline" size="sm" onClick={download}>
                <DownloadIcon className="size-4" />
                {t("auth.download")}
              </Button>
            </div>
          ) : (
            <Button variant="outline" disabled={reveal.isPending} onClick={() => reveal.mutate()}>
              <KeyRoundIcon className="size-4" />
              {t("settings.recoveryKey")}
            </Button>
          )}
          {reveal.error != null && <ErrorDisplay error={reveal.error} compact />}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("settings.rotateKey")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">{t("settings.rotateKeyHelp")}</p>
          {rotate.error != null && <ErrorDisplay error={rotate.error} compact />}
          <Button
            variant="outline"
            disabled={rotate.isPending}
            onClick={() => {
              void confirm({
                title: t("settings.rotateKey"),
                description: t("settings.rotateKeyHelp"),
                confirmLabel: t("settings.rotateKeyConfirm"),
              }).then((yes) => {
                if (yes) rotate.mutate()
              })
            }}
          >
            {rotate.isPending && <Spinner />}
            {rotate.isPending ? t("common.saving") : t("settings.rotateKeyConfirm")}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}

function AuditPanel() {
  const { t } = useTranslation()
  const { team } = useSession()

  const audit = useQuery({
    queryKey: ["audit", team?.id],
    queryFn: () => api.get<List<AuditEvent>>(`/api/teams/${team!.id}/audit`),
    enabled: Boolean(team),
  })

  if (audit.isLoading) return <Skeleton className="h-64" />
  if (audit.error) return <ErrorDisplay error={audit.error} onRetry={() => void audit.refetch()} />

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("settings.auditLogHelp")}</p>
      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.updated")}</TableHead>
                <TableHead>{t("common.actions")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("common.name")}</TableHead>
                <TableHead className="hidden md:table-cell">IP</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {audit.data?.items.map((event) => (
                <TableRow key={event.id}>
                  <TableCell className="text-xs whitespace-nowrap">
                    {formatDateTime(event.at)}
                  </TableCell>
                  <TableCell>
                    {/* The same phrase the Activity page shows, so one thing
                        is not called two names in one product. The code stays
                        on the hover, because this is the tab somebody reads
                        with a log open beside it. */}
                    <div className="text-xs" title={event.action}>
                      {t(`activity.action.${event.action}`, { defaultValue: event.action })}
                    </div>
                    <div className="text-xs text-muted-foreground">{event.actor_label}</div>
                  </TableCell>
                  <TableCell className="hidden text-xs sm:table-cell">
                    {event.target_label || event.target_type}
                  </TableCell>
                  <TableCell className="hidden font-mono text-xs text-muted-foreground md:table-cell">
                    {event.ip}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  )
}
