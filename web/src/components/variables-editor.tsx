import { useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  EyeOffIcon,
  GitPullRequestIcon,
  HammerIcon,
  KeyRoundIcon,
  LinkIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  Trash2Icon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { CopyButton } from "@/components/copy-button"
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
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
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
import { useSession } from "@/hooks/use-session"
import { dotEnvLine, parseDotEnv } from "@/lib/dotenv"
import { api, type List } from "@/lib/api"
import { queryClient } from "@/lib/query"
import type {
  SecretManager,
  SecretReference,
  VariableEffect,
  VariablesRefresh,
} from "@/lib/types"

type EditableVariable = {
  id: string
  key: string
  value?: string
  is_secret: boolean
  build_time?: boolean
  /** What a pull request's preview gets: the same value, its own, or none. */
  preview_mode?: "" | "value" | "none"
  preview_value?: string
  /** Where the value is read from, when it is not stored in the panel. */
  reference?: SecretReference
}

/** What a new variable is: a value, or a reference to a secret manager. */
type NewVariable = {
  key: string
  value?: string
  is_secret?: boolean
  build_time?: boolean
  from?: { connection: string; path: string; key?: string }
}

/**
 * What saving did, in a sentence: rolled out, rebuilt — with the deployment
 * doing it — or, for a value the build reads, why it waits for a deploy.
 */
function effectMessage(t: TFunction, result?: VariableEffect): string {
  if (result?.requires_rebuild) {
    return result.deployment
      ? t("variables.rebuildStarted", { number: result.deployment.number })
      : t("variables.rebuildQueued")
  }
  switch (result?.not_rebuilt) {
    case "locked":
      return t("variables.rebuildLocked")
    case "not_deployed":
      return t("variables.rebuildFirstDeploy")
    case "failed":
      return t("variables.rebuildNextDeploy")
  }
  return t("deploy.noRebuildNeeded")
}

/** A reference as the CLI writes it: connection:path#key. */
function referenceText(reference: SecretReference): string {
  const name = reference.connection ?? reference.connection_id
  return `${name}:${reference.path}${reference.key ? `#${reference.key}` : ""}`
}

/**
 * The variables as "Edit as text" opens with them: the plain ones as KEY=value
 * lines, in order of name, and a comment for each one whose value is not
 * here to show — a secret, a reference, a value over several lines — saying
 * it is left as it is.
 *
 * The box used to open empty: it was a paste box, and changing one value in
 * it meant typing every other one again or losing nothing only by luck.
 */
function variablesAsText(t: TFunction, items: EditableVariable[]): string {
  const lines: string[] = []
  const kept: string[] = []
  for (const variable of [...items].sort((a, b) => (a.key < b.key ? -1 : 1))) {
    const key = variable.key
    if (variable.reference) {
      kept.push(t("variables.textReference", { key, reference: referenceText(variable.reference) }))
    } else if (variable.is_secret) {
      kept.push(t("variables.textSecret", { key }))
    } else {
      const line = dotEnvLine(key, variable.value ?? "")
      if (line === null) kept.push(t("variables.textMultiline", { key }))
      else lines.push(line)
    }
  }
  const comments = kept.map((sentence) => `# ${sentence}`)
  return [...lines, ...(lines.length > 0 && comments.length > 0 ? [""] : []), ...comments]
    .map((line) => `${line}\n`)
    .join("")
}

/**
 * What a saved box changes: the lines that add a variable or give one a
 * different value. Only those are sent, because every key the batch sets is
 * rolled out — a box saved as it opened restarted the app for nothing. A line
 * for a secret or a reference always counts: its value was never here to
 * compare with, so a line for it is a new one.
 */
function changedLines(
  text: Record<string, string>,
  items: EditableVariable[],
): { key: string; value: string }[] {
  const loaded = new Map(items.map((variable) => [variable.key, variable]))
  return Object.entries(text)
    .filter(([key, value]) => {
      const was = loaded.get(key)
      return !was || was.is_secret || was.reference != null || (was.value ?? "") !== value
    })
    .map(([key, value]) => ({ key, value }))
}

/** Whether a box holds anything but blank lines and comments. */
const hasLines = (text: string) =>
  text.split(/\r?\n/).some((line) => line.trim() !== "" && !line.trim().startsWith("#"))

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
  refreshable,
  description,
  projectId,
}: {
  /** The API prefix, such as /api/apps/{id} or /api/projects/{id}. */
  base: string
  queryKey: unknown[]
  showBuildTime?: boolean
  /** Offer what previews get, which is an app's to say. */
  showPreviews?: boolean
  /** Offer to read the ones from secret managers again: an app's. */
  refreshable?: boolean
  description?: string
  /**
   * The project the variables are read in, so only the secret managers it
   * may use are offered. The panel refuses the others either way.
   */
  projectId?: string
}) {
  const { t } = useTranslation()
  const [adding, setAdding] = useState(false)
  // The variable whose row is open as a form, in place of the row.
  const [editing, setEditing] = useState<EditableVariable | null>(null)
  // Plain values shown whole rather than cut off at the column's edge.
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const [previewing, setPreviewing] = useState<EditableVariable | null>(null)
  const [bulk, setBulk] = useState<string | null>(null)

  const variables = useQuery({
    queryKey,
    queryFn: () => api.get<List<EditableVariable>>(`${base}/variables`),
  })

  const invalidate = () => void queryClient.invalidateQueries({ queryKey })

  const save = useMutation({
    mutationFn: (variable: NewVariable) => api.put<VariableEffect>(`${base}/variables`, variable),
    // The answer to "does changing this rebuild my app?" is the single most
    // asked question about this screen, so the panel answers it every time
    // rather than leaving the user to watch the deployments tab.
    onSuccess: (result) => {
      invalidate()
      toast.success(effectMessage(t, result))
    },
  })

  const remove = useMutation({
    mutationFn: (key: string) =>
      api.delete<VariableEffect>(`${base}/variables/${encodeURIComponent(key)}`),
    onSuccess: (result) => {
      invalidate()
      // Only a removal the build read is worth a word: the image is built
      // again without it, or waits for a deploy to be.
      if (result?.requires_rebuild || result?.not_rebuilt) toast.success(effectMessage(t, result))
    },
  })

  // Read the ones from secret managers again. The answer says which changed
  // and what was done about it, by name: a rollout, a rebuild, or nothing.
  const refresh = useMutation({
    mutationFn: () => api.post<VariablesRefresh>(`${base}/variables/refresh`),
    onSuccess: (result) => {
      invalidate()
      const names = result.changed.join(", ")
      if (result.changed.length === 0) toast.success(t("variables.refreshNothing"))
      else if (result.not_deployed) toast.success(t("variables.refreshNotDeployed", { names }))
      else if (result.deployment)
        toast.success(
          t("variables.refreshRebuilding", { names: result.build_time_changed.join(", ") }),
        )
      else toast.success(t("variables.refreshRolledOut", { names }))
    },
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
      // A reference loses nothing: the value stays where it is kept.
      consequence:
        variable.is_secret && !variable.reference
          ? t("variables.deleteSecretConsequence")
          : undefined,
      confirmLabel: t("common.delete"),
      destructive: true,
    }).then((yes) => {
      if (yes) remove.mutate(variable.key)
    })
  }

  const saveBulk = useMutation({
    mutationFn: async (set: { key: string; value: string }[]) => {
      // is_secret is left out on purpose. It used to say false for every
      // line, and a pasted .env is exactly where somebody's API keys are, so
      // they were stored as ordinary values that came back from the API and
      // sat on this page. Left out, the panel keeps a secret a secret and
      // decides a new one by the rule its log redaction uses.
      //
      // One request for the lot: all of them or none, and one rollout. One
      // request per line rolled the app out once per line, each time with
      // half a configuration.
      // build_time is left out for the same reason: false took every variable
      // the build reads out of the build, and the next build went without it.
      return api.post<VariableEffect>(`${base}/variables/batch`, { set })
    },
    // Said, as a single variable's save is: it closed with nothing on the
    // screen, the same whether thirty lines were saved or none.
    onSuccess: (result, set) => {
      invalidate()
      setBulk(null)
      toast.success(t("variables.bulkSaved", { count: set.length }), {
        description: effectMessage(t, result),
      })
    },
  })

  const items = variables.data?.items ?? []
  const hasReferences = items.some((variable) => variable.reference)

  // What the box holds, read on every render: Save is offered only for lines
  // that are KEY=value, so a paste of something else is said rather than
  // closed with nothing saved.
  const bulkRead = bulk === null ? {} : parseDotEnv(bulk)
  const bulkUnreadable = bulk !== null && hasLines(bulk) && Object.keys(bulkRead).length === 0
  const bulkChanges = changedLines(bulkRead, items)
  const saveText = () => {
    if (bulkChanges.length === 0) {
      setBulk(null)
      toast.info(t("variables.nothingChanged"))
      return
    }
    saveBulk.mutate(bulkChanges)
  }

  // One form open at a time: two would race to save the same variable.
  const openForm = (variable: EditableVariable | null) => {
    setEditing(variable)
    setAdding(variable === null)
  }
  const toggleExpanded = (id: string) =>
    setExpanded((open) => {
      const next = new Set(open)
      if (!next.delete(id)) next.add(id)
      return next
    })

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="max-w-xl text-sm text-muted-foreground">
          {description ?? t("variables.help")}
        </p>
        <div className="flex flex-wrap gap-2">
          {refreshable && hasReferences && (
            <Button
              variant="outline"
              size="sm"
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
            >
              {refresh.isPending ? <Spinner /> : <RefreshCwIcon className="size-4" />}
              {t("variables.refresh")}
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            onClick={() => setBulk(bulk === null ? variablesAsText(t, items) : null)}
          >
            {t("variables.bulkEdit")}
          </Button>
          <Button size="sm" onClick={() => openForm(null)}>
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
            <p className="text-xs text-muted-foreground">{t("variables.bulkEditKeeps")}</p>
            {bulkUnreadable && (
              <p className="text-xs text-destructive">{t("variables.bulkNothing")}</p>
            )}
            {saveBulk.error && <ErrorDisplay error={saveBulk.error} compact />}
            <div className="flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => setBulk(null)}>
                {t("common.cancel")}
              </Button>
              <Button size="sm" disabled={saveBulk.isPending || bulkUnreadable} onClick={saveText}>
                {saveBulk.isPending && <Spinner />}
                {saveBulk.isPending ? t("common.saving") : t("common.save")}
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {refresh.error != null && <ErrorDisplay error={refresh.error} compact />}

      {adding && (
        <VariableForm
          showBuildTime={showBuildTime}
          projectId={projectId}
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
            <Button onClick={() => openForm(null)}>
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
                  <TableHead className="w-28" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((variable) =>
                  editing?.id === variable.id ? (
                    <TableRow key={variable.id}>
                      <TableCell colSpan={3} className="p-3 whitespace-normal">
                        <VariableForm
                          // Keyed by the variable, so it starts from this one.
                          key={variable.id}
                          existing={variable}
                          showBuildTime={showBuildTime}
                          projectId={projectId}
                          pending={save.isPending}
                          error={save.error}
                          onCancel={() => setEditing(null)}
                          onUnchanged={() => {
                            setEditing(null)
                            toast.info(t("variables.nothingChanged"))
                          }}
                          onSave={(changed) =>
                            save.mutate(changed, {
                              onSuccess: () => setEditing(null),
                            })
                          }
                        />
                      </TableCell>
                    </TableRow>
                  ) : (
                    <TableRow key={variable.id}>
                      <TableCell className="font-mono text-xs font-medium">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {variable.key}
                          {variable.reference ? (
                            <Badge variant="outline" className="gap-1 text-[10px]">
                              <LinkIcon className="size-2.5" />
                              {t("variables.fromManager")}
                            </Badge>
                          ) : (
                            variable.is_secret && (
                              <Badge variant="outline" className="gap-1 text-[10px]">
                                <EyeOffIcon className="size-2.5" />
                                {t("variables.secret")}
                              </Badge>
                            )
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
                      {variable.reference || variable.is_secret || !variable.value ? (
                        <TableCell className="max-w-xs truncate font-mono text-xs text-muted-foreground">
                          {variable.reference
                            ? t("variables.from", {
                                manager: variable.reference.kind
                                  ? t(`secretManagers.kind.${variable.reference.kind}`)
                                  : t("variables.unknownManager"),
                                reference: referenceText(variable.reference),
                              })
                            : variable.is_secret
                              ? t("variables.hidden")
                              : variable.value}
                        </TableCell>
                      ) : (
                        // A plain value, which is the panel's to show: whole on
                        // a click, where it used to stop at the column's edge
                        // with no way to read the rest, and copied in one.
                        <TableCell className="max-w-xs font-mono text-xs text-muted-foreground">
                          {/* At least wide enough to read when opened: a
                              wrapped value's narrowest is one character. */}
                          <div className="flex max-w-xs min-w-40 items-start gap-1">
                            <Button
                              variant="ghost"
                              size="sm"
                              aria-expanded={expanded.has(variable.id)}
                              className="h-auto min-h-8 min-w-0 flex-1 justify-start px-1.5 font-mono text-xs font-normal text-muted-foreground"
                              onClick={() => toggleExpanded(variable.id)}
                            >
                              <span
                                className={
                                  expanded.has(variable.id)
                                    ? "min-w-0 text-left break-all whitespace-pre-wrap"
                                    : "min-w-0 truncate"
                                }
                              >
                                {variable.value}
                              </span>
                            </Button>
                            <CopyButton value={variable.value} label={variable.key} />
                          </div>
                        </TableCell>
                      )}
                      <TableCell className="text-right whitespace-nowrap">
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={t("variables.editKey", { key: variable.key })}
                          onClick={() => openForm(variable)}
                        >
                          <PencilIcon className="size-4 text-muted-foreground" />
                        </Button>
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
                          aria-label={t("variables.deleteKey", { key: variable.key })}
                          disabled={remove.isPending}
                          onClick={() => askThenRemove(variable)}
                        >
                          <Trash2Icon className="size-4 text-muted-foreground" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ),
                )}
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

/**
 * The form for a variable: a new one, or an existing one's row opened in
 * place.
 *
 * An existing variable keeps its name, starts from what it is — its value
 * when that is the panel's to show, whether it is a secret and whether the
 * build reads it — and sends both answers as they stand. Changing a value used
 * to mean adding the variable again and typing its name, on a form that said
 * nothing of whether it had been a secret.
 */
function VariableForm({
  existing,
  showBuildTime,
  projectId,
  pending,
  error,
  onSave,
  onUnchanged,
  onCancel,
}: {
  existing?: EditableVariable
  showBuildTime?: boolean
  projectId?: string
  pending: boolean
  error: unknown
  onSave: (variable: NewVariable) => void
  /** Saved with nothing different: nothing to send, and no rollout. */
  onUnchanged?: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const { team } = useSession()
  const reference = existing?.reference
  // A secret's value is sealed and never sent back, so its box starts empty
  // and an empty box means "as it is".
  const hiddenValue = Boolean(existing?.is_secret && !reference)
  const [key, setKey] = useState(existing?.key ?? "")
  const [value, setValue] = useState(existing && !existing.is_secret ? (existing.value ?? "") : "")
  const [isSecret, setIsSecret] = useState(existing?.is_secret ?? false)
  const [buildTime, setBuildTime] = useState(existing?.build_time ?? false)
  const [source, setSource] = useState<"value" | "manager">(reference ? "manager" : "value")
  const [connection, setConnection] = useState(reference?.connection ?? "")
  const [path, setPath] = useState(reference?.path ?? "")
  const [refKey, setRefKey] = useState(reference?.key ?? "")

  // Asked for only once somebody chooses to read the value from one.
  const managers = useQuery({
    queryKey: ["secret-managers", team?.id],
    queryFn: () => api.get<List<SecretManager>>(`/api/teams/${team?.id}/secret-managers`),
    enabled: Boolean(team) && source === "manager",
  })
  const connected = managers.data?.items ?? []
  // Only the ones this project may read through: one limited to other
  // projects would be refused when the variable is saved.
  const connections = connected.filter(
    (manager) =>
      projectId === undefined ||
      manager.allowed_project_ids.length === 0 ||
      manager.allowed_project_ids.includes(projectId),
  )
  // The one chosen, or the only one there is.
  const chosen = connection || (connections.length === 1 ? connections[0].name : "")
  const chosenManager = connections.find((manager) => manager.name === chosen)
  const fromManager = source === "manager"

  // The panel refuses a name Kubernetes cannot carry, and says so before the
  // request rather than after it.
  const invalid = key !== "" && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)

  // A secret kept as it is cannot change what it is: making it plain would
  // need its value, and so would anything else that is written with it.
  const flagsChanged =
    existing !== undefined &&
    (isSecret !== existing.is_secret || buildTime !== (existing.build_time ?? false))
  const needsValue = hiddenValue && !fromManager && value === "" && flagsChanged
  // Nothing different from what is stored: saved without a request, because
  // every save rolls the app out.
  const unchanged =
    existing !== undefined &&
    !flagsChanged &&
    (fromManager
      ? reference !== undefined &&
        chosen === reference.connection &&
        path.trim() === reference.path &&
        refKey.trim() === (reference.key ?? "")
      : hiddenValue
        ? value === ""
        : !reference && value === (existing.value ?? ""))

  return (
    <Card>
      <CardContent className="pt-6">
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            if (invalid || needsValue) return
            if (unchanged) {
              onUnchanged?.()
              return
            }
            // An existing variable's boxes started from what it is, so they
            // are answers: sent as they stand, ticked or not.
            if (existing) {
              const answers = {
                key: existing.key,
                build_time: showBuildTime ? buildTime : undefined,
              }
              onSave(
                fromManager
                  ? {
                      ...answers,
                      from: {
                        connection: chosen,
                        path: path.trim(),
                        key: refKey.trim() || undefined,
                      },
                    }
                  : { ...answers, value, is_secret: isSecret },
              )
              return
            }
            // A box left unticked says nothing rather than no. Saying no made a
            // secret re-added with a new value a plain one, shown to viewers,
            // and a build variable a runtime one; unticked, the panel keeps
            // what the variable was.
            if (fromManager) {
              // No value and no secret box: the value is never here, and a
              // variable read from a secret manager is a secret anyway.
              onSave({
                key: key.trim(),
                build_time: buildTime || undefined,
                from: { connection: chosen, path: path.trim(), key: refKey.trim() || undefined },
              })
              return
            }
            onSave({
              key: key.trim(),
              value,
              is_secret: isSecret || undefined,
              build_time: buildTime || undefined,
            })
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
                // An existing variable is changed under its own name: another
                // name is another variable.
                readOnly={existing !== undefined}
                autoFocus={existing === undefined}
                required
                aria-invalid={invalid}
              />
              {invalid && <FieldError>{t("variables.invalidKey")}</FieldError>}
            </Field>
            <Field>
              <FieldLabel htmlFor="variable-source">{t("variables.source")}</FieldLabel>
              <Select
                value={source}
                onValueChange={(next) => setSource(next as "value" | "manager")}
              >
                <SelectTrigger id="variable-source">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="value">{t("variables.sourceValue")}</SelectItem>
                  <SelectItem value="manager">{t("variables.sourceManager")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          </div>

          {fromManager ? (
            managers.isLoading ? (
              <Skeleton className="h-16" />
            ) : managers.error ? (
              <ErrorDisplay error={managers.error} compact />
            ) : connections.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {connected.length === 0
                  ? t("variables.noSecretManagers")
                  : t("variables.noSecretManagersForProject")}
              </p>
            ) : (
              <div className="space-y-2">
                <div className="grid gap-4 sm:grid-cols-3">
                  <Field>
                    <FieldLabel htmlFor="variable-connection">
                      {t("variables.connection")}
                    </FieldLabel>
                    <Select value={chosen} onValueChange={setConnection}>
                      <SelectTrigger id="variable-connection">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {connections.map((manager) => (
                          <SelectItem key={manager.id} value={manager.name}>
                            {manager.name} · {t(`secretManagers.kind.${manager.kind}`)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="variable-path">{t("variables.path")}</FieldLabel>
                    <Input
                      id="variable-path"
                      value={path}
                      className="font-mono"
                      spellCheck={false}
                      placeholder="shop/production"
                      onChange={(event) => setPath(event.target.value)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="variable-ref-key">{t("variables.refKey")}</FieldLabel>
                    <Input
                      id="variable-ref-key"
                      value={refKey}
                      className="font-mono"
                      spellCheck={false}
                      placeholder="stripe_key"
                      onChange={(event) => setRefKey(event.target.value)}
                    />
                  </Field>
                </div>
                <FieldDescription>{t("variables.referenceHelp")}</FieldDescription>
                {chosenManager && chosenManager.allowed_paths.length > 0 && (
                  <FieldDescription>
                    {t("variables.allowedPathsHint", {
                      name: chosenManager.name,
                      paths: chosenManager.allowed_paths.join(", "),
                    })}
                  </FieldDescription>
                )}
              </div>
            )
          ) : (
            <Field>
              <FieldLabel htmlFor="variable-value">{t("variables.value")}</FieldLabel>
              <Input
                id="variable-value"
                value={value}
                onChange={(event) => setValue(event.target.value)}
                className="font-mono"
                type={isSecret ? "password" : "text"}
                autoComplete="off"
                spellCheck={false}
                autoFocus={existing !== undefined}
                placeholder={hiddenValue ? t("variables.keptPlaceholder") : undefined}
                aria-invalid={needsValue}
              />
              {hiddenValue && !needsValue && (
                <FieldDescription>{t("variables.keptHelp")}</FieldDescription>
              )}
              {needsValue && <FieldError>{t("variables.keptNeedsValue")}</FieldError>}
            </Field>
          )}

          <div className="space-y-2">
            {!fromManager && (
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
            )}
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
            <Button
              type="submit"
              disabled={
                pending ||
                !key.trim() ||
                invalid ||
                needsValue ||
                (fromManager && (!chosen || !path.trim()))
              }
            >
              {pending ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
