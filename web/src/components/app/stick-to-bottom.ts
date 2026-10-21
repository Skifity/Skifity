import { useLayoutEffect, useRef, type UIEvent } from "react"

/** How close to the end still counts as "at the end", for a line that wraps. */
const SLACK = 24

/**
 * Keeps a ScrollArea of output at its end while it grows, for as long as the
 * reader is there.
 *
 * Only the ScrollArea's own viewport moves. scrollIntoView, which this
 * replaced, scrolls every scrollable ancestor as well: a build printing a line
 * a second pulled the whole page back down to the log, and somebody who had
 * scrolled up inside it to read an earlier error was put back at the bottom on
 * every line. Scrolling up now lets go, and scrolling back to the end takes
 * hold again.
 *
 * Spread the result onto the ScrollArea. `content` is whatever changes when a
 * line arrives; `enabled` is false while following is paused.
 */
export function useStickToBottom(content: unknown, enabled = true) {
  const ref = useRef<HTMLDivElement>(null)
  const stuck = useRef(true)
  const wasEnabled = useRef(enabled)

  useLayoutEffect(() => {
    // Resuming means "take me to the end", wherever the reader had scrolled.
    if (enabled && !wasEnabled.current) stuck.current = true
    wasEnabled.current = enabled
    if (!enabled || !stuck.current) return
    const viewport = ref.current?.querySelector<HTMLElement>('[data-slot="scroll-area-viewport"]')
    if (viewport) viewport.scrollTop = viewport.scrollHeight
  }, [content, enabled])

  // Scroll events do not bubble, but they are captured on the way down, so
  // the root hears its viewport's without reaching inside the component.
  const onScrollCapture = (event: UIEvent<HTMLDivElement>) => {
    const viewport = event.target as HTMLElement
    if (viewport.dataset.slot !== "scroll-area-viewport") return
    stuck.current = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= SLACK
  }

  return { ref, onScrollCapture }
}
