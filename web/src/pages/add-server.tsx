import { useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { ArrowLeftIcon, ChevronRightIcon, InfoIcon, KeyRoundIcon, LockIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Page, PageHeader } from "@/components/page"
import { OperationProgress } from "@/components/operation-progress"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { useEvents } from "@/hooks/use-events"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import type { Operation } from "@/lib/types"
import { Spinner } from "@/components/ui/spinner"

/**
 * Adding a server.
 *
 * The form asks for an address and one way to sign in. Everything else, from
 * the firewall to Kubernetes, happens on the other side of the button, and the
 * user watches it happen step by step.
 */
export function AddServerPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { team } = useSession()

  const [name, setName] = useState("")
  const [host, setHost] = useState("")
  const [sshPort, setSSHPort] = useState("22")
  const [sshUser, setSSHUser] = useState("root")
  const [authMethod, setAuthMethod] = useState<"password" | "key">("password")
  const [password, setPassword] = useState("")
  const [privateKey, setPrivateKey] = useState("")
  const [passphrase, setPassphrase] = useState("")
  const [location, setLocation] = useState("")
  const [controlPlane, setControlPlane] = useState(false)
  const [operationId, setOperationId] = useState<string | null>(null)
  const [logLines, setLogLines] = useState<string[]>([])

  const add = useMutation({
    mutationFn: () =>
      api.post<Operation>(`/api/teams/${team!.id}/servers`, {
        name: name.trim() || host.trim(),
        host: host.trim(),
        ssh_port: Number(sshPort) || 22,
        ssh_user: sshUser.trim() || "root",
        password: authMethod === "password" ? password : undefined,
        private_key: authMethod === "key" ? privateKey : undefined,
        passphrase: authMethod === "key" && passphrase ? passphrase : undefined,
        location: location.trim() || undefined,
        control_plane: controlPlane,
      }),
    onSuccess: (operation) => {
      setOperationId(operation.id)
      // The password only ever existed in this form; drop it as soon as the
      // request is away.
      setPassword("")
      setPrivateKey("")
      setPassphrase("")
    },
  })

  if (operationId) {
    return (
      <ProvisioningView
        operationId={operationId}
        serverName={name || host}
        logLines={logLines}
        onLogLine={(line) => setLogLines((previous) => [...previous.slice(-400), line])}
        onDone={(targetId) => navigate(`/servers/${targetId}`)}
      />
    )
  }

  return (
    <Page width="narrow">
      <PageHeader
        back={
          <Button variant="ghost" size="sm" asChild className="-ml-2">
            <Link to="/servers">
              <ArrowLeftIcon />
              {t("servers.title")}
            </Link>
          </Button>
        }
        title={t("servers.addServer")}
        description={t("servers.addServerIntro", { product: "Skifity" })}
      />

      <Card>
        <CardContent className="pt-6">
          <form
            className="space-y-5"
            onSubmit={(event) => {
              event.preventDefault()
              add.mutate()
            }}
          >
            {add.error && <ErrorDisplay error={add.error} />}

            <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
              <Field>
                <FieldLabel htmlFor="host">{t("servers.host")}</FieldLabel>
                <Input
                  id="host"
                  value={host}
                  onChange={(event) => setHost(event.target.value)}
                  placeholder={t("servers.hostPlaceholder")}
                  required
                  autoFocus
                  className="font-mono"
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="port">{t("servers.sshPort")}</FieldLabel>
                <Input
                  id="port"
                  value={sshPort}
                  onChange={(event) => setSSHPort(event.target.value)}
                  inputMode="numeric"
                />
              </Field>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="user">{t("servers.sshUser")}</FieldLabel>
                <Input
                  id="user"
                  value={sshUser}
                  onChange={(event) => setSSHUser(event.target.value)}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="name">
                  {t("servers.serverName")}{" "}
                  <span className="text-muted-foreground">({t("common.optional")})</span>
                </FieldLabel>
                <Input
                  id="name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  placeholder={host || t("servers.serverNameExample")}
                />
              </Field>
            </div>

            <Tabs
              value={authMethod}
              onValueChange={(value) => setAuthMethod(value as "password" | "key")}
            >
              <TabsList className="w-full">
                <TabsTrigger value="password" className="flex-1">
                  <LockIcon className="size-3.5" />
                  {t("servers.authPassword")}
                </TabsTrigger>
                <TabsTrigger value="key" className="flex-1">
                  <KeyRoundIcon className="size-3.5" />
                  {t("servers.authKey")}
                </TabsTrigger>
              </TabsList>

              <TabsContent value="password" className="pt-4">
                <Field>
                  <FieldLabel htmlFor="password">{t("servers.authPassword")}</FieldLabel>
                  <Input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    autoComplete="off"
                    required={authMethod === "password"}
                  />
                  <FieldDescription className="flex items-start gap-1.5">
                    <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
                    {t("servers.passwordNotStored", { product: "Skifity" })}
                  </FieldDescription>
                </Field>
              </TabsContent>

              <TabsContent value="key" className="space-y-4 pt-4">
                <Field>
                  <FieldLabel htmlFor="key">{t("servers.authKey")}</FieldLabel>
                  <Textarea
                    id="key"
                    value={privateKey}
                    onChange={(event) => setPrivateKey(event.target.value)}
                    placeholder={t("servers.authKeyPlaceholder")}
                    rows={6}
                    className="font-mono text-xs"
                    required={authMethod === "key"}
                  />
                </Field>
                <Field>
                  <FieldLabel htmlFor="passphrase">
                    {t("servers.passphrase")}{" "}
                    <span className="text-muted-foreground">({t("common.optional")})</span>
                  </FieldLabel>
                  <Input
                    id="passphrase"
                    type="password"
                    value={passphrase}
                    onChange={(event) => setPassphrase(event.target.value)}
                    autoComplete="off"
                  />
                </Field>
              </TabsContent>
            </Tabs>

            <Collapsible className="rounded-md border">
              <CollapsibleTrigger className="group/advanced flex w-full items-center gap-2 p-3 text-sm font-medium">
                <ChevronRightIcon className="size-4 transition-transform group-data-[state=open]/advanced:rotate-90" />
                {t("common.showAdvanced")}
              </CollapsibleTrigger>
              <CollapsibleContent>
                <FieldGroup className="gap-4 border-t p-4">
                  <Field>
                    <FieldLabel htmlFor="location">{t("servers.location")}</FieldLabel>
                    <Input
                      id="location"
                      value={location}
                      onChange={(event) => setLocation(event.target.value)}
                      placeholder={t("servers.locationExample")}
                    />
                    <FieldDescription>{t("servers.locationHelp")}</FieldDescription>
                  </Field>
                  <Field orientation="horizontal">
                    <FieldContent>
                      <FieldTitle>{t("servers.controlPlane")}</FieldTitle>
                      <FieldDescription>{t("servers.controlPlaneHelp")}</FieldDescription>
                    </FieldContent>
                    <Switch
                      id="control-plane"
                      checked={controlPlane}
                      onCheckedChange={setControlPlane}
                    />
                  </Field>
                </FieldGroup>
              </CollapsibleContent>
            </Collapsible>

            <Alert>
              <InfoIcon />
              <AlertTitle>{t("servers.requirements")}</AlertTitle>
              <AlertDescription>{t("servers.requirementsList")}</AlertDescription>
            </Alert>

            <Button type="submit" className="w-full" disabled={add.isPending}>
              {add.isPending && <Spinner />}
              {add.isPending ? t("common.loading") : t("servers.addServer")}
            </Button>
          </form>
        </CardContent>
      </Card>
    </Page>
  )
}

