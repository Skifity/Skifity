import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"

import { api, ApiError, setUnauthenticatedHandler } from "@/lib/api"
import { closeEventStreams } from "@/lib/events"
import { queryClient } from "@/lib/query"
import type { Theme } from "@/lib/theme"
import type { Meta, Team, User } from "@/lib/types"

/**
 * Who is signed in, which team they are looking at, and what this build is.
 *
 * Loaded once at start-up: every page needs it, and a panel that renders its
 * shell before knowing whether anyone is signed in flashes the wrong screen.
 * It goes through the same query client as everything else, so a 401 anywhere
 * in the app can mark it signed out and the sign-in screen appears by itself.
 *
 * The rest of the query cache belongs to whoever is signed in. It is dropped
 * when they sign out, when their session turns out to be gone, and when the
 * session comes back as somebody else, so the next person never sees the last
 * one's pages, even for the moment a refetch takes.
 */

type SessionState = {
  loading: boolean
  /**
   * Why the session could not be loaded at all: the panel unreachable, or
   * failing. Not being signed in is not an error; it is user === null.
   */
  error: unknown
  needsSetup: boolean
  user: User | null
  teams: Team[]
  team: Team | null
  meta: Meta | null
  /** Unused two-factor recovery codes, so the account page can say so. */
  recoveryCodesLeft: number
  /**
   * Whether this sign-in would pass a team's requirement for a second factor
   * or single sign-on, so the panel can say why such a team refuses.
   */
  strongAuth: boolean
  /**
   * False for an account that signs in only through the identity provider,
   * which sets a first password rather than changing one.
   */
  hasPassword: boolean
  /**
   * Somebody signed in on this page — through the sign-in form, setup or an
   * invitation — rather than arriving with a session already open. The
   * account's language and theme are applied then, and only then: a page
   * loaded with a session keeps what was last picked in this browser.
   */
  justSignedIn: boolean
  setTeam: (team: Team) => void
  refresh: () => Promise<void>
  signOut: () => Promise<void>
}

const SessionContext = createContext<SessionState | null>(null)

const TEAM_STORAGE_KEY = "skifity-team"
const SESSION_QUERY_KEY = ["session"]

type Bootstrap = {
  needsSetup: boolean
  meta: Meta | null
  user: User | null
  teams: Team[]
  recoveryCodesLeft: number
  strongAuth: boolean
  hasPassword: boolean
  justSignedIn: boolean
}

function signedOut(meta: Meta | null): Bootstrap {
  return {
    needsSetup: false,
    meta,
    user: null,
    teams: [],
    recoveryCodesLeft: 0,
    strongAuth: false,
    hasPassword: true,
    justSignedIn: false,
  }
}

/** Whose data the rest of the query cache holds. */
let cacheOwner: string | null = null

/** Everything but the session itself. */
const notTheSession = (query: { queryKey: readonly unknown[] }) =>
  query.queryKey[0] !== SESSION_QUERY_KEY[0]

async function loadSession(): Promise<Bootstrap> {
  const before = queryClient.getQueryData<Bootstrap>(SESSION_QUERY_KEY)

  // A failure here is not "nobody is signed in": the panel being unreachable
  // or failing is said as such, with a way to try again. The installer's
  // /setup link opened during a restart used to show a sign-in form for a
  // panel that has no accounts.
  const [status, meta] = await Promise.all([
    api.anonymous<{ needs_setup: boolean }>("/api/setup/status"),
    api.anonymous<Meta>("/api/meta"),
  ])
  if (status.needs_setup) return { ...signedOut(meta), needsSetup: true }

  let me: {
    user: User
    teams: Team[]
    recovery_codes_left?: number
    strong_auth?: boolean
    has_password?: boolean
  }
  try {
    me = await api.anonymous("/api/me")
  } catch (caught) {
    // Not signed in. That is an answer, not a failure; anything else is one.
    if (caught instanceof ApiError && caught.status === 401) return signedOut(meta)
    throw caught
  }

  if (cacheOwner !== null && cacheOwner !== me.user.id) {
    // Somebody else signed in, in another tab or over this one's session.
    // Reset rather than removed: the pages showing are still mounted, and a
    // reset tells them, then refetches what they show as the new person.
    void queryClient.resetQueries({ predicate: notTheSession })
  }
  cacheOwner = me.user.id
  return {
    needsSetup: false,
    meta,
    user: me.user,
    teams: me.teams,
    recoveryCodesLeft: me.recovery_codes_left ?? 0,
    strongAuth: me.strong_auth ?? false,
    hasPassword: me.has_password ?? true,
    justSignedIn: before !== undefined && !before.user,
  }
}

/**
 * The session is gone: the panel answered a request with auth.required.
 * Signed out at once rather than by asking again.
 */
function sessionEnded() {
  const session = queryClient.getQueryData<Bootstrap>(SESSION_QUERY_KEY)
  if (!session?.user) return
  closeEventStreams()
  queryClient.setQueryData<Bootstrap>(SESSION_QUERY_KEY, signedOut(session.meta))
}

