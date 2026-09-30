import { useEffect, useRef } from "react"

import { subscribe } from "@/lib/api"
import { queryClient } from "@/lib/query"

/**
 * Subscribes to the panel's event stream for as long as a component is mounted.
 *
 * The handlers are kept in a ref so that a component re-rendering does not tear
 * the stream down and reconnect: a build log that reconnects on every new line
 * is worse than no live updates at all.
 */
export function useEvents(
  topics: string[],
  handlers: Record<string, (data: unknown) => void>,
  enabled = true,
) {
  const handlersRef = useRef(handlers)
  // Updated after each render rather than during it: a ref written while
  // rendering is not something React guarantees anything about.
  useEffect(() => {
    handlersRef.current = handlers
  })

  // Joining the topics gives a stable dependency: a new array with the same
  // contents must not reconnect.
  const key = topics.filter(Boolean).sort().join(",")

  useEffect(() => {
    if (!enabled || !key) return

    const wrapped: Record<string, (data: unknown) => void> = {}
    for (const name of Object.keys(handlersRef.current)) {
      wrapped[name] = (data) => handlersRef.current[name]?.(data)
    }

    // The hub never blocks a publisher: a client that cannot keep up loses
    // events and is told so. A page that missed one cannot know what it missed,
    // so the only honest answer is to throw away what it has and ask again.
    wrapped.desync = () => {
      void queryClient.invalidateQueries()
    }

    return subscribe(key.split(","), wrapped)
  }, [key, enabled])
}
