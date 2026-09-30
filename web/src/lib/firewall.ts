/**
 * The firewall's shapes, and the small amount of logic the form needs.
 *
 * They mirror internal/edgerules exactly, because the JSON goes straight into
 * the database and then to the process that enforces it. A field renamed on one
 * side and not the other is a rule that silently stops matching.
 */

export type FirewallAction = "allow" | "block"

export type FirewallField =
  | "ip.src"
  | "ip.country"
  | "ip.asn"
  | "http.host"
  | "http.path"
  | "http.method"
  | "http.user_agent"
  | "http.header"

export type FirewallOperator =
  "in" | "not_in" | "contains" | "starts_with" | "ends_with" | "matches"

export type FirewallTest = {
  field: FirewallField
  /** Only for http.header. */
  key?: string
  op: FirewallOperator
  values: string[]
}

/** Exactly one of the four is set, the same as the Go side. */
export type FirewallExpr = {
  all?: FirewallExpr[]
  any?: FirewallExpr[]
  not?: FirewallExpr
  test?: FirewallTest
}

export type FirewallRule = {
  id: string
  name: string
  action: FirewallAction
  expr: FirewallExpr
  enabled: boolean
}

export type FirewallRuleSet = {
  rules?: FirewallRule[]
  default?: FirewallAction
}

export type Firewall = {
  enabled: boolean
  rules: FirewallRuleSet
  /** Whether the component that enforces these is running. */
  installed: boolean
  country_available: boolean
  network_available: boolean
  attribution?: string
  attribution_url?: string
  /** The addresses these rules apply to. None means they guard nothing. */
  hostnames: string[]
}

/** The fields, in the order the form lists them. */
export const FIREWALL_FIELDS: FirewallField[] = [
  "ip.src",
  "ip.country",
  "ip.asn",
  "http.host",
  "http.path",
  "http.method",
  "http.user_agent",
  "http.header",
]

/**
 * Which comparisons a field allows.
 *
 * An address, a country and a network are membership tests and nothing else:
 * "contains" on an address would compile, save, and never match anything, which
 * is the failure this whole product keeps finding. The Go side refuses it too;
 * this is what stops it being offered in the first place.
 */
export function operatorsFor(field: FirewallField): FirewallOperator[] {
  if (field === "ip.src" || field === "ip.country" || field === "ip.asn") {
    return ["in", "not_in"]
  }
  return ["in", "not_in", "contains", "starts_with", "ends_with", "matches"]
}

export type GeoNeeds = { country: boolean; asn: boolean }

export function needsGeo(expr: FirewallExpr): GeoNeeds {
  if (expr.test) {
    return { country: expr.test.field === "ip.country", asn: expr.test.field === "ip.asn" }
  }
  if (expr.not) return needsGeo(expr.not)
  const children = [...(expr.all ?? []), ...(expr.any ?? [])]
  const found: GeoNeeds = { country: false, asn: false }
  for (const child of children) {
    const child_ = needsGeo(child)
    found.country = found.country || child_.country
    found.asn = found.asn || child_.asn
  }
  return found
}

/** A new, empty condition: one test that matches nothing until it is filled in. */
export function newTest(): FirewallExpr {
  return { test: { field: "ip.src", op: "in", values: [] } }
}

/** A new group, which starts as "all of these" with one condition in it. */
export function newGroup(): FirewallExpr {
  return { all: [newTest()] }
}

/** Reads a group's kind and children, whichever of the two it is. */
export function groupOf(
  expr: FirewallExpr,
): { kind: "all" | "any"; children: FirewallExpr[] } | null {
  if (expr.all) return { kind: "all", children: expr.all }
  if (expr.any) return { kind: "any", children: expr.any }
  return null
}

/** Rebuilds a group with the other kind, keeping its children. */
export function withGroupKind(expr: FirewallExpr, kind: "all" | "any"): FirewallExpr {
  const group = groupOf(expr)
  const children = group?.children ?? []
  return kind === "all" ? { all: children } : { any: children }
}

/**
 * Removes one condition from a group, and never leaves the group empty.
 *
 * A group that is there and holds nothing is not "no conditions": the panel
 * refuses to save it, because reading it as true would make a half-finished
 * block rule match every request and lock everybody out. Deleting the last
 * condition therefore clears it back to a fresh one rather than removing it —
 * a rule that should apply to everybody is written by deleting the rule and
 * setting the set's default action.
 */
export function withoutCondition(expr: FirewallExpr, index: number): FirewallExpr {
  const group = groupOf(expr)
  if (!group) return expr
  const children = group.children.filter((_, i) => i !== index)
  return withGroupKind({ all: children.length > 0 ? children : [newTest()] }, group.kind)
}
