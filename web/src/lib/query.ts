import { QueryClient } from "@tanstack/react-query"

import { ApiError } from "@/lib/api"

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
        if (error instanceof ApiError) {
          if (error.status < 500 && error.status !== 429) return false
        }
        return failureCount < 2
      },
    },
    mutations: {
      // A mutation that failed is a decision for the user, not something to
      // repeat silently.
      retry: false,
    },
  },
})
