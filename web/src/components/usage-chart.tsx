import { useId } from "react"

/**
 * A line over time, drawn as an SVG, with a threshold if there is one.
 *
 * Not a charting library: the panel draws two lines on one page, and a library
 * for that is a second component system and a heavier bundle for the phone
 * that opens it. The scale is 0 to max, so a percentage chart always shows the
 * limit at the top rather than stretching a flat line into a cliff.
 */
export function UsageChart({
  points,
  max,
  threshold,
  label,
  format,
  height = 96,
}: {
  points: { at: string; value: number }[]
  max: number
  threshold?: number
  /** What the chart shows, for a screen reader. */
  label: string
  format: (value: number) => string
  height?: number
}) {
  const gradient = useId()
  const width = 600
  const top = Math.max(max, ...points.map((point) => point.value), 1)
  const times = points.map((point) => new Date(point.at).getTime())
  const start = Math.min(...times)
  const span = Math.max(Math.max(...times) - start, 1)
  const x = (at: number) => ((at - start) / span) * width
  const y = (value: number) => height - (value / top) * height

  const line = points
    .map(
      (point, i) => `${i === 0 ? "M" : "L"}${x(times[i]).toFixed(1)},${y(point.value).toFixed(1)}`,
    )
    .join(" ")
  const area = points.length > 0 ? `${line} L${width},${height} L0,${height} Z` : ""
  const last = points.at(-1)

  return (
    <figure className="space-y-1">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        className="h-24 w-full overflow-visible"
        role="img"
        aria-label={last ? `${label}: ${format(last.value)}` : label}
      >
        <defs>
          <linearGradient id={gradient} x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor="currentColor" stopOpacity="0.25" />
            <stop offset="100%" stopColor="currentColor" stopOpacity="0" />
          </linearGradient>
        </defs>
        <g className="text-primary">
          {area && <path d={area} fill={`url(#${gradient})`} />}
          {line && (
            <path
              d={line}
              fill="none"
              stroke="currentColor"
              strokeWidth={2}
              vectorEffect="non-scaling-stroke"
            />
          )}
        </g>
        {threshold != null && threshold > 0 && (
          <line
            x1={0}
            x2={width}
            y1={y(threshold)}
            y2={y(threshold)}
            className="text-destructive"
            stroke="currentColor"
            strokeDasharray="6 4"
            vectorEffect="non-scaling-stroke"
          />
        )}
      </svg>
      <figcaption className="flex justify-between text-xs text-muted-foreground tabular-nums">
        <span>
          {points.length > 0
            ? new Date(start).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
            : ""}
        </span>
        <span>{last ? format(last.value) : ""}</span>
      </figcaption>
    </figure>
  )
}
