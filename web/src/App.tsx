import {
  Fragment,
  Suspense,
  lazy,
  useEffect,
  useEffectEvent,
  useState,
  type ReactNode,
} from "react"
import { Navigate, Route, Routes, useLocation, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useIsFetching } from "@tanstack/react-query"
import { RefreshCwIcon } from "lucide-react"

import { AppShell } from "@/components/app-shell"
import { CenteredLayout } from "@/components/centered-layout"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { forgetSignedOutUser, saveThemeToAccount, useSession } from "@/hooks/use-session"
import { currentLanguage, isLanguage, setLanguage } from "@/lib/i18n"
import { canOpenSettings } from "@/lib/permissions"
import { useTheme } from "@/lib/theme"
import { AccountPage } from "@/pages/account"
import { ActivityPage } from "@/pages/activity"
import { AddServerPage } from "@/pages/add-server"
import { AppDetailPage } from "@/pages/app-detail"
import { DashboardPage } from "@/pages/dashboard"
import { DatabaseDetailPage } from "@/pages/database-detail"
import { DatabasesPage } from "@/pages/databases"
import { InvitePage } from "@/pages/invite"
import { LoginPage } from "@/pages/login"
import { ResetPasswordPage } from "@/pages/reset-password"
import { NewAppPage } from "@/pages/new-app"
import { NotFoundPage } from "@/pages/not-found"
import { ProjectDetailPage } from "@/pages/project-detail"
import { ProjectsPage } from "@/pages/projects"
import { ServerDetailPage } from "@/pages/server-detail"
import { ServersPage } from "@/pages/servers"
import { SetupPage } from "@/pages/setup"

// Settings and templates are rarely the first page anyone opens, so they load
// on demand and stay out of the initial bundle.
const SettingsPage = lazy(() =>
  import("@/pages/settings").then((module) => ({ default: module.SettingsPage })),
)
const TemplatesPage = lazy(() =>
  import("@/pages/templates").then((module) => ({ default: module.TemplatesPage })),
)
const PluginsPage = lazy(() =>
  import("@/pages/plugins").then((module) => ({ default: module.PluginsPage })),
)

/**
 * The panel's routes.
 *
 * There are a few states before the routes matter: the session is still
 * loading, the panel could not be reached, the panel has never been set up,
 * or nobody is signed in. Each of them takes over the whole screen, because
 * showing a half-usable shell behind a sign-in form is how people end up
 * clicking things that then fail.
 */
export function App() {
  const { t } = useTranslation()
  const { loading, error, needsSetup, user, refresh } = useSession()
  const { pathname } = useLocation()
  const retrying = useIsFetching({ queryKey: ["session"] }) > 0
  // Set once the first account exists and its recovery key is on screen.
  // From then on the panel no longer needs setting up, and any refetch of the
  // session would have swapped the key for the shell before it was saved.
  const [settingUp, setSettingUp] = useState(false)

  // Nobody is signed in any more, whether they signed out in another tab or
  // their session ran out: what the cache holds was fetched as them, and is
  // dropped now that the pages showing it have gone, before anybody else
  // signs in at this screen.
  useEffect(() => {
    if (!loading && !user) forgetSignedOutUser()
  }, [loading, user])

  if (loading) {
    return (
      <CenteredLayout>
        <div className="w-full max-w-sm space-y-3">
          <Skeleton className="h-9 w-36" />
          <Skeleton className="h-40" />
        </div>
      </CenteredLayout>
    )
  }

  // Before the sign-in gate: somebody holding an invitation has no account, so
  // sending them to a sign-in form is sending them to a form they cannot use.
  // Before the setup gate too — an install with no account has no invitations,
  // and a link that survived a wipe should say so rather than hand a stranger
  // first-run setup.
  if (pathname.startsWith("/invite/")) {
    return (
      <Routes>
        <Route path="/invite/:token" element={<InvitePage onSignedIn={refresh} />} />
      </Routes>
    )
  }

  // A reset link is followed by somebody who is not signed in, and it works
  // the same whether a session happens to be open in this browser or not.
  if (pathname === "/reset-password") return <ResetPasswordPage />

  if (settingUp || needsSetup) {
    return (
      <SetupPage
        onAccountCreated={() => setSettingUp(true)}
        onComplete={() => {
          setSettingUp(false)
          void refresh()
        }}
      />
    )
  }

  // The panel could not say whether anybody is signed in. That is not the same
  // as nobody being signed in: the sign-in form is a dead end on a panel that
  // has no accounts yet, and a lie to somebody whose session is fine.
  if (error) {
    return (
      <CenteredLayout>
        <div className="w-full max-w-lg space-y-3">
          <ErrorDisplay error={error} />
          <Button variant="outline" disabled={retrying} onClick={() => void refresh()}>
            {retrying ? <Spinner /> : <RefreshCwIcon />}
            {t("common.retry")}
          </Button>
        </div>
      </CenteredLayout>
    )
  }

  if (!user) return <LoginPage onSignedIn={() => void refresh()} />

  return (
    <>
      <AccountPreferences />
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<DashboardPage />} />
          <Route path="projects" element={<ProjectsPage />} />
          <Route
            path="projects/:projectId"
            element={
              <KeyedBy param="projectId">
                <ProjectDetailPage />
              </KeyedBy>
            }
          />
          <Route
            path="environments/:envId/apps/new"
            element={
              <KeyedBy param="envId">
                <NewAppPage />
              </KeyedBy>
            }
          />
          <Route
            path="apps/:appId"
            element={
              <KeyedBy param="appId">
                <AppDetailPage />
              </KeyedBy>
            }
          />
          <Route path="databases" element={<DatabasesPage />} />
          <Route
            path="databases/:databaseId"
            element={
              <KeyedBy param="databaseId">
                <DatabaseDetailPage />
              </KeyedBy>
            }
          />
          <Route path="servers" element={<ServersPage />} />
          <Route path="servers/new" element={<AddServerPage />} />
          <Route
            path="servers/:serverId"
            element={
              <KeyedBy param="serverId">
                <ServerDetailPage />
              </KeyedBy>
            }
          />
          <Route
            path="templates"
            element={
              <Suspense fallback={<Skeleton className="h-64" />}>
                <TemplatesPage />
              </Suspense>
            }
          />
          <Route path="plugins" element={<PluginsRoute />} />
          <Route path="activity" element={<ActivityPage />} />
          {/*
            The installer prints a link to /setup, and the gates above are what
            serve that page — they are not routes. Once there is an account the
            gates are gone, so the person who just finished setup was left on a
            path with no route behind it, looking at "Not found". Same for
            /login, which a browser will happily offer from history.
          */}
          <Route path="setup" element={<Navigate to="/" replace />} />
          <Route path="login" element={<Navigate to="/" replace />} />
          <Route path="account" element={<AccountPage />} />
          <Route path="settings" element={<SettingsRoute />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </>
  )
}

