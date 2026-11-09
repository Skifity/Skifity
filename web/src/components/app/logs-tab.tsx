import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import {
  DownloadIcon,
  HistoryIcon,
  PauseIcon,
  PlayIcon,
  RadioIcon,
  ScrollTextIcon,
} from "lucide-react"

import { LogRow, shortInstance, type LogLine } from "@/components/app/log-line"
import { useStickToBottom } from "@/components/app/stick-to-bottom"
import { EmptyState } from "@/components/empty-state"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { api, type List } from "@/lib/api"
import { downloadText } from "@/lib/download"
import { formatNumber } from "@/lib/format"
import { currentLanguage } from "@/lib/i18n"
import type { App, AppLogs, AppProcess, AppStatus, LogEntry } from "@/lib/types"

/** How far back a read starts, in lines across every instance read. */
const TAILS = [200, 1000, 5000] as const

/** How far back in time a read reaches; "all" is as far as the lines go. */
const RANGES = ["all", "15m", "1h", "6h", "24h"] as const
type Range = (typeof RANGES)[number]

/** Times are shown in the reader's own zone or in UTC, which is what servers log in. */
type Zone = "local" | "utc"
const ZONE_KEY = "skifity.logs.zone"

function storedZone(): Zone {
  try {
    return localStorage.getItem(ZONE_KEY) === "utc" ? "utc" : "local"
  } catch {
    // Storage refused, as in a private window: the default is fine.
    return "local"
  }
}

/** The calendar day a time falls on, in a zone. */
function dayOf(at: string, timeZone: string | undefined): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone, dateStyle: "short" }).format(new Date(at))
}

/** Every line ever shown gets a key of its own, across reconnects and changes of stream. */
let nextLineID = 0

function keyed(entry: LogEntry): LogLine {
  nextLineID += 1
  return { ...entry, id: nextLineID }
}

/**
 * Live application logs, from every instance or one.
 *
 * This reads the app's own stream rather than the shared event hub, because the
 * panel scrubs each line as it passes through and a log stream is long-lived in
 * a way the hub's fan-out buffer is not meant for.
 */
