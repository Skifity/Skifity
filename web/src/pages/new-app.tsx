import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ChevronRightIcon,
  ContainerIcon,
  FolderIcon,
  GitBranchIcon,
  SparklesIcon,
} from "lucide-react"
import { toast } from "sonner"

import { cn } from "cn"

import { AppNeeds } from "@/components/app-needs"
import { ErrorDisplay } from "@/components/error-display"
import {
  FolderPicker,
  FolderProblem,
  FolderSummary,
  TerminalInstructions,
} from "@/components/folder-picker"
import { Page, PageHeader } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useSession } from "@/hooks/use-session"
import { api, type List } from "@/lib/api"
import { parseDotEnv } from "@/lib/dotenv"
import { packFolder } from "@/lib/folder"
import { queryClient } from "@/lib/query"
import type {
  App,
  ComposeService,
  Deployment,
  Detection,
  GitSource,
  InitialDatabaseResult,
  WebhookStatus,
} from "@/lib/types"
import { Spinner } from "@/components/ui/spinner"

type SourceType = "git" | "image" | "upload"

/**
 * Creating an app asks for as little as possible.
 *
 * A repository URL is enough: the builder is detected, the port comes from the
 * PORT variable the panel sets, and everything else has a safe default that can
 * be changed later from the app's own settings.
 */
