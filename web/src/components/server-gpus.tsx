import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BookOpenIcon, CpuIcon, PackagePlusIcon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldContent, FieldDescription, FieldTitle } from "@/components/ui/field"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { api, type List } from "@/lib/api"
import { formatMemory } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { Component, NodeGPU, NodeInfo } from "@/lib/types"

/** The component "Enable GPUs" installs; see internal/cluster/gpu.go. */
export const NVIDIA_COMPONENT = "nvidia-device-plugin"

/** The docs page for setting a server up, with the commands the panel does not run. */
const SETUP_DOCS = "/docs/gpus#setting-up-a-server"

/** Whether the device plugin is installed, read from the components list. Administrators only. */
export function useGPUComponent(enabled: boolean) {
  return useQuery({
    queryKey: ["components"],
    queryFn: () => api.get<List<Component>>("/api/components"),
    enabled,
    select: (data) => data.items.find((component) => component.name === NVIDIA_COMPONENT),
  })
}

/** Installs NVIDIA's device plugin, after saying what it does and what it does not. */
export function EnableGPUsButton({ size = "sm" }: { size?: "sm" | "default" }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const install = useMutation({
    mutationFn: () => api.post(`/api/components/${NVIDIA_COMPONENT}/install`),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["components"] })
      void queryClient.invalidateQueries({ queryKey: ["server-metrics"] })
      void queryClient.invalidateQueries({ queryKey: ["cluster"] })
    },
  })
  return (
    <div className="space-y-2">
      <Button
        size={size}
        disabled={install.isPending}
        onClick={() => {
          void confirm({
            title: t("servers.gpu.enableConfirmTitle"),
            description: t("servers.gpu.enableConfirm"),
            confirmLabel: t("servers.gpu.enable"),
          }).then((yes) => {
            if (yes) install.mutate()
          })
        }}
      >
        {install.isPending ? <Spinner /> : <PackagePlusIcon />}
        {t("servers.gpu.enable")}
      </Button>
      {install.error != null && <ErrorDisplay error={install.error} compact />}
    </div>
  )
}

/** "2 × NVIDIA-A10, 1 in use", for a table cell. */
export function GPUSummary({ node }: { node?: NodeInfo }) {
  const { t } = useTranslation()
  const gpus = node?.gpus ?? []
  if (gpus.length > 0) {
    return (
      <span className="space-y-0.5">
        {gpus.map((gpu) => (
          <span key={gpu.resource} className="block tabular-nums">
            {t("servers.gpu.count", {
              n: gpu.allocatable,
              name: gpu.product || t(`servers.gpu.vendors.${gpu.vendor}`),
            })}
            <span className="block text-xs text-muted-foreground">
              {t("servers.gpu.inUse", { used: gpu.in_use, total: gpu.allocatable })}
            </span>
          </span>
        ))}
      </span>
    )
  }
  if (node?.gpu_hardware) {
    return (
      <Badge variant="outline" className="border-warning/40 text-warning">
        {t("servers.gpu.needsSetup")}
      </Badge>
    )
  }
  return <span className="text-muted-foreground">—</span>
}

/**
 * A server's GPUs: what its device plugins offer and how much of it is taken;
 * and for a server with a card Kubernetes cannot use yet, what is missing.
 * An administrator can install the plugin and mark the server from here.
 */
