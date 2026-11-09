import type { TFunction } from "i18next"

import { formatRelative } from "@/lib/format"
import type { InstanceExit } from "@/lib/types"

/**
 * How an instance's previous container ended, in the product's words.
 *
 * The kubelet's reason mirrors the instance's states: OOMKilled is said as
 * running out of memory, which is what it is and what nothing in the app's
 * own log says, because the process never got to.
 */
export function exitReason(t: TFunction, exit: InstanceExit): string {
  const code = exit.exit_code
  switch (exit.reason) {
    case "OOMKilled":
      return t("apps.instanceExit.outOfMemory")
    case "Completed":
      return t("apps.instanceExit.completed")
    case "ContainerCannotRun":
    case "StartError":
      return t("apps.instanceExit.cannotStart", { code })
    case "":
    case "Error":
      return t("apps.instanceExit.exitCode", { code })
    default:
      return t("apps.instanceExit.other", { reason: exit.reason, code })
  }
}

/** When it ended, as "12 minutes ago", and how. */
export function exitWords(t: TFunction, exit: InstanceExit) {
  return { when: formatRelative(exit.finished_at), reason: exitReason(t, exit) }
}
