import { Link, useLocation } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { ArrowLeftIcon, CompassIcon, LayoutDashboardIcon } from "lucide-react"

import { EmptyState } from "@/components/empty-state"
import { Page } from "@/components/page"
import { Button } from "@/components/ui/button"
import { ButtonGroup } from "@/components/ui/button-group"

/**
 * A page that does not exist.
 *
 * It shows the address that was asked for, because the usual cause is a link
 * from somewhere else — a bookmark to an app that was deleted, a URL in a chat
 * — and knowing which one it was is the difference between "something broke"
 * and "ah, that app is gone".
 */
export function NotFoundPage() {
  const { t } = useTranslation()
  const { pathname } = useLocation()

  return (
    <Page width="narrow">
      <EmptyState
        className="py-16"
        icon={CompassIcon}
        title={t("errors.pageNotFound")}
        description={t("errors.pageNotFoundHelp")}
        action={
          <div className="flex flex-col items-center gap-4">
            <code className="rounded-md border bg-muted px-2 py-1 font-mono text-xs break-all text-muted-foreground">
              {pathname}
            </code>
            <ButtonGroup>
              <Button variant="outline" onClick={() => window.history.back()}>
                <ArrowLeftIcon />
                {t("common.back")}
              </Button>
              <Button asChild>
                <Link to="/">
                  <LayoutDashboardIcon />
                  {t("errors.goHome")}
                </Link>
              </Button>
            </ButtonGroup>
          </div>
        }
      />
    </Page>
  )
}