export function ServerGPUCard({
  serverId,
  node,
  isAdmin,
}: {
  serverId: string
  node: NodeInfo
  isAdmin: boolean
}) {
  const { t } = useTranslation()
  const component = useGPUComponent(isAdmin)
  const pluginInstalled = component.data?.status === "installed"

  const mark = useMutation({
    mutationFn: (nvidia: boolean) => api.put(`/api/servers/${serverId}/gpu`, { nvidia }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["server-metrics", serverId] })
      void queryClient.invalidateQueries({ queryKey: ["cluster"] })
    },
  })

  const gpus = node.gpus ?? []
  // A card is known to be here and Kubernetes cannot use it: say why.
  const missing =
    gpus.length === 0 && node.gpu_hardware != null
      ? whatIsMissing(node, isAdmin, pluginInstalled)
      : null

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <CpuIcon className="size-4" />
          {t("servers.gpu.title")}
        </CardTitle>
        <CardDescription>{t("servers.gpu.help")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {gpus.map((gpu) => (
          <GPUUsage key={gpu.resource} gpu={gpu} />
        ))}

        {missing != null && (
          <Alert variant="warning">
            <CpuIcon />
            <AlertTitle>{t(`servers.gpu.hardware.${node.gpu_hardware}`)}</AlertTitle>
            <AlertDescription className="space-y-2">
              <p>{t(missing.key, missing.params)}</p>
              <p>{t("servers.gpu.driverNote")}</p>
              {isAdmin && component.isSuccess && !pluginInstalled && <EnableGPUsButton />}
              <Button variant="ghost" size="sm" className="-ml-2" asChild>
                <a href={SETUP_DOCS} target="_blank" rel="noreferrer">
                  <BookOpenIcon className="size-3.5" />
                  {t("servers.gpu.docs")}
                </a>
              </Button>
            </AlertDescription>
          </Alert>
        )}

        {gpus.length === 0 && missing == null && (
          <p className="text-sm text-muted-foreground">
            {node.nfd ? t("servers.gpu.nfdNone") : t("servers.gpu.undetectable")}
          </p>
        )}

        {/* Only where nothing else can tell; a card the plugin already
            offers, or one NFD found, needs no label. */}
        {isAdmin && (node.gpu_labelled || (gpus.length === 0 && node.gpu_hardware == null)) && (
          <Field orientation="horizontal">
            <FieldContent>
              <FieldTitle>{t("servers.gpu.mark")}</FieldTitle>
              <FieldDescription>{t("servers.gpu.markHelp")}</FieldDescription>
            </FieldContent>
            <Switch
              checked={node.gpu_labelled === true}
              disabled={mark.isPending}
              onCheckedChange={(checked) => mark.mutate(checked)}
            />
          </Field>
        )}
        {mark.error != null && <ErrorDisplay error={mark.error} compact />}
      </CardContent>
    </Card>
  )
}

/** Which of the pieces is not there yet, as the sentence that says so. */
function whatIsMissing(
  node: NodeInfo,
  isAdmin: boolean,
  pluginInstalled: boolean,
): { key: string; params?: Record<string, string> } {
  const plugin = node.gpu_plugin
  if (plugin != null) {
    // Running and not yet offering the card: it registers within seconds.
    if (plugin.ready) return { key: "servers.gpu.pluginStarting" }
    const reason = plugin.reason ?? ""
    // containerd's `no runtime for "nvidia" is configured`: no toolkit.
    if (reason.includes('"nvidia"')) return { key: "servers.gpu.pluginRuntime" }
    return { key: "servers.gpu.pluginFailing", params: { reason } }
  }
  // A non-administrator cannot read whether it is installed.
  if (isAdmin && !pluginInstalled) return { key: "servers.gpu.missingPlugin" }
  return { key: "servers.gpu.pluginNotHere" }
}

function GPUUsage({ gpu }: { gpu: NodeGPU }) {
  const { t } = useTranslation()
  const percent = gpu.allocatable > 0 ? Math.round((gpu.in_use / gpu.allocatable) * 100) : 0
  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span>
          {t("servers.gpu.count", {
            n: gpu.allocatable,
            name: gpu.product || t(`servers.gpu.vendors.${gpu.vendor}`),
          })}
          {gpu.memory_mb ? (
            <span className="ml-2 text-xs text-muted-foreground">
              {t("servers.gpu.memory", { memory: formatMemory(gpu.memory_mb) })}
            </span>
          ) : null}
        </span>
        <span className="tabular-nums text-muted-foreground">
          {t("servers.gpu.inUse", { used: gpu.in_use, total: gpu.allocatable })}
        </span>
      </div>
      <Progress value={Math.min(100, percent)} />
      {gpu.capacity > gpu.allocatable && (
        <p className="text-xs text-warning">
          {t("servers.gpu.unhealthy", { n: gpu.capacity - gpu.allocatable })}
        </p>
      )}
    </div>
  )
}
