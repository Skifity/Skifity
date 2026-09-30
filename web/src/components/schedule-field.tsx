import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * Choosing when something runs, without knowing cron.
 *
 * A backup schedule and a scheduled command were both a text box containing
 * `0 3 * * *`. That asks every user to know a syntax, to get five fields in the
 * right order, and to find out they were wrong at three in the morning when the
 * backup they thought they had did not happen. Four presets cover what people
 * actually pick; cron stays, behind "Custom", for the times they do not.
 *
 * The times are UTC and the labels say so. A schedule that silently meant the
 * server's idea of local time would be a different failure in every timezone.
 */

/** The presets, in the order they are offered. */
export const SCHEDULE_PRESETS = [
  { value: "0 * * * *", label: "schedule.everyHour" },
  { value: "0 3 * * *", label: "schedule.everyDay" },
  { value: "0 3 * * 1", label: "schedule.everyWeek" },
  { value: "0 3 1 * *", label: "schedule.everyMonth" },
] as const

const CUSTOM = "custom"

/** presetFor returns the preset a cron expression is, if it is one. */
export function presetFor(value: string) {
  const normalised = value.trim().split(/\s+/).join(" ")
  return SCHEDULE_PRESETS.find((preset) => preset.value === normalised)
}

/**
 * scheduleWords renders a schedule the way a person reads it.
 *
 * A row that says "0 3 * * *" asks whoever is looking at it to parse cron in
 * their head to find out when their job runs. One of the four presets has
 * words; anything else is shown as it was written, because inventing a sentence
 * for an arbitrary expression is how a list ends up lying about a schedule.
 */
export function scheduleWords(translate: (key: string) => string, value: string): string {
  const preset = presetFor(value)
  return preset ? translate(preset.label) : value
}

export function ScheduleField({
  id,
  value,
  onChange,
  description,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  description?: string
}) {
  const { t } = useTranslation()

  // Which mode the control is in is derived from the value — except for the
  // moment somebody picks "Custom" while the box still holds a preset, which
  // is an intent no value can carry. That is what this one piece of state is.
  const [customChosen, setCustomChosen] = useState(false)
  const preset = presetFor(value)
  const custom = customChosen || !preset

  return (
    <Field>
      <FieldLabel htmlFor={id}>{t("schedule.label")}</FieldLabel>
      <Select
        value={custom ? CUSTOM : preset!.value}
        onValueChange={(next) => {
          if (next === CUSTOM) {
            setCustomChosen(true)
            return
          }
          setCustomChosen(false)
          onChange(next)
        }}
      >
        <SelectTrigger id={id}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {SCHEDULE_PRESETS.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {t(option.label)}
            </SelectItem>
          ))}
          <SelectItem value={CUSTOM}>{t("schedule.custom")}</SelectItem>
        </SelectContent>
      </Select>

      {custom && (
        <Input
          aria-label={t("schedule.customLabel")}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className="mt-2 font-mono"
          placeholder="0 3 * * *"
          spellCheck={false}
        />
      )}

      <FieldDescription>{custom ? t("schedule.customHelp") : description}</FieldDescription>
    </Field>
  )
}
