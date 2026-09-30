import { Suspense, lazy, useEffect } from "react"
import { Navigate, Route, Routes, useLocation } from "react-router-dom"
import { useTranslation } from "react-i18next"

import { AppShell } from "@/components/app-shell"
import { Skeleton } from "@/components/ui/skeleton"
import { useSession } from "@/hooks/use-session"
import { AccountPage } from "@/pages/account"
import { ActivityPage } from "@/pages/activity"
import { AddServerPage } from "@/pages/add-server"
import { AppDetailPage } from "@/pages/app-detail"
import { DashboardPage } from "@/pages/dashboard"
import { DatabaseDetailPage } from "@/pages/database-detail"
import { DatabasesPage } from "@/pages/databases"
import { InvitePage } from "@/pages/invite"
import { LoginPage } from "@/pages/login"
import { NewAppPage } from "@/pages/new-app"
import { NotFoundPage } from "@/pages/not-found"
import { ProjectDetailPage } from "@/pages/project-detail"
import { ProjectsPage } from "@/pages/projects"
import { ServerDetailPage } from "@/pages/server-detail"
import { ServersPage } from "@/pages/servers"
import { CenteredLayout, SetupPage } from "@/pages/setup"

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
 * There are exactly three states before the routes matter: the session is still
 * loading, the panel has never been set up, or nobody is signed in. Each of them
 * takes over the whole screen, because showing a half-usable shell behind a
 * sign-in form is how people end up clicking things that then fail.
 */
export function App() {
  const { loading, needsSetup, user, refresh } = useSession()

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
  if (window.location.pathname.startsWith("/invite/")) {
    return (
      <Routes>
        <Route path="/invite/:token" element={<InvitePage onSignedIn={() => void refresh()} />} />
      </Routes>
    )
  }

  if (needsSetup) return <SetupPage onComplete={() => void refresh()} />
  if (!user) return <LoginPage onSignedIn={() => void refresh()} />

  return (
    <>
      <DocumentTitle />
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<DashboardPage />} />
          <Route path="projects" element={<ProjectsPage />} />
          <Route path="projects/:projectId" element={<ProjectDetailPage />} />
          <Route path="environments/:envId/apps/new" element={<NewAppPage />} />
          <Route path="apps/:appId" element={<AppDetailPage />} />
          <Route path="databases" element={<DatabasesPage />} />
          <Route path="databases/:databaseId" element={<DatabaseDetailPage />} />
          <Route path="servers" element={<ServersPage />} />
          <Route path="servers/new" element={<AddServerPage />} />
          <Route path="servers/:serverId" element={<ServerDetailPage />} />
          <Route
            path="templates"
            element={
              <Suspense fallback={<Skeleton className="h-64" />}>
                <TemplatesPage />
              </Suspense>
            }
          />
          <Route
            path="plugins"
            element={
              <Suspense fallback={<Skeleton className="h-64" />}>
                <PluginsPage />
              </Suspense>
            }
          />
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

/** Settings are an administrator's page; anyone else is sent back to the overview. */
function SettingsRoute() {
  const { user } = useSession()
  if (!user?.is_admin) return <Navigate to="/" replace />
  return (
    <Suspense fallback={<Skeleton className="h-64" />}>
      <SettingsPage />
    </Suspense>
  )
}

/** Keeps the browser tab's title in step with the page. */
function DocumentTitle() {
  const { t } = useTranslation()
  const location = useLocation()

  useEffect(() => {
    const segment = location.pathname.split("/")[1] || "overview"
    const label = t(`nav.${segment}`, { defaultValue: "" })
    document.title = label ? `${label} · Skifity` : "Skifity"
  }, [location.pathname, t])

  return null
}
