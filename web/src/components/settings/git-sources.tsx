import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  CheckIcon,
  ClipboardIcon,
  GitBranchIcon,
  PlusIcon,
  Trash2Icon,
  WebhookIcon,
} from "lucide-react"
import { toast } from "sonner"

import { EmptyState } from "@/components/empty-state"
import { useConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Card, CardContent } from "@/components/ui/card"
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
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { GitSource } from "@/lib/types"

/**
 * Where a repository delivers pushes, and the secret it signs them with. A
 * connection made before each had a secret of its own signs with its token,
 * which is never shown here.
 */
type Webhook = { url: string; secret: string; secret_is_token?: boolean }

/** What each provider calls itself, and whether it can be self-hosted. */
const PROVIDERS = [
  { kind: "github_pat", label: "GitHub", selfHosted: false },
  { kind: "gitlab", label: "GitLab", selfHosted: true },
  { kind: "gitea", label: "Gitea / Forgejo", selfHosted: true },
] as const

export function GitSources() {
  const { t } = useTranslation()
  const { team } = useSession()
  const [adding, setAdding] = useState(false)
  const confirm = useConfirm()
  // Shown after connecting, and again on asking: a self-hosted Gitea or
  // GitLab whose token cannot register a webhook needs these pasted in by hand.
  const [webhook, setWebhook] = useState<Webhook | null>(null)

  const sources = useQuery({
    queryKey: ["git-sources", team?.id],
    queryFn: () => api.get<List<GitSource>>(`/api/teams/${team!.id}/git-sources`),
    enabled: Boolean(team),
  })

  const remove = useMutation({
    mutationFn: (sourceID: string) => api.delete(`/api/teams/${team!.id}/git-sources/${sourceID}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["git-sources", team?.id] }),
  })
  // Asked for on a click rather than loaded with the list: reading the secret
  // is recorded in the activity log, and only an admin may.
  const reveal = useMutation({
    mutationFn: (sourceID: string) =>
      api.get<Webhook>(`/api/teams/${team!.id}/git-sources/${sourceID}/webhook`),
    onSuccess: setWebhook,
  })

  if (sources.isLoading) return <Skeleton className="h-48" />
  if (sources.error) {
    return <ErrorDisplay error={sources.error} onRetry={() => void sources.refetch()} />
  }

  const items = sources.data?.items ?? []

  return (
    <div className="space-y-4">
      <p className="max-w-2xl text-sm text-muted-foreground">{t("git.help")}</p>

      {webhook && <WebhookNotice webhook={webhook} onDismiss={() => setWebhook(null)} />}
      {reveal.error != null && <ErrorDisplay error={reveal.error} compact />}

      {items.length === 0 && !adding ? (
        <EmptyState
          icon={GitBranchIcon}
          title={t("git.empty")}
          description={t("git.help")}
          action={
            <Button onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("git.connect")}
            </Button>
          }
        />
      ) : (
        <div className="space-y-3">
          {items.map((source) => (
            <Card key={source.id}>
              <CardContent className="flex flex-wrap items-center gap-3 py-4">
                <GitBranchIcon className="size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate font-medium">{source.name}</span>
                    <Badge variant="secondary" className="text-[10px]">
                      {PROVIDERS.find((provider) => provider.kind === source.kind)?.label ??
                        source.kind}
                    </Badge>
                  </div>
                  <p className="truncate text-xs text-muted-foreground">
                    {source.account || source.base_url || formatRelative(source.created_at)}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={reveal.isPending}
                  onClick={() => reveal.mutate(source.id)}
                >
                  <WebhookIcon className="size-4" />
                  {t("git.showWebhook")}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={remove.isPending}
                  onClick={() => {
                    void confirm({
                      title: t("git.disconnect"),
                      description: t("git.disconnectWarning"),
                      confirmLabel: t("git.disconnect"),
                      destructive: true,
                    }).then((yes) => {
                      if (yes) remove.mutate(source.id)
                    })
                  }}
                >
                  <Trash2Icon className="size-4" />
                  {t("git.disconnect")}
                </Button>
              </CardContent>
            </Card>
          ))}

          {!adding && (
            <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
              <PlusIcon className="size-4" />
              {t("git.connect")}
            </Button>
          )}
        </div>
      )}

      {adding && (
        <ConnectForm
          onDone={(made) => {
            setAdding(false)
            setWebhook(made ?? null)
          }}
        />
      )}
      {remove.error != null && <ErrorDisplay error={remove.error} />}
    </div>
  )
}

function WebhookNotice({ webhook, onDismiss }: { webhook: Webhook; onDismiss: () => void }) {
  const { t } = useTranslation()
  return (
    <Alert>
      <AlertTitle>{t("git.webhookURL")}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t("git.webhookURLHelp")}</p>
        <CopyLine value={webhook.url} />
        {webhook.secret ? (
          <>
            <p className="font-medium text-foreground">{t("git.webhookSecret")}</p>
            <p>{t("git.webhookSecretHelp")}</p>
            <CopyLine value={webhook.secret} />
          </>
        ) : (
          webhook.secret_is_token && <p>{t("git.webhookSecretIsToken")}</p>
        )}
        <Button variant="ghost" size="sm" onClick={onDismiss}>
          {t("common.done")}
        </Button>
      </AlertDescription>
    </Alert>
  )
}

function CopyLine({ value }: { value: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      toast.success(t("common.copied"))
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error(t("errors.somethingWentWrong"))
    }
  }

  return (
    <div className="flex items-center gap-2">
      <code className="block min-w-0 flex-1 rounded-md border bg-muted p-2 font-mono text-xs break-all">
        {value}
      </code>
      <Button variant="outline" size="sm" onClick={() => void copy()}>
        {copied ? <CheckIcon className="size-3.5" /> : <ClipboardIcon className="size-3.5" />}
        {copied ? t("common.copied") : t("common.copy")}
      </Button>
    </div>
  )
}

function ConnectForm({ onDone }: { onDone: (webhook?: Webhook) => void }) {
  const { t } = useTranslation()
  const { team } = useSession()
  const [kind, setKind] = useState<string>("github_pat")
  const [name, setName] = useState("")
  const [token, setToken] = useState("")
  const [baseURL, setBaseURL] = useState("")
  const [account, setAccount] = useState("")

  const provider = PROVIDERS.find((candidate) => candidate.kind === kind)

  const create = useMutation({
    mutationFn: () =>
      api.post<{ webhook_url: string; webhook_secret: string }>(
        `/api/teams/${team!.id}/git-sources`,
        {
          kind,
          name: name.trim() || provider?.label,
          token: token.trim(),
          base_url: baseURL.trim(),
          account: account.trim(),
        },
      ),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["git-sources", team?.id] })
      // The token only ever existed in this form.
      setToken("")
      onDone({ url: result.webhook_url, secret: result.webhook_secret })
    },
  })

  return (
    <Card>
      <CardContent className="pt-6">
        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault()
            create.mutate()
          }}
        >
          <Field>
            <FieldLabel htmlFor="git-kind">{t("git.provider")}</FieldLabel>
            <Select value={kind} onValueChange={setKind}>
              <SelectTrigger id="git-kind">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PROVIDERS.map((candidate) => (
                  <SelectItem key={candidate.kind} value={candidate.kind}>
                    {candidate.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>

          <Field>
            <FieldLabel htmlFor="git-name">{t("git.sourceName")}</FieldLabel>
            <Input
              id="git-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={provider?.label}
            />
            <FieldDescription>{t("git.sourceNameHelp")}</FieldDescription>
          </Field>

          {provider?.selfHosted && (
            <Field>
              <FieldLabel htmlFor="git-base-url">{t("git.baseURL")}</FieldLabel>
              <Input
                id="git-base-url"
                value={baseURL}
                onChange={(event) => setBaseURL(event.target.value)}
                placeholder="https://git.example.com"
              />
              <FieldDescription>{t("git.baseURLHelp")}</FieldDescription>
            </Field>
          )}

          <Field>
            <FieldLabel htmlFor="git-account">
              {t("git.account")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Input
              id="git-account"
              value={account}
              onChange={(event) => setAccount(event.target.value)}
              placeholder="your-org"
            />
          </Field>

          <Field>
            <FieldLabel htmlFor="git-token">{t("git.token")}</FieldLabel>
            <Input
              id="git-token"
              type="password"
              autoComplete="off"
              value={token}
              onChange={(event) => setToken(event.target.value)}
              required
            />
            <FieldDescription>{t("git.tokenHelp")}</FieldDescription>
          </Field>

          {create.error != null && <ErrorDisplay error={create.error} compact />}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={() => onDone()}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!token.trim() || create.isPending}>
              {create.isPending && <Spinner />}
              {create.isPending ? t("common.saving") : t("git.connect")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
