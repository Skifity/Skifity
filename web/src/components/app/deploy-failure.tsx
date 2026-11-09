import { useTranslation } from "react-i18next"
import { Link } from "react-router-dom"
import { ArrowRightIcon, ChevronRightIcon } from "lucide-react"

import { recordedTitle } from "@/components/app/recorded-title"
import { CopyButton } from "@/components/copy-button"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { usePanelMeta } from "@/hooks/use-session"
import { problemToMarkdown } from "@/lib/api"
import { translated } from "@/lib/say"
import type { Deployment } from "@/lib/types"

/**
 * How a container stopped, sorted by what fixes it. The same four as the
 * server's kube.StopKind, worked out the same way from the kubelet's reason
 * and the exit code.
 */
export type StopKind = "out_of_memory" | "not_runnable" | "exited_cleanly" | "exited"

const STOP_KINDS: readonly StopKind[] = [
  "out_of_memory",
  "not_runnable",
  "exited_cleanly",
  "exited",
]

export function stopKind(reason: string, exitCode: number): StopKind {
  if (reason === "OOMKilled") return "out_of_memory"
  if (
    exitCode === 126 ||
    exitCode === 127 ||
    reason === "StartError" ||
    reason === "ContainerCannotRun"
  ) {
    return "not_runnable"
  }
  return exitCode === 0 ? "exited_cleanly" : "exited"
}

/** The tabs of an app page a failure can send somebody to. */
export type FixTab = "scaling" | "settings" | "variables" | "logs"

/** Where each kind of crash is fixed: memory, the start command, a variable. */
export const CRASH_FIX_TAB: Record<StopKind, FixTab> = {
  out_of_memory: "scaling",
  not_runnable: "settings",
  exited_cleanly: "settings",
  exited: "variables",
}

/**
 * A button to the app's tab where a failure is fixed. The tab is in the
 * address, so it is a link: the page reads ?tab= and opens it.
 */
export function OpenTabButton({ tab }: { tab: FixTab }) {
  const { t } = useTranslation()
  return (
    <Button asChild size="sm" variant="outline">
      <Link to={`?tab=${tab}`}>
        {t(`deploy.failure.open.${tab}`)}
        <ArrowRightIcon className="size-3.5" />
      </Link>
    </Button>
  )
}

/**
 * Why a deployment failed: the cause first, in the reader's language, then
 * the one thing to do about it with a button to where it is done, and what
 * the server recorded behind Details.
 *
 * A failure used to be its English message and its English hint, with only the
 * headline translated, and a version that crashed said "did not start" ten
 * minutes later with a list of usual causes. The deployment now keeps the
 * whole problem, so the sentence and the button are chosen here from its code
 * and, for a crash, from how the container stopped.
 */
export function DeployFailure({ deployment }: { deployment: Deployment }) {
  const { t } = useTranslation()
  const meta = usePanelMeta()
  const problem = deployment.problem
  const code = deployment.error_code ?? problem?.code ?? ""
  const key = `errors.catalogue.${code.replaceAll(".", "_")}`
  const context = problem?.context ?? {}

  const kind =
    code === "deploy.crashed" && STOP_KINDS.includes(context.kind as StopKind)
      ? (context.kind as StopKind)
      : null
  const exitCode = context.exit_code ?? ""

  let cause = deployment.error_message ?? ""
  let fix = deployment.error_hint ?? ""
  let tab: FixTab | null = null
  if (kind) {
    cause = t(`deploy.failure.crashed.${kind}`, { code: exitCode })
    fix = t(`deploy.failure.fix.${kind}`)
    tab = CRASH_FIX_TAB[kind]
  } else if (problem) {
    cause = translated(t, `${key}.cause`, problem.cause, problem.args?.cause) || cause
    fix = translated(t, `${key}.fix`, problem.fix, problem.args?.fix) || fix
    // A version that never answered its health check: the check and the
    // time it is given to start are both on Settings.
    if (code === "deploy.rollout_timeout") tab = "settings"
  }

  const tail = context.log_tail ?? ""
  // What an assistant needs to say what is wrong: the problem whole, with
  // the last lines in its context. A deployment that failed before problems
  // were kept has its two sentences.
  const diagnosis = problem
    ? problemToMarkdown(problem, meta?.version)
    : [deployment.error_message, deployment.error_hint].filter(Boolean).join("\n\n")

  return (
    <div className="space-y-2" data-slot="deploy-failure">
      {code && (
        <p className="font-medium">
          {recordedTitle(t, code, problem?.title ?? deployment.error_message ?? "")}
        </p>
      )}
      {cause && <p>{cause}</p>}
      {(fix || tab) && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {fix && <p className="min-w-0 flex-1 text-muted-foreground">{fix}</p>}
          {tab && <OpenTabButton tab={tab} />}
        </div>
      )}
      <Collapsible className="text-xs">
        <div className="flex items-center gap-1">
          <CollapsibleTrigger className="group/details flex items-center gap-1 rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50">
            <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]/details:rotate-90" />
            {t("errors.details")}
          </CollapsibleTrigger>
          <CopyButton value={diagnosis} label={t("deploy.failure.diagnosis")} className="size-7" />
        </div>
        <CollapsibleContent className="space-y-2 pt-2">
          {tail !== "" && (
            <>
              <p className="text-muted-foreground">{t("deploy.failure.lastLines")}</p>
              <pre className="log-output max-h-64 overflow-auto rounded-md bg-muted/50 p-3 whitespace-pre-wrap break-all">
                {tail}
              </pre>
            </>
          )}
          {/* What the server recorded, in its words, which is what a search
              or an issue report wants. */}
          {deployment.error_message && (
            <p className="break-words text-muted-foreground">{deployment.error_message}</p>
          )}
          {deployment.error_hint && (
            <p className="break-words text-muted-foreground">{deployment.error_hint}</p>
          )}
        </CollapsibleContent>
      </Collapsible>
    </div>
  )
}
