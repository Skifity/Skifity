import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ChevronRightIcon, FolderUpIcon, TerminalIcon } from "lucide-react"
import { toast } from "sonner"

import { ErrorDisplay } from "@/components/error-display"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import {
  FolderEmpty,
  FolderTooLarge,
  folderLimits,
  packFolder,
  type PackedFolder,
} from "@/lib/folder"
import { formatBytes } from "@/lib/format"
import { queryClient } from "@/lib/query"
import type { App, Deployment, Meta } from "@/lib/types"

/**
 * A button that opens the browser's folder picker.
 *
 * The input is the browser's own, hidden: a folder picker is the one control
 * that cannot be drawn, only asked for. `webkitdirectory` is what every current
 * browser calls it, including Firefox and Safari, and React does not know the
 * attribute, hence the spread.
 */
export function FolderPicker({
  onPicked,
  pending,
  label,
  variant = "outline",
}: {
  onPicked: (files: File[]) => void
  pending: boolean
  label: string
  variant?: "outline" | "default"
}) {
  const input = useRef<HTMLInputElement>(null)
  // The input comes first: Tailwind's space-y puts its margin on every child
  // but the last, and a hidden last child would push the button off the line
  // the buttons beside it sit on.
  return (
    <>
      <Input
        ref={input}
        type="file"
        className="hidden"
        aria-hidden
        tabIndex={-1}
        data-testid="folder-input"
        {...({ webkitdirectory: "", directory: "" } as Record<string, string>)}
        onChange={(event) => {
          // Copied out first: a FileList is live, and clearing the input
          // below empties it before anything has read it.
          const files = Array.from(event.target.files ?? [])
          if (files.length > 0) onPicked(files)
          // Picking the same folder again, after changing it, is the point of
          // "send a new version"; an input keeps its value and would not fire.
          event.target.value = ""
        }}
      />
      <Button
        type="button"
        variant={variant}
        disabled={pending}
        onClick={() => input.current?.click()}
      >
        {pending ? <Spinner /> : <FolderUpIcon />}
        {label}
      </Button>
    </>
  )
}

/** "my-app · 42 files, 180 KB · 3,408 left out". */
export function FolderSummary({ folder }: { folder: PackedFolder }) {
  const { t } = useTranslation()
  return (
    <p className="text-sm text-muted-foreground">
      <span className="font-medium text-foreground">{folder.name}</span>
      {" · "}
      {t("folder.files", { count: folder.files.length, size: formatBytes(folder.archive.size) })}
      {folder.leftOut > 0 && ` · ${t("folder.leftOut", { count: folder.leftOut })}`}
    </p>
  )
}

/**
 * Why a folder was not packed. The two failures that happen before anything
 * is sent are the browser's own, so they are said here, with the part of the
 * folder that is the problem — which the panel, never having seen it, cannot.
 */
export function FolderProblem({ error }: { error: unknown }) {
  const { t } = useTranslation()
  if (error instanceof FolderEmpty) {
    return (
      <Alert variant="destructive">
        <AlertTitle>{t("folder.emptyTitle")}</AlertTitle>
        <AlertDescription>{t("folder.empty")}</AlertDescription>
      </Alert>
    )
  }
  if (error instanceof FolderTooLarge) {
    return (
      <Alert variant="destructive">
        <AlertTitle>{t("folder.tooLargeTitle")}</AlertTitle>
        <AlertDescription>
          <p>
            {t(`folder.tooLarge.${error.kind}`, {
              entries: folderLimits.entries.toLocaleString(),
              size: formatBytes(
                error.kind === "compressed" ? folderLimits.compressed : folderLimits.unpacked,
              ),
            })}
          </p>
          {error.largest.length > 0 && (
            <ul className="mt-1 font-mono text-xs">
              {error.largest.map((part) => (
                <li key={part.name}>
                  {part.name} — {formatBytes(part.size)}
                </li>
              ))}
            </ul>
          )}
          <p className="mt-1">{t("folder.tooLargeFix")}</p>
        </AlertDescription>
      </Alert>
    )
  }
  return <ErrorDisplay error={error} compact />
}

