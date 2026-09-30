import { useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { FolderIcon, PlusIcon } from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
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
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { Project } from "@/lib/types"

export function ProjectsPage() {
  const { t } = useTranslation()
  const { team } = useSession()
  const [creating, setCreating] = useState(false)

  // Both are published and neither was listened for, so a project created in
  // another tab — or by the CLI, or by an assistant over MCP — did not appear.
  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      "project.created": () =>
        void queryClient.invalidateQueries({ queryKey: ["projects", team?.id] }),
      "project.deleted": () =>
        void queryClient.invalidateQueries({ queryKey: ["projects", team?.id] }),
    },
    Boolean(team),
  )

  const projects = useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: Boolean(team),
  })

  return (
    <Page>
      <PageHeader
        title={t("projects.title")}
        description={t("projects.subtitle")}
        actions={
          <Button onClick={() => setCreating(true)}>
            <PlusIcon />
            {t("projects.newProject")}
          </Button>
        }
      />

      {projects.isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-32" />
          <Skeleton className="h-32" />
          <Skeleton className="h-32" />
        </div>
      ) : projects.error ? (
        <ErrorDisplay error={projects.error} onRetry={() => void projects.refetch()} />
      ) : (projects.data?.items.length ?? 0) === 0 ? (
        <EmptyState
          icon={FolderIcon}
          title={t("projects.empty")}
          description={t("projects.emptyHelp")}
          action={
            <Button onClick={() => setCreating(true)}>
              <PlusIcon className="size-4" />
              {t("projects.newProject")}
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {projects.data?.items.map((project) => (
            <Link key={project.id} to={`/projects/${project.id}`} className="group">
              <Card className="h-full transition-colors group-hover:border-primary/50">
                <CardHeader>
                  <CardTitle className="truncate text-base">{project.name}</CardTitle>
                  <CardDescription className="line-clamp-2">
                    {project.description || t("projects.emptyHelp")}
                  </CardDescription>
                </CardHeader>
                <CardContent className="text-xs text-muted-foreground">
                  {t("common.created")} {formatRelative(project.created_at)}
                </CardContent>
              </Card>
            </Link>
          ))}
        </div>
      )}

      <NewProjectDialog open={creating} onOpenChange={setCreating} />
    </Page>
  )
}

function NewProjectDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const navigate = useNavigate()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")

  const create = useMutation({
    mutationFn: () =>
      api.post<Project>(`/api/teams/${team!.id}/projects`, {
        name: name.trim(),
        description: description.trim(),
      }),
    // Straight into the project, the way creating a database goes straight to
    // the database. Nobody makes a project to look at a list of projects: the
    // next thing is always the first app, and that button is in there.
    onSuccess: (project) => {
      void queryClient.invalidateQueries({ queryKey: ["projects", team?.id] })
      setName("")
      setDescription("")
      onOpenChange(false)
      navigate(`/projects/${project.id}`)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("projects.newProject")}</DialogTitle>
          <DialogDescription>{t("projects.emptyHelp")}</DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="project-name">{t("projects.projectName")}</FieldLabel>
            <Input
              id="project-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="project-description">
              {t("projects.description")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Textarea
              id="project-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={3}
            />
          </Field>

          {create.error && <ErrorDisplay error={create.error} compact />}

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending && <Spinner />}
              {create.isPending ? t("common.saving") : t("common.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
