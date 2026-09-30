import { useState } from "react"
import { useTranslation } from "react-i18next"
import {
  AlertTriangleIcon,
  CheckIcon,
  ChevronRightIcon,
  ClipboardIcon,
  ExternalLinkIcon,
  RefreshCwIcon,
} from "lucide-react"
import { toast } from "sonner"
import { cn } from "cn"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { ApiError, type Problem } from "@/lib/api"
import { translated } from "@/lib/say"

/**
 * Renders a failure the way the product promises: what happened, what it means,
 * how to fix it, and a button that copies the whole thing for an AI assistant.
 *
 * A bare message is never shown. If a Problem has no cause or fix, that is a
 * gap in the error catalogue, not something to paper over here.
 */
export function ErrorDisplay({
  error,
  onRetry,
  className,
  compact,
}: {
  error: unknown
  onRetry?: () => void
  className?: string
  compact?: boolean
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  const problem = toProblem(error)
  if (!problem) return null

  // The server writes its errors once, in English: it has one language, and the
  // API, the CLI and an assistant all read those strings. The panel has five and
  // looks them up by the error's own code, falling back to what the server sent.
  // Same mechanism as the settings page, which was English in every language
  // until Phase 55 for exactly this reason.
  const say = (field: "title" | "cause" | "impact" | "fix") =>
    translated(
      t,
      `errors.catalogue.${problem.code.replaceAll(".", "_")}.${field}`,
      problem[field],
      problem.args?.[field as "cause"],
    )

  const copyForAI = async () => {
    const markdown = error instanceof ApiError ? error.toMarkdown() : problemToMarkdown(problem)
    try {
      await navigator.clipboard.writeText(markdown)
      setCopied(true)
      toast.success(t("errors.copiedForAI"))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard access can be refused; showing the text is the fallback that
      // still lets someone copy it by hand.
      toast.error(markdown.slice(0, 200))
    }
  }

  const tone =
    problem.severity === "warning"
      ? "border-warning/40 bg-warning/5"
      : problem.severity === "info"
        ? "border-info/40 bg-info/5"
        : "border-destructive/40 bg-destructive/5"

  return (
    <Alert className={cn(tone, className)} data-slot="error-display">
      <AlertTriangleIcon className="size-4" />
      <AlertTitle className="text-base">{say("title")}</AlertTitle>
      <AlertDescription className="space-y-3">
        {!compact && problem.cause && (
          <Field label={t("errors.whatHappened")} value={say("cause")} />
        )}
        {!compact && problem.impact && (
          <Field label={t("errors.whatItMeans")} value={say("impact")} />
        )}
        {problem.fix && <Field label={t("errors.howToFix")} value={say("fix")} />}

        {!compact && problem.context && Object.keys(problem.context).length > 0 && (
          <Collapsible className="text-xs">
            <CollapsibleTrigger className="group/details flex items-center gap-1 text-muted-foreground hover:text-foreground">
              <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]/details:rotate-90" />
              {t("errors.details")}
            </CollapsibleTrigger>
            <CollapsibleContent>
              <dl className="log-output mt-2 max-h-64 overflow-auto rounded-md bg-muted/50 p-3">
                {Object.entries(problem.context).map(([key, value]) => (
                  <div key={key} className="flex gap-2">
                    <dt className="shrink-0 text-muted-foreground">{key}:</dt>
                    <dd className="min-w-0 break-all">{value}</dd>
                  </div>
                ))}
              </dl>
            </CollapsibleContent>
          </Collapsible>
        )}

        <div className="flex flex-wrap items-center gap-2 pt-1">
          {onRetry && problem.retryable && (
            <Button size="sm" variant="outline" onClick={onRetry}>
              <RefreshCwIcon className="size-3.5" />
              {t("common.retry")}
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={copyForAI}>
            {copied ? <CheckIcon className="size-3.5" /> : <ClipboardIcon className="size-3.5" />}
            {t("common.copyForAI")}
          </Button>
          {problem.docs_path && (
            <Button size="sm" variant="ghost" asChild>
              <a href={problem.docs_path} target="_blank" rel="noreferrer">
                <ExternalLinkIcon className="size-3.5" />
                {t("common.openDocs")}
              </a>
            </Button>
          )}
          <span className="ml-auto font-mono text-[11px] text-muted-foreground">
            {problem.code}
          </span>
        </div>
      </AlertDescription>
    </Alert>
  )
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <p className="text-sm">{value}</p>
    </div>
  )
}

/** Turns anything thrown into a Problem, so nothing reaches the UI unexplained. */
export function toProblem(error: unknown): Problem | null {
  if (!error) return null
  if (error instanceof ApiError) return error.problem
  if (error instanceof Error) {
    return {
      code: "unexpected",
      title: error.message || "Something went wrong",
      cause: error.message,
      impact: "The action may not have completed.",
      fix: "Try again. If it keeps happening, copy this and open an issue.",
      severity: "error",
      retryable: true,
      at: new Date().toISOString(),
    }
  }
  return {
    code: "unexpected",
    title: "Something went wrong",
    severity: "error",
    retryable: true,
    at: new Date().toISOString(),
  }
}

function problemToMarkdown(problem: Problem): string {
  const parts = [`## Skifity error: ${problem.title}`, "", `- **Error code**: \`${problem.code}\``]
  if (problem.cause) parts.push("", "**What happened**", "", problem.cause)
  if (problem.impact) parts.push("", "**What it means**", "", problem.impact)
  if (problem.fix) parts.push("", "**Suggested fix**", "", problem.fix)
  return parts.join("\n")
}
