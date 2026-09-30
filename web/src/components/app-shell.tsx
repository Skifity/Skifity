import { Fragment } from "react"
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import {
  ActivityIcon,
  BookOpenIcon,
  BoxesIcon,
  ChevronsUpDownIcon,
  DatabaseIcon,
  ExternalLinkIcon,
  FolderIcon,
  InfoIcon,
  LayoutGridIcon,
  LogOutIcon,
  PuzzleIcon,
  SearchIcon,
  ServerIcon,
  SettingsIcon,
  ShieldAlertIcon,
  UserIcon,
  XIcon,
} from "lucide-react"

import { CommandPalette } from "@/components/command-palette"
import { ErrorBoundary } from "@/components/error-boundary"
import { LanguageSwitcher } from "@/components/language-switcher"
import { Logo } from "@/components/logo"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Kbd, KbdGroup } from "@/components/ui/kbd"
import { Separator } from "@/components/ui/separator"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { usePaletteShortcut } from "@/hooks/use-palette"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import type { Project, Server } from "@/lib/types"

/**
 * The panel's frame.
 *
 * Built on shadcn's Sidebar rather than a hand-rolled aside, which is what
 * gives it a collapsible rail, keyboard control, a mobile drawer and a
 * remembered open state without any of that being written here.
 */
export function AppShell() {
  const { t } = useTranslation()
  const { paletteOpen, setPaletteOpen } = usePaletteShortcut()
  const location = useLocation()

  return (
    <SidebarProvider>
      {/*
        Visible only once it has focus. Without it, a keyboard reaches the page
        by tabbing through every link in the sidebar, on every navigation — the
        sidebar is the same on all of them, so it is the same twenty presses
        every time.
      */}
      <a
        href="#content"
        className="sr-only z-50 rounded-md bg-primary px-4 py-2 text-primary-foreground focus:not-sr-only focus:absolute focus:top-3 focus:left-3"
      >
        {t("common.skipToContent")}
      </a>
      <AppSidebar />
      {/*
        min-w-0 is load-bearing. A flex item's automatic minimum size is its
        min-content width, so without this the inset refuses to be narrower
        than the widest thing in the header — and the whole page scrolls
        sideways by exactly that difference. It was 7px on a 768px tablet.
      */}
      <SidebarInset className="min-w-0">
        <Header onOpenPalette={() => setPaletteOpen(true)} />
        <main id="content" tabIndex={-1} className="min-w-0 flex-1 p-4 sm:p-6 lg:p-8">
          {/*
            Inside the shell rather than around it, so a page that throws
            leaves the sidebar, the header and the command palette working and
            the person can simply go somewhere else. The path is the reset key:
            navigating away is what clears it.
          */}
          <InstallNotes />
          <ErrorBoundary resetKey={location.pathname}>
            <Outlet />
          </ErrorBoundary>
        </main>
      </SidebarInset>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </SidebarProvider>
  )
}

/**
 * What a template said had to be done by hand, on the page it sent you to.
 *
 * A template's notes are the steps Skifity cannot do for you — a bucket that
 * has to be created, a migration that has to be run, a shared directory that is
 * two directories here. They were shown in the install dialog and nowhere else,
 * so the one moment they matter — after installing, when the app is up and
 * half-working — they were gone. Now the install carries them along in the
 * navigation and this shows them until they are dismissed.
 */
function InstallNotes() {
  const { t } = useTranslation()
  const location = useLocation()
  const navigate = useNavigate()
  const notes = (location.state as { installedNotes?: string } | null)?.installedNotes
  if (!notes) return null
  return (
    <Alert className="mb-6">
      <InfoIcon />
      <AlertTitle>{t("templates.notesTitle")}</AlertTitle>
      <AlertDescription>{notes}</AlertDescription>
      <Button
        variant="ghost"
        size="sm"
        className="absolute right-2 top-2"
        aria-label={t("common.dismiss")}
        // Replace rather than push: going back should not bring the note back.
        onClick={() => navigate(location.pathname, { replace: true, state: null })}
      >
        <XIcon className="size-4" />
      </Button>
    </Alert>
  )
}

