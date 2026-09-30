import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQueries, useQuery } from "@tanstack/react-query"
import { ChevronDownIcon, DatabaseIcon, PlusIcon, SearchIcon } from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { NewDatabaseDialog } from "@/pages/project-detail"
import { StatusBadge } from "@/components/status-badge"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import { formatRelative } from "@/lib/format"
import type { Database, Environment, Project } from "@/lib/types"

/**
 * Every database in the team, whichever project it belongs to.
 *
 * Databases live in an environment, but people look for "my database" without
 * remembering which project that was, so the panel gathers them here and says
 * where each one lives.
 */
export function DatabasesPage() {
  const { t } = useTranslation()
  const { team } = useSession()
  const [creating, setCreating] = useState<string | null>(null)
  const [search, setSearch] = useState("")

  const projects = useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: Boolean(team),
  })

  // This page had neither a subscription nor a poll, and a database takes
  // minutes to come up: it was created, the list said "creating", and it went
  // on saying that until somebody reloaded. The server has been publishing
  // "database" the whole time.
  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      database: () => void queryClient.invalidateQueries({ queryKey: ["databases"] }),
    },
    Boolean(team),
  )

  const projectItems = useMemo(() => projects.data?.items ?? [], [projects.data])

  const environmentQueries = useQueries({
    queries: projectItems.map((project) => ({
      queryKey: ["environments", project.id],
      queryFn: () => api.get<List<Environment>>(`/api/projects/${project.id}/environments`),
    })),
  })

  const environments = useMemo(() => {
    const out: { environment: Environment; project: Project }[] = []
    environmentQueries.forEach((query, index) => {
      for (const environment of query.data?.items ?? []) {
        out.push({ environment, project: projectItems[index] })
      }
    })
    return out
  }, [environmentQueries, projectItems])

  const databaseQueries = useQueries({
    queries: environments.map(({ environment }) => ({
      queryKey: ["databases", environment.id],
      queryFn: () => api.get<List<Database>>(`/api/environments/${environment.id}/databases`),
    })),
  })

  const rows = useMemo(() => {
    const out: { database: Database; environment: Environment; project: Project }[] = []
    databaseQueries.forEach((query, index) => {
      for (const database of query.data?.items ?? []) {
        out.push({ database, ...environments[index] })
      }
    })
    return out
  }, [databaseQueries, environments])

  const loading =
    projects.isLoading ||
    environmentQueries.some((query) => query.isLoading) ||
    databaseQueries.some((query) => query.isLoading)

  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase()
    if (!needle) return rows
    return rows.filter(
      ({ database, environment, project }) =>
        database.name.toLowerCase().includes(needle) ||
        database.engine.toLowerCase().includes(needle) ||
        project.name.toLowerCase().includes(needle) ||
        environment.name.toLowerCase().includes(needle),
    )
  }, [rows, search])

  /**
   * A database belongs to an environment, so creating one is a choice of
   * where. With one environment there is nothing to choose and the button
   * just opens the dialog; with several it opens a menu that says which
   * project each belongs to.
   */
  const newDatabaseButton =
    environments.length === 1 ? (
      <Button onClick={() => setCreating(environments[0].environment.id)}>
        <PlusIcon />
        {t("databases.newDatabase")}
      </Button>
    ) : (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button>
            <PlusIcon />
            {t("databases.newDatabase")}
            <ChevronDownIcon className="opacity-60" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-64">
          <DropdownMenuLabel>{t("projects.environments")}</DropdownMenuLabel>
          {environments.map(({ environment, project }) => (
            <DropdownMenuItem
              key={environment.id}
              onSelect={() => setCreating(environment.id)}
              className="flex-col items-start gap-0"
            >
              <span>{environment.name}</span>
              <span className="text-xs text-muted-foreground">{project.name}</span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    )

  if (projects.error) {
    return <ErrorDisplay error={projects.error} onRetry={() => void projects.refetch()} />
  }

  return (
    <Page>
      <PageHeader
        title={t("databases.title")}
        description={t("databases.subtitle")}
        actions={environments.length > 0 && newDatabaseButton}
      />

      {loading ? (
        <Skeleton className="h-48" />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={DatabaseIcon}
          title={t("databases.empty")}
          description={
            projectItems.length === 0 ? t("projects.emptyHelp") : t("databases.emptyHelp")
          }
          action={
            projectItems.length === 0 ? (
              <Button asChild>
                <Link to="/projects">
                  <PlusIcon className="size-4" />
                  {t("projects.newProject")}
                </Link>
              </Button>
            ) : (
              // The same control as the header, not a second one that behaves
              // differently: with several environments the header asks where
              // and this used to pick the first, and with none it set null,
              // which is a button that does nothing at all.
              newDatabaseButton
            )
          }
        />
      ) : (
        <>
          {rows.length > 6 && (
            <div className="flex flex-wrap items-center gap-2">
              <InputGroup className="max-w-xs">
                <InputGroupAddon>
                  <SearchIcon />
                </InputGroupAddon>
                <InputGroupInput
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder={t("common.search")}
                />
              </InputGroup>
              <span className="ml-auto text-xs text-muted-foreground tabular-nums">
                {shown.length} / {rows.length}
              </span>
            </div>
          )}

          <Card className="overflow-hidden py-0">
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("common.name")}</TableHead>
                    <TableHead>{t("databases.engine")}</TableHead>
                    <TableHead className="hidden sm:table-cell">{t("nav.projects")}</TableHead>
                    <TableHead>{t("common.status")}</TableHead>
                    <TableHead className="hidden md:table-cell">{t("databases.storage")}</TableHead>
                    <TableHead className="hidden lg:table-cell">{t("common.created")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.length === 0 ? (
                    <TableRow className="hover:bg-transparent">
                      <TableCell colSpan={6} className="p-0">
                        <EmptyState
                          bordered={false}
                          icon={SearchIcon}
                          title={t("common.noMatches")}
                          description={t("common.noMatchesHelp")}
                        />
                      </TableCell>
                    </TableRow>
                  ) : (
                    shown.map(({ database, environment, project }) => (
                      <TableRow key={database.id} className="group">
                        <TableCell>
                          <Link
                            to={`/databases/${database.id}`}
                            className="block font-medium group-hover:text-primary"
                          >
                            {database.name}
                          </Link>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline" className="font-mono text-[10px]">
                            {database.engine} {database.engine_version}
                          </Badge>
                        </TableCell>
                        <TableCell className="hidden text-xs text-muted-foreground sm:table-cell">
                          {project.name} · {environment.name}
                        </TableCell>
                        <TableCell>
                          <StatusBadge
                            status={database.status}
                            label={t(`databases.status.${database.status}`, {
                              defaultValue: database.status,
                            })}
                          />
                        </TableCell>
                        <TableCell className="hidden tabular-nums md:table-cell">
                          {database.storage_gb} GB
                        </TableCell>
                        <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">
                          {formatRelative(database.created_at)}
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      )}

      {creating && (
        <NewDatabaseDialog
          environmentId={creating}
          open
          onOpenChange={(open) => {
            if (!open) setCreating(null)
          }}
        />
      )}
    </Page>
  )
}