/**
 * Drops what the cache holds for somebody who is no longer signed in, and
 * the event stream that was keeping it current.
 *
 * Called once the pages are gone — the sign-in screen is up — so no page is
 * left watching a query removed from under it. The sign-in screen's own are
 * watched, and already on their way by the time this runs: removing those
 * cancelled them and left the screen waiting for ever, with no single sign-on
 * or passkey button. Every query the signed-out person's pages read has no
 * page watching it any more, so leaving the watched ones drops all of theirs.
 */
export function forgetSignedOutUser() {
  if (cacheOwner === null) return
  cacheOwner = null
  closeEventStreams()
  queryClient.removeQueries({ predicate: (query) => notTheSession(query) && !query.isActive() })
}

/**
 * Saves a theme somebody picked to their account, so it follows them to
 * another machine. Nothing to do for nobody; a failure keeps the theme here.
 */
export function saveThemeToAccount(theme: Theme) {
  const session = queryClient.getQueryData<Bootstrap>(SESSION_QUERY_KEY)
  if (!session?.user || session.user.theme === theme) return
  // The session says so straight away, so nothing reads the old one back as
  // the account's choice while the request is on its way.
  queryClient.setQueryData<Bootstrap>(SESSION_QUERY_KEY, {
    ...session,
    user: { ...session.user, theme },
  })
  void api.patch("/api/me", { theme }).catch(() => {
    // The panel may be unreachable. The theme still changed here.
  })
}

/**
 * Saves a language somebody picked to their account, as saveThemeToAccount
 * does a theme, and for the same reason says so in the session straight away:
 * left as it was, the account's old language was read back and put on screen
 * again the next time anything else about the account changed.
 */
export function saveLanguageToAccount(locale: string) {
  const session = queryClient.getQueryData<Bootstrap>(SESSION_QUERY_KEY)
  if (!session?.user || session.user.locale === locale) return
  queryClient.setQueryData<Bootstrap>(SESSION_QUERY_KEY, {
    ...session,
    user: { ...session.user, locale },
  })
  void api.patch("/api/me", { locale }).catch(() => {
    // The panel may be unreachable. The language still changed here.
  })
}

function readStoredTeam(): string | null {
  try {
    return localStorage.getItem(TEAM_STORAGE_KEY)
  } catch {
    // Storage may be blocked; the first team is a fine default.
    return null
  }
}

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const session = useQuery({
    queryKey: SESSION_QUERY_KEY,
    queryFn: loadSession,
    // A failure is shown, with a button to try again; retrying the whole
    // bootstrap behind a blank screen first only delays that.
    retry: false,
    staleTime: 30_000,
  })

  const [chosenTeam, setChosenTeam] = useState<string | null>(readStoredTeam)

  // A request that comes back 401 auth.required means the session is gone,
  // and the app goes to the sign-in screen instead of showing broken pages.
  useEffect(() => {
    setUnauthenticatedHandler(sessionEnded)
  }, [])

  const teams = useMemo(() => session.data?.teams ?? [], [session.data])

  // Which team is shown is derived: the remembered one while it still exists,
  // otherwise the first. Nothing has to repair it when a team is removed.
  const team = teams.find((candidate) => candidate.id === chosenTeam) ?? teams[0] ?? null

  const setTeam = useCallback((next: Team) => {
    setChosenTeam(next.id)
    try {
      localStorage.setItem(TEAM_STORAGE_KEY, next.id)
    } catch {
      // Not remembering the choice is not a reason to refuse it.
    }
  }, [])

  const refresh = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: SESSION_QUERY_KEY })
  }, [])

  const signOut = useCallback(async () => {
    try {
      await api.post("/api/auth/logout")
    } catch {
      // The page below asks the panel afresh, and shows whatever is true.
    }
    // A new page rather than a re-render. Clearing the cache under a shell
    // that was still mounted left the shell up, with the name and every page
    // of the person who had just signed out. Loading the page again closes
    // the event stream and drops every byte of the cache along with it.
    closeEventStreams()
    window.location.assign("/")
  }, [])

  const value = useMemo<SessionState>(
    () => ({
      loading: session.isPending,
      error: session.data ? null : session.error,
      needsSetup: session.data?.needsSetup ?? false,
      user: session.data?.user ?? null,
      teams,
      team,
      meta: session.data?.meta ?? null,
      recoveryCodesLeft: session.data?.recoveryCodesLeft ?? 0,
      strongAuth: session.data?.strongAuth ?? false,
      hasPassword: session.data?.hasPassword ?? true,
      justSignedIn: session.data?.justSignedIn ?? false,
      setTeam,
      refresh,
      signOut,
    }),
    [session.isPending, session.data, session.error, teams, team, setTeam, refresh, signOut],
  )

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession(): SessionState {
  const context = useContext(SessionContext)
  if (!context) throw new Error("useSession must be used inside a SessionProvider")
  return context
}

/** This panel's build, where it is known; null outside a SessionProvider. */
export function usePanelMeta(): Meta | null {
  return useContext(SessionContext)?.meta ?? null
}
