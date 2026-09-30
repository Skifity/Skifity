import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { HistoryIcon, PauseIcon, PlayIcon, RadioIcon, ScrollTextIcon } from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import type { App } from "@/lib/types"

/** How many lines are kept in memory. A chatty app must not grow the tab forever. */
const MAX_LINES = 2000

/**
 * Live application logs.
 *
 * This reads the app's own stream rather than the shared event hub, because the
 * panel scrubs each line as it passes through and a log stream is long-lived in
 * a way the hub's fan-out buffer is not meant for.
 */
export function LogsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [lines, setLines] = useState<string[]>([])
  const [following, setFollowing] = useState(true)
  const [filter, setFilter] = useState("")
  /**
   * "live" is the container running now; "previous" is the one before it.
   *
   * An app that keeps restarting printed the reason in a container that has
   * already been replaced, and the live stream no longer has it. Without this
   * the one thing worth reading is the one thing that cannot be read.
   */
  const [source, setSource] = useState<"live" | "previous">("live")
  const bottom = useRef<HTMLDivElement>(null)

  const earlier = useQuery({
    queryKey: ["app-logs-previous", app.id],
    queryFn: () => api.get<{ lines: string[] }>(`/api/apps/${app.id}/logs?previous=true&tail=500`),
    enabled: source === "previous",
    retry: false,
  })

  useEffect(() => {
    if (!following || source !== "live") return

    const stream = new EventSource(`/api/apps/${app.id}/logs?follow=true&tail=200`)
    stream.addEventListener("log", (message) => {
      try {
        const line = JSON.parse((message as MessageEvent).data) as string
        setLines((previous) => {
          const next = [...previous, line]
          return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
        })
      } catch {
        // A frame we cannot parse is not worth tearing the stream down for.
      }
    })
    return () => stream.close()
  }, [app.id, following, source])

  // Derived, not copied: which set of lines is on screen follows the tab, and
  // the filter is applied in the same pass so nothing has to be kept in step.
  const earlierLines = earlier.data?.lines
  const all = useMemo(
    () => (source === "previous" ? (earlierLines ?? []) : lines),
    [source, earlierLines, lines],
  )

  const shown = useMemo(() => {
    if (!filter.trim()) return all
    const needle = filter.toLowerCase()
    return all.filter((line) => line.toLowerCase().includes(needle))
  }, [all, filter])

  useEffect(() => {
    if (following && source === "live") bottom.current?.scrollIntoView({ block: "nearest" })
  }, [shown.length, following, source])

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder={t("common.search")}
          className="h-9 max-w-xs"
        />
        <ButtonGroup>
          <Button
            variant={source === "live" ? "default" : "outline"}
            size="sm"
            onClick={() => setSource("live")}
          >
            <RadioIcon className="size-3.5" />
            {t("apps.logsLive")}
          </Button>
          <Button
            variant={source === "previous" ? "default" : "outline"}
            size="sm"
            onClick={() => setSource("previous")}
          >
            {earlier.isFetching && source === "previous" ? (
              <Spinner />
            ) : (
              <HistoryIcon className="size-3.5" />
            )}
            {t("apps.logsPrevious")}
          </Button>
        </ButtonGroup>

        {source === "live" && (
          <Button variant="outline" size="sm" onClick={() => setFollowing(!following)}>
            {following ? <PauseIcon className="size-3.5" /> : <PlayIcon className="size-3.5" />}
            {following ? t("common.off") : t("common.on")}
          </Button>
        )}
        <span className="text-xs text-muted-foreground">
          {shown.length} / {all.length}
        </span>
      </div>

      {source === "previous" && <p className="text-xs text-muted-foreground">{t("apps.logsPreviousHelp")}</p>}
      {source === "previous" && earlier.error != null && (
        <ErrorDisplay error={earlier.error} compact />
      )}

      {all.length === 0 ? (
        <EmptyState
          icon={ScrollTextIcon}
          title={t("apps.logs")}
          description={source === "previous" ? t("apps.logsPreviousNone") : t("apps.noInstances")}
        />
      ) : (
        <ScrollArea className="h-[28rem] rounded-md border bg-muted/30">
          <pre className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
            {shown.join("\n")}
            <div ref={bottom} />
          </pre>
        </ScrollArea>
      )}
    </div>
  )
}
