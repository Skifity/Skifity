import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"

import { Progress } from "@/components/ui/progress"
import { api } from "@/lib/api"
import type { EnvironmentQuota as Quota } from "@/lib/types"

/**
 * How much of an environment's ceiling is in use.
 *
 * Every environment has had a ResourceQuota since the day it was created and
 * nothing in the panel showed it, so the first sign of reaching one was a
 * deployment that failed with a message about a resource nobody had heard of.
 *
 * It shows nothing at all below the threshold. A row of bars at 4% is noise on
 * a page whose subject is the apps, and the limits only become information once
 * they are close.
 */
const SHOW_ABOVE_PERCENT = 60

/** The limits worth a bar, in the order somebody reads them. */
const SHOWN = ["requests.cpu", "requests.memory", "pods", "requests.storage"]

export function EnvironmentQuota({ environmentId }: { environmentId: string }) {
  const { t } = useTranslation()

  const quota = useQuery({
    queryKey: ["quota", environmentId],
    queryFn: () => api.get<Quota>(`/api/environments/${environmentId}/quota`),
    // A limit does not move between one click and the next, and this sits on a
    // page that is already making three other calls.
    staleTime: 60_000,
  })

  const items = (quota.data?.items ?? []).filter((item) => SHOWN.includes(item.resource))
  const worst = items.reduce((highest, item) => Math.max(highest, item.percent), 0)

  // A failed read says nothing rather than showing an error: this is a detail
  // beside the apps, and a cluster that cannot be reached is already reported
  // at the top of every page.
  if (!quota.data?.found || worst < SHOW_ABOVE_PERCENT) return null

  return (
    <section className="rounded-lg border p-4" aria-labelledby="quota-heading">
      <h3 id="quota-heading" className="text-sm font-medium">
        {t("projects.quota")}
      </h3>
      <p className="mt-1 text-xs text-muted-foreground">
        {worst >= 100 ? t("projects.quotaFull") : t("projects.quotaHelp")}
      </p>
      <dl className="mt-3 grid gap-3 sm:grid-cols-2">
        {items.map((item) => (
          <div key={item.resource}>
            <div className="flex items-baseline justify-between gap-2 text-xs">
              <dt className="text-muted-foreground">
                {t(`projects.quotaResource.${item.resource}`, { defaultValue: item.resource })}
              </dt>
              <dd className="font-mono tabular-nums">
                {item.used} / {item.hard}
              </dd>
            </div>
            <Progress
              value={item.percent}
              className="mt-1.5 h-1.5"
              aria-label={t(`projects.quotaResource.${item.resource}`, {
                defaultValue: item.resource,
              })}
            />
          </div>
        ))}
      </dl>
    </section>
  )
}
