import { useTranslation } from "react-i18next"

/** The shortest password the panel accepts. */
export const MIN_PASSWORD_LENGTH = 12

/**
 * A quiet hint under a new password, so the minimum is not a surprise. The
 * same one on first-run setup, an invitation and a reset: a second wording of
 * the rule is a second thing to keep in step with what the server enforces.
 */
export function PasswordStrength({ password }: { password: string }) {
  const { t } = useTranslation()
  if (!password) return null

  const tooShort = password.length < MIN_PASSWORD_LENGTH
  return (
    <p className={tooShort ? "text-xs text-warning" : "text-xs text-success"}>
      {tooShort
        ? t("auth.passwordMinLength", { min: MIN_PASSWORD_LENGTH })
        : t("auth.passwordLength", { count: password.length })}
    </p>
  )
}
