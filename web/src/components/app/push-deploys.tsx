import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { Link, useNavigate, type NavigateFunction } from "react-router-dom"
import { useMutation, useMutationState } from "@tanstack/react-query"
import { GitBranchIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import type { App, WebhookStatus } from "@/lib/types"

/** Where somebody pushed and nothing happened is answered. */
const PUSHED_AND_NOTHING = "/docs/troubleshooting#i-pushed-and-nothing-happened"

/**
 * Says whether pushes will deploy the app, as the server found when it asked
 * the repository. Said now rather than the first time somebody pushes and
 * nothing happens: on creating an app, on saving its settings, and on asking
 * again.
 */
export function sayWebhook(t: TFunction, navigate: NavigateFunction, webhook?: WebhookStatus) {
  if (!webhook) return
  // An app made from a public repository deploys on push by default, and
  // with no connection nothing was ever registered to make that true.
  if (webhook.reason_code === "no_connection") {
    toast.warning(t("git.pushDeploysNoConnection"), {
      description: t("git.pushDeploysNoConnectionHelp"),
      duration: 20000,
      action: { label: t("git.connectAccount"), onClick: () => navigate("/settings?tab=git") },
    })
    return
  }
  if (!webhook.url) return
  if (webhook.registered) {
    toast.success(t("git.pushDeploysOn"), { description: t("git.pushDeploysOnHelp") })
    return
  }
  toast.warning(t("git.pushDeploysManual"), {
    description: `${t("git.pushDeploysManualHelp")} ${webhook.url}`,
    duration: 15000,
    action: {
      label: t("git.pushDeploysWhatToCheck"),
      onClick: () => window.open(PUSHED_AND_NOTHING, "_blank", "noreferrer"),
    },
  })
}

/**
 * The key both of the settings tab's answers about the webhook are kept
 * under: a save's, which carries one when it asked the repository, and
 * Register again's. Kept by the mutation cache rather than by the form, which
 * starts again from the app after every save that changes it.
 */
export function webhookAnswerKey(appId: string, kind: "save" | "register") {
  return ["app-webhook", appId, kind]
}

/** The answer about the webhook the app was last given, if any was asked for. */
function useLastWebhook(appId: string): WebhookStatus | undefined {
  const answers = useMutationState({
    filters: { mutationKey: ["app-webhook", appId], status: "success" },
    select: (mutation) =>
      mutation.options.mutationKey?.[2] === "register"
        ? (mutation.state.data as WebhookStatus | undefined)
        : (mutation.state.data as { webhook?: WebhookStatus } | undefined)?.webhook,
  })
  return answers.filter((answer) => answer != null).at(-1)
}

/**
 * What the settings tab says under Deploy on push.
 *
 * With the switch on and no Git connection chosen, that no webhook is
 * registered and why, worked out from the form as it stands. With a
 * connection whose last answer was not registered, a button to ask the
 * repository again once whatever stopped it has been put right.
 */
export function DeployOnPushNotice({
  app,
  autoDeploy,
  gitSourceID,
}: {
  app: App
  autoDeploy: boolean
  gitSourceID: string
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const last = useLastWebhook(app.id)
  const register = useMutation({
    mutationKey: webhookAnswerKey(app.id, "register"),
    mutationFn: () => api.post<WebhookStatus>(`/api/apps/${app.id}/webhook`),
    onSuccess: (webhook) => sayWebhook(t, navigate, webhook),
  })

  if (!autoDeploy) return null
  if (!gitSourceID) {
    return (
      <Alert variant="warning" data-slot="push-no-connection">
        <GitBranchIcon />
        <AlertTitle>{t("apps.pushNoConnection")}</AlertTitle>
        <AlertDescription className="space-y-3">
          <p>{t("apps.pushNoConnectionHelp")}</p>
          <div className="flex flex-wrap gap-2">
            <Button asChild size="sm" variant="outline">
              <Link to="/settings?tab=git">{t("git.connectAccount")}</Link>
            </Button>
            <Button asChild size="sm" variant="ghost">
              <a href={PUSHED_AND_NOTHING} target="_blank" rel="noreferrer">
                {t("git.pushDeploysWhatToCheck")}
              </a>
            </Button>
          </div>
        </AlertDescription>
      </Alert>
    )
  }
  // Only for the connection that is saved: asking again with one only chosen
  // in the form would ask with the old one.
  if (!last || last.registered || last.reason_code || gitSourceID !== app.git_source_id) {
    return null
  }
  return (
    <Alert variant="warning">
      <GitBranchIcon />
      <AlertTitle>{t("apps.pushNotRegistered")}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t("apps.pushNotRegisteredHelp")}</p>
        {register.error != null && <ErrorDisplay error={register.error} compact />}
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={register.isPending}
          onClick={() => register.mutate()}
        >
          {register.isPending ? <Spinner /> : <RefreshCwIcon className="size-3.5" />}
          {t("apps.registerWebhookAgain")}
        </Button>
      </AlertDescription>
    </Alert>
  )
}
