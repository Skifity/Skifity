import { memo, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronRightIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import type { LogEntry } from "@/lib/types"
import { cn } from "cn"

/** A line on screen: an entry, and a key that stays with it while older lines are let go. */
export type LogLine = LogEntry & { id: number }

/**
 * The part of an instance's name after its last hyphen: what tells an app's
 * instances apart, and what the CLI prints and accepts as --instance.
 */
export function shortInstance(name: string): string {
  const cut = name.lastIndexOf("-")
  return cut >= 0 && cut < name.length - 1 ? name.slice(cut + 1) : name
}

/** A line that is a JSON object, as structured loggers write them, or nothing. */
function parseObject(line: string): Record<string, unknown> | undefined {
  const text = line.trim()
  if (!text.startsWith("{") || !text.endsWith("}")) return undefined
  try {
    const value: unknown = JSON.parse(text)
    return value && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : undefined
  } catch {
    return undefined
  }
}

/**
 * One line of an app's log: when, which instance when more than one is
 * read, and the line. A line that is a JSON object can be opened to read its
 * fields one to a line.
 *
 * Memoised, and each line keeps its id, so a new line arriving renders one row
 * rather than every row on screen.
 */
export const LogRow = memo(function LogRow({
  entry,
  time,
  tagged,
}: {
  entry: LogLine
  /** Formats the line's time; absent when the line has none. */
  time: (at: string) => string
  /** Whether to say which instance wrote it. */
  tagged: boolean
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const fields = useMemo(() => parseObject(entry.line), [entry.line])

  return (
    <div className="flex items-start gap-2">
      <span className="w-[6.5rem] shrink-0 text-muted-foreground tabular-nums" title={entry.at}>
        {entry.at ? time(entry.at) : ""}
      </span>
      {tagged && (
        <Badge
          variant="outline"
          className="mt-px h-4 shrink-0 rounded-sm px-1 font-mono text-[10px] font-normal"
          title={entry.instance}
        >
          {shortInstance(entry.instance)}
        </Badge>
      )}
      {fields ? (
        <Button
          variant="ghost"
          size="icon-xs"
          className="size-4 shrink-0"
          aria-expanded={open}
          aria-label={open ? t("apps.logsFieldsHide") : t("apps.logsFieldsShow")}
          onClick={() => setOpen(!open)}
        >
          <ChevronRightIcon className={cn("transition-transform", open && "rotate-90")} />
        </Button>
      ) : (
        <span className="size-4 shrink-0" />
      )}
      <div className="min-w-0 flex-1">
        <span className="break-all whitespace-pre-wrap">{entry.line}</span>
        {open && fields && (
          <pre className="mt-1 mb-2 rounded-sm border bg-background p-2 break-all whitespace-pre-wrap">
            {JSON.stringify(fields, null, 2)}
          </pre>
        )}
      </div>
    </div>
  )
})
