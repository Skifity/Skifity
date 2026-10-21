import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ArrowLeftIcon,
  ArrowUpCircleIcon,
  InfoIcon,
  PencilIcon,
  RefreshCwIcon,
  ServerIcon,
  Trash2Icon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { OperationProgress, sayServerStatus } from "@/components/operation-progress"
import { ServerGPUCard } from "@/components/server-gpus"
import { ServerHardening } from "@/components/server-hardening"
import { ServerUsageCard } from "@/components/server-usage-card"
import { cloudProviderTitle } from "@/components/settings/cloud-providers"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { ApiError, api } from "@/lib/api"
import { formatCPU, formatDateTime, formatMemory, formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { NodeInfo, Operation, Server } from "@/lib/types"

/**
 * What Retry runs is adding the server. After a removal or a promotion that
 * failed it would join the server back to the cluster, so it is offered only
 * after an add; the panel refuses the rest.
 */
const RETRIED_KINDS = ["server.add", "server.create"]

/** A server in these states is being changed already, by somebody else's hand. */
const BUSY_STATUSES: Server["status"][] = ["pending", "provisioning", "removing"]

export function ServerDetailPage() {
  const { t } = useTranslation()
  const { serverId = "" } = useParams()
  const navigate = useNavigate()
  const { team, user } = useSession()
  // Which machines are in the cluster is the panel administrator's to change.
  const canChangeServers = user?.is_admin === true
  // Renaming one and setting its alerts are a team administrator's.
  const teamAdmin = team?.role === "admin" || team?.role === "owner"
  const confirm = useConfirm()
  const [wipe, setWipe] = useState(true)
  const [deleteMachine, setDeleteMachine] = useState(true)
  // Asked for only once the panel has said data lives only on this server.
  const [acceptDataLoss, setAcceptDataLoss] = useState(false)
  const [name, setName] = useState<string | null>(null)
  const [operationId, setOperationId] = useState<string | null>(null)

  const server = useQuery({
    queryKey: ["server", serverId],
    queryFn: () => api.get<Server>(`/api/servers/${serverId}`),
  })

  const metrics = useQuery({
    queryKey: ["server-metrics", serverId],
    queryFn: () => api.get<NodeInfo>(`/api/servers/${serverId}/metrics`),
    enabled: server.data?.status === "ready",
    refetchInterval: 15_000,
    retry: false,
  })

  useEvents(
    team ? [`team:${team.id}`] : [],
    {
      operation: () => void queryClient.invalidateQueries({ queryKey: ["server", serverId] }),
      // The watcher noticing this server stop answering, which is exactly what
      // somebody on this page is watching for.
      server: () => void queryClient.invalidateQueries({ queryKey: ["server", serverId] }),
    },
    // Refused to a member limited to projects, who cannot open a server at all.
    Boolean(team) && !team?.scoped,
  )

  const rename = useMutation({
    mutationFn: (next: string) => api.patch(`/api/servers/${serverId}`, { name: next }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["server", serverId] })
      setName(null)
    },
  })

  const retry = useMutation({
    mutationFn: () => api.post<Operation>(`/api/servers/${serverId}/retry`),
    onSuccess: (operation) => setOperationId(operation.id),
  })

  const promote = useMutation({
    mutationFn: () => api.post<Operation>(`/api/servers/${serverId}/promote`),
    onSuccess: (operation) => setOperationId(operation.id),
  })

  const remove = useMutation({
    // A machine the panel created can go with it; the name typed into the
    // dialog is sent as the confirmation the API asks for.
    mutationFn: ({
      machine,
      acceptLoss,
    }: {
      machine: { name: string } | null
      acceptLoss: boolean
    }) =>
      api.delete<Operation>(
        (machine
          ? `/api/servers/${serverId}?delete_machine=true&confirm=${encodeURIComponent(machine.name)}`
          : `/api/servers/${serverId}?wipe=${wipe}`) + (acceptLoss ? "&accept_data_loss=true" : ""),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["servers", team?.id] })
      navigate("/servers")
    },
  })
  // The volumes the panel said exist only here, from its refusal to remove it.
  const localVolumes = localVolumesIn(remove.error)

  if (server.error)
    return <ErrorDisplay error={server.error} onRetry={() => void server.refetch()} />
  if (server.isLoading || !server.data) return <Skeleton className="h-96" />

  const current = server.data
  // Only a server the panel created at a provider has a machine it may delete.
  const cloud = current.cloud
  const cloudTitle = cloud ? cloudProviderTitle(cloud.provider_kind) : ""
  const deletingMachine = Boolean(cloud) && deleteMachine
  const latest = current.operation
  // Being added, removed or promoted: a promotion leaves the status alone, so
  // the operation says so too.
  const busy =
    BUSY_STATUSES.includes(current.status) ||
    latest?.status === "pending" ||
    latest?.status === "running"
  const canRetry =
    canChangeServers &&
    current.status === "failed" &&
    !current.adopted &&
    latest != null &&
    RETRIED_KINDS.includes(latest.kind)
  // The one started here, or what was being done when the page opened: a
  // reload in the middle of a ten-minute add resumes its progress, and one
  // that failed shows the step, its cause and its fix.
  const liveOperationId =
    operationId ?? (latest && latest.status !== "succeeded" ? latest.id : null)
  // Ticking "Remove anyway" counts only while the panel's list of volumes is
  // on screen, so what the dialog says and what is sent agree.
  const acceptingLoss = acceptDataLoss && localVolumes.length > 0
  const machineConsequence = deletingMachine
    ? t("servers.cloud.deleteMachineConsequence", { provider: cloudTitle })
    : wipe
      ? t("servers.wipeServer")
      : undefined

  return (
    <Page>
      {name !== null ? (
        <form
          className="flex flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            rename.mutate(name.trim())
          }}
        >
          <FieldLabel htmlFor="server-rename" className="sr-only">
            {t("servers.serverName")}
          </FieldLabel>
          <Input
            id="server-rename"
            value={name}
            onChange={(event) => setName(event.target.value)}
            autoFocus
            className="max-w-xs text-lg font-semibold"
          />
          <Button type="submit" size="sm" disabled={rename.isPending}>
            {rename.isPending && <Spinner />}
            {t("common.save")}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => setName(null)}>
            {t("common.cancel")}
          </Button>
        </form>
      ) : null}

      <PageHeader
        icon={ServerIcon}
        title={name === null ? current.name : undefined}
        description={`${current.ssh_user}@${current.host}:${current.ssh_port}`}
        back={
          <Button variant="ghost" size="sm" asChild className="-ml-2">
            <Link to="/servers">
              <ArrowLeftIcon />
              {t("servers.title")}
            </Link>
          </Button>
        }
        badge={
          <>
            <StatusBadge
              status={current.status}
              label={t(`servers.status.${current.status}`, { defaultValue: current.status })}
            />
            {name === null && teamAdmin && (
              <Button
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs text-muted-foreground"
                onClick={() => setName(current.name)}
              >
                <PencilIcon />
                {t("common.edit")}
              </Button>
            )}
          </>
        }
        actions={
          <>
            {canRetry && (
              <Button variant="outline" disabled={retry.isPending} onClick={() => retry.mutate()}>
                <RefreshCwIcon className="size-4" />
                {t("common.retry")}
              </Button>
            )}
            {canChangeServers &&
              current.role === "worker" &&
              current.status === "ready" &&
              !busy &&
              !current.adopted && (
                <Button
                  variant="outline"
                  disabled={promote.isPending}
                  onClick={() => {
                    void confirm({
                      title: t("servers.promote"),
                      description: t("servers.promoteHelp"),
                      confirmLabel: t("servers.promote"),
                    }).then((yes) => {
                      if (yes) promote.mutate()
                    })
                  }}
                >
                  <ArrowUpCircleIcon />
                  {t("servers.promote")}
                </Button>
              )}
          </>
        }
      />

      {current.status_detail && (
        <p className="-mt-3 text-sm text-muted-foreground">{sayServerStatus(t, current)}</p>
      )}

      {retry.error != null && <ErrorDisplay error={retry.error} />}
      {promote.error != null && <ErrorDisplay error={promote.error} />}
      {rename.error != null && <ErrorDisplay error={rename.error} />}

      {liveOperationId && (
        <LiveOperation
          key={liveOperationId}
          operationId={liveOperationId}
          live={Boolean(team) && !team?.scoped}
        />
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("servers.requirements")}</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-x-8 gap-y-3 sm:grid-cols-2">
          <Fact
            label={t("common.type")}
            value={t(`servers.role.${current.role}`, { defaultValue: current.role })}
          />
          <Fact
            label={t("servers.operatingSystem")}
            value={current.os_info || t("common.unknown")}
          />
          <Fact
            label={t("servers.cpu")}
            value={current.cpu_cores ? String(current.cpu_cores) : "—"}
          />
          <Fact
            label={t("servers.memory")}
            value={current.memory_mb ? formatMemory(current.memory_mb) : "—"}
          />
          <Fact label={t("servers.disk")} value={current.disk_gb ? `${current.disk_gb} GB` : "—"} />
          <Fact label={t("servers.architecture")} value={current.arch || "—"} />
          <Fact label={t("apps.node")} value={current.node_name || "—"} mono />
          <Fact
            label={t("servers.lastSeen")}
            value={current.last_seen_at ? formatRelative(current.last_seen_at) : t("common.never")}
          />
          <Fact label={t("common.created")} value={formatDateTime(current.created_at)} />
          {cloud && (
            <Fact
              label={t("servers.cloud.createdAt", { provider: cloudTitle })}
              value={`${cloud.server_type} · ${cloud.location} · ${cloud.image}`}
              mono
            />
          )}
        </CardContent>
      </Card>

      {metrics.data && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("dashboard.clusterHealth")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-5">
            <Usage
              label={t("dashboard.cpuUsed")}
              used={metrics.data.cpu_used_m}
              total={metrics.data.cpu_capacity_m}
              render={formatCPU}
            />
            <Usage
              label={t("dashboard.memoryUsed")}
              used={metrics.data.memory_used_mb}
              total={metrics.data.memory_capacity_mb}
              render={formatMemory}
            />
            {(metrics.data.disk_capacity_mb ?? 0) > 0 && (
              <Usage
                label={t("servers.diskUsed")}
                used={metrics.data.disk_used_mb ?? 0}
                total={metrics.data.disk_capacity_mb ?? 0}
                render={formatMemory}
              />
            )}
            <Fact label={t("apps.instances")} value={String(metrics.data.pod_count)} />
            <Fact label={t("servers.kubeletVersion")} value={metrics.data.kubelet_version} mono />
          </CardContent>
        </Card>
      )}

      {/* What its GPUs are doing, or what is missing for a card Kubernetes
          cannot use yet. Live, like the card above. */}
      {metrics.data && (
        <ServerGPUCard serverId={serverId} node={metrics.data} isAdmin={canChangeServers} />
      )}

      {/* History outlives the node: a server that stopped answering is the
          one whose last hours somebody wants to see. */}
      {(current.status === "ready" || current.status === "not_ready") && (
        <ServerUsageCard serverId={serverId} canEdit={teamAdmin} />
      )}

      {/* It signs in over SSH with the key Skifity installed, so it is for a
          panel administrator, and only on a server Skifity added. */}
      {canChangeServers && !current.adopted && current.status === "ready" && (
        <ServerHardening serverId={serverId} />
      )}

      {/*
        A server Skifity found rather than installed — normally the machine the
        panel itself runs on. There is no key to it, so offering Remove would
        offer an action that can only end in an SSH error.
      */}
      {current.adopted && (
        <Alert>
          <InfoIcon />
          <AlertTitle>{t("servers.adopted")}</AlertTitle>
          <AlertDescription>{t("servers.adoptedHelp")}</AlertDescription>
        </Alert>
      )}

      {/* Not while it is being added, removed or promoted: the panel refuses
          a second change on top of one that is running. */}
      {canChangeServers && !current.adopted && !busy && (
        <Card className="border-destructive/30">
          <CardHeader>
            <CardTitle className="text-base text-destructive">
              {t("servers.removeServer")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <p className="text-sm text-muted-foreground">
              {localVolumes.length > 0
                ? t("servers.removeServerDataLoss")
                : t("servers.removeServerWarning")}
            </p>
            {cloud && (
              <label className="flex items-center gap-2.5 text-sm">
                <Checkbox
                  checked={deleteMachine}
                  onCheckedChange={(checked) => setDeleteMachine(checked === true)}
                />
                {t("servers.cloud.deleteMachine", { provider: cloudTitle })}
              </label>
            )}
            {cloud && !deleteMachine && (
              <p className="text-sm text-muted-foreground">
                {t("servers.cloud.keepsRunning", { provider: cloudTitle })}
              </p>
            )}
            {!deletingMachine && (
              <label className="flex items-center gap-2.5 text-sm">
                <Checkbox checked={wipe} onCheckedChange={(checked) => setWipe(checked === true)} />
                {t("servers.wipeServer")}
              </label>
            )}
            {remove.error != null && <ErrorDisplay error={remove.error} compact />}
            {/* The refusal's title and fix are above; which volumes it means
                is what somebody needs before ticking the box. */}
            {localVolumes.length > 0 && (
              <div className="space-y-2">
                <p className="text-sm">
                  {t("servers.localVolumes", { count: localVolumes.length })}
                </p>
                <ul className="list-disc space-y-0.5 pl-5 font-mono text-xs">
                  {localVolumes.map((volume) => (
                    <li key={volume}>{volume}</li>
                  ))}
                </ul>
                <label className="flex items-center gap-2.5 text-sm text-destructive">
                  <Checkbox
                    checked={acceptDataLoss}
                    onCheckedChange={(checked) => setAcceptDataLoss(checked === true)}
                  />
                  {t("servers.acceptDataLoss")}
                </label>
              </div>
            )}
            <Button
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => {
                const acceptLoss = acceptingLoss
                void confirm({
                  title: t("servers.removeServer"),
                  description: acceptLoss
                    ? t("servers.removeServerDataLoss")
                    : t("servers.removeServerWarning"),
                  consequence:
                    [
                      acceptLoss &&
                        t("servers.dataLossConsequence", { volumes: localVolumes.join(", ") }),
                      machineConsequence,
                    ]
                      .filter(Boolean)
                      .join(" ") || undefined,
                  confirmLabel: t("servers.removeServer"),
                  destructive: true,
                  typeToConfirm: current.name,
                }).then((yes) => {
                  if (yes) {
                    remove.mutate({
                      machine: deletingMachine ? { name: current.name } : null,
                      acceptLoss,
                    })
                  }
                })
              }}
            >
              <Trash2Icon className="size-4" />
              {t("servers.removeServer")}
            </Button>
          </CardContent>
        </Card>
      )}
    </Page>
  )
}