export function NewAppPage() {
  const { t } = useTranslation()
  const { envId = "" } = useParams()
  const navigate = useNavigate()

  const [sourceType, setSourceType] = useState<SourceType>("git")
  const [name, setName] = useState("")
  const [repoURL, setRepoURL] = useState("")
  const [branch, setBranch] = useState("")
  const [rootDir, setRootDir] = useState("")
  const [image, setImage] = useState("")
  const [builder, setBuilder] = useState("auto")
  const [dockerfilePath, setDockerfilePath] = useState("")
  const [port, setPort] = useState("")
  const [healthPath, setHealthPath] = useState("")
  const [buildCommand, setBuildCommand] = useState("")
  const [staticDir, setStaticDir] = useState("")
  const [startCommand, setStartCommand] = useState("")
  const [deployNow, setDeployNow] = useState(true)
  const [gitSourceID, setGitSourceID] = useState("")
  // The Compose service this app is, when the repository has a Compose file.
  // A file describes several services and an app runs one, so this is a choice
  // rather than an import: picking one fills the form in from it.
  const [composeService, setComposeService] = useState("")
  const [variables, setVariables] = useState<Record<string, string>>({})
  // What detection found this app needs is offered, not imposed: this holds
  // only the databases somebody unticked, and the rest is worked out below.
  const [declined, setDeclined] = useState<string[]>([])
  // A pasted .env, as typed. Parsed on every render rather than into state, so
  // what is sent is always exactly what is in the box.
  const [pastedEnv, setPastedEnv] = useState("")

  // A public repository needs no account. This picker only appears once one is
  // connected, so the common case stays a single field.
  const { team } = useSession()
  const gitSources = useQuery({
    queryKey: ["git-sources", team?.id],
    queryFn: () => api.get<List<GitSource>>(`/api/teams/${team!.id}/git-sources`),
    enabled: Boolean(team),
  })
  const sources = gitSources.data?.items ?? []

  // Looking at the repository before anything is created.
  //
  // Explicit rather than on every keystroke: a provider's rate limit is shared
  // by the whole panel, and a form that quietly makes requests while somebody
  // types is one that runs out of them on the day it matters.
  const detect = useMutation({
    mutationFn: () =>
      api.post<Detection>(`/api/teams/${team!.id}/detect`, {
        repo_url: repoURL.trim(),
        branch: branch.trim(),
        root_dir: rootDir.trim(),
        git_source_id: gitSourceID,
      }),
    onSuccess: (found) => applyDetection(found),
  })

  // A folder picked on this computer: packed here, leaving out what
  // .gitignore, node_modules and every .env are, and read by the panel the way
  // a repository is. Nothing is kept until the app is created; the same
  // archive is sent to it then.
  const pick = useMutation({
    mutationFn: async (files: File[]) => {
      const folder = await packFolder(files)
      const detection = await api.upload<Detection>(
        `/api/teams/${team!.id}/detect-upload`,
        folder.archive,
        "POST",
      )
      return { folder, detection }
    },
    onSuccess: ({ folder, detection }) => {
      applyDetection(detection)
      // The .env is offered, in the box below, where it can be read and
      // changed before anything is sent — and it is sent as variables, never
      // as a file.
      if (folder.dotenv && !pastedEnv.trim()) setPastedEnv(folder.dotenv)
    },
  })

  function applyDetection(found: Detection) {
    // A different repository is a different set of needs; what was unticked
    // for the last one says nothing about this one.
    setDeclined([])
    // Only fields nobody has filled in, and only when the guess is a
    // statement rather than a question. Overwriting what somebody typed
    // because a heuristic disagreed is the behaviour that makes people stop
    // trusting a form.
    if (found.compose && found.compose.length > 0) {
      setComposeService("")
      setVariables({})
    }
    if (found.confidence !== "high") return
    if (!port && found.port) setPort(String(found.port))
    if (!healthPath && found.health_path) setHealthPath(found.health_path)
    if (!startCommand && found.start_command) setStartCommand(found.start_command)
    // A front end is built and then served. These two say how, and until now
    // the form read them and sent neither, so the build copied the
    // repository into a web server and the page came up blank.
    if (!buildCommand && found.build_command) setBuildCommand(found.build_command)
    if (!staticDir && found.static_dir) setStaticDir(found.static_dir)
    if (builder === "auto" && (found.builder === "dockerfile" || found.builder === "static")) {
      setBuilder(found.builder)
    }
    if (!dockerfilePath && found.dockerfile_path) setDockerfilePath(found.dockerfile_path)
  }
  const found = sourceType === "upload" ? pick.data?.detection : detect.data
  const folder = sourceType === "upload" ? pick.data?.folder : undefined

  const pasted = parseDotEnv(pastedEnv)
  const needs = sourceType === "image" ? [] : (found?.needs ?? [])
  const databases = needs
    .filter((n) => n.kind === "database" && n.provided && n.engine && !declined.includes(n.engine))
    .map((n) => ({ engine: n.engine!, variable: n.variable ?? "" }))

  // "github.com/you/blog" becomes "blog", which is almost always the name the
  // user would have typed anyway.
  const suggestedName = (() => {
    if (sourceType === "image") return image.split("/").pop()?.split(":")[0] ?? ""
    if (sourceType === "upload") return folder?.name ?? ""
    return (
      repoURL
        .replace(/\.git$/, "")
        .split("/")
        .filter(Boolean)
        .pop() ?? ""
    )
  })()

  type Created = {
    app: App
    deployment?: Deployment
    webhook?: WebhookStatus
    databases?: InitialDatabaseResult[]
  }
  const create = useMutation({
    mutationFn: async () => {
      const result = await api.post<Created>(`/api/environments/${envId}/apps`, {
        name: (name.trim() || suggestedName).trim(),
        source_type: sourceType,
        repo_url: sourceType === "git" ? repoURL.trim() : "",
        git_source_id: sourceType === "git" ? gitSourceID : "",
        branch: branch.trim(),
        root_dir: rootDir.trim(),
        image: sourceType === "image" ? image.trim() : "",
        builder,
        dockerfile_path: dockerfilePath.trim(),
        port: Number(port) || 0,
        health_path: healthPath.trim(),
        build_command: buildCommand.trim(),
        static_dir: staticDir.trim(),
        start_command: startCommand.trim(),
        // An app from a folder has no code until the folder is sent, which
        // happens below, once it exists.
        deploy: sourceType === "upload" ? false : deployNow,
        // A pasted line wins over the same name from a Compose service:
        // it is what the person typed last, into the box in front of them.
        variables: { ...variables, ...pasted },
        // Created and linked before the first deploy, so its first start
        // finds the connection string there.
        databases,
      })
      if (sourceType !== "upload" || !folder) return result

      // The app exists now, so a failure from here on is not a failure to
      // create it: it is said, and the app's page is where the folder can be
      // sent again.
      try {
        const sent = await api.upload<{ sha256: string }>(
          `/api/apps/${result.app.id}/source`,
          folder.archive,
        )
        if (deployNow) {
          result.deployment = await api.post<Deployment>(`/api/apps/${result.app.id}/deploy`, {
            commit_sha: sent.sha256,
          })
        }
      } catch (error) {
        toast.error(t("folder.sendFailed"), {
          description: error instanceof Error ? error.message : String(error),
          duration: 15000,
        })
      }
      return result
    },
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["apps", envId] })
      // A database that could not be made does not undo the app, so it has to
      // be said here — the app page will not know it was ever asked for.
      for (const database of result.databases ?? []) {
        if (database.error) {
          toast.warning(
            t("needs.databaseFailed", { engine: t(`needs.engine.${database.engine}`) }),
            {
              description: database.error,
              duration: 15000,
            },
          )
        }
      }
      // Whether pushes will deploy is worth knowing now rather than the first
      // time somebody pushes and nothing happens.
      if (result.webhook?.url) {
        if (result.webhook.registered) {
          toast.success(t("git.pushDeploysOn"), { description: t("git.pushDeploysOnHelp") })
        } else {
          toast.warning(t("git.pushDeploysManual"), {
            description: `${t("git.pushDeploysManualHelp")} ${result.webhook.url}`,
            duration: 15000,
          })
        }
      }
      navigate(`/apps/${result.app.id}`)
    },
  })

  const ready =
    sourceType === "git"
      ? repoURL.trim() !== ""
      : sourceType === "image"
        ? image.trim() !== ""
        : folder !== undefined

  return (
    <Page width="narrow">
      {/* The description used to be the apps empty state, which ends "or start
          from a template" — a third path the form does not offer. It is a
          button now, and the sentence says what this page actually does. */}
      <PageHeader
        title={t("apps.newApp")}
        description={t("apps.newAppSubtitle")}
        actions={
          <Button variant="outline" asChild>
            <Link to="/templates">
              <SparklesIcon />
              {t("apps.startFromTemplate")}
            </Link>
          </Button>
        }
      />

      <form
        className="space-y-6"
        onSubmit={(event) => {
          event.preventDefault()
          create.mutate()
        }}
      >
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("apps.source")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-3">
              <SourceChoice
                active={sourceType === "git"}
                icon={GitBranchIcon}
                label={t("apps.sourceGit")}
                onClick={() => setSourceType("git")}
              />
              <SourceChoice
                active={sourceType === "upload"}
                icon={FolderIcon}
                label={t("apps.sourceFolder")}
                onClick={() => setSourceType("upload")}
              />
              <SourceChoice
                active={sourceType === "image"}
                icon={ContainerIcon}
                label={t("apps.sourceImage")}
                onClick={() => setSourceType("image")}
              />
            </div>

            {sourceType === "upload" ? (
              <>
                <Field>
                  <FieldLabel>{t("folder.label")}</FieldLabel>
                  <div className="flex flex-wrap items-center gap-3">
                    <FolderPicker
                      pending={pick.isPending}
                      label={folder ? t("folder.chooseAnother") : t("folder.choose")}
                      onPicked={(files) => pick.mutate(files)}
                    />
                    {folder && <FolderSummary folder={folder} />}
                  </div>
                  <FieldDescription>{t("folder.help")}</FieldDescription>
                </Field>
                {pick.error != null && <FolderProblem error={pick.error} />}
                {found && <DetectionSummary detection={found} />}
                {folder?.dotenv && pastedEnv === folder.dotenv && (
                  <Alert variant="info">
                    <AlertDescription>{t("folder.dotenvRead")}</AlertDescription>
                  </Alert>
                )}
                {found && (
                  <AppNeeds
                    needs={needs}
                    declined={declined}
                    onToggle={(engine, create) =>
                      setDeclined(
                        create ? declined.filter((e) => e !== engine) : [...declined, engine],
                      )
                    }
                    pasted={pastedEnv}
                    onPaste={setPastedEnv}
                    pastedKeys={Object.keys(pasted)}
                    otherKeys={Object.keys(variables)}
                  />
                )}
                <TerminalInstructions />
              </>
            ) : sourceType === "git" ? (
              <>
                <Field>
                  <FieldLabel htmlFor="repo-url">{t("apps.repository")}</FieldLabel>
                  <Input
                    id="repo-url"
                    value={repoURL}
                    onChange={(event) => setRepoURL(event.target.value)}
                    placeholder={t("apps.repositoryPlaceholder")}
                    autoFocus
                    required
                  />
                  <FieldDescription>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="-ml-2 h-7"
                      disabled={!repoURL.trim() || detect.isPending}
                      onClick={() => detect.mutate()}
                    >
                      {detect.isPending ? <Spinner /> : <SparklesIcon className="size-3.5" />}
                      {t("apps.detect")}
                    </Button>
                  </FieldDescription>
                </Field>

                {detect.error != null && <ErrorDisplay error={detect.error} compact />}
                {found && <DetectionSummary detection={found} />}
                {found && sourceType === "git" && (
                  <AppNeeds
                    needs={needs}
                    declined={declined}
                    onToggle={(engine, create) =>
                      setDeclined(
                        create ? declined.filter((e) => e !== engine) : [...declined, engine],
                      )
                    }
                    pasted={pastedEnv}
                    onPaste={setPastedEnv}
                    pastedKeys={Object.keys(pasted)}
                    otherKeys={Object.keys(variables)}
                  />
                )}
                {found?.compose && found.compose.length > 0 && (
                  <ComposeServices
                    services={found.compose}
                    warnings={found.compose_warnings}
                    chosen={composeService}
                    onChoose={(service) => {
                      setComposeService(service.name)
                      setVariables(service.environment ?? {})
                      if (!name.trim()) setName(service.name)
                      setRootDir(normaliseContext(service.build))
                      setPort(service.ports?.[0] ? String(service.ports[0]) : "")
                      // A service with an image and nothing to build is a
                      // prebuilt image, which is a different kind of app.
                      if (service.image && !service.build) {
                        setSourceType("image")
                        setImage(service.image)
                      } else {
                        setSourceType("git")
                        setImage("")
                      }
                    }}
                  />
                )}
                {sources.length > 0 && (
                  <Field>
                    <FieldLabel htmlFor="git-source">
                      {t("git.title")}{" "}
                      <span className="text-muted-foreground">({t("common.optional")})</span>
                    </FieldLabel>
                    <Select value={gitSourceID} onValueChange={setGitSourceID}>
                      <SelectTrigger id="git-source">
                        <SelectValue placeholder={t("common.none")} />
                      </SelectTrigger>
                      <SelectContent>
                        {sources.map((source) => (
                          <SelectItem key={source.id} value={source.id}>
                            {source.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FieldDescription>{t("git.help")}</FieldDescription>
                  </Field>
                )}
                <Field>
                  <FieldLabel htmlFor="branch">
                    {t("apps.branch")}{" "}
                    <span className="text-muted-foreground">({t("common.optional")})</span>
                  </FieldLabel>
                  <Input
                    id="branch"
                    value={branch}
                    onChange={(event) => setBranch(event.target.value)}
                    placeholder="main"
                  />
                </Field>
              </>
            ) : (
              <Field>
                <FieldLabel htmlFor="image">{t("apps.image")}</FieldLabel>
                <Input
                  id="image"
                  value={image}
                  onChange={(event) => setImage(event.target.value)}
                  placeholder={t("apps.imagePlaceholder")}
                  className="font-mono"
                  autoFocus
                  required
                />
              </Field>
            )}

            <Field>
              <FieldLabel htmlFor="app-name">{t("apps.appName")}</FieldLabel>
              <Input
                id="app-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={suggestedName}
              />
            </Field>
          </CardContent>
        </Card>

        {/* The same control as the one on Add a server: a bordered row with a
            chevron. It used to be a bare ghost button that said "Show advanced"
            and then "Hide advanced", which is a different affordance for the
            same idea two pages apart in one flow. */}
        <Collapsible className="rounded-md border">
          <CollapsibleTrigger className="group/advanced flex w-full items-center gap-2 p-3 text-sm font-medium">
            <ChevronRightIcon className="size-4 transition-transform group-data-[state=open]/advanced:rotate-90" />
            {t("common.showAdvanced")}
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="space-y-4 border-t p-4">
              <Field>
                <FieldLabel htmlFor="port">{t("apps.port")}</FieldLabel>
                <Input
                  id="port"
                  type="number"
                  min={1}
                  max={65535}
                  value={port}
                  onChange={(event) => setPort(event.target.value)}
                  placeholder="3000"
                />
                <FieldDescription>{t("apps.portHelp", { product: "Skifity" })}</FieldDescription>
              </Field>

              <Field>
                <FieldLabel htmlFor="health-path">{t("apps.healthPath")}</FieldLabel>
                <Input
                  id="health-path"
                  value={healthPath}
                  onChange={(event) => setHealthPath(event.target.value)}
                  placeholder="/healthz"
                  className="font-mono"
                />
                <FieldDescription>{t("apps.healthPathHelp")}</FieldDescription>
              </Field>

              {sourceType !== "image" && (
                <>
                  <Field>
                    <FieldLabel htmlFor="root-dir">{t("apps.rootDirectory")}</FieldLabel>
                    <Input
                      id="root-dir"
                      value={rootDir}
                      onChange={(event) => setRootDir(event.target.value)}
                      placeholder="apps/web"
                      className="font-mono"
                    />
                    <FieldDescription>{t("apps.rootDirectoryHelp")}</FieldDescription>
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="builder">{t("apps.builder")}</FieldLabel>
                    <Select value={builder} onValueChange={setBuilder}>
                      <SelectTrigger id="builder">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="auto">{t("apps.builderAuto")}</SelectItem>
                        <SelectItem value="dockerfile">{t("apps.builderDockerfile")}</SelectItem>
                        <SelectItem value="static">{t("apps.builderStatic")}</SelectItem>
                      </SelectContent>
                    </Select>
                  </Field>

                  {builder === "dockerfile" && (
                    <Field>
                      <FieldLabel htmlFor="dockerfile-path">{t("apps.dockerfilePath")}</FieldLabel>
                      <Input
                        id="dockerfile-path"
                        value={dockerfilePath}
                        onChange={(event) => setDockerfilePath(event.target.value)}
                        placeholder="Dockerfile"
                        className="font-mono"
                      />
                    </Field>
                  )}
                </>
              )}

              <Field>
                <FieldLabel htmlFor="start-command">{t("apps.startCommand")}</FieldLabel>
                <Input
                  id="start-command"
                  value={startCommand}
                  onChange={(event) => setStartCommand(event.target.value)}
                  className="font-mono"
                />
                <FieldDescription>{t("apps.startCommandHelp")}</FieldDescription>
              </Field>
            </div>
          </CollapsibleContent>
        </Collapsible>

        <label className="flex items-center gap-2.5 text-sm">
          <Checkbox
            checked={deployNow}
            onCheckedChange={(checked) => setDeployNow(checked === true)}
          />
          {t("deploy.deployNow")}
        </label>

        {create.error && <ErrorDisplay error={create.error} />}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={() => navigate(-1)}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={!ready || create.isPending}>
            {create.isPending && <Spinner />}
            {create.isPending ? t("apps.deploying") : t("common.create")}
          </Button>
        </div>
      </form>
    </Page>
  )
}

function SourceChoice({
  active,
  icon: Icon,
  label,
  onClick,
}: {
  active: boolean
  icon: typeof GitBranchIcon
  label: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "flex items-center gap-2.5 rounded-lg border px-3 py-3 text-sm transition-colors",
        active
          ? "border-primary bg-primary/5 font-medium"
          : "text-muted-foreground hover:border-primary/40 hover:text-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" />
      {label}
    </button>
  )
}

/**
 * What the panel worked out about a repository.
 *
 * A guess presented as a guess. High confidence comes from a marker file the
 * repository's author put there — a package.json naming Next.js — and is stated;
 * anything lower is a question, and the fields below are left for the person to
 * answer. The notes are what make it reviewable rather than magic.
 */
/**
 * The services a Compose file describes, as things to create.
 *
 * Skifity runs one service per app, so this is a choice and not an import: the
 * file is read, every service is listed with what carried over and what did
 * not, and picking one fills the form in. The others are created the same way,
 * in the same environment, where they reach each other by name — which is what
 * the links in a Compose file become.
 */
function ComposeServices({
  services,
  warnings,
  chosen,
  onChoose,
}: {
  services: ComposeService[]
  warnings?: string[]
  chosen: string
  onChoose: (service: ComposeService) => void
}) {
  const { t } = useTranslation()

  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">{t("apps.composeFound", { count: services.length })}</p>
      <div className="grid gap-2">
        {services.map((service) => {
          const active = service.name === chosen
          const details = [
            service.image ? service.image : t("apps.composeBuilt"),
            service.ports?.[0] ? t("apps.composePort", { port: service.ports[0] }) : "",
            service.environment && Object.keys(service.environment).length > 0
              ? t("apps.composeVariables", { count: Object.keys(service.environment).length })
              : "",
          ].filter(Boolean)

          return (
            <button
              key={service.name}
              type="button"
              aria-pressed={active}
              onClick={() => onChoose(service)}
              className={cn(
                "rounded-lg border p-3 text-left transition-colors",
                "focus-visible:ring-ring/50 focus-visible:outline-none focus-visible:ring-[3px]",
                active ? "border-primary bg-primary/5" : "hover:bg-accent/40",
              )}
            >
              <span className="text-sm font-medium">{service.name}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">
                {details.join(" · ")}
              </span>
              {service.unsupported && service.unsupported.length > 0 && (
                <ul className="mt-1.5 space-y-0.5 text-xs text-muted-foreground">
                  {service.unsupported.map((note) => (
                    <li key={note}>{note}</li>
                  ))}
                </ul>
              )}
            </button>
          )
        })}
      </div>
      {warnings && warnings.length > 0 && (
        <ul className="space-y-0.5 text-xs text-muted-foreground">
          {warnings.map((warning) => (
            <li key={warning}>{warning}</li>
          ))}
        </ul>
      )}
      {chosen !== "" && <p className="text-xs text-muted-foreground">{t("apps.composeRest")}</p>}
    </div>
  )
}

/**
 * A Compose build context as a root directory.
 *
 * "." and "./" mean the repository itself, which is an empty root directory
 * here; anything else is the subdirectory, without the leading "./" that a
 * Compose file usually writes.
 */
function normaliseContext(context?: string): string {
  const trimmed = (context ?? "").trim().replace(/^\.\//, "").replace(/\/$/, "")
  return trimmed === "." ? "" : trimmed
}

function DetectionSummary({ detection }: { detection: Detection }) {
  const { t } = useTranslation()
  const sure = detection.confidence === "high"
  const what = [detection.framework, detection.language].filter(Boolean).join(" · ")

  return (
    <div
      className={cn(
        "rounded-lg border p-3 text-sm",
        sure ? "border-success/40 bg-success/5" : "border-dashed",
      )}
    >
      <p className="font-medium">
        {sure
          ? t("apps.detected", { what: what || detection.builder })
          : t("apps.detectedMaybe", { what: what || detection.builder })}
      </p>
      {detection.notes && detection.notes.length > 0 && (
        <ul className="mt-1.5 space-y-0.5 text-xs text-muted-foreground">
          {detection.notes.map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      )}
      {detection.truncated && (
        <p className="mt-1.5 text-xs text-muted-foreground">{t("apps.detectedPartly")}</p>
      )}
    </div>
  )
}
