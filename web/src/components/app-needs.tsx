import { useTranslation } from "react-i18next"
import { CircleCheckIcon, DatabaseIcon, KeyRoundIcon, TriangleAlertIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import type { AppNeed } from "@/lib/types"

/**
 * What an app will need once it is running, said before its first deploy.
 *
 * This is the part of the form written for somebody who has never deployed
 * anything. Their app — very often written with an assistant — reads
 * DATABASE_URL, and nothing has made a database; or it keeps its data in
 * SQLite, which works for an hour and is erased by the next deploy. The panel
 * used to learn both from a crash, or not at all. Here they are a checkbox and
 * a sentence, while the fix is still one click.
 *
 * Everything shown is evidence the panel read from the repository — a package,
 * a Prisma provider line, a name in .env.example — and each item says where it
 * came from, so it can be checked rather than trusted.
 *
 * The selection is not copied into state: `declined` holds only the databases
 * somebody unticked, and what will be created is worked out from the needs and
 * that list on every render.
 */
export function AppNeeds({
  needs,
  declined,
  onToggle,
  pasted,
  onPaste,
  pastedKeys,
  otherKeys,
}: {
  needs: AppNeed[]
  /** Engines somebody chose not to create. */
  declined: string[]
  onToggle: (engine: string, create: boolean) => void
  /** The text of a pasted .env, as typed. */
  pasted: string
  onPaste: (text: string) => void
  /** The names that paste already provides. */
  pastedKeys: string[]
  /** Names provided some other way, such as a Compose service's environment. */
  otherKeys: string[]
}) {
  const { t } = useTranslation()
  const engine = (name?: string) =>
    t(`needs.engine.${name ?? "file"}`, { defaultValue: name ?? "" })

  const databases = needs.filter((n) => n.kind === "database")
  const offered = databases.filter((n) => n.provided)
  const elsewhere = databases.filter((n) => !n.provided)
  const ephemeral = needs.filter((n) => n.kind === "ephemeral")
  const expected = needs.find((n) => n.kind === "variables")?.variables ?? []

  const creating = offered.filter((n) => n.engine && !declined.includes(n.engine))
  // A setting is covered by what was pasted, by another source, or by a
  // database that is about to be created and linked under that name.
  const covered = new Set([...pastedKeys, ...otherKeys, ...creating.map((n) => n.variable ?? "")])
  const missing = expected.filter((name) => !covered.has(name))
  // A pasted name that a database about to be created will set. The pasted
  // value is the address on the computer the .env came from, and the panel
  // leaves it out rather than store it (see handleCreateApp).
  const replaced = creating
    .filter((n) => n.variable && pastedKeys.includes(n.variable))
    .map((n) => ({ variable: n.variable!, engine: engine(n.engine) }))

  return (
    <div className="space-y-4">
      {offered.length > 0 && (
        <div className="space-y-3 rounded-lg border p-3">
          <p className="flex items-center gap-2 text-sm font-medium">
            <DatabaseIcon className="size-4 text-muted-foreground" />
            {t("needs.databasesTitle")}
          </p>
          {offered.map((need) => {
            const id = `need-${need.engine}`
            const checked = need.engine ? !declined.includes(need.engine) : false
            return (
              <Field key={need.engine} orientation="horizontal">
                <Checkbox
                  id={id}
                  checked={checked}
                  onCheckedChange={(value) => need.engine && onToggle(need.engine, value === true)}
                />
                <div className="space-y-0.5">
                  <FieldLabel htmlFor={id} className="font-normal">
                    {t("needs.databaseCreate", {
                      engine: engine(need.engine),
                      variable: need.variable,
                    })}
                  </FieldLabel>
                  <FieldDescription>
                    {t("needs.because", { source: need.source, evidence: need.evidence })}
                  </FieldDescription>
                </div>
              </Field>
            )
          })}
          {creating.length > 0 && (
            <p className="text-xs text-muted-foreground">{t("needs.databaseStarting")}</p>
          )}
        </div>
      )}

      {elsewhere.map((need) => (
        <Alert key={need.engine} variant="info">
          <DatabaseIcon />
          <AlertTitle>{t("needs.notProvidedTitle", { engine: engine(need.engine) })}</AlertTitle>
          <AlertDescription>
            {t("needs.notProvided", { engine: engine(need.engine), variable: need.variable })}{" "}
            {t("needs.because", { source: need.source, evidence: need.evidence })}
          </AlertDescription>
        </Alert>
      ))}

      {ephemeral.map((need) => (
        <Alert key={need.engine} variant="warning">
          <TriangleAlertIcon />
          <AlertTitle>{t("needs.ephemeralTitle")}</AlertTitle>
          <AlertDescription>
            <p>{t("needs.ephemeral", { what: engine(need.engine), evidence: need.evidence })}</p>
            <p>
              {offered.length > 0 ? t("needs.ephemeralFixDatabase") : t("needs.ephemeralFixVolume")}
            </p>
          </AlertDescription>
        </Alert>
      ))}

      <Field>
        <FieldLabel htmlFor="pasted-env" className="flex items-center gap-2">
          <KeyRoundIcon className="size-4 text-muted-foreground" />
          {t("needs.variablesTitle")}
        </FieldLabel>
        <FieldDescription>
          {expected.length > 0
            ? t("needs.variablesExpected", {
                source: needs.find((n) => n.kind === "variables")?.source,
              })
            : t("needs.variablesOptional")}{" "}
          {t("needs.variablesSecret")}
        </FieldDescription>
        <Textarea
          id="pasted-env"
          rows={4}
          spellCheck={false}
          autoComplete="off"
          className="font-mono text-xs"
          placeholder={t("needs.variablesPlaceholder")}
          value={pasted}
          onChange={(event) => onPaste(event.target.value)}
        />
        {expected.length > 0 &&
          (missing.length > 0 ? (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <span className="text-muted-foreground">{t("needs.missing")}</span>
              {missing.map((name) => (
                <Badge key={name} variant="outline" className="font-mono">
                  {name}
                </Badge>
              ))}
            </div>
          ) : (
            <p className="flex items-center gap-1.5 text-xs text-success">
              <CircleCheckIcon className="size-3.5" />
              {t("needs.allSet")}
            </p>
          ))}
        {replaced.map((r) => (
          <p key={r.variable} className="text-xs text-muted-foreground">
            {t("needs.pastedReplaced", { variable: r.variable, engine: r.engine })}
          </p>
        ))}
      </Field>
    </div>
  )
}
