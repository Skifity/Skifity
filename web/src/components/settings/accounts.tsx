import { useTranslation } from "react-i18next"
import { useMutation, useQuery } from "@tanstack/react-query"
import {
  BanIcon,
  KeyRoundIcon,
  MoreHorizontalIcon,
  ShieldCheckIcon,
  ShieldOffIcon,
  Trash2Icon,
  UserCheckIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useConfirm, useDeleteConfirm } from "@/components/confirm-dialog"
import { ErrorDisplay } from "@/components/error-display"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { api, type List } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import { currentLanguage } from "@/lib/i18n"
import { queryClient } from "@/lib/query"
import type { Account, Role, User } from "@/lib/types"

/** Names in a sentence, joined the way the reader's language joins them. */
function listOf(items: string[]): string {
  return new Intl.ListFormat(currentLanguage(), { style: "long", type: "conjunction" }).format(
    items,
  )
}

/** What an account is called on this page: its name, or its address. */
const nameOf = (account: Account) => account.user.name || account.user.email

/**
 * Settings → Accounts: everybody who can sign in to the panel, in every team.
 *
 * The panel administrator's, and only for other people's accounts: the
 * administrator's own row has no actions, because their own sign-in is the
 * Account page's and locking yourself out should not be one click away.
 *
 * Taking access away — disabling, demoting, deleting — asks here first.
 * Giving it — making an administrator, enabling an account, resetting its
 * second factor — asks for the password too, which the API client does by
 * itself when the panel says so.
 */
