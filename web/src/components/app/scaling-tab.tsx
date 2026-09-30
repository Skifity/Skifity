import { useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CheckCircle2Icon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { translated } from "@/lib/say"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type { App, Scaling, ScalingFinding } from "@/lib/types"

/**
 * Scaling, with the readiness check in front of it.
 *
 * Running several instances of an app that writes to a local disk or keeps
 * sessions in memory breaks it in ways that are hard to diagnose. The panel
 * looks for those before the user commits, which is the moment the warning is
 * worth something.
 */
export function ScalingTab({ app }: { app: App }) {
  const { t } = useTranslation()

  const scaling = useQuery({
    queryKey: ["scaling", app.id],
    queryFn: () => api.get<Scaling>(`/api/apps/${app.id}/scaling`),
  })

  const readiness = useQuery({
    queryKey: ["scaling-readiness", app.id],
    queryFn: () => api.get<List<ScalingFinding>>(`/api/apps/${app.id}/scaling/readiness`),
  })

  // The edited values sit on top of what was loaded, rather than being copied
  // into state by an effect: until something is changed there is nothing to
  // keep in sync, and a refetch cannot silently discard an edit in progress.
  const [draft, setDraft] = useState<Scaling | null>(null)
  const form = draft ?? scaling.data ?? null
  const setForm = setDraft

  const save = useMutation({
    mutationFn: (next: Scaling) => api.put(`/api/apps/${app.id}/scaling`, next),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["scaling", app.id] })
      void queryClient.invalidateQueries({ queryKey: ["app-status", app.id] })
    },
  })

  const resources = useMutation({
    mutationFn: (next: Partial<App>) => api.patch(`/api/apps/${app.id}`, next),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["app", app.id] }),
  })

  const [cpuRequest, setCPURequest] = useState(String(app.cpu_request_m))
  const [cpuLimit, setCPULimit] = useState(String(app.cpu_limit_m))
  const [memRequest, setMemRequest] = useState(String(app.mem_request_mb))
  const [memLimit, setMemLimit] = useState(String(app.mem_limit_mb))

  if (scaling.isLoading || !form) return <Skeleton className="h-64" />
  if (scaling.error)
    return <ErrorDisplay error={scaling.error} onRetry={() => void scaling.refetch()} />

  const findings = readiness.data?.items ?? []

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("scaling.readiness")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {readiness.isLoading ? (
            <p className="text-sm text-muted-foreground">{t("scaling.readinessChecking")}</p>
          ) : findings.length === 0 ? (
            <p className="flex items-center gap-2 text-sm text-success">
              <CheckCircle2Icon className="size-4" />
              {t("scaling.readinessOk")}
            </p>
          ) : (
            findings.map((finding) => (
              <Alert
                key={finding.code}
                variant={finding.severity === "error" ? "destructive" : "default"}
              >
                <AlertTitle>{say(t, finding, "title")}</AlertTitle>
                <AlertDescription>
                  <p>{say(t, finding, "detail")}</p>
                  <p className="mt-1 font-medium">{say(t, finding, "fix")}</p>
                </AlertDescription>
              </Alert>
            ))
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("scaling.title")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">
              {t("scaling.automatic")}
              {/*
                The line under the switch says what the switch does, and used to
                repeat its own label when autoscaling was on: "Scale
                automatically / Scale automatically".
              */}
              <span className="block text-xs text-muted-foreground">
                {form.autoscale ? t("scaling.automaticHelp") : t("scaling.fixed")}
              </span>
            </span>
            <Switch
              checked={form.autoscale}
              onCheckedChange={(checked) => setForm({ ...form, autoscale: checked })}
            />
          </label>

          {form.autoscale ? (
            <div className="grid gap-4 sm:grid-cols-2">
              <NumberField
                id="min-replicas"
                label={t("scaling.minInstances")}
                value={form.min_replicas}
                min={form.scale_to_zero ? 0 : 1}
                onChange={(value) => setForm({ ...form, min_replicas: value })}
              />
              <NumberField
                id="max-replicas"
                label={t("scaling.maxInstances")}
                value={form.max_replicas}
                min={1}
                onChange={(value) => setForm({ ...form, max_replicas: value })}
              />
              <NumberField
                id="cpu-target"
                label={t("scaling.cpuTarget")}
                value={form.cpu_target}
                min={1}
                max={100}
                suffix="%"
                onChange={(value) => setForm({ ...form, cpu_target: value })}
              />
              <NumberField
                id="memory-target"
                label={t("scaling.memoryTarget")}
                value={form.memory_target}
                min={0}
                max={100}
                suffix="%"
                onChange={(value) => setForm({ ...form, memory_target: value })}
              />
            </div>
          ) : (
            <NumberField
              id="replicas"
              label={t("scaling.instances")}
              value={form.replicas}
              min={0}
              onChange={(value) => setForm({ ...form, replicas: value })}
            />
          )}

          <label className="flex items-center justify-between gap-4">
            <span className="text-sm">
              {t("scaling.scaleToZero")}
              <span className="block text-xs text-muted-foreground">
                {t("scaling.scaleToZeroHelp")}
              </span>
            </span>
            <Switch
              checked={form.scale_to_zero}
              onCheckedChange={(checked) => setForm({ ...form, scale_to_zero: checked })}
            />
          </label>

          {/*
            Said out loud rather than left for somebody to discover. An app that
            can stop when idle is woken and scaled by the requests waiting for
            it, so the CPU and memory targets above stop being what decides:
            leaving two fields on screen that no longer do anything is the kind
            of quiet lie this panel is meant not to tell.
          */}
          {form.autoscale && form.scale_to_zero && (
            <p className="text-xs text-muted-foreground">
              {t("scaling.scaleToZeroUsesRequests")}
            </p>
          )}

          <p className="text-xs text-muted-foreground">{t("scaling.spreadHelp")}</p>

          {save.error != null && <ErrorDisplay error={save.error} compact />}

          <div className="flex justify-end">
            <Button disabled={save.isPending} onClick={() => save.mutate(form)}>
              {save.isPending && <Spinner />}
              {save.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("scaling.resources")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">{t("scaling.resourcesHelp")}</p>
          <div className="grid gap-4 sm:grid-cols-2">
            <TextField
              id="cpu-request"
              label={t("scaling.cpuReserved")}
              value={cpuRequest}
              suffix="m"
              onChange={setCPURequest}
            />
            <TextField
              id="cpu-limit"
              label={t("scaling.cpuLimit")}
              value={cpuLimit}
              suffix="m"
              onChange={setCPULimit}
            />
            <TextField
              id="mem-request"
              label={t("scaling.memoryReserved")}
              value={memRequest}
              suffix="MB"
              onChange={setMemRequest}
            />
            <TextField
              id="mem-limit"
              label={t("scaling.memoryLimit")}
              value={memLimit}
              suffix="MB"
              onChange={setMemLimit}
            />
          </div>

          {resources.error != null && <ErrorDisplay error={resources.error} compact />}

          <div className="flex justify-end">
            <Button
              disabled={resources.isPending}
              onClick={() =>
                resources.mutate({
                  cpu_request_m: Number(cpuRequest) || 0,
                  cpu_limit_m: Number(cpuLimit) || 0,
                  mem_request_mb: Number(memRequest) || 0,
                  mem_limit_mb: Number(memLimit) || 0,
                })
              }
            >
              {resources.isPending && <Spinner />}
              {resources.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}

function NumberField({
  id,
  label,
  value,
  min,
  max,
  suffix,
  onChange,
}: {
  id: string
  label: string
  value: number
  min?: number
  max?: number
  suffix?: string
  onChange: (value: number) => void
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex items-center gap-2">
        <Input
          id={id}
          type="number"
          min={min}
          max={max}
          value={value}
          onChange={(event) => onChange(Number(event.target.value))}
        />
        {suffix && <span className="text-sm text-muted-foreground">{suffix}</span>}
      </div>
    </Field>
  )
}

function TextField({
  id,
  label,
  value,
  suffix,
  onChange,
}: {
  id: string
  label: string
  value: string
  suffix?: string
  onChange: (value: string) => void
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex items-center gap-2">
        <Input
          id={id}
          type="number"
          min={0}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
        {suffix && <span className="text-sm text-muted-foreground">{suffix}</span>}
      </div>
    </Field>
  )
}

/**
 * One sentence of a scaling finding, in the reader's language.
 *
 * The checker writes its findings in English — the API, the CLI and an
 * assistant all read them — and the panel looks each one up by the finding's
 * own code. Eleven findings, three sentences each, and every one of them was
 * English on a Russian page until the catalogue existed.
 */
function say(t: TFunction, finding: ScalingFinding, field: "title" | "detail" | "fix"): string {
  return translated(t, `scaling.finding.${finding.code}.${field}`, finding[field], finding.args?.[field])
}
