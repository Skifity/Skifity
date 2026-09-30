/**
 * The Skifity mark.
 *
 * Three stacked layers that shift as they rise: servers underneath, apps on
 * top. Drawn inline so it inherits the current colour and needs no request.
 */
export function Logo({ className, title }: { className?: string; title?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      className={className}
      role={title ? "img" : "presentation"}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <path d="M16 3.5 28 9.25 16 15 4 9.25 16 3.5Z" className="fill-current" opacity="0.95" />
      <path
        d="M4 15.4 16 21.15 28 15.4"
        className="stroke-current"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        opacity="0.62"
      />
      <path
        d="M4 21.9 16 27.65 28 21.9"
        className="stroke-current"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        opacity="0.32"
      />
    </svg>
  )
}
