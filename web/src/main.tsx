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
import { SessionProvider } from "@/hooks/use-session"
import { queryClient } from "@/lib/query"
import { ThemeProvider } from "@/lib/theme"
import "@/lib/i18n"
import "./index.css"

const container = document.getElementById("root")
if (!container) throw new Error("the page has no #root element to mount into")

createRoot(container).render(
  <StrictMode>
    <ThemeProvider>
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