export function LogsTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [lines, setLines] = useState<LogLine[]>([])
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
  /** Which process: "web" is the app itself, anything else one beside it. */
  const [process, setProcess] = useState("web")
  /** Which instance: "all" is every one that has started, merged. */
  const [instance, setInstance] = useState("all")
  const [range, setRange] = useState<Range>("all")
  const [tail, setTail] = useState<number>(200)
  const [zone, setZone] = useState<Zone>(storedZone)
  /** What the stream says it is reading. Set by the stream's own events, never copied. */
  const [reading, setReading] = useState({ instances: [] as string[], omitted: 0 })
  /** Where the live stream is. Set by the stream's own events, never copied. */
  const [stream, setStream] = useState<"connecting" | "open" | "reconnecting" | "failed">(
    "connecting",
  )
  /** Bumped by Retry, to open a stream that gave up. */
  const [attempt, setAttempt] = useState(0)

  const processes = useQuery({
    queryKey: ["processes", app.id],
    queryFn: () => api.get<List<AppProcess>>(`/api/apps/${app.id}/processes`),
    refetchInterval: 10_000,
  })
  // The same query as the page's header and Overview, so this costs nothing.
  const status = useQuery({
    queryKey: ["app-status", app.id],
    queryFn: () => api.get<AppStatus>(`/api/apps/${app.id}/status`),
    refetchInterval: 10_000,
  })
  const others = processes.data?.items ?? []
  const instances =
    (process === "web"
      ? status.data?.instances
      : others.find((other) => other.name === process)?.instance_list) ?? []
  // An instance that has gone — replaced by a deploy, scaled away — is not
  // asked for again: the read goes back to every instance.
  const known = process === "web" ? status.isSuccess : processes.isSuccess
  const chosen =
    instance !== "all" && known && !instances.some((one) => one.name === instance)
      ? "all"
      : instance

  const params = new URLSearchParams()
  if (process !== "web") params.set("process", process)
  if (chosen !== "all") params.set("instance", chosen)
  const which = params.toString()
  const since = range === "all" ? "" : `&since=${range}`

  const earlier = useQuery({
    queryKey: ["app-logs-previous", app.id, which, tail],
    queryFn: () =>
      api.get<AppLogs>(
        `/api/apps/${app.id}/logs?previous=true&tail=${tail}${which ? `&${which}` : ""}`,
      ),
    enabled: source === "previous",
    retry: false,
  })

  // More than the read asked for, so following a chatty app keeps what the
  // reader asked to see; never so many that the tab grows without end.
  const keep = Math.max(tail, 2000)

  useEffect(() => {
    if (!following || source !== "live") return

    const events = new EventSource(
      `/api/apps/${app.id}/logs?follow=true&tail=${tail}${since}${which ? `&${which}` : ""}`,
    )
    // A first read of thousands of lines arrives as thousands of events. Put
    // on screen one at a time, each re-rendered every line before it; a
    // tenth of a second's worth at once is the same to read.
    let pending: LogLine[] = []
    let timer: number | undefined
    const flush = () => {
      timer = undefined
      const batch = pending
      pending = []
      setLines((previous) => {
        const next = previous.concat(batch)
        return next.length > keep ? next.slice(next.length - keep) : next
      })
    }
    // Every connection — the first, a reconnect after a dropped one, a
    // resume after a pause — starts with the newest lines again. Appended,
    // they were on screen twice, three times; the lines already shown are
    // what the replay is, so they are replaced by it.
    events.addEventListener("open", () => {
      pending = []
      window.clearTimeout(timer)
      timer = undefined
      setLines([])
      setStream("open")
    })
    // A stream that fails is said, rather than looking like an app with
    // nothing running. The browser retries one that dropped by itself, and
    // gives up on one the panel refused.
    events.addEventListener("error", () => {
      setStream(events.readyState === EventSource.CLOSED ? "failed" : "reconnecting")
    })
    events.addEventListener("instances", (message) => {
      try {
        setReading(JSON.parse((message as MessageEvent).data))
      } catch {
        // Which instances are read is worth showing, not worth failing over.
      }
    })
    events.addEventListener("entry", (message) => {
      try {
        pending.push(keyed(JSON.parse((message as MessageEvent).data) as LogEntry))
        timer ??= window.setTimeout(flush, 100)
      } catch {
        // A frame we cannot parse is not worth tearing the stream down for.
      }
    })
    return () => {
      window.clearTimeout(timer)
      events.close()
    }
  }, [app.id, following, source, which, since, tail, keep, attempt])

  // Derived, not copied: which set of lines is on screen follows the tab, and
  // the filter is applied in the same pass so nothing has to be kept in step.
  const earlierEntries = earlier.data?.entries
  const earlierLines = useMemo(() => (earlierEntries ?? []).map(keyed), [earlierEntries])
  const all = source === "previous" ? earlierLines : lines

  const shown = useMemo(() => {
    if (!filter.trim()) return all
    const needle = filter.toLowerCase()
    return all.filter((entry) => entry.line.toLowerCase().includes(needle))
  }, [all, filter])

  // Which instance wrote a line is said once more than one is being read.
  const tagged = useMemo(
    () =>
      (source === "live" && reading.instances.length > 1) ||
      new Set(all.map((entry) => entry.instance)).size > 1,
    [source, reading.instances.length, all],
  )

  // The date is said only when the lines on screen are not all of one day.
  // A boolean, so the formatter below — and with it every row on screen — is
  // not made again for each line that arrives.
  const timeZone = zone === "utc" ? "UTC" : undefined
  const first = all.find((entry) => entry.at)?.at
  const last = all.findLast((entry) => entry.at)?.at
  const spansDays = Boolean(first && last && dayOf(first, timeZone) !== dayOf(last, timeZone))
  const time = useMemo(() => {
    const format = new Intl.DateTimeFormat(currentLanguage(), {
      timeZone,
      hourCycle: "h23",
      ...(spansDays ? { month: "2-digit", day: "2-digit" } : {}),
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      fractionalSecondDigits: 3,
    })
    return (at: string) => format.format(new Date(at))
  }, [timeZone, spansDays])

  // `shown` itself, not its length: past the limit every new line keeps the
  // length the same.
  const stick = useStickToBottom(shown, following && source === "live")

  /** Another stream: its lines start fresh rather than under the last one's. */
  const restart = (change: () => void) => {
    setLines([])
    setReading({ instances: [], omitted: 0 })
    change()
  }

  const chooseZone = (next: Zone) => {
    setZone(next)
    try {
      localStorage.setItem(ZONE_KEY, next)
    } catch {
      // Not remembered in a private window, and still applied.
    }
  }

  // What was loaded, as a log file: each line with its time, in UTC as a log
  // file's are, and the instance that wrote it.
  const download = () => {
    const text = all
      .map((entry) => [entry.at, entry.instance && `[${entry.instance}]`, entry.line].filter(Boolean).join(" "))
      .join("\n")
    downloadText(`${app.slug}${process === "web" ? "" : `-${process}`}.log`, `${text}\n`)
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder={t("common.search")}
          className="h-9 max-w-xs"
        />
        {others.length > 0 && (
          <Select
            value={process}
            onValueChange={(next) =>
              restart(() => {
                setProcess(next)
                // Another process has instances of its own.
                setInstance("all")
              })
            }
          >
            <SelectTrigger size="sm" className="w-40" aria-label={t("processes.title")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="web">web</SelectItem>
              {others.map((other) => (
                <SelectItem key={other.name} value={other.name}>
                  {other.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <Select value={chosen} onValueChange={(next) => restart(() => setInstance(next))}>
          <SelectTrigger size="sm" className="w-44" aria-label={t("apps.logsInstance")}>
            <SelectValue>
              {chosen === "all" ? t("apps.logsInstanceAll") : shortInstance(chosen)}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("apps.logsInstanceAll")}</SelectItem>
            {instances.map((one) => (
              <SelectItem key={one.name} value={one.name} className="font-mono text-xs">
                {one.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {/* The container before the restart has ended: a time range is
            about what is running. */}
        {source === "live" && (
          <Select value={range} onValueChange={(next) => restart(() => setRange(next as Range))}>
            <SelectTrigger size="sm" className="w-40" aria-label={t("apps.logsRange.label")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RANGES.map((one) => (
                <SelectItem key={one} value={one}>
                  {t(`apps.logsRange.${one}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <Select value={String(tail)} onValueChange={(next) => restart(() => setTail(Number(next)))}>
          <SelectTrigger size="sm" className="w-32" aria-label={t("apps.logsTailLabel")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TAILS.map((one) => (
              <SelectItem key={one} value={String(one)}>
                {t("apps.logsTail", { count: one, lines: formatNumber(one) })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
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

        {/* It says what pressing it does. It used to read "Off" while
            following and "On" while paused, the opposite of the state. No
            aria-pressed: a label that changes with the state already says it,
            and "Resume, pressed" says it twice and confusingly. */}
        {source === "live" && (
          <Button variant="outline" size="sm" onClick={() => setFollowing(!following)}>
            {following ? <PauseIcon className="size-3.5" /> : <PlayIcon className="size-3.5" />}
            {following ? t("apps.logsPause") : t("apps.logsResume")}
          </Button>
        )}
        <ButtonGroup aria-label={t("apps.logsZone")}>
          <Button
            variant={zone === "local" ? "default" : "outline"}
            size="sm"
            aria-pressed={zone === "local"}
            onClick={() => chooseZone("local")}
          >
            {t("apps.logsZoneLocal")}
          </Button>
          <Button
            variant={zone === "utc" ? "default" : "outline"}
            size="sm"
            aria-pressed={zone === "utc"}
            onClick={() => chooseZone("utc")}
          >
            UTC
          </Button>
        </ButtonGroup>
        <Button variant="outline" size="sm" disabled={all.length === 0} onClick={download}>
          <DownloadIcon className="size-3.5" />
          {t("apps.logsDownload")}
        </Button>
        <span className="text-xs text-muted-foreground tabular-nums">
          {shown.length} / {all.length}
        </span>
      </div>

      {source === "live" && chosen === "all" && reading.omitted > 0 && (
        <p className="text-xs text-muted-foreground">
          {t("apps.logsOmitted", {
            shown: reading.instances.length,
            total: reading.instances.length + reading.omitted,
          })}
        </p>
      )}
      {source === "previous" && (
        <p className="text-xs text-muted-foreground">{t("apps.logsPreviousHelp")}</p>
      )}
      {source === "previous" && earlier.error != null && (
        <ErrorDisplay error={earlier.error} compact />
      )}

      {source === "live" && following && stream === "failed" && (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center gap-3">
            <span>{t("apps.logsStreamFailed")}</span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setStream("connecting")
                setAttempt((n) => n + 1)
              }}
            >
              {t("common.retry")}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {source === "live" && following && stream === "reconnecting" && (
        <p className="text-xs text-muted-foreground">{t("apps.logsReconnecting")}</p>
      )}

      {all.length === 0 ? (
        <EmptyState
          icon={ScrollTextIcon}
          title={t("apps.logs")}
          description={source === "previous" ? t("apps.logsPreviousNone") : t("apps.logsQuiet")}
        />
      ) : (
        <ScrollArea className="h-[28rem] rounded-md border bg-muted/30" {...stick}>
          <div role="log" className="space-y-px p-3 font-mono text-xs leading-relaxed">
            {shown.map((entry) => (
              <LogRow key={entry.id} entry={entry} time={time} tagged={tagged} />
            ))}
          </div>
        </ScrollArea>
      )}
    </div>
  )
}
