import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"
import { useMutation, useQueries, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  AlertTriangleIcon,
  BoxesIcon,
  ExternalLinkIcon,
  InfoIcon,
  LibraryIcon,
  SearchIcon,
} from "lucide-react"
import { cn } from "cn"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { TemplateCataloguesSheet, useTemplateCatalogues } from "@/components/template-catalogues"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import type { Environment, Project, Template } from "@/lib/types"

/** What the category picker holds for "every category": a Select item cannot be empty. */
const EVERY_CATEGORY = "__all__"

/** How the same grid is laid out while the catalogue is loading and once it has loaded. */
const GRID = "grid gap-4 sm:grid-cols-2 lg:grid-cols-3"

export function TemplatesPage() {
  const { t } = useTranslation()
  const { team } = useSession()
  const [search, setSearch] = useState("")
  const [category, setCategory] = useState("")
  const [installing, setInstalling] = useState<Template | null>(null)
  const [managing, setManaging] = useState(false)

  // The catalogue as this team sees it: its own catalogues' templates, then
  // the built-in ones. Without a team there is only the built-in one.
  const templates = useQuery({
    queryKey: ["templates", team?.id ?? null],
    queryFn: () =>
      api.get<List<Template>>(team ? `/api/teams/${team.id}/templates` : "/api/templates"),
  })
  const catalogues = useTemplateCatalogues()
  // How many of the team's templates were refused, and whether any
  // catalogue's last refresh failed: both worth a line above the cards,
  // because a template that is simply missing reads as a bug.
  const refused = (catalogues.data?.items ?? []).reduce(
    (total, catalogue) => total + (catalogue.problems?.length ?? 0),
    0,
  )
  const stale = (catalogues.data?.items ?? []).filter((catalogue) => catalogue.last_error).length

  const items = useMemo(() => templates.data?.items ?? [], [templates.data])
  const searched = useMemo(() => {
    const needle = search.trim().toLowerCase()
    if (!needle) return items
    return items.filter(
      (template) =>
        template.name.toLowerCase().includes(needle) ||
        template.description.toLowerCase().includes(needle) ||
        template.category.toLowerCase().includes(needle) ||
        (template.catalogue?.name.toLowerCase().includes(needle) ?? false),
    )
  }, [items, search])

  // The category is a slug in the file — "cms", "ai" — and showing the slug is
  // how a page looks unfinished. defaultValue keeps a category nobody has
  // translated yet readable rather than blank.
  const labelOf = (slug: string) => t(`templates.categories.${slug}`, { defaultValue: slug })

  // What each category holds under the current search, so a chip's number is
  // what choosing it will show. Sorted by the name somebody reads, not by the
  // slug underneath it: "cms" and "ai" alphabetically put Websites first in
  // English and somewhere else entirely in Russian, for a reason nobody can see.
  const counts = useMemo(() => {
    const out = new Map<string, number>()
    for (const template of searched) out.set(template.category, (out.get(template.category) ?? 0) + 1)
    return out
  }, [searched])
  const categories = [...counts.keys()].sort((a, b) =>
    // "Everything else" is where a template goes when nothing fits, so it is
    // the last thing on the page and not a heading in the middle of the alphabet.
    a === "other" || b === "other"
      ? Number(a === "other") - Number(b === "other")
      : labelOf(a).localeCompare(labelOf(b)),
  )

  // A category chosen before the search emptied it is not a filter any more.
  // Derived here rather than reset in an effect, so there is one render with
  // the answer instead of one with a stale chip and one with the fix.
  const active = counts.has(category) ? category : ""
  const sections = active ? [active] : categories
  const total = active ? (counts.get(active) ?? 0) : searched.length

  return (
    <Page>
      <PageHeader
        title={t("templates.title")}
        description={t("templates.subtitle")}
        actions={
          team &&
          !team.scoped && (
            <Button variant="outline" onClick={() => setManaging(true)}>
              <LibraryIcon className="size-4" />
              {t("templates.catalogues.button")}
            </Button>
          )
        }
      />

      {(refused > 0 || stale > 0) && (
        <Alert>
          <AlertTriangleIcon />
          <AlertTitle>
            {refused > 0
              ? t("templates.catalogues.notice", { count: refused })
              : t("templates.catalogues.staleNotice", { count: stale })}
          </AlertTitle>
          <AlertDescription>
            <Button variant="link" className="h-auto p-0" onClick={() => setManaging(true)}>
              {t("templates.catalogues.review")}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      <div className="space-y-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
          <InputGroup className="sm:max-w-sm">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t("templates.search")}
            />
          </InputGroup>
          {/*
            Sixteen chips are a row of filters on a laptop and five rows of
            nothing but filters on a phone, so a phone gets the same choice as
            one dropdown. Both write the same state.
          */}
          {categories.length > 1 && (
            <Select
              value={active || EVERY_CATEGORY}
              onValueChange={(value) => setCategory(value === EVERY_CATEGORY ? "" : value)}
            >
              <SelectTrigger className="w-full sm:w-56 md:hidden" aria-label={t("templates.category")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={EVERY_CATEGORY}>
                  {t("templates.allCategories")} · {searched.length}
                </SelectItem>
                {categories.map((slug) => (
                  <SelectItem key={slug} value={slug}>
                    {labelOf(slug)} · {counts.get(slug)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {searched.length > 0 && (
            <p
              className="shrink-0 text-sm whitespace-nowrap text-muted-foreground tabular-nums sm:ml-auto"
              aria-live="polite"
            >
              {t("templates.results", { count: total })}
            </p>
          )}
        </div>

        {categories.length > 1 && (
          <div
            role="group"
            aria-label={t("templates.category")}
            className="hidden flex-wrap gap-2 md:flex"
          >
            <CategoryChip
              active={active === ""}
              label={t("templates.allCategories")}
              count={searched.length}
              onClick={() => setCategory("")}
            />
            {categories.map((slug) => (
              <CategoryChip
                key={slug}
                active={active === slug}
                label={labelOf(slug)}
                count={counts.get(slug) ?? 0}
                onClick={() => setCategory(slug)}
              />
            ))}
          </div>
        )}
      </div>

      {templates.isLoading ? (
        <div className={GRID} aria-hidden>
          {Array.from({ length: 9 }, (_, index) => (
            <Skeleton key={index} className="h-40 rounded-xl" />
          ))}
        </div>
      ) : templates.error ? (
        <ErrorDisplay error={templates.error} onRetry={() => void templates.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={BoxesIcon}
          title={t("templates.title")}
          description={t("templates.subtitle")}
        />
      ) : searched.length === 0 ? (
        <EmptyState
          icon={SearchIcon}
          title={t("common.noMatches")}
          description={t("common.noMatchesHelp")}
          action={
            <Button variant="outline" onClick={() => setSearch("")}>
              {t("common.clear")}
            </Button>
          }
        />
      ) : (
        <div className="space-y-10">
          {sections.map((slug) => (
            <section key={slug} aria-labelledby={`templates-${slug}`} className="space-y-4">
              <h2
                id={`templates-${slug}`}
                className="flex items-baseline gap-2 border-b pb-2 text-lg font-semibold tracking-tight"
              >
                {labelOf(slug)}
                <span className="text-sm font-normal text-muted-foreground tabular-nums">
                  {counts.get(slug)}
                </span>
              </h2>
              <div className={GRID} role="list">
                {searched
                  .filter((template) => template.category === slug)
                  .map((template) => (
                    <TemplateCard
                      // An id is unique within its catalogue, not across them:
                      // a team's own wiki and the built-in one are two cards.
                      key={`${template.catalogue?.id ?? ""}/${template.id}`}
                      template={template}
                      onInstall={() => setInstalling(template)}
                    />
                  ))}
              </div>
            </section>
          ))}
        </div>
      )}

      {installing && <InstallDialog template={installing} onClose={() => setInstalling(null)} />}
      <TemplateCataloguesSheet open={managing} onOpenChange={setManaging} />
    </Page>
  )
}

function CategoryChip({
  active,
  label,
  count,
  onClick,
}: {
  active: boolean
  label: string
  count: number
  onClick: () => void
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant={active ? "default" : "outline"}
      aria-pressed={active}
      className="rounded-full"
      onClick={onClick}
    >
      {label}
      <span
        className={cn(
          "text-xs font-normal tabular-nums",
          active ? "text-primary-foreground/70" : "text-muted-foreground",
        )}
      >
        {count}
      </span>
    </Button>
  )
}

/**
 * One template, as a card the same height as its neighbours.
 *
 * The footer sits at the bottom whatever the description's length, the
 * description stops at two lines, and the website is an icon rather than a
 * second labelled button: three hundred and seventy-four of those made the
 * catalogue read as a wall of buttons instead of a list of apps.
 */
function TemplateCard({ template, onInstall }: { template: Template; onInstall: () => void }) {
  const { t } = useTranslation()
  return (
    <Card
      role="listitem"
      className="gap-4 py-4 transition-[border-color,box-shadow] hover:border-primary/40 hover:shadow-md"
    >
      <CardHeader className="gap-3 px-4">
        {/*
          min-w-0 on every flex ancestor, not only on the text. shadcn's
          CardHeader is a grid, and a flex or grid item will not shrink below
          its content unless each ancestor says it may, so without this the
          `truncate` below never applies: a long name pushed a card 92px past
          the side of a 375px screen.
        */}
        <div className="flex min-w-0 items-center gap-3">
          <TemplateIcon template={template} />
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex min-w-0 items-center gap-2">
              <CardTitle className="truncate text-sm leading-tight" title={template.name}>
                {template.name}
              </CardTitle>
              {template.beta && (
                <Badge variant="outline" className="text-[10px]">
                  {t("common.beta")}
                </Badge>
              )}
              {template.catalogue && (
                <Badge
                  variant="secondary"
                  className="max-w-24 shrink truncate text-[10px]"
                  title={template.catalogue.name}
                >
                  <span className="truncate">{template.catalogue.name}</span>
                </Badge>
              )}
            </div>
            <p className="truncate text-xs text-muted-foreground">{installs(template, t)}</p>
          </div>
        </div>
        <CardDescription className="line-clamp-2">{template.description}</CardDescription>
      </CardHeader>
      <CardContent className="mt-auto flex items-center gap-2 px-4">
        <Button
          size="sm"
          variant="secondary"
          className="flex-1"
          aria-label={t("templates.installNamed", { name: template.name })}
          onClick={onInstall}
        >
          {t("templates.install")}
        </Button>
        {template.website && (
          <Button variant="ghost" size="icon-sm" asChild>
            <a
              href={template.website}
              target="_blank"
              rel="noreferrer"
              aria-label={t("templates.websiteNamed", { name: template.name })}
              title={t("templates.website")}
            >
              <ExternalLinkIcon />
            </a>
          </Button>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * What a template actually installs, in one line.
 *
 * For one app, the version: every template names one, none of them runs
 * `latest`, and "WordPress" alone does not say which WordPress. For a stack,
 * the count instead — joining four tags with a dot read as "3210 · 6791 ·
 * 26.2.4.23 · postgres", which says nothing and looks like a fault. The
 * databases stay either way, because a template that brings one is a bigger
 * thing to install than a template that does not.
 */
function installs(template: Template, t: TFunction): string {
  // Defaulted rather than trusted. The panel answered `"databases": null` for
  // every template without one — 157 of them — and iterating that threw, which
  // put an error boundary where the catalogue should be. The server sends an
  // array now; a client that falls over when a field is not the shape it
  // expected is the other half of that bug, and this is the other half's fix.
  const services = template.services ?? []
  const parts =
    services.length === 1
      ? [versionOf(services[0].image)]
      : [t("common.app", { count: services.length })]
  for (const database of template.databases ?? []) {
    parts.push(t(`templates.engines.${database.engine}`, { defaultValue: database.engine }))
  }
  return parts.join(" · ")
}

/**
 * The tag of an image reference, or the whole reference when it has none.
 *
 * A registry host may carry a port — `registry:5000/app` — so the tag is what
 * follows the last colon, and only when no slash follows it. A tag that is a
 * version is written the same way whether the project tags it `v1.1.11` or
 * `1.16`: with the v, so it reads as a version and not as a count.
 */
function versionOf(image: string): string {
  const colon = image.lastIndexOf(":")
  if (colon < 0 || image.slice(colon).includes("/")) return image
  const tag = image.slice(colon + 1).replace(/^v(?=\d)/i, "")
  return /^\d/.test(tag) ? `v${tag}` : tag
}

/**
 * The colours a template without a logo is given, so that a page of letters is
 * not a page of grey squares. Chosen from the template's id, so a template has
 * the same colour every time it is shown and in every language.
 */
const TONES = [
  "bg-blue-500/10 text-blue-700 dark:text-blue-300",
  "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  "bg-amber-500/10 text-amber-700 dark:text-amber-300",
  "bg-violet-500/10 text-violet-700 dark:text-violet-300",
  "bg-rose-500/10 text-rose-700 dark:text-rose-300",
  "bg-cyan-500/10 text-cyan-700 dark:text-cyan-300",
]

function toneFor(id: string): string {
  let hash = 0
  for (const char of id) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  return TONES[hash % TONES.length]
}

/**
 * A template's logo, or its first letter.
 *
 * The logos are served by the panel out of its own binary — not a CDN — so
 * they work with no outbound network and tell nobody else which self-hosted
 * apps somebody is browsing.
 *
 * The letter is not only the fallback for a template the collection has no logo
 * for: it is also what is shown if the picture fails to load, which is why
 * `onError` clears the flag rather than leaving a broken image icon on the card.
 * A logo sits on a white plate whatever the theme, because a logo drawn in dark
 * ink on a dark card is not there.
 */
function TemplateIcon({ template }: { template: Template }) {
  const [broken, setBroken] = useState(false)
  const shell = "flex size-10 shrink-0 items-center justify-center rounded-lg"

  // The name is right beside it, so this is decoration and a screen reader
  // should skip it in both shapes.
  if (!template.icon || broken) {
    return (
      <span aria-hidden className={cn(shell, "text-base font-semibold", toneFor(template.id))}>
        {template.name.slice(0, 1).toUpperCase()}
      </span>
    )
  }
  return (
    <span aria-hidden className={cn(shell, "overflow-hidden border bg-white p-1.5")}>
      <img
        src={iconURL(template)}
        alt=""
        loading="lazy"
        className="size-full object-contain"
        onError={() => setBroken(true)}
      />
    </span>
  )
}

/**
 * Where a template's logo is served from. Always the panel: one from a team's
 * own catalogue was fetched from the catalogue's host when the catalogue was,
 * so the browser is never sent anywhere else to draw it.
 */
function iconURL(template: Template): string {
  const path = `/api/templates/${encodeURIComponent(template.id)}/icon`
  return template.catalogue
    ? `${path}?catalogue=${encodeURIComponent(template.catalogue.id)}`
    : path
}

/** What POST /api/templates/{id}/install answers with. */
type Installed = { apps: { id: string }[]; databases: { id: string }[]; notes?: string }

function InstallDialog({ template, onClose }: { template: Template; onClose: () => void }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const navigate = useNavigate()
  const [environmentId, setEnvironmentId] = useState("")
  const [name, setName] = useState(template.name)
  const [values, setValues] = useState<Record<string, string>>(
    Object.fromEntries((template.inputs ?? []).map((input) => [input.key, input.default ?? ""])),
  )

  const projects = useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: Boolean(team),
  })

  const projectItems = useMemo(() => projects.data?.items ?? [], [projects.data])
  // See below: `chosen` is what the form actually installs into.
  const environmentQueries = useQueries({
    queries: projectItems.map((project) => ({
      queryKey: ["environments", project.id],
      queryFn: () => api.get<List<Environment>>(`/api/projects/${project.id}/environments`),
    })),
  })

  const environments = useMemo(() => {
    const out: { environment: Environment; project: Project }[] = []
    environmentQueries.forEach((query, index) => {
      for (const environment of query.data?.items ?? []) {
        out.push({ environment, project: projectItems[index] })
      }
    })
    return out
  }, [environmentQueries, projectItems])

  // One click means one click. Almost every panel has exactly one environment —
  // first-run setup makes it — and the form opened with the picker empty and
  // the button greyed out, so "install" started with a decision that had one
  // possible answer. Derived rather than copied into state by an effect: when
  // there are several, nothing is picked for you, because installing into the
  // wrong environment is not a mistake anybody notices straight away.
  const chosen = environmentId || (environments.length === 1 ? environments[0].environment.id : "")

  const install = useMutation({
    mutationFn: () =>
      api.post<Installed>(`/api/templates/${encodeURIComponent(template.id)}/install`, {
        environment_id: chosen,
        // Which catalogue it is in: an id is only unique within one, and the
        // panel looks for it among this team's own and nobody else's.
        ...(template.catalogue ? { catalogue_id: template.catalogue.id } : {}),
        name: name.trim(),
        // A generated value is filled in by the panel, so anything left empty
        // is sent empty rather than as an accidental literal.
        values,
      }),
    // Land on the thing that was made, not on the list it is somewhere inside.
    // One app has a page of its own; a stack does not, so its project is the
    // nearest place that shows all of it at once. Sending everybody to
    // /projects meant a four-app install finished with no sign of where it
    // went.
    onSuccess: (result) => {
      const apps = result?.apps ?? []
      toast.success(
        apps.length > 1
          ? t("templates.installedApps", { count: apps.length })
          : t("templates.installedNote"),
      )
      onClose()
      const project = environments.find((entry) => entry.environment.id === chosen)?.project
      // The notes ride along so the shell can show them where you land: they
      // are the steps Skifity cannot do for you, and a dialog you just closed
      // is the one place they are of no use.
      const state = result?.notes ? { installedNotes: result.notes } : undefined
      if (apps.length === 1) navigate(`/apps/${apps[0].id}`, { state })
      else if (project) navigate(`/projects/${project.id}`, { state })
      else navigate("/projects", { state })
    },
  })

  // Inputs the panel generates itself do not need to be asked for.
  const asked = (template.inputs ?? []).filter((input) => !input.generate)

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-center gap-2">
            {template.name}
            {template.catalogue && <Badge variant="secondary">{template.catalogue.name}</Badge>}
          </DialogTitle>
          <DialogDescription>{template.description}</DialogDescription>
        </DialogHeader>

        <form
          onSubmit={(event) => {
            event.preventDefault()
            install.mutate()
          }}
        >
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="install-environment">{t("templates.installWhere")}</FieldLabel>
              {environments.length === 0 ? (
                <FieldDescription>{t("projects.emptyHelp")}</FieldDescription>
              ) : (
                <Select value={chosen} onValueChange={setEnvironmentId}>
                  <SelectTrigger id="install-environment">
                    <SelectValue placeholder={t("projects.environments")} />
                  </SelectTrigger>
                  <SelectContent>
                    {environments.map(({ environment, project }) => (
                      <SelectItem key={environment.id} value={environment.id}>
                        {project.name} · {environment.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>

            <Field>
              <FieldLabel htmlFor="install-name">{t("apps.appName")}</FieldLabel>
              <Input
                id="install-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </Field>

            {asked.map((input) => (
              <Field key={input.key}>
                <FieldLabel htmlFor={`input-${input.key}`}>
                  {input.label}
                  {!input.required && (
                    <span className="text-muted-foreground"> ({t("common.optional")})</span>
                  )}
                </FieldLabel>
                <Input
                  id={`input-${input.key}`}
                  type={input.secret ? "password" : "text"}
                  value={values[input.key] ?? ""}
                  onChange={(event) => setValues({ ...values, [input.key]: event.target.value })}
                  required={input.required}
                />
                {input.help && <FieldDescription>{input.help}</FieldDescription>}
              </Field>
            ))}

            {template.notes && (
              <Alert>
                <InfoIcon />
                <AlertDescription>{template.notes}</AlertDescription>
              </Alert>
            )}

            {install.error != null && <ErrorDisplay error={install.error} compact />}
          </FieldGroup>

          <DialogFooter className="pt-4">
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!chosen || install.isPending}>
              {install.isPending && <Spinner />}
              {install.isPending ? t("templates.installing") : t("templates.install")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
