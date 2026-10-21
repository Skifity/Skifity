import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  ChevronRightIcon,
  ContainerIcon,
  FolderIcon,
  GitBranchIcon,
  LayersIcon,
  ShieldAlertIcon,
  SparklesIcon,
} from "lucide-react"
import { toast } from "sonner"

import { cn } from "cn"

import { AppNeeds, usableVariableName } from "@/components/app-needs"
import { DetectedProcesses } from "@/components/app/processes"
import { ErrorDisplay, toProblem } from "@/components/error-display"
import {
  FolderPicker,
  FolderProblem,
  FolderSummary,
  TerminalInstructions,
} from "@/components/folder-picker"
import { Page, PageHeader } from "@/components/page"
import { canListRepositories, RepositoryPicker } from "@/components/repository-picker"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useSession } from "@/hooks/use-session"
import { api, type List, type Problem } from "@/lib/api"
import { parseDotEnv } from "@/lib/dotenv"
import { packFolder } from "@/lib/folder"
import { queryClient } from "@/lib/query"
import { translated } from "@/lib/say"
import type {
  App,
  ComposeService,
  Deployment,
  Detection,
  Environment,
  GitListing,
  GitRepository,
  GitSource,
  InitialDatabaseResult,
  StackNote,
  WebhookStatus,
} from "@/lib/types"
import { Spinner } from "@/components/ui/spinner"

type SourceType = "git" | "image" | "upload"

/** What the Git connection picker holds for "none": a Select item cannot be empty. */
const NO_GIT_SOURCE = "__none__"

