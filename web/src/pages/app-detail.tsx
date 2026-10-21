import { Link, useSearchParams, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BoxIcon, ExternalLinkIcon, InfoIcon, RefreshCwIcon, RocketIcon } from "lucide-react"
import { toast } from "sonner"

import { AdvancedTab } from "@/components/app/advanced-tab"
import { DeployButton, DeploymentsTab } from "@/components/app/deployments-tab"
import { SendFolderButton } from "@/components/folder-picker"
import { DomainsTab } from "@/components/app/domains-tab"
import { AppFirewall } from "@/components/app/firewall"
import { AppPasswordProtection } from "@/components/app/password-protection"
import { ConsoleTab, RUN_PARAM } from "@/components/app/console-tab"
import { LogsTab } from "@/components/app/logs-tab"
import { ScalingTab } from "@/components/app/scaling-tab"
import { SecurityTab, vulnerabilitiesKey } from "@/components/app/security-tab"
import { FilesCard } from "@/components/app/files"
import { PortsCard } from "@/components/app/ports"
import { ProcessesCard } from "@/components/app/processes"
import { SettingsTab, settingsKey } from "@/components/app/settings-tab"
import { StorageTab } from "@/components/app/storage-tab"
import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { DeployLockButton, DeployLockNotice } from "@/components/app/deploy-lock"
import { PreviewBranchButton } from "@/components/app/preview-branch"
import { DriftNotice } from "@/components/app/drift"
import { MaintenanceNotice } from "@/components/app/maintenance"
import { UsageCard } from "@/components/app/usage-card"
import { Page, PageHeader } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { VariablesEditor } from "@/components/variables-editor"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
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
import { api } from "@/lib/api"
import { formatCPU, formatMemory, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, AppStatus, Environment, Instance, Project } from "@/lib/types"

