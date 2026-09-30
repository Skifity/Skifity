import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { CheckIcon, ClipboardIcon } from "lucide-react"
import { toast } from "sonner"

import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import type { App } from "@/lib/types"

type Advanced = { namespace: string; name: string; manifests: string }

/**
 * The one place the panel says "Kubernetes" out loud.
 *
 * Everything else in the product is deliberately expressed in human nouns, but
 * an advanced user must never be stuck behind the abstraction: this shows the
 * exact objects the panel applies, ready to copy.
 */
export function AdvancedTab({ app }: { app: App }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  const advanced = useQuery({
    queryKey: ["advanced", app.id],
    queryFn: () => api.get<Advanced>(`/api/apps/${app.id}/advanced`),
  })

  if (advanced.isLoading) return <Skeleton className="h-96" />
  if (advanced.error) {
    return <ErrorDisplay error={advanced.error} onRetry={() => void advanced.refetch()} />
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(advanced.data?.manifests ?? "")
      setCopied(true)
      toast.success(t("common.copied"))
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error(t("errors.somethingWentWrong"))
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <CardTitle className="text-base">{t("apps.advanced")}</CardTitle>
            <p className="mt-1 font-mono text-xs text-muted-foreground">
              {advanced.data?.namespace} / {advanced.data?.name}
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={() => void copy()}>
            {copied ? <CheckIcon className="size-3.5" /> : <ClipboardIcon className="size-3.5" />}
            {copied ? t("common.copied") : t("common.copy")}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <ScrollArea className="h-[30rem] rounded-md border bg-muted/30">
          <pre className="p-3 font-mono text-xs leading-relaxed">{advanced.data?.manifests}</pre>
        </ScrollArea>
      </CardContent>
    </Card>
  )
}
