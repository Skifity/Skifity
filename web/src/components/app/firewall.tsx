import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { PlusIcon, Trash2Icon, ShieldIcon, TriangleAlertIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
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
import {
  FIREWALL_FIELDS,
  groupOf,
  needsGeo,
  newGroup,
  newTest,
  operatorsFor,
  withGroupKind,
  withoutCondition,
  type Firewall,
  type FirewallAction,
  type FirewallExpr,
  type FirewallField,
  type FirewallOperator,
  type FirewallRule,
} from "@/lib/firewall"

/**
 * Who may reach this app.
 *
 * The form is the reason the rules are a tree and not a string. Cloudflare
 * writes theirs as a small language people have to learn; here "match all of
 * these" and "match any of these" are groups on a page, and nesting one inside
 * the other is what and-or-and means. Nobody types a syntax and nothing has to
 * parse one.
 *
 * Everything below is derived from `draft` during render. The saved rules are a
 * query and the draft is the edit in progress; copying one into the other with
 * an effect would render the stale version first, every time.
 */
export function AppFirewall({ appId }: { appId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const firewall = useQuery({
    queryKey: ["firewall", appId],
    queryFn: () => api.get<Firewall>(`/api/apps/${appId}/firewall`),
  })

  const [draft, setDraft] = useState<Firewall | null>(null)
  const current = draft ?? firewall.data ?? null

  const save = useMutation({
    mutationFn: (next: Firewall) =>
      api.put<Firewall>(`/api/apps/${appId}/firewall`, {
        enabled: next.enabled,
        rules: next.rules,
      }),
    onSuccess: (saved) => {
      setDraft(null)
      queryClient.setQueryData(["firewall", appId], saved)
    },
  })

  if (firewall.isLoading) return <Skeleton className="h-64" />
  if (firewall.error) {
    return <ErrorDisplay error={firewall.error} onRetry={() => void firewall.refetch()} />
  }
  if (!current) return null

  const rules = current.rules.rules ?? []
  const dirty = draft !== null
  const usesGeo = rules.some((rule) => {
    const need = needsGeo(rule.expr)
    return need.country || need.asn
  })

  function update(next: Partial<Firewall>) {
    setDraft({ ...current!, ...next })
  }

  function updateRules(nextRules: FirewallRule[]) {
    update({ rules: { ...current!.rules, rules: nextRules } })
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center gap-4 py-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <ShieldIcon className="size-4 shrink-0 text-muted-foreground" />
              <span className="text-sm font-medium">{t("firewall.title")}</span>
            </div>
            <p className="mt-0.5 text-sm text-muted-foreground">{t("firewall.help")}</p>
          </div>
          <Switch
            checked={current.enabled}
            onCheckedChange={(enabled) => update({ enabled })}
            aria-label={t("firewall.title")}
          />
        </CardContent>
      </Card>

      {/* Three ways for a firewall to be on and guarding nothing, each said out
          loud rather than left for somebody to discover. */}
      {current.enabled && !current.installed && (
        <Alert variant="warning">
          <TriangleAlertIcon />
          <AlertTitle>{t("firewall.notInstalled")}</AlertTitle>
          <AlertDescription>{t("firewall.notInstalledHelp")}</AlertDescription>
        </Alert>
      )}
      {current.enabled && current.hostnames.length === 0 && (
        <Alert variant="warning">
          <TriangleAlertIcon />
          <AlertTitle>{t("firewall.noDomain")}</AlertTitle>
          <AlertDescription>{t("firewall.noDomainHelp")}</AlertDescription>
        </Alert>
      )}
      {current.enabled && usesGeo && !current.country_available && (
        <Alert variant="warning">
          <TriangleAlertIcon />
          <AlertTitle>{t("firewall.noGeo")}</AlertTitle>
          <AlertDescription>{t("firewall.noGeoHelp")}</AlertDescription>
        </Alert>
      )}

      {current.enabled && (
        <>
          {rules.map((rule, index) => (
            <RuleCard
              key={rule.id}
              rule={rule}
              onChange={(next) => updateRules(rules.map((r, i) => (i === index ? next : r)))}
              onRemove={() => updateRules(rules.filter((_, i) => i !== index))}
            />
          ))}

          <div className="flex flex-wrap items-center gap-3">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                updateRules([
                  ...rules,
                  {
                    id: `rule_${rules.length + 1}_${Date.now().toString(36)}`,
                    name: "",
                    action: "block",
                    enabled: true,
                    expr: newGroup(),
                  },
                ])
              }
            >
              <PlusIcon className="size-4" />
              {t("firewall.addRule")}
            </Button>

            <div className="flex items-center gap-2">
              <Label htmlFor="firewall-default" className="text-sm text-muted-foreground">
                {t("firewall.otherwise")}
              </Label>
              <Select
                value={current.rules.default ?? "allow"}
                onValueChange={(value) =>
                  update({ rules: { ...current.rules, default: value as FirewallAction } })
                }
              >
                <SelectTrigger id="firewall-default" className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="allow">{t("firewall.action.allow")}</SelectItem>
                  <SelectItem value="block">{t("firewall.action.block")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </>
      )}

      {current.attribution && usesGeo && (
        <p className="text-xs text-muted-foreground">
          <a
            href={current.attribution_url}
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-2"
          >
            {current.attribution}
          </a>
        </p>
      )}

      {save.error != null && <ErrorDisplay error={save.error} />}

      <div className="flex items-center gap-3">
        <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(current)}>
          {save.isPending && <Spinner className="size-4" />}
          {t("common.save")}
        </Button>
        {dirty && (
          <Button variant="ghost" onClick={() => setDraft(null)}>
            {t("common.cancel")}
          </Button>
        )}
      </div>
    </div>
  )
}

function RuleCard({
  rule,
  onChange,
  onRemove,
}: {
  rule: FirewallRule
  onChange: (rule: FirewallRule) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  return (
    <Card>
      <CardHeader className="flex flex-wrap items-center gap-3 pb-3">
        <Input
          value={rule.name}
          placeholder={t("firewall.rulePlaceholder")}
          onChange={(e) => onChange({ ...rule, name: e.target.value })}
          className="max-w-xs"
          aria-label={t("firewall.ruleName")}
        />
        <Select
          value={rule.action}
          onValueChange={(value) => onChange({ ...rule, action: value as FirewallAction })}
        >
          <SelectTrigger className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="allow">{t("firewall.action.allow")}</SelectItem>
            <SelectItem value="block">{t("firewall.action.block")}</SelectItem>
          </SelectContent>
        </Select>
        <CardTitle className="sr-only">{rule.name}</CardTitle>
        <div className="ml-auto flex items-center gap-2">
          {!rule.enabled && (
            <Badge variant="outline" className="text-[10px]">
              {t("firewall.off")}
            </Badge>
          )}
          <Switch
            checked={rule.enabled}
            onCheckedChange={(enabled) => onChange({ ...rule, enabled })}
            aria-label={t("firewall.ruleEnabled")}
          />
          <Button variant="ghost" size="icon" onClick={onRemove} aria-label={t("common.delete")}>
            <Trash2Icon className="size-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <ExprEditor expr={rule.expr} onChange={(expr) => onChange({ ...rule, expr })} depth={0} />
      </CardContent>
    </Card>
  )
}

/**
 * One condition: a group, or a single test.
 *
 * Recursive, because the shape is. The depth is carried only to stop the
 * nesting going deeper than anybody can read — the engine allows eight and a
 * form that offered eight would be a form nobody could follow.
 */
function ExprEditor({
  expr,
  onChange,
  depth,
}: {
  expr: FirewallExpr
  onChange: (expr: FirewallExpr) => void
  depth: number
}) {
  const { t } = useTranslation()
  const group = groupOf(expr)

  if (!group) {
    return (
      <TestEditor test={expr.test ?? newTest().test!} onChange={(test) => onChange({ test })} />
    )
  }

  return (
    <div className="space-y-2 rounded-md border border-dashed p-3">
      <div className="flex items-center gap-2">
        <Select
          value={group.kind}
          onValueChange={(kind) => onChange(withGroupKind(expr, kind as "all" | "any"))}
        >
          <SelectTrigger className="w-44" aria-label={t("firewall.match")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("firewall.matchAll")}</SelectItem>
            <SelectItem value="any">{t("firewall.matchAny")}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {group.children.map((child, index) => (
        <div key={index} className="flex items-start gap-2">
          <div className="min-w-0 flex-1">
            <ExprEditor
              expr={child}
              depth={depth + 1}
              onChange={(next) =>
                onChange(
                  withGroupKind(
                    { all: group.children.map((c, i) => (i === index ? next : c)) },
                    group.kind,
                  ),
                )
              }
            />
          </div>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("common.delete")}
            onClick={() => onChange(withoutCondition(expr, index))}
          >
            <Trash2Icon className="size-4" />
          </Button>
        </div>
      ))}

      <div className="flex flex-wrap gap-2">
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            onChange(withGroupKind({ all: [...group.children, newTest()] }, group.kind))
          }
        >
          <PlusIcon className="size-4" />
          {t("firewall.addCondition")}
        </Button>
        {depth < 2 && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              onChange(withGroupKind({ all: [...group.children, newGroup()] }, group.kind))
            }
          >
            <PlusIcon className="size-4" />
            {t("firewall.addGroup")}
          </Button>
        )}
      </div>
    </div>
  )
}

