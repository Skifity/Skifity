import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  AlertTriangleIcon,
  BookOpenIcon,
  ChevronDownIcon,
  LibraryIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { TemplateCatalogue } from "@/lib/types"

/**
 * The query both the page and the sheet read the team's catalogues through, so
 * the notice on the page and the list in the sheet are the same answer.
 *
 * A member limited to some projects is not shown where the team's templates
 * come from — the panel refuses them the list — so it is not asked for.
 */
export function useTemplateCatalogues() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["template-catalogues", team?.id],
    queryFn: () => api.get<List<TemplateCatalogue>>(`/api/teams/${team!.id}/template-catalogues`),
    enabled: Boolean(team) && !team?.scoped,
  })
}

/**
 * A team's own template catalogues: where they come from, when each was last
 * downloaded, and why any of its templates cannot be installed.
 *
 * Anybody who may see them sees all of that. Adding, refreshing and removing
 * one is an administrator's, as the panel enforces; the buttons are only
 * hidden here so nobody is offered something that will be refused.
 */
export function TemplateCataloguesSheet({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const catalogues = useTemplateCatalogues()
  const [adding, setAdding] = useState(false)
  const admin = team?.role === "admin" || team?.role === "owner"
  const items = catalogues.data?.items ?? []

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            <LibraryIcon className="size-4" />
            {t("templates.catalogues.title")}
          </SheetTitle>
          <SheetDescription>{t("templates.catalogues.help")}</SheetDescription>
        </SheetHeader>

        <div className="space-y-4 px-4 pb-6">
          <div className="flex flex-wrap items-center gap-2">
            {admin && !adding && (
              <Button size="sm" onClick={() => setAdding(true)}>
                <PlusIcon className="size-4" />
                {t("templates.catalogues.add")}
              </Button>
            )}
            <Button variant="ghost" size="sm" asChild>
              <a href="/docs/templates#private-catalogues" target="_blank" rel="noreferrer">
                <BookOpenIcon className="size-4" />
                {t("templates.catalogues.docs")}
              </a>
            </Button>
          </div>

          {adding && <AddCatalogueForm onDone={() => setAdding(false)} />}

          {catalogues.isLoading ? (
            <Skeleton className="h-24" />
          ) : catalogues.error ? (
            <ErrorDisplay error={catalogues.error} onRetry={() => void catalogues.refetch()} />
          ) : items.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("templates.catalogues.empty")}</p>
          ) : (
            <ul className="space-y-3">
              {items.map((catalogue) => (
                <CatalogueItem key={catalogue.id} catalogue={catalogue} admin={admin} />
              ))}
            </ul>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function refreshLists() {
  void queryClient.invalidateQueries({ queryKey: ["template-catalogues"] })
  void queryClient.invalidateQueries({ queryKey: ["templates"] })
}

function CatalogueItem({ catalogue, admin }: { catalogue: TemplateCatalogue; admin: boolean }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const confirm = useConfirm()
  const path = `/api/teams/${team?.id}/template-catalogues/${catalogue.id}`

  const refresh = useMutation({
    mutationFn: () => api.post<TemplateCatalogue>(`${path}/refresh`),
    onSuccess: () => toast.success(t("templates.catalogues.refreshed", { name: catalogue.name })),
    // A refresh that failed is recorded on the catalogue, so the list is read
    // again either way: its error is what the row shows next.
    onSettled: refreshLists,
  })
  const remove = useMutation({
    mutationFn: () => api.delete(path),
    onSuccess: refreshLists,
  })

  const problems = catalogue.problems ?? []
  return (
    <li className="space-y-3 rounded-md border p-3">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{catalogue.name}</span>
            {catalogue.format && (
              <Badge variant="outline" className="font-mono text-[10px]">
                {catalogue.format}
              </Badge>
            )}
          </div>
          <code className="block truncate font-mono text-xs text-muted-foreground">
            {catalogue.url}
          </code>
          <p className="text-xs text-muted-foreground">
            {t("templates.catalogues.fetched", { when: formatRelative(catalogue.fetched_at) })}
            {" · "}
            {t("templates.catalogues.installable", { count: catalogue.templates })}
            {catalogue.auth_header_name && (
              <>
                {" · "}
                {t("templates.catalogues.withHeader", { header: catalogue.auth_header_name })}
              </>
            )}
          </p>
        </div>
        {admin && (
          <div className="flex shrink-0 gap-1">
            <Button
              variant="ghost"
              size="icon"
              aria-label={t("templates.catalogues.refresh", { name: catalogue.name })}
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              {refresh.isPending ? <Spinner /> : <RefreshCwIcon className="size-4" />}
            </Button>
            <Button
              variant="ghost"
              size="icon"
              aria-label={t("templates.catalogues.remove", { name: catalogue.name })}
              disabled={remove.isPending}
              onClick={() =>
                void confirm({
                  title: t("templates.catalogues.remove", { name: catalogue.name }),
                  description: t("templates.catalogues.removeConfirm"),
                  confirmLabel: t("common.delete"),
                  destructive: true,
                }).then((yes) => {
                  if (yes) remove.mutate()
                })
              }
            >
              <Trash2Icon className="size-4" />
            </Button>
          </div>
        )}
      </div>

      {catalogue.last_error && (
        <Alert variant="destructive">
          <AlertTriangleIcon />
          <AlertTitle>{t("templates.catalogues.lastError")}</AlertTitle>
          <AlertDescription className="break-words">{catalogue.last_error}</AlertDescription>
        </Alert>
      )}

      {problems.length > 0 && (
        <Collapsible>
          <CollapsibleTrigger asChild>
            <Button variant="outline" size="sm" className="group w-full justify-between">
              {t("templates.catalogues.problems", { count: problems.length })}
              <ChevronDownIcon className="size-4 transition-transform group-data-[state=open]:rotate-180" />
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul className="mt-2 space-y-2">
              {problems.map((problem) => (
                <li key={problem.file} className="rounded-md bg-muted/50 p-2 text-xs">
                  <p className="font-medium">
                    {problem.name || problem.id || problem.file}
                    <span className="ml-2 font-mono text-muted-foreground">{problem.file}</span>
                  </p>
                  {/*
                    The panel's own words, from the checks every template is
                    held to: written for whoever maintains the catalogue, and
                    kept as they are, like a build log.
                  */}
                  <ul className="mt-1 list-disc space-y-0.5 pl-4 text-muted-foreground">
                    {problem.errors.map((error) => (
                      <li key={error} className="break-words">
                        {error}
                      </li>
                    ))}
                  </ul>
                </li>
              ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      )}

      {refresh.error != null && <ErrorDisplay error={refresh.error} compact />}
      {remove.error != null && <ErrorDisplay error={remove.error} compact />}
    </li>
  )
}

function AddCatalogueForm({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const [name, setName] = useState("")
  const [url, setUrl] = useState("")
  const [headerName, setHeaderName] = useState("")
  const [headerValue, setHeaderValue] = useState("")

  const add = useMutation({
    mutationFn: () =>
      api.post<TemplateCatalogue>(`/api/teams/${team!.id}/template-catalogues`, {
        name: name.trim(),
        url: url.trim(),
        // Sent only when both are there; the panel refuses half a header.
        ...(headerName.trim() || headerValue
          ? { auth_header_name: headerName.trim(), auth_header_value: headerValue }
          : {}),
      }),
    onSuccess: (added) => {
      toast.success(t("templates.catalogues.added", { name: added.name, count: added.templates }))
      refreshLists()
      onDone()
    },
  })

  return (
    <form
      className="rounded-md border p-3"
      onSubmit={(event) => {
        event.preventDefault()
        add.mutate()
      }}
    >
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="catalogue-name">{t("templates.catalogues.name")}</FieldLabel>
          <Input
            id="catalogue-name"
            value={name}
            maxLength={40}
            onChange={(event) => setName(event.target.value)}
          />
          <FieldDescription>{t("templates.catalogues.nameHelp")}</FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="catalogue-url">{t("templates.catalogues.url")}</FieldLabel>
          <Input
            id="catalogue-url"
            type="url"
            value={url}
            spellCheck={false}
            placeholder="https://"
            onChange={(event) => setUrl(event.target.value)}
          />
          <FieldDescription>{t("templates.catalogues.urlHelp")}</FieldDescription>
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="catalogue-header">
              {t("templates.catalogues.headerName")}
              <span className="text-muted-foreground"> ({t("common.optional")})</span>
            </FieldLabel>
            <Input
              id="catalogue-header"
              value={headerName}
              spellCheck={false}
              autoComplete="off"
              onChange={(event) => setHeaderName(event.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="catalogue-header-value">
              {t("templates.catalogues.headerValue")}
            </FieldLabel>
            <Input
              id="catalogue-header-value"
              type="password"
              value={headerValue}
              autoComplete="new-password"
              onChange={(event) => setHeaderValue(event.target.value)}
            />
          </Field>
        </div>
        <FieldDescription>{t("templates.catalogues.headerHelp")}</FieldDescription>

        {add.error != null && <ErrorDisplay error={add.error} compact />}

        <div className="flex gap-2">
          <Button type="submit" disabled={add.isPending || !name.trim() || !url.trim()}>
            {add.isPending && <Spinner />}
            {add.isPending ? t("templates.catalogues.adding") : t("templates.catalogues.save")}
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            {t("common.cancel")}
          </Button>
        </div>
      </FieldGroup>
    </form>
  )
}
