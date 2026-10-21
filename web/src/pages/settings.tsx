import { useState } from "react"
import { Link, Navigate, useSearchParams } from "react-router-dom"
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
  RotateCcwIcon,
  ShieldAlertIcon,
  Trash2Icon,
  UserCogIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { K3sUpgradeCard } from "@/components/settings/k3s-upgrade"
import { PanelUpgradeCard } from "@/components/settings/panel-upgrade"
import { BackupVerification } from "@/components/backup-verification"
import { CloudProvidersCard } from "@/components/settings/cloud-providers"
import { CertificatesCard } from "@/components/settings/certificates"
import { DNSProvidersCard } from "@/components/settings/dns-providers"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { GitSources } from "@/components/settings/git-sources"
import { RegistriesCard } from "@/components/settings/registries"
import { RecoveryKeyShown } from "@/components/settings/recovery-key"
import {
  AccessSummary,
  ChangeAccessDialog,
  ProjectLimitField,
  RoleSelect,
  canLimit,
  limitFor,
} from "@/components/settings/member-access"
import { LogDrains } from "@/components/settings/log-drains"
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
  SelectGroup,
  SelectItem,
  SelectLabel,
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
import { copyText } from "@/lib/clipboard"
import { downloadBlob } from "@/lib/download"
import { managesTeam } from "@/lib/permissions"
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

/**
 * Who may open a tab, by what its API asks for.
 *
 * "admin" is the panel's administrator: panel-wide settings, components,
 * plugins and the master key, all behind requireAdmin. "team" is anybody the
 * team's own pages answer — the panel's administrator included, since its
 * lists only want a viewer — while what they change is limited to the team's
 * owners and admins inside each card. "teamAdmin" is a tab whose list itself
 * wants the team's owner or admin: the audit log.
 */
type Access = "admin" | "team" | "teamAdmin"

type NavGroup = "panel" | "team" | "integrations"

const TABS: { value: string; label: string; group: NavGroup; access: Access }[] = [
  { value: "panel", label: "settings.general", group: "panel", access: "admin" },
  { value: "components", label: "settings.components", group: "panel", access: "admin" },
  { value: "plugins", label: "settings.plugins", group: "panel", access: "admin" },
  { value: "security", label: "settings.security", group: "panel", access: "admin" },
  { value: "members", label: "settings.members", group: "team", access: "team" },
  { value: "audit", label: "settings.auditLog", group: "team", access: "teamAdmin" },
  { value: "git", label: "settings.git", group: "integrations", access: "team" },
  { value: "certificates", label: "settings.certificates", group: "integrations", access: "team" },
  { value: "dns", label: "settings.dnsProviders", group: "integrations", access: "team" },
  { value: "cloud", label: "settings.cloudProviders", group: "integrations", access: "team" },
  {
    value: "notifications",
    label: "settings.notifications",
    group: "integrations",
    access: "team",
  },
  { value: "secrets", label: "settings.secretManagers", group: "integrations", access: "team" },
  { value: "logs", label: "settings.logDrains", group: "integrations", access: "team" },
]

const NAV_GROUPS: { key: NavGroup; label: string }[] = [
  { key: "panel", label: "settings.navPanel" },
  { key: "team", label: "settings.navTeam" },
  { key: "integrations", label: "settings.navIntegrations" },
]

/** Whether the signed-in person is an owner or admin of the team in view. */
/**
 * An owner or admin of the current team who sees the whole of it: the team's
 * API refuses one limited to some projects, so the tabs and buttons do too.
 */
function useTeamAdmin(): boolean {
  const { team } = useSession()
  return managesTeam(team)
}