/**
 * A page about one thing, started afresh for each. React Router keeps the
 * same page mounted when only the id in the address changes, and the command
 * palette goes straight from one server to another: the page went on holding
 * what it had about the first — an error, a box ticked to accept losing its
 * data, an operation being followed — while showing the second, where "Remove
 * anyway" would then have been sent with the first one's acceptance.
 */
function KeyedBy({ param, children }: { param: string; children: ReactNode }) {
  const params = useParams()
  return <Fragment key={params[param]}>{children}</Fragment>
}

/**
 * Settings hold the panel-wide tabs, for the panel's administrator, and the
 * team's own — members, invitations, connections — for the team's owners and
 * admins. Anyone else is sent back to the overview.
 */
function SettingsRoute() {
  const { user, team } = useSession()
  if (!canOpenSettings(user, team)) return <Navigate to="/" replace />
  return (
    <Suspense fallback={<Skeleton className="h-64" />}>
      <SettingsPage />
    </Suspense>
  )
}

/** Plugins are installed for the whole panel, so only its administrator sees them. */
function PluginsRoute() {
  const { user } = useSession()
  if (!user?.is_admin) return <Navigate to="/" replace />
  return (
    <Suspense fallback={<Skeleton className="h-64" />}>
      <PluginsPage />
    </Suspense>
  )
}

/**
 * The account's language and theme, applied when somebody signs in, so both
 * follow them to another machine.
 *
 * Only at signing in. A page loaded with a session already open keeps what
 * was last picked in this browser: a choice whose save had not reached the
 * panel before a reload was otherwise undone by the account's older one.
 */
function AccountPreferences() {
  const { user, justSignedIn } = useSession()
  const { theme, applyTheme } = useTheme()

  const apply = useEffectEvent(() => {
    if (!user) return
    if (isLanguage(user.locale) && user.locale !== currentLanguage()) setLanguage(user.locale)
    if (user.theme === "light" || user.theme === "dark") {
      if (user.theme !== theme) applyTheme(user.theme)
    } else if (theme !== "system") {
      // "system" is what every account starts with, and until now nothing
      // saved a choice to the account at all. One made in this browser is the
      // better guess at what the person wants, so it is recorded instead.
      saveThemeToAccount(theme)
    }
  })

  // Signing in is an event in another system — the panel's — and this
  // applies its answer to two more, i18next and the theme. Once per sign-in,
  // keyed by who: the account changing afterwards is not signing in again,
  // and following every change put back the account's old language the
  // moment a theme was picked after a new one.
  const signedInAs = justSignedIn ? (user?.id ?? null) : null
  useEffect(() => {
    if (signedInAs) apply()
  }, [signedInAs])

  return null
}