export function AppDetailPage() {
  const { t } = useTranslation()
  const { appId = "" } = useParams()
  const { team } = useSession()
  const confirm = useConfirm()
  const [params, setParams] = useSearchParams()
  const tab = params.get("tab") ?? "overview"
  // A console run stays named in the address while other tabs are open, so
  // its output is there again when the console is: see ConsoleTab.
  const openTab = (next: string) =>
    setParams((previous) => {
      const kept = new URLSearchParams()
      if (next !== "overview") kept.set("tab", next)
      const run = previous.get(RUN_PARAM)
      if (run) kept.set(RUN_PARAM, run)
      return kept
    })

  const app = useQuery({
    queryKey: ["app", appId],
    queryFn: () => api.get<App>(`/api/apps/${appId}`),
  })

  const status = useQuery({
    queryKey: ["app-status", appId],
    queryFn: () => api.get<AppStatus>(`/api/apps/${appId}/status`),
    // A rollout changes what is running from one second to the next, and the
    // event stream only carries deployment state, not instance state.
    refetchInterval: 10_000,
  })

  // Where this app lives. An app page used to be an island: the breadcrumb said
  // "Apps", the sidebar could not say which project this belonged to, and the
  // only way back was the browser's own button. Both queries are keyed so they
  // come from the cache when anything else has already asked.
  const environment = useQuery({
    queryKey: ["environment", app.data?.environment_id],
    queryFn: () => api.get<Environment>(`/api/environments/${app.data!.environment_id}`),
    enabled: Boolean(app.data?.environment_id),
  })
  const project = useQuery({
    queryKey: ["project", environment.data?.project_id],
    queryFn: () => api.get<Project>(`/api/projects/${environment.data!.project_id}`),
    enabled: Boolean(environment.data?.project_id),
  })

  // The team's stream carries every project's events, so a member limited to
  // some projects is refused it; their page keeps up by polling instead.
  useEvents(
    team && !team.scoped ? [`team:${team.id}`] : [],
    {
      deployment: () => {
        void queryClient.invalidateQueries({ queryKey: ["app-status", appId] })
        void queryClient.invalidateQueries({ queryKey: ["deployments", appId] })
      },
      // The watcher publishes these two and nothing listened: an app that fell
      // over between deployments, and a certificate that went active or failed.
      // The status poll caught the first within ten seconds; the second was not
      // caught at all, on the tab where somebody sits and waits for it.
      //
      // An app event is also how an alert crossing its threshold is said, and
      // the usage card's "firing" line read the alerts it loaded first.
      app: () => {
        void queryClient.invalidateQueries({ queryKey: ["app-status", appId] })
        void queryClient.invalidateQueries({ queryKey: ["alerts", appId] })
      },
      domain: () => void queryClient.invalidateQueries({ queryKey: ["domains", appId] }),
      // A scan of this app's image, or any other's: which one is not worth
      // reading the event for, and a refetch of one report is cheap.
      scan: () => void queryClient.invalidateQueries({ queryKey: vulnerabilitiesKey(appId) }),
      // The watcher found this app changed outside the panel, or back in step.
      drift: () => void queryClient.invalidateQueries({ queryKey: ["drift", appId] }),
    },
    Boolean(team),
  )

  const restart = useMutation({
    mutationFn: () => api.post(`/api/apps/${appId}/restart`),
    onSuccess: () => {
      // Instances are replaced one at a time, so for a while nothing on the
      // page looks any different from before the button was pressed.
      toast.success(t("apps.restartStarted"))
      void queryClient.invalidateQueries({ queryKey: ["app-status", appId] })
    },
  })

  if (app.error) return <ErrorDisplay error={app.error} onRetry={() => void app.refetch()} />
  if (app.isLoading || !app.data) return <Skeleton className="h-96" />

  const current = app.data

  return (
    <Page>
      <PageHeader
        icon={BoxIcon}
        title={current.name}
        description={
          project.data && (
            <span className="inline-flex flex-wrap items-center gap-1.5">
              {/* min-h-6: a link in a subtitle is still a tap target, and the
                  layout test counts anything under 24px as a failure. */}
              <Link
                to={`/projects/${project.data.id}`}
                className="inline-flex min-h-6 items-center underline-offset-4 hover:underline"
              >
                {project.data.name}
              </Link>
              {environment.data && (
                <>
                  <span aria-hidden>·</span>
                  <span>{environment.data.name}</span>
                </>
              )}
            </span>
          )
        }
        badge={
          <StatusBadge
            status={status.data?.phase ?? current.status}
            label={t(`apps.phase.${status.data?.phase ?? current.status}`, {
              defaultValue: status.data?.phase ?? current.status,
            })}
          />
        }
        actions={
          <>
            <Button
              variant="outline"
              disabled={restart.isPending}
              onClick={() => {
                void confirm({
                  title: t("apps.restart"),
                  description: t("apps.restartConfirm"),
                  confirmLabel: t("apps.restart"),
                }).then((yes) => {
                  if (yes) restart.mutate()
                })
              }}
            >
              <RefreshCwIcon />
              {t("apps.restart")}
            </Button>
            {current.source_type === "upload" && <SendFolderButton app={current} />}
            {current.source_type === "git" && environment.data?.kind === "standard" && (
              <PreviewBranchButton app={current} projectId={environment.data.project_id} />
            )}
            <DeployLockButton app={current} />
            <DeployButton appId={appId} />
          </>
        }
      />

      {/* The app's addresses belong directly under its name: on this page they
          are the thing people came for. */}
      {(status.data?.urls?.length ?? 0) > 0 && (
        <div className="-mt-3 flex flex-wrap items-center gap-2">
          {status.data?.urls?.map((url) => (
            // The address and the way to take it with you, as one control: an
            // address is read far more often than it is clicked, and reading it
            // off the screen to paste it somewhere was the only way to do that.
            <div key={url} className="flex items-center rounded-md border">
              <Button variant="ghost" size="sm" className="rounded-r-none" asChild>
                <a href={url} target="_blank" rel="noreferrer">
                  <ExternalLinkIcon />
                  {url.replace(/^https?:\/\//, "")}
                </a>
              </Button>
              <CopyButton value={url} label={t("apps.address")} className="rounded-l-none" />
            </div>
          ))}
        </div>
      )}

      {/* An internal app has no address to open, only the name its
          environment reaches it by, which is worth being able to copy. */}
      {status.data?.internal_address && (
        <div className="-mt-3 flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">{t("apps.internalAddress")}</span>
          <div className="flex items-center rounded-md border">
            <code className="px-2.5 text-xs">{status.data.internal_address}</code>
            <CopyButton
              value={status.data.internal_address}
              label={t("apps.internalAddress")}
              className="rounded-l-none"
            />
          </div>
        </div>
      )}

      {restart.error != null && <ErrorDisplay error={restart.error} />}
      <DeployLockNotice app={current} />
      <MaintenanceNotice app={current} />
      {/* Not on the Advanced tab itself, which lists the same thing in full. */}
      {tab !== "advanced" && <DriftNotice app={current} onOpen={() => openTab("advanced")} />}

      <Tabs value={tab} onValueChange={openTab}>
        <TabsList className="flex-wrap">
          <TabsTrigger value="overview">{t("apps.overview")}</TabsTrigger>
          <TabsTrigger value="deployments">{t("apps.deployments")}</TabsTrigger>
          <TabsTrigger value="logs">{t("apps.logs")}</TabsTrigger>
          <TabsTrigger value="console">{t("apps.console")}</TabsTrigger>
          <TabsTrigger value="variables">{t("apps.variables")}</TabsTrigger>
          <TabsTrigger value="domains">{t("apps.domains")}</TabsTrigger>
          <TabsTrigger value="firewall">{t("apps.firewall")}</TabsTrigger>
          <TabsTrigger value="security">{t("apps.security")}</TabsTrigger>
          <TabsTrigger value="scaling">{t("apps.scaling")}</TabsTrigger>
          <TabsTrigger value="storage">{t("apps.storage")}</TabsTrigger>
          <TabsTrigger value="settings">{t("apps.settings")}</TabsTrigger>
          <TabsTrigger value="advanced">{t("apps.advanced")}</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="pt-4">
          <Overview
            app={current}
            status={status.data}
            loading={status.isLoading}
            error={status.error}
            onRetry={() => void status.refetch()}
          />
        </TabsContent>
        <TabsContent value="deployments" className="pt-4">
          <DeploymentsTab app={current} />
        </TabsContent>
        <TabsContent value="logs" className="pt-4">
          <LogsTab app={current} />
        </TabsContent>
        <TabsContent value="console" className="pt-4">
          <ConsoleTab app={current} />
        </TabsContent>
        <TabsContent value="variables" className="space-y-6 pt-4">
          <VariablesEditor
            base={`/api/apps/${appId}`}
            queryKey={["variables", appId]}
            showBuildTime
            showPreviews
            refreshable
            projectId={environment.data?.project_id}
          />
          <FilesCard app={current} />
        </TabsContent>
        <TabsContent value="domains" className="pt-4">
          <DomainsTab app={current} />
          <div className="pt-6">
            <PortsCard app={current} />
          </div>
        </TabsContent>
        <TabsContent value="firewall" className="space-y-4 pt-4">
          <AppPasswordProtection appId={appId} />
          <AppFirewall appId={appId} />
        </TabsContent>
        <TabsContent value="security" className="pt-4">
          <SecurityTab app={current} />
        </TabsContent>
        <TabsContent value="scaling" className="space-y-6 pt-4">
          <ScalingTab app={current} />
          <ProcessesCard app={current} />
        </TabsContent>
        <TabsContent value="storage" className="pt-4">
          <StorageTab app={current} />
        </TabsContent>
        <TabsContent value="settings" className="pt-4">
          <SettingsTab key={settingsKey(current)} app={current} />
        </TabsContent>
        <TabsContent value="advanced" className="pt-4">
          <AdvancedTab app={current} />
        </TabsContent>
      </Tabs>
    </Page>
  )
}

/** detailTone maps an app's phase onto how loudly its status line should read. */
function detailTone(phase?: string): "destructive" | "warning" | "default" {
  switch (phase) {
    case "failed":
    case "unreachable":
      return "destructive"
    case "degraded":
    case "unknown":
      return "warning"
    default:
      return "default"
  }
}

/**
 * The sentence under the phase, in the reader's language.
 *
 * The phases whose sentence never varies, or varies only by the instance
 * counts the response already carries, are said here; the rest carry a
 * message from the cluster and fall back to what the panel was told, in
 * English. "1 instance(s) running." was the most common sentence on this page,
 * and it was English in all five languages.
 */
function phaseDetail(t: TFunction, status: AppStatus): string {
  const english = status.detail ?? ""
  switch (status.phase) {
    case "running":
      return t("apps.detail.running", { count: status.ready_replicas })
    case "updating":
      return t("apps.detail.updating", {
        ready: status.ready_replicas,
        desired: status.desired_replicas,
      })
    case "starting":
    case "crashing":
    case "sleeping":
    case "stopped":
    case "not_deployed":
      return t(`apps.detail.${status.phase}`, { defaultValue: english })
    default:
      return status.detail_key === "noCluster" ? t("apps.detail.noCluster") : english
  }
}

/** What an instance's state is called and how loudly it reads. */
type InstanceState = { key: string; tone: string }

/**
 * An instance's state, in the product's words.
 *
 * The cluster reports a pod's phase, or the reason its container is waiting or
 * stopped, and that word was shown as it came: CrashLoopBackOff on the
 * Overview, in English in every language, and in the same grey as an instance
 * nobody knows anything about. Each reason is one of a few states here, with a
 * word of its own and the colour of what it means; the cluster's own message
 * is still in the card under the table. `tone` is the status StatusBadge
 * colours by.
 */
function instanceState(instance: Instance): InstanceState {
  if (instance.ready) return { key: "running", tone: "running" }
  switch (instance.status) {
    // Up, and not answering its health check yet: starting, or failing it.
    case "Running":
      return { key: "notReady", tone: "not_ready" }
    case "Pending":
    case "ContainerCreating":
    case "PodInitializing":
      return { key: "starting", tone: "starting" }
    case "Unschedulable":
      return { key: "waitingForCapacity", tone: "pending" }
    case "CrashLoopBackOff":
      return { key: "crashing", tone: "crashing" }
    case "ImagePullBackOff":
    case "ErrImagePull":
    case "ErrImageNeverPull":
    case "InvalidImageName":
      return { key: "imageUnavailable", tone: "failed" }
    case "CreateContainerConfigError":
    case "CreateContainerError":
    case "RunContainerError":
    case "ContainerCannotRun":
      return { key: "cannotStart", tone: "failed" }
    case "OOMKilled":
      return { key: "outOfMemory", tone: "failed" }
    case "Error":
    case "Failed":
    case "DeadlineExceeded":
      return { key: "failed", tone: "failed" }
    case "Terminating":
      return { key: "stopping", tone: "stopped" }
    case "Completed":
    case "Succeeded":
      return { key: "exited", tone: "stopped" }
    default:
      return { key: "unknown", tone: "unknown" }
  }
}

function Overview({
  app,
  status,
  loading,
  error,
  onRetry,
}: {
  app: App
  status?: AppStatus
  loading: boolean
  error: unknown
  onRetry: () => void
}) {
  const { t } = useTranslation()

  if (loading) return <Skeleton className="h-64" />
  // Without this a failed read showed "0 / 0" and "No instances", which is a
  // claim about the app rather than about the request.
  if (error != null && !status) return <ErrorDisplay error={error} onRetry={onRetry} />

  const instances = status?.instances ?? []

  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-3">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">
              {/* "Instances" was the label here and the title of the card
                  below, and "0 / 0" said nothing about which number was
                  which. */}
              {t("apps.instancesReady")}
            </CardTitle>
          </CardHeader>
          <CardContent className="text-2xl font-semibold tabular-nums">
            {status?.ready_replicas ?? 0}
            <span className="text-muted-foreground"> / {status?.desired_replicas ?? 0}</span>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">
              {t("apps.source")}
            </CardTitle>
          </CardHeader>
          <CardContent className="truncate font-mono text-sm">
            {app.source_type === "image"
              ? app.image
              : app.source_type === "upload"
                ? t("apps.sourceUpload")
                : app.repo_url || "—"}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium text-muted-foreground">
              {t("apps.image")}
            </CardTitle>
          </CardHeader>
          <CardContent className="truncate font-mono text-xs text-muted-foreground">
            {status?.image || "—"}
          </CardContent>
        </Card>
      </div>

      {/* The same condition is an Alert on the Overview page and was a grey
          sentence floating between cards here. Its tone follows the phase it
          belongs to, so a cluster that cannot be reached reads as a problem and
          "waiting for the new instances" does not. */}
      {status?.detail && (
        <Alert variant={detailTone(status.phase)}>
          <InfoIcon />
          <AlertDescription>{phaseDetail(t, status)}</AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("apps.instances")}</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {instances.length === 0 ? (
            // No button here, and that is the one place this panel does not
            // repeat its primary action in an empty state.
            //
            // On Databases, Servers and Projects the empty state is the whole
            // page: the header's button and the empty state's are obviously the
            // same thing, because there is nothing else to be. This one sits in
            // a card titled "Instances", among other cards, with "Deploy now"
            // already in the header — and a second "Deploy now" inside a card
            // about instances reads as though it might start an instance
            // without deploying, which is not a thing. The sentence says what
            // deploying does instead, and the button it names is at the top of
            // the page, on every tab.
            <EmptyState
              bordered={false}
              icon={RocketIcon}
              // A title, not the sentence the logs tab uses as a description:
              // "No instances are running." with a full stop reads as a
              // paragraph where every other empty state has a heading.
              title={t("apps.noInstancesTitle")}
              // The button's own label, so the sentence names what is actually
              // written at the top of the page: it says "Deploy sekarang" in
              // Indonesian and "立即部署" in Chinese.
              description={t("apps.noInstancesHelp", { action: t("deploy.deployNow") })}
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("apps.instanceName")}</TableHead>
                  <TableHead>{t("common.status")}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t("apps.node")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("apps.restarts")}</TableHead>
                  {/* What the instance is using, not what it reserved: the
                      reserved numbers are the same for every instance and are
                      on the scaling tab, where they can be changed. */}
                  <TableHead className="hidden md:table-cell">{t("dashboard.cpuUsed")}</TableHead>
                  <TableHead className="hidden md:table-cell">
                    {t("dashboard.memoryUsed")}
                  </TableHead>
                  <TableHead className="hidden lg:table-cell">{t("common.created")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {instances.map((instance) => {
                  const state = instanceState(instance)
                  return (
                    <TableRow key={instance.name}>
                      <TableCell className="font-mono text-xs">{instance.name}</TableCell>
                      <TableCell>
                        <StatusBadge
                          status={state.tone}
                          label={t(`apps.instanceStatus.${state.key}`)}
                        />
                      </TableCell>
                      <TableCell className="hidden font-mono text-xs sm:table-cell">
                        {instance.node}
                      </TableCell>
                      <TableCell className="hidden tabular-nums md:table-cell">
                        {instance.restarts}
                      </TableCell>
                      <TableCell className="hidden tabular-nums md:table-cell">
                        {instance.cpu_m ? formatCPU(instance.cpu_m) : "—"}
                      </TableCell>
                      <TableCell className="hidden tabular-nums md:table-cell">
                        {instance.memory_mb ? formatMemory(instance.memory_mb) : "—"}
                      </TableCell>
                      <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">
                        {instance.started_at ? formatRelative(instance.started_at) : "—"}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {instances.some((instance) => instance.message) && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RocketIcon className="size-4" />
              {t("errors.details")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-1 text-sm text-muted-foreground">
            {instances
              .filter((instance) => instance.message)
              .map((instance) => (
                <p key={instance.name}>
                  <span className="font-mono text-xs">{instance.name}</span>: {instance.message}
                </p>
              ))}
          </CardContent>
        </Card>
      )}

      <UsageCard app={app} />
    </div>
  )
}
