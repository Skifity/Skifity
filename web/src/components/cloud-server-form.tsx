import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import { CloudIcon, InfoIcon, ShieldCheckIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { CloudProviderForm, useCloudProviders } from "@/components/settings/cloud-providers"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field"
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
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useSession } from "@/hooks/use-session"
import { api } from "@/lib/api"
import type { CloudOptions, CloudServerType, Operation } from "@/lib/types"

type Arch = "amd64" | "arm64"

/** A name Hetzner, a hostname and a Kubernetes node all accept. */
const NAME = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/

/**
 * Creating a server at Hetzner Cloud.
 *
 * Everything offered comes from what the provider sells now, read with the
 * team's token: the locations, the server types that can be ordered in the one
 * picked, with their monthly price there, and the images for their processor.
 * What is chosen is checked again by the panel before anything is ordered.
 */
export function CloudServerForm({
  onStarted,
}: {
  onStarted: (operation: Operation, name: string) => void
}) {
  const { t, i18n } = useTranslation()
  const { team } = useSession()
  const providers = useCloudProviders()
  const [picked, setPicked] = useState<string | null>(null)
  const [location, setLocation] = useState<string | null>(null)
  const [arch, setArch] = useState<Arch>("amd64")
  const [serverType, setServerType] = useState<string | null>(null)
  const [image, setImage] = useState<string | null>(null)
  const [name, setName] = useState("")
  const [sshFromCluster, setSSHFromCluster] = useState(false)
  const [controlPlane, setControlPlane] = useState(false)

  const connections = providers.data?.items ?? []
  // Derived, not synchronised: the one picked while it still exists, or the
  // first.
  const connection = connections.find((item) => item.id === picked) ?? connections[0]

  const options = useQuery({
    queryKey: ["cloud-options", team?.id, connection?.id],
    queryFn: () =>
      api.get<CloudOptions>(`/api/teams/${team?.id}/cloud-providers/${connection?.id}/options`),
    enabled: Boolean(team && connection),
    staleTime: 5 * 60_000,
  })

  const catalogue = options.data
  const effectiveLocation =
    catalogue?.locations.find((item) => item.name === location)?.name ??
    catalogue?.locations[0]?.name ??
    ""
  const typesHere = (catalogue?.server_types ?? [])
    .filter((item) => item.arch === arch && item.locations.includes(effectiveLocation))
    .sort((a, b) => monthly(a, effectiveLocation) - monthly(b, effectiveLocation))
  const chosenType = typesHere.find((item) => item.name === serverType) ?? typesHere[0]
  const imagesForArch = (catalogue?.images ?? []).filter((item) => item.arch === arch)
  const chosenImage =
    imagesForArch.find((item) => item.name === image)?.name ??
    imagesForArch.find((item) => item.name === catalogue?.default_image)?.name ??
    imagesForArch[0]?.name ??
    ""
  const trimmedName = name.trim().toLowerCase()
  const nameValid = NAME.test(trimmedName)

  const price = (type: CloudServerType | undefined) => {
    const entry = type?.prices.find((item) => item.location === effectiveLocation)
    if (!entry) return ""
    return new Intl.NumberFormat(i18n.language, {
      style: "currency",
      currency: entry.currency,
    }).format(Number(entry.monthly))
  }

  const create = useMutation({
    mutationFn: () =>
      api.post<Operation>(`/api/teams/${team?.id}/servers/cloud`, {
        provider_id: connection?.id,
        name: trimmedName,
        location: effectiveLocation,
        server_type: chosenType?.name,
        image: chosenImage,
        ssh_access: sshFromCluster ? "cluster" : "anywhere",
        control_plane: controlPlane,
      }),
    onSuccess: (operation) => onStarted(operation, trimmedName),
  })

  if (providers.isLoading) return <Skeleton className="h-40" />
  if (providers.error)
    return <ErrorDisplay error={providers.error} onRetry={() => void providers.refetch()} />
  if (!connection) {
    return (
      <div className="space-y-4">
        <Alert>
          <CloudIcon />
          <AlertDescription>{t("servers.cloud.noConnection")}</AlertDescription>
        </Alert>
        <CloudProviderForm onSaved={(provider) => setPicked(provider.id)} />
      </div>
    )
  }

  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault()
        create.mutate()
      }}
    >
      <p className="text-sm text-muted-foreground">{t("servers.cloud.intro")}</p>
      {create.error && <ErrorDisplay error={create.error} />}

      {connections.length > 1 && (
        <Field>
          <FieldLabel htmlFor="cloud-connection">{t("servers.cloud.connection")}</FieldLabel>
          <Select value={connection.id} onValueChange={setPicked}>
            <SelectTrigger id="cloud-connection">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {connections.map((item) => (
                <SelectItem key={item.id} value={item.id}>
                  {item.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}

      {options.isLoading ? (
        <Skeleton className="h-48" />
      ) : options.error ? (
        <ErrorDisplay error={options.error} onRetry={() => void options.refetch()} />
      ) : (
        catalogue && (
          <FieldGroup className="gap-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="cloud-location">{t("servers.cloud.location")}</FieldLabel>
                <Select value={effectiveLocation} onValueChange={setLocation}>
                  <SelectTrigger id="cloud-location">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {catalogue.locations.map((item) => (
                      <SelectItem key={item.name} value={item.name}>
                        {item.city} ({item.country}) · {item.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldTitle>{t("servers.cloud.arch")}</FieldTitle>
                <Tabs value={arch} onValueChange={(value) => setArch(value as Arch)}>
                  <TabsList className="w-full">
                    <TabsTrigger value="amd64" className="flex-1">
                      {t("servers.cloud.archAmd64")}
                    </TabsTrigger>
                    <TabsTrigger value="arm64" className="flex-1">
                      {t("servers.cloud.archArm64")}
                    </TabsTrigger>
                  </TabsList>
                </Tabs>
              </Field>
            </div>

            <Field>
              <FieldLabel htmlFor="cloud-type">{t("servers.cloud.serverType")}</FieldLabel>
              {typesHere.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("servers.cloud.noTypes")}</p>
              ) : (
                <Select value={chosenType?.name ?? ""} onValueChange={setServerType}>
                  <SelectTrigger id="cloud-type">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {typesHere.map((item) => (
                      <SelectItem key={item.name} value={item.name}>
                        <span className="font-mono">{item.name}</span>
                        <span className="text-muted-foreground">
                          {t("servers.cloud.typeSpec", {
                            cores: item.cores,
                            memory: item.memory_gb,
                            disk: item.disk_gb,
                          })}
                          {item.cpu_type === "dedicated"
                            ? ` · ${t("servers.cloud.dedicated")}`
                            : ""}
                        </span>
                        <span>{t("servers.cloud.perMonth", { price: price(item) })}</span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>

            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="cloud-image">{t("servers.cloud.image")}</FieldLabel>
                <Select value={chosenImage} onValueChange={setImage}>
                  <SelectTrigger id="cloud-image">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {imagesForArch.map((item) => (
                      <SelectItem key={item.name} value={item.name}>
                        {item.description}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="cloud-server-name">{t("servers.cloud.name")}</FieldLabel>
                <Input
                  id="cloud-server-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  placeholder={t("servers.serverNameExample")}
                  className="font-mono"
                  aria-invalid={name !== "" && !nameValid}
                  required
                />
                <FieldDescription>{t("servers.cloud.nameHelp")}</FieldDescription>
              </Field>
            </div>

            <Field orientation="horizontal">
              <FieldContent>
                <FieldTitle>{t("servers.cloud.sshCluster")}</FieldTitle>
                <FieldDescription>{t("servers.cloud.sshClusterHelp")}</FieldDescription>
              </FieldContent>
              <Switch
                id="cloud-ssh-cluster"
                checked={sshFromCluster}
                onCheckedChange={setSSHFromCluster}
              />
            </Field>
            <Field orientation="horizontal">
              <FieldContent>
                <FieldTitle>{t("servers.controlPlane")}</FieldTitle>
                <FieldDescription>{t("servers.controlPlaneHelp")}</FieldDescription>
              </FieldContent>
              <Switch
                id="cloud-control-plane"
                checked={controlPlane}
                onCheckedChange={setControlPlane}
              />
            </Field>
          </FieldGroup>
        )
      )}

      <Alert>
        <ShieldCheckIcon />
        <AlertDescription>{t("servers.cloud.firewall")}</AlertDescription>
      </Alert>
      {chosenType && (
        <Alert>
          <InfoIcon />
          <AlertDescription>
            {t("servers.cloud.cost", { price: price(chosenType), provider: connection.title })}
          </AlertDescription>
        </Alert>
      )}

      <Button
        type="submit"
        className="w-full"
        disabled={create.isPending || !chosenType || !chosenImage || !nameValid}
      >
        {create.isPending && <Spinner />}
        {t("servers.cloud.create")}
      </Button>
    </form>
  )
}

/** A type's monthly price in a location, for sorting; unknown sorts last. */
function monthly(type: CloudServerType, location: string) {
  const entry = type.prices.find((item) => item.location === location)
  return entry ? Number(entry.monthly) : Number.POSITIVE_INFINITY
}
