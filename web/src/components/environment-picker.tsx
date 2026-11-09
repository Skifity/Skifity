import { useMemo } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQueries } from "@tanstack/react-query"

import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { FieldDescription } from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useProjects } from "@/hooks/use-projects"
import { api, type List } from "@/lib/api"
import type { Environment, Project } from "@/lib/types"

/** An environment something can be made in, and the project it is in. */
export type EnvironmentTarget = { environment: Environment; project: Project }

/**
 * Every environment of the team's projects that something can be made in,
 * as "Project · Environment".
 *
 * A pull request's preview is left out: it is deleted when the request
 * closes, and whatever was made in it goes with it. `keep` is one that stays
 * all the same — the one a page was opened from, which somebody chose.
 *
 * Loading, failing and there being nowhere at all are three different things
 * to say, so each is returned rather than folded into an empty list.
 */
export function useEnvironmentTargets(keep?: string, enabled = true) {
  // The same query keys the project pages use, so this is their cache.
  const projects = useProjects(enabled)
  const projectItems = useMemo(() => projects.data?.items ?? [], [projects.data])
  const environmentQueries = useQueries({
    queries: projectItems.map((project) => ({
      queryKey: ["environments", project.id],
      queryFn: () => api.get<List<Environment>>(`/api/projects/${project.id}/environments`),
      enabled,
    })),
  })

  const targets = useMemo(() => {
    const out: EnvironmentTarget[] = []
    environmentQueries.forEach((query, index) => {
      for (const environment of query.data?.items ?? []) {
        if (environment.kind === "preview" && environment.id !== keep) continue
        out.push({ environment, project: projectItems[index] })
      }
    })
    return out
  }, [environmentQueries, projectItems, keep])

  return {
    targets,
    loading: projects.isLoading || environmentQueries.some((query) => query.isLoading),
    error: projects.error ?? environmentQueries.find((query) => query.error)?.error,
  }
}

/**
 * What was picked, or the only place there is.
 *
 * One click means one click: almost every panel has exactly one environment —
 * first-run setup makes it — and a form that opened with nothing picked
 * started with a decision that had one possible answer. Derived rather than
 * copied into state, and only for one: with several, nothing is picked for
 * anybody, because making something in the wrong environment — production,
 * say — is not a mistake anybody notices straight away.
 */
export function pickedOrOnly(picked: string, targets: EnvironmentTarget[]): string {
  return picked || (targets.length === 1 ? targets[0].environment.id : "")
}

/** The target with this environment's id, when it is one of them. */
export function targetOf(id: string, targets: EnvironmentTarget[]) {
  return targets.find((target) => target.environment.id === id)
}

/**
 * Where something goes, as a choice of "Project · Environment".
 *
 * `empty` is what to say when the team has no project at all, which comes
 * with the way to make one.
 */
export function EnvironmentPicker({
  id,
  value,
  onChange,
  targets,
  loading,
  error,
  empty,
  placeholder,
}: {
  id: string
  value: string
  onChange: (environmentId: string) => void
  targets: EnvironmentTarget[]
  loading: boolean
  error: unknown
  empty: string
  placeholder?: string
}) {
  const { t } = useTranslation()

  if (loading) return <Skeleton className="h-9" />
  if (error != null) return <ErrorDisplay error={error} compact />
  if (targets.length === 0) {
    return (
      <div className="flex flex-wrap items-center gap-3">
        <FieldDescription>{empty}</FieldDescription>
        <Button type="button" variant="outline" size="sm" asChild>
          <Link to="/projects">{t("projects.newProject")}</Link>
        </Button>
      </div>
    )
  }
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id={id}>
        <SelectValue placeholder={placeholder ?? t("projects.environments")} />
      </SelectTrigger>
      <SelectContent>
        {targets.map(({ environment, project }) => (
          <SelectItem key={environment.id} value={environment.id}>
            {project.name} · {environment.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
