import { useMemo, useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BoxIcon,
  CopyIcon,
  DatabaseIcon,
  ExternalLinkIcon,
  FolderIcon,
  LayersIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  RocketIcon,
  Trash2Icon,
} from "lucide-react"

import { CriticalBadge } from "@/components/app/security-tab"
import { EmptyState } from "@/components/empty-state"
import { CloneEnvironmentDialog } from "@/components/environment-clone"
import { EnvironmentConfinement } from "@/components/environment-confinement"
import { EnvironmentPreviews } from "@/components/environment-previews"
import { EnvironmentQuota } from "@/components/environment-quota"
import { useDeleteConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader, Section } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { VariablesEditor } from "@/components/variables-editor"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import { Textarea } from "@/components/ui/textarea"
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
import { useDatabaseEngines } from "@/hooks/use-database-engines"
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
  DatabaseEngineName,
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
  const [cloning, setCloning] = useState(false)
  const [editing, setEditing] = useState(false)
  // The API's own lines: a member may change a project's name, only an
  // admin may delete one of its environments.
  const canEdit = team?.role !== undefined && team.role !== "viewer"
  const isAdmin = team?.role === "owner" || team?.role === "admin"

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
      // The list says which app has something critical, from its newest scan.
      scan: () => void queryClient.invalidateQueries({ queryKey: ["apps"] }),
    },
    // A member limited to some projects is refused the team's stream — it
    // carries every project's events — so the lists below are asked again
    // instead; subscribing anyway left a database "creating" until a reload.
    Boolean(team) && !team?.scoped,
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

  // One environment and everything in it. The page goes back to the first
  // one left, because the one it showed is gone.
  const removeEnvironment = useMutation({
    mutationFn: (id: string) => api.delete(`/api/environments/${id}`),
    onSuccess: () => {
      setChosen(null)
      void queryClient.invalidateQueries({ queryKey: ["environments", projectId] })
      void queryClient.invalidateQueries({ queryKey: ["canvas", projectId] })
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
                {canEdit && project.data && (
                  <DropdownMenuItem onSelect={() => setEditing(true)}>
                    <PencilIcon className="size-4" />
                    {t("projects.edit")}
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem onSelect={() => setNewEnvironment(true)}>
                  <PlusIcon className="size-4" />
                  {t("projects.newEnvironment")}
                </DropdownMenuItem>
                {chosenEnvironment?.kind === "standard" && (
                  <DropdownMenuItem onSelect={() => setCloning(true)}>
                    <CopyIcon className="size-4" />
                    {t("environments.cloneAction", { name: chosenEnvironment.name })}
                  </DropdownMenuItem>
                )}
                {isAdmin && chosenEnvironment && (
                  <DropdownMenuItem
                    variant="destructive"
                    onSelect={() => {
                      const target = chosenEnvironment
                      void confirmDelete(
                        target.name,
                        t("environments.deleteConfirm"),
                        t("common.cannotBeUndone"),
                      ).then((yes) => {
                        if (yes) removeEnvironment.mutate(target.id)
                      })
                    }}
                  >
                    <Trash2Icon className="size-4" />
                    {t("environments.deleteAction", { name: chosenEnvironment.name })}
                  </DropdownMenuItem>
                )}
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
      {removeEnvironment.error != null && <ErrorDisplay error={removeEnvironment.error} />}

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

        <TabsContent value="services" className="space-y-8 pt-4">
          {/* A failed list is not an empty one. It used to leave the tab
              blank and take the New app button with it, with nothing said. */}
          {environments.error ? (
            <ErrorDisplay error={environments.error} onRetry={() => void environments.refetch()} />
          ) : environments.isLoading ? (
            <Skeleton className="h-40" />
          ) : chosenEnvironment ? (
            <>
              {/* What the page is about first; the environment's own settings
                  after it. They used to come first and pushed the apps below
                  the fold on every visit. */}
              <EnvironmentServices environmentId={chosenEnvironment.id} />
              <Section title={t("projects.environmentSettings", { name: chosenEnvironment.name })}>
                <div className="space-y-3">
                  <EnvironmentQuota environmentId={chosenEnvironment.id} />
                  <EnvironmentConfinement environment={chosenEnvironment} />
                  <EnvironmentPreviews environment={chosenEnvironment} />
                </div>
              </Section>
            </>
          ) : (
            <EmptyState
              icon={LayersIcon}
              title={t("projects.noEnvironments")}
              description={t("projects.newEnvironmentHelp")}
              action={
                <Button onClick={() => setNewEnvironment(true)}>
                  <PlusIcon />
                  {t("projects.newEnvironment")}
                </Button>
              }
            />
          )}
        </TabsContent>

        <TabsContent value="canvas" className="pt-4">
          <ProjectCanvas projectId={projectId} environments={items} />
        </TabsContent>

        <TabsContent value="variables" className="pt-4">
          <VariablesEditor
            base={`/api/projects/${projectId}`}
            queryKey={["shared-variables", projectId]}
            description={t("projects.sharedVariablesHelp")}
            projectId={projectId}
          />
        </TabsContent>
      </Tabs>

      <NewEnvironmentDialog
        projectId={projectId}
        open={newEnvironment}
        onOpenChange={setNewEnvironment}
        onCreated={(environment) => setEnvironmentId(environment.id)}
      />
      <CloneEnvironmentDialog
        environment={chosenEnvironment}
        open={cloning}
        onOpenChange={setCloning}
        onCloned={(environment) => setEnvironmentId(environment.id)}
      />
      {/* Mounted only while open, so it starts from the project as it is now
          rather than copying it into state when it changes. */}
      {editing && project.data && (
        <EditProjectDialog project={project.data} onClose={() => setEditing(false)} />
      )}
    </Page>
  )
}

function EnvironmentServices({ environmentId }: { environmentId: string }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const [newDatabase, setNewDatabase] = useState(false)
  // No live stream for a member limited to projects; see the page above.
  const poll = team?.scoped ? 15_000 : false

  const apps = useQuery({
    queryKey: ["apps", environmentId],
    queryFn: () => api.get<List<App>>(`/api/environments/${environmentId}/apps`),
    refetchInterval: poll,
  })
  const databases = useQuery({
    queryKey: ["databases", environmentId],
    queryFn: () => api.get<List<Database>>(`/api/environments/${environmentId}/databases`),
    refetchInterval: poll,
  })

  const appItems = apps.data?.items ?? []
  const databaseItems = databases.data?.items ?? []
  const noServices = appItems.length === 0 && databaseItems.length === 0

  if (apps.error) return <ErrorDisplay error={apps.error} onRetry={() => void apps.refetch()} />
  // Both lists, before deciding the environment is empty: the first-run
  // state flashed while the databases loaded, and stayed when they failed.
  if (apps.isLoading || databases.isLoading) return <Skeleton className="h-40" />

  if (noServices && !databases.error) {
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
        {databases.error ? (
          <ErrorDisplay error={databases.error} onRetry={() => void databases.refetch()} />
        ) : databaseItems.length === 0 ? (
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
              <Item key={database.id} asChild className="flex-nowrap">
                <Link to={`/databases/${database.id}`}>
                  <ItemMedia variant="icon">
                    <DatabaseIcon />
                  </ItemMedia>
                  <ItemContent className="min-w-0">
                    <ItemTitle>{database.name}</ItemTitle>
                    <ItemDescription className="font-mono break-all">
                      {database.engine} {database.engine_version}
                      {database.storage_gb > 0 && ` · ${database.storage_gb} GB`}
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
    // Not wrapped: a long image name pushed the status under the name on a
    // phone, on some rows and not others.
    <Item asChild className="flex-nowrap">
      <Link to={`/apps/${app.id}`}>
        <ItemMedia variant="icon">
          <BoxIcon />
        </ItemMedia>
        <ItemContent className="min-w-0">
          <ItemTitle>{app.name}</ItemTitle>
          <ItemDescription className="font-mono break-all">
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
          <CriticalBadge counts={app.vulnerabilities} />
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
function ProjectCanvas({
  projectId,
  environments: known,
}: {
  projectId: string
  environments: Environment[]
}) {
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
  // The canvas names an environment by its slug; the heading is its name, as
  // everywhere else on the page.
  const nameOf = (slug: string) => known.find((item) => item.slug === slug)?.name ?? slug

  return (
    <div className="space-y-8">
      <p className="text-sm text-muted-foreground">{t("projects.canvasHelp")}</p>
      {environments.map((environment) => (
        <section key={environment} className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground">{nameOf(environment)}</h2>
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
                        {/* A database's status words are not an app's: "creating"
                            fell through to the English it was sent as. */}
                        <StatusBadge
                          status={node.status}
                          label={t(
                            node.kind === "database"
                              ? `databases.status.${node.status}`
                              : `apps.phase.${node.status}`,
                            { defaultValue: node.status },
                          )}
                        />
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
          <DialogDescription>{t("projects.newEnvironmentHelp")}</DialogDescription>
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
              placeholder={t("environments.namePlaceholder")}
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

/**
 * A project's name and description. Nothing else about a project can change,
 * and nothing hangs off its name: apps keep their addresses.
 */
function EditProjectDialog({ project, onClose }: { project: Project; onClose: () => void }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const [name, setName] = useState(project.name)
  const [description, setDescription] = useState(project.description)

  const save = useMutation({
    mutationFn: () =>
      api.patch<Project>(`/api/projects/${project.id}`, {
        name: name.trim(),
        description: description.trim(),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["project", project.id] })
      void queryClient.invalidateQueries({ queryKey: ["projects", team?.id] })
      onClose()
    },
  })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("projects.edit")}</DialogTitle>
          <DialogDescription>{t("projects.editHelp")}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            save.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="edit-project-name">{t("projects.projectName")}</FieldLabel>
            <Input
              id="edit-project-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="edit-project-description">
              {t("projects.description")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Textarea
              id="edit-project-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={3}
            />
          </Field>
          {save.error != null && <ErrorDisplay error={save.error} compact />}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!name.trim() || save.isPending}>
              {save.isPending && <Spinner />}
              {save.isPending ? t("common.saving") : t("common.save")}
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
  const engines = useDatabaseEngines()
  const [name, setName] = useState("")
  const [engine, setEngine] = useState<DatabaseEngineName>("postgres")
  // null is "the chosen engine's default", worked out below rather than
  // copied in when the engine changes.
  const [version, setVersion] = useState<string | null>(null)
  const [storage, setStorage] = useState("10")

  const chosen = engines.data?.find((item) => item.name === engine)
  const versionValue = version ?? chosen?.default_version ?? ""
  // A cache keeps nothing on a disk, so it is asked for none.
  const keepsData = chosen?.storage ?? true

  const create = useMutation({
    mutationFn: () =>
      api.post<Database>(`/api/environments/${environmentId}/databases`, {
        name: name.trim(),
        engine,
        version: versionValue || undefined,
        storage_gb: keepsData ? Number(storage) || 10 : undefined,
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
          {engines.error ? (
            <ErrorDisplay error={engines.error} compact />
          ) : !engines.data ? (
            <Skeleton className="h-16" />
          ) : (
            <div className="grid gap-4 sm:grid-cols-[1fr_9rem]">
              <Field>
                <FieldLabel htmlFor="database-engine">{t("databases.engine")}</FieldLabel>
                <Select
                  value={engine}
                  onValueChange={(value) => {
                    setEngine(value as DatabaseEngineName)
                    setVersion(null)
                  }}
                >
                  <SelectTrigger id="database-engine">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {engines.data.map((item) => (
                      <SelectItem key={item.name} value={item.name}>
                        {t(`databases.${item.name}`, { defaultValue: item.title })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="database-version">{t("databases.version")}</FieldLabel>
                <Select value={versionValue} onValueChange={setVersion}>
                  <SelectTrigger id="database-version">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(chosen?.versions ?? []).map((item) => (
                      <SelectItem key={item} value={item}>
                        {item === chosen?.default_version
                          ? t("databases.versionDefault", { version: item })
                          : item}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {chosen && (
                <FieldDescription className="sm:col-span-2">
                  {t(`databases.engineHelp.${chosen.name}`)}
                </FieldDescription>
              )}
            </div>
          )}
          {chosen && !chosen.backups && (
            <Alert variant="info">
              <DatabaseIcon />
              <AlertTitle>
                {t("databases.noBackupsTitle", {
                  engine: t(`databases.${chosen.name}`, { defaultValue: chosen.title }),
                })}
              </AlertTitle>
              <AlertDescription>{t(`databases.noBackupsReason.${chosen.name}`)}</AlertDescription>
            </Alert>
          )}
          {keepsData && (
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
          )}
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
