import { useTranslation } from "react-i18next"
import { cn } from "cn"
import { Loader2Icon } from "lucide-react"

/**
 * A spinner announces itself, in the reader's own language.
 *
 * The label was hardcoded English. Nobody sighted ever sees it, which is
 * exactly why it stayed that way in a panel that ships five languages.
 */
function Spinner({ className, label, ...props }: React.ComponentProps<"svg"> & { label?: string }) {
  const { t } = useTranslation()
  return (
    <Loader2Icon
      role="status"
      aria-label={label ?? t("common.loading")}
      className={cn("size-4 animate-spin", className)}
      {...props}
    />
  )
}

export { Spinner }
