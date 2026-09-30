import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { PlusIcon, SearchIcon, ServerIcon } from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"
import { Card, CardContent } from "@/components/ui/card"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatCPU, formatMemory, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { Server } from "@/lib/types"

type Filter = "all" | "ready" | "attention"

export function ServersPage() {
  const { t } = useTranslation()
  const { team } = useSession()
  const [search, setSearch] = useState("")
  const [filter, setFilter] = useState<Filter>("all")

  const servers = useQuery({
    queryKey: ["servers", team?.id],
    queryFn: () => api.get<List<Server>>(`/api/teams/${team!.id}/servers`),
    enabled: Boolean(team),
  })

  useEvents(
    team ? [`team:${team.id}`] : [],
    // "operation" covers a server being added or removed. "server" is the
    // watcher noticing one stop answering, which is the whole reason the
    // watcher exists and never reached this list.
    {
      operation: () => void queryClient.invalidateQueries({ queryKey: ["servers", team?.id] }),
      server: () => void queryClient.invalidateQueries({ queryKey: ["servers", team?.id] }),
    },
    Boolean(team),
  )

  const items = useMemo(() => servers.data?.items ?? [], [servers.data])

  const needsAttention = useMemo(
    () => items.filter((server) => server.status === "failed" || server.status === "not_ready"),
    [items],
  )

  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return items.filter((server) => {
      if (filter === "ready" && server.status !== "ready") return false
      if (filter === "attention" && !needsAttention.includes(server)) return false
      if (!needle) return true
      return (
        server.name.toLowerCase().includes(needle) ||
        server.host.toLowerCase().includes(needle) ||
        server.node_name.toLowerCase().includes(needle)
      )
    })
  }, [items, search, filter, needsAttention])

  const addButton = (
    <Button asChild>
      <Link to="/servers/new">
        <PlusIcon />
        {t("servers.addServer")}
      </Link>
    </Button>
  )

  return (
    <Page>
      <PageHeader
        title={t("servers.title")}
        description={t("servers.subtitle")}
        badge={
          needsAttention.length > 0 && (
            <Badge variant="outline" className="border-destructive/40 text-destructive">
              {t("servers.needAttention", { count: needsAttention.length })}
            </Badge>
          )
        }
        actions={addButton}
      />

      {servers.isLoading ? (
        <Skeleton className="h-64" />
      ) : servers.error ? (
        <ErrorDisplay error={servers.error} onRetry={() => void servers.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={ServerIcon}
          title={t("servers.empty")}
          description={t("servers.emptyHelp")}
          action={addButton}
        />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <InputGroup className="max-w-xs">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={t("common.search")}
              />
            </InputGroup>

            <ButtonGroup>
              {(["all", "ready", "attention"] as const).map((value) => (
                <Button
                  key={value}
                  variant={filter === value ? "default" : "outline"}
                  size="sm"
                  onClick={() => setFilter(value)}
                >
                  {t(`servers.filter.${value}`)}
                  {value === "attention" && needsAttention.length > 0 && (
                    <Badge variant="secondary" className="ml-1">
                      {needsAttention.length}
                    </Badge>
                  )}
                </Button>
              ))}
            </ButtonGroup>

            <span className="ml-auto text-xs text-muted-foreground tabular-nums">
              {shown.length} / {items.length}
            </span>
          </div>

          <Card className="overflow-hidden py-0">
            <CardContent className="p-0">
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>{t("common.name")}</TableHead>
                    <TableHead>{t("common.status")}</TableHead>
                    <TableHead className="hidden sm:table-cell">{t("common.type")}</TableHead>
                    <TableHead className="hidden md:table-cell">{t("servers.cpu")}</TableHead>
                    <TableHead className="hidden md:table-cell">{t("servers.memory")}</TableHead>
                    <TableHead className="hidden lg:table-cell">{t("servers.disk")}</TableHead>
                    <TableHead className="hidden lg:table-cell">{t("servers.lastSeen")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.length === 0 ? (
                    <TableRow className="hover:bg-transparent">
                      <TableCell colSpan={7} className="p-0">
                        <EmptyState
                          bordered={false}
                          icon={SearchIcon}
                          title={t("common.noMatches")}
                          description={t("common.noMatchesHelp")}
                        />
                      </TableCell>
                    </TableRow>
                  ) : (
                    shown.map((server) => (
                      <TableRow key={server.id} className="group">
                        <TableCell>
                          <Link
                            to={`/servers/${server.id}`}
                            className="block font-medium group-hover:text-primary"
                          >
                            {server.name}
                            <span className="block font-mono text-xs font-normal text-muted-foreground">
                              {server.host}
                            </span>
                          </Link>
                        </TableCell>
                        <TableCell>
                          <StatusBadge
                            status={server.status}
                            label={t(`servers.status.${server.status}`, {
                              defaultValue: server.status,
                            })}
                          />
                          {server.status_detail && (
                            <span className="mt-1 block max-w-48 truncate text-xs text-muted-foreground">
                              {server.status_detail}
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="hidden text-xs sm:table-cell">
                          {t(`servers.role.${server.role}`, { defaultValue: server.role })}
                        </TableCell>
                        <TableCell className="hidden tabular-nums md:table-cell">
                          {server.cpu_cores ? formatCPU(server.cpu_cores * 1000) : "—"}
                        </TableCell>
                        <TableCell className="hidden tabular-nums md:table-cell">
                          {server.memory_mb ? formatMemory(server.memory_mb) : "—"}
                        </TableCell>
                        <TableCell className="hidden tabular-nums lg:table-cell">
                          {server.disk_gb ? `${server.disk_gb} GB` : "—"}
                        </TableCell>
                        <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">
                          {server.last_seen_at
                            ? formatRelative(server.last_seen_at)
                            : t("common.never")}
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      )}
    </Page>
  )
}
