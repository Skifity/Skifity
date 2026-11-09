import { useTranslation } from "react-i18next"
import { HammerIcon } from "lucide-react"

import { DeployButton } from "@/components/app/deployments-tab"
import { settingLabel } from "@/components/app/setting-label"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import type { AppStatus } from "@/lib/types"

/**
 * Saved settings the running version was not built with: a new branch, build
 * command or output folder, an image app's new image, a build-time variable.
 * Saving them applies nothing, because they go into the image; without this
 * the page said "Saved" and went on running the old build. Read from the
 * status the page already asks for, so it goes as soon as a deploy has them.
 */
export function PendingChangesNotice({ appId, status }: { appId: string; status?: AppStatus }) {
  const { t } = useTranslation()
  const changes = status?.pending_changes ?? []
  if (changes.length === 0) return null
  const names = changes
    .map((change) =>
      change.setting === "variable"
        ? t("apps.pendingVariable", { key: change.key })
        : settingLabel(t, change.setting),
    )
    .join(", ")
  return (
    <Alert variant="info">
      <HammerIcon />
      <AlertTitle>{t("apps.pendingTitle")}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t("apps.pendingBody", { names })}</p>
        <DeployButton appId={appId} />
      </AlertDescription>
    </Alert>
  )
}
