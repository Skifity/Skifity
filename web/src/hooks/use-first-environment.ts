import { useQuery } from "@tanstack/react-query"

import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import type { Environment, Project } from "@/lib/types"

/**
 * Where a "New app" button should land.
 *
 * Creating an app only existed inside a project's page, so the quick start
 * told people to press a button that is not on the screen they are standing
 * on — and somebody with one project has to click through two lists to reach
 * a form that takes one field. This is the first project's first environment,
 * which is the one first-run setup creates and the one they mean.
 *
 * It returns an empty string until it knows, and stays empty on a panel with no
 * projects, so a caller can simply not render the button.
 */
export function useFirstEnvironment(enabled = true): string {
  const { team } = useSession()

  // The same query keys the pages use, so this shares their cache rather than
  // asking again.
  const projects = useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: enabled && Boolean(team),
  })
  const first = projects.data?.items[0]

  const environments = useQuery({
    queryKey: ["environments", first?.id],
    queryFn: () => api.get<List<Environment>>(`/api/projects/${first!.id}/environments`),
    enabled: enabled && Boolean(first),
  })

  return environments.data?.items[0]?.id ?? ""
}
