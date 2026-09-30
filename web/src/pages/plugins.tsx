import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  BadgeCheckIcon,
  ExternalLinkIcon,
  OctagonAlertIcon,
  PuzzleIcon,
  ShieldAlertIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react"

import { useConfirm } from "@/components/confirm-dialog"
import { EmptyState } from "@/components/empty-state"
import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { StatusBadge } from "@/components/status-badge"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { api, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import type {
  InstalledPlugin,
  PluginInspection,
  StoreCatalogue,
  StoreEntry,
} from "@/lib/types"

/**
 * Plugins.
 *
 * Installing is two steps and the page is shaped around that: whatever you
 * started from — the store, an address, a pasted manifest — you end at the same
 * dialog listing what the plugin may do, and only then is there a button. One
 * step would mean the permissions were shown by the same click that granted
 * them, which is a dialog nobody reads because it is already too late.
 */
export function PluginsPage() {
  const { t } = useTranslation()
  const [pending, setPending] = useState<PluginInspection | null>(null)
  const [source, setSource] = useState<{ url?: string; manifest?: string }>({})

  const installed = useQuery({
    queryKey: ["plugins"],
    queryFn: () => api.get<List<InstalledPlugin>>("/api/plugins"),
  })

  function review(inspection: PluginInspection, from: { url?: string; manifest?: string }) {
    setSource(from)
    setPending(inspection)
  }

  return (
    <Page>
      <PageHeader title={t("plugins.title")} description={t("plugins.subtitle")} />

      <Tabs defaultValue="installed">
        <TabsList>
          <TabsTrigger value="installed">{t("plugins.installed")}</TabsTrigger>
          <TabsTrigger value="store">{t("plugins.store")}</TabsTrigger>
          <TabsTrigger value="manual">{t("plugins.fromAddress")}</TabsTrigger>
        </TabsList>

        <TabsContent value="installed" className="space-y-3 pt-4">
          {installed.isLoading ? (
            <Skeleton className="h-40" />
          ) : installed.error ? (
            <ErrorDisplay error={installed.error} onRetry={() => void installed.refetch()} />
          ) : (installed.data?.items.length ?? 0) === 0 ? (
            <EmptyState
              icon={PuzzleIcon}
              title={t("plugins.empty")}
              description={t("plugins.emptyHelp")}
            />
          ) : (
            installed.data?.items.map((plugin) => (
              <InstalledCard key={plugin.id} plugin={plugin} />
            ))
          )}
        </TabsContent>

        <TabsContent value="store" className="pt-4">
          <StoreTab onReview={review} />
        </TabsContent>

        <TabsContent value="manual" className="pt-4">
          <ManualTab onReview={review} />
        </TabsContent>
      </Tabs>

      <ReviewDialog
        inspection={pending}
        source={source}
        onOpenChange={(open) => !open && setPending(null)}
      />
    </Page>
  )
}

/** One installed plugin: what it is, what it may do, and its own settings. */
function InstalledCard({ plugin }: { plugin: InstalledPlugin }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const [values, setValues] = useState<Record<string, string>>({})

  const update = useMutation({
    mutationFn: (body: { enabled?: boolean; settings?: Record<string, string> }) =>
      api.patch<InstalledPlugin>(`/api/plugins/${plugin.id}`, body),
    onSuccess: () => {
      setValues({})
      void queryClient.invalidateQueries({ queryKey: ["plugins"] })
    },
  })

  const remove = useMutation({
    mutationFn: () => api.delete(`/api/plugins/${plugin.id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["plugins"] }),
  })

  async function onRemove() {
    const ok = await confirm({
      title: t("plugins.removeTitle", { name: plugin.decoded.name }),
      description: t("plugins.removeConfirm"),
      confirmLabel: t("common.delete"),
      destructive: true,
    })
    if (ok) remove.mutate()
  }

  const declared = plugin.decoded.settings ?? []

  return (
    <Card>
      <CardContent className="space-y-4 py-4">
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{plugin.decoded.name}</span>
              <Badge variant="outline" className="font-mono text-[10px]">
                {plugin.version}
              </Badge>
              <StatusBadge
                status={plugin.status}
                label={t(`plugins.status.${plugin.status}`, { defaultValue: plugin.status })}
              />
            </div>
            <p className="mt-0.5 text-sm text-muted-foreground">{plugin.decoded.description}</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {plugin.decoded.author.name} · {plugin.decoded.license} ·{" "}
              {t("plugins.installedWhen", { when: formatRelative(plugin.installed_at) })}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Switch
              checked={plugin.enabled}
              onCheckedChange={(enabled) => update.mutate({ enabled })}
              aria-label={t("plugins.enabled")}
            />
            <Button
              variant="ghost"
              size="icon"
              onClick={() => void onRemove()}
              aria-label={t("common.delete")}
            >
              <Trash2Icon className="size-4" />
            </Button>
          </div>
        </div>

        {plugin.status === "failed" && plugin.status_detail && (
          <Alert variant="destructive">
            <OctagonAlertIcon />
            <AlertTitle>{t("plugins.notRunning")}</AlertTitle>
            <AlertDescription>{plugin.status_detail}</AlertDescription>
          </Alert>
        )}

        <PermissionList permissions={plugin.decoded.permissions ?? []} />

        {declared.length > 0 && (
          <div className="space-y-3 border-t pt-3">
            {declared.map((setting) => {
              const stored = plugin.settings.find((s) => s.key === setting.key)
              return (
                <div key={setting.key} className="space-y-1">
                  <Label htmlFor={`${plugin.id}-${setting.key}`} className="text-sm">
                    {setting.label}
                    {setting.required && <span className="text-destructive"> *</span>}
                  </Label>
                  <Input
                    id={`${plugin.id}-${setting.key}`}
                    type={setting.secret ? "password" : "text"}
                    // A secret's stored value is never sent back, so the field
                    // is empty and says it is already set rather than showing
                    // dots that could be typed over by accident.
                    placeholder={
                      stored?.configured && setting.secret
                        ? t("plugins.secretStored")
                        : undefined
                    }
                    value={values[setting.key] ?? (setting.secret ? "" : (stored?.value ?? ""))}
                    onChange={(e) =>
                      setValues({ ...values, [setting.key]: e.target.value })
                    }
                  />
                  {setting.help && (
                    <p className="text-xs text-muted-foreground">{setting.help}</p>
                  )}
                </div>
              )
            })}
            <Button
              size="sm"
              variant="outline"
              disabled={Object.keys(values).length === 0 || update.isPending}
              onClick={() => update.mutate({ settings: values })}
            >
              {update.isPending && <Spinner className="size-4" />}
              {t("common.save")}
            </Button>
          </div>
        )}

        {update.error != null && <ErrorDisplay error={update.error} />}
        {remove.error != null && <ErrorDisplay error={remove.error} />}
      </CardContent>
    </Card>
  )
}

/** The store's catalogue, and what is known about who vouched for it. */
function StoreTab({
  onReview,
}: {
  onReview: (inspection: PluginInspection, from: { url?: string }) => void
}) {
  const { t } = useTranslation()
  const [inspecting, setInspecting] = useState<string | null>(null)

  const store = useQuery({
    queryKey: ["plugin-store"],
    queryFn: () => api.get<StoreCatalogue>("/api/plugins/store"),
    retry: false,
  })

  const inspect = useMutation({
    mutationFn: (entry: StoreEntry) =>
      api.post<PluginInspection>("/api/plugins/inspect", { url: entry.manifest_url }),
  })

  if (store.isLoading) return <Skeleton className="h-40" />
  if (store.error) {
    return <ErrorDisplay error={store.error} onRetry={() => void store.refetch()} />
  }
  const catalogue = store.data
  if (!catalogue) return null

  return (
    <div className="space-y-3">
      {/* Three different things, said differently: signed by the key this panel
          trusts, signed by nobody because no key is set, and — not here —
          signed by the wrong key, which is an error and never a catalogue. */}
      {catalogue.verified ? (
        <p className="flex items-center gap-1.5 text-xs text-success">
          <BadgeCheckIcon className="size-4" />
          {t("plugins.storeVerified", { url: catalogue.url })}
        </p>
      ) : (
        <Alert variant="warning">
          <TriangleAlertIcon />
          <AlertTitle>{t("plugins.storeUnsigned")}</AlertTitle>
          <AlertDescription>{t("plugins.storeUnsignedHelp")}</AlertDescription>
        </Alert>
      )}

      {catalogue.index.plugins.length === 0 ? (
        <EmptyState
          icon={PuzzleIcon}
          title={t("plugins.storeEmpty")}
          description={t("plugins.storeEmptyHelp")}
        />
      ) : (
        catalogue.index.plugins.map((entry) => {
          const version = catalogue.installed[entry.id]
          return (
            <Card key={entry.id}>
              <CardContent className="flex flex-wrap items-center gap-4 py-4">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{entry.name}</span>
                    <Badge variant="outline" className="font-mono text-[10px]">
                      {entry.version}
                    </Badge>
                    {entry.paid && (
                      <Badge variant="outline" className="text-[10px]">
                        {t("plugins.paid")}
                      </Badge>
                    )}
                  </div>
                  <p className="mt-0.5 text-sm text-muted-foreground">{entry.description}</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {entry.author} · {entry.license}
                  </p>
                </div>

                {entry.paid && entry.purchase_url && (
                  <Button variant="ghost" size="sm" asChild>
                    <a href={entry.purchase_url} target="_blank" rel="noreferrer">
                      <ExternalLinkIcon className="size-4" />
                      {t("plugins.buy")}
                    </a>
                  </Button>
                )}
                {version ? (
                  <Badge variant="secondary">
                    {version === entry.version
                      ? t("plugins.alreadyInstalled")
                      : t("plugins.upgradeAvailable", { version: entry.version })}
                  </Badge>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={inspect.isPending}
                    onClick={() => {
                      setInspecting(entry.id)
                      inspect.mutate(entry, {
                        onSuccess: (inspection) =>
                          onReview(inspection, { url: entry.manifest_url }),
                      })
                    }}
                  >
                    {inspect.isPending && inspecting === entry.id && (
                      <Spinner className="size-4" />
                    )}
                    {t("plugins.review")}
                  </Button>
                )}
              </CardContent>
            </Card>
          )
        })
      )}
      {inspect.error != null && <ErrorDisplay error={inspect.error} />}
    </div>
  )
}

/** Installing from an address, or from a manifest pasted straight in. */
function ManualTab({
  onReview,
}: {
  onReview: (inspection: PluginInspection, from: { url?: string; manifest?: string }) => void
}) {
  const { t } = useTranslation()
  const [url, setUrl] = useState("")
  const [manifest, setManifest] = useState("")

  const inspect = useMutation({
    mutationFn: (body: { url?: string; manifest?: string }) =>
      api.post<PluginInspection>("/api/plugins/inspect", body),
  })

  function submit() {
    const body = manifest.trim() ? { manifest } : { url }
    inspect.mutate(body, { onSuccess: (inspection) => onReview(inspection, body) })
  }

  return (
    <div className="max-w-2xl space-y-4">
      <p className="text-sm text-muted-foreground">{t("plugins.fromAddressHelp")}</p>

      <div className="space-y-1">
        <Label htmlFor="plugin-url">{t("plugins.manifestAddress")}</Label>
        <Input
          id="plugin-url"
          value={url}
          placeholder="https://example.com/skifity-plugin.yaml"
          onChange={(e) => setUrl(e.target.value)}
        />
      </div>

      <div className="space-y-1">
        <Label htmlFor="plugin-manifest">{t("plugins.orPaste")}</Label>
        <Textarea
          id="plugin-manifest"
          rows={10}
          value={manifest}
          className="font-mono text-xs"
          placeholder={"apiVersion: plugin.skifity.com/v1\nid: com.example.my-plugin"}
          onChange={(e) => setManifest(e.target.value)}
        />
      </div>

      <Button disabled={(!url.trim() && !manifest.trim()) || inspect.isPending} onClick={submit}>
        {inspect.isPending && <Spinner className="size-4" />}
        {t("plugins.review")}
      </Button>

      {inspect.error != null && <ErrorDisplay error={inspect.error} />}
    </div>
  )
}

/**
 * What installing this would mean, and the only place there is an install
 * button.
 *
 * The permissions go back with the request. The server refuses when they no
 * longer match the manifest, which is what stops a manifest changing between
 * this screen and the button.
 */
function ReviewDialog({
  inspection,
  source,
  onOpenChange,
}: {
  inspection: PluginInspection | null
  source: { url?: string; manifest?: string }
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const install = useMutation({
    mutationFn: () =>
      api.post<InstalledPlugin>("/api/plugins", {
        ...source,
        permissions: inspection?.permissions ?? [],
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["plugins"] })
      void queryClient.invalidateQueries({ queryKey: ["plugin-store"] })
      onOpenChange(false)
    },
  })

  if (!inspection) return null
  const manifest = inspection.manifest

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{manifest.name}</DialogTitle>
          <DialogDescription>{manifest.description}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4 text-sm">
          <div className="text-xs text-muted-foreground">
            {manifest.author.name} · {manifest.license} · {manifest.version}
          </div>

          {/* The one power that can stop everybody's work rather than merely
              read something, so it is not a line in a list. */}
          {inspection.blocks_deploys && (
            <Alert variant="warning">
              <ShieldAlertIcon />
              <AlertTitle>{t("plugins.blocksDeploys")}</AlertTitle>
              <AlertDescription>{t("plugins.blocksDeploysHelp")}</AlertDescription>
            </Alert>
          )}

          <div>
            <p className="mb-1 font-medium">{t("plugins.willBeAbleTo")}</p>
            <PermissionList permissions={inspection.permissions} />
          </div>

          <p className="text-xs text-muted-foreground">{t("plugins.confinementNote")}</p>

          <div className="space-y-1 text-xs text-muted-foreground">
            <p className="font-mono break-all">{manifest.image}</p>
            <p>{t("plugins.digestNote")}</p>
          </div>
        </div>

        {install.error != null && <ErrorDisplay error={install.error} />}

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button disabled={install.isPending} onClick={() => install.mutate()}>
            {install.isPending && <Spinner className="size-4" />}
            {t("plugins.install")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** A permission list in words, with the resource and the direction apart. */
function PermissionList({ permissions }: { permissions: string[] }) {
  const { t } = useTranslation()
  if (permissions.length === 0) {
    return <p className="text-xs text-muted-foreground">{t("plugins.noPermissions")}</p>
  }
  return (
    <ul className="space-y-1 text-sm">
      {permissions.map((permission) => {
        const [resource, action] = permission.split(":")
        return (
          <li key={permission} className="flex items-center gap-2">
            <Badge variant="outline" className="font-mono text-[10px]">
              {permission}
            </Badge>
            <span className="text-muted-foreground">
              {t(`plugins.permission.${action}`, { defaultValue: action })}{" "}
              {t(`plugins.resource.${resource}`, { defaultValue: resource })}
            </span>
          </li>
        )
      })}
    </ul>
  )
}
