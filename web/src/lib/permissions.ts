import type { Team, User } from "@/lib/types"

/**
 * What the interface offers to whom.
 *
 * The server decides every one of these again, and refuses what does not
 * pass. These only keep the interface from offering a link to a refusal: a
 * page that bounces somebody back to the overview, or a list that fails for
 * them every time, is a trap rather than a page.
 */

/**
 * Whether the team's own business is open to this person: its members and
 * invitations, its connections, its audit log. The panel asks for an owner or
 * an admin of the whole team (authorizeTeam with RoleAdmin), so a member
 * limited to some projects is not one, whatever their role.
 */
export function managesTeam(team: Team | null | undefined): boolean {
  return Boolean(team && !team.scoped && (team.role === "owner" || team.role === "admin"))
}

/**
 * Whether Settings has anything for this person: the panel-wide tabs for the
 * panel's administrator, the team's tabs for whoever manages the team.
 */
export function canOpenSettings(user: User | null | undefined, team: Team | null | undefined) {
  return Boolean(user?.is_admin) || managesTeam(team)
}

/**
 * Whether what belongs to the whole team — its servers, its event stream — is
 * open to this person. A member limited to some projects is refused it.
 */
export function seesWholeTeam(team: Team | null | undefined): boolean {
  return Boolean(team && !team.scoped)
}
