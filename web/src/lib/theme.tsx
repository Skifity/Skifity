import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react"

/**
 * Theme handling.
 *
 * The choice is stored per browser and applied before the first paint by an
 * inline script in index.html, so a dark-mode user never sees a white flash.
 * When the user is signed in, their choice is also saved to their account, so
 * it follows them to another machine.
 */

export type Theme = "light" | "dark" | "system"

const STORAGE_KEY = "skifity-theme"

type ThemeContextValue = {
  theme: Theme
  /** The theme actually in effect, with "system" resolved. */
  resolved: "light" | "dark"
  setTheme: (theme: Theme) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

function readStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === "light" || stored === "dark" || stored === "system") return stored
  } catch {
    // Private browsing, or storage blocked. The default is fine.
  }
  return "system"
}

function systemTheme(): "light" | "dark" {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

/**
 * Subscribes to the operating system's dark-mode preference.
 *
 * This is an external store in React's sense, so it is read through
 * useSyncExternalStore rather than copied into state by an effect: the panel
 * then follows the system switching to dark in the evening without a render
 * pass that briefly shows the old theme.
 */
function subscribeToSystemTheme(onChange: () => void) {
  const media = window.matchMedia("(prefers-color-scheme: dark)")
  media.addEventListener("change", onChange)
  return () => media.removeEventListener("change", onChange)
}

/** Server-rendering is not used, but useSyncExternalStore requires a snapshot. */
function systemThemeFallback(): "light" | "dark" {
  return "light"
}

export function ThemeProvider({
  children,
  onChange,
}: {
  children: React.ReactNode
  /** Called when the user picks a theme, so it can be saved to their account. */
  onChange?: (theme: Theme) => void
}) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme)
  const system = useSyncExternalStore(subscribeToSystemTheme, systemTheme, systemThemeFallback)
  const resolved = theme === "system" ? system : theme

  // The only thing left for an effect to do is tell the document, which is an
  // external system.
  useEffect(() => {
    document.documentElement.classList.toggle("dark", resolved === "dark")
    document.documentElement.style.colorScheme = resolved
  }, [resolved])

  const setTheme = useCallback(
    (next: Theme) => {
      setThemeState(next)
      try {
        localStorage.setItem(STORAGE_KEY, next)
      } catch {
        // Not being able to remember the choice is not a reason to refuse it.
      }
      onChange?.(next)
    },
    [onChange],
  )

  const value = useMemo(() => ({ theme, resolved, setTheme }), [theme, resolved, setTheme])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext)
  if (!context) {
    // The toaster renders outside the provider in tests; a sensible default is
    // better than throwing.
    return { theme: "system", resolved: "light", setTheme: () => {} }
  }
  return context
}
