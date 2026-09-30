import type { LucideIcon } from "lucide-react"
import { cn } from "cn"

import { Skeleton } from "@/components/ui/skeleton"

/**
 * The frame every page sits in.
 *
 * Before this each page repeated its own `mx-auto max-w-6xl` and its own header
 * markup, which drifted: different titles were different sizes, some pages had
 * a description and some did not, and the actions sat in a different place on
 * each one. One component means the panel looks like one product.
 *
 * `w-full` matters more than it looks: the shell's main element is a flex
 * column, and an auto-margined child in a flex column shrinks to its content
 * instead of filling the row.
 */
export function Page({
  children,
  className,
  width = "wide",
}: {
  children: React.ReactNode
  className?: string
  /** "wide" for tables and dashboards, "narrow" for forms and reading. */
  width?: "wide" | "narrow" | "full"
}) {
  return (
    <div
      className={cn(
        "mx-auto w-full space-y-6",
        width === "wide" && "max-w-6xl",
        width === "narrow" && "max-w-2xl",
        className,
      )}
    >
      {children}
    </div>
  )
}

/**
 * A page's title row.
 *
 * The description is not decoration: on a panel that hides Kubernetes, the line
 * under the title is often the only place the underlying idea is named.
 */
export function PageHeader({
  title,
  description,
  icon: Icon,
  actions,
  badge,
  back,
  loading,
}: {
  title?: string
  /** A node rather than a string: a page whose subtitle is where it lives —
      "Storefront · production" — needs a link inside it. */
  description?: React.ReactNode
  icon?: LucideIcon
  actions?: React.ReactNode
  /** A status or count that belongs next to the title rather than below it. */
  badge?: React.ReactNode
  /** A "back to the list" link, rendered above the title. */
  back?: React.ReactNode
  loading?: boolean
}) {
  return (
    <div className="space-y-2">
      {back}
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div className="min-w-0 space-y-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2.5">
            {Icon && <Icon className="size-5 shrink-0 text-muted-foreground" />}
            {loading ? (
              <Skeleton className="h-8 w-56" />
            ) : (
              <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
            )}
            {badge}
          </div>
          {description && (
            <p className="max-w-2xl text-sm text-balance text-muted-foreground">{description}</p>
          )}
        </div>
        {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  )
}

/** A titled block within a page, for pages that are a stack of sections. */
export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn("space-y-3", className)}>
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="space-y-0.5">
          <h2 className="text-base font-medium">{title}</h2>
          {description && <p className="text-sm text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  )
}
