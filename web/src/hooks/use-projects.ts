import { useQuery } from "@tanstack/react-query"

import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import type { Project } from "@/lib/types"

/**
 * The current team's projects.
 *
 * Asked for by the sidebar, the overview, the palette and half the pages, and
 * written out by hand in each of them. One hook means one query key, so they
 * all share one request and one cache entry, and invalidating
 * ["projects", team.id] reaches every one of them.
 */
export function useProjects(enabled = true) {
  const { team } = useSession()
  return useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: enabled && Boolean(team),
  })
}
