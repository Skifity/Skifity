import { useQuery } from "@tanstack/react-query"

import { api, type List } from "@/lib/api"
import type { DatabaseEngine } from "@/lib/types"

/**
 * The engines a database can be, from the panel's own catalogue: which
 * versions each is offered at, and whether the panel backs it up.
 *
 * The list changes only when the panel does, so it is asked for once. Nothing
 * here is decided in the interface: the form offers what the API accepts, and
 * a database's page says what the backup jobs do, because both read one list.
 */
export function useDatabaseEngines() {
  return useQuery({
    queryKey: ["database-engines"],
    queryFn: () => api.get<List<DatabaseEngine>>("/api/database-engines"),
    staleTime: Infinity,
    select: (data) => data.items,
  })
}