/** Watches an add-server operation to completion. */
function ProvisioningView({
  operationId,
  serverName,
  logLines,
  onLogLine,
  onDone,
}: {
  operationId: string
  serverName: string
  logLines: string[]
  onLogLine: (line: string) => void
  onDone: (serverId: string) => void
}) {
  const { t } = useTranslation()

  const operation = useQuery({
    queryKey: ["operation", operationId],
    queryFn: () => api.get<Operation>(`/api/operations/${operationId}`),
    // The event stream drives updates; this poll is the safety net for a
    // dropped connection.
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === "running" || status === "pending" ? 5_000 : false
    },
  })

  useEvents(
    [`operation:${operationId}`],
    {
      // "operation" carries the whole operation, steps included, so every
      // change to a step arrives on it. There was a "step" handler here too,
      // listening for an event nothing has ever published.
      operation: () => void operation.refetch(),
      failed: () => void operation.refetch(),
      log: (data) => {
        const line = (data as { line?: string }).line
        if (line) onLogLine(line)
      },
    },
    true,
  )

  const retry = useMutation({
    mutationFn: () => api.post(`/api/servers/${operation.data!.target_id}/retry`),
    onSuccess: () => void operation.refetch(),
  })

  const cancel = useMutation({
    mutationFn: () => api.post(`/api/operations/${operationId}/cancel`),
    onSuccess: () => void operation.refetch(),
  })

  const status = operation.data?.status
  const finished = status === "succeeded"
  const failed = status === "failed" || status === "cancelled"

  return (
    <Page width="narrow">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          {t("servers.progress", { name: serverName })}
        </h1>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("servers.addServer")}</CardTitle>
          {operation.data?.error_message && (
            <CardDescription className="text-destructive">
              {operation.data.error_message}
            </CardDescription>
          )}
        </CardHeader>
        <CardContent className="space-y-4">
          {operation.data && <OperationProgress operation={operation.data} logs={logLines} />}

          <div className="flex flex-wrap gap-2 border-t pt-4">
            {finished && (
              <Button onClick={() => onDone(operation.data!.target_id)}>{t("common.done")}</Button>
            )}
            {failed && (
              <>
                <Button onClick={() => retry.mutate()} disabled={retry.isPending}>
                  {t("common.retry")}
                </Button>
                <Button variant="outline" asChild>
                  <Link to="/servers">{t("common.back")}</Link>
                </Button>
              </>
            )}
            {!finished && !failed && (
              <Button variant="outline" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
                {t("servers.cancelAndClean")}
              </Button>
            )}
          </div>
        </CardContent>
      </Card>
    </Page>
  )
}
