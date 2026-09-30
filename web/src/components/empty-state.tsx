import type { LucideIcon } from "lucide-react"
import { cn } from "cn"

import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"

/**
 * An empty state always shows the next step.
 *
 * A page that says "no apps" and nothing else leaves the user to work out what
 * to do, which is exactly the moment a first-time user gives up.
 *
 * This wraps shadcn's Empty so every empty state in the panel is the same
 * shape, and so improving one improves all of them.
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
  bordered = true,
}: {
  icon?: LucideIcon
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
  /** Off when the empty state already sits inside a card or a table. */
  bordered?: boolean
}) {
  return (
    <Empty className={cn(bordered && "rounded-lg border border-dashed", className)}>
      <EmptyHeader>
        {Icon && (
          <EmptyMedia variant="icon">
            <Icon />
          </EmptyMedia>
        )}
        <EmptyTitle>{title}</EmptyTitle>
        {description && <EmptyDescription>{description}</EmptyDescription>}
      </EmptyHeader>
      {action && <EmptyContent>{action}</EmptyContent>}
    </Empty>
  )
}