export function AccountsPanel() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const confirmDelete = useDeleteConfirm()

  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.get<List<Account>>("/api/users"),
  })
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["accounts"] })

  const change = useMutation({
    mutationFn: ({
      account,
      body,
    }: {
      account: Account
      body: Partial<Pick<User, "disabled" | "is_admin">>
    }) => api.patch<User>(`/api/users/${account.user.id}`, body),
    onSuccess: (_, { account, body }) => {
      refresh()
      const name = nameOf(account)
      if (body.disabled === true) toast.success(t("accounts.disabledDone", { name }))
      else if (body.disabled === false) toast.success(t("accounts.enabledDone", { name }))
      else if (body.is_admin === true) toast.success(t("accounts.adminGrantedDone", { name }))
      else toast.success(t("accounts.adminRevokedDone", { name }))
    },
  })
  const resetTwoFactor = useMutation({
    mutationFn: (account: Account) => api.post(`/api/users/${account.user.id}/two-factor/reset`),
    onSuccess: (_, account) => {
      refresh()
      toast.success(t("accounts.twoFactorResetDone", { name: nameOf(account) }))
    },
  })
  const remove = useMutation({
    mutationFn: (account: Account) => api.delete(`/api/users/${account.user.id}`),
    onSuccess: (_, account) => {
      refresh()
      toast.success(t("accounts.deletedDone", { name: nameOf(account) }))
    },
  })
  const busy = change.isPending || resetTwoFactor.isPending || remove.isPending

  const disable = async (account: Account) => {
    const plugins = account.blockers.plugins.map((plugin) => plugin.name)
    const yes = await confirm({
      title: t("accounts.disableTitle", { name: nameOf(account) }),
      description: t("accounts.disableDescription"),
      // A plugin runs with its installer's token, which a disabled account's
      // is not: saying so here is the difference between a decision and a
      // plugin that stops without anybody knowing why.
      consequence:
        plugins.length > 0 ? t("accounts.disablePlugins", { plugins: listOf(plugins) }) : undefined,
      confirmLabel: t("accounts.disable"),
      destructive: true,
    })
    if (yes) change.mutate({ account, body: { disabled: true } })
  }

  const grantAdmin = async (account: Account) => {
    const yes = await confirm({
      title: t("accounts.makeAdminTitle", { name: nameOf(account) }),
      description: t("accounts.makeAdminDescription"),
      confirmLabel: t("accounts.makeAdmin"),
    })
    if (yes) change.mutate({ account, body: { is_admin: true } })
  }

  const revokeAdmin = async (account: Account) => {
    const yes = await confirm({
      title: t("accounts.revokeAdminTitle", { name: nameOf(account) }),
      description: t("accounts.revokeAdminDescription"),
      confirmLabel: t("accounts.revokeAdmin"),
      destructive: true,
    })
    if (yes) change.mutate({ account, body: { is_admin: false } })
  }

  const reset = async (account: Account) => {
    const strict = account.teams.filter((team) => team.require_strong_auth).map((team) => team.name)
    const yes = await confirm({
      title: t("accounts.resetTwoFactorTitle", { name: nameOf(account) }),
      description: t("accounts.resetTwoFactorDescription"),
      consequence:
        strict.length > 0
          ? t("accounts.resetTwoFactorStrongTeams", { teams: listOf(strict) })
          : undefined,
      confirmLabel: t("accounts.resetTwoFactor"),
      destructive: true,
    })
    if (yes) resetTwoFactor.mutate(account)
  }

  const askDelete = async (account: Account) => {
    // What goes with it, from the row the page already has, so the question
    // is asked with its answer in front of the person.
    const goes = [
      account.teams.length > 0 &&
        t("accounts.goesTeams", {
          count: account.teams.length,
          teams: listOf(account.teams.map((team) => team.name)),
        }),
      account.tokens > 0 && t("accounts.goesTokens", { count: account.tokens }),
      account.passkeys > 0 && t("accounts.goesPasskeys", { count: account.passkeys }),
      account.sso_issuers.length > 0 && t("accounts.goesSSO"),
    ].filter((part): part is string => typeof part === "string")
    const yes = await confirmDelete(
      account.user.email,
      t("accounts.deleteDescription"),
      goes.length > 0
        ? t("accounts.deleteGoes", { what: listOf(goes) })
        : t("accounts.deleteGoesNothing"),
    )
    if (yes) remove.mutate(account)
  }

  if (accounts.isLoading) return <Skeleton className="h-64" />
  if (accounts.error) {
    return <ErrorDisplay error={accounts.error} onRetry={() => void accounts.refetch()} />
  }

  const roleName = (role: Role) =>
    t(`settings.role${role.charAt(0).toUpperCase()}${role.slice(1)}`, { defaultValue: role })

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("accounts.title")}</CardTitle>
        <CardDescription className="space-y-1">
          <span className="block">{t("accounts.description")}</span>
          <span className="block">{t("accounts.serverHint", { command: "skifity admin" })}</span>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 p-0">
        {[change.error, resetTwoFactor.error, remove.error].map(
          (error, index) =>
            error != null && (
              <div key={index} className="px-6">
                <ErrorDisplay error={error} compact />
              </div>
            ),
        )}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("accounts.account")}</TableHead>
              <TableHead className="hidden md:table-cell">{t("accounts.signsInWith")}</TableHead>
              <TableHead className="hidden sm:table-cell">{t("accounts.teams")}</TableHead>
              <TableHead className="hidden lg:table-cell">{t("accounts.lastSignIn")}</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {accounts.data?.items.map((account) => {
              const blocked =
                account.blockers.sole_owner_of.length > 0 || account.blockers.plugins.length > 0
              const ways = [
                account.has_password && t("accounts.password"),
                account.user.totp_enabled && t("accounts.twoFactor"),
                account.passkeys > 0 && t("accounts.passkeys", { count: account.passkeys }),
                account.sso_issuers.length > 0 && t("accounts.sso"),
              ].filter((way): way is string => typeof way === "string")
              return (
                <TableRow
                  key={account.user.id}
                  data-account={account.user.email}
                  className={account.user.disabled ? "text-muted-foreground" : undefined}
                >
                  <TableCell className="align-top">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-medium">{nameOf(account)}</span>
                      {account.current && <Badge variant="outline">{t("accounts.you")}</Badge>}
                      {account.user.is_admin && (
                        <Badge variant="secondary">{t("accounts.administrator")}</Badge>
                      )}
                      {account.user.disabled && (
                        <Badge variant="destructive">{t("accounts.disabledBadge")}</Badge>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground">{account.user.email}</div>
                    {account.blockers.sole_owner_of.length > 0 && (
                      <div className="text-xs text-muted-foreground">
                        {t("accounts.onlyOwnerOf", {
                          teams: listOf(account.blockers.sole_owner_of.map((team) => team.name)),
                        })}
                      </div>
                    )}
                    {account.blockers.plugins.length > 0 && (
                      <div className="text-xs text-muted-foreground">
                        {t("accounts.installedPlugins", {
                          plugins: listOf(account.blockers.plugins.map((plugin) => plugin.name)),
                        })}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="hidden align-top text-sm md:table-cell">
                    {ways.length > 0 ? listOf(ways) : t("accounts.noWayIn")}
                  </TableCell>
                  <TableCell className="hidden align-top text-sm sm:table-cell">
                    {account.teams.length > 0 ? (
                      <ul className="space-y-0.5">
                        {account.teams.map((team) => (
                          <li key={team.id}>
                            {team.name}{" "}
                            <span className="text-xs text-muted-foreground">
                              {roleName(team.role)}
                            </span>
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <span className="text-muted-foreground">{t("accounts.noTeams")}</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden align-top text-sm text-muted-foreground lg:table-cell">
                    {account.user.last_login_at
                      ? formatRelative(account.user.last_login_at)
                      : t("accounts.never")}
                  </TableCell>
                  <TableCell className="text-right align-top">
                    {!account.current && (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={busy}
                            aria-label={t("accounts.actions", { name: nameOf(account) })}
                          >
                            <MoreHorizontalIcon className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-60">
                          {account.user.is_admin ? (
                            <DropdownMenuItem onSelect={() => void revokeAdmin(account)}>
                              <ShieldOffIcon />
                              {t("accounts.revokeAdmin")}
                            </DropdownMenuItem>
                          ) : (
                            <DropdownMenuItem onSelect={() => void grantAdmin(account)}>
                              <ShieldCheckIcon />
                              {t("accounts.makeAdmin")}
                            </DropdownMenuItem>
                          )}
                          {account.user.disabled ? (
                            <DropdownMenuItem
                              onSelect={() => change.mutate({ account, body: { disabled: false } })}
                            >
                              <UserCheckIcon />
                              {t("accounts.enable")}
                            </DropdownMenuItem>
                          ) : (
                            <DropdownMenuItem onSelect={() => void disable(account)}>
                              <BanIcon />
                              {t("accounts.disable")}
                            </DropdownMenuItem>
                          )}
                          {account.user.totp_enabled && (
                            <DropdownMenuItem onSelect={() => void reset(account)}>
                              <KeyRoundIcon />
                              {t("accounts.resetTwoFactor")}
                            </DropdownMenuItem>
                          )}
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            disabled={blocked}
                            onSelect={() => void askDelete(account)}
                          >
                            <Trash2Icon />
                            {t("common.delete")}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
