import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  EyeOffIcon,
  GitPullRequestIcon,
  HammerIcon,
  KeyRoundIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
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
import { Textarea } from "@/components/ui/textarea"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { parseDotEnv } from "@/lib/dotenv"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"

type EditableVariable = {
  id: string
  key: string
  value?: string
  is_secret: boolean
  build_time?: boolean
  /** What a pull request's preview gets: the same value, its own, or none. */
  preview_mode?: "" | "value" | "none"
  preview_value?: string
}

/**
 * The variables table, shared by an app and by a project's shared variables.
 *
 * Both are the same idea at different scopes, and a user who has learned one
 * should not have to learn the other. The only difference is that a build-time
 * variable is an app concept: it changes the image, so it triggers a rebuild.
 */
export function VariablesEditor({
  base,
  queryKey,
  showBuildTime,
  showPreviews,
  description,
}: {
  /** The API prefix, such as /api/apps/{id} or /api/projects/{id}. */
  base: string
  queryKey: unknown[]
  showBuildTime?: boolean
  /** Offer what previews get, which is an app's to say. */
  showPreviews?: boolean
  description?: string
}) {
  const { t } = useTranslation()
  const [adding, setAdding] = useState(false)
  const [previewing, setPreviewing] = useState<EditableVariable | null>(null)
  const [bulk, setBulk] = useState<string | null>(null)

  const variables = useQuery({
    queryKey,
    queryFn: () => api.get<List<EditableVariable>>(`${base}/variables`),
  })

  const invalidate = () => void queryClient.invalidateQueries({ queryKey })

  const save = useMutation({
    mutationFn: (variable: {
      key: string
      value: string
      is_secret: boolean
      build_time: boolean
    }) => api.put<{ requires_rebuild: boolean }>(`${base}/variables`, variable),
    // The answer to "does changing this rebuild my app?" is the single most
    // asked question about this screen, so the panel answers it every time
    // rather than leaving the user to watch the deployments tab.
    onSuccess: (result) => {
      invalidate()
      toast.success(
        result?.requires_rebuild ? t("variables.rebuildQueued") : t("deploy.noRebuildNeeded"),
      )
    },
  })

  const remove = useMutation({
    mutationFn: (key: string) => api.delete(`${base}/variables/${encodeURIComponent(key)}`),
    onSuccess: invalidate,
  })

  // A secret's value is sealed and never shown again, so deleting one is not
  // something a stray click should be able to do: there is nowhere to read it
  // back from. A plain variable asks too, because the row it sits in is one
  // pixel from its neighbour's.
  const confirm = useConfirm()
  const askThenRemove = (variable: EditableVariable) => {
    void confirm({
      title: t("common.deleteNamed", { name: variable.key }),
      description: t("variables.deleteConfirm"),
      consequence: variable.is_secret ? t("variables.deleteSecretConsequence") : undefined,
      confirmLabel: t("common.delete"),
      destructive: true,
    }).then((yes) => {
      if (yes) remove.mutate(variable.key)
    })
  }

  const saveBulk = useMutation({
    mutationFn: async (text: string) => {
      // is_secret is left out on purpose. It used to say false for every
      // line, and a pasted .env is exactly where somebody's API keys are, so
      // they were stored as ordinary values that came back from the API and
      // sat on this page. Left out, the panel keeps a secret a secret and
      // decides a new one by the rule its log redaction uses.
      //
      // One request for the lot: all of them or none, and one rollout. One
      // request per line rolled the app out once per line, each time with
      // half a configuration.
      const set = Object.entries(parseDotEnv(text)).map(([key, value]) => ({
        key,
        value,
        build_time: false,
      }))
      if (set.length > 0) await api.post(`${base}/variables/batch`, { set })
    },
    onSuccess: () => {
      invalidate()
      setBulk(null)
    },
  })

  const items = variables.data?.items ?? []

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="max-w-xl text-sm text-muted-foreground">
          {description ?? t("variables.help")}
        </p>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => setBulk(bulk === null ? "" : null)}>
            {t("variables.bulkEdit")}
          </Button>
          <Button size="sm" onClick={() => setAdding(true)}>
            <PlusIcon className="size-4" />
            {t("variables.addVariable")}
          </Button>
        </div>
      </div>

      {bulk !== null && (
        <Card>
          <CardContent className="space-y-3 pt-6">
            <Label htmlFor="bulk-variables">{t("variables.bulkEditHelp")}</Label>
            <Textarea
              id="bulk-variables"
              value={bulk}
              onChange={(event) => setBulk(event.target.value)}
              rows={8}
              className="font-mono text-xs"
              placeholder={"DATABASE_URL=postgres://...\nLOG_LEVEL=info"}
            />
            {saveBulk.error && <ErrorDisplay error={saveBulk.error} compact />}
            <div className="flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => setBulk(null)}>
                {t("common.cancel")}
              </Button>
              <Button size="sm" disabled={saveBulk.isPending} onClick={() => saveBulk.mutate(bulk)}>
                {saveBulk.isPending && <Spinner />}
                {saveBulk.isPending ? t("common.saving") : t("common.save")}
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {adding && (
        <NewVariableRow
          showBuildTime={showBuildTime}
          pending={save.isPending}
          error={save.error}
          onCancel={() => setAdding(false)}
          onSave={(variable) =>
            save.mutate(variable, {
              onSuccess: () => setAdding(false),
            })
          }
        />
      )}

      {variables.isLoading ? (
        <Skeleton className="h-40" />
      ) : variables.error ? (
        <ErrorDisplay error={variables.error} onRetry={() => void variables.refetch()} />
      ) : items.length === 0 && !adding ? (
        <EmptyState
          icon={KeyRoundIcon}
          title={t("variables.empty")}
          description={t("variables.help")}
          action={
            <Button onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("variables.addVariable")}
            </Button>
          }
        />
      ) : items.length > 0 ? (
        <Card>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("variables.key")}</TableHead>
                  <TableHead>{t("variables.value")}</TableHead>
                  <TableHead className="w-20" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((variable) => (
                  <TableRow key={variable.id}>
                    <TableCell className="font-mono text-xs font-medium">
                      <div className="flex flex-wrap items-center gap-1.5">
                        {variable.key}
                        {variable.is_secret && (
                          <Badge variant="outline" className="gap-1 text-[10px]">
                            <EyeOffIcon className="size-2.5" />
                            {t("variables.secret")}
                          </Badge>
                        )}
                        {variable.build_time && (
                          <Badge variant="outline" className="gap-1 text-[10px]">
                            <HammerIcon className="size-2.5" />
                            {t("variables.buildTime")}
                          </Badge>
                        )}
                        {variable.preview_mode && (
                          <Badge variant="outline" className="gap-1 text-[10px]">
                            <GitPullRequestIcon className="size-2.5" />
                            {t(`variables.previewBadge.${variable.preview_mode}`)}
                          </Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="max-w-xs truncate font-mono text-xs text-muted-foreground">
                      {variable.is_secret ? t("variables.hidden") : variable.value}
                    </TableCell>
                    <TableCell className="text-right whitespace-nowrap">
                      {showPreviews && (
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t("variables.previewTitle", { key: variable.key })}
                          onClick={() => setPreviewing(variable)}
                        >
                          <GitPullRequestIcon className="size-4 text-muted-foreground" />
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("common.delete")}
                        disabled={remove.isPending}
                        onClick={() => askThenRemove(variable)}
                      >
                        <Trash2Icon className="size-4 text-muted-foreground" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ) : null}

      {remove.error && <ErrorDisplay error={remove.error} compact />}

      {previewing != null && (
        <PreviewValueDialog
          // Keyed by the variable, so opening it for another starts from that
          // one's setting rather than the last one's.
          key={previewing.id}
          base={base}
          variable={previewing}
          onClose={() => setPreviewing(null)}
          onSaved={invalidate}
        />
      )}
    </div>
  )
}

/**
 * What a pull request's preview gets for one variable.
 *
 * The live payment key in every preview is how a branch charges real cards;
 * a mail address is how it writes to real customers. A preview can get the
 * same value, one of its own — the test key — or nothing. It is read when a
 * preview is made, so the ones already running keep what they were given.
 */
function PreviewValueDialog({
  base,
  variable,
  onClose,
  onSaved,
}: {
  base: string
  variable: EditableVariable
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<string>(variable.preview_mode || "same")
  const [value, setValue] = useState(variable.preview_value ?? "")

  const save = useMutation({
    mutationFn: () =>
      api.put(`${base}/variables/${encodeURIComponent(variable.key)}/preview`, {
        mode,
        value: mode === "value" ? value : undefined,
      }),
    onSuccess: () => {
      onSaved()
      onClose()
    },
  })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("variables.previewTitle", { key: variable.key })}</DialogTitle>
          <DialogDescription>{t("variables.previewHelp")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="preview-mode">{t("variables.previewMode")}</Label>
            <Select value={mode} onValueChange={setMode}>
              <SelectTrigger id="preview-mode">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="same">{t("variables.previewSame")}</SelectItem>
                <SelectItem value="value">{t("variables.previewOwn")}</SelectItem>
                <SelectItem value="none">{t("variables.previewNone")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {mode === "value" && (
            <div className="space-y-2">
              <Label htmlFor="preview-value">{t("variables.previewValue")}</Label>
              <Input
                id="preview-value"
                value={value}
                type={variable.is_secret ? "password" : "text"}
                autoComplete="off"
                spellCheck={false}
                className="font-mono text-xs"
                placeholder={
                  variable.is_secret ? t("variables.previewSecretPlaceholder") : undefined
                }
                onChange={(event) => setValue(event.target.value)}
              />
            </div>
          )}
          {save.error != null && <ErrorDisplay error={save.error} compact />}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            // A secret's preview value is never shown back, so an empty box is
            // "not typed yet" rather than "make it empty".
            disabled={save.isPending || (mode === "value" && variable.is_secret && value === "")}
            onClick={() => save.mutate()}
          >
            {save.isPending && <Spinner />}
            {save.isPending ? t("common.saving") : t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function NewVariableRow({
  showBuildTime,
  pending,
  error,
  onSave,
  onCancel,
}: {
  showBuildTime?: boolean
  pending: boolean
  error: unknown
  onSave: (variable: {
    key: string
    value: string
    is_secret: boolean
    build_time: boolean
  }) => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const [key, setKey] = useState("")
  const [value, setValue] = useState("")
  const [isSecret, setIsSecret] = useState(false)
  const [buildTime, setBuildTime] = useState(false)

  // The panel refuses a name Kubernetes cannot carry, and says so before the
  // request rather than after it.
  const invalid = key !== "" && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)

  return (
    <Card>
      <CardContent className="pt-6">
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            if (invalid) return
            onSave({ key: key.trim(), value, is_secret: isSecret, build_time: buildTime })
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="variable-key">{t("variables.key")}</FieldLabel>
              <Input
                id="variable-key"
                value={key}
                onChange={(event) => setKey(event.target.value)}
                className="font-mono"
                autoFocus
                required
                aria-invalid={invalid}
              />
              {invalid && <FieldError>{t("variables.invalidKey")}</FieldError>}
            </Field>
            <Field>
              <FieldLabel htmlFor="variable-value">{t("variables.value")}</FieldLabel>
              <Input
                id="variable-value"
                value={value}
                onChange={(event) => setValue(event.target.value)}
                className="font-mono"
                type={isSecret ? "password" : "text"}
              />
            </Field>
          </div>

          <div className="space-y-2">
            <label className="flex items-start gap-2.5 text-sm">
              <Checkbox
                checked={isSecret}
                onCheckedChange={(checked) => setIsSecret(checked === true)}
              />
              <span>
                {t("variables.secret")}
                <span className="block text-xs text-muted-foreground">
                  {t("variables.secretHelp")}
                </span>
              </span>
            </label>
            {showBuildTime && (
              <label className="flex items-start gap-2.5 text-sm">
                <Checkbox
                  checked={buildTime}
                  onCheckedChange={(checked) => setBuildTime(checked === true)}
                />
                <span>
                  {t("variables.buildTime")}
                  <span className="block text-xs text-muted-foreground">
                    {t("variables.buildTimeHelp")}
                  </span>
                </span>
              </label>
            )}
          </div>

          {error != null && <ErrorDisplay error={error} compact />}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onCancel}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={pending || !key.trim() || invalid}>
              {pending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
