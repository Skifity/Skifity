import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"

import { api, setUnauthenticatedHandler } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { Meta, Team, User } from "@/lib/types"

/**
 * Who is signed in, which team they are looking at, and what this build is.
 *
 * Loaded once at start-up: every page needs it, and a panel that renders its
 * shell before knowing whether anyone is signed in flashes the wrong screen.
 * It goes through the same query client as everything else, so a 401 anywhere
 * in the app can invalidate it and the sign-in screen appears by itself.
 */

type SessionState = {
  loading: boolean
  needsSetup: boolean
  user: User | null
  teams: Team[]
  team: Team | null
  meta: Meta | null
  /** Unused two-factor recovery codes, so the account page can say so. */
  recoveryCodesLeft: number
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
}

async function loadSession(): Promise<Bootstrap> {
  const [status, meta] = await Promise.all([
    api.anonymous<{ needs_setup: boolean }>("/api/setup/status"),
    api.anonymous<Meta>("/api/meta"),
  ])
  if (status.needs_setup) {
    return { needsSetup: true, meta, user: null, teams: [], recoveryCodesLeft: 0 }
  }
  try {
    const me = await api.anonymous<{
      user: User
      teams: Team[]
      recovery_codes_left?: number
    }>("/api/me")
    return {
      needsSetup: false,
      meta,
      user: me.user,
      teams: me.teams,
      recoveryCodesLeft: me.recovery_codes_left ?? 0,
    }
  } catch {
    // Not signed in. That is an answer, not a failure.
    return { needsSetup: false, meta, user: null, teams: [], recoveryCodesLeft: 0 }
  }
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
    // The panel being unreachable is shown by the pages themselves, and
    // retrying the whole bootstrap behind a blank screen only delays that.
    retry: false,
    staleTime: 30_000,
  })

  const [chosenTeam, setChosenTeam] = useState<string | null>(readStoredTeam)

  // A request that comes back 401 means the session is gone. Reloading the
  // bootstrap sends the app to the sign-in screen instead of showing broken
  // pages.
  useEffect(() => {
    setUnauthenticatedHandler(() => {
      void queryClient.invalidateQueries({ queryKey: SESSION_QUERY_KEY })
    })
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
    } finally {
      // Everything cached was fetched as the user who just left.
      queryClient.clear()
      await queryClient.invalidateQueries({ queryKey: SESSION_QUERY_KEY })
    }
  }, [])

  const value = useMemo<SessionState>(
    () => ({
      loading: session.isPending,
      needsSetup: session.data?.needsSetup ?? false,
      user: session.data?.user ?? null,
      teams,
      team,
      meta: session.data?.meta ?? null,
      recoveryCodesLeft: session.data?.recoveryCodesLeft ?? 0,
      setTeam,
      refresh,
      signOut,
    }),
    [session.isPending, session.data, teams, team, setTeam, refresh, signOut],
  )

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

export function useSession(): SessionState {
  const context = useContext(SessionContext)
  if (!context) throw new Error("useSession must be used inside a SessionProvider")
  return context
}
