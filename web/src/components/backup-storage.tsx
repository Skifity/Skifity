import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { DatabaseBackupIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import type { BackupStorage } from "@/lib/types"

/**
 * Whether backups have somewhere to go. Anybody may ask, and the answer says
 * nothing about where: that is a panel administrator's, on a page nobody
 * else can open.
 */
export function useBackupStorage() {
  return useQuery({
    queryKey: ["backup-storage"],
    queryFn: () => api.get<BackupStorage>("/api/backup-storage"),
    staleTime: 60_000,
  })
}

/**
 * Where backup storage is set: a link for the one person who can follow it,
 * and who to ask for everybody else. It used to say "Settings, then Storage"
 * to people who had no such tab.
 */
export function BackupStorageWhere() {
  const { t } = useTranslation()
  const { user } = useSession()
  if (!user?.is_admin) return <>{t("backups.storage.askAdmin")}</>
  return (
    <Link to="/settings?tab=panel" className="underline underline-offset-2 hover:text-primary">
      {t("backups.storage.openSettings")}
    </Link>
  )
}

/** Said where a backup would be offered, when there is nowhere to put one. */
export function BackupStorageMissing() {
  const { t } = useTranslation()
  return (
    <Alert variant="warning">
      <DatabaseBackupIcon />
      <AlertTitle>{t("backups.storage.missingTitle")}</AlertTitle>
      <AlertDescription>
        <p>
          {t("backups.storage.missing")} <BackupStorageWhere />
        </p>
      </AlertDescription>
    </Alert>
  )
}
