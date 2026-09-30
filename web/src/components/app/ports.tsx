import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { NetworkIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, AppPort } from "@/lib/types"

/**
 * Ports an app takes connections on that are not HTTP — a game server, an
 * MQTT broker. Each is opened on every server at its public port.
 */
export function PortsCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const ports = useQuery({
    queryKey: ["ports", app.id],
    queryFn: () => api.get<List<AppPort>>(`/api/apps/${app.id}/ports`),
  })
  const [adding, setAdding] = useState(false)
  const [port, setPort] = useState("")
  const [protocol, setProtocol] = useState("tcp")
  const [publicPort, setPublicPort] = useState("")

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["ports", app.id] })
  const open = useMutation({
    mutationFn: () =>
      api.post<AppPort>(`/api/apps/${app.id}/ports`, {
        port: Number(port),
        protocol,
        public_port: publicPort ? Number(publicPort) : undefined,
      }),
    onSuccess: () => {
      refresh()
      setAdding(false)
      setPort("")
      setPublicPort("")
    },
  })
  const close = useMutation({
    mutationFn: (id: string) => api.delete(`/api/apps/${app.id}/ports/${id}`),
    onSuccess: refresh,
  })

  const items = ports.data?.items ?? []
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-base">
            <NetworkIcon className="size-4" />
            {t("ports.title")}
          </CardTitle>
          <CardDescription>{t("ports.help")}</CardDescription>
        </div>
        {!adding && (
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            <PlusIcon className="size-4" />
            {t("ports.add")}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {ports.isLoading ? (
          <Skeleton className="h-12" />
        ) : ports.error ? (
          <ErrorDisplay error={ports.error} onRetry={() => void ports.refetch()} />
        ) : items.length === 0 ? (
          !adding && <p className="text-sm text-muted-foreground">{t("ports.empty")}</p>
        ) : (
          <ul className="divide-y rounded-md border">
            {items.map((item) => {
              const label = `${item.public_port}/${item.protocol}`
              return (
                <li key={item.id} className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
                  <Badge variant="secondary" className="font-mono">
                    {label}
                  </Badge>
                  {item.port !== item.public_port && (
                    <span className="text-xs text-muted-foreground">→ {item.port}</span>
                  )}
                  <span className="min-w-0 flex-1 text-xs text-muted-foreground">
                    {t("ports.reachedAt")}{" "}
                    {item.addresses.length > 0 ? (
                      item.addresses.map((address) => (
                        <span key={address} className="me-2 inline-flex items-center gap-1">
                          <code className="font-mono">{address}</code>
                          <CopyButton value={address} label={t("common.copy")} />
                        </span>
                      ))
                    ) : (
                      <span>{t("ports.anyServer", { port: item.public_port })}</span>
                    )}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("ports.close", { port: label })}
                    disabled={close.isPending}
                    onClick={() =>
                      void confirm({
                        title: t("ports.close", { port: label }),
                        description: t("ports.closeConfirm", { port: label }),
                        confirmLabel: t("common.delete"),
                        destructive: true,
                      }).then((yes) => {
                        if (yes) close.mutate(item.id)
                      })
                    }
                  >
                    <Trash2Icon className="size-4" />
                  </Button>
                </li>
              )
            })}
          </ul>
        )}

        {adding && (
          <form
            className="grid gap-3 sm:grid-cols-[1fr_8rem_1fr]"
            onSubmit={(event) => {
              event.preventDefault()
              open.mutate()
            }}
          >
            <Field>
              <FieldLabel htmlFor="port-port">{t("ports.port")}</FieldLabel>
              <Input
                id="port-port"
                type="number"
                min={1}
                max={65535}
                value={port}
                placeholder="25565"
                onChange={(event) => setPort(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="port-protocol">{t("ports.protocol")}</FieldLabel>
              <Select value={protocol} onValueChange={setProtocol}>
                <SelectTrigger id="port-protocol">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="tcp">TCP</SelectItem>
                  <SelectItem value="udp">UDP</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="port-public">{t("ports.publicPort")}</FieldLabel>
              <Input
                id="port-public"
                type="number"
                min={1}
                max={65535}
                value={publicPort}
                placeholder={port || undefined}
                onChange={(event) => setPublicPort(event.target.value)}
              />
            </Field>
            <FieldDescription className="sm:col-span-3">
              {t("ports.publicPortHelp")}
            </FieldDescription>
            {open.error != null && (
              <div className="sm:col-span-3">
                <ErrorDisplay error={open.error} compact />
              </div>
            )}
            <div className="flex gap-2 sm:col-span-3">
              <Button type="submit" disabled={open.isPending || !port}>
                {open.isPending && <Spinner />}
                {t("ports.add")}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setAdding(false)}>
                {t("common.cancel")}
              </Button>
            </div>
          </form>
        )}
        {close.error != null && <ErrorDisplay error={close.error} compact />}
      </CardContent>
    </Card>
  )
}