export function SettingsPage() {
  const { t } = useTranslation()
  const { user, team } = useSession()
  const teamAdmin = useTeamAdmin()
  // The tab is in the address, so a notification can link to the one it is
  // about: a certificate running out links to the certificates.
  const [params, setParams] = useSearchParams()
  // Edits to panel-wide settings, kept here rather than in each tab's form:
  // a tab that is not shown is unmounted, and somebody who typed an SMTP host
  // and went to look at the Git tab lost it without a word.
  const [draft, setDraft] = useState<Record<string, string>>({})

  const save = useMutation({
    mutationFn: () => api.put("/api/settings", { values: draft }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["settings"] })
      setDraft({})
      toast.success(t("settings.saved"))
    },
  })

  const isAdmin = user?.is_admin === true
  const allowed = (access: Access) => {
    if (access === "admin") return isAdmin
    if (!team) return false
    return access === "team" ? isAdmin || teamAdmin : teamAdmin
  }
  const visible = TABS.filter((entry) => allowed(entry.access))
  // Nothing here is theirs to see; the route should not have let them in.
  if (visible.length === 0) return <Navigate to="/" replace />

  // An unknown tab, or one this person may not open, is the first one they may.
  const tab = visible.find((entry) => entry.value === params.get("tab"))?.value ?? visible[0].value
  const choose = (value: string) => setParams({ tab: value }, { replace: true })
  const shows = (value: string) => visible.some((entry) => entry.value === value)
  const nav = NAV_GROUPS.map((group) => ({
    ...group,
    tabs: visible.filter((entry) => entry.group === group.key),
  })).filter((group) => group.tabs.length > 0)

  const edit = (key: string, value: string | undefined) =>
    setDraft((current) => {
      const next = { ...current }
      if (value === undefined) delete next[key]
      else next[key] = value
      return next
    })
  const pending = Object.keys(draft).length

  return (
    <Page>
      <PageHeader title={t("settings.title")} description={t("settings.subtitle")} />

      {/* Thirteen tabs in a row wrapped into a second, stretched one. On a
          wide screen they are a list down the side, in groups; below that, a
          picker. The list needs a laptop's width: on a tablet the sidebar and
          the list left the cards too narrow for their own buttons. Both are
          rendered and CSS picks one, so the layout follows the window without
          a re-render. */}
      <Tabs
        value={tab}
        onValueChange={choose}
        orientation="vertical"
        className="flex-col gap-6 lg:flex-row"
      >
        <Select value={tab} onValueChange={choose}>
          <SelectTrigger className="w-full lg:hidden" aria-label={t("settings.sections")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {nav.map((group) => (
              <SelectGroup key={group.key}>
                <SelectLabel>{t(group.label)}</SelectLabel>
                {group.tabs.map((entry) => (
                  <SelectItem key={entry.value} value={entry.value}>
                    {t(entry.label)}
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
          </SelectContent>
        </Select>
        <nav
          aria-label={t("settings.sections")}
          className="hidden w-48 shrink-0 space-y-4 lg:block"
        >
          {nav.map((group) => (
            <div key={group.key} className="space-y-1">
              <p
                id={`settings-nav-${group.key}`}
                className="px-3 text-xs font-medium text-muted-foreground"
              >
                {t(group.label)}
              </p>
              <TabsList
                aria-labelledby={`settings-nav-${group.key}`}
                className="w-full items-stretch gap-0.5 bg-transparent p-0"
              >
                {group.tabs.map((entry) => (
                  <TabsTrigger
                    key={entry.value}
                    value={entry.value}
                    className="px-3 py-1.5 data-[state=active]:bg-muted data-[state=active]:shadow-none"
                  >
                    {t(entry.label)}
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>
          ))}
        </nav>

        <div className="min-w-0 flex-1">
          {shows("panel") && (
            <TabsContent value="panel" className="space-y-6">
              {/* Git and plugins have tabs of their own. */}
              <SettingGroups except={["git", "plugins"]} draft={draft} onEdit={edit} />
              <PanelBackupsCard />
              <PanelUpgradeCard />
            </TabsContent>
          )}
          {shows("components") && (
            <TabsContent value="components">
              <ComponentsPanel />
            </TabsContent>
          )}
          {shows("plugins") && (
            <TabsContent value="plugins" className="space-y-6">
              <SettingGroups only={["plugins"]} draft={draft} onEdit={edit} />
              <StoreCheckCard />
            </TabsContent>
          )}
          {shows("security") && (
            <TabsContent value="security">
              <SecurityPanel />
            </TabsContent>
          )}
          {shows("members") && (
            <TabsContent value="members">
              <MembersPanel />
            </TabsContent>
          )}
          {shows("audit") && (
            <TabsContent value="audit">
              <AuditPanel />
            </TabsContent>
          )}
          {shows("git") && (
            <TabsContent value="git" className="space-y-6">
              <GitSources />
              <RegistriesCard />
              {/* The hosts and authorities builds trust are the whole panel's,
                  so only its administrator sees them. */}
              {isAdmin && <SettingGroups only={["git"]} draft={draft} onEdit={edit} />}
            </TabsContent>
          )}
          {shows("certificates") && (
            <TabsContent value="certificates">
              <CertificatesCard />
            </TabsContent>
          )}
          {shows("dns") && (
            <TabsContent value="dns">
              <DNSProvidersCard />
            </TabsContent>
          )}
          {shows("cloud") && (
            <TabsContent value="cloud">
              <CloudProvidersCard />
            </TabsContent>
          )}
          {shows("notifications") && (
            <TabsContent value="notifications">
              <NotificationChannels />
            </TabsContent>
          )}
          {shows("secrets") && (
            <TabsContent value="secrets">
              <SecretManagersCard />
            </TabsContent>
          )}
          {shows("logs") && (
            <TabsContent value="logs">
              <LogDrains />
            </TabsContent>
          )}
        </div>
      </Tabs>

      {save.error != null && <ErrorDisplay error={save.error} />}

      {/* The bar only appears once something has changed, so it is never a
          button that does nothing. It stays while somebody moves between
          tabs, because the edits do. */}
      {pending > 0 && (
        <div className="sticky bottom-4 z-10 flex items-center justify-between gap-3 rounded-lg border bg-card/95 px-4 py-3 shadow-lg backdrop-blur">
          <span className="text-sm text-muted-foreground">
            {t("settings.unsaved", { count: pending })}
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
    </Page>
  )
}

function SettingGroups({
  only,
  except,
  draft,
  onEdit,
}: {
  only?: string[]
  except?: string[]
  draft: Record<string, string>
  /** A new value for a setting, "" to clear it, or undefined to forget the edit. */
  onEdit: (key: string, value: string | undefined) => void
}) {
  const { t } = useTranslation()
  const confirmRemove = useConfirm()

  const settings = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<List<Setting>>("/api/settings"),
  })

  // Removing a stored secret is its own act, asked about, and saved at once:
  // it used to be what happened to anybody who typed in the box and then
  // emptied it again, on the next save of something unrelated.
  const removeSecret = useMutation({
    mutationFn: (key: string) => api.put("/api/settings", { values: { [key]: "" } }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["settings"] }),
  })

  if (settings.isLoading) return <Skeleton className="h-96" />
  if (settings.error) {
    return <ErrorDisplay error={settings.error} onRetry={() => void settings.refetch()} />
  }

  const items = settings.data?.items ?? []
  const shown = groupsOf(items).filter(
    (group) => (!only || only.includes(group.key)) && (!except || !except.includes(group.key)),
  )

  /** What a text box shows: the edit in progress, else what is stored. */
  const typed = (setting: Setting) =>
    draft[setting.key] ?? (setting.secret ? "" : (setting.value ?? ""))

  /**
   * What a switch or a choice shows: the edit in progress, else what is
   * stored, else what the panel does while nothing is — a cleared one
   * included, which goes back to that.
   */
  const effective = (setting: Setting) => {
    const edit = draft[setting.key]
    if (edit !== undefined && edit !== "") return edit
    if (edit === "") return setting.default ?? ""
    return setting.value ?? setting.default ?? ""
  }

  const change = (setting: Setting, value: string) => {
    // An emptied secret box is a change of mind, not an instruction: saving
    // "" deletes what is stored, and the passphrase every backup is sealed
    // with is not something to lose to a stray keystroke.
    if (setting.secret && value.trim() === "") onEdit(setting.key, undefined)
    else onEdit(setting.key, value)
  }

  /** Back to what unset means: forget an edit to an unset one, or clear a stored one. */
  const reset = (setting: Setting) =>
    onEdit(setting.key, setting.value === undefined ? undefined : "")

  const askRemove = (setting: Setting, label: string) => {
    void confirmRemove({
      title: t("settings.removeSecretTitle", { name: label }),
      description: t("settings.removeSecretConfirm"),
      confirmLabel: t("common.remove"),
      destructive: true,
    }).then((yes) => {
      if (!yes) return
      removeSecret.mutate(setting.key, {
        onSuccess: () => toast.success(t("settings.secretRemoved", { name: label })),
      })
    })
  }

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
                {groupItems.map((setting) => {
                  const edit = draft[setting.key]
                  return (
                    <SettingField
                      key={setting.key}
                      setting={setting}
                      value={
                        setting.kind === "bool" || setting.kind === "choice"
                          ? effective(setting)
                          : typed(setting)
                      }
                      edited={edit !== undefined}
                      // A switch or a choice cannot be emptied by hand, so it
                      // offers the way back to unset; a text box is emptied.
                      resettable={
                        !setting.secret &&
                        (setting.default !== undefined ||
                          setting.kind === "bool" ||
                          setting.kind === "choice") &&
                        edit !== "" &&
                        (setting.value !== undefined || edit !== undefined)
                      }
                      removing={removeSecret.isPending && removeSecret.variables === setting.key}
                      onChange={(value) => change(setting, value)}
                      onReset={() => reset(setting)}
                      onRemove={(label) => askRemove(setting, label)}
                    />
                  )
                })}
              </FieldGroup>
            </CardContent>
          </Card>
        )
      })}

      {removeSecret.error != null && <ErrorDisplay error={removeSecret.error} />}
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
  resettable,
  removing,
  onChange,
  onReset,
  onRemove,
}: {
  setting: Setting
  value: string
  edited: boolean
  /** Whether going back to what unset means would change anything. */
  resettable: boolean
  removing: boolean
  onChange: (value: string) => void
  onReset: () => void
  onRemove: (label: string) => void
}) {
  const { t } = useTranslation()
  const id = `setting-${setting.key}`
  // A text box that is empty shows what empty means, when there is a default.
  const placeholder =
    setting.secret && setting.configured
      ? t("settings.secretStored")
      : (setting.default ?? setting.placeholder)

  // The server's label and help are English, because the server has one
  // language and the API, the CLI and an assistant all read them. The panel has
  // five, and this page was the largest English surface left in it: every label
  // and every paragraph, in Indonesian, Hindi, Russian and Chinese alike. The
  // translation is looked up by the setting's own key, with the server's text
  // as the fallback for a setting added before anybody has translated it.
  const field = `settings.field.${setting.key.replace(".", "_")}`
  const label = t(`${field}.label`, { defaultValue: setting.label })
  const help = setting.help ? t(`${field}.help`, { defaultValue: setting.help }) : ""

  const resetButton = resettable && (
    <Button type="button" variant="ghost" size="xs" className="w-fit" onClick={onReset}>
      <RotateCcwIcon />
      {t("settings.resetToDefault")}
    </Button>
  )

  if (setting.kind === "bool") {
    return (
      <Field orientation="horizontal" data-edited={edited || undefined}>
        <FieldContent>
          <FieldLabel htmlFor={id}>
            {label}
            {edited && (
              <Badge variant="secondary" className="ml-2 text-[10px]">
                {t("settings.edited")}
              </Badge>
            )}
          </FieldLabel>
          <FieldDescription>{help}</FieldDescription>
          {resetButton}
        </FieldContent>
        {/* Unset is drawn as what unset does: "Scan images" is on until
            somebody turns it off, and was drawn off beside help text saying
            so. */}
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
      {resetButton}
      {setting.secret && setting.configured && !edited && (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="w-fit text-destructive hover:text-destructive"
          disabled={removing}
          onClick={() => onRemove(label)}
        >
          {removing ? <Spinner /> : <Trash2Icon />}
          {t("common.remove")}
        </Button>
      )}
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
  const teamAdmin = useTeamAdmin()
  const confirm = useConfirm()

  const update = useMutation({
    mutationFn: (required: boolean) =>
      api.patch(`/api/teams/${team!.id}`, { require_strong_auth: required }),
    onSuccess: () => void refresh(),
  })

  if (!team || !teamAdmin) return null

  return (
    <Card>
      <CardContent className="space-y-3 pt-6">
        <Field orientation="horizontal">
          <Switch
            id="require-strong-auth"
            checked={team.require_strong_auth ?? false}
            disabled={update.isPending}
            onCheckedChange={(checked) => {
              // Relaxing it locks nobody out. Requiring it does, on their very
              // next request, everybody in the team with a password alone.
              if (!checked) {
                update.mutate(false)
                return
              }
              void confirm({
                title: t("settings.requireStrongAuth"),
                description: t("settings.requireStrongAuthConfirm", { team: team.name }),
                confirmLabel: t("settings.requireStrongAuthTurnOn"),
              }).then((yes) => {
                if (yes) update.mutate(true)
              })
            }}
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
 *
 * Only for the team's owners and admins, which is what the export asks for.
 */
function ExportCard() {
  const { t } = useTranslation()
  const { team } = useSession()

  // Fetched rather than linked: a plain link saved whatever came back, so a
  // refusal or a failure was written to disk as the export, under the
  // export's name, and looked like one until somebody needed it.
  const download = useMutation({
    mutationFn: () => api.get<unknown>(`/api/teams/${team!.id}/export`),
    onSuccess: (data) =>
      downloadBlob(
        `${team!.slug}-export.json`,
        new Blob([`${JSON.stringify(data, null, 2)}\n`], { type: "application/json" }),
      ),
  })

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
          <Button variant="outline" disabled={download.isPending} onClick={() => download.mutate()}>
            {download.isPending ? <Spinner /> : <DownloadIcon />}
            {t("settings.exportDownload")}
          </Button>
          <code className="rounded bg-muted px-2 py-1 font-mono text-xs">skifity export</code>
        </div>
        {download.error != null && <ErrorDisplay error={download.error} compact />}
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
          {/* min-h-6: a link under 24px is a target somebody misses. */}
          <Link
            className="inline-flex min-h-6 items-center underline underline-offset-4"
            to="/plugins"
          >
            {t("settings.pluginStoreGoToPlugins")}
          </Link>
        </p>
      </CardContent>
    </Card>
  )
}

function ComponentsPanel() {
  const { t } = useTranslation()
  const confirmUpgrade = useConfirm()

  const title = (component: Component) =>
    t(`components.${component.name}.title`, { defaultValue: component.title })

  // The answer comes when the install has finished, up to ten minutes later.
  // Until then the list is what says it is happening — the server records
  // "installing" before it starts — on this tab and on any other that opens
  // it, including after a reload.
  const install = useMutation({
    mutationFn: (component: Component) => api.post(`/api/components/${component.name}/install`),
    onSuccess: (_, component) =>
      toast.success(t("settings.componentInstalledToast", { name: title(component) })),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ["components"] }),
  })
  const upgrade = useMutation({
    mutationFn: (name: string) => api.post(`/api/components/${name}/upgrade`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["components"] }),
  })

  const components = useQuery({
    queryKey: ["components"],
    queryFn: () => api.get<List<Component>>("/api/components"),
    refetchInterval: (query) =>
      install.isPending ||
      (query.state.data?.items ?? []).some((component) => component.status === "installing")
        ? 3000
        : false,
  })

  if (components.isLoading) return <Skeleton className="h-64" />
  if (components.error) {
    return <ErrorDisplay error={components.error} onRetry={() => void components.refetch()} />
  }

  // The server installs one at a time, so a second press would only queue
  // behind the first without saying so.
  const busy =
    install.isPending ||
    (components.data?.items ?? []).some((component) => component.status === "installing")

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
                  <span className="font-medium">{title(component)}</span>
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
                  {component.status === "installing" && (
                    <StatusBadge status="installing" label={t("settings.componentInstalling")} />
                  )}
                  {component.status === "failed" && (
                    <StatusBadge status="failed" label={t("settings.componentFailed")} />
                  )}
                </div>
                <p className="mt-0.5 text-sm text-muted-foreground">
                  {t(`components.${component.name}.description`, {
                    defaultValue: component.description,
                  })}
                </p>
                {component.detail && (
                  <p
                    className={
                      component.status === "failed"
                        ? "mt-0.5 text-xs break-words text-destructive"
                        : "mt-0.5 text-xs text-muted-foreground"
                    }
                  >
                    {component.detail}
                  </p>
                )}
              </div>

              {component.external ? (
                // An Install button that always answers "use Helm instead" is
                // worse than no button: it looks like a failure. The page is
                // the panel's documentation, which Go serves and the app's
                // router does not know: a router link opened "Not found".
                <Button variant="outline" size="sm" asChild>
                  <a href={component.docs ?? "/docs"} target="_blank" rel="noreferrer">
                    <BookOpenIcon className="size-4" />
                    {t("settings.componentExternal")}
                  </a>
                </Button>
              ) : component.upgrade_available ? (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={upgrade.isPending}
                  onClick={() =>
                    void confirmUpgrade({
                      title: t("settings.componentUpgradeTitle", { name: title(component) }),
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
              ) : component.status === "installing" ? null : (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  onClick={() => install.mutate(component)}
                >
                  {install.isPending && install.variables?.name === component.name ? (
                    <Spinner />
                  ) : (
                    <PackagePlusIcon className="size-4" />
                  )}
                  {component.status === "failed"
                    ? t("common.retry")
                    : t("settings.componentInstall")}
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
  // Adding, changing and removing people is the team's owners' and admins'.
  // A panel administrator who is a member here sees the list and nothing that
  // would answer 403.
  const canChange = useTeamAdmin()
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
    enabled: Boolean(team) && canChange,
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
                {canChange && <TableHead className="w-20" />}
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
                  {canChange && (
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
                  )}
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

      {canChange && (
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
      )}

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
                void copyText(link).then((copied) =>
                  copied ? toast.success(t("common.copied")) : toast.error(t("common.copyFailed")),
                )
              }}
            >
              <CopyIcon className="size-4" />
              {t("common.copy")}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {invitations.error != null && (
        <ErrorDisplay error={invitations.error} onRetry={() => void invitations.refetch()} />
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

      {canChange && <ExportCard />}
    </div>
  )
}

function SecurityPanel() {
  const { t } = useTranslation()
  const { meta, refresh } = useSession()
  const [shown, setShown] = useState<{ key: string; rotated: boolean } | null>(null)
  const confirm = useConfirm()

  const reveal = useMutation({
    mutationFn: () => api.get<{ recovery_key: string }>("/api/security/recovery-key"),
    onSuccess: (data) => setShown({ key: data.recovery_key, rotated: false }),
  })

  const rotate = useMutation({
    mutationFn: () =>
      api.post<{ rewrapped: number; failed: number; recovery_key: string }>(
        "/api/security/rotate-key",
      ),
    onSuccess: (data) => {
      setShown({ key: data.recovery_key, rotated: true })
      // The panel has asked every administrator to save the new key, this
      // one included; the button that says so needs to know.
      void refresh()
      // A partial rotation is not a failure: the old key is kept so the
      // secrets that did not rewrap stay readable. Say which one happened.
      if (data.failed > 0) {
        toast.warning(t("settings.rotateKeyPartial"))
      } else {
        toast.success(t("settings.rotateKeyDone"))
      }
    },
  })

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
          {shown ? (
            <RecoveryKeyShown recoveryKey={shown.key} rotated={shown.rotated} />
          ) : (
            <Button variant="outline" disabled={reveal.isPending} onClick={() => reveal.mutate()}>
              {reveal.isPending ? <Spinner /> : <KeyRoundIcon className="size-4" />}
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
                // The recovery key is a copy of the key being replaced, so
                // the one on paper stops opening what is re-encrypted.
                consequence: t("settings.rotateKeyConsequence"),
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

  const items = audit.data?.items ?? []

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("settings.auditLogHelp")}</p>
      {items.length === 0 ? (
        <EmptyState title={t("settings.auditEmpty")} />
      ) : (
        <Card>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("settings.auditTime")}</TableHead>
                  <TableHead>{t("settings.auditAction")}</TableHead>
                  <TableHead className="hidden sm:table-cell">
                    {t("settings.auditTarget")}
                  </TableHead>
                  <TableHead className="hidden md:table-cell">{t("common.ipAddress")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((event) => (
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
      )}
    </div>
  )
}
