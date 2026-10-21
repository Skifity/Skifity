import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { LockIcon, ShieldCheckIcon, ShieldXIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { Backup } from "@/lib/types"

/**
 * Whether a backup is sealed, and whether it has been shown to open and read
 * through — and the button that checks. The check runs in the background, so
 * the list is read again a few times afterwards rather than waited on.
 */
export function BackupVerification({
  backup,
  verifyPath,
  listKey,
  canVerify = true,
}: {
  backup: Backup
  /** The route that verifies this backup, from where it is listed. */
  verifyPath: string
  /** The query the list comes from, read again as the check finishes. */
  listKey: readonly unknown[]
  /** Whether this person may ask for a check; a viewer still sees the result. */
  canVerify?: boolean
}) {
  const { t } = useTranslation()
  const verify = useMutation({
    // Called with when the backup was last verified: every check that ends
    // writes a new time, which is how the button knows this one has.
    mutationFn: (_lastVerified: string) => api.post(verifyPath),
    onSuccess: () => {
      for (const delay of [3_000, 10_000, 30_000]) {
        setTimeout(() => void queryClient.invalidateQueries({ queryKey: listKey }), delay)
      }
    },
  })
  if (backup.status !== "succeeded") return null
  const lastVerified = backup.verified_at ?? ""
  // Asked, and no result written since: derived from the list, so the button
  // comes back as soon as the check that was asked for has an answer.
  const waiting = verify.isPending || (verify.isSuccess && verify.variables === lastVerified)

  return (
    <div className="mt-1 space-y-1.5">
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        {backup.encrypted && (
          <Badge variant="outline" className="gap-1">
            <LockIcon className="size-3" />
            {t("backups.sealed")}
          </Badge>
        )}
        {backup.verify_error ? (
          <span className="flex items-center gap-1 text-destructive">
            <ShieldXIcon className="size-3.5" />
            {t("backups.verifyFailed", { reason: backup.verify_error })}
          </span>
        ) : backup.verified_at ? (
          <span className="flex items-center gap-1 text-success">
            <ShieldCheckIcon className="size-3.5" />
            {t("backups.verified", { when: formatRelative(backup.verified_at) })}
          </span>
        ) : null}
        {canVerify && (
          <Button
            variant="link"
            size="sm"
            className="h-auto p-0 text-xs"
            disabled={waiting}
            onClick={() => verify.mutate(lastVerified)}
          >
            {waiting && <Spinner />}
            {waiting ? t("backups.verifying") : t("backups.verify")}
          </Button>
        )}
      </div>
      {verify.error != null && <ErrorDisplay error={verify.error} compact />}
    </div>
  )
}
