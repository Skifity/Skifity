"use client"

import * as React from "react"
import { cn } from "cn"
import { CheckIcon } from "lucide-react"
import { Checkbox as CheckboxPrimitive } from "radix-ui"

/**
 * A 16px box inside a 24px target.
 *
 * shadcn's checkbox is 16px square, which is what it should look like and not
 * what it should be to a thumb: WCAG 2.2 (AA, 2.5.8) puts the minimum at 24px.
 * So the control is 24px and the box people see is drawn inside it, with a
 * negative margin so that box lands exactly where the 16px one used to — the
 * target grew, the design did not move.
 */
function Checkbox({
  className,
  ...props
}: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer group/checkbox -m-1 inline-flex size-6 shrink-0 items-center justify-center rounded-md outline-none disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      {...props}
    >
      <span
        aria-hidden
        className="grid size-4 place-content-center rounded-[4px] border border-input shadow-xs transition-shadow group-focus-visible/checkbox:border-ring group-focus-visible/checkbox:ring-[3px] group-focus-visible/checkbox:ring-ring/50 group-aria-invalid/checkbox:border-destructive group-aria-invalid/checkbox:ring-destructive/20 group-data-[state=checked]/checkbox:border-primary group-data-[state=checked]/checkbox:bg-primary group-data-[state=checked]/checkbox:text-primary-foreground dark:bg-input/30 dark:group-aria-invalid/checkbox:ring-destructive/40 dark:group-data-[state=checked]/checkbox:bg-primary"
      >
        <CheckboxPrimitive.Indicator
          data-slot="checkbox-indicator"
          className="grid place-content-center text-current transition-none"
        >
          <CheckIcon className="size-3.5" />
        </CheckboxPrimitive.Indicator>
      </span>
    </CheckboxPrimitive.Root>
  )
}

export { Checkbox }
