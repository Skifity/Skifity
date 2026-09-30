import { useSyncExternalStore } from "react"

const MOBILE_BREAKPOINT = 768

/**
 * Whether the viewport is phone-sized.
 *
 * The viewport is an external store in React's sense, so it is read through
 * useSyncExternalStore rather than copied into state by an effect: shadcn's
 * generated version renders once at the wrong width and then corrects itself,
 * which shows as the sidebar flashing open on a phone.
 */
const query = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`

function subscribe(onChange: () => void) {
  const media = window.matchMedia(query)
  media.addEventListener("change", onChange)
  return () => media.removeEventListener("change", onChange)
}

function snapshot(): boolean {
  return window.matchMedia(query).matches
}

/** Server-rendering is not used, but useSyncExternalStore requires a snapshot. */
function serverSnapshot(): boolean {
  return false
}

export function useIsMobile(): boolean {
  return useSyncExternalStore(subscribe, snapshot, serverSnapshot)
}
