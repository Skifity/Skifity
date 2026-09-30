import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"

import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { Project, Role, User } from "@/lib/types"

/** Who can be limited to projects: admins and owners look after the whole team. */
export function canLimit(role: Role): boolean {
  return role === "member" || role === "viewer"
}

/** The team's projects, which the limits are chosen from and named by. */
export function useTeamProjects() {
  const { team } = useSession()
  return useQuery({
    queryKey: ["projects", team?.id],
    queryFn: () => api.get<List<Project>>(`/api/teams/${team!.id}/projects`),
    enabled: Boolean(team),
  })
}

/** The select every role is picked from, in order, lowest first. */
export function RoleSelect({
  id,
  value,
  onChange,
}: {
  id: string
  value: Role
  onChange: (role: Role) => void
}) {
  const { t } = useTranslation()
  return (
    <Select value={value} onValueChange={(next) => onChange(next as Role)}>
      <SelectTrigger id={id}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="viewer">{t("settings.roleViewer")}</SelectItem>
        <SelectItem value="member">{t("settings.roleMember")}</SelectItem>
        <SelectItem value="admin">{t("settings.roleAdmin")}</SelectItem>
        <SelectItem value="owner">{t("settings.roleOwner")}</SelectItem>
      </SelectContent>
    </Select>
  )
}

/**
 * The switch that limits somebody to some projects, and the projects.
 *
 * Only offered for the roles that can be limited, so the form never holds a
 * limit the server will refuse. What is sent is decided by the caller with
 * limitFor: a limit that is off, or a role that cannot have one, is the whole
 * team.
 */
export function ProjectLimitField({
  id,
  role,
  limited,
  onLimitedChange,
  chosen,
  onChosenChange,
}: {
  id: string
  role: Role
  limited: boolean
  onLimitedChange: (limited: boolean) => void
  chosen: string[]
  onChosenChange: (chosen: string[]) => void
}) {
  const { t } = useTranslation()
  const projects = useTeamProjects()
  if (!canLimit(role)) return null

  return (
    <div className="space-y-3 rounded-lg border p-3">
      <Field orientation="horizontal">
        <Switch id={`${id}-limited`} checked={limited} onCheckedChange={onLimitedChange} />
        <div className="space-y-0.5">
          <FieldLabel htmlFor={`${id}-limited`} className="font-normal">
            {t("settings.limitToProjects")}
          </FieldLabel>
          <FieldDescription>{t("settings.limitToProjectsHelp")}</FieldDescription>
        </div>
      </Field>
      {limited && (
        <div className="space-y-2 pl-1">
          {(projects.data?.items ?? []).map((project) => {
            const box = `${id}-project-${project.id}`
            return (
              <Field key={project.id} orientation="horizontal">
                <Checkbox
                  id={box}
                  checked={chosen.includes(project.id)}
                  onCheckedChange={(value) =>
                    onChosenChange(
                      value === true
                        ? [...chosen, project.id]
                        : chosen.filter((other) => other !== project.id),
                    )
                  }
                />
                <FieldLabel htmlFor={box} className="font-normal">
                  {project.name}
                </FieldLabel>
              </Field>
            )
          })}
          {chosen.length === 0 && (
            <p className="text-xs text-muted-foreground">{t("settings.limitChooseOne")}</p>
          )}
        </div>
      )}
    </div>
  )
}

/** What to send for a limit: the projects, or nothing for the whole team. */
export function limitFor(role: Role, limited: boolean, chosen: string[]): string[] | undefined {
  return canLimit(role) && limited ? chosen : undefined
}

/** A member's access in words: their projects by name, or the whole team. */
export function AccessSummary({ scoped, projects }: { scoped: boolean; projects?: string[] }) {
  const { t } = useTranslation()
  const all = useTeamProjects()
  if (!scoped) return <span>{t("settings.allProjects")}</span>
  const names = (projects ?? [])
    .map((id) => all.data?.items.find((project) => project.id === id)?.name)
    .filter(Boolean)
  return <span>{names.length > 0 ? names.join(", ") : t("settings.noProjectsLeft")}</span>
}

/**
 * Changing somebody's role or limits, after they have joined.
 *
 * The members endpoint both adds and changes, and sending it without projects
 * lifts a limit, so the dialog always sends the whole of what it shows.
 */
export function ChangeAccessDialog({
  member,
  open,
  onOpenChange,
}: {
  member: { user: User; role: Role; scoped: boolean; projects?: string[] }
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const [role, setRole] = useState<Role>(member.role)
  const [limited, setLimited] = useState(member.scoped)
  const [chosen, setChosen] = useState<string[]>(member.projects ?? [])

  const save = useMutation({
    mutationFn: () =>
      api.post(`/api/teams/${team!.id}/members`, {
        email: member.user.email,
        role,
        projects: limitFor(role, limited, chosen),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["members", team?.id] })
      onOpenChange(false)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("settings.changeAccess")}</DialogTitle>
          <DialogDescription>{member.user.name || member.user.email}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="access-role">{t("settings.memberRole")}</Label>
            <RoleSelect id="access-role" value={role} onChange={setRole} />
          </div>
          <ProjectLimitField
            id="access"
            role={role}
            limited={limited}
            onLimitedChange={setLimited}
            chosen={chosen}
            onChosenChange={setChosen}
          />
          {save.error != null && <ErrorDisplay error={save.error} compact />}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={save.isPending || (canLimit(role) && limited && chosen.length === 0)}
            onClick={() => save.mutate()}
          >
            {save.isPending && <Spinner />}
            {save.isPending ? t("common.saving") : t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
