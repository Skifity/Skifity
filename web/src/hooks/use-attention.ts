import { useQuery } from "@tanstack/react-query"

import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { seesWholeTeam } from "@/lib/permissions"
import { queryClient } from "@/lib/query"
import type { AttentionItem } from "@/lib/types"

/**
 * A member limited to some projects is refused the team's event stream, so
 * their list is asked for again on a clock instead.
 */
const SCOPED_REFRESH_MS = 30_000

/**
 * Even with the stream, a backup can become missed and a certificate can come
 * within its last days without anything happening that the stream would
 * carry: the clock is the event. Once a minute catches those.
 */
const CLOCK_REFRESH_MS = 60_000

/**
 * The team's events after which the list may have changed. "audit" is every
 * change somebody made through the panel — a notification channel added, a
 * certificate uploaded — and carries nothing but the fact.
 */
const CHANGES = [
  "deployment",
  "app",
  "app.created",
  "app.deleted",
  "project.deleted",
  "scan",
  "database",
  "backups",
  "operation",
  "server",
  "domain",
  "drift",
  "audit",
]

/**
 * What needs attention across the team in view: the overview's first section
 * and the header's bell, from one query.
 */
export function useAttention() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["attention", team?.id],
    queryFn: () => api.get<List<AttentionItem>>(`/api/teams/${team!.id}/attention`),
    enabled: Boolean(team),
    refetchInterval: team?.scoped ? SCOPED_REFRESH_MS : CLOCK_REFRESH_MS,
  })
}

/**
 * Keeps the list current from the team's event stream. Called once, by the
 * shell, which is always there; the pages that show the list share its query.
 */
export function useAttentionUpdates() {
  const { team } = useSession()
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["attention", team?.id] })
  useEvents(
    team ? [`team:${team.id}`] : [],
    Object.fromEntries(CHANGES.map((name) => [name, refresh])),
    Boolean(team) && seesWholeTeam(team),
  )
}
