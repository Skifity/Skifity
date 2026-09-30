import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import {
  CheckIcon,
  ChevronRightIcon,
  CircleIcon,
  Loader2Icon,
  MinusIcon,
  XIcon,
} from "lucide-react"
import { cn } from "cn"

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { translated } from "@/lib/say"
import type { Operation, OperationStep, StepDetail } from "@/lib/types"

/**
 * Shows a long-running operation step by step.
 *
 * Every step is named in the user's language, carries what it found, and a
 * failed step shows the full explanation inline rather than sending the user to
 * look for it. This is what the add-server flow is watched through.
 */
export function OperationProgress({
  operation,
  logs,
}: {
  operation: Operation
  /** Live output of the running step, if any. */
  logs?: string[]
}) {
  const { t } = useTranslation()
  const steps = operation.steps ?? []

  return (
    <ol className="space-y-1" data-slot="operation-progress">
      {steps.map((step) => (
        <li key={step.id}>
          <div
            className={cn(
              "flex items-start gap-3 rounded-md px-3 py-2.5",
              step.status === "running" && "bg-info/5",
              step.status === "failed" && "bg-destructive/5",
            )}
          >
            <StepIcon status={step.status} />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    "text-sm",
                    step.status === "pending" && "text-muted-foreground",
                    step.status === "failed" && "font-medium text-destructive",
                    step.status === "running" && "font-medium",
                  )}
                >
                  {t(`servers.steps.${step.key}`, { defaultValue: step.key })}
                </span>
              </div>
              {step.message && (
                <p
                  className={cn(
                    "mt-0.5 text-xs",
                    step.status === "failed" ? "text-destructive" : "text-muted-foreground",
                  )}
                >
                  {sayStep(t, step)}
                </p>
              )}
              {/* Shown whatever the step's state.
                  This used to read `step.status === "failed" && step.detail`,
                  so three things the panel writes against steps that *succeed*
                  — the preflight warnings, the server's host key, the
                  fingerprint of the key that was installed — were collected,
                  stored, and displayed to nobody. A failure opens on its own,
                  because that is what somebody is looking at; anything else
                  waits behind a line they can click. */}
              {(step.notes?.length || step.detail) && (
                <Collapsible defaultOpen={step.status === "failed"} className="mt-1.5">
                  <CollapsibleTrigger className="group/detail flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
                    <ChevronRightIcon className="size-3 transition-transform group-data-[state=open]/detail:rotate-90" />
                    {t("errors.details")}
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    {step.notes && step.notes.length > 0 && (
                      <ul className="mt-1.5 space-y-1 rounded-md bg-muted/60 p-2.5 text-xs">
                        {step.notes.map((note, index) => (
                          <li key={index}>{sayNote(t, note)}</li>
                        ))}
                      </ul>
                    )}
                    {step.detail && (
                      <pre className="log-output mt-1.5 max-h-72 overflow-auto rounded-md bg-muted p-3 text-xs">
                        {step.detail}
                      </pre>
                    )}
                  </CollapsibleContent>
                </Collapsible>
              )}
              {step.status === "running" && logs && logs.length > 0 && (
                <pre className="log-output mt-2 max-h-48 overflow-auto rounded-md bg-muted p-2.5 text-xs">
                  {logs.slice(-40).join("\n")}
                </pre>
              )}
            </div>
          </div>
        </li>
      ))}
    </ol>
  )
}

/**
 * The line under a step's name, in the reader's language.
 *
 * The name has been translated since the panel shipped, from servers.steps.*;
 * the sentence under it was the English the Go code wrote, so somebody adding a
 * server in Indonesian read a translated heading over "Connected to
 * 203.0.113.10". A step that failed shows its problem's title, which the error
 * catalogue already translates — the "problem:" prefix says to look there.
 */
function sayStep(t: TFunction, step: OperationStep): string {
  const key = step.message_key
  if (!key) return step.message
  if (key.startsWith("problem:")) {
    const code = key.slice("problem:".length).replaceAll(".", "_")
    return translated(t, `errors.catalogue.${code}.title`, step.message, step.message_args)
  }
  return translated(t, `servers.stepMessage.${key}`, step.message, step.message_args)
}

/**
 * One extra line under a step, in the reader's language.
 *
 * A preflight warning's key is the same one its fatal twin uses in the error
 * catalogue, so the sentence is written once and read from both places.
 */
function sayNote(t: TFunction, note: StepDetail): string {
  if (!note.key) return note.text
  if (note.key.startsWith("preflight.")) {
    const [, code, field] = note.key.split(".")
    return translated(t, `errors.catalogue.preflight_${code}.${field === "detail" ? "cause" : "fix"}`, note.text, note.args)
  }
  return translated(t, `servers.stepNote.${note.key}`, note.text, note.args)
}

function StepIcon({ status }: { status: OperationStep["status"] }) {
  const base = "mt-0.5 size-4 shrink-0"
  switch (status) {
    case "succeeded":
      return <CheckIcon className={cn(base, "text-success")} />
    case "running":
      return <Loader2Icon className={cn(base, "animate-spin text-info")} />
    case "failed":
      return <XIcon className={cn(base, "text-destructive")} />
    case "skipped":
      return <MinusIcon className={cn(base, "text-muted-foreground")} />
    default:
      return <CircleIcon className={cn(base, "text-muted-foreground/40")} />
  }
}
