import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { ActivityIcon } from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { api } from "@/lib/api"
import { formatDateTime, formatRelative } from "@/lib/format"
import { translated } from "@/lib/say"
import type { ObjectEvent } from "@/lib/types"

/**
 * What Kubernetes said about an app's or a database's objects: the scheduler
 * that could not place an instance, the health check that failed, the
 * instance the kernel stopped for its memory. When the logs are empty, the
 * reason is usually here.
 *
 * Only ever under Advanced: the objects are Kubernetes' own, named as it
 * names them. Refreshed while it is on the screen, which is only while the
 * Advanced tab is open, because the tab's content is not mounted otherwise.
 */
export function EventsCard({
  path,
  queryKey,
  description,
}: {
  path: string
  queryKey: unknown[]
  description: string
}) {
  const { t } = useTranslation()
  const [warningsOnly, setWarningsOnly] = useState(false)
  const events = useQuery({
    queryKey,
    queryFn: () => api.get<{ items: ObjectEvent[] }>(path),
    refetchInterval: 10_000,
    retry: false,
  })

  // The filter is applied to what was fetched, during render: the list is
  // the one the panel sent, and switching back needs no request.
  const all = events.data?.items ?? []
  const shown = warningsOnly ? all.filter((event) => event.type === "Warning") : all

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1.5">
            <CardTitle className="text-base">{t("events.title")}</CardTitle>
            <CardDescription>{description}</CardDescription>
          </div>
          <label className="flex min-h-6 items-center gap-2 text-sm">
            <Switch
              checked={warningsOnly}
              onCheckedChange={setWarningsOnly}
              aria-label={t("events.warningsOnly")}
            />
            {t("events.warningsOnly")}
          </label>
        </div>
      </CardHeader>
      <CardContent className="p-0">
        {events.isPending ? (
          <div className="p-6 pt-0">
            <Skeleton className="h-32 w-full" />
          </div>
        ) : events.error != null ? (
          <div className="p-6 pt-0">
            <ErrorDisplay error={events.error} compact onRetry={() => void events.refetch()} />
          </div>
        ) : shown.length === 0 ? (
          <EmptyState
            bordered={false}
            icon={ActivityIcon}
            title={warningsOnly ? t("events.emptyWarnings") : t("events.empty")}
            description={t("events.emptyHelp")}
          />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("events.lastSeen")}</TableHead>
                  <TableHead>{t("common.type")}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t("events.reason")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("events.object")}</TableHead>
                  <TableHead>{t("events.message")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((event) => (
                  <EventRow
                    key={`${event.kind}/${event.name}/${event.type}/${event.reason}/${event.message}`}
                    event={event}
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function EventRow({ event }: { event: ObjectEvent }) {
  const { t } = useTranslation()
  const explanation = event.explanation_code
    ? translated(
        t,
        `events.explain.${event.explanation_code}`,
        event.explanation,
        event.explanation_args,
      )
    : ""
  return (
    <TableRow className="align-top">
      <TableCell className="text-xs whitespace-nowrap" title={formatDateTime(event.last_seen)}>
        {formatRelative(event.last_seen)}
        {event.count > 1 && (
          <span className="block text-muted-foreground">
            {t("events.times", { times: event.count })}
          </span>
        )}
      </TableCell>
      <TableCell>
        <Badge variant={event.type === "Warning" ? "destructive" : "secondary"}>
          {event.type === "Warning" ? t("events.warning") : t("events.normal")}
        </Badge>
      </TableCell>
      <TableCell className="hidden font-mono text-xs sm:table-cell">{event.reason}</TableCell>
      <TableCell className="hidden max-w-56 font-mono text-xs break-all md:table-cell">
        {event.kind}/{event.name}
      </TableCell>
      <TableCell className="min-w-64 text-sm">
        {explanation && <p>{explanation}</p>}
        <p className={explanation ? "mt-1 text-xs text-muted-foreground" : "text-xs"}>
          {/* On a phone the reason and the object have no column of their
              own, so they lead the message. */}
          <span className="font-mono sm:hidden">
            {event.reason} · {event.kind}/{event.name} ·{" "}
          </span>
          {event.message}
        </p>
      </TableCell>
    </TableRow>
  )
}
