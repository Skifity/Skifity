import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { DownloadIcon, KeyRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"

/** Hands the browser a file to save, made from text the page already has. */
export function downloadText(filename: string, text: string, type = "text/plain") {
  const blob = new Blob([text], { type })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

/**
 * The recovery key, on screen: a way to keep it, and a way to say it is kept.
 *
 * After a rotation the key is a different one, and the copy somebody saved
 * before opens nothing that was re-encrypted. It used to appear in the card
 * with a success toast and nothing else, so the old one stayed on paper until
 * the day a restore needed it. The panel now asks every administrator to save
 * it again, and this is where they say they have.
 */
export function RecoveryKeyShown({
  recoveryKey,
  rotated = false,
}: {
  recoveryKey: string
  rotated?: boolean
}) {
  const { t } = useTranslation()
  const { user, refresh } = useSession()

  const acknowledge = useMutation({
    mutationFn: () => api.post("/api/security/recovery-key/saved"),
    onSuccess: () => {
      toast.success(t("settings.recoveryKeySaved"))
      void refresh()
    },
  })

  return (
    <div className="space-y-3">
      {rotated && (
        <Alert variant="warning">
          <KeyRoundIcon />
          <AlertTitle>{t("settings.rotateKeyNewTitle")}</AlertTitle>
          <AlertDescription>{t("settings.rotateKeyNew")}</AlertDescription>
        </Alert>
      )}
      <pre className="rounded-md border bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap">
        {recoveryKey}
      </pre>
      <Alert>
        <AlertTitle>{t("settings.recoveryKeyKeepTitle")}</AlertTitle>
        <AlertDescription>{t("settings.recoveryKeyKeep")}</AlertDescription>
      </Alert>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => downloadText("skifity-recovery-key.txt", `${recoveryKey}\n`)}
        >
          <DownloadIcon className="size-4" />
          {t("auth.download")}
        </Button>
        {user?.recovery_saved === false && (
          <Button size="sm" disabled={acknowledge.isPending} onClick={() => acknowledge.mutate()}>
            {acknowledge.isPending && <Spinner />}
            {t("auth.recoveryKeyConfirm")}
          </Button>
        )}
      </div>
      {acknowledge.error != null && <ErrorDisplay error={acknowledge.error} compact />}
    </div>
  )
}
