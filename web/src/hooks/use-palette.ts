import { useEffect, useState } from "react"

/**
 * The command palette's open state and its keyboard shortcut.
 *
 * Lives outside the palette component so the header button and the shortcut
 * drive the same state, and so the listener is registered once rather than by
 * whichever component happens to be mounted.
 */
export function usePaletteShortcut() {
  const [paletteOpen, setPaletteOpen] = useState(false)

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault()
        setPaletteOpen((open) => !open)
      }
    }
    document.addEventListener("keydown", onKeyDown)
    return () => document.removeEventListener("keydown", onKeyDown)
  }, [])

  return { paletteOpen, setPaletteOpen }
}
