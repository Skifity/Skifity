import { useEffect, useRef } from "react"

import { subscribe } from "@/lib/events"

/**
 * Subscribes to the panel's event stream for as long as a component is mounted.
 *
 * Every subscriber in the tab shares one stream (see lib/events.ts), which
 * also comes back by itself after the panel restarts, and refetches what may
 * have changed meanwhile.
 *
 * The handlers are kept in a ref so that a component re-rendering does not tear
 * the subscription down and set it up again: a build log that resubscribes on
 * every new line is worse than no live updates at all.
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
  // contents must not resubscribe.
  const key = topics.filter(Boolean).sort().join(",")

  useEffect(() => {
    if (!enabled || !key) return

    const wrapped: Record<string, (data: unknown) => void> = {}
    for (const name of Object.keys(handlersRef.current)) {
      wrapped[name] = (data) => handlersRef.current[name]?.(data)
    }
    return subscribe(key.split(","), wrapped)
  }, [key, enabled])
}
