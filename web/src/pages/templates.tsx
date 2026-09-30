import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"
import { useMutation, useQueries, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { BoxesIcon, ExternalLinkIcon, InfoIcon, SearchIcon } from "lucide-react"
import { cn } from "cn"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
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
import { Alert, AlertDescription } from "@/components/ui/alert"
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

export function TemplatesPage() {
  const { t } = useTranslation()
  const [search, setSearch] = useState("")
  const [installing, setInstalling] = useState<Template | null>(null)

  const templates = useQuery({
    queryKey: ["templates"],
    queryFn: () => api.get<List<Template>>("/api/templates"),
  })

  const items = useMemo(() => templates.data?.items ?? [], [templates.data])
  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase()
    if (!needle) return items
    return items.filter(
      (template) =>
        template.name.toLowerCase().includes(needle) ||
        template.description.toLowerCase().includes(needle) ||
        template.category.toLowerCase().includes(needle),
    )
  }, [items, search])

  // Sorted by the name somebody reads, not by the slug underneath it: sorting
  // "cms" and "ai" alphabetically puts Websites first in English and somewhere
  // else entirely in Russian, for a reason nobody can see on screen.
  const categories = useMemo(
    () =>
      [...new Set(shown.map((template) => template.category))].sort((a, b) =>
        t(`templates.categories.${a}`, { defaultValue: a }).localeCompare(
          t(`templates.categories.${b}`, { defaultValue: b }),
        ),
      ),
    [shown, t],
  )

  return (
    <Page>
      <PageHeader title={t("templates.title")} description={t("templates.subtitle")} />

      <InputGroup className="max-w-sm">
        <InputGroupAddon>
          <SearchIcon />
        </InputGroupAddon>
        <InputGroupInput
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t("templates.search")}
        />
      </InputGroup>

      {templates.isLoading ? (
        <Skeleton className="h-64" />
      ) : templates.error ? (
        <ErrorDisplay error={templates.error} onRetry={() => void templates.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={BoxesIcon}
          title={t("templates.title")}
          description={t("templates.subtitle")}
        />
      ) : shown.length === 0 ? (
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
        categories.map((category) => (
          <section key={category} className="space-y-3">
            {/*
              The category is a slug in the file — "cms", "ai" — and showing
              the slug is how a page looks unfinished. defaultValue keeps a
              category nobody has translated yet readable rather than blank.
            */}
            <h2 className="text-sm font-medium text-muted-foreground">
              {t(`templates.categories.${category}`, { defaultValue: category })}
            </h2>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {shown
                .filter((template) => template.category === category)
                .map((template) => (
                  <Card
                    key={template.id}
                    className="flex h-full flex-col transition-colors hover:border-primary/40"
                  >
                    <CardHeader>
                      {/*
                        min-w-0 on the row, not only on the text inside it.
                        shadcn's CardHeader is a grid, and a grid item defaults
                        to `min-width: auto`, which means "never narrower than
                        my content" — so `truncate` further down never applied
                        and "Calibre Web Automated Book Downloader" pushed the
                        card 92px past the side of a 375px screen. Measured, not
                        guessed: the row was 426px wide inside a 341px cell.
                      */}
                      <div className="flex min-w-0 items-start gap-3">
                        <TemplateIcon template={template} />
                        <div className="min-w-0 flex-1">
                          {/*
                            min-w-0 again, on the row as well as on its parent.
                            A flex item will not shrink below its content unless
                            every flex ancestor says it may, so without this the
                            `truncate` below never applies: "Calibre Web
                            Automated Book Downloader" pushed the card 92px past
                            the side of a 375px screen, and the whole page moved
                            sideways under a thumb that meant to scroll down.
                          */}
                          <div className="flex min-w-0 items-start justify-between gap-2">
                            <CardTitle className="truncate text-base">{template.name}</CardTitle>
                            {template.beta && (
                              <Badge variant="outline" className="text-[10px]">
                                {t("common.beta")}
                              </Badge>
                            )}
                          </div>
                          <p className="truncate text-xs text-muted-foreground">
                            {installs(template, t)}
                          </p>
                        </div>
                      </div>
                      <CardDescription className="line-clamp-3">
                        {template.description}
                      </CardDescription>
                    </CardHeader>
                    <CardContent className="mt-auto flex items-center gap-2">
                      <Button size="sm" onClick={() => setInstalling(template)}>
                        {t("templates.install")}
                      </Button>
                      {template.website && (
                        <Button variant="ghost" size="sm" asChild>
                          <a href={template.website} target="_blank" rel="noreferrer">
                            <ExternalLinkIcon className="size-3.5" />
                            {t("templates.website")}
                          </a>
                        </Button>
                      )}
                    </CardContent>
                  </Card>
                ))}
            </div>
          </section>
        ))
      )}

      {installing && <InstallDialog template={installing} onClose={() => setInstalling(null)} />}
    </Page>
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
  for (const database of template.databases ?? []) parts.push(database.engine)
  return parts.join(" · ")
}

/**
 * The tag of an image reference, or the whole reference when it has none.
 *
 * A registry host may carry a port — `registry:5000/app` — so the tag is what
 * follows the last colon, and only when no slash follows it.
 */
function versionOf(image: string): string {
  const colon = image.lastIndexOf(":")
  if (colon < 0 || image.slice(colon).includes("/")) return image
  return image.slice(colon + 1)
}

/**
 * A template's logo, or its first letter.
 *
 * The catalogue is the first page anybody opens and it was three hundred grey
 * squares with a letter in them. The logos are served by the panel out of its
 * own binary — not a CDN — so they work with no outbound network and tell
 * nobody else which self-hosted apps somebody is browsing.
 *
 * The letter is not only the fallback for a template the collection has no logo
 * for: it is also what is shown if the picture fails to load, which is why
 * `onError` clears the flag rather than leaving a broken image icon on the card.
 */
function TemplateIcon({ template }: { template: Template }) {
  const [broken, setBroken] = useState(false)
  const shell =
    "flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted text-sm font-semibold text-muted-foreground"

  // The name is right beside it, so this is decoration and a screen reader
  // should skip it in both shapes.
  if (!template.icon || broken) {
    return (
      <span aria-hidden className={shell}>
        {template.name.slice(0, 1).toUpperCase()}
      </span>
    )
  }
  return (
    <span aria-hidden className={cn(shell, "overflow-hidden bg-background p-1.5")}>
      <img
        src={`/api/templates/${template.id}/icon`}
        alt=""
        loading="lazy"
        className="size-full object-contain"
        onError={() => setBroken(true)}
      />
    </span>
  )
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
      api.post<Installed>(`/api/templates/${template.id}/install`, {
        environment_id: chosen,
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
          <DialogTitle>{template.name}</DialogTitle>
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
