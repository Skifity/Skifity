import { Component, type ErrorInfo, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { RefreshCwIcon, TriangleAlertIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"

/**
 * Catches a render that threw, so one broken screen is not the whole panel.
 *
 * React unmounts the entire tree when a render throws and nothing catches it,
 * and what is left is a white page with a message only the browser console
 * knows about. On a self-hosted panel that is the worst possible failure: the
 * cluster is fine, the apps are serving, and the operator has no way to tell.
 *
 * There is no hook for this — an error boundary has to be a class — so this is
 * the one class component in the panel.
 *
 * `resetKey` is how a boundary recovers. Without it a screen that threw once
 * stays broken until the tab is reloaded, including after navigating somewhere
 * else entirely, because the boundary has no reason to try again. The shell
 * passes the current path, so leaving the broken page is enough.
 */
export class ErrorBoundary extends Component<
  { children: ReactNode; resetKey?: string; onReset?: () => void },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidUpdate(previous: { resetKey?: string }) {
    if (this.state.error && previous.resetKey !== this.props.resetKey) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The console is where a developer looks, and the component stack is the
    // part that says which screen it was. Nothing is sent anywhere: a
    // self-hosted panel reports to nobody.
    console.error("A screen in the panel failed to render.", error, info.componentStack)
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <ErrorScreen
        error={this.state.error}
        onReset={() => {
          this.setState({ error: null })
          this.props.onReset?.()
        }}
      />
    )
  }
}

function ErrorScreen({ error, onReset }: { error: Error; onReset: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex min-h-[60vh] items-center justify-center p-6">
      <Empty className="max-w-lg rounded-lg border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <TriangleAlertIcon />
          </EmptyMedia>
          <EmptyTitle>{t("errors.crashTitle")}</EmptyTitle>
          <EmptyDescription>{t("errors.crashHelp")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent className="w-full">
          <div className="flex flex-wrap justify-center gap-2">
            <Button size="sm" onClick={onReset}>
              <RefreshCwIcon className="size-3.5" />
              {t("common.retry")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => window.location.reload()}>
              {t("errors.reload")}
            </Button>
          </div>
          {error.message && (
            <Collapsible className="mt-4 w-full text-left text-xs">
              <CollapsibleTrigger className="text-muted-foreground hover:text-foreground">
                {t("errors.details")}
              </CollapsibleTrigger>
              <CollapsibleContent>
                <pre className="log-output mt-2 max-h-48 overflow-auto rounded-md bg-muted/50 p-3 whitespace-pre-wrap">
                  {error.message}
                </pre>
              </CollapsibleContent>
            </Collapsible>
          )}
        </EmptyContent>
      </Empty>
    </div>
  )
}
