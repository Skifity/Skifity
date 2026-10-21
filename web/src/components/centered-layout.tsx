import { LanguageSwitcher } from "@/components/language-switcher"
import { ThemeToggle } from "@/components/theme-toggle"

/**
 * The frame of every screen outside the shell: first-run setup, sign-in, an
 * invitation, a password reset, and the panel failing to start. The language
 * and the theme can be changed before anybody has signed in.
 */
export function CenteredLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-svh flex-col bg-muted/30">
      <div className="flex justify-end gap-1 p-4">
        <LanguageSwitcher />
        <ThemeToggle />
      </div>
      <main className="flex flex-1 items-center justify-center px-4 pb-16">{children}</main>
    </div>
  )
}