function AppSidebar() {
  const { t } = useTranslation()
  const { team, teams, setTeam, user } = useSession()

  // Counts next to the navigation, so the sidebar says something about the
  // state of the system rather than being a list of nouns.
  const servers = useQuery({
    queryKey: ["servers", team?.id],
    queryFn: () => api.get<List<Server>>(`/api/teams/${team!.id}/servers`),
    enabled: Boolean(team),
  })
  const projects = useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: Boolean(team),
  })

  const unhealthy = (servers.data?.items ?? []).filter(
    (server) => server.status === "failed" || server.status === "not_ready",
  ).length

  const links = [
    { to: "/", label: t("nav.overview"), icon: LayoutGridIcon, end: true },
    { to: "/projects", label: t("nav.projects"), icon: FolderIcon, count: projects.data?.total },
    { to: "/databases", label: t("nav.databases"), icon: DatabaseIcon },
    {
      to: "/servers",
      label: t("nav.servers"),
      icon: ServerIcon,
      count: servers.data?.total,
      alert: unhealthy > 0,
    },
    { to: "/templates", label: t("nav.templates"), icon: BoxesIcon },
    { to: "/plugins", label: t("nav.plugins"), icon: PuzzleIcon },
    { to: "/activity", label: t("nav.activity"), icon: ActivityIcon },
  ]

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton asChild size="lg" tooltip="Skifity">
              <Link to="/">
                <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                  <Logo className="size-4" />
                </div>
                <div className="grid flex-1 text-left leading-tight">
                  <span className="truncate font-semibold">Skifity</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {team?.name ?? "—"}
                  </span>
                </div>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>

          {teams.length > 1 && (
            <SidebarMenuItem>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <SidebarMenuButton tooltip={t("settings.team")}>
                    <ChevronsUpDownIcon />
                    <span className="truncate">{team?.name ?? "—"}</span>
                  </SidebarMenuButton>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="w-56">
                  <DropdownMenuLabel>{t("settings.team")}</DropdownMenuLabel>
                  {teams.map((candidate) => (
                    <DropdownMenuItem key={candidate.id} onSelect={() => setTeam(candidate)}>
                      <span className="truncate">{candidate.name}</span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            </SidebarMenuItem>
          )}
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        {/* No group label: there is one group, and it used to be headed
            "Overview" above an item called "Overview". */}
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {links.map((link) => (
                <SidebarMenuItem key={link.to}>
                  <SidebarMenuButton asChild tooltip={link.label}>
                    <NavLink to={link.to} end={link.end}>
                      {({ isActive }) => (
                        <>
                          <link.icon data-active={isActive} />
                          <span>{link.label}</span>
                        </>
                      )}
                    </NavLink>
                  </SidebarMenuButton>
                  {link.alert ? (
                    <SidebarMenuBadge className="text-destructive">{unhealthy}</SidebarMenuBadge>
                  ) : link.count ? (
                    <SidebarMenuBadge>{link.count}</SidebarMenuBadge>
                  ) : null}
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <SidebarMenu>
          {user?.is_admin && (
            <SidebarMenuItem>
              <SidebarMenuButton asChild tooltip={t("nav.settings")}>
                <NavLink to="/settings">
                  <SettingsIcon />
                  <span>{t("nav.settings")}</span>
                </NavLink>
              </SidebarMenuButton>
            </SidebarMenuItem>
          )}
          <SidebarMenuItem>
            <SidebarMenuButton asChild tooltip={t("nav.documentation")}>
              {/* The one link in the sidebar that leaves the panel, so it says
                  so rather than surprising somebody with a new tab. */}
              <a href="/docs/" target="_blank" rel="noreferrer">
                <BookOpenIcon />
                <span>{t("nav.documentation")}</span>
                <ExternalLinkIcon className="ml-auto size-3.5 opacity-50" />
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>

      <SidebarRail />
    </Sidebar>
  )
}

function Header({ onOpenPalette }: { onOpenPalette: () => void }) {
  const { t } = useTranslation()

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-1 h-4" />

      <Crumbs />

      {/*
        The label appears at lg rather than sm because what decides whether it
        fits is the space beside the sidebar, not the width of the window: at
        768px the sidebar has already taken 256 of it.
      */}
      <Button
        variant="outline"
        size="sm"
        onClick={onOpenPalette}
        aria-label={t("nav.commandPalette")}
        className="ml-auto gap-2 text-muted-foreground"
      >
        <SearchIcon className="size-4" />
        <span className="hidden lg:inline">{t("nav.commandPalette")}</span>
        <KbdGroup className="hidden lg:flex">
          <Kbd>⌘</Kbd>
          <Kbd>K</Kbd>
        </KbdGroup>
      </Button>

      <LanguageSwitcher signedIn />
      <ThemeToggle />
      <AccountMenu />
    </header>
  )
}

/**
 * Where you are, from the URL.
 *
 * Only the segments that name something are shown: an id in the middle of a
 * path is noise, and the page itself already says what it is looking at.
 *
 * A crumb is a link only when the path it would point at is a page. Dropping
 * the ids means the trail has to be rebuilt from what is left, and that used to
 * produce addresses nothing serves: on the new-app page —
 * /environments/env_x/apps/new — "Environments" linked to /environments and
 * "Apps" to /environments/apps, both of which are the not-found page.
 */
const PAGES = new Set([
  "/projects",
  "/databases",
  "/servers",
  "/templates",
  "/plugins",
  "/activity",
  "/settings",
  "/account",
])
function Crumbs() {
  const { t } = useTranslation()
  const location = useLocation()

  const segments = location.pathname.split("/").filter(Boolean)
  const named = segments.filter((segment) => !segment.includes("_"))

  if (named.length === 0) {
    return (
      <Breadcrumb>
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbPage>{t("nav.overview")}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>
    )
  }

  return (
    <Breadcrumb className="min-w-0">
      <BreadcrumbList>
        <BreadcrumbItem className="hidden sm:block">
          <BreadcrumbLink asChild>
            <Link to="/">{t("nav.overview")}</Link>
          </BreadcrumbLink>
        </BreadcrumbItem>
        {named.map((segment, index) => {
          const last = index === named.length - 1
          const label = t(`nav.${segment}`, { defaultValue: humanise(segment) })
          const path = "/" + named.slice(0, index + 1).join("/")
          return (
            <Fragment key={segment + index}>
              <BreadcrumbSeparator className="hidden sm:block" />
              <BreadcrumbItem className="min-w-0">
                {last || !PAGES.has(path) ? (
                  <BreadcrumbPage className="truncate">{label}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink asChild>
                    <Link to={path}>{label}</Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}

function humanise(segment: string): string {
  return segment.charAt(0).toUpperCase() + segment.slice(1).replace(/-/g, " ")
}

function AccountMenu() {
  const { t } = useTranslation()
  const { user, signOut } = useSession()
  const navigate = useNavigate()

  const initial = (user?.name || user?.email || "?").charAt(0).toUpperCase()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("nav.account")}
          data-slot="account-menu"
          className="relative"
        >
          <Avatar className="size-7">
            <AvatarFallback className="bg-primary text-xs font-medium text-primary-foreground">
              {initial}
            </AvatarFallback>
          </Avatar>
          {!user?.recovery_saved && (
            <span className="absolute top-1 right-1 size-2 rounded-full bg-warning ring-2 ring-background" />
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="font-normal">
          <div className="flex items-center gap-2">
            <Avatar className="size-8">
              <AvatarFallback className="bg-primary text-xs font-medium text-primary-foreground">
                {initial}
              </AvatarFallback>
            </Avatar>
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">{user?.name || user?.email}</div>
              {user?.name && (
                <div className="truncate text-xs text-muted-foreground">{user.email}</div>
              )}
            </div>
          </div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => navigate("/account")}>
          <UserIcon />
          {t("nav.account")}
        </DropdownMenuItem>
        {!user?.recovery_saved && (
          <DropdownMenuItem onSelect={() => navigate("/account?recovery=1")}>
            <ShieldAlertIcon className="text-warning" />
            <span className="text-warning">{t("auth.recoveryKeyReminder")}</span>
            <Badge variant="outline" className="ml-auto border-warning/40 text-warning">
              !
            </Badge>
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => {
            void signOut()
          }}
        >
          <LogOutIcon />
          {t("nav.signOut")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
