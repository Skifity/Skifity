import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import {
  ActivityIcon,
  BoxesIcon,
  DatabaseIcon,
  FolderIcon,
  LayoutGridIcon,
  PlusIcon,
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
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import type { App, Project, Server } from "@/lib/types"

/**
 * Command palette.
 *
 * It searches what the user actually navigates between: apps, projects and
 * servers. Loading those lists only while the palette is open keeps the cost
 * where it belongs.
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
  const { team } = useSession()

  const { data: projects } = useQuery({
    queryKey: ["palette", "projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: open && Boolean(team),
  })

  const { data: servers } = useQuery({
    queryKey: ["palette", "servers", team?.id],
    queryFn: () => api.get<List<Server>>(`/api/teams/${team!.id}/servers`),
    enabled: open && Boolean(team),
  })

  const { data: apps } = useQuery({
    queryKey: ["palette", "apps", team?.id, projects?.items.map((p) => p.id).join(",")],
    queryFn: async () => {
      // Apps live under environments, so they are gathered per project. The
      // palette only opens on demand, so a handful of requests is acceptable.
      const all: { app: App; project: Project }[] = []
      for (const project of projects?.items ?? []) {
        const environments = await api.get<List<{ id: string }>>(
          `/api/projects/${project.id}/environments`,
        )
        for (const environment of environments.items) {
          const found = await api.get<List<App>>(`/api/environments/${environment.id}/apps`)
          for (const app of found.items) all.push({ app, project })
        }
      }
      return all
    },
    enabled: open && Boolean(projects?.items.length),
  })

  const firstEnvironment = useFirstEnvironment(open)

  const go = (path: string) => {
    onOpenChange(false)
    navigate(path)
  }

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title={t("nav.commandPalette")}>
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
          <CommandItem onSelect={() => go("/servers")}>
            <ServerIcon className="size-4" />
            {t("nav.servers")}
          </CommandItem>
          <CommandItem onSelect={() => go("/databases")}>
            <DatabaseIcon className="size-4" />
            {t("nav.databases")}
          </CommandItem>
          <CommandItem onSelect={() => go("/templates")}>
            <BoxesIcon className="size-4" />
            {t("nav.templates")}
          </CommandItem>
          <CommandItem onSelect={() => go("/activity")}>
            <ActivityIcon className="size-4" />
            {t("nav.activity")}
          </CommandItem>
          <CommandItem onSelect={() => go("/settings")}>
            <SettingsIcon className="size-4" />
            {t("nav.settings")}
          </CommandItem>
        </CommandGroup>

        {(apps?.length ?? 0) > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading={t("nav.apps")}>
              {apps?.map(({ app, project }) => (
                <CommandItem
                  key={app.id}
                  value={`${app.name} ${project.name}`}
                  onSelect={() => go(`/apps/${app.id}`)}
                >
                  <LayoutGridIcon className="size-4" />
                  <span>{app.name}</span>
                  <span className="ml-auto text-xs text-muted-foreground">{project.name}</span>
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

        {(servers?.items.length ?? 0) > 0 && (
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
          <CommandItem onSelect={() => go("/servers/new")}>
            <PlusIcon className="size-4" />
            {t("servers.addServer")}
          </CommandItem>
          <CommandItem onSelect={() => go("/projects?new=1")}>
            <PlusIcon className="size-4" />
            {t("projects.newProject")}
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
