import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useQuery } from "@tanstack/react-query"
import { ChevronsUpDownIcon, LockIcon, SearchIcon } from "lucide-react"

import { ErrorDisplay } from "@/components/error-display"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api"
import type { GitListing, GitRepository, GitSource } from "@/lib/types"

/** The kinds of connection whose host has an API the panel can list from. */
export function canListRepositories(source: GitSource | undefined): boolean {
  return (
    source !== undefined &&
    (source.kind === "github_pat" ||
      source.kind === "gitlab" ||
      source.kind === "gitea" ||
      source.kind === "bitbucket")
  )
}

/** The kinds whose host searches its own list, past what the panel was given. */
function searchesOnTheHost(source: GitSource): boolean {
  return source.kind === "gitlab" || source.kind === "bitbucket"
}

/**
 * The repositories a connection can read, searchable, as an offer beside the
 * address field rather than in place of it.
 *
 * Nothing is asked until the list is opened, and what came back is kept for a
 * few minutes: a provider's rate limit is shared by the whole panel. The list
 * is filtered here as somebody types. Only GitLab and Bitbucket search more
 * than the panel was given, so only they are offered a search of the host —
 * and only when somebody asks for one, not on every keystroke.
 */
export function RepositoryPicker({
  teamId,
  source,
  onPick,
}: {
  teamId: string
  source: GitSource
  onPick: (repository: GitRepository) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState("")
  // What the host was asked to search for. It is set by choosing "search",
  // never by typing, so a search is one request rather than one a keystroke.
  const [asked, setAsked] = useState("")

  const repositories = useQuery({
    queryKey: ["git-repositories", source.id, asked],
    queryFn: () =>
      api.get<GitListing<GitRepository>>(
        `/api/teams/${teamId}/git-sources/${source.id}/repositories` +
          (asked ? `?q=${encodeURIComponent(asked)}` : ""),
      ),
    enabled: open,
    staleTime: 5 * 60_000,
  })
  const listing = repositories.data
  const query = typed.trim()
  const canSearchHost =
    searchesOnTheHost(source) && listing?.truncated === true && query !== "" && query !== asked

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button type="button" variant="ghost" size="sm" className="-ml-2 h-7">
          <ChevronsUpDownIcon className="size-3.5" />
          {t("git.chooseRepository", { source: source.name })}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0 sm:w-96" align="start">
        <Command>
          <CommandInput
            value={typed}
            onValueChange={setTyped}
            placeholder={t("git.searchRepositories")}
          />
          <CommandList>
            {repositories.isPending && (
              <div className="flex items-center gap-2 p-3 text-sm text-muted-foreground">
                <Spinner />
                {t("git.readingRepositories")}
              </div>
            )}
            {repositories.error != null && (
              <div className="p-2">
                <ErrorDisplay error={repositories.error} compact />
              </div>
            )}
            {listing && <CommandEmpty>{t("git.noRepositories")}</CommandEmpty>}
            {listing && listing.items.length > 0 && (
              <CommandGroup>
                {listing.items.map((repository) => (
                  <CommandItem
                    key={repository.full_name}
                    value={repository.full_name}
                    onSelect={() => {
                      onPick(repository)
                      setOpen(false)
                    }}
                  >
                    <span className="truncate font-mono text-xs">{repository.full_name}</span>
                    {repository.private && (
                      <LockIcon className="ml-auto size-3.5" aria-label={t("git.private")} />
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {canSearchHost && (
              <CommandGroup forceMount>
                <CommandItem forceMount value={`search ${query}`} onSelect={() => setAsked(query)}>
                  <SearchIcon />
                  {t("git.searchHost", { query, source: source.name })}
                </CommandItem>
              </CommandGroup>
            )}
          </CommandList>
          {listing?.truncated && (
            <p className="border-t p-2 text-xs text-muted-foreground">
              {t("git.repositoriesTruncated", { limit: listing.items.length })}
            </p>
          )}
        </Command>
      </PopoverContent>
    </Popover>
  )
}
