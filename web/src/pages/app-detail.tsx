import { Link, useSearchParams, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BoxIcon, ExternalLinkIcon, InfoIcon, RefreshCwIcon, RocketIcon } from "lucide-react"

import { AdvancedTab } from "@/components/app/advanced-tab"
import { DeployButton, DeploymentsTab } from "@/components/app/deployments-tab"
import { SendFolderButton } from "@/components/folder-picker"
import { DomainsTab } from "@/components/app/domains-tab"
import { AppFirewall } from "@/components/app/firewall"
import { ConsoleTab } from "@/components/app/console-tab"
import { LogsTab } from "@/components/app/logs-tab"
import { ScalingTab } from "@/components/app/scaling-tab"
import { SettingsTab } from "@/components/app/settings-tab"
import { StorageTab } from "@/components/app/storage-tab"
import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
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
import type { App, AppStatus, Environment, Project } from "@/lib/types"

export function AppDetailPage() {
  const { t } = useTranslation()
  const { appId = "" } = useParams()
  const { team } = useSession()
  const confirm = useConfirm()
  const [params, setParams] = useSearchParams()
  const tab = params.get("tab") ?? "overview"

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

  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      deployment: () => {
        void queryClient.invalidateQueries({ queryKey: ["app-status", appId] })
        void queryClient.invalidateQueries({ queryKey: ["deployments", appId] })
      },
      // The watcher publishes these two and nothing listened: an app that fell
      // over between deployments, and a certificate that went active or failed.
      // The status poll caught the first within ten seconds; the second was not
      // caught at all, on the tab where somebody sits and waits for it.
      app: () => void queryClient.invalidateQueries({ queryKey: ["app-status", appId] }),
      domain: () => void queryClient.invalidateQueries({ queryKey: ["domains", appId] }),
    },
    Boolean(team),
  )

  const restart = useMutation({
    mutationFn: () => api.post(`/api/apps/${appId}/restart`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["app-status", appId] }),
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

      {restart.error != null && <ErrorDisplay error={restart.error} />}

      <Tabs
        value={tab}
        onValueChange={(next) => setParams(next === "overview" ? {} : { tab: next })}
      >
        <TabsList className="flex-wrap">
          <TabsTrigger value="overview">{t("apps.overview")}</TabsTrigger>
          <TabsTrigger value="deployments">{t("apps.deployments")}</TabsTrigger>
          <TabsTrigger value="logs">{t("apps.logs")}</TabsTrigger>
          <TabsTrigger value="console">{t("apps.console")}</TabsTrigger>
          <TabsTrigger value="variables">{t("apps.variables")}</TabsTrigger>
          <TabsTrigger value="domains">{t("apps.domains")}</TabsTrigger>
          <TabsTrigger value="firewall">{t("apps.firewall")}</TabsTrigger>
          <TabsTrigger value="scaling">{t("apps.scaling")}</TabsTrigger>
          <TabsTrigger value="storage">{t("apps.storage")}</TabsTrigger>
          <TabsTrigger value="settings">{t("apps.settings")}</TabsTrigger>
          <TabsTrigger value="advanced">{t("apps.advanced")}</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="pt-4">
          <Overview app={current} status={status.data} loading={status.isLoading} />
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
        <TabsContent value="variables" className="pt-4">
          <VariablesEditor
            base={`/api/apps/${appId}`}
            queryKey={["variables", appId]}
            showBuildTime
          />
        </TabsContent>
        <TabsContent value="domains" className="pt-4">
          <DomainsTab app={current} />
        </TabsContent>
        <TabsContent value="firewall" className="pt-4">
          <AppFirewall appId={appId} />
        </TabsContent>
        <TabsContent value="scaling" className="pt-4">
          <ScalingTab app={current} />
        </TabsContent>
        <TabsContent value="storage" className="pt-4">
          <StorageTab app={current} />
        </TabsContent>
        <TabsContent value="settings" className="pt-4">
          <SettingsTab app={current} />
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

function Overview({ app, status, loading }: { app: App; status?: AppStatus; loading: boolean }) {
  const { t } = useTranslation()

  if (loading) return <Skeleton className="h-64" />

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
          {/* A phase whose sentence never varies has that sentence in every
              language; the rest carry numbers or a message from the cluster
              and fall back to what the panel was told, in English. */}
          <AlertDescription>
            {t(`apps.detail.${status.phase}`, { defaultValue: status.detail })}
          </AlertDescription>
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
                {instances.map((instance) => (
                  <TableRow key={instance.name}>
                    <TableCell className="font-mono text-xs">{instance.name}</TableCell>
                    <TableCell>
                      <StatusBadge
                        status={instance.ready ? "running" : instance.status}
                        label={t(`apps.phase.${instance.status}`, {
                          defaultValue: instance.status,
                        })}
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
                ))}
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
    </div>
  )
}
