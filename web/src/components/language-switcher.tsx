import { useTranslation } from "react-i18next"
import { CheckIcon, LanguagesIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { api } from "@/lib/api"
import { LANGUAGES, currentLanguage, setLanguage, type LanguageCode } from "@/lib/i18n"

/**
 * The language switcher.
 *
 * The choice is stored in the browser straight away, and saved to the account
 * in the background so it follows the user to another machine. A failure to
 * save must not undo the change the user just made.
 */
export function LanguageSwitcher({ signedIn }: { signedIn?: boolean }) {
  const { t } = useTranslation()
  const active = currentLanguage()

  const choose = (code: LanguageCode) => {
    setLanguage(code)
    if (signedIn) {
      void api.patch("/api/me", { locale: code }).catch(() => {
        // The panel may be unreachable. The language still changed here.
      })
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("nav.language")}
          data-slot="language-switcher"
        >
          <LanguagesIcon className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>{t("nav.language")}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {LANGUAGES.map((language) => (
          <DropdownMenuItem
            key={language.code}
            onSelect={() => choose(language.code)}
            className="justify-between"
            // The name is in its own language, so the label's lang attribute
            // has to match or the browser picks the wrong font for it.
            lang={language.code}
          >
            <span>{language.name}</span>
            {active === language.code && <CheckIcon className="size-4" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
