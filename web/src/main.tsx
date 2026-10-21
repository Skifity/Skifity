import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router-dom"
import { QueryClientProvider } from "@tanstack/react-query"

import { App } from "@/App"
import { ConfirmProvider } from "@/components/confirm-dialog"
import { ReauthProvider } from "@/components/reauth-dialog"
import { ErrorBoundary } from "@/components/error-boundary"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { SessionProvider, saveThemeToAccount } from "@/hooks/use-session"
import { queryClient } from "@/lib/query"
import { ThemeProvider } from "@/lib/theme"
import "@/lib/i18n"
import "./index.css"

/** When this tab last reloaded for a missing chunk, so it never loops. */
const RELOADED_AT_KEY = "skifity-reloaded-for-chunk"

/**
 * A tab opened before an upgrade asks for the pages it loads on demand by the
 * old build's file names, and the upgrade deleted those files. The panel
 * answers 404, and React keeps the failed import, so the error screen's Retry
 * could only fail again. Loading the page anew fetches the new build.
 *
 * Once: a reload within a few seconds of the last one means the file is
 * missing from the new build too, and the error screen says so rather than
 * the tab reloading for ever. Without storage there is no way to tell, so no
 * reload either.
 */
window.addEventListener("vite:preloadError", (event) => {
  try {
    const last = Number(sessionStorage.getItem(RELOADED_AT_KEY) ?? 0)
    if (Date.now() - last < 10_000) return
    sessionStorage.setItem(RELOADED_AT_KEY, String(Date.now()))
  } catch {
    return
  }
  event.preventDefault()
  window.location.reload()
})

const container = document.getElementById("root")
if (!container) throw new Error("the page has no #root element to mount into")

createRoot(container).render(
  <StrictMode>
    <ThemeProvider onChange={saveThemeToAccount}>
      <QueryClientProvider client={queryClient}>
        <SessionProvider>
          <TooltipProvider delayDuration={300}>
            <ConfirmProvider>
              <ReauthProvider>
                <BrowserRouter>
                  {/*
                  The outer boundary, for the screens that are not inside the
                  shell: sign-in, first-run setup, and the shell itself. The
                  shell has its own, so an ordinary page that throws does not
                  take the navigation with it.
                */}
                  <ErrorBoundary>
                    <App />
                  </ErrorBoundary>
                </BrowserRouter>
              </ReauthProvider>
            </ConfirmProvider>
            <Toaster />
          </TooltipProvider>
        </SessionProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
