import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import {
  ActivityIcon,
  ArrowRightIcon,
  BoxesIcon,
  CpuIcon,
  FolderIcon,
  MemoryStickIcon,
  PlusIcon,
  RocketIcon,
  ServerIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
} from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader, Section } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { useEvents } from "@/hooks/use-events"
import { useFirstEnvironment } from "@/hooks/use-first-environment"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatCPU, formatMemory, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { AuditEvent, ClusterSummary, Project, Server } from "@/lib/types"

/**
 * The first screen.
 *
 * It answers three questions in order: is anything wrong, what have I got, and
 * what should I do next. A dashboard that leads with charts nobody reads is a
 * dashboard people stop opening.
 */
export function DashboardPage() {
  const { t } = useTranslation()
  const { team, user, meta } = useSession()

  const cluster = useQuery({
    queryKey: ["cluster", team?.id],
    queryFn: () => api.get<ClusterSummary>(`/api/teams/${team!.id}/cluster`),
    enabled: Boolean(team),
    refetchInterval: 15_000,
  })
  const servers = useQuery({
    queryKey: ["servers", team?.id],
    queryFn: () => api.get<List<Server>>(`/api/teams/${team!.id}/servers`),
    enabled: Boolean(team),
  })
  const projects = useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: Boolean(team),
  })
  const audit = useQuery({
    queryKey: ["audit", team?.id, "recent"],
    queryFn: () => api.get<List<AuditEvent>>(`/api/teams/${team!.id}/audit?limit=8`),
    enabled: Boolean(team) && Boolean(user?.is_admin),
  })

  // Anything that changes in this team refreshes the page without a reload.
  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      operation: () => {
        void queryClient.invalidateQueries({ queryKey: ["servers", team?.id] })
        void queryClient.invalidateQueries({ queryKey: ["cluster", team?.id] })
      },
      deployment: () =>
        void queryClient.invalidateQueries({ queryKey: ["audit", team?.id, "recent"] }),
    },
    Boolean(team),
  )

  const serverItems = servers.data?.items ?? []
  const projectItems = projects.data?.items ?? []
  const summary = cluster.data
  const loading = servers.isLoading || cluster.isLoading

  const firstRun = !loading && serverItems.length === 0
  const firstEnvironment = useFirstEnvironment(!firstRun)

  return (
    <Page>
      <PageHeader
        title={t("dashboard.title")}
        description={
          user?.name ? t("dashboard.welcomeBack", { name: user.name }) : t("dashboard.welcome")
        }
        actions={
          <>
            <Button variant="outline" asChild>
              <Link to="/servers/new">
                <ServerIcon />
                {t("servers.addServer")}
              </Link>
            </Button>
            {/*
              Deploying is the thing people came here to do, and until now the
              only way in was two lists deep: Projects, then the project, then
              New app. The quick start even told them to press a button that is
              not on this screen.
            */}
            {firstEnvironment ? (
              <Button asChild>
                <Link to={`/environments/${firstEnvironment}/apps/new`}>
                  <PlusIcon />
                  {t("apps.newApp")}
                </Link>
              </Button>
            ) : (
              <Button asChild>
                <Link to="/projects">
                  <PlusIcon />
                  {t("projects.newProject")}
                </Link>
              </Button>
            )}
          </>
        }
      />

      {/* Anything wrong comes first, above everything else on the page. */}
      {summary && !summary.reachable && (
        <Alert variant="destructive">
          <ShieldAlertIcon />
          <AlertTitle>{t("dashboard.clusterUnreachable")}</AlertTitle>
          <AlertDescription>
            <p>{t("dashboard.clusterUnreachableHelp", { product: meta?.product ?? "Skifity" })}</p>
            {summary.message && (
              <code className="mt-1 block font-mono text-xs break-all">{summary.message}</code>
            )}
          </AlertDescription>
        </Alert>
      )}

      {!user?.recovery_saved && (
        <Alert>
          <ShieldAlertIcon />
          <AlertTitle>{t("auth.recoveryKeyReminder")}</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center gap-3">
            <span>{t("auth.recoveryKeyIntro", { product: meta?.product ?? "Skifity" })}</span>
            <Button size="sm" variant="outline" asChild>
              <Link to="/account?recovery=1">{t("auth.download")}</Link>
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {cluster.error && (
        <ErrorDisplay error={cluster.error} onRetry={() => void cluster.refetch()} />
      )}

      {firstRun ? (
        <FirstRun />
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Stat
              icon={ServerIcon}
              label={t("nav.servers")}
              value={loading ? null : String(summary?.ready_nodes ?? serverItems.length)}
              detail={
                summary
                  ? t("common.of") + " " + (summary.nodes?.length ?? serverItems.length)
                  : undefined
              }
              to="/servers"
            />
            <Stat
              icon={FolderIcon}
              label={t("nav.projects")}
              value={projects.isLoading ? null : String(projectItems.length)}
              to="/projects"
            />
            <UsageStat
              icon={CpuIcon}
              label={t("dashboard.cpuUsed")}
              used={summary?.used_cpu_m ?? 0}
              total={summary?.total_cpu_m ?? 0}
              render={formatCPU}
              loading={loading}
            />
            <UsageStat
              icon={MemoryStickIcon}
              label={t("dashboard.memoryUsed")}
              used={summary?.used_memory_mb ?? 0}
              total={summary?.total_memory_mb ?? 0}
              render={formatMemory}
              loading={loading}
            />
          </div>

          {summary?.reachable && (
            <Alert variant={summary.high_availability ? "default" : "warning"}>
              {summary.high_availability ? <ShieldCheckIcon /> : <ShieldAlertIcon />}
              <AlertTitle>
                {summary.high_availability
                  ? t("dashboard.highAvailability")
                  : t("dashboard.notHighlyAvailable")}
              </AlertTitle>
              {!summary.high_availability && (
                <AlertDescription>{t("dashboard.notHighlyAvailableHelp")}</AlertDescription>
              )}
            </Alert>
          )}

          <div className="grid gap-6 lg:grid-cols-2">
            <Section
              title={t("nav.projects")}
              description={t("projects.emptyHelp")}
              actions={
                projectItems.length > 0 && (
                  <Button variant="ghost" size="sm" asChild>
                    <Link to="/projects">
                      {t("common.viewAll")}
                      <ArrowRightIcon />
                    </Link>
                  </Button>
                )
              }
            >
              {projects.isLoading ? (
                <Skeleton className="h-32" />
              ) : projectItems.length === 0 ? (
                <Empty className="border border-dashed">
                  <EmptyHeader>
                    <EmptyMedia variant="icon">
                      <FolderIcon />
                    </EmptyMedia>
                    <EmptyTitle>{t("projects.empty")}</EmptyTitle>
                    <EmptyDescription>{t("projects.emptyHelp")}</EmptyDescription>
                  </EmptyHeader>
                  <EmptyContent>
                    <Button size="sm" asChild>
                      <Link to="/projects">
                        <PlusIcon />
                        {t("projects.newProject")}
                      </Link>
                    </Button>
                  </EmptyContent>
                </Empty>
              ) : (
                <ItemGroup className="rounded-lg border">
                  {projectItems.slice(0, 5).map((project) => (
                    <Item key={project.id} asChild>
                      <Link to={`/projects/${project.id}`}>
                        <ItemMedia variant="icon">
                          <FolderIcon />
                        </ItemMedia>
                        <ItemContent>
                          <ItemTitle>{project.name}</ItemTitle>
                          {project.description && (
                            <ItemDescription>{project.description}</ItemDescription>
                          )}
                        </ItemContent>
                        <ItemActions className="text-xs text-muted-foreground">
                          {formatRelative(project.created_at)}
                        </ItemActions>
                      </Link>
                    </Item>
                  ))}
                </ItemGroup>
              )}
            </Section>

            <Section
              title={t("nav.servers")}
              description={t("servers.requirementsList")}
              actions={
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/servers">
                    {t("common.viewAll")}
                    <ArrowRightIcon />
                  </Link>
                </Button>
              }
            >
              {servers.isLoading ? (
                <Skeleton className="h-32" />
              ) : (
                <ItemGroup className="rounded-lg border">
                  {serverItems.slice(0, 5).map((server) => (
                    <Item key={server.id} asChild>
                      <Link to={`/servers/${server.id}`}>
                        <ItemMedia variant="icon">
                          <ServerIcon />
                        </ItemMedia>
                        <ItemContent>
                          <ItemTitle>{server.name}</ItemTitle>
                          <ItemDescription className="font-mono">{server.host}</ItemDescription>
                        </ItemContent>
                        <ItemActions>
                          <StatusBadge
                            status={server.status}
                            label={t(`servers.status.${server.status}`, {
                              defaultValue: server.status,
                            })}
                          />
                        </ItemActions>
                      </Link>
                    </Item>
                  ))}
                </ItemGroup>
              )}
            </Section>
          </div>

          {user?.is_admin && (audit.data?.items.length ?? 0) > 0 && (
            <Section
              title={t("dashboard.recentActivity")}
              actions={
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/activity">
                    {t("common.viewAll")}
                    <ArrowRightIcon />
                  </Link>
                </Button>
              }
            >
              <ItemGroup className="rounded-lg border">
                {audit.data?.items.slice(0, 6).map((event) => (
                  <Item key={event.id} size="sm">
                    <ItemMedia variant="icon">
                      <ActivityIcon />
                    </ItemMedia>
                    <ItemContent>
                      <ItemTitle className="font-mono text-xs font-normal">
                        {event.action}
                      </ItemTitle>
                      <ItemDescription>
                        {event.target_label || event.target_type} · {event.actor_label}
                      </ItemDescription>
                    </ItemContent>
                    <ItemActions className="text-xs text-muted-foreground">
                      {formatRelative(event.at)}
                    </ItemActions>
                  </Item>
                ))}
              </ItemGroup>
            </Section>
          )}
        </>
      )}
    </Page>
  )
}

/**
 * What a brand new install sees.
 *
 * Not an empty dashboard with zeroes in it: the only useful thing on this
 * screen is the first step, so that is the whole screen.
 */
function FirstRun() {
  const { t } = useTranslation()
  const { meta } = useSession()

  return (
    <Empty className="border border-dashed py-16">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <ServerIcon />
        </EmptyMedia>
        <EmptyTitle>{t("dashboard.noServers")}</EmptyTitle>
        <EmptyDescription>
          {t("dashboard.noServersHelp", { product: meta?.product ?? "Skifity" })}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button size="lg" asChild>
          <Link to="/servers/new">
            <PlusIcon />
            {t("dashboard.addFirstServer")}
          </Link>
        </Button>
        <div className="flex flex-wrap items-center justify-center gap-2 pt-2">
          <Button variant="ghost" size="sm" asChild>
            <Link to="/templates">
              <BoxesIcon />
              {t("templates.title")}
            </Link>
          </Button>
          <Button variant="ghost" size="sm" asChild>
            <a href="/docs/quick-start" target="_blank" rel="noreferrer">
              <RocketIcon />
              {t("nav.documentation")}
            </a>
          </Button>
        </div>
      </EmptyContent>
    </Empty>
  )
}

function Stat({
  icon: Icon,
  label,
  value,
  detail,
  to,
}: {
  icon: typeof ServerIcon
  label: string
  value: string | null
  detail?: string
  to?: string
}) {
  const body = (
    <Card className="transition-colors hover:border-primary/40">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Icon className="size-3.5" />
          {label}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex items-baseline gap-1.5">
        {value === null ? (
          <Skeleton className="h-8 w-12" />
        ) : (
          <span className="text-2xl font-semibold tabular-nums">{value}</span>
        )}
        {detail && <span className="text-sm text-muted-foreground">{detail}</span>}
      </CardContent>
    </Card>
  )
  return to ? <Link to={to}>{body}</Link> : body
}

function UsageStat({
  icon: Icon,
  label,
  used,
  total,
  render,
  loading,
}: {
  icon: typeof CpuIcon
  label: string
  used: number
  total: number
  render: (value: number) => string
  loading: boolean
}) {
  const percent = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Icon className="size-3.5" />
          {label}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {loading ? (
          <Skeleton className="h-8 w-20" />
        ) : (
          <>
            <div className="flex items-baseline gap-1.5">
              <span className="text-2xl font-semibold tabular-nums">{percent}%</span>
              <span className="text-xs text-muted-foreground tabular-nums">
                {render(used)} / {render(total)}
              </span>
            </div>
            <Progress value={percent} className="h-1.5" />
          </>
        )}
      </CardContent>
    </Card>
  )
}
