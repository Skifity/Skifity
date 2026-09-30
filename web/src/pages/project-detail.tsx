import { useMemo, useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BoxIcon,
  DatabaseIcon,
  ExternalLinkIcon,
  FolderIcon,
  MoreHorizontalIcon,
  PlusIcon,
  RocketIcon,
  Trash2Icon,
} from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { EnvironmentConfinement } from "@/components/environment-confinement"
import { EnvironmentQuota } from "@/components/environment-quota"
import { useDeleteConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader, Section } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { VariablesEditor } from "@/components/variables-editor"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { repoName } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type {
  App,
  AppStatus,
  CanvasEdge,
  CanvasNode,
  Database,
  Environment,
  Project,
} from "@/lib/types"

export function ProjectDetailPage() {
  const { t } = useTranslation()
  const { projectId = "" } = useParams()
  const navigate = useNavigate()
  const confirmDelete = useDeleteConfirm()
  const { team } = useSession()
  const [chosen, setChosen] = useState<string | null>(null)
  const [newEnvironment, setNewEnvironment] = useState(false)

  const project = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.get<Project>(`/api/projects/${projectId}`),
  })

  // The page lists apps and databases, and both change underneath it: a deploy
  // finishing, an app created from the CLI, a database coming up. It had one
  // 30-second status poll per app row and nothing else.
  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      deployment: () => void queryClient.invalidateQueries({ queryKey: ["app-status"] }),
      app: () => void queryClient.invalidateQueries({ queryKey: ["app-status"] }),
      "app.created": () => void queryClient.invalidateQueries({ queryKey: ["apps"] }),
      "app.deleted": () => void queryClient.invalidateQueries({ queryKey: ["apps"] }),
      database: () => void queryClient.invalidateQueries({ queryKey: ["databases"] }),
    },
    Boolean(team),
  )

  const environments = useQuery({
    queryKey: ["environments", projectId],
    queryFn: () => api.get<List<Environment>>(`/api/projects/${projectId}/environments`),
  })

  const items = useMemo(() => environments.data?.items ?? [], [environments.data])

  // Which environment is shown is derived, not stored: the first one until the
  // user picks another, and back to the first if the one they picked is
  // deleted from under the page. Keeping it in state would need an effect to
  // repair it, and that effect would render the broken state first.
  const environmentId = items.some((environment) => environment.id === chosen)
    ? chosen!
    : (items[0]?.id ?? "")
  const setEnvironmentId = setChosen
  // Derived from the same list rather than fetched again: the row is already
  // here, and a second query would be a second source of truth for it.
  const chosenEnvironment = items.find((environment) => environment.id === environmentId)

  const remove = useMutation({
    mutationFn: () => api.delete(`/api/projects/${projectId}`),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["projects", team?.id] })
      navigate("/projects")
    },
  })

  if (project.error) {
    return <ErrorDisplay error={project.error} onRetry={() => void project.refetch()} />
  }

  return (
    <Page>
      <PageHeader
        icon={FolderIcon}
        loading={project.isLoading}
        title={project.data?.name}
        description={project.data?.description}
        back={
          <Button variant="ghost" size="sm" asChild className="-ml-2">
            <Link to="/projects">
              <ArrowLeftIcon />
              {t("projects.title")}
            </Link>
          </Button>
        }
        actions={
          <>
            {environmentId && (
              <Button asChild>
                <Link to={`/environments/${environmentId}/apps/new`}>
                  <PlusIcon className="size-4" />
                  {t("apps.newApp")}
                </Link>
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon" aria-label={t("common.actions")}>
                  <MoreHorizontalIcon className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setNewEnvironment(true)}>
                  <PlusIcon className="size-4" />
                  {t("projects.newEnvironment")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={() => {
                    void confirmDelete(
                      project.data?.name ?? "",
                      t("projects.deleteProjectWarning"),
                      t("common.cannotBeUndone"),
                    ).then((yes) => {
                      if (yes) remove.mutate()
                    })
                  }}
                >
                  <Trash2Icon className="size-4" />
                  {t("projects.deleteProject")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      {remove.error && <ErrorDisplay error={remove.error} />}

      {items.length > 1 && (
        <Field orientation="horizontal" className="w-fit">
          <FieldLabel htmlFor="environment" className="text-sm text-muted-foreground">
            {t("projects.environments")}
          </FieldLabel>
          <Select value={environmentId} onValueChange={setEnvironmentId}>
            <SelectTrigger id="environment" className="w-56">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {items.map((environment) => (
                <SelectItem key={environment.id} value={environment.id}>
                  {environment.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}

      <Tabs defaultValue="services">
        <TabsList>
          <TabsTrigger value="services">{t("nav.apps")}</TabsTrigger>
          <TabsTrigger value="canvas">{t("projects.canvas")}</TabsTrigger>
          <TabsTrigger value="variables">{t("projects.sharedVariables")}</TabsTrigger>
        </TabsList>

        <TabsContent value="services" className="space-y-6 pt-4">
          {environments.isLoading ? (
            <Skeleton className="h-40" />
          ) : environmentId ? (
            <>
              {chosenEnvironment && (
                <EnvironmentConfinement environment={chosenEnvironment} />
              )}
              <EnvironmentServices environmentId={environmentId} />
            </>
          ) : null}
        </TabsContent>

        <TabsContent value="canvas" className="pt-4">
          <ProjectCanvas projectId={projectId} />
        </TabsContent>

        <TabsContent value="variables" className="pt-4">
          <VariablesEditor
            base={`/api/projects/${projectId}`}
            queryKey={["shared-variables", projectId]}
            description={t("projects.sharedVariablesHelp")}
          />
        </TabsContent>
      </Tabs>

      <NewEnvironmentDialog
        projectId={projectId}
        open={newEnvironment}
        onOpenChange={setNewEnvironment}
        onCreated={(environment) => setEnvironmentId(environment.id)}
      />
    </Page>
  )
}

function EnvironmentServices({ environmentId }: { environmentId: string }) {
  const { t } = useTranslation()
  const [newDatabase, setNewDatabase] = useState(false)

  const apps = useQuery({
    queryKey: ["apps", environmentId],
    queryFn: () => api.get<List<App>>(`/api/environments/${environmentId}/apps`),
  })
  const databases = useQuery({
    queryKey: ["databases", environmentId],
    queryFn: () => api.get<List<Database>>(`/api/environments/${environmentId}/databases`),
  })

  const appItems = apps.data?.items ?? []
  const databaseItems = databases.data?.items ?? []
  const noServices = appItems.length === 0 && databaseItems.length === 0

  if (apps.error) return <ErrorDisplay error={apps.error} onRetry={() => void apps.refetch()} />
  if (apps.isLoading) return <Skeleton className="h-40" />

  if (noServices) {
    return (
      <>
        <EmptyState
          icon={RocketIcon}
          title={t("apps.empty")}
          description={t("apps.emptyHelp")}
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button asChild>
                <Link to={`/environments/${environmentId}/apps/new`}>
                  <PlusIcon />
                  {t("apps.newApp")}
                </Link>
              </Button>
              <Button variant="outline" onClick={() => setNewDatabase(true)}>
                <DatabaseIcon />
                {t("databases.newDatabase")}
              </Button>
              <Button variant="ghost" asChild>
                <Link to="/templates">{t("templates.title")}</Link>
              </Button>
            </div>
          }
        />
        <NewDatabaseDialog
          environmentId={environmentId}
          open={newDatabase}
          onOpenChange={setNewDatabase}
        />
      </>
    )
  }

  return (
    <div className="space-y-6">
      <EnvironmentQuota environmentId={environmentId} />
      {appItems.length > 0 && (
        <Section title={t("nav.apps")}>
          <ItemGroup className="rounded-lg border">
            {appItems.map((app) => (
              <AppRow key={app.id} app={app} />
            ))}
          </ItemGroup>
        </Section>
      )}

      <Section
        title={t("nav.databases")}
        actions={
          <Button variant="ghost" size="sm" onClick={() => setNewDatabase(true)}>
            <PlusIcon />
            {t("databases.newDatabase")}
          </Button>
        }
      >
        {databaseItems.length === 0 ? (
          <EmptyState
            icon={DatabaseIcon}
            title={t("databases.empty")}
            description={t("databases.emptyHelp")}
            action={
              <Button size="sm" onClick={() => setNewDatabase(true)}>
                <PlusIcon />
                {t("databases.newDatabase")}
              </Button>
            }
          />
        ) : (
          <ItemGroup className="rounded-lg border">
            {databaseItems.map((database) => (
              <Item key={database.id} asChild>
                <Link to={`/databases/${database.id}`}>
                  <ItemMedia variant="icon">
                    <DatabaseIcon />
                  </ItemMedia>
                  <ItemContent>
                    <ItemTitle>{database.name}</ItemTitle>
                    <ItemDescription className="font-mono">
                      {database.engine} {database.engine_version} · {database.storage_gb} GB
                    </ItemDescription>
                  </ItemContent>
                  <ItemActions>
                    <StatusBadge
                      status={database.status}
                      label={t(`databases.status.${database.status}`, {
                        defaultValue: database.status,
                      })}
                    />
                  </ItemActions>
                </Link>
              </Item>
            ))}
          </ItemGroup>
        )}
      </Section>

      <NewDatabaseDialog
        environmentId={environmentId}
        open={newDatabase}
        onOpenChange={setNewDatabase}
      />
    </div>
  )
}

/**
 * One app in the list.
 *
 * The row asks for the app's live state separately, because the app record
 * itself only knows what was last written to it: an app that crashed five
 * minutes ago still says "running" in the database.
 */
function AppRow({ app }: { app: App }) {
  const { t } = useTranslation()

  const status = useQuery({
    queryKey: ["app-status", app.id],
    queryFn: () => api.get<AppStatus>(`/api/apps/${app.id}/status`),
    refetchInterval: 30_000,
    retry: false,
  })

  const phase = status.data?.phase ?? app.status
  const url = status.data?.urls?.[0]

  return (
    <Item asChild>
      <Link to={`/apps/${app.id}`}>
        <ItemMedia variant="icon">
          <BoxIcon />
        </ItemMedia>
        <ItemContent>
          <ItemTitle>{app.name}</ItemTitle>
          <ItemDescription className="font-mono">
            {app.source_type === "image"
              ? app.image
              : app.source_type === "upload"
                ? t("apps.sourceUpload")
                : repoName(app.repo_url) || app.repo_url}
            {app.branch ? ` · ${app.branch}` : ""}
          </ItemDescription>
        </ItemContent>
        <ItemActions className="gap-3">
          {url && (
            <span className="hidden max-w-48 truncate text-xs text-muted-foreground lg:inline">
              {url.replace(/^https?:\/\//, "")}
            </span>
          )}
          {status.data && status.data.desired_replicas > 0 && (
            <span className="hidden text-xs text-muted-foreground tabular-nums sm:inline">
              {status.data.ready_replicas}/{status.data.desired_replicas}
            </span>
          )}
          <StatusBadge status={phase} label={t(`apps.phase.${phase}`, { defaultValue: phase })} />
        </ItemActions>
      </Link>
    </Item>
  )
}

/**
 * The project canvas: which apps talk to which databases.
 *
 * Deliberately not a draggable graph. What people actually need is to see the
 * shape of a project at a glance and click through, and a layout they cannot
 * accidentally rearrange stays readable on a phone.
 */
function ProjectCanvas({ projectId }: { projectId: string }) {
  const { t } = useTranslation()

  const canvas = useQuery({
    queryKey: ["canvas", projectId],
    queryFn: () =>
      api.get<{ nodes: CanvasNode[]; edges: CanvasEdge[] }>(`/api/projects/${projectId}/canvas`),
  })

  if (canvas.isLoading) return <Skeleton className="h-64" />
  if (canvas.error)
    return <ErrorDisplay error={canvas.error} onRetry={() => void canvas.refetch()} />

  const nodes = canvas.data?.nodes ?? []
  const edges = canvas.data?.edges ?? []
  if (nodes.length === 0) {
    return (
      <EmptyState icon={BoxIcon} title={t("apps.empty")} description={t("projects.canvasHelp")} />
    )
  }

  const byId = new Map(nodes.map((node) => [node.id, node]))
  const environments = [...new Set(nodes.map((node) => node.environment))]

  return (
    <div className="space-y-8">
      <p className="text-sm text-muted-foreground">{t("projects.canvasHelp")}</p>
      {environments.map((environment) => (
        <section key={environment} className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground">{environment}</h2>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {nodes
              .filter((node) => node.environment === environment)
              .map((node) => {
                const outgoing = edges.filter((edge) => edge.from === node.id)
                return (
                  <Card key={node.id}>
                    <CardHeader>
                      <div className="flex items-start justify-between gap-2">
                        <CardTitle className="flex min-w-0 items-center gap-2 text-sm">
                          {node.kind === "database" ? (
                            <DatabaseIcon className="size-4 shrink-0 text-muted-foreground" />
                          ) : (
                            <BoxIcon className="size-4 shrink-0 text-muted-foreground" />
                          )}
                          <Link
                            to={
                              node.kind === "database"
                                ? `/databases/${node.id}`
                                : `/apps/${node.id}`
                            }
                            className="truncate hover:text-primary"
                          >
                            {node.name}
                          </Link>
                        </CardTitle>
                        <StatusBadge status={node.status} />
                      </div>
                      {node.detail && (
                        <CardDescription className="font-mono text-xs">
                          {node.detail}
                        </CardDescription>
                      )}
                    </CardHeader>
                    {(outgoing.length > 0 || (node.urls?.length ?? 0) > 0) && (
                      <CardContent className="space-y-1.5 text-xs">
                        {node.urls?.map((url) => (
                          <a
                            key={url}
                            href={url}
                            target="_blank"
                            rel="noreferrer"
                            className="flex items-center gap-1.5 text-primary hover:underline"
                          >
                            <ExternalLinkIcon className="size-3 shrink-0" />
                            <span className="truncate">{url.replace(/^https?:\/\//, "")}</span>
                          </a>
                        ))}
                        {outgoing.map((edge) => (
                          <div
                            key={`${edge.from}-${edge.to}-${edge.label}`}
                            className="flex items-center gap-1.5 text-muted-foreground"
                          >
                            <ArrowRightIcon className="size-3 shrink-0" />
                            <span className="truncate">{byId.get(edge.to)?.name ?? edge.to}</span>
                            <Badge variant="outline" className="font-mono text-[10px]">
                              {edge.label}
                            </Badge>
                          </div>
                        ))}
                      </CardContent>
                    )}
                  </Card>
                )
              })}
          </div>
        </section>
      ))}
    </div>
  )
}

function NewEnvironmentDialog({
  projectId,
  open,
  onOpenChange,
  onCreated,
}: {
  projectId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (environment: Environment) => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState("")

  const create = useMutation({
    mutationFn: () =>
      api.post<Environment>(`/api/projects/${projectId}/environments`, { name: name.trim() }),
    onSuccess: (environment) => {
      void queryClient.invalidateQueries({ queryKey: ["environments", projectId] })
      onCreated(environment)
      setName("")
      onOpenChange(false)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("projects.newEnvironment")}</DialogTitle>
          <DialogDescription>{t("projects.sharedVariablesHelp")}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="environment-name">{t("projects.environmentName")}</FieldLabel>
            <Input
              id="environment-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="staging"
              autoFocus
              required
            />
          </Field>
          {create.error && <ErrorDisplay error={create.error} compact />}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending && <Spinner />}
              {create.isPending ? t("common.saving") : t("common.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function NewDatabaseDialog({
  environmentId,
  open,
  onOpenChange,
}: {
  environmentId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [name, setName] = useState("")
  const [engine, setEngine] = useState("postgres")
  const [storage, setStorage] = useState("10")

  const create = useMutation({
    mutationFn: () =>
      api.post<Database>(`/api/environments/${environmentId}/databases`, {
        name: name.trim(),
        engine,
        storage_gb: Number(storage) || 10,
      }),
    onSuccess: (database) => {
      void queryClient.invalidateQueries({ queryKey: ["databases", environmentId] })
      onOpenChange(false)
      navigate(`/databases/${database.id}`)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("databases.newDatabase")}</DialogTitle>
          <DialogDescription>{t("databases.emptyHelp")}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="database-name">{t("databases.databaseName")}</FieldLabel>
            <Input
              id="database-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="database-engine">{t("databases.engine")}</FieldLabel>
            <Select value={engine} onValueChange={setEngine}>
              <SelectTrigger id="database-engine">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="postgres">{t("databases.postgres")}</SelectItem>
                <SelectItem value="redis">{t("databases.redis")}</SelectItem>
                <SelectItem value="mysql">{t("databases.mysql")}</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor="database-storage">{t("databases.storage")}</FieldLabel>
            <Input
              id="database-storage"
              type="number"
              min={1}
              value={storage}
              onChange={(event) => setStorage(event.target.value)}
            />
            <FieldDescription>{t("databases.storageHelp")}</FieldDescription>
          </Field>
          {create.error && <ErrorDisplay error={create.error} compact />}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending && <Spinner />}
              {create.isPending ? t("common.saving") : t("common.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
