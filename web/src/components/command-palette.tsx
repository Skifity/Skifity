import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query"
import {
  ActivityIcon,
  BoxesIcon,
  DatabaseIcon,
  FolderIcon,
  LayoutGridIcon,
  PlusIcon,
  PuzzleIcon,
  ServerIcon,
  SettingsIcon,
} from "lucide-react"

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { useFirstEnvironment } from "@/hooks/use-first-environment"
import { useProjects } from "@/hooks/use-projects"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { canOpenSettings, managesTeam, seesWholeTeam } from "@/lib/permissions"
import type { App, Environment, Project, Server } from "@/lib/types"

type Found = { app: App; project: Project; environment: Environment }

/**
 * Every app in these projects, with where it lives.
 *
 * Apps live under environments, and there is no list of a team's apps, so they
 * are gathered per project and per environment — all at once rather than one
 * request after another, and through the same cache entries the project page
 * fills, so a palette opened after visiting a project asks for little or
 * nothing again.
 */
async function gatherApps(client: QueryClient, projects: Project[]): Promise<Found[]> {
  const perProject = await Promise.all(
    projects.map(async (project) => {
      const environments = await client.fetchQuery({
        queryKey: ["environments", project.id],
        queryFn: () => api.get<List<Environment>>(`/api/projects/${project.id}/environments`),
        staleTime: 10_000,
      })
      const perEnvironment = await Promise.all(
        environments.items.map(async (environment) => {
          const apps = await client.fetchQuery({
            queryKey: ["apps", environment.id],
            queryFn: () => api.get<List<App>>(`/api/environments/${environment.id}/apps`),
            staleTime: 10_000,
          })
          return apps.items.map((app) => ({ app, project, environment }))
        }),
      )
      return perEnvironment.flat()
    }),
  )
  return perProject.flat()
}

/**
 * Command palette.
 *
 * It searches what the user actually navigates between: apps, projects and
 * servers. Loading those lists only while the palette is open keeps the cost
 * where it belongs. What it offers follows the same rules as the sidebar, so
 * nothing here leads to a page that refuses the person who picked it.
 */
export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const client = useQueryClient()
  const { team, user } = useSession()
  const teamWide = seesWholeTeam(team)

  // The sidebar's own queries, so the palette shares their cache.
  const { data: projects } = useProjects(open)
  const { data: servers } = useQuery({
    queryKey: ["servers", team?.id],
    queryFn: () => api.get<List<Server>>(`/api/teams/${team!.id}/servers`),
    // Servers belong to the whole team; a member limited to projects is
    // refused them.
    enabled: open && Boolean(team) && teamWide,
  })

  const projectIds = projects?.items.map((project) => project.id).join(",") ?? ""
  const { data: apps } = useQuery({
    queryKey: ["palette", "apps", team?.id, projectIds],
    queryFn: () => gatherApps(client, projects?.items ?? []),
    enabled: open && projectIds !== "",
  })

  const firstEnvironment = useFirstEnvironment(open)

  const go = (path: string) => {
    onOpenChange(false)
    navigate(path)
  }

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("nav.commandPalette")}
      description={t("nav.commandPaletteHelp")}
    >
      <CommandInput placeholder={t("nav.commandPalette")} />
      <CommandList>
        <CommandEmpty>{t("common.none")}</CommandEmpty>

        <CommandGroup heading={t("nav.overview")}>
          <CommandItem onSelect={() => go("/")}>
            <LayoutGridIcon className="size-4" />
            {t("nav.overview")}
          </CommandItem>
          <CommandItem onSelect={() => go("/projects")}>
            <FolderIcon className="size-4" />
            {t("nav.projects")}
          </CommandItem>
          {teamWide && (
            <CommandItem onSelect={() => go("/servers")}>
              <ServerIcon className="size-4" />
              {t("nav.servers")}
            </CommandItem>
          )}
          <CommandItem onSelect={() => go("/databases")}>
            <DatabaseIcon className="size-4" />
            {t("nav.databases")}
          </CommandItem>
          <CommandItem onSelect={() => go("/templates")}>
            <BoxesIcon className="size-4" />
            {t("nav.templates")}
          </CommandItem>
          {user?.is_admin && (
            <CommandItem onSelect={() => go("/plugins")}>
              <PuzzleIcon className="size-4" />
              {t("nav.plugins")}
            </CommandItem>
          )}
          {managesTeam(team) && (
            <CommandItem onSelect={() => go("/activity")}>
              <ActivityIcon className="size-4" />
              {t("nav.activity")}
            </CommandItem>
          )}
          {canOpenSettings(user, team) && (
            <CommandItem onSelect={() => go("/settings")}>
              <SettingsIcon className="size-4" />
              {t("nav.settings")}
            </CommandItem>
          )}
        </CommandGroup>

        {(apps?.length ?? 0) > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading={t("nav.apps")}>
              {apps?.map(({ app, project, environment }) => (
                <CommandItem
                  key={app.id}
                  // The palette selects by value, so it has to tell apart two
                  // apps of the same name: "web" in production and in staging
                  // highlighted together and trapped the arrow keys.
                  value={`${app.name} ${project.name} ${environment.name}`}
                  onSelect={() => go(`/apps/${app.id}`)}
                >
                  <LayoutGridIcon className="size-4" />
                  <span className="truncate">{app.name}</span>
                  <span className="ml-auto flex shrink-0 gap-1 text-xs text-muted-foreground">
                    <span>{project.name}</span>
                    <span aria-hidden>·</span>
                    <span>{environment.name}</span>
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {(projects?.items.length ?? 0) > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading={t("nav.projects")}>
              {projects?.items.map((project) => (
                <CommandItem
                  key={project.id}
                  value={project.name}
                  onSelect={() => go(`/projects/${project.id}`)}
                >
                  <FolderIcon className="size-4" />
                  {project.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {teamWide && (servers?.items.length ?? 0) > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading={t("nav.servers")}>
              {servers?.items.map((server) => (
                <CommandItem
                  key={server.id}
                  value={`${server.name} ${server.host}`}
                  onSelect={() => go(`/servers/${server.id}`)}
                >
                  <ServerIcon className="size-4" />
                  <span>{server.name}</span>
                  <span className="ml-auto text-xs text-muted-foreground">{server.host}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        <CommandSeparator />
        <CommandGroup heading={t("common.actions")}>
          {firstEnvironment && (
            <CommandItem onSelect={() => go(`/environments/${firstEnvironment}/apps/new`)}>
              <PlusIcon />
              {t("apps.newApp")}
            </CommandItem>
          )}
          {teamWide && (
            <CommandItem onSelect={() => go("/servers/new")}>
              <PlusIcon className="size-4" />
              {t("servers.addServer")}
            </CommandItem>
          )}
          <CommandItem onSelect={() => go("/projects?new=1")}>
            <PlusIcon className="size-4" />
            {t("projects.newProject")}
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