/** What POST /stack answers with, for a whole file or one service of it. */
type StackResult = { apps: App[]; notes: StackNote[]; webhook?: WebhookStatus }

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
  const [releaseCommand, setReleaseCommand] = useState("")
  const [deployNow, setDeployNow] = useState(true)
  const [gitSourceID, setGitSourceID] = useState("")
  // The Compose service this app is, when the repository has a Compose file.
  // A file describes several services and an app runs one, so this is a
  // choice. Picking one does not fill the form in: the service is created the
  // way the stack creates it, by the panel's own reading of the file, so one
  // service and the whole file can never disagree about its port, its address,
  // its variables or its disks.
  const [composeService, setComposeService] = useState("")
  // What detection found this app needs is offered, not imposed: this holds
  // only the databases somebody unticked, and the rest is worked out below.
  const [declined, setDeclined] = useState<string[]>([])
  // The Procfile's processes somebody unticked; the rest start with the app.
  const [skippedProcesses, setSkippedProcesses] = useState<string[]>([])
  // A pasted .env, as typed. Parsed on every render rather than into state, so
  // what is sent is always exactly what is in the box.
  const [pastedEnv, setPastedEnv] = useState("")

  // A public repository needs no account. This picker only appears once one is
  // connected, so the common case stays a single field.
  const { team, meta } = useSession()
  // Where Cancel goes: the project this environment is in. Going back in the
  // browser's history left the panel when the form was opened in a new tab.
  const environment = useQuery({
    queryKey: ["environment", envId],
    queryFn: () => api.get<Environment>(`/api/environments/${envId}`),
    enabled: Boolean(envId),
  })
  const gitSources = useQuery({
    queryKey: ["git-sources", team?.id],
    queryFn: () => api.get<List<GitSource>>(`/api/teams/${team!.id}/git-sources`),
    enabled: Boolean(team),
  })
  const sources = gitSources.data?.items ?? []
  // The connection chosen, when its host can be asked what it holds.
  const chosenSource = sources.find((source) => source.id === gitSourceID)
  const listable = canListRepositories(chosenSource) ? chosenSource : undefined
  // The repository picked from that list. It is the one in the form only while
  // the address is the one picking it filled in and the connection is the one
  // it came from: typing over either is typing the branch again too.
  const [picked, setPicked] = useState<{ sourceId: string; repository: GitRepository }>()
  const pickedRepository =
    picked && listable?.id === picked.sourceId && repoURL.trim() === picked.repository.url
      ? picked.repository
      : undefined
  const branches = useQuery({
    queryKey: ["git-branches", listable?.id, pickedRepository?.full_name],
    queryFn: () =>
      api.get<GitListing<{ name: string }>>(
        `/api/teams/${team!.id}/git-sources/${listable!.id}/branches?repo=` +
          encodeURIComponent(pickedRepository!.full_name),
      ),
    enabled: Boolean(team && listable && pickedRepository),
    staleTime: 60_000,
  })
  // A select only when the list is whole: one cut short could hide the branch
  // somebody wants, so then it is typed. What is in the field is always among
  // the choices, so the select never shows something other than what is sent.
  const branchChoices = (() => {
    if (!pickedRepository || !branches.data || branches.data.truncated) return undefined
    const names = branches.data.items.map((item) => item.name)
    if (branch && !names.includes(branch)) names.unshift(branch)
    return names.length > 0 ? names : undefined
  })()

  // Looking at the repository before anything is created.
  //
  // Explicit rather than on every keystroke: a provider's rate limit is shared
  // by the whole panel, and a form that quietly makes requests while somebody
  // types is one that runs out of them on the day it matters.
  const detectTarget = {
    repo_url: repoURL.trim(),
    branch: branch.trim(),
    root_dir: rootDir.trim(),
    git_source_id: gitSourceID,
  }
  const detect = useMutation({
    mutationFn: (target: typeof detectTarget) =>
      api.post<Detection>(`/api/teams/${team!.id}/detect`, target),
    onSuccess: (found) => applyDetection(found),
  })
  // What was detected belongs to what was looked at. Changing the repository
  // after Detect kept the first one's databases, processes and seed, and
  // Create made the second app with them.
  const detected =
    detect.data && JSON.stringify(detect.variables) === JSON.stringify(detectTarget)
      ? detect.data
      : undefined

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
    setSkippedProcesses([])
    // A service of the last file is not one of this one, even by the same name.
    setComposeService("")
    // Only fields nobody has filled in, and only when the guess is a
    // statement rather than a question. Overwriting what somebody typed
    // because a heuristic disagreed is the behaviour that makes people stop
    // trusting a form.
    //
    // A Procfile's release line and app.json's settings are the repository
    // saying so rather than a guess, so they apply whatever the confidence.
    if (!releaseCommand && found.release_command) setReleaseCommand(found.release_command)
    if (found.env_template && !pastedEnv.trim()) setPastedEnv(found.env_template)
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
  const found = sourceType === "upload" ? pick.data?.detection : detected
  const folder = sourceType === "upload" ? pick.data?.folder : undefined
  // The service picked, only while it is one of the file that was read.
  const composeServices = sourceType === "git" ? (detected?.compose ?? []) : []
  const chosenService = composeServices.find((service) => service.name === composeService)

  // What detection found is sent only while it is on the screen. The pasted
  // .env, the databases, the processes and the seed used to go with whatever
  // was created after it: an nginx image picked after a folder got that
  // folder's .env, which nobody was looking at any more.
  const showsNeeds = found !== undefined && sourceType !== "image" && !chosenService
  const pasted = parseDotEnv(pastedEnv)
  const needs = showsNeeds ? (found?.needs ?? []) : []
  const databases = needs
    .filter((n) => n.kind === "database" && n.provided && n.engine && !declined.includes(n.engine))
    .map((n) => ({ engine: n.engine!, variable: n.variable ?? "" }))
  const detectedProcesses = showsNeeds ? (found?.processes ?? []) : []
  const processes = detectedProcesses.filter((p) => !skippedProcesses.includes(p.name))

  // "github.com/you/blog" becomes "blog", which is almost always the name the
  // user would have typed anyway. A Compose service is called what the file
  // calls it, which is also the name the other services reach it by.
  const suggestedName = (() => {
    if (chosenService) return chosenService.name
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

  const appName = (name.trim() || suggestedName).trim()

  // Whether pushes will deploy is worth knowing now rather than the first
  // time somebody pushes and nothing happens. Said for a stack too, which
  // registers the same webhook.
  function sayWebhook(webhook?: WebhookStatus) {
    if (!webhook?.url) return
    if (webhook.registered) {
      toast.success(t("git.pushDeploysOn"), { description: t("git.pushDeploysOnHelp") })
    } else {
      toast.warning(t("git.pushDeploysManual"), {
        description: `${t("git.pushDeploysManualHelp")} ${webhook.url}`,
        duration: 15000,
      })
    }
  }

  type Created = {
    app: App
    deployment?: Deployment
    webhook?: WebhookStatus
    databases?: InitialDatabaseResult[]
  }
  const create = useMutation({
    mutationFn: async () => {
      const result = await api.post<Created>(`/api/environments/${envId}/apps`, {
        name: appName,
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
        release_command: releaseCommand.trim(),
        // app.json's postdeploy script, which Heroku runs in each review app.
        preview_seed: showsNeeds ? (found?.preview_seed ?? "") : "",
        // An app from a folder has no code until the folder is sent, which
        // happens below, once it exists.
        deploy: sourceType === "upload" ? false : deployNow,
        // Only while the box that shows them is on the screen.
        variables: showsNeeds ? pasted : {},
        // Created and linked before the first deploy, so its first start
        // finds the connection string there.
        databases,
        // The Procfile's worker and clock lines, on the same build.
        processes,
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
          description: problemLine(t, toProblem(error)),
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
              // The server's English only when it sent no problem to say.
              description: database.problem ? problemLine(t, database.problem) : database.error,
              duration: 15000,
            },
          )
        }
      }
      sayWebhook(result.webhook)
      navigate(`/apps/${result.app.id}`)
    },
  })

  // Services of a Compose file, created by the panel's own reading of it: the
  // whole file as one stack, or the one service picked. See handleCreateStack.
  const postStack = (services: ComposeService[], name?: string) =>
    api.post<StackResult>(`/api/environments/${envId}/stack`, {
      repo_url: repoURL.trim(),
      git_source_id: gitSourceID,
      branch: branch.trim(),
      // Where the Compose file is, which its build contexts are relative to:
      // the directory that was read, since the detection is still current.
      root_dir: rootDir.trim(),
      services,
      deploy: deployNow,
      name,
    })
  function afterStack(result: StackResult) {
    void queryClient.invalidateQueries({ queryKey: ["apps", envId] })
    if (result.apps.length > 1) {
      toast.success(t("apps.stackCreated", { count: result.apps.length }))
    }
    if (result.notes.length > 0) {
      toast.warning(t("apps.stackNotesTitle"), {
        description: result.notes
          .map((note) =>
            t(`apps.stackNote.${note.code}`, { service: note.service, value: note.value }),
          )
          .join("\n"),
        duration: 20000,
      })
    }
    sayWebhook(result.webhook)
    const first = result.apps.find((app) => !app.internal) ?? result.apps[0]
    if (first) navigate(`/apps/${first.id}`)
  }
  const stack = useMutation({
    mutationFn: () => postStack(found?.compose ?? []),
    onSuccess: afterStack,
  })
  // One service, under the name in the form: the stack's planner given a list
  // of one, so it is made exactly as the whole file would have made it. The
  // name goes beside the service rather than over the file's, which is how the
  // planner tells "My API" typed here from a name the file's other services
  // use, and only says the second was changed.
  const createService = useMutation({
    mutationFn: (service: ComposeService) => postStack([service], appName),
    onSuccess: afterStack,
  })

  // One request at a time. Create and "create all" could both be pressed, and
  // the second made an app the first had already made, or failed on its name.
  const busy = create.isPending || stack.isPending || createService.isPending
  const submitting = create.isPending || createService.isPending
  const submitError = chosenService ? createService.error : create.error

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
          if (busy) return
          if (chosenService) createService.mutate(chosenService)
          else create.mutate()
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
                {showsNeeds && (
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
                  />
                )}
                {showsNeeds && (
                  <DetectedProcesses
                    processes={detectedProcesses}
                    declined={skippedProcesses}
                    onToggle={(process, run) =>
                      setSkippedProcesses(
                        run
                          ? skippedProcesses.filter((p) => p !== process)
                          : [...skippedProcesses, process],
                      )
                    }
                  />
                )}
                <TerminalInstructions />
              </>
            ) : sourceType === "git" ? (
              <>
                {/* First, because the connection is what the repositories
                    below are offered from. */}
                {sources.length > 0 && (
                  <Field>
                    <FieldLabel htmlFor="git-source">
                      {t("git.title")}{" "}
                      <span className="text-muted-foreground">({t("common.optional")})</span>
                    </FieldLabel>
                    <Select
                      value={gitSourceID || NO_GIT_SOURCE}
                      onValueChange={(value) =>
                        setGitSourceID(value === NO_GIT_SOURCE ? "" : value)
                      }
                    >
                      <SelectTrigger id="git-source">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {/* A public repository needs no connection, and one
                            picked by mistake could not be put back. */}
                        <SelectItem value={NO_GIT_SOURCE}>{t("common.none")}</SelectItem>
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
                  <FieldLabel htmlFor="repo-url">{t("apps.repository")}</FieldLabel>
                  <Input
                    id="repo-url"
                    value={repoURL}
                    onChange={(event) => setRepoURL(event.target.value)}
                    placeholder={t("apps.repositoryPlaceholder")}
                    autoFocus
                    required
                  />
                  <FieldDescription className="flex flex-wrap items-center gap-x-4">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="-ml-2 h-7"
                      disabled={!repoURL.trim() || detect.isPending}
                      onClick={() => detect.mutate(detectTarget)}
                    >
                      {detect.isPending ? <Spinner /> : <SparklesIcon className="size-3.5" />}
                      {t("apps.detect")}
                    </Button>
                    {team && listable && (
                      <RepositoryPicker
                        key={listable.id}
                        teamId={team.id}
                        source={listable}
                        onPick={(repository) => {
                          setRepoURL(repository.url)
                          setBranch(repository.default_branch)
                          setPicked({ sourceId: listable.id, repository })
                        }}
                      />
                    )}
                  </FieldDescription>
                </Field>

                {detect.error != null && <ErrorDisplay error={detect.error} compact />}
                {found && <DetectionSummary detection={found} />}
                {showsNeeds && (
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
                  />
                )}
                {showsNeeds && (
                  <DetectedProcesses
                    processes={detectedProcesses}
                    declined={skippedProcesses}
                    onToggle={(process, run) =>
                      setSkippedProcesses(
                        run
                          ? skippedProcesses.filter((p) => p !== process)
                          : [...skippedProcesses, process],
                      )
                    }
                  />
                )}
                {composeServices.length > 0 && (
                  <ComposeServices
                    services={composeServices}
                    warnings={detected?.compose_warnings}
                    chosen={chosenService?.name ?? ""}
                    busy={busy}
                    creatingStack={stack.isPending}
                    stackError={stack.error}
                    onCreateStack={() => stack.mutate()}
                    // Picking the one already picked puts it back: the app is
                    // then the repository itself, as detected.
                    onChoose={(service) =>
                      setComposeService(service.name === composeService ? "" : service.name)
                    }
                  />
                )}
                {chosenService && <ComposeServicePlan service={chosenService} />}
                <Field>
                  <FieldLabel htmlFor="branch">
                    {t("apps.branch")}{" "}
                    <span className="text-muted-foreground">({t("common.optional")})</span>
                  </FieldLabel>
                  {branchChoices ? (
                    <Select value={branch} onValueChange={setBranch}>
                      <SelectTrigger id="branch" className="font-mono">
                        <SelectValue placeholder={t("git.chooseBranch")} />
                      </SelectTrigger>
                      <SelectContent>
                        {branchChoices.map((name) => (
                          <SelectItem key={name} value={name} className="font-mono">
                            {name === pickedRepository?.default_branch
                              ? t("git.defaultBranch", { branch: name })
                              : name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <Input
                      id="branch"
                      value={branch}
                      onChange={(event) => setBranch(event.target.value)}
                      placeholder="main"
                    />
                  )}
                  {branches.isFetching && (
                    <FieldDescription className="flex items-center gap-2">
                      <Spinner />
                      {t("git.readingBranches")}
                    </FieldDescription>
                  )}
                  {branches.error != null && <ErrorDisplay error={branches.error} compact />}
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
            same idea two pages apart in one flow.

            Not offered for a Compose service: its port, commands and build
            come from the file, as the plan above says, and fields that would
            not be sent are fields that lie. */}
        {!chosenService && (
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
                  <FieldDescription>
                    {t("apps.portHelp", { product: meta?.product ?? "Skifity" })}
                  </FieldDescription>
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
                        <FieldLabel htmlFor="dockerfile-path">
                          {t("apps.dockerfilePath")}
                        </FieldLabel>
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

                <Field>
                  <FieldLabel htmlFor="release-command">{t("apps.releaseCommand")}</FieldLabel>
                  <Input
                    id="release-command"
                    value={releaseCommand}
                    onChange={(event) => setReleaseCommand(event.target.value)}
                    placeholder="npm run migrate"
                    className="font-mono"
                  />
                  <FieldDescription>{t("apps.releaseCommandHelp")}</FieldDescription>
                </Field>
              </div>
            </CollapsibleContent>
          </Collapsible>
        )}

        <label className="flex items-center gap-2.5 text-sm">
          <Checkbox
            checked={deployNow}
            onCheckedChange={(checked) => setDeployNow(checked === true)}
          />
          {t("deploy.deployNow")}
        </label>

        {submitError != null && <ErrorDisplay error={submitError} />}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" asChild>
            <Link
              to={
                environment.data?.project_id
                  ? `/projects/${environment.data.project_id}`
                  : "/projects"
              }
            >
              {t("common.cancel")}
            </Link>
          </Button>
          {/* The words say what pressing it does: "Deploying..." while
              nothing was going to deploy was the button telling a lie. */}
          <Button type="submit" disabled={!ready || busy}>
            {submitting && <Spinner />}
            {submitting
              ? deployNow
                ? t("apps.deploying")
                : t("apps.creating")
              : deployNow
                ? t("apps.createAndDeploy")
                : t("common.create")}
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
    <Button
      type="button"
      variant="outline"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "h-auto justify-start gap-2.5 rounded-lg px-3 py-3",
        active
          ? "border-primary bg-primary/5 hover:bg-primary/10"
          : "font-normal text-muted-foreground",
      )}
    >
      <Icon />
      {label}
    </Button>
  )
}

/**
 * The services a Compose file describes, as things to create.
 *
 * Skifity runs one service per app, so this is a choice and not an import: the
 * file is read, every service is listed with what carried over and what did
 * not, and picking one makes this app that service. The others are created the
 * same way, in the same environment, where they reach each other by name —
 * which is what the links in a Compose file become.
 */
function ComposeServices({
  services,
  warnings,
  chosen,
  onChoose,
  busy,
  creatingStack,
  stackError,
  onCreateStack,
}: {
  services: ComposeService[]
  warnings?: string[]
  chosen: string
  onChoose: (service: ComposeService) => void
  /** Something is being created already, by this or by the form's own button. */
  busy: boolean
  creatingStack: boolean
  stackError: unknown
  onCreateStack: () => void
}) {
  const { t } = useTranslation()

  // The file's warnings repeat every service's notes as "name: note", which
  // printed most of them twice: under the service, and again below the list.
  const repeated = new Set(
    services.flatMap((service) =>
      (service.unsupported ?? []).map((note) => `${service.name}: ${note}`),
    ),
  )
  const ownWarnings = (warnings ?? []).filter((warning) => !repeated.has(warning))

  return (
    <div className="space-y-2">
      {/* The whole file at once: what Compose itself does with it. */}
      {services.length > 1 && (
        <div className="space-y-2 rounded-lg border border-primary/30 bg-primary/5 p-3">
          <p className="text-xs text-muted-foreground">{t("apps.stackHelp")}</p>
          {stackError != null && <ErrorDisplay error={stackError} compact />}
          <Button type="button" size="sm" disabled={busy} onClick={onCreateStack}>
            {creatingStack ? <Spinner /> : <LayersIcon />}
            {t("apps.stackCreate", { count: services.length })}
          </Button>
        </div>
      )}
      <p className="text-sm font-medium">{t("apps.composeFound", { count: services.length })}</p>
      <div className="grid gap-2">
        {services.map((service) => {
          const active = service.name === chosen
          const port = service.ports?.[0] ?? service.expose?.[0]
          const details = [
            service.image ? service.image : t("apps.composeBuilt"),
            port ? t("apps.composePort", { port }) : "",
            service.environment && Object.keys(service.environment).length > 0
              ? t("apps.composeVariables", { count: Object.keys(service.environment).length })
              : "",
          ].filter(Boolean)

          return (
            <Button
              key={service.name}
              type="button"
              variant="outline"
              aria-pressed={active}
              onClick={() => onChoose(service)}
              className={cn(
                "h-auto w-full flex-col items-start gap-0.5 rounded-lg p-3 text-left font-normal whitespace-normal",
                active && "border-primary bg-primary/5 hover:bg-primary/10",
              )}
            >
              <span className="text-sm font-medium">{service.name}</span>
              <span className="text-xs text-muted-foreground">{details.join(" · ")}</span>
              {service.unsupported?.map((note) => (
                <span key={note} className="mt-1 block text-xs text-muted-foreground">
                  {composeNoteText(t, note)}
                </span>
              ))}
            </Button>
          )
        })}
      </div>
      {ownWarnings.length > 0 && (
        <ul className="space-y-0.5 text-xs text-muted-foreground">
          {ownWarnings.map((warning) => (
            <li key={warning}>{composeNoteText(t, warning)}</li>
          ))}
        </ul>
      )}
      {chosen !== "" && <p className="text-xs text-muted-foreground">{t("apps.composeRest")}</p>}
    </div>
  )
}

/**
 * What creating the one Compose service picked will make, said before it is.
 *
 * The panel's own reading of the file decides, the same one the stack uses, so
 * this says what that reading does rather than what the form's fields would:
 * whether it gets an address, and which of its variables arrive. A name a
 * container here cannot be given is shown as left out, rather than found after
 * the app exists.
 */
function ComposeServicePlan({ service }: { service: ComposeService }) {
  const { t } = useTranslation()
  const keys = Object.keys(service.environment ?? {}).sort()
  const usable = keys.filter((key) => usableVariableName(key))
  const leftOut = keys.filter((key) => !usableVariableName(key))
  // Compose publishes a service to the host only through ports:, which is
  // what makes one public here too.
  const reachable = (service.ports?.length ?? 0) > 0

  return (
    <div className="space-y-2 rounded-lg border p-3 text-sm">
      <p className="font-medium">{t("apps.composePlanTitle", { name: service.name })}</p>
      <p className="text-xs text-muted-foreground">{t("apps.composePlanHelp")}</p>
      <p className="text-xs text-muted-foreground">
        {reachable ? t("apps.composePlanPublic") : t("apps.composePlanInternal")}
      </p>
      {usable.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="text-muted-foreground">{t("apps.composePlanVariables")}</span>
          {usable.map((key) => (
            <Badge key={key} variant="outline" className="font-mono">
              {key}
            </Badge>
          ))}
        </div>
      )}
      {leftOut.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="text-warning">{t("apps.composePlanLeftOut")}</span>
          {leftOut.map((key) => (
            <Badge key={key} variant="outline" className="font-mono line-through">
              {key}
            </Badge>
          ))}
        </div>
      )}
    </div>
  )
}

