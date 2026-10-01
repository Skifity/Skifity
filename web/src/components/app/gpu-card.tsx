import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { BookOpenIcon, CpuIcon, InfoIcon, TriangleAlertIcon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field"
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
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, AppGPUSettings, GPUVendor } from "@/lib/types"

const VENDORS: GPUVendor[] = ["nvidia", "amd", "intel"]
/** The most one instance may ask for; kube.MaxGPUs. */
const MAX_GPUS = 16
/** The app itself, among the workloads that can be given GPUs. */
const WEB = "web"
/** A Select item cannot have an empty value, so "any model" is this. */
const ANY = "__any__"

/**
 * The GPUs an app's instances are given: how many, whose, a model to prefer,
 * and which of its workloads get them. What the cluster offers is beside each
 * choice, so nobody asks for a card no server has and learns it from an error.
 */
export function GPUCard({ app }: { app: App }) {
  const { t } = useTranslation()
  const view = useQuery({
    queryKey: ["gpu", app.id],
    queryFn: () => api.get<AppGPUSettings>(`/api/apps/${app.id}/gpu`),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <CpuIcon className="size-4" />
          {t("apps.gpu.title")}
        </CardTitle>
        <CardDescription>{t("apps.gpu.help")}</CardDescription>
      </CardHeader>
      <CardContent>
        {view.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : view.error != null ? (
          <ErrorDisplay error={view.error} compact />
        ) : (
          // Keyed on what is stored, so the form starts from it, and again
          // whenever it changes, without an effect copying it into state.
          <GPUForm key={JSON.stringify(view.data.gpu)} app={app} view={view.data} />
        )}
      </CardContent>
    </Card>
  )
}

function GPUForm({ app, view }: { app: App; view: AppGPUSettings }) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const stored = view.gpu
  const [enabled, setEnabled] = useState(stored.count > 0)
  const [count, setCount] = useState(String(stored.count > 0 ? stored.count : 1))
  const [vendor, setVendor] = useState<GPUVendor>(stored.vendor || "nvidia")
  const [product, setProduct] = useState(stored.product)
  const [workloads, setWorkloads] = useState<string[]>(
    stored.workloads.length > 0 ? stored.workloads : [WEB],
  )

  const offers = view.cluster.vendors
  const offer = offers.find((candidate) => candidate.vendor === vendor)
  const anyOffered = offers.some((candidate) => candidate.allocatable > 0)
  // A model the app prefers stays in the list after the server that had it
  // went, so the choice is not quietly dropped.
  const products = [...(offer?.products ?? [])]
  if (product && !products.includes(product)) products.push(product)

  const save = useMutation({
    mutationFn: (body: object) => api.put<AppGPUSettings>(`/api/apps/${app.id}/gpu`, body),
    onSuccess: (data) => {
      queryClient.setQueryData(["gpu", app.id], data)
      void queryClient.invalidateQueries({ queryKey: ["app", app.id] })
    },
  })

  const submit = () => {
    if (!enabled) {
      if (stored.count === 0) return
      // Taking a card away from an app that needs one stops it working.
      void confirm({
        title: t("apps.gpu.remove"),
        description: t("apps.gpu.removeConfirm"),
        confirmLabel: t("apps.gpu.remove"),
        destructive: true,
      }).then((yes) => {
        if (yes) save.mutate({ count: 0 })
      })
      return
    }
    save.mutate({
      count: Number(count),
      vendor,
      product: vendor === "nvidia" ? product : "",
      workloads,
    })
  }

  const toggleWorkload = (name: string, checked: boolean) =>
    setWorkloads((current) =>
      checked ? [...current, name] : current.filter((workload) => workload !== name),
    )

  // Refused by the panel either way; said here before anybody presses Save.
  const sleepsWithGPU = enabled && app.scale_to_zero && workloads.includes(WEB)

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        submit()
      }}
    >
      {!view.cluster.known ? (
        <p className="text-sm text-muted-foreground">{t("apps.gpu.unknown")}</p>
      ) : (
        !anyOffered && (
          <Alert>
            <InfoIcon />
            <AlertTitle>{t("apps.gpu.noneOffered")}</AlertTitle>
            <AlertDescription>
              <p>{t("apps.gpu.noneOfferedHelp")}</p>
              <Button variant="ghost" size="sm" className="-ml-2" asChild>
                <a href="/docs/gpus#setting-up-a-server" target="_blank" rel="noreferrer">
                  <BookOpenIcon className="size-3.5" />
                  {t("apps.gpu.setUp")}
                </a>
              </Button>
            </AlertDescription>
          </Alert>
        )
      )}

      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>{t("apps.gpu.enabled")}</FieldTitle>
          <FieldDescription>{t("apps.gpu.enabledHelp")}</FieldDescription>
        </FieldContent>
        <Switch checked={enabled} onCheckedChange={setEnabled} />
      </Field>

      {enabled && (
        <>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="gpu-vendor">{t("apps.gpu.vendor")}</FieldLabel>
              <Select value={vendor} onValueChange={(value) => setVendor(value as GPUVendor)}>
                <SelectTrigger id="gpu-vendor" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {VENDORS.map((name) => {
                    const available = offers.find((candidate) => candidate.vendor === name)
                    return (
                      <SelectItem key={name} value={name}>
                        {t(`servers.gpu.vendors.${name}`)}
                        <span className="text-xs text-muted-foreground">
                          {available && available.allocatable > 0
                            ? t("apps.gpu.vendorOffer", {
                                total: available.allocatable,
                                used: available.in_use,
                              })
                            : view.cluster.known
                              ? t("apps.gpu.vendorNone")
                              : ""}
                        </span>
                      </SelectItem>
                    )
                  })}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="gpu-count">{t("apps.gpu.count")}</FieldLabel>
              <Input
                id="gpu-count"
                type="number"
                min={1}
                max={MAX_GPUS}
                value={count}
                onChange={(event) => setCount(event.target.value)}
                className="w-32"
              />
              {offer != null && offer.most_on_one_server > 0 && (
                <FieldDescription>
                  {t("apps.gpu.countHelp", { most: offer.most_on_one_server })}
                </FieldDescription>
              )}
            </Field>
          </div>

          {vendor === "nvidia" && (
            <Field>
              <FieldLabel htmlFor="gpu-product">{t("apps.gpu.product")}</FieldLabel>
              <Select
                value={product || ANY}
                onValueChange={(value) => setProduct(value === ANY ? "" : value)}
              >
                <SelectTrigger id="gpu-product" className="w-full sm:w-72">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ANY}>{t("apps.gpu.productAny")}</SelectItem>
                  {products.map((name) => (
                    <SelectItem key={name} value={name} className="font-mono">
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t("apps.gpu.productHelp")}</FieldDescription>
            </Field>
          )}

          <Field>
            <FieldTitle>{t("apps.gpu.workloads")}</FieldTitle>
            <div className="space-y-1">
              {[WEB, ...view.processes].map((name) => (
                <label key={name} className="flex items-center gap-2.5 text-sm">
                  <Checkbox
                    checked={workloads.includes(name)}
                    onCheckedChange={(checked) => toggleWorkload(name, checked === true)}
                  />
                  {name === WEB ? (
                    t("apps.gpu.workloadWeb")
                  ) : (
                    <span className="font-mono">{name}</span>
                  )}
                </label>
              ))}
            </div>
            <FieldDescription>{t("apps.gpu.workloadsHelp")}</FieldDescription>
          </Field>

          <Alert variant="info">
            <InfoIcon />
            <AlertDescription>{t("apps.gpu.rollout")}</AlertDescription>
          </Alert>
        </>
      )}

      {sleepsWithGPU && (
        <Alert variant="warning">
          <TriangleAlertIcon />
          <AlertDescription>{t("apps.gpu.scaleToZero")}</AlertDescription>
        </Alert>
      )}

      {stored.count > 0 &&
        view.warnings.map((warning) => (
          <Alert key={warning.code + JSON.stringify(warning.params ?? {})} variant="warning">
            <TriangleAlertIcon />
            <AlertDescription>
              {t(`apps.gpu.warning.${warning.code}`, {
                ...warning.params,
                defaultValue: warning.text,
              })}
            </AlertDescription>
          </Alert>
        ))}

      {save.error != null && <ErrorDisplay error={save.error} compact />}

      <div className="flex justify-end">
        <Button
          type="submit"
          disabled={
            save.isPending ||
            (enabled && workloads.length === 0) ||
            (!enabled && stored.count === 0)
          }
        >
          {save.isPending && <Spinner />}
          {save.isPending ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </form>
  )
}
