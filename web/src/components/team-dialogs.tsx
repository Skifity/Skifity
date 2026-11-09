import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { toast } from "sonner"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay, toProblem } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import { translated } from "@/lib/say"
import type { Team } from "@/lib/types"

/**
 * A new team, which the person creating it owns and is taken to.
 *
 * Mounted only while open, so it starts empty every time rather than keeping
 * the last name typed into it.
 */
export function NewTeamDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const { setTeam, refresh } = useSession()
  const navigate = useNavigate()
  const [name, setName] = useState("")

  const create = useMutation({
    mutationFn: () => api.post<Team>("/api/teams", { name: name.trim() }),
    onSuccess: async (team) => {
      // The session lists the new team once it is asked again; choosing it
      // first means the shell shows it the moment it is there.
      setTeam(team)
      await refresh()
      toast.success(t("teams.created", { name: team.name }))
      onClose()
      navigate("/")
    },
  })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("teams.new")}</DialogTitle>
          <DialogDescription>{t("teams.newHelp")}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="team-name">{t("teams.name")}</FieldLabel>
            <Input
              id="team-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
              required
            />
            <FieldDescription>{t("teams.nameHelp")}</FieldDescription>
          </Field>
          {create.error != null && <ErrorDisplay error={create.error} compact />}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending && <Spinner />}
              {create.isPending ? t("common.saving") : t("teams.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Leaving a team, asked about first, from the team menu: the one place every
 * member can reach, since Settings is for those who manage the team.
 *
 * Afterwards the panel shows another of their teams, or the empty state when
 * there is none, from the overview: whatever page was open belonged to the
 * team they left.
 */
export function useLeaveTeam() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const { refresh } = useSession()
  const navigate = useNavigate()

  const leave = useMutation({
    mutationFn: (team: Team) => api.post(`/api/teams/${team.id}/leave`),
    onSuccess: async (_, team) => {
      await refresh()
      toast.success(t("teams.left", { name: team.name }))
      navigate("/")
    },
    // A menu has no room for the error card, so the refusal — the last owner,
    // most likely — is a toast in the reader's language, with its fix.
    onError: (error) => {
      const problem = toProblem(error)
      if (!problem) return
      const key = `errors.catalogue.${problem.code.replaceAll(".", "_")}`
      toast.error(translated(t, `${key}.title`, problem.title, problem.args?.title), {
        description: translated(t, `${key}.fix`, problem.fix, problem.args?.fix) || undefined,
      })
    },
  })

  return (team: Team) => {
    void confirm({
      title: t("teams.leaveTitle", { name: team.name }),
      description: t("teams.leaveConfirm"),
      confirmLabel: t("teams.leave"),
      destructive: true,
    }).then((yes) => {
      if (yes) leave.mutate(team)
    })
  }
}