/** The Compose keys the reader names at the start of a note about them. */
const COMPOSE_NOTE_KEYS = [
  "network_mode",
  "privileged",
  "cap_add",
  "devices",
  "pid",
  "extends",
  "restart",
]

/**
 * A sentence from the panel's Compose reader, in the reader's language.
 *
 * The server sends these as English sentences without a code, each about the
 * Compose key it names first, so that key is what is looked up. A sentence
 * this build has no words for is shown as the server wrote it.
 */
function composeNoteText(t: TFunction, note: string): string {
  const first = note.split(" ", 1)[0]
  if (COMPOSE_NOTE_KEYS.includes(first)) {
    return t(`apps.composeNote.${first}`, { defaultValue: note })
  }
  if (note.startsWith("no port is published")) {
    return t("apps.composeNote.no_port", { defaultValue: note })
  }
  const skipped = /^The service (.+) could not be read and was skipped\.$/.exec(note)
  if (skipped) return t("apps.composeNote.skipped", { name: skipped[1], defaultValue: note })
  return note
}

/**
 * A problem as a toast has room for it: what went wrong and what to do, in the
 * reader's language, looked up the way ErrorDisplay looks it up. A toast is
 * where these go because the page they happened on has already been left.
 */
function problemLine(t: TFunction, problem: Problem | null) {
  if (!problem) return undefined
  const key = `errors.catalogue.${problem.code.replaceAll(".", "_")}`
  const fix = translated(t, `${key}.fix`, problem.fix, problem.args?.fix)
  return (
    <>
      <span className="block">
        {translated(t, `${key}.title`, problem.title, problem.args?.title)}
      </span>
      {fix && <span className="block">{fix}</span>}
    </>
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
          {detection.notes.map((note, index) => {
            // The code is what is translated; the English is the fallback
            // for a note this build of the interface has no words for.
            const coded = detection.note_codes?.[index]
            return (
              <li key={note}>
                {coded
                  ? t(`detect.note.${coded.code}`, { ...coded.params, defaultValue: note })
                  : note}
              </li>
            )
          })}
        </ul>
      )}
      {detection.truncated && (
        <p className="mt-1.5 text-xs text-muted-foreground">{t("apps.detectedPartly")}</p>
      )}
      {/*
        The versions platforms refused to deploy. Said here, before anything
        exists, and again in the build log of every deploy that installs one.
      */}
      {detection.advisories?.map((advisory) => (
        <Alert
          key={`${advisory.package}-${advisory.version}-${advisory.id}`}
          variant="warning"
          className="mt-3"
        >
          <ShieldAlertIcon />
          <AlertTitle>
            {t("apps.advisoryTitle", { name: advisory.package, version: advisory.version })}
          </AlertTitle>
          <AlertDescription>
            <p>{t("apps.advisoryBody", { id: advisory.id, fixed: advisory.fixed_in })}</p>
            <a href={advisory.url} target="_blank" rel="noreferrer" className="underline">
              {t("apps.advisoryLink")}
            </a>
          </AlertDescription>
        </Alert>
      ))}
    </div>
  )
}
