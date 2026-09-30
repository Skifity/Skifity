import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { CheckIcon, CopyIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "cn"

/**
 * Copies one value to the clipboard.
 *
 * Three pages had written this out by hand and none of them agreed: one showed
 * a toast, one showed nothing, and the third put the word "Copy" in a button
 * wide enough to push the value it belonged to off the row. A host, an id and
 * an app's address are all the same gesture, so they are all the same control.
 */
export function CopyButton({
  value,
  label,
  className,
  variant = "ghost",
}: {
  value: string
  /** What is being copied, for the screen reader and the tooltip. */
  label: string
  className?: string
  variant?: "ghost" | "outline"
}) {
  const { t } = useTranslation()
  // A tick where the icon was, for a moment: the toast is confirmation, and
  // the icon is the same confirmation for somebody who was looking at the row
  // rather than at the corner of the screen.
  const [copied, setCopied] = useState(false)

  const copy = () => {
    void navigator.clipboard.writeText(value).then(
      () => {
        setCopied(true)
        toast.success(t("common.copied"))
        window.setTimeout(() => setCopied(false), 1500)
      },
      // Clipboard access is refused outside a secure context, and a button
      // that silently does nothing is worse than one that says why.
      () => toast.error(t("common.copyFailed")),
    )
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant={variant}
          size="icon"
          className={cn("size-8", className)}
          aria-label={t("common.copyValue", { name: label })}
          onClick={copy}
        >
          {copied ? <CheckIcon className="size-4 text-success" /> : <CopyIcon className="size-4" />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{t("common.copyValue", { name: label })}</TooltipContent>
    </Tooltip>
  )
}