function TestEditor({
  test,
  onChange,
}: {
  test: NonNullable<FirewallExpr["test"]>
  onChange: (test: NonNullable<FirewallExpr["test"]>) => void
}) {
  const { t } = useTranslation()
  const operators = operatorsFor(test.field)
  // Derived, not repaired by an effect: changing the field to an address while
  // "contains" is selected would leave a comparison that can never match, and
  // the form should never be able to show one.
  const op = operators.includes(test.op) ? test.op : operators[0]

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Select
        value={test.field}
        onValueChange={(value) => {
          const field = value as FirewallField
          const allowed = operatorsFor(field)
          onChange({
            ...test,
            field,
            op: allowed.includes(test.op) ? test.op : allowed[0],
            key: field === "http.header" ? (test.key ?? "") : undefined,
          })
        }}
      >
        <SelectTrigger className="w-44" aria-label={t("firewall.field")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {FIREWALL_FIELDS.map((field) => (
            <SelectItem key={field} value={field}>
              {t(`firewall.fields.${field}`)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {test.field === "http.header" && (
        <Input
          value={test.key ?? ""}
          placeholder={t("firewall.headerPlaceholder")}
          onChange={(e) => onChange({ ...test, key: e.target.value })}
          className="w-40"
          aria-label={t("firewall.headerName")}
        />
      )}

      <Select
        value={op}
        onValueChange={(value) => onChange({ ...test, op: value as FirewallOperator })}
      >
        <SelectTrigger className="w-36" aria-label={t("firewall.operator")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {operators.map((operator) => (
            <SelectItem key={operator} value={operator}>
              {t(`firewall.operators.${operator}`)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <Input
        value={test.values.join(", ")}
        placeholder={t(`firewall.placeholders.${test.field}`)}
        onChange={(e) =>
          onChange({
            ...test,
            // Split on save rather than on every keystroke, so that typing a
            // comma does not tear the value being typed into two.
            values: e.target.value
              .split(",")
              .map((value) => value.trim())
              .filter((value) => value !== ""),
          })
        }
        className="min-w-48 flex-1"
        aria-label={t("firewall.values")}
      />
    </div>
  )
}
