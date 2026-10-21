import { QueryClient } from "@tanstack/react-query"

import { ApiError } from "@/lib/api"

/** The longest a Retry-After is honoured for before the query gives up waiting. */
const MAX_RETRY_AFTER_MS = 60_000

/**
 * One query client for the whole panel.
 *
 * The defaults matter here: a cluster panel shows live state, so data goes
 * stale quickly, but refetching on every window focus would hammer a 1 GB
 * server with a dozen browser tabs open.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchOnWindowFocus: false,
      retry: (failureCount, error) => {
        // Retrying a 401 or a 403 achieves nothing except more 401s, and
        // retrying a 404 makes a deleted resource look like a network problem.
        // Status 0 is a request that never reached the panel, which is worth
        // another go.
        if (error instanceof ApiError && error.status !== 0) {
          if (error.status < 500 && error.status !== 429) return false
        }
        return failureCount < 2
      },
      // A 429 says how long to wait. Retrying on the usual one, two, four
      // seconds while the panel asked for ten only earns more 429s.
      retryDelay: (failureCount, error) => {
        if (error instanceof ApiError && error.retryAfterMs !== undefined) {
          return Math.min(error.retryAfterMs, MAX_RETRY_AFTER_MS)
        }
        return Math.min(1000 * 2 ** failureCount, 30_000)
      },
    },
    mutations: {
      // A mutation that failed is a decision for the user, not something to
      // repeat silently.
      retry: false,
    },
  },
})
