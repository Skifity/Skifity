import { useEffect, useRef } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { recordedTitle } from "@/components/app/recorded-title"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import { seesWholeTeam } from "@/lib/permissions"
import { queryClient } from "@/lib/query"
import type { App, Deployment } from "@/lib/types"

/**
 * Says when a deploy you started has ended, wherever in the panel you are.
 *
 * Somebody who presses Deploy and goes to look at something else for the ten
 * minutes a build takes was told nothing: the result was on the app's
 * Deployments tab, and on a notification channel if the team has one. Now the
 * deploy ending raises one toast, with a link to its build, for the person who
 * started it — not for the rest of the team, whose deploys are not theirs to be
 * interrupted by. One open on that app's page is watching it already and is
 * not told twice.
 *
 * The deployments come from the team's event stream, so a member limited to
 * some projects, who is refused it, is not told this way.
 */
export function useDeployToasts() {
  const { t } = useTranslation()
  const { team, user } = useSession()
  const navigate = useNavigate()
  const location = useLocation()

  // Read when an event arrives rather than when the subscription is made, so
  // a navigation does not resubscribe.
  const where = useRef(location.pathname)
  useEffect(() => {
    where.current = location.pathname
  }, [location.pathname])

  // A deployment is published at every change of state, and again by a
  // stream that reconnected and caught up; each is told about once.
  const told = useRef(new Set<string>())

  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      deployment: (data) => {
        const deployment = data as Deployment
        if (!user || deployment.created_by !== user.id) return
        if (deployment.status !== "succeeded" && deployment.status !== "failed") return
        if (told.current.has(deployment.id)) return
        told.current.add(deployment.id)
        if (where.current === `/apps/${deployment.app_id}`) return

        const path = `/apps/${deployment.app_id}?tab=deployments&deployment=${deployment.id}`
        const show = (name: string) => {
          const options = {
            action: { label: t("deployToast.open"), onClick: () => navigate(path) },
          }
          if (deployment.status === "succeeded") {
            toast.success(
              t("deployToast.succeeded", { app: name, number: deployment.number }),
              options,
            )
          } else {
            // The cause in the person's language where the catalogue has it.
            toast.error(t("deployToast.failed", { app: name, number: deployment.number }), {
              ...options,
              description: deployment.error_code
                ? recordedTitle(t, deployment.error_code, deployment.error_message ?? "")
                : undefined,
            })
          }
        }
        // The app's name, from the cache when its page was open, else asked
        // for once; a toast without one is still worth more than none.
        queryClient
          .fetchQuery({
            queryKey: ["app", deployment.app_id],
            queryFn: () => api.get<App>(`/api/apps/${deployment.app_id}`),
            staleTime: 60_000,
          })
          .then(
            (app) => show(app.name),
            () => show(t("deployToast.anApp")),
          )
      },
    },
    Boolean(team && user) && seesWholeTeam(team),
  )
}
