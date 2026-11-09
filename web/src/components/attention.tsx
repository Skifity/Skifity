import { useState } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { BellIcon, CheckCircle2Icon, TriangleAlertIcon, XCircleIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Section } from "@/components/page"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Item,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle,
} from "@/components/ui/item"
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Skeleton } from "@/components/ui/skeleton"
import { useAttention } from "@/hooks/use-attention"
import { formatRelative } from "@/lib/format"
import type { AttentionItem } from "@/lib/types"

/**
 * What needs attention across the team: the overview's first section and the
 * header's bell. The list is built by the panel (GET /api/teams/{team}/attention)
 * from what it has recorded; each item is a sentence and the page to deal with
 * it on.
 */

/** How many the overview shows before "Show all". */
const SHOWN = 8

/** The item's sentence in the person's language. */
function sentence(t: TFunction, item: AttentionItem): string {
  const params: Record<string, string | number> = { ...item.params }
  // Alert names are tokens, translated the way the usage cards translate them.
  if (typeof params.alerts === "string") {
    const names = item.kind === "server_alert" ? "servers.alertName" : "apps.alertName"
    params.alerts = params.alerts
      .split(",")
      .filter(Boolean)
      .map((name) => t(`${names}.${name}`, { defaultValue: name }))
      .join(", ")
  }
  return t(`attention.${item.code}`, { ...params, defaultValue: item.message })
}

/** Where it is and since when, as far as the item says. */
function context(item: AttentionItem): string {
  const parts: string[] = []
  if (item.project_name) {
    parts.push(
      item.environment_name ? `${item.project_name} · ${item.environment_name}` : item.project_name,
    )
  }
  if (item.since) parts.push(formatRelative(item.since))
  return parts.join(" · ")
}

/** The list itself: a severity, a sentence, where and when, and a link. */
export function AttentionRows({
  items,
  compact,
  onFollow,
}: {
  items: AttentionItem[]
  compact?: boolean
  /** Called when one is followed, for a popover to close behind it. */
  onFollow?: () => void
}) {
  const { t } = useTranslation()
  return (
    <ItemGroup className={compact ? undefined : "rounded-lg border"}>
      {items.map((item) => {
        const Icon = item.severity === "error" ? XCircleIcon : TriangleAlertIcon
        const where = context(item)
        return (
          <Item key={`${item.kind}:${item.code}:${item.target_id}`} size="sm" asChild>
            <Link to={item.path} onClick={onFollow}>
              <ItemMedia
                variant="icon"
                className={item.severity === "error" ? "text-destructive" : "text-warning"}
              >
                {/* Not by colour alone: the shape differs, and the word is read out. */}
                <Icon aria-hidden />
                <span className="sr-only">{t(`attention.severity.${item.severity}`)}</span>
              </ItemMedia>
              <ItemContent>
                <ItemTitle className="font-normal">{sentence(t, item)}</ItemTitle>
                {where && <ItemDescription>{where}</ItemDescription>}
              </ItemContent>
            </Link>
          </Item>
        )
      })}
    </ItemGroup>
  )
}

/**
 * The overview's "Needs attention": first on the page, above what the team
 * has, because whether anything is wrong is the first thing to know. Nothing
 * wrong says so in one line rather than taking the space.
 */
export function AttentionSection() {
  const { t } = useTranslation()
  const attention = useAttention()
  // Which way the list is folded is this screen's business only.
  const [all, setAll] = useState(false)
  const items = attention.data?.items ?? []
  const shown = all ? items : items.slice(0, SHOWN)

  return (
    <Section
      title={t("attention.title")}
      actions={
        items.length > SHOWN && (
          <Button variant="ghost" size="sm" onClick={() => setAll(!all)}>
            {all ? t("attention.showFewer") : t("attention.showAll", { total: items.length })}
          </Button>
        )
      }
    >
      {attention.isLoading ? (
        <Skeleton className="h-16" />
      ) : attention.error ? (
        <ErrorDisplay error={attention.error} onRetry={() => void attention.refetch()} compact />
      ) : items.length === 0 ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <CheckCircle2Icon className="size-4 text-success" aria-hidden />
          {t("attention.nothing")}
        </p>
      ) : (
        <AttentionRows items={shown} />
      )}
    </Section>
  )
}

/**
 * The same list from anywhere: a bell in the header, with how many things
 * need attention, red when one of them is broken.
 */
export function AttentionBell() {
  const { t } = useTranslation()
  const attention = useAttention()
  const [open, setOpen] = useState(false)
  const items = attention.data?.items ?? []
  const broken = items.some((item) => item.severity === "error")

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative"
          aria-label={
            items.length > 0
              ? t("attention.bellCount", { count: items.length })
              : t("attention.title")
          }
        >
          <BellIcon />
          {items.length > 0 && (
            <Badge
              variant={broken ? "destructive" : "secondary"}
              className="absolute -top-1 -right-1 h-4 min-w-4 px-1 text-[10px] tabular-nums"
              aria-hidden
              data-testid="attention-count"
            >
              {items.length}
            </Badge>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96 max-w-[calc(100vw-2rem)] p-0">
        <PopoverHeader className="border-b px-4 py-3">
          <PopoverTitle>{t("attention.title")}</PopoverTitle>
        </PopoverHeader>
        {attention.error ? (
          <div className="p-3">
            <ErrorDisplay
              error={attention.error}
              onRetry={() => void attention.refetch()}
              compact
            />
          </div>
        ) : items.length === 0 ? (
          <p className="flex items-center gap-2 px-4 py-3 text-sm text-muted-foreground">
            <CheckCircle2Icon className="size-4 text-success" aria-hidden />
            {t("attention.nothing")}
          </p>
        ) : (
          // Following a link is leaving the popover, so it closes behind it.
          <div className="max-h-96 overflow-y-auto p-1">
            <AttentionRows items={items} compact onFollow={() => setOpen(false)} />
          </div>
        )}
        <div className="border-t p-1">
          <Button variant="ghost" size="sm" className="w-full" asChild>
            <Link to="/" onClick={() => setOpen(false)}>
              {t("attention.openOverview")}
            </Link>
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