/**
 * Sends the folder again, for an app that came from one: the panel's version
 * of running `skifity up` a second time.
 */
export function SendFolderButton({ app }: { app: App }) {
  const { t } = useTranslation()
  const send = useMutation({
    mutationFn: async (files: File[]) => {
      const folder = await packFolder(files)
      const sent = await api.upload<{ sha256: string }>(
        `/api/apps/${app.id}/source`,
        folder.archive,
      )
      return api.post<Deployment>(`/api/apps/${app.id}/deploy`, { commit_sha: sent.sha256 })
    },
    onSuccess: (deployment) => {
      void queryClient.invalidateQueries({ queryKey: ["deployments", app.id] })
      void queryClient.invalidateQueries({ queryKey: ["app-status", app.id] })
      toast.success(t("folder.sent", { number: deployment.number }))
    },
  })

  return (
    <div className="space-y-2">
      <FolderPicker
        pending={send.isPending}
        label={send.isPending ? t("folder.sending") : t("folder.sendNew")}
        onPicked={(files) => send.mutate(files)}
      />
      {send.error != null && <FolderProblem error={send.error} />}
    </div>
  )
}

/**
 * The same thing from a terminal, for whoever would rather — and for an
 * assistant, which can run a command but cannot press a button.
 *
 * The commands are for the computer this page is open on: the panel runs on
 * Linux, and a Linux binary is no use on the Mac most people deploy from. The
 * platform is guessed from the browser and can be changed; a browser cannot
 * tell an Apple silicon Mac from an Intel one, so a Mac is taken to be the
 * kind sold for the last five years.
 */
export function TerminalInstructions() {
  const { t } = useTranslation()
  const meta = useQuery({ queryKey: ["meta"], queryFn: () => api.get<Meta>("/api/meta") })
  const [chosen, setChosen] = useState<string | null>(null)
  const available = meta.data?.cli_platforms ?? (meta.data ? [meta.data.cli_platform] : [])
  const platform = chosen ?? guessPlatform(available)
  const origin = window.location.origin
  const [os, arch] = platform.split("/")
  const url = `${origin}/api/cli/download?os=${os}&arch=${arch}`
  const commands =
    os === "windows"
      ? `curl.exe -fsS "${url}" -o skifity.exe
.\\skifity.exe login --url ${origin}
.\\skifity.exe up`
      : `curl -fsS "${url}" -o skifity && chmod +x skifity
./skifity login --url ${origin}
./skifity up`

  return (
    <Collapsible className="rounded-md border">
      <CollapsibleTrigger className="group/terminal flex w-full items-center gap-2 p-3 text-sm font-medium">
        <ChevronRightIcon className="size-4 transition-transform group-data-[state=open]/terminal:rotate-90" />
        <TerminalIcon className="size-4 text-muted-foreground" />
        {t("folder.terminalTitle")}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="space-y-3 border-t p-4 text-sm">
          <p className="text-muted-foreground">{t("folder.terminalHelp")}</p>
          {available.length > 1 && (
            <Select value={platform} onValueChange={setChosen}>
              <SelectTrigger className="w-full sm:w-64" aria-label={t("folder.platform")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {available.map((p) => (
                  <SelectItem key={p} value={p}>
                    {t(`folder.platforms.${p.replace("/", "_")}`, { defaultValue: p })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <pre className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">
            {commands}
          </pre>
          <p className="text-xs text-muted-foreground">{t("folder.terminalAssistant")}</p>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

/** The platform this browser is most likely running on, among those offered. */
function guessPlatform(available: string[]): string {
  const agent = navigator.userAgent
  const os = /Windows/i.test(agent)
    ? "windows"
    : /Macintosh|Mac OS X/i.test(agent)
      ? "darwin"
      : "linux"
  const arch = os === "darwin" || /arm64|aarch64/i.test(agent) ? "arm64" : "amd64"
  const exact = `${os}/${arch}`
  if (available.includes(exact)) return exact
  return available.find((p) => p.startsWith(`${os}/`)) ?? available[0] ?? exact
}