/**
 * Reads the volumes out of the panel's refusal to remove a server that holds
 * the only copy of them. Its cause names five; the context has them all.
 */
function localVolumesIn(error: unknown): string[] {
  if (!(error instanceof ApiError) || error.problem.code !== "server.has_local_volumes") return []
  return (error.problem.context?.volumes ?? "").split(", ").filter(Boolean)
}

/**
 * One operation, step by step, as it happens.
 *
 * The event stream writes each new state into the query the card reads, so
 * there is one copy of the operation: a refetch after a missed event replaces
 * it, and a poll covers a stream that dropped. Keyed by the operation, so a
 * retry or a promotion does not show the last one's steps first.
 */
function LiveOperation({ operationId, live }: { operationId: string; live: boolean }) {
  const operation = useQuery({
    queryKey: ["operation", operationId],
    queryFn: () => api.get<Operation>(`/api/operations/${operationId}`),
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === "pending" || status === "running" ? 5_000 : false
    },
  })

  useEvents(
    [`operation:${operationId}`],
    {
      operation: (data) => queryClient.setQueryData(["operation", operationId], data as Operation),
    },
    live,
  )

  if (!operation.data) return null

  return (
    <Card>
      <CardContent className="pt-6">
        <OperationProgress operation={operation.data} />
      </CardContent>
    </Card>
  )
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={mono ? "font-mono text-sm" : "text-sm"}>{value}</dd>
    </div>
  )
}

function Usage({
  label,
  used,
  total,
  render,
}: {
  label: string
  used: number
  total: number
  render: (value: number) => string
}) {
  const percent = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between text-sm">
        <span>{label}</span>
        <span className="tabular-nums text-muted-foreground">
          {render(used)} / {render(total)}
        </span>
      </div>
      <Progress value={percent} />
    </div>
  )
}
