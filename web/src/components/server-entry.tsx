import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { BookOpenIcon, UnplugIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import type { NodeInfo, Server, ServerEntryReason } from "@/lib/types"

/**
 * Whether a server takes the traffic that reaches it, as the cluster says
 * (internal/kube/entry.go). A server can be ready — Kubernetes hears from it —
 * and still not answer a visitor, and round-robin DNS keeps naming its address
 * because DNS cannot tell. The panel does not change DNS records for it: the
 * ones at a round-robin name are the operator's, and it only ever changes its
 * own. It says so instead, and the docs say what it does not see.
 */

/** Where the docs say what is checked and what is not. */
const ENTRY_DOCS = "/docs/adding-servers#which-servers-are-taking-traffic"

/**
 * A server the panel has as ready that would not answer a visitor now. One
 * that is not ready says so already, and louder: the watcher marks it within
 * a minute of the cluster noticing, so that reason is left to it.
 */
export function notTakingTraffic(server: Server, node: NodeInfo | undefined): boolean {
  return (
    server.status === "ready" &&
    node?.entry === "not_serving" &&
    node.entry_reason !== "node_not_ready"
  )
}

/** What a server does with traffic, in a few words, or null when the cluster cannot say. */
export function sayEntry(t: TFunction, node: NodeInfo): string | null {
  switch (node.entry) {
    case "serving":
      return t("servers.entry.serving")
    case "not_serving":
      return t("servers.entry.notServing")
    case "none":
      return t("servers.entry.none")
  }
  return null
}

/**
 * The warning: how many servers, or this one and why; what it means for a
 * visitor whose DNS names its address; and that the panel leaves DNS alone.
 */
export function NotTakingTrafficAlert({
  count,
  reason,
}: {
  /** How many, on the list. Absent on a server's own page. */
  count?: number
  reason?: ServerEntryReason
}) {
  const { t } = useTranslation()
  return (
    <Alert variant="warning">
      <UnplugIcon />
      <AlertTitle>
        {count === undefined
          ? t("servers.entry.alertTitleThis")
          : t("servers.entry.alertTitle", { count })}
      </AlertTitle>
      <AlertDescription className="space-y-2">
        {reason && <p>{t(`servers.entry.reason.${reason}`)}</p>}
        <p>{t("servers.entry.alertHelp")}</p>
        <Button variant="ghost" size="sm" className="-ml-2" asChild>
          <a href={ENTRY_DOCS} target="_blank" rel="noreferrer">
            <BookOpenIcon className="size-3.5" />
            {t("servers.entry.docs")}
          </a>
        </Button>
      </AlertDescription>
    </Alert>
  )
}
