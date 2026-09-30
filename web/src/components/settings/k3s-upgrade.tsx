import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ArrowUpCircleIcon, CheckCircle2Icon } from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { api } from "@/lib/api"
import type { K3sUpgrade, K3sUpgradePlan, UpgradeReason } from "@/lib/types"

/**
 * The k3s every server runs, and upgrading it: a plan first, with every
 * reason not to start, then the system-upgrade-controller doing it one server
 * at a time.
 */
export function K3sUpgradeCard() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  // What was typed or picked; the first release is offered until then.
  const [typed, setTyped] = useState<string | null>(null)
  const [checked, setChecked] = useState<string | null>(null)

  const current = useQuery({
    queryKey: ["k3s-upgrade"],
    queryFn: () => api.get<K3sUpgrade>("/api/k3s/upgrade"),
  })
  const plan = useQuery({
    queryKey: ["k3s-upgrade", checked],
    queryFn: () => api.get<K3sUpgrade>(`/api/k3s/upgrade?version=${encodeURIComponent(checked!)}`),
    enabled: checked !== null,
  })
  const start = useMutation({
    mutationFn: (version: string) => api.post<K3sUpgradePlan>("/api/k3s/upgrade", { version }),
  })

  if (current.isLoading) return <Skeleton className="h-40" />
  if (current.error) {
    return <ErrorDisplay error={current.error} onRetry={() => void current.refetch()} />
  }
  const data = current.data!
  const releases = data.releases
  const target = typed ?? releases.find((r) => r.stable)?.version ?? releases[0]?.version ?? ""
  const shown = checked === target ? plan.data?.plan : undefined

  const say = (reason: UpgradeReason) =>
    t(`k3s.reason.${reason.code}`, { ...reason.params, defaultValue: reason.text })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ArrowUpCircleIcon className="size-4" />
          {t("k3s.title")}
        </CardTitle>
        <CardDescription>{t("k3s.help")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("k3s.server")}</TableHead>
              <TableHead>{t("k3s.role")}</TableHead>
              <TableHead>{t("k3s.version")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.nodes.map((node) => (
              <TableRow key={node.name}>
                <TableCell className="font-medium">{node.name}</TableCell>
                <TableCell>
                  {node.control_plane ? t("k3s.roleControlPlane") : t("k3s.roleWorker")}
                </TableCell>
                <TableCell className="font-mono text-xs">{node.version}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>

        <div className="flex flex-wrap items-end gap-2">
          <Field className="w-64">
            <FieldLabel htmlFor="k3s-target">{t("k3s.target")}</FieldLabel>
            {releases.length > 0 ? (
              <Select value={target} onValueChange={setTyped}>
                <SelectTrigger id="k3s-target">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {releases.map((release) => (
                    <SelectItem key={release.version} value={release.version}>
                      {release.stable
                        ? t("k3s.recommended", { version: release.version })
                        : release.version}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                id="k3s-target"
                value={typed ?? ""}
                onChange={(event) => setTyped(event.target.value)}
                placeholder="v1.36.4+k3s1"
                className="font-mono"
              />
            )}
          </Field>
          <Button
            variant="outline"
            disabled={!target.trim() || plan.isFetching}
            onClick={() => {
              start.reset()
              setChecked(target.trim())
            }}
          >
            {plan.isFetching && <Spinner />}
            {t("k3s.check")}
          </Button>
        </div>
        {data.releases_error && releases.length === 0 && (
          <FieldDescription>{t("k3s.releasesUnavailable")}</FieldDescription>
        )}
        {plan.error != null && <ErrorDisplay error={plan.error} compact />}

        {shown && (
          <div className="space-y-3">
            {shown.nothing ? (
              <p className="flex items-center gap-1.5 text-sm text-success">
                <CheckCircle2Icon className="size-4" />
                {t("k3s.upToDate")}
              </p>
            ) : (
              <ol className="space-y-1 text-sm">
                {shown.steps.map((step, index) => (
                  <li key={step.node} className="flex flex-wrap items-center gap-2">
                    <span className="text-muted-foreground">{index + 1}.</span>
                    <span className="font-medium">{step.node}</span>
                    <Badge variant="outline" className="text-[10px]">
                      {step.role === "control-plane"
                        ? t("k3s.roleControlPlane")
                        : t("k3s.roleWorker")}
                    </Badge>
                    <span className="font-mono text-xs text-muted-foreground">
                      {step.upgrade ? `${step.from} → ${step.to}` : t("k3s.alreadyThere")}
                    </span>
                  </li>
                ))}
              </ol>
            )}
            {shown.blockers.length > 0 && (
              <Alert variant="destructive">
                <AlertTitle>{t("k3s.blockers")}</AlertTitle>
                <AlertDescription>
                  <ul className="list-disc space-y-1 pl-4">
                    {shown.blockers.map((reason) => (
                      <li key={reason.code + reason.text}>{say(reason)}</li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            )}
            {shown.warnings.length > 0 && (
              <Alert>
                <AlertTitle>{t("k3s.warnings")}</AlertTitle>
                <AlertDescription>
                  <ul className="list-disc space-y-1 pl-4">
                    {shown.warnings.map((reason) => (
                      <li key={reason.code}>{say(reason)}</li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            )}
            {!shown.nothing && shown.blockers.length === 0 && (
              <Button
                disabled={start.isPending || start.isSuccess}
                onClick={() =>
                  void confirm({
                    title: t("k3s.confirmTitle", { version: shown.target }),
                    description: t("k3s.confirm"),
                    confirmLabel: t("k3s.start"),
                    destructive: true,
                  }).then((yes) => {
                    if (yes) start.mutate(shown.target)
                  })
                }
              >
                {start.isPending && <Spinner />}
                {t("k3s.start")}
              </Button>
            )}
            {start.isSuccess && (
              <Alert variant="info">
                <AlertDescription>{t("k3s.started")}</AlertDescription>
              </Alert>
            )}
            {start.error != null && <ErrorDisplay error={start.error} />}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
