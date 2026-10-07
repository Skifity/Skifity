package errdoc

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"skifity/internal/version"
)

// This file is the catalogue of failures the panel knows how to explain.
//
// A code that appears here gets a translated message in the UI and a specific
// fix. Anything not here falls back to the generic "internal" problem, which is
// a signal that the catalogue needs a new entry, not that the message is fine.

// --- request and authorization ---

// BadRequest is a malformed or invalid request from the client.
func BadRequest(cause string) *Problem {
	return New("request.invalid", "That request was not valid").
		WithCause("%s", cause).
		WithImpact("Nothing was changed.").
		WithFix("Correct the highlighted field and try again.").
		WithStatus(http.StatusBadRequest)
}

// Unauthorized means no valid credentials were presented.
func Unauthorized() *Problem {
	return New("auth.required", "You need to sign in").
		WithCause("This request had no valid session or API token.").
		WithImpact("The action was not performed.").
		WithFix("Sign in again. If you are using the CLI, run `skifity login`.").
		WithStatus(http.StatusUnauthorized)
}

// Forbidden means the caller is known but not allowed.
func Forbidden(action string) *Problem {
	return New("auth.forbidden", "You do not have permission for this").
		WithCause("Your role in this team does not allow %s.", action).
		WithImpact("The action was not performed.").
		WithFix("Ask a team owner or admin to do this, or to raise your role.").
		WithStatus(http.StatusForbidden)
}

// SchemaNewer is a binary asked to open a database that a later version has
// already migrated.
func SchemaNewer(have, know int) *Problem {
	return New("store.schema_newer", "This database belongs to a newer version").
		WithCause("The database has been migrated to schema %d, and this version knows schema %d at most.", have, know).
		WithImpact("Nothing was read or changed. An older version writing to a newer database is how data is lost without anybody noticing.").
		WithFix("Run the version that upgraded it. To go back to this version instead, restore the copy taken before the upgrade with `%s admin restore-db`.", version.Binary).
		WithDocs("/docs/configuration#upgrading")
}

// StrongAuthRequired is somebody signed in with a password alone asking for a
// team that requires more.
func StrongAuthRequired() *Problem {
	return New("auth.strong_auth_required", "This team asks for two-factor authentication").
		WithCause("Everybody in this team has to sign in with a second factor, a passkey or single sign-on, and this sign-in used none of them.").
		WithImpact("Nothing in the team was shown or changed.").
		WithFix("Turn on two-factor authentication under Account, or sign in with a passkey or with single sign-on.").
		WithDocs("/docs/configuration#signing-in").
		WithStatus(http.StatusForbidden)
}

// DeployLocked is a deploy or a rollback of an app somebody has locked.
func DeployLocked(by, reason string) *Problem {
	return New("deploy.locked", "Deploys to this app are locked").
		WithCause("%s locked them: %s", by, reason).
		WithImpact("Nothing was deployed. The version running now keeps running.").
		WithFix("Unlock deploys on the app's page, or with `%s unlock`, once what they were locked for is over.", version.Binary).
		WithStatus(http.StatusConflict)
}

// WatchPathInvalid is a line in an app's watch paths that is not a pattern.
func WatchPathInvalid(line string) *Problem {
	return New("app.watch_path_invalid", "That is not a path the app can watch").
		WithCause("%s is not a path or a pattern.", line).
		WithImpact("Nothing was changed.").
		WithFix("Write one path or pattern per line, starting at the repository's root: apps/web, "+
			"apps/**/package.json, or !**/*.md to leave something out. A path cannot use .. to leave the repository.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#only-the-paths-an-app-watches").
		With("line", line)
}

// TooManyWatchPaths is more watch paths than an app can have.
func TooManyWatchPaths(max int) *Problem {
	return New("app.too_many_watch_paths", "That is too many paths to watch").
		WithCause("An app can watch at most %d paths or patterns.", max).
		WithImpact("Nothing was changed.").
		WithFix("Use a pattern that covers several paths at once, such as apps/** or packages/*/src.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#only-the-paths-an-app-watches")
}

// DeployTriggerInvalid is something other than a branch or a tag to deploy on.
func DeployTriggerInvalid(value string) *Problem {
	return New("app.deploy_trigger_invalid", "An app deploys on a branch or on a tag").
		WithCause("%s is not something an app can deploy on.", value).
		WithImpact("Nothing was changed.").
		WithFix("Use branch to deploy every push to the app's branch, or tag to deploy only the tags whose name matches its tag pattern.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#deploying-a-tag")
}

// TagPatternInvalid is a tag pattern path.Match cannot read.
func TagPatternInvalid(pattern string) *Problem {
	return New("app.tag_pattern_invalid", "That is not a tag pattern").
		WithCause("%s is not a pattern a tag's name can be matched against.", pattern).
		WithImpact("Nothing was changed.").
		WithFix("Write one pattern of up to 100 characters, such as v* or release-*: * matches any run of characters " +
			"except a slash, ? matches one character, and [0-9] one of a set. Close every [ you open.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#deploying-a-tag")
}

// GitListingUnsupported is a Git connection with no API to list from.
func GitListingUnsupported(connection string) *Problem {
	return New("git.listing_unsupported", "This Git connection cannot list repositories").
		WithCause("%s is a plain Git connection: it has a token and no API the panel knows how to ask.", connection).
		WithImpact("Nothing was listed. Apps can still be created from it.").
		WithFix("Type the repository's address and its branch into the form.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#choosing-the-repository")
}

// GitListingFailed is a Git host that did not answer with a list.
func GitListingFailed(connection, reason string) *Problem {
	return New("git.listing_failed", "The Git host did not list what it has").
		WithCause("Asked through %s, %s.", connection, reason).
		WithImpact("Nothing was listed. You can still type the repository's address and branch yourself.").
		WithFix("Check that the connection's token can read repositories — the repo scope or Contents read access on GitHub, " +
			"read_api on GitLab, read:repository on Gitea, read:repository:bitbucket on Bitbucket — and that the panel can reach the Git host.").
		WithStatus(http.StatusBadGateway).
		WithDocs("/docs/concepts#choosing-the-repository")
}

// BitbucketTokenRefused is a Bitbucket connection whose token Bitbucket turned
// down when the panel checked it, before saving it.
func BitbucketTokenRefused(answer string) *Problem {
	return New("git.bitbucket_token_refused", "Bitbucket did not accept this token").
		WithCause("Bitbucket answered %s when the panel asked what the token can read.", answer).
		WithImpact("The connection was not saved.").
		WithFix("Use an API token with read:repository:bitbucket and read:workspace:bitbucket, " +
			"with the Atlassian account's email or with none; or an access token with Repositories read, " +
			"with its workspace named. App passwords no longer work.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/troubleshooting#bitbucket-refuses-the-token-or-a-webhook")
}

// BitbucketWorkspaceUnknown is a Bitbucket connection naming a workspace its
// token cannot see.
func BitbucketWorkspaceUnknown(workspace string) *Problem {
	return New("git.bitbucket_workspace_unknown", "Bitbucket has no such workspace for this token").
		WithCause("Bitbucket answered that there is no workspace %s this token can read.", workspace).
		WithImpact("The connection was not saved.").
		WithFix("Give the workspace's id, the part of a repository's address after bitbucket.org/, " +
			"or leave it empty to use every workspace an API token can see.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#connecting-bitbucket")
}

// BitbucketCheckFailed is Bitbucket not answering the panel's check of a new
// connection's token.
func BitbucketCheckFailed(reason string) *Problem {
	return New("git.bitbucket_check_failed", "Bitbucket could not be asked about this token").
		WithCause("Checking the token, %s.", reason).
		WithImpact("The connection was not saved.").
		WithFix("Check that this server can reach api.bitbucket.org over HTTPS, then try again.").
		WithStatus(http.StatusBadGateway).
		WithDocs("/docs/troubleshooting#bitbucket-refuses-the-token-or-a-webhook").
		Retry()
}

// MaintenanceMessage is a maintenance page with nothing to say, or too much.
func MaintenanceMessage(max int) *Problem {
	return New("app.maintenance_message", "Write the message visitors will read").
		WithCause("The maintenance page shows the message and nothing else, so it cannot be empty or longer than %d characters.", max).
		WithImpact("Nothing was changed. The app is not in maintenance.").
		WithFix("Write a sentence or two in the language your visitors read: what is happening, and when the app is back.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#maintenance")
}

// MaintenanceAddress is an allow list entry that is not an address.
func MaintenanceAddress(entry string, max int) *Problem {
	return New("app.maintenance_address", "That is not an address to let through").
		WithCause("%s is not an address or a range, or there are more than %d of them.", entry, max).
		WithImpact("Nothing was changed. The app is not in maintenance.").
		WithFix("Write one address, like 203.0.113.7, or one range, like 203.0.113.0/24, per line.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#maintenance").
		With("entry", entry)
}

// MaintenanceNoDomain is maintenance for an app nobody can reach by name.
func MaintenanceNoDomain() *Problem {
	return New("app.maintenance_no_domain", "This app has no address to show a page at").
		WithCause("Maintenance is a page shown at the app's domains, and this app has none.").
		WithImpact("Nothing was changed.").
		WithFix("Add a domain on the Domains tab first. An app with no address has no visitors to show a page to.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/concepts#maintenance")
}

// HealthCheckUnknown is a health check that is none of the three there are.
func HealthCheckUnknown(check string) *Problem {
	return New("app.health_check_unknown", "That is not a health check").
		WithCause("%q is not a way to check an app. It is http, tcp or none.", check).
		WithImpact("Nothing was changed.").
		WithFix("Choose http to ask the app's health path, tcp to check that its port is open, or none for software that answers neither.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#health-checks").
		With("health_check", check)
}

// HealthPathRequired is an HTTP health check with no path to ask.
func HealthPathRequired() *Problem {
	return New("app.health_path_required", "An HTTP health check needs a path").
		WithCause("The health check is set to HTTP, which asks the app a path, and there is no path to ask.").
		WithImpact("Nothing was changed.").
		WithFix("Enter a path that returns 200 once the app is ready, such as /healthz, or choose TCP to check only that the port is open.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#health-checks")
}

// HealthStartOutOfRange is a start budget too short to mean anything or too
// long to be anything but stuck.
func HealthStartOutOfRange(seconds, low, high int) *Problem {
	return New("app.health_start_out_of_range", "That is not a time an app can be given to start").
		WithCause("The time to start has to be from %d to %d seconds, and it is %d.", low, high, seconds).
		WithImpact("Nothing was changed.").
		WithFix("Give the app as long as its slowest start takes, with some room: two minutes suits most apps, and five to ten a JVM or an image that migrates its database before it listens.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#health-checks")
}

// HealthTimeoutOutOfRange is a probe timeout outside what is allowed.
func HealthTimeoutOutOfRange(seconds, low, high int) *Problem {
	return New("app.health_timeout_out_of_range", "That is not a time a health check can wait").
		WithCause("A health check can wait from %d to %d seconds for an answer, and this one is set to %d.", low, high, seconds).
		WithImpact("Nothing was changed.").
		WithFix("Three seconds suits most apps. Raise it only for a health path that does real work, such as checking the database, and keep that work quick.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#health-checks")
}

// StackSize is a stack with no services, or more than one request takes.
func StackSize(max int) *Problem {
	return New("stack.size", "That is not a stack Skifity can create").
		WithCause("A stack has between 1 and %d services.", max).
		WithImpact("Nothing was created.").
		WithFix("Pick the services to create. For a larger file, create it in parts in the same environment; the parts still reach each other by name.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#a-compose-file")
}

// StackNeedsRepository is a service built from source in a stack with no
// repository to build it from.
func StackNeedsRepository(service string) *Problem {
	return New("stack.needs_repository", "A service is built from a repository nobody named").
		WithCause("The service %s is built from source, and the stack has no repository to build it from.", service).
		WithImpact("Nothing was created.").
		WithFix("Paste the repository's address first, then create the stack.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#a-compose-file")
}

// StackNoSource is a service with neither an image nor a build.
func StackNoSource(service string) *Problem {
	return New("stack.no_source", "A service has nothing to run").
		WithCause("The service %s has neither an image nor a build.", service).
		WithImpact("Nothing was created.").
		WithFix("Give it an image or a build in the Compose file, or leave it out of the stack.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#a-compose-file")
}

// StackDuplicate is two services that would be the same app.
func StackDuplicate(first, second, slug string) *Problem {
	return New("stack.duplicate", "Two services would have the same name").
		WithCause("%s and %s both become %s, and an environment has one app by each name.", first, second, slug).
		WithImpact("Nothing was created.").
		WithFix("Rename one of them in the Compose file.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#a-compose-file")
}

// TemplateGone is an app whose template, or whose service in it, this
// version of the panel no longer has.
func TemplateGone(app string) *Problem {
	return New("template.gone", "There is no template version to update to").
		WithCause("%s did not come from a template this panel still has.", app).
		WithImpact("Nothing was changed.").
		WithFix("Change the image under Settings yourself if a newer version exists.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/templates#updates")
}

// TemplateUpToDate is an app already on the template's image.
func TemplateUpToDate(app, image string) *Problem {
	return New("template.up_to_date", "This app is already on the template's version").
		WithCause("%s runs %s, which is what the template sets.", app, image).
		WithImpact("Nothing was changed.").
		WithFix("Updates arrive with new versions of the panel.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/templates#updates")
}

// TemplateChangedByHand is an app whose image somebody set themselves.
func TemplateChangedByHand(current, installed string) *Problem {
	return New("template.changed_by_hand", "The image was changed by hand").
		WithCause("The app runs %s, and the template last set %s: somebody chose that image, and an update would replace it.", current, installed).
		WithImpact("Nothing was changed.").
		WithFix("Update anyway to go back to the template's version, or keep the image you chose.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/templates#updates")
}

// TemplateUpdateRunning is an update already waiting on its backups.
func TemplateUpdateRunning(app string) *Problem {
	return New("template.update_running", "An update is already under way").
		WithCause("%s is being backed up before an update.", app).
		WithImpact("Nothing else was started.").
		WithFix("Wait for it: the app page says when it has deployed or why it stopped.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/templates#updates")
}

// TemplateNeedsBackups is an update with data to back up and nowhere to put it.
func TemplateNeedsBackups(app string) *Problem {
	return New("template.needs_backups", "There is nowhere to back up to first").
		WithCause("%s has disks or databases, and an update backs them up before it starts, but backups are not configured.", app).
		WithImpact("Nothing was changed.").
		WithFix("Set up backup storage under Settings, or update without a backup if you have one of your own.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/templates#updates")
}

// TemplateCatalogueAddress is an address a team's catalogue cannot be fetched
// from: not https, or carrying a credential that belongs in the header.
func TemplateCatalogueAddress(reason string) *Problem {
	return New("template.catalogue_address", "That address cannot be a template catalogue").
		WithCause("%s", reason).
		WithImpact("Nothing was saved.").
		WithFix("Give the https address of a YAML or JSON index of templates, or of a .tar.gz or .zip of them. " +
			"A token for a private Git host goes in the header, where it is sealed, and not in the address.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/templates#private-catalogues")
}

// TemplateCatalogueHeader is a header a catalogue cannot be fetched with.
func TemplateCatalogueHeader(reason string) *Problem {
	return New("template.catalogue_header", "That header cannot be sent with the catalogue's download").
		WithCause("%s", reason).
		WithImpact("Nothing was saved.").
		WithFix("Name the header the Git host reads a token from — Authorization, or PRIVATE-TOKEN for GitLab — and give its whole value, such as Bearer followed by the token.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/templates#private-catalogues")
}

// TemplateCatalogueFetchFailed is a catalogue being added whose address did
// not answer with something the panel could keep.
func TemplateCatalogueFetchFailed(address, reason string) *Problem {
	return New("template.catalogue_fetch_failed", "The catalogue could not be downloaded").
		WithCause("%s did not answer with a catalogue: %s", address, reason).
		WithImpact("Nothing was saved: a catalogue is kept once it has been downloaded and read.").
		WithFix("Open the address in a browser, check the header for a private host, and check this server can reach it. " +
			"An address on this machine or the cloud's metadata service is refused on purpose, and so is a redirect to a private address.").
		WithStatus(http.StatusBadGateway).
		WithDocs("/docs/templates#private-catalogues").
		Retry()
}

// TemplateCatalogueUnreadable is a download that is not a catalogue at all.
func TemplateCatalogueUnreadable(address, reason string) *Problem {
	return New("template.catalogue_unreadable", "That is not a template catalogue").
		WithCause("What %s answered could not be read: %s", address, reason).
		WithImpact("Nothing was saved.").
		WithFix("Point it at a YAML or JSON file with a list of templates under templates:, or at a .tar.gz or .zip of " +
			"*.yaml files in the same schema as the built-in catalogue. A raw file's address, not the page that shows it.").
		WithStatus(http.StatusUnprocessableEntity).
		WithDocs("/docs/templates#private-catalogues")
}

// TemplateCatalogueRefreshFailed is a refresh that did not work. The copy
// downloaded before is still the one templates are installed from.
func TemplateCatalogueRefreshFailed(name, reason string) *Problem {
	return New("template.catalogue_refresh_failed", "The catalogue could not be refreshed").
		WithCause("%s could not be downloaded or read again: %s", name, reason).
		WithImpact("Its templates are still installed from the copy downloaded before, which was kept.").
		WithFix("Check the address still answers with the catalogue and the header is still valid, then refresh it again. " +
			"It is also tried again every day.").
		WithStatus(http.StatusBadGateway).
		WithDocs("/docs/templates#private-catalogues").
		Retry()
}

// TooManyTemplateCatalogues is a team adding a catalogue past the limit.
func TooManyTemplateCatalogues(max int) *Problem {
	return New("template.catalogue_limit", "This team has as many catalogues as it can").
		WithCause("A team can have %d template catalogues.", max).
		WithImpact("Nothing was saved.").
		WithFix("Remove one it no longer uses, or put the templates of several into one catalogue.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/templates#private-catalogues")
}

// TemplateNotInstallable is a template in a team's catalogue that does not
// pass the checks every template is held to.
func TemplateNotInstallable(id, catalogue, why string) *Problem {
	return New("template.not_installable", "This template cannot be installed").
		WithCause("%s in %s does not pass the checks every template is held to: %s", id, catalogue, why).
		WithImpact("Nothing was created.").
		WithFix("Correct the template in the catalogue and refresh it. The catalogue's entry under Templates lists why each template was refused.").
		WithStatus(http.StatusUnprocessableEntity).
		WithDocs("/docs/templates#private-catalogues")
}

// PromotionBuiltDifferently is an image built from other build settings or
// build-time variables than the app it is promoted to has.
func PromotionBuiltDifferently(app string) *Problem {
	return New("promote.built_differently", "This app would build that version differently").
		WithCause("The image was built with other build settings or build-time variables than %s has, so it would run code built for the other environment.", app).
		WithImpact("Nothing was deployed.").
		WithFix("Make the build-time variables match, promote anyway to run the image as it was built, or deploy here to build it with this app's own.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/concepts#promoting-a-version")
}

// PromotionNotPossible is a promotion between two apps that are not stages
// of the same thing.
func PromotionNotPossible(reason string) *Problem {
	return New("promote.not_possible", "That version cannot be promoted here").
		WithCause("%s", reason).
		WithImpact("Nothing was deployed.").
		WithFix("Promote between the same app in two environments of one project, from a version that deployed.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/concepts#promoting-a-version")
}

// BackupPassphraseMissing is a sealed backup and no passphrase to open it.
func BackupPassphraseMissing() *Problem {
	return New("backup.passphrase_missing", "This backup is sealed, and there is no passphrase to open it").
		WithCause("The backup was encrypted with the backup passphrase, and none is set now.").
		WithImpact("Nothing was restored or changed.").
		WithFix("Enter the passphrase it was sealed with under Settings, then Storage, and restore again.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/backups#encryption")
}

// BackupNoPanelImage is a sealed backup or restore with no image to seal or
// open it with: the step that does runs the panel's own image, found from the
// panel's Deployment.
func BackupNoPanelImage(detail string) *Problem {
	return New("backup.no_panel_image", "A sealed backup cannot be made or opened here").
		WithCause("Sealing runs the panel's own image in the backup job, and the panel's Deployment could not be read to find it: %s", detail).
		WithImpact("Nothing was backed up, restored or changed, and nothing went to the bucket unencrypted.").
		WithFix("This works when the panel runs inside the cluster it manages, as the installer sets it up. " +
			"A panel outside it can back up only without a passphrase, which sends backups unencrypted.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/backups#encryption")
}

// BackupWrongPassphrase is a sealed backup the current passphrase does not open.
func BackupWrongPassphrase() *Problem {
	return New("backup.wrong_passphrase", "The backup passphrase does not open this backup").
		WithCause("This backup was sealed with a different passphrase than the one set now.").
		WithImpact("Nothing was restored or changed.").
		WithFix("Set the passphrase it was sealed with under Settings, then Storage, restore, and set the current one back.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/backups#encryption")
}

// BackupDamaged is a sealed backup that is not whole.
func BackupDamaged(detail string) *Problem {
	return New("backup.damaged", "This backup is damaged").
		WithCause("%s", detail).
		WithImpact("Nothing was restored or changed.").
		WithFix("Restore an earlier backup, and check the storage the damaged one is in.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/backups#verifying")
}

// K3sUpgradeBlocked is a k3s upgrade that cannot start as things are.
func K3sUpgradeBlocked(reasons string) *Problem {
	return New("k3s.upgrade_blocked", "k3s cannot be upgraded yet").
		WithCause("%s.", reasons).
		WithImpact("Nothing was changed; every server runs the version it did.").
		WithFix("Deal with each reason and check the plan again.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/configuration#upgrading-k3s")
}

// BlueprintInvalid is a skifity.yaml that cannot be applied as it is.
func BlueprintInvalid(detail string) *Problem {
	return New("blueprint.invalid", "skifity.yaml cannot be applied").
		WithCause("%s.", detail).
		WithImpact("Nothing was changed.").
		WithFix("Correct the file and run `skifity plan` again.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/cli#describing-an-environment-in-a-file")
}

// ProcessNameInvalid is a process name that cannot be one.
func ProcessNameInvalid(name string) *Problem {
	return New("process.name_invalid", "That cannot be a process's name").
		WithCause("%q is not a lowercase name of up to 20 letters, digits and hyphens, or it is web or release, which an app already has.", name).
		WithImpact("The process was not saved.").
		WithFix("Use the name from the Procfile, such as worker or clock.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#processes")
}

// TooManyProcesses is an app with as many processes as one may have.
func TooManyProcesses(limit int) *Problem {
	return New("process.too_many", "This app has as many processes as it can").
		WithCause("An app runs at most %d processes beside web.", limit).
		WithImpact("The process was not added.").
		WithFix("Remove one it no longer needs, or make the rest a separate app from the same repository.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#processes")
}

// FilesTooLarge is a file, or an app's files together, over what one Secret
// holds.
func FilesTooLarge(limitKiB int) *Problem {
	return New("file.too_large", "That is more than an app's files can hold").
		WithCause("An app's files are configuration, kept together in one place that holds at most %d KiB.", limitKiB).
		WithImpact("The file was not saved.").
		WithFix("Put data in a volume, or build it into the image, and keep files for configuration.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#files")
}

// TooManyFiles is an app that already has as many files as it can.
func TooManyFiles(limit int) *Problem {
	return New("file.too_many", "This app has as many files as it can").
		WithCause("An app has at most %d files.", limit).
		WithImpact("The file was not added.").
		WithFix("Remove one it no longer needs, or put several settings in one file.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#files")
}

// RegistryLoginRefused is a registry that answered and turned the
// credentials down.
func RegistryLoginRefused(host string) *Problem {
	return New("registry.login_refused", "The registry turned these credentials down").
		WithCause("%s answered that this username and password cannot sign in.", host).
		WithImpact("The credentials were not saved, so nothing will pull with them.").
		WithFix("Check the username, and use a token with permission to read packages — ghcr.io and Docker Hub want a token in place of the account's password.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/concepts#private-registries")
}

// PortTaken is a public port another app already has open.
func PortTaken(port int, protocol string) *Problem {
	return New("port.taken", "That port is already open for another app").
		WithCause("%d/%s is open on every server already, for another app on this panel.", port, protocol).
		WithImpact("The port was not opened.").
		WithFix("Choose another public port: the app can still listen on its own, and only the public side changes.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/concepts#ports-that-are-not-http")
}

// PasswordResetUnavailable is a panel with nothing to send a reset link
// through, or no address to link to.
func PasswordResetUnavailable() *Problem {
	return New("auth.reset_unavailable", "This panel cannot send a reset link").
		WithCause("It has no mail server set, or no Panel URL to link to.").
		WithImpact("No link was sent.").
		WithFix("Ask an administrator of this panel to reset your password, or to fill in Settings → Email and the Panel URL. Whoever runs the server can also use skifity admin reset-password.").
		WithStatus(http.StatusConflict).
		WithDocs("/docs/troubleshooting#i-have-forgotten-my-password")
}

// PasswordResetInvalid is a reset link that was used, has expired or never
// existed — one answer for all three.
func PasswordResetInvalid() *Problem {
	return New("auth.reset_invalid", "This reset link cannot be used").
		WithCause("It has been used already, it is more than 30 minutes old, or it was never a link this panel sent.").
		WithImpact("Your password has not changed.").
		WithFix("Ask for a new link from the sign-in page. Only the newest one works.").
		WithStatus(http.StatusBadRequest).
		WithDocs("/docs/troubleshooting#i-have-forgotten-my-password")
}

// PasskeysUnavailable is a passkey asked for on a panel a browser would not
// offer one to: plain HTTP somewhere other than localhost, an IP address, or
// no address at all. The default install is the first (ADR-0015).
func PasskeysUnavailable(address string) *Problem {
	p := New("auth.passkeys_unavailable", "Passkeys need the panel on HTTPS at a domain").
		WithCause("A browser offers passkeys only to a page it reached over HTTPS at a domain name, or at localhost. This panel's address, the Panel URL setting or SKIFITY_PUBLIC_URL, is not one of those.").
		WithImpact("Nobody can add a passkey or sign in with one. Passwords, two-factor authentication and single sign-on work as before.").
		WithFix("Put a domain on the panel and set Settings → General → Panel URL to its https:// address.").
		WithDocs("/docs/configuration#put-a-domain-on-the-panel").
		WithStatus(http.StatusConflict)
	if address != "" {
		p = p.With("address", address)
	}
	return p
}

// PasskeyRefused is a passkey sign-in that did not check out. The caller is
// anonymous, so which check failed is in the panel's log and not here.
func PasskeyRefused() *Problem {
	return New("auth.passkey_refused", "That passkey was not accepted").
		WithCause("The passkey is not one this panel knows, or its answer did not check out.").
		WithImpact("You are not signed in.").
		WithFix("Try again, or sign in with your email address and password. After several failed attempts sign-in pauses for a while.").
		WithStatus(http.StatusUnauthorized)
}

// PasskeyExpired is an answer to a passkey challenge that is not waiting any
// more: it took longer than five minutes, it was answered already, or it was
// started in another browser.
func PasskeyExpired() *Problem {
	return New("auth.passkey_expired", "That passkey request is no longer waiting").
		WithCause("Each request works once, for five minutes, in the browser that started it. This one expired, was used already, or came from somewhere else.").
		WithImpact("Nothing was changed.").
		WithFix("Start again.").
		WithStatus(http.StatusBadRequest)
}

// PasskeyNotAdded is a new passkey whose answer did not check out.
func PasskeyNotAdded() *Problem {
	return New("auth.passkey_not_added", "The passkey could not be added").
		WithCause("The answer from your browser or security key did not check out, so it is not a passkey this panel can trust.").
		WithImpact("No passkey was added.").
		WithFix("Try again. If it keeps failing, open the panel at its Panel URL, or use another browser or security key.").
		WithDocs("/docs/configuration#passkeys").
		WithStatus(http.StatusBadRequest)
}

// PasskeyTaken is a credential already stored for an account.
func PasskeyTaken() *Problem {
	return New("auth.passkey_taken", "That passkey is already on an account").
		WithCause("This panel already has the passkey your device offered.").
		WithImpact("Nothing was added.").
		WithFix("Use the passkey you have, or make a new one on another device or security key.").
		WithStatus(http.StatusConflict)
}

// PasskeyLastWayIn is removing the passkey an account with no password and no
// single sign-on cannot do without.
func PasskeyLastWayIn() *Problem {
	return New("auth.passkey_last_way_in", "This passkey is this account's only way in").
		WithCause("The account has no password and no single sign-on, so without its last passkey nobody could sign in to it.").
		WithImpact("Nothing was changed.").
		WithFix("Add another passkey first. Whoever runs the server can also set a password with %s admin reset-password.", version.Binary).
		WithDocs("/docs/configuration#passkeys").
		WithStatus(http.StatusConflict)
}

// TunnelUnreachable is a tunnel to a database the panel cannot reach.
func TunnelUnreachable(database string) *Problem {
	return New("database.tunnel_unreachable", "The panel could not reach this database").
		WithCause("%s did not answer the panel, so there is nothing to tunnel to.", database).
		WithImpact("No connection was opened.").
		WithFix("Check the database is running on its page; one that is starting answers within a minute.").
		WithStatus(http.StatusBadGateway).
		WithDocs("/docs/cli#reaching-a-database")
}

// ScopedToProjects is a member limited to some of a team's projects asking
// for something that belongs to the whole team.
func ScopedToProjects() *Problem {
	return New("auth.scoped_to_projects", "Your access is limited to some projects").
		WithCause("You are in this team for some of its projects only, and this belongs to the whole team.").
		WithImpact("The action was not performed.").
		WithFix("Ask an admin of the team for access to the whole team, if you need this.").
		WithStatus(http.StatusForbidden)
}

// NotFound means the resource does not exist, or the caller may not see it.
func NotFound(kind, id string) *Problem {
	return Newf("resource.not_found", "That %s does not exist", kind).
		WithCause("No %s with the id %s is visible to you.", kind, id).
		WithImpact("Nothing was changed.").
		WithFix("Check the id, or go back to the list and pick it again.").
		WithStatus(http.StatusNotFound).
		With("kind", kind).With("id", id)
}

// NameTaken means an app or a database in the same environment already answers
// to this name.
//
// The two share a namespace and both create a Service under their own name, so
// this is a collision rather than a preference: the second one would take the
// first one's address over, and removing either would take the other's Service
// with it.
func NameTaken(kind, name string) *Problem {
	what := "An app"
	if kind == "database" {
		what = "A database"
	}
	return New("resource.name_taken", "That name is already used here").
		WithCause("%s in this environment is already called %s, and an app and a database "+
			"in one environment share an address.", what, name).
		WithImpact("Nothing was created.").
		WithFix("Pick a different name. Other environments are unaffected: the same name "+
			"in staging and in production is fine.").
		WithStatus(http.StatusConflict).
		With("name", name).With("taken_by", kind)
}

// DeployInterrupted means a deployment was cut off by a panel restart twice.
//
// The first restart is survived: the deployment is started again from where
// it stood (internal/deploy/resume.go). A second one is not, because a
// deployment the panel keeps dying during may be the reason it keeps dying.
func DeployInterrupted() *Problem {
	return New("deploy.interrupted", "The panel restarted twice during this deployment").
		WithCause("The panel restarted while this deployment was running, started it again, " +
			"and restarted again before it finished. It is not started a third time, in case " +
			"this deployment is why the panel keeps restarting.").
		WithImpact("Whatever had already been applied is still applied. Unless the new version " +
			"had finished rolling out, the version that was serving before is still serving.").
		WithFix("Deploy again. If the panel restarts during that one too, its own log says why: " +
			"kubectl -n skifity-system logs deploy/skifity-panel --previous").
		WithStatus(http.StatusConflict)
}

// ImageCollected means a version is too old to roll back to.
//
// The registry keeps the last few images for each app and collects the rest,
// because otherwise the disk fills. The record of the deployment is kept far
// longer, so this is not a missing record: it is a record whose image is gone.
func ImageCollected(number, kept int) *Problem {
	return New("deploy.image_collected", "That version is too old to roll back to").
		WithCause("Only the last %d versions of an app keep their image. Version %d is "+
			"further back than that, and its image was removed to keep the disk free.", kept, number).
		WithImpact("Nothing was changed. The version that is running now is still running.").
		WithFix("Roll back to one of the last %d versions, or deploy the commit you want "+
			"again, which builds it fresh.", kept).
		WithStatus(http.StatusConflict).
		With("version", fmt.Sprintf("%d", number))
}

// Conflict means a uniqueness rule or a state rule rejected the write.
func Conflict(cause, fix string) *Problem {
	return New("resource.conflict", "That name is already taken").
		WithCause("%s", cause).
		WithImpact("Nothing was changed.").
		WithFix("%s", fix).
		WithStatus(http.StatusConflict)
}

// RateLimited means too many attempts in too short a time.
func RateLimited(retryAfter string) *Problem {
	return New("auth.rate_limited", "Too many attempts").
		WithCause("There have been too many failed sign-in attempts for this account or from this address.").
		WithImpact("Sign-in is paused so that passwords cannot be guessed.").
		WithFix("Wait %s and try again. If this was not you, change your password once you can sign in.", retryAfter).
		WithStatus(http.StatusTooManyRequests).
		With("retry_after", retryAfter)
}

// --- SSH and provisioning ---

// SSHUnreachable means the TCP connection to the SSH port failed.
func SSHUnreachable(host string, port int, err error) *Problem {
	return New("ssh.unreachable", "Could not reach the server over SSH").
		WithCause("Nothing answered on %s port %d.", host, port).
		WithImpact("The server was not added. Nothing was changed on it.").
		WithFix("Check that the IP address and port are right, that the server is running, and that your provider's firewall allows inbound TCP on port %d. Many providers block everything by default in their control panel, which SSH cannot open from here.", port).
		WithDocs("/docs/adding-servers#when-a-step-fails").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("host", host).
		Wrap(err)
}

// SSHAuthFailed means the credentials were rejected.
func SSHAuthFailed(host, user string, usedKey bool) *Problem {
	method := "password"
	fix := "Check the username and password. If the server only allows key-based login, switch to the private key option."
	if usedKey {
		method = "private key"
		fix = "Check that this key is in ~/.ssh/authorized_keys for " + user + " on the server, and that the key has no passphrase (or supply it)."
	}
	return New("ssh.auth_failed", "The server refused those credentials").
		WithCause("Signing in as %s on %s with a %s was rejected.", user, host, method).
		WithImpact("The server was not added. Nothing was changed on it.").
		WithFix("%s", fix).
		WithDocs("/docs/adding-servers#the-password").
		WithStatus(http.StatusBadRequest).
		With("host", host).With("user", user).With("auth_method", method)
}

// SSHHostKeyChanged means the server's identity does not match what we stored.
// This is deliberately not retryable: it can mean an interception attempt.
func SSHHostKeyChanged(host, expected, got string) *Problem {
	return New("ssh.host_key_changed", "This server's identity has changed").
		WithCause("%s presented a different SSH host key than the one recorded when it was added.", host).
		WithImpact("The connection was refused. Skifity will not run commands on a server it cannot recognise.").
		WithFix("If you rebuilt or reinstalled this server, remove it from Skifity and add it again. If you did not, stop and investigate: something may be intercepting the connection.").
		WithDocs("/docs/adding-servers#when-a-step-fails").
		WithStatus(http.StatusConflict).
		WithSeverity(SeverityError).
		With("host", host).
		With("expected_fingerprint", expected).
		With("presented_fingerprint", got)
}

// PreflightFailed reports a server that does not meet requirements.
func PreflightFailed(code, detail, fix string, detailArgs, fixArgs []string) *Problem {
	p := New("preflight."+code, "This server is not ready to join").
		WithImpact("The server was not added. Nothing was changed on it.").
		WithDocs("/docs/quick-start#what-you-need").
		WithStatus(http.StatusBadRequest).
		Retry().
		With("check", code)
	// Set rather than formatted through WithCause. The preflight report has
	// already rendered these two sentences and kept the values that went into
	// them; running them back through "%s" would make the whole English
	// sentence the one argument, and the locale entry could then only be
	// "{{0}}" — which is the English again, in every language.
	p.Cause, p.Args.Cause = detail, detailArgs
	p.Fix, p.Args.Fix = fix, fixArgs
	return p
}

// PortBlocked reports a cluster port that could not be reached between nodes.
func PortBlocked(host string, port int, proto string) *Problem {
	return New("network.port_blocked", "A cluster port is blocked").
		WithCause("Cluster members could not reach %s on %s/%d.", host, proto, port).
		WithImpact("The node cannot join the cluster, or pods on it cannot talk to pods elsewhere.").
		WithFix("Open %s/%d between your servers. Skifity configures UFW and iptables on the server itself, but a firewall in your provider's control panel has to be opened there. Check the security group or firewall rules for this machine.", proto, port).
		WithDocs("/docs/adding-servers#when-a-step-fails").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("host", host).With("port", itoa(port)).With("protocol", proto)
}

// ServerNotOurs reports an operation that would need to reach a server Skifity
// did not install.
//
// The machine the panel runs on is adopted into the server list so that a fresh
// install does not open on "add your first server" while looking at a cluster
// that is already running. It is listed, and it is not managed: there is no key
// to it, nothing was installed on it, and pretending otherwise would end in an
// SSH failure that reads like a network problem.
func ServerNotOurs(name, action string) *Problem {
	return New("server.not_ours", "Skifity did not add this server").
		WithCause("%s is a node this cluster already had when Skifity was installed — usually the machine the panel itself runs on. There is no key to it and nothing of ours was put on it.", name).
		WithImpact("%s was not done.", action).
		WithFix("Change this machine from the machine itself. To take it out of the cluster entirely, run the uninstaller on it: `sudo sh /usr/local/bin/skifity-uninstall`. To add capacity instead, add a second server, which Skifity does install and can manage.").
		WithDocs("/docs/adding-servers#how-traffic-reaches-your-apps").
		WithStatus(http.StatusConflict).
		With("server", name)
}

// TokenNetworkRefused means an API token was used from an address outside
// the networks tokens are limited to.
func TokenNetworkRefused(ip string) *Problem {
	return New("auth.token_network", "API tokens are not accepted from this address").
		WithCause("This request came from %s, which is not on the list of networks API tokens may be used from.", ip).
		WithImpact("The request was refused. The token itself is still valid.").
		WithFix("Make the request from an address on the list, or ask a panel administrator to add this one under Settings, Sign-in.").
		WithDocs("/docs/configuration#api-tokens-only-from").
		WithStatus(http.StatusForbidden).
		With("ip", ip)
}

// ServerSSHFailed means the panel could not sign in to a server it added,
// for something done after it was added: a hardening check, a change to it.
func ServerSSHFailed(name string, err error) *Problem {
	return New("server.ssh_failed", "Could not sign in to this server").
		WithCause("Signing in to %s over SSH with the key Skifity installed on it did not work.", name).
		WithImpact("Nothing was checked or changed on the server. It keeps running, and so do its apps.").
		WithFix("Check that the server is up and that SSH is reachable from the panel. If somebody removed Skifity's key from ~/.ssh/authorized_keys, add it back, or remove the server and add it again.").
		WithDocs("/docs/adding-servers#hardening").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("server", name).
		Wrap(err)
}

// SSHHardeningRefused means turning SSH passwords off was not done, and why.
func SSHHardeningRefused(name, outcome string) *Problem {
	cause := "The change could not be confirmed on " + name + ", so it was undone."
	switch outcome {
	case "no_pubkey":
		cause = "The SSH daemon on " + name + " does not accept keys, so turning passwords off would leave no way in."
	case "no_dropins":
		cause = "The SSH daemon on " + name + " does not read /etc/ssh/sshd_config.d, so the change would not take."
	case "no_sshd":
		cause = "No SSH daemon was found on " + name + "."
	case "invalid":
		cause = "The SSH daemon on " + name + " refused the new configuration, so it was removed again."
	}
	return New("server.ssh_hardening_refused", "SSH passwords were not turned off").
		WithCause("%s", cause).
		WithImpact("SSH on the server is as it was: nothing was changed.").
		WithFix("Set PasswordAuthentication no and KbdInteractiveAuthentication no in /etc/ssh/sshd_config on the server by hand, check it with sshd -t, and reload ssh.").
		WithDocs("/docs/adding-servers#hardening").
		WithStatus(http.StatusConflict).
		With("server", name).
		With("outcome", outcome)
}

// K3sInstallFailed reports a failed k3s installation on a node.
func K3sInstallFailed(host string, exitCode int, output string) *Problem {
	return New("k3s.install_failed", "Kubernetes could not be installed on this server").
		WithCause("The k3s installer exited with code %d on %s.", exitCode, host).
		WithImpact("The server is registered but is not part of the cluster. No workloads are running on it.").
		WithFix("Open the step output below. The most common causes are no outbound internet access, an old kernel without the required modules, and a conflicting container runtime. Fix the cause and press Retry; the step is safe to run again.").
		WithDocs("/docs/adding-servers#when-a-step-fails").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("host", host).With("exit_code", itoa(exitCode)).With("output_tail", tail(output, 2000))
}

// ClusterTokenMissing reports that the panel cannot add a server to the cluster
// it is already running in, because it does not know that cluster's join token.
//
// This is a dead end worth being loud about. Inventing a token would build a
// second, separate cluster on the new server, which looks like it worked until
// somebody wonders why their app is not running anywhere.
func ClusterTokenMissing(path string) *Problem {
	if path == "" {
		path = version.ConfigDir + "/cluster-token"
	}
	return New("cluster.token_missing", "Skifity does not have this cluster's join token").
		WithCause("The panel is running in a Kubernetes cluster it did not create, and a server can only join that cluster with the token it was started with.").
		WithImpact("No server can be added. Nothing was changed on the server you were adding.").
		WithFix("On the server the panel runs on, copy the token into place and restart the panel:\n\n  sudo cp /var/lib/rancher/k3s/server/token %s\n  sudo chmod 600 %s", path, path).
		WithDocs("/docs/adding-servers#when-a-step-fails").
		WithStatus(http.StatusPreconditionFailed).
		With("path", path)
}

// --- servers created at a cloud provider ---

// cloudDocs is where every cloud problem points: the section on creating a
// server at a provider, which covers the token, the host key and deleting.
const cloudDocs = "/docs/adding-servers#creating-a-server-at-a-cloud-provider"

// CloudTokenInvalid is a provider refusing a token outright.
func CloudTokenInvalid(provider string) *Problem {
	return New("cloud.token_invalid", "The cloud provider does not accept that token").
		WithCause("%s answered that the token is invalid or unknown.", provider).
		WithImpact("Nothing was saved or created.").
		WithFix("Create an API token that can write in the provider's console — for Hetzner Cloud, the project's Security page, then API tokens; for DigitalOcean, API, then Tokens — and use that one.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusBadRequest).
		With("provider", provider)
}

// CloudTokenReadOnly is a token that can list what a provider sells and
// cannot order any of it.
func CloudTokenReadOnly(provider string) *Problem {
	return New("cloud.token_read_only", "That token can only read").
		WithCause("%s accepts the token for reading only, and creating a server is a write.", provider).
		WithImpact("Nothing was saved or created.").
		WithFix("Create a token that can write — Read & Write at Hetzner Cloud, Full Access at DigitalOcean — in the account the servers should go in, and use that one instead.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusBadRequest).
		With("provider", provider)
}

// CloudRateLimited is a provider asking the panel to slow down.
func CloudRateLimited(provider string) *Problem {
	return New("cloud.rate_limited", "The cloud provider asked the panel to slow down").
		WithCause("%s allows a limited number of requests an hour for each project, and this one has used them.", provider).
		WithImpact("The request was not made.").
		WithFix("Wait a few minutes and try again. Other tools that use the same project's token count against the same limit.").
		WithStatus(http.StatusTooManyRequests).
		Retry().
		With("provider", provider)
}

// CloudUnreachable is a provider's API that did not answer.
func CloudUnreachable(provider string, err error) *Problem {
	return New("cloud.unreachable", "The cloud provider could not be reached").
		WithCause("The panel could not reach %s's API: %s", provider, err.Error()).
		WithImpact("Nothing was changed.").
		WithFix("Check that the panel's server has outbound HTTPS access, then try again.").
		WithStatus(http.StatusBadGateway).
		Retry().
		Wrap(err).
		With("provider", provider)
}

// CloudRequestFailed is a provider refusing a request, in its own words.
func CloudRequestFailed(provider, code, message string, theirFault bool) *Problem {
	status := http.StatusBadRequest
	if theirFault {
		status = http.StatusBadGateway
	}
	p := New("cloud.request_failed", "The cloud provider refused the request").
		WithCause("%s answered %s: %s", provider, code, message).
		WithImpact("Nothing was changed by this request.").
		WithFix("The provider's own message says what it objected to. Correct that and try again.").
		WithStatus(status).
		With("provider", provider).
		With("provider_code", code)
	// Their fault is worth trying again; a refusal of what was sent is not.
	p.Retryable = theirFault
	return p
}

// CloudLimitReached is a provider account at one of its limits.
func CloudLimitReached(provider, message string) *Problem {
	return New("cloud.limit_reached", "The cloud account has reached a limit").
		WithCause("%s refused: %s", provider, message).
		WithImpact("No server was created.").
		WithFix("Ask the provider to raise the limit — Hetzner Cloud takes a request under the project's Limits, DigitalOcean under the account's droplet limit — or delete a server that is no longer needed.").
		WithStatus(http.StatusConflict).
		With("provider", provider)
}

// CloudUnavailable is a server type a location cannot supply right now.
func CloudUnavailable(provider, message string) *Problem {
	return New("cloud.unavailable", "That server type is not available there right now").
		WithCause("%s answered: %s", provider, message).
		WithImpact("No server was created.").
		WithFix("Pick another location or another server type, and try again.").
		WithStatus(http.StatusConflict).
		With("provider", provider)
}

// CloudProviderInUse is a connection with servers the panel created through it.
func CloudProviderInUse(count int) *Problem {
	return New("cloud.provider_in_use", "Servers were created with this connection").
		WithCause("%d server(s) in the panel were created with this connection, and deleting one of their machines needs it.", count).
		WithImpact("The connection was not removed.").
		WithFix("Remove those servers first, deleting their machines or keeping them. To use a new token, add a second connection.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusConflict)
}

// CloudNameTaken is a name the cluster already has a node or a server under.
func CloudNameTaken(name string) *Problem {
	return New("cloud.name_taken", "There is already a server with that name").
		WithCause("%s is already the name of a server or a node in this cluster. A node is named after its machine, and two nodes cannot share a name.", name).
		WithImpact("Nothing was created.").
		WithFix("Choose another name.").
		WithStatus(http.StatusConflict).
		With("name", name)
}

// CloudMachineGone is a machine the panel created that the provider no
// longer has.
func CloudMachineGone(provider, id, server string) *Problem {
	return New("cloud.machine_gone", "The machine is no longer at the cloud provider").
		WithCause("%s has no machine with the id %s, which is the one the panel created for %s.", provider, id, server).
		WithImpact("The server cannot be set up. The panel does not order a second machine on its own.").
		WithFix("Remove this server in the panel, then create it again.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusConflict).
		With("machine_id", id)
}

// CloudMachineNotOurs is a machine that does not carry the label the panel
// put on the one it created, which is the check before anything is deleted.
func CloudMachineNotOurs(provider, id, server string) *Problem {
	return New("cloud.machine_not_ours", "That machine was not created by this panel").
		WithCause("The machine %s at %s does not carry the label the panel put on the one it created for %s.", id, provider, server).
		WithImpact("Nothing was deleted. The server left the cluster; the machine is still running, and still billed.").
		WithFix("Look at the machine in the provider's console, and delete it there if it should go.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusConflict).
		With("machine_id", id)
}

// CloudNotCreated is a request to delete the machine of a server the panel
// did not create.
func CloudNotCreated(server string) *Problem {
	return New("cloud.not_created", "Skifity did not create this server's machine").
		WithCause("%s was added with an address and SSH access, not created at a cloud provider by the panel.", server).
		WithImpact("Nothing was removed.").
		WithFix("Remove it without deleting the machine, then delete the machine yourself at your provider if it is no longer needed.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusConflict).
		With("server", server)
}

// CloudBootTimeout is a machine that did not come up.
func CloudBootTimeout(provider, server string, waited time.Duration) *Problem {
	return New("cloud.boot_timeout", "The new machine did not start").
		WithCause("%s did not report %s as running with an address within %s.", provider, server, waited).
		WithImpact("The machine exists at the provider and is billed, but it has not joined the cluster.").
		WithFix("Press Retry to keep waiting. If it still does not start, look at it in the provider's console, and remove the server here with its machine.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusGatewayTimeout).
		Retry()
}

// CloudSSHTimeout is a running machine that never let the panel in.
func CloudSSHTimeout(server string, waited time.Duration, err error) *Problem {
	reason := "it did not answer"
	if err != nil {
		reason = err.Error()
	}
	return New("cloud.ssh_timeout", "The new machine did not answer over SSH").
		WithCause("%s is running, but did not accept the panel's key on port 22 within %s: %s", server, waited, reason).
		WithImpact("The machine exists and is billed, but nothing was installed on it.").
		WithFix("Press Retry. When SSH is open to the cluster's servers only, the panel has to run inside the cluster to reach it; otherwise remove the server with its machine and create it again with SSH open.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusGatewayTimeout).
		Retry()
}

// CloudHostKeyMismatch is a new machine presenting a host key other than the
// one the panel generated for it. No credentials were sent: the host key is
// checked before the panel authenticates.
func CloudHostKeyMismatch(server, expected, presented string) *Problem {
	return New("cloud.host_key_mismatch", "The new machine did not present the host key the panel gave it").
		WithCause("The panel generated %s's SSH host key and passed it to the machine when it was created, and the machine presented %s instead.", server, presented).
		WithImpact("The panel refused the connection before signing in, and sent no credentials. Nothing was installed.").
		WithFix("Something may be answering for this address, or the image ignored the key. Remove the server with its machine and create it again; if it happens again, stop and find out what is answering.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusConflict).
		With("expected_fingerprint", expected).
		With("presented_fingerprint", presented)
}

// CloudHostKeyNotReplaced is the step after the first connection failing:
// the bootstrap host key could not be swapped for one the machine made.
func CloudHostKeyNotReplaced(server, reason string) *Problem {
	return New("cloud.host_key_not_replaced", "The machine's host key could not be replaced").
		WithCause("Replacing the SSH host key on %s failed: %s", server, reason).
		WithImpact("The machine still answers with the key the panel gave it at creation, which the provider also stores. Nothing else was installed.").
		WithFix("Press Retry; this step is safe to repeat.").
		WithDocs(cloudDocs).
		WithStatus(http.StatusBadGateway).
		Retry()
}

// CloudCreateFailed is the CLI following a created server to a failure.
func CloudCreateFailed(server, reason string) *Problem {
	return New("cloud.create_failed", "The new server did not join the cluster").
		WithCause("Creating %s stopped: %s", server, reason).
		WithImpact("The machine may exist at the provider, and be billed, without being in the cluster.").
		WithFix("Open the server in the panel and press Retry, or remove it there and delete its machine too.").
		WithDocs(cloudDocs).
		Retry()
}

// --- builds and deploys ---

// BuildFailed reports a build that did not produce an image.
func BuildFailed(app, stage, logTail string) *Problem {
	return New("build.failed", "The build failed").
		WithCause("Building %s failed during the %s stage.", app, stage).
		WithImpact("The new version was not deployed. The previous version is still running and still serving traffic.").
		WithFix("Read the build log below. Then fix it in your repository and push again, or press Retry if you believe it was a transient failure.").
		WithDocs("/docs/troubleshooting#a-deployment-failed").
		WithStatus(http.StatusBadRequest).
		Retry().
		With("app", app).With("stage", stage).With("log_tail", tail(logTail, 4000))
}

// NoBuilderDetected reports a repository we cannot work out how to build.
func NoBuilderDetected(repo string) *Problem {
	return New("build.no_builder", "Skifity could not work out how to build this repository").
		WithCause("No Dockerfile was found in %s, and the files present do not match any language Skifity recognises.", repo).
		WithImpact("No build was started.").
		WithFix("Add a Dockerfile to the repository, or set the root directory if your app lives in a subfolder of a monorepo, or choose a prebuilt image instead.").
		WithDocs("/docs/troubleshooting#a-deployment-failed").
		WithStatus(http.StatusBadRequest).
		With("repository", repo)
}

// RolloutTimedOut reports a deploy whose pods never became ready.
func RolloutTimedOut(app string, ready, want int, reason string) *Problem {
	return New("deploy.rollout_timeout", "The new version did not start").
		WithCause("%d of %d instances of %s became ready before the timeout. %s", ready, want, app, reason).
		WithImpact("Kubernetes kept the previous version running, so your app is still up. The new version was not rolled out.").
		WithFix("Check the app logs for a crash on startup. The usual causes are a missing environment variable, a health check path that does not exist yet, a port mismatch between the app and the configured port, and an app that needs longer to start than its time to start under Settings allows.").
		WithDocs("/docs/troubleshooting#a-deployment-failed").
		WithStatus(http.StatusGatewayTimeout).
		Retry().
		With("app", app).With("ready_instances", itoa(ready)).With("wanted_instances", itoa(want))
}

// ImagePullFailed reports a node that could not pull the image.
func ImagePullFailed(image, reason string) *Problem {
	return New("deploy.image_pull_failed", "The image could not be pulled").
		WithCause("Pulling %s failed: %s", image, reason).
		WithImpact("The new instances cannot start. The previous version is still running.").
		WithFix("If this is a private image, add the registry credentials in Settings. If it is an internal build, the in-cluster registry may not be reachable from this node: check that the node joined the cluster network.").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("image", image)
}

// CrashLoop reports an app whose container keeps exiting.
func CrashLoop(app string, restarts int, logTail string) *Problem {
	return New("app.crash_loop", "This app keeps restarting").
		WithCause("%s has restarted %d times in a row. Kubernetes is backing off between restarts.", app, restarts).
		WithImpact("The app is not serving traffic reliably.").
		WithFix("Read the last log lines below: the cause is almost always in them. Missing environment variables, a database that is not reachable, and a port the app does not actually listen on are the usual three.").
		WithDocs("/docs/troubleshooting#an-app-is-crashing").
		WithStatus(http.StatusBadGateway).
		With("app", app).With("restarts", itoa(restarts)).With("log_tail", tail(logTail, 4000))
}

// --- scanning images for vulnerabilities ---

// VulnerableImage is a deploy stopped because its image has a critical
// vulnerability with a fixed version, and the panel is set to stop those.
func VulnerableImage(app string, count int, packages string) *Problem {
	return New("deploy.vulnerable", "This image has critical vulnerabilities that have a fix").
		WithCause("%d critical vulnerabilities in the image of %s have a fixed version: %s.", count, app, packages).
		WithImpact("Nothing was deployed. The version running now keeps running.").
		WithFix("Move to the fixed versions — usually a newer base image or dependency — and deploy again. "+
			"To deploy this image anyway, deploy it with the vulnerabilities accepted: Deploy anyway on the stopped "+
			"deployment, or `%s deploy --accept-vulnerabilities`. That is recorded in the activity log.", version.Binary).
		WithDocs("/docs/concepts#stopping-deploys-that-have-a-fix-waiting").
		WithStatus(http.StatusConflict).
		With("app", app).With("fixable_critical", itoa(count))
}

// ScanDatabaseUnavailable is a scanner that could not download the database
// of known vulnerabilities it compares an image against.
func ScanDatabaseUnavailable(detail string) *Problem {
	return New("scan.database_unavailable", "The vulnerability database could not be downloaded").
		WithCause("The scanner could not fetch its database: %s", detail).
		WithImpact("This image was not scanned. Deploys are never held up by a scan that could not run.").
		WithFix("The cluster needs to reach mirror.gcr.io or ghcr.io over HTTPS, where the database is published. " +
			"On a cluster with no way out to the internet, turn scanning off under Settings, Image scanning.").
		WithDocs("/docs/concepts#without-the-internet").
		WithStatus(http.StatusBadGateway).
		Retry()
}

// ScanPullFailed is a scanner that could not read the image it was given.
func ScanPullFailed(image, detail string) *Problem {
	return New("scan.pull_failed", "The scanner could not read the image").
		WithCause("Reading %s failed: %s", image, detail).
		WithImpact("This image was not scanned. The app is not affected.").
		WithFix("For an image in a private registry, add the registry's credentials under Settings, Git, Private registries. "+
			"For an image the panel built, deploy the app again: the registry keeps the last ten images of an app.").
		WithDocs("/docs/concepts#private-registries").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("image", image)
}

// ScanTimedOut is a scan that did not finish in the time it has.
func ScanTimedOut(image string) *Problem {
	return New("scan.timeout", "The scan took too long and was stopped").
		WithCause("Scanning %s did not finish within fifteen minutes.", image).
		WithImpact("This image was not scanned. The app is not affected.").
		WithFix("A very large image, or a slow way out to the internet the first time the database is downloaded, "+
			"can take this long. Scan it again: the database is kept, so a second scan is quicker.").
		WithDocs("/docs/concepts#scanning-every-image-for-vulnerabilities").
		WithStatus(http.StatusGatewayTimeout).
		Retry().
		With("image", image)
}

// ScanFailed is a scan that ended without a report for any other reason.
func ScanFailed(image, detail string) *Problem {
	return New("scan.failed", "The image could not be scanned").
		WithCause("Scanning %s failed: %s", image, detail).
		WithImpact("This image was not scanned. The app is not affected, and deploys are never held up by it.").
		WithFix("Scan it again. If it fails the same way, copy this error and open an issue.").
		WithDocs("/docs/concepts#scanning-every-image-for-vulnerabilities").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("image", image)
}

// ScanReportUnreadable is a scan whose report the panel could not read.
func ScanReportUnreadable(detail string) *Problem {
	return New("scan.report_unreadable", "The scan's report could not be read").
		WithCause("The scanner finished, and what it printed was not a report the panel can read: %s", detail).
		WithImpact("Nothing was recorded for this scan. The app is not affected.").
		WithFix("Scan it again. If it keeps happening, copy this error and open an issue: it is a bug in Skifity.").
		WithStatus(http.StatusBadGateway).
		Retry()
}

// ScanInterrupted is a scan the panel stopped in the middle of by restarting.
func ScanInterrupted() *Problem {
	return New("scan.interrupted", "The panel restarted during this scan").
		WithCause("The scan was queued or running when the panel stopped.").
		WithImpact("This image was not scanned this time. The app is not affected.").
		WithFix("Scan it again with Scan now, or wait for the next scheduled scan.")
}

// ScanningDisabled is a scan asked for while scanning is switched off.
func ScanningDisabled() *Problem {
	return New("scan.disabled", "Image scanning is switched off").
		WithCause("An administrator turned vulnerability scanning off for this panel.").
		WithImpact("Nothing was scanned.").
		WithFix("Turn it on under Settings, Image scanning.").
		WithDocs("/docs/concepts#scanning-every-image-for-vulnerabilities").
		WithStatus(http.StatusConflict)
}

// NothingToScan is a scan asked for of an app that has never been deployed.
func NothingToScan(app string) *Problem {
	return New("scan.no_image", "This app has no image to scan yet").
		WithCause("%s has not been deployed, so there is no image to look at.", app).
		WithImpact("Nothing was scanned.").
		WithFix("Deploy it. Its image is scanned once it is built.").
		WithStatus(http.StatusConflict).
		With("app", app)
}

// --- deploying a folder ---

// NoUpload reports a deploy of an app whose code comes from uploads, before
// any code was sent.
func NoUpload(app string) *Problem {
	return New("upload.none", "There is no code to deploy yet").
		WithCause("%s deploys a folder from somebody's computer, and nothing has been sent yet.", app).
		WithImpact("Nothing was built or deployed.").
		WithFix("Press Send a new version on the app's page and pick its folder, or run `skifity up` in the folder.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusConflict).
		With("app", app)
}

// NixpacksUnavailable reports an app set to build with Nixpacks.
//
// The builder was offered and could never have run: the only image its makers
// publish is the base image their builds start from, which has no nixpacks
// command in it, and they have since replaced Nixpacks with Railpack.
func NixpacksUnavailable(app string) *Problem {
	return New("build.nixpacks_unavailable", "Nixpacks is not available").
		WithCause("%s is set to build with Nixpacks. Its makers replaced it with Railpack, and its command line is not published as an image a build can run.", app).
		WithImpact("Nothing was built or deployed.").
		WithFix("Set the app's builder to Automatic, which uses Railpack, or to Dockerfile, under the app's Settings.").
		WithStatus(http.StatusBadRequest).
		With("app", app)
}

// UploadNotFound reports a deploy that names an upload the panel does not have.
func UploadNotFound(sha string) *Problem {
	return New("upload.not_found", "That upload is not on the panel").
		WithCause("No upload with the hash %s belongs to this app. The panel keeps each app's ten newest uploads.", sha).
		WithImpact("Nothing was built or deployed.").
		WithFix("Send the folder again, from the app's page or with `skifity up`.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusNotFound).
		With("sha", sha)
}

// The ways an upload is refused once it has been read. Each is its own entry,
// rather than one entry with the reason as an argument, so the reason can be
// read in the language of whoever sent it.

// UploadNotAnArchive reports something that is not a whole gzipped tar.
func UploadNotAnArchive() *Problem {
	return New("upload.not_an_archive", "The panel could not read this upload").
		WithCause("What was sent is not a whole gzipped tar archive.").
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("Send the folder with `skifity up`, which packs it the way the panel reads it. If you did, the transfer was probably cut off, so run it again.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusBadRequest)
}

// UploadUnsafeEntry reports an entry that could be unpacked outside its folder.
func UploadUnsafeEntry(entry string) *Problem {
	return New("upload.unsafe_entry", "The upload has a file the panel will not unpack").
		WithCause("%s is a link, a special file or a path that leads outside the folder, and unpacking it could write somewhere it should not.", entry).
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("Remove it from the folder, or list it in .skifityignore so it is not sent.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusBadRequest).
		With("entry", entry)
}

// UploadSecretsFile reports a .env with real values in an upload.
func UploadSecretsFile(entry string) *Problem {
	return New("upload.secrets_file", "The upload has a .env file in it").
		WithCause("%s holds the app's real settings. A build puts every file it is given into the image, where anyone who can pull the image could read them.", entry).
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("Set those values under the app's Variables instead. `skifity up` leaves .env files out by itself, so this one was sent some other way.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusBadRequest).
		With("entry", entry)
}

// UploadTooManyFiles reports an upload with more files than source code has.
func UploadTooManyFiles(limit int) *Problem {
	return New("upload.too_many_files", "The upload has too many files").
		WithCause("It holds more than %d files, which source code almost never does.", limit).
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("A dependency folder such as node_modules or a virtualenv is probably in it. List it in .skifityignore: the build installs dependencies itself.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusRequestEntityTooLarge)
}

// UploadUnpacksTooLarge reports a small archive that expands into a lot.
func UploadUnpacksTooLarge(limitMB int64) *Problem {
	return New("upload.unpacks_too_large", "The upload is too large once unpacked").
		WithCause("Its contents add up to more than %d MB.", limitMB).
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("Build output, a dependency folder or a data file is probably in it. List it in .skifityignore and send it again.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusRequestEntityTooLarge)
}

// UploadEmpty reports an archive with no files in it.
func UploadEmpty() *Problem {
	return New("upload.empty", "The upload has no files in it").
		WithCause("The archive was read to the end and held folders at most.").
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("Run `skifity up` from inside the app's folder, or name the folder: `skifity up ./my-app`.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusBadRequest)
}

// UploadTooLarge reports an archive over the size the panel accepts.
func UploadTooLarge(limitMB int64) *Problem {
	return New("upload.too_large", "The upload is too large").
		WithCause("The panel accepts up to %d MB of compressed code, and this was more.", limitMB).
		WithImpact("Nothing was stored, built or deployed.").
		WithFix("Something that is not source code is probably in the folder, such as a dependency folder, build output or a database file. Add it to .skifityignore and send it again.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusRequestEntityTooLarge)
}

// NotAnUploadApp reports code sent to an app that builds from somewhere else.
func NotAnUploadApp(app string) *Problem {
	return New("upload.wrong_source", "This app does not deploy uploaded code").
		WithCause("%s builds from a repository or an image, so code sent to it would never be used.", app).
		WithImpact("Nothing was stored.").
		WithFix("Push to the repository instead, or create a new app for the folder with `skifity up --new`.").
		WithDocs("/docs/cli#deploying-a-folder").
		WithStatus(http.StatusConflict).
		With("app", app)
}

// UploadDeliveryFailed reports code that could not be handed to the build.
func UploadDeliveryFailed(detail string) *Problem {
	return New("upload.delivery_failed", "The code could not be handed to the build").
		WithCause("The panel could not send the uploaded code into the build: %s", detail).
		WithImpact("Nothing was built or deployed. The previous version is still running.").
		WithFix("Press Retry. If it fails again, check that the panel can reach the cluster's API and that the build pod started.").
		WithDocs("/docs/troubleshooting#a-deployment-failed").
		WithStatus(http.StatusBadGateway).
		Retry()
}

// CLIPlatformUnknown is a download for a platform no CLI is built for.
func CLIPlatformUnknown(platform string) *Problem {
	return New("cli.platform_unknown", "There is no command line tool for that platform").
		WithCause("%s is not a platform the command line tool is built for. It is built for linux, darwin (macOS) and windows, on amd64 and arm64.", platform).
		WithImpact("Nothing was downloaded.").
		WithFix("Ask for one of those, for example ?os=darwin&arch=arm64 for a Mac with Apple silicon.").
		WithDocs("/docs/cli#getting-it").
		WithStatus(http.StatusBadRequest)
}

// CLIPlatformUnavailable is a platform this panel was not given a build for.
func CLIPlatformUnavailable(platform, available string) *Problem {
	return New("cli.platform_unavailable", "This panel does not have the command line tool for that platform").
		WithCause("There is no build for %s beside this panel. It has: %s.", platform, available).
		WithImpact("Nothing was downloaded. Deploying a folder from the panel's own New app page works without it.").
		WithFix("Build it with `make release` and copy it into the panel's CLI directory, or use the panel's image, which carries every platform.").
		WithDocs("/docs/cli#getting-it").
		WithStatus(http.StatusNotFound)
}

// APIDescriptionUnreadable is the OpenAPI description built into the binary
// failing to convert. Only a broken build can cause it: the file is not read
// from disk, and a test parses it before anything ships.
func APIDescriptionUnreadable(err error) *Problem {
	return New("api.description_unreadable", "The API description could not be read").
		WithCause("The OpenAPI description built into this panel is not valid YAML: %s", err).
		WithImpact("Only the description is missing. Every route it describes works as before.").
		WithFix("This build of Skifity is faulty. Report it with the version /api/meta names; the routes are also listed in llms.txt.").
		WithDocs("/docs/cli#the-openapi-description").
		WithStatus(http.StatusInternalServerError).
		Wrap(err)
}

// --- domains and TLS ---

// DNSNotPointing reports a custom domain whose DNS does not resolve to us.
func DNSNotPointing(hostname, want, got string) *Problem {
	return New("domain.dns_mismatch", "This domain does not point here yet").
		WithCause("%s currently resolves to %s, but it needs to resolve to %s.", hostname, orNone(got), want).
		WithImpact("The certificate cannot be issued and the domain will not serve your app.").
		WithFix("Create an A record for %s pointing to %s, then wait for it to propagate. Skifity checks again every minute.", hostname, want).
		WithDocs("/docs/troubleshooting#a-domain-does-not-work").
		WithStatus(http.StatusBadRequest).
		WithSeverity(SeverityWarning).
		Retry().
		With("hostname", hostname).With("expected", want).With("actual", orNone(got))
}

// DNSLookupFailed is a DNS check that got no answer at all: not "there is no
// record", which is an answer, but a resolver that timed out or failed.
func DNSLookupFailed(hostname string, err error) *Problem {
	return New("domain.dns_lookup_failed", "The DNS check did not get an answer").
		WithCause("Looking up %s did not finish: the DNS server the panel asks timed out or refused.", hostname).
		WithImpact("Nothing was changed. The domain is still attached, and whether it points here is not known yet.").
		WithFix("Try again in a minute. If it keeps failing, check %s from your own computer with `dig +short %s`; a panel whose server cannot resolve names at all has a DNS problem of its own.", hostname, hostname).
		WithDocs("/docs/troubleshooting#a-domain-does-not-work").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("hostname", hostname).
		Wrap(err)
}

// --- DNS providers ---
//
// A team connects Cloudflare, Hetzner, DigitalOcean or Route 53, and the panel
// creates a domain's record there. Every one of these is about a record the
// panel did not make, or a provider that would not do what it was asked: the
// panel never overwrites a record it did not create, so most of them end with
// a person deciding.

// DNSProviderRefused is a provider turning a connection's credentials down.
func DNSProviderRefused(provider, detail string) *Problem {
	return New("dns.provider_refused", "The DNS provider refused the credentials").
		WithCause("%s answered: %s", provider, orNone(detail)).
		WithImpact("Nothing was saved and no record was changed.").
		WithFix("Check that it is the whole token, that it has not expired, and that it may edit DNS: Zone, DNS, Edit and Zone, Zone, Read at "+
			"Cloudflare; Read & Write at Hetzner; the domain scopes at DigitalOcean; route53:ListHostedZones, "+
			"route53:ListResourceRecordSets and route53:ChangeResourceRecordSets at Route 53.").
		WithDocs("/docs/concepts#connecting-a-dns-provider").
		WithStatus(http.StatusBadRequest).
		With("provider", provider)
}

// DNSProviderFailed is a provider that could not be reached, or answered
// with a failure of its own.
func DNSProviderFailed(provider string, err error) *Problem {
	return New("dns.provider_failed", "The DNS provider did not do what it was asked").
		WithCause("Asking %s failed: %s", provider, err.Error()).
		WithImpact("Nothing was changed at the provider. A record the panel keeps is tried again every few minutes.").
		WithFix("Check the provider's status page, then test the connection under Settings, DNS providers. A domain's record can be tried again from its Domains tab.").
		WithDocs("/docs/troubleshooting#the-panel-did-not-create-a-dns-record").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("provider", provider).
		Wrap(err)
}

// DNSNoZones is credentials the provider accepts and that can see no zone.
func DNSNoZones(provider string) *Problem {
	return New("dns.no_zones", "These credentials can see no zones").
		WithCause("%s accepted them and listed no zones.", provider).
		WithImpact("Nothing was saved: a connection with no zones would never be used.").
		WithFix("Give the token the zones it should manage — at Cloudflare, under Zone Resources, include the zones or every zone of the account — and connect it again.").
		WithDocs("/docs/concepts#connecting-a-dns-provider").
		WithStatus(http.StatusBadRequest).
		With("provider", provider)
}

// DNSGlobalKey is Cloudflare's Global API Key pasted where a token belongs.
func DNSGlobalKey() *Problem {
	return New("dns.cloudflare_global_key", "That is Cloudflare's Global API Key").
		WithCause("The value has the shape of the Global API Key, which can do anything in the Cloudflare account: delete zones, change the plan, read every setting.").
		WithImpact("It was not sent anywhere and nothing was saved.").
		WithFix("Create an API token instead: My Profile, API Tokens, Create Token, the Edit zone DNS template, with the zones this panel should manage. Paste that token.").
		WithDocs("/docs/concepts#connecting-a-dns-provider").
		WithStatus(http.StatusBadRequest)
}

// DNSRecordConflict is a record somebody else made standing where the one the
// panel would create goes, saying something else.
func DNSRecordConflict(hostname, found, provider, want string) *Problem {
	return New("dns.record_conflict", "Another record is in the way").
		WithCause("%s already has %s at %s, which Skifity did not create.", hostname, found, provider).
		WithImpact("Nothing was created or changed. Skifity never changes a record it did not create, so the name keeps sending visitors where that record says.").
		WithFix("If nothing needs that record any more, delete it at %s and press Try again. Or change it yourself to %s: a record that already says that is left to you, and the domain works either way.", provider, want).
		WithDocs("/docs/troubleshooting#the-panel-did-not-create-a-dns-record").
		WithStatus(http.StatusConflict).
		With("hostname", hostname).With("found", found).With("wanted", want)
}

// DNSRecordExtra is an address of a type the panel is not creating, made by
// somebody else: an old AAAA left beside the new A is the usual one.
func DNSRecordExtra(hostname, found, provider string) *Problem {
	return New("dns.record_extra", "An old address would still get some visitors").
		WithCause("%s also has %s at %s, which Skifity did not create and is not this cluster's.", hostname, found, provider).
		WithImpact("Nothing was created. With that record there, some visitors would reach this app and some would be sent there instead.").
		WithFix("Delete that record at %s if it is left over, and press Try again. If it is this cluster's own IPv6 address, an administrator sets it under Settings, Domains, Cluster public IPv6 address, and the panel keeps the AAAA record itself.", provider).
		WithDocs("/docs/troubleshooting#the-panel-did-not-create-a-dns-record").
		WithStatus(http.StatusConflict).
		With("hostname", hostname).With("found", found)
}

// DNSRecordChanged is a record the panel made that somebody changed since.
func DNSRecordChanged(hostname, kind, provider, now string) *Problem {
	return New("dns.record_changed", "Somebody changed the record Skifity created").
		WithCause("The %s record Skifity created for %s at %s now says %s.", kind, hostname, provider, now).
		WithImpact("It was changed outside the panel, so it is not the panel's any more: Skifity stopped keeping it, and will neither change nor delete it.").
		WithFix("If the change was a mistake, delete the record at %s and press Try again, and the panel creates it afresh. If it was meant, press Stop keeping the record on the domain.", provider).
		WithDocs("/docs/troubleshooting#the-panel-did-not-create-a-dns-record").
		WithStatus(http.StatusConflict).
		With("hostname", hostname).With("found", now)
}

// DNSNoZone is a record asked for at a hostname no connected zone covers.
func DNSNoZone(hostname string) *Problem {
	return New("dns.no_zone", "No connected DNS zone covers this domain").
		WithCause("None of the team's DNS providers has a zone that %s is in.", hostname).
		WithImpact("Nothing was changed.").
		WithFix("Connect the provider that serves %s under Settings, DNS providers, or create the record yourself: the Domains tab shows what it has to say.", hostname).
		WithDocs("/docs/concepts#connecting-a-dns-provider").
		WithStatus(http.StatusBadRequest).
		With("hostname", hostname)
}

// DNSNoAddress is a record the panel cannot write because it does not know
// where the cluster is.
func DNSNoAddress(hostname string) *Problem {
	return New("dns.no_address", "The panel does not know where this domain should point").
		WithCause("No public address is set for this cluster and none of its servers has one the panel knows, so there is nothing to put in the record for %s.", hostname).
		WithImpact("No record was created.").
		WithFix("An administrator sets the address under Settings, Domains, Cluster public IP, and the record is created within a few minutes.").
		WithDocs("/docs/troubleshooting#the-panel-did-not-create-a-dns-record").
		WithStatus(http.StatusConflict).
		With("hostname", hostname)
}

// DNSTunnelNeedsCloudflare is a record for a Cloudflare tunnel asked for in a
// zone somewhere else, where it would not work.
func DNSTunnelNeedsCloudflare(hostname, provider string) *Problem {
	return New("dns.tunnel_needs_cloudflare", "A Cloudflare tunnel is reached only through Cloudflare's DNS").
		WithCause("This cluster is reached through a Cloudflare tunnel, and %s is in a zone at %s. A record pointing at a tunnel only works in a zone that is on Cloudflare.", hostname, provider).
		WithImpact("No record was created.").
		WithFix("Move the zone's DNS to Cloudflare and connect it here, or add %s as a public hostname on the tunnel in the Cloudflare dashboard.", hostname).
		WithDocs("/docs/concepts#cloudflares-proxy-and-tunnels").
		WithStatus(http.StatusConflict).
		With("hostname", hostname)
}

// DNSApexCNAME is a zone's own name that would need a CNAME, which DNS
// allows nowhere but at Cloudflare, which flattens it.
func DNSApexCNAME(hostname, target, provider string) *Problem {
	return New("dns.apex_cname", "A zone's own name cannot be a CNAME").
		WithCause("%s is the zone itself, and pointing it at %s needs a CNAME, which %s does not allow there.", hostname, target, provider).
		WithImpact("No record was created.").
		WithFix("Use a name inside the zone, such as www.%s, and redirect the bare name to it; or have an administrator set Cluster public IP under Settings, Domains to an IP address.", hostname).
		WithDocs("/docs/troubleshooting#the-panel-did-not-create-a-dns-record").
		WithStatus(http.StatusConflict).
		With("hostname", hostname)
}

// CertificateFailed reports a failed Let's Encrypt issuance.
func CertificateFailed(hostname, reason string) *Problem {
	return New("domain.certificate_failed", "The HTTPS certificate could not be issued").
		WithCause("Let's Encrypt refused to issue a certificate for %s: %s", hostname, reason).
		WithImpact("The domain works over HTTP but not HTTPS.").
		WithFix("Check that the domain resolves to this cluster and that port 80 is reachable from the internet, which is how the challenge is verified. If you have hit a rate limit, wait an hour before retrying.").
		WithDocs("/docs/troubleshooting#a-domain-does-not-work").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("hostname", hostname).With("reason", reason)
}

// --- a certificate of the team's own ---
//
// Every one of these is a refusal before anything is stored: a certificate
// the panel would have to serve and cannot is better found while the person is
// still holding the files.

// CertificateUnreadable is a chain with no certificate in it that could be
// read.
func CertificateUnreadable(detail string) *Problem {
	return New("certificate.unreadable", "That is not a certificate the panel can read").
		WithCause("The certificate box holds no PEM certificate that could be read: %s.", detail).
		WithImpact("Nothing was saved.").
		WithFix("Paste the certificate file's contents, from -----BEGIN CERTIFICATE----- to -----END CERTIFICATE-----, followed by the intermediate certificates your certificate authority gave you. A .crt or .cer file in DER form converts with `openssl x509 -inform der -in cert.cer -out cert.pem`.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateKeyUnreadable is a private key that is missing, encrypted or in a
// form nothing reads.
func CertificateKeyUnreadable() *Problem {
	return New("certificate.key_unreadable", "That is not a private key the panel can read").
		WithCause("The private key box holds no unencrypted PEM private key.").
		WithImpact("Nothing was saved.").
		WithFix("Paste the key that begins with -----BEGIN PRIVATE KEY-----, -----BEGIN RSA PRIVATE KEY----- or -----BEGIN EC PRIVATE KEY-----. A key protected by a passphrase has to be decrypted first: `openssl pkey -in encrypted.key -out plain.key`.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateKeyMismatch is a key that belongs to none of the certificates.
func CertificateKeyMismatch(subject string) *Problem {
	return New("certificate.key_mismatch", "The private key does not belong to this certificate").
		WithCause("The private key is not the one %s was issued for.", subject).
		WithImpact("Nothing was saved. Served together, every visitor's connection would fail.").
		WithFix("Use the key the certificate request was made with. `openssl x509 -noout -pubkey -in cert.pem` and `openssl pkey -pubout -in key.pem` print the same thing for a key and certificate that belong together.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateChainBroken is a certificate among those pasted that is not part
// of the leaf's chain.
func CertificateChainBroken(stray, leaf string) *Problem {
	return New("certificate.chain_broken", "These certificates are not one chain").
		WithCause("%s did not sign %s or any certificate above it, so it is not part of its chain.", stray, leaf).
		WithImpact("Nothing was saved. The order does not matter, and was put right when that was all that was wrong.").
		WithFix("Paste your certificate and the intermediate certificates your certificate authority gave you for it, and nothing else — not another site's certificate, not an old intermediate.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateExpired is a certificate past its date.
func CertificateExpired(subject, when string) *Problem {
	return New("certificate.expired", "This certificate has expired").
		WithCause("The certificate for %s stopped being valid on %s.", subject, when).
		WithImpact("Nothing was saved. Every browser refuses an expired certificate.").
		WithFix("Renew it with your certificate authority and upload the new one under the same name.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateNotYetValid is a certificate whose validity starts later.
func CertificateNotYetValid(subject, when string) *Problem {
	return New("certificate.not_yet_valid", "This certificate is not valid yet").
		WithCause("The certificate for %s is valid from %s.", subject, when).
		WithImpact("Nothing was saved. Served now, every browser would refuse it.").
		WithFix("Upload it once its validity has started. If the date looks wrong, check the clock on the server the panel runs on.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateWeakKey is a key browsers or certificate authorities no longer
// accept.
func CertificateWeakKey(keyType string) *Problem {
	return New("certificate.weak_key", "This certificate's key is not strong enough").
		WithCause("The key is %s. The panel serves RSA keys of 2048 bits or more, ECDSA keys on P-256 or P-384, and Ed25519 keys.", keyType).
		WithImpact("Nothing was saved.").
		WithFix("Make a new key and certificate request, for example `openssl req -new -newkey rsa:3072 -nodes -keyout key.pem -out request.csr`, and have it issued again.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateNoHostnames is a certificate with no DNS names, which browsers
// match against nothing.
func CertificateNoHostnames(subject string) *Problem {
	return New("certificate.no_hostnames", "This certificate names no hostname").
		WithCause("The certificate for %s has no DNS names in its Subject Alternative Name, and browsers stopped reading the Common Name years ago.", subject).
		WithImpact("Nothing was saved. It would match none of your domains.").
		WithFix("Have it issued again with the hostnames as Subject Alternative Names, such as DNS:shop.example.com or DNS:*.example.com.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusBadRequest)
}

// CertificateHostnameTaken is a certificate naming a hostname that belongs to
// another team on the panel, or to the panel itself.
//
// The ingress controller serves every certificate in the cluster by name, and
// two certificates for the same name are served in whatever order it read
// them. A certificate naming a hostname that is somebody else's would be served
// to their visitors some of the time.
func CertificateHostnameTaken(hostname string) *Problem {
	return New("certificate.hostname_taken", "This certificate names a hostname that is not this team's").
		WithCause("%s is this panel's own address, another team's domain, or named by another team's certificate.", hostname).
		WithImpact("Nothing was saved. The ingress serves certificates by name for the whole cluster, so this one would be sent to visitors of that hostname too.").
		WithFix("Use a certificate that names only this team's hostnames. If the hostname is yours, remove it from where it is used first.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusConflict)
}

// DomainNamedByCertificate is a hostname another team's certificate names.
func DomainNamedByCertificate(hostname string) *Problem {
	return New("domain.named_by_certificate", "Another team's certificate names this hostname").
		WithCause("%s is named in a certificate another team on this panel uploaded.", hostname).
		WithImpact("The domain was not added. Both certificates would be served for it, in whatever order the ingress read them.").
		WithFix("Ask the panel's administrator which team uses the hostname. A certificate is removed under Settings, Certificates.").
		WithDocs("/docs/troubleshooting#your-own-certificate-is-refused").
		WithStatus(http.StatusConflict)
}

// --- cluster and capacity ---

// InsufficientCapacity reports a workload that cannot be scheduled.
func InsufficientCapacity(what, detail string) *Problem {
	return New("cluster.insufficient_capacity", "There is not enough room in the cluster").
		WithCause("%s could not be scheduled: %s", what, detail).
		WithImpact("The instances are pending and not serving traffic.").
		WithFix("Add another server in Servers, lower this app's CPU or memory request, or reduce the number of instances.").
		WithDocs("/docs/performance").
		WithStatus(http.StatusConflict).
		Retry().
		With("workload", what)
}

// ClusterUnreachable reports a Kubernetes API that is not answering.
func ClusterUnreachable(err error) *Problem {
	return New("cluster.unreachable", "The cluster is not responding").
		WithCause("The Kubernetes API did not answer.").
		WithImpact("Your apps keep running, but Skifity cannot make changes or read live status right now.").
		WithFix("This usually clears up on its own within a minute. If it does not, check that the control plane server is up and that port 6443 is reachable between your servers.").
		WithDocs("/docs/troubleshooting#the-cluster-is-unreachable").
		WithStatus(http.StatusServiceUnavailable).
		Retry().
		Wrap(err)
}

// DriftCheckFailed is the cluster answering, and refusing to show one of an
// app's objects to the check that compares them with what the panel applies.
func DriftCheckFailed(object, reason string, err error) *Problem {
	return New("drift.check_failed", "The app could not be compared with what the panel applies").
		WithCause("Reading %s from the cluster failed: %s", object, reason).
		WithImpact("Nothing was changed. Whether anybody changed the app outside the panel is not known until this works.").
		WithFix("Check that the panel's service account can still read the app's namespace; somebody may have changed its permissions. If the cluster was just upgraded, try again in a minute.").
		WithDocs("/docs/troubleshooting#changed-outside-skifity").
		WithStatus(http.StatusBadGateway).
		Retry().
		Wrap(err)
}

// EventsUnreadable is the cluster answering, and refusing to list the events
// in a namespace.
func EventsUnreadable(namespace, reason string, err error) *Problem {
	return New("events.unreadable", "The cluster's events could not be read").
		WithCause("Listing the events in %s failed: %s", namespace, reason).
		WithImpact("Nothing was changed, and the app keeps running. Only this list is missing.").
		WithFix("Check that the panel's service account can still list events and pods in that namespace. If the cluster was just upgraded, try again in a minute.").
		WithDocs("/docs/troubleshooting#what-kubernetes-said").
		WithStatus(http.StatusBadGateway).
		Retry().
		Wrap(err)
}

// QuorumRisk reports a removal that would break etcd quorum.
func QuorumRisk(remaining int) *Problem {
	return New("cluster.quorum_risk", "Removing this server would break the cluster").
		WithCause("This is a control plane server, and removing it would leave %d of them. Embedded etcd needs an odd number of at least three to survive a failure.", remaining).
		WithImpact("Nothing was changed. The server is still part of the cluster.").
		WithFix("Promote another server to control plane first, then remove this one. With one control plane server you can remove it only by removing the whole cluster.").
		WithDocs("/docs/adding-servers#control-plane-servers").
		WithStatus(http.StatusConflict).
		With("remaining_control_planes", itoa(remaining))
}

// LastControlPlane refuses to remove the server the cluster is running on.
//
// A different sentence from QuorumRisk, because it is a different event: that
// one risks the cluster surviving a later failure, this one ends it now, along
// with the panel saying so.
func LastControlPlane() *Problem {
	return New("cluster.last_control_plane", "This is the only server running the cluster").
		WithCause("Removing it would delete the last control plane node. Kubernetes, every " +
			"app on it, and this panel run there.").
		WithImpact("Nothing was changed.").
		WithFix("Add another server and promote it to control plane first. To take the whole " +
			"cluster down deliberately, run the uninstaller on the server itself.").
		WithDocs("/docs/adding-servers#control-plane-servers").
		WithStatus(http.StatusConflict)
}

// ControlPlaneUnverifiable refuses a removal the panel cannot prove is safe.
func ControlPlaneUnverifiable() *Problem {
	return New("cluster.control_plane_unverifiable", "The cluster cannot be asked how many servers run it").
		WithCause("Removing a control plane server is only safe when the panel can see how " +
			"many are left, and the Kubernetes API did not answer.").
		WithImpact("Nothing was changed.").
		WithFix("Wait for the cluster to be reachable and try again. A worker server can be " +
			"removed either way.").
		WithDocs("/docs/troubleshooting#the-cluster-is-unreachable").
		WithStatus(http.StatusConflict)
}

// --- GPUs ---

// GPUUnavailable refuses GPUs of a kind no server advertises: an app asking
// for them would sit waiting for a server for ever.
func GPUUnavailable(vendor, resource string) *Problem {
	return New("app.gpu_unavailable", "No server offers that kind of GPU").
		WithCause("No server in the cluster advertises %s, which is how Kubernetes counts %s cards.", resource, vendor).
		WithImpact("Nothing was changed. An app asking for one would never start.").
		WithFix("On the server with the card, install the driver and the container toolkit, then enable GPUs "+
			"on the Servers page. The Servers page says for each server what is missing.").
		WithDocs("/docs/gpus#setting-up-a-server").
		WithStatus(http.StatusConflict).
		With("resource", resource)
}

// GPUTooMany refuses more GPUs for one instance than any one server has: an
// instance's cards all have to be on the server it runs on.
func GPUTooMany(count int, resource string, most int64) *Problem {
	return New("app.gpu_too_many", "No server has that many GPUs").
		WithCause("Each instance would ask for %d of %s, and the most any one server offers is %d. "+
			"An instance's cards all have to be in the server it runs on.", count, resource, most).
		WithImpact("Nothing was changed.").
		WithFix("Ask for %d or fewer for each instance, and run more instances if the app can share the work; "+
			"or add a server with more cards.", most).
		WithDocs("/docs/gpus#asking-for-gpus").
		WithStatus(http.StatusConflict).
		With("resource", resource)
}

// GPUScaleToZero refuses a GPU on an app that sleeps, from either side.
func GPUScaleToZero() *Problem {
	return New("app.gpu_scale_to_zero", "An app with a GPU cannot scale to zero").
		WithCause("A sleeping app gives its card back, and whichever app takes it is never asked to return it " +
			"when a request comes in to wake this one: that request would wait for a card that does not come free.").
		WithImpact("Nothing was changed.").
		WithFix("Turn scale to zero off before giving the app a GPU. Or give the GPU to its processes " +
			"alone, which never sleep, and let the app itself scale to zero.").
		WithDocs("/docs/gpus#what-an-app-with-a-gpu-cannot-do").
		WithStatus(http.StatusConflict)
}

// GPUDevicePluginExists refuses to install NVIDIA's device plugin beside one
// that is already there.
func GPUDevicePluginExists(foreign []string) *Problem {
	return New("gpu.device_plugin_exists", "This cluster already runs an NVIDIA device plugin").
		WithCause("%s already offers the cluster's NVIDIA cards to Kubernetes, and two plugins for one card "+
			"take turns being the one Kubernetes listens to.", strings.Join(foreign, ", ")).
		WithImpact("Nothing was installed. Apps can use the cards that plugin offers without the panel's.").
		WithFix("Leave it, and ask for GPUs in an app's settings: they come from that plugin. " +
			"Remove it first only if you want the panel to look after the plugin instead.").
		WithDocs("/docs/gpus#a-device-plugin-is-already-installed").
		WithStatus(http.StatusConflict)
}

// --- storage and backups ---

// StorageNotConfigured reports a backup with nowhere to go.
func StorageNotConfigured() *Problem {
	return New("backup.storage_not_configured", "No backup storage is configured").
		WithCause("This team has no S3-compatible storage set up yet.").
		WithImpact("Backups cannot run, so nothing is being kept safe.").
		WithFix("Open Settings, then Storage, and add an S3-compatible bucket. Any provider works: AWS S3, Backblaze B2, Cloudflare R2, Wasabi, or a MinIO server you run yourself.").
		WithDocs("/docs/backups#storage").
		WithStatus(http.StatusBadRequest)
}

// BackupFailed reports a failed backup run.
func BackupFailed(target, reason string) *Problem {
	return New("backup.failed", "The backup failed").
		WithCause("Backing up %s failed: %s", target, reason).
		WithImpact("There is no new backup from this run. Earlier backups are untouched.").
		WithFix("Check the storage credentials in Settings and that the bucket exists and is writable. Then run the backup again from the database page.").
		WithDocs("/docs/backups#failures").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("target", target).With("reason", reason)
}

// RestoreRefused reports a restore that would overwrite live data.
func RestoreRefused(target string) *Problem {
	return New("backup.restore_refused", "This restore would overwrite live data").
		WithCause("%s is in use and the restore would replace its current contents.", target).
		WithImpact("Nothing was changed.").
		WithFix("Restore into a new database instead, check it, then point your app at it. If you really do mean to overwrite, confirm it explicitly on the restore dialog.").
		WithDocs("/docs/backups#restoring").
		WithStatus(http.StatusConflict).
		With("target", target)
}

// BackupNotOffered is a backup, a schedule or a restore asked of a database
// whose engine the panel does not back up: a cache, and the engines whose
// snapshots cannot be taken from outside the server without handing the
// bucket's credentials to it. docs/backups.md says which, and why.
func BackupNotOffered(database, engine string) *Problem {
	return New("backup.not_offered", "Skifity does not back up this kind of database").
		WithCause("%s runs %s, which Skifity does not back up.", database, engine).
		WithImpact("Nothing was changed.").
		WithFix("Keep data you cannot lose in a database that is backed up, such as PostgreSQL, or copy it with the engine's own tools through `%s db connect`.", version.Binary).
		WithDocs("/docs/backups#what-is-not-backed-up").
		WithStatus(http.StatusConflict).
		With("database", database).With("engine", engine)
}

// --- secret managers ---
//
// name is the connection's name, kind what it is ("Vault"), and detail the
// sentence internal/secretmgr wrote about what happened, which names hosts,
// paths and keys and never a value.

// SecretManagerUnreachable is a secret manager that did not answer.
func SecretManagerUnreachable(name, kind, detail string) *Problem {
	return New("secrets.unreachable", "The secret manager could not be reached").
		WithCause("%s (%s): %s.", name, kind, detail).
		WithImpact("Nothing was read from it and nothing was changed.").
		WithFix("Check the address in Settings → Secret managers, and that the panel's server can reach it: a firewall, a VPN, a certificate the panel does not trust. Then try again.").
		WithDocs("/docs/configuration#secret-managers").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("connection", name)
}

// SecretManagerDenied is a secret manager that refused the credentials, or
// refused what was asked with them.
func SecretManagerDenied(name, kind, detail string) *Problem {
	return New("secrets.denied", "The secret manager refused the panel").
		WithCause("%s (%s): %s.", name, kind, detail).
		WithImpact("Nothing was read from it and nothing was changed.").
		WithFix("Check the connection's credentials, and that the policy they have allows reading this secret. Give the connection new credentials in Settings → Secret managers if they expired.").
		WithDocs("/docs/configuration#secret-managers").
		WithStatus(http.StatusBadGateway).
		With("connection", name)
}

// SecretNotFound is a secret, or a key of one, that is not there.
func SecretNotFound(name, kind, detail string) *Problem {
	return New("secrets.not_found", "The secret is not in the secret manager").
		WithCause("%s (%s): %s.", name, kind, detail).
		WithImpact("Nothing was read from it and nothing was changed.").
		WithFix("Check the path and the key, and that the secret exists in the project, environment or mount the connection reads.").
		WithDocs("/docs/configuration#secret-managers").
		WithStatus(http.StatusUnprocessableEntity).
		With("connection", name)
}

// SecretManagerBadAnswer is a secret manager whose answer was not what that
// kind of manager sends.
func SecretManagerBadAnswer(name, kind, detail string) *Problem {
	return New("secrets.bad_answer", "The secret manager answered something unexpected").
		WithCause("%s (%s): %s.", name, kind, detail).
		WithImpact("Nothing was read from it and nothing was changed.").
		WithFix("Check that the address is the secret manager's API and not a page in front of it, and that it is the kind of manager the connection says.").
		WithDocs("/docs/configuration#secret-managers").
		WithStatus(http.StatusBadGateway).
		With("connection", name)
}

// ReferenceUnresolved is a variable read from a secret manager that could not
// be read, found while deploying, applying a change or refreshing.
//
// It names the variable, where it is read from, and why, and says the one
// thing a person most needs to know: the version already running keeps
// running with what it had. Nothing is ever deployed with the variable empty.
func ReferenceUnresolved(variable, reference, name, kind, detail, reason string) *Problem {
	return New("secrets.reference_unresolved", "A variable could not be read from its secret manager").
		WithCause("%s is read from %s (%s, %s), and %s.", variable, reference, name, kind, detail).
		WithImpact("Nothing was deployed or changed. The version running now keeps running with the values it had.").
		WithFix("Check the connection with Test under Settings → Secret managers, and that the secret exists at that path. Then deploy again, or refresh the app's variables.").
		WithDocs("/docs/concepts#variables-from-a-secret-manager").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("variable", variable).With("connection", name).With("reason", reason)
}

// SecretManagerInUse is a connection somebody tried to remove while variables
// still read it.
func SecretManagerInUse(name string, count int, uses string) *Problem {
	return New("secrets.connection_in_use", "Variables still read this secret manager").
		WithCause("%d variables read %s: %s.", count, name, uses).
		WithImpact("Nothing was removed. Without the connection, the next deploy of those apps would fail.").
		WithFix("Give those variables a value of their own, or point them at another connection, then remove this one.").
		WithDocs("/docs/configuration#secret-managers").
		WithStatus(http.StatusConflict).
		With("connection", name)
}

// ReferenceNotAllowed is a variable read through a connection from a path, or
// in a project, that the connection is limited away from. It is refused when
// the variable is set and again whenever it is read, so a connection narrowed
// after the variable was set stops it at the next deploy.
//
// detail is internal/secretmgr's clause for what the connection allows. limit
// is which limit refused it: "path" or "project".
func ReferenceNotAllowed(variable, reference, name, detail, limit string) *Problem {
	return New("secrets.reference_not_allowed", "The secret manager connection does not allow this").
		WithCause("%s is read from %s, and the connection %s %s.", variable, reference, name, detail).
		WithImpact("This variable was not read. Setting it is refused, and a deploy, a sync or a refresh that needs it stops before anything reaches the cluster: the version running keeps running.").
		WithFix("Point the variable at a path the connection allows, or read it through a connection meant for this project. A team admin changes what a connection allows under Settings → Secret managers.").
		WithDocs("/docs/configuration#limiting-a-connection").
		WithStatus(http.StatusForbidden).
		With("variable", variable).With("connection", name).With("limit", limit)
}

// SecretManagerLimitsBreakReferences is a change to a connection's limits
// that would leave variables that read it now outside them, refused until the
// administrator says that is what they mean.
func SecretManagerLimitsBreakReferences(name string, count int, uses string) *Problem {
	return New("secrets.limits_break_references", "Variables would stop resolving through this connection").
		WithCause("%d variables read through %s would be outside its new limits: %s.", count, name, uses).
		WithImpact("Nothing was changed. With the new limits, the next deploy, sync or refresh of those apps would stop before anything reaches the cluster.").
		WithFix("Point those variables at an allowed path or another connection first. If cutting them off is the point, save the limits anyway: Save anyway in the panel, --force on the command line, force: true in the API.").
		WithDocs("/docs/configuration#limiting-a-connection").
		WithStatus(http.StatusConflict).
		With("connection", name)
}

// --- a database's life: stopping, resizing, passwords and imports ---

// DatabaseStopLinked is a stop asked of a database apps still read, without
// saying that is meant.
func DatabaseStopLinked(database, apps string) *Problem {
	return New("database.stop_linked", "Apps use this database").
		WithCause("%s is linked to %s.", database, apps).
		WithImpact("Nothing was changed. Stopped, it would answer none of them until it is started again.").
		WithFix("Confirm that these apps may lose their database while it is stopped, or unlink them first.").
		WithDocs("/docs/databases#stopping-and-starting").
		WithStatus(http.StatusConflict).
		With("database", database)
}

// DatabaseNotRunning is a change that needs a database answering, asked of
// one that is not.
func DatabaseNotRunning(database, status string) *Problem {
	return New("database.not_running", "This database is not running").
		WithCause("%s is %s, and this needs it running.", database, status).
		WithImpact("Nothing was changed.").
		WithFix("Start it, or wait until it is running, then try again.").
		WithStatus(http.StatusConflict).
		With("database", database)
}

// DatabaseBusy is a change asked of a database something else is already
// changing: being created, restored, given a dump or a new password.
func DatabaseBusy(database string) *Problem {
	return New("database.busy", "Something is already being done to this database").
		WithCause("%s is being created, restored, imported into or given a new password.", database).
		WithImpact("Nothing was changed.").
		WithFix("Wait for that to finish, then try again. The database's History says how it is going.").
		WithStatus(http.StatusConflict).
		With("database", database)
}

// DatabaseStorageShrink is a disk asked to be smaller than it is. A volume
// cannot shrink: Kubernetes refuses the request, and no storage driver takes
// it.
func DatabaseStorageShrink(database string, haveGB, askedGB int) *Problem {
	return New("database.storage_shrink", "A database's disk cannot be made smaller").
		WithCause("%s has %d GB, and %d GB is less.", database, haveGB, askedGB).
		WithImpact("Nothing was changed.").
		WithFix("A disk can only grow. To use less, create a smaller database, import a backup of this one into it, and link your apps to that.").
		WithDocs("/docs/troubleshooting#a-database-disk-cannot-shrink").
		WithStatus(http.StatusBadRequest).
		With("database", database)
}

// DatabaseStorageNotExpandable is a disk whose storage class does not let a
// volume grow. k3s's own local-path is one.
func DatabaseStorageNotExpandable(database, class string) *Problem {
	return New("database.storage_not_expandable", "This database's disk cannot grow").
		WithCause("%s keeps its data on the storage class %s, which does not allow a volume to grow.", database, class).
		WithImpact("Nothing was changed, its CPU and memory included.").
		WithFix("k3s's own local-path storage never allows it. Set allowVolumeExpansion: true on this storage class if its driver can grow a volume, or make a bigger database and import a backup of this one into it.").
		WithDocs("/docs/troubleshooting#a-database-disk-cannot-grow").
		WithStatus(http.StatusConflict).
		With("database", database).With("class", class)
}

// DatabaseOverQuota is a resize that would take an environment past what it
// is allowed. Kubernetes would refuse the database's new instance and leave
// the old one stopped, so it is refused here instead.
func DatabaseOverQuota(resource, wouldBe, hard string) *Problem {
	return New("database.over_quota", "That is more than this environment is allowed").
		WithCause("Its %s would come to %s, and the environment is allowed %s.", resource, wouldBe, hard).
		WithImpact("Nothing was changed.").
		WithFix("Ask for less, or make room: resize or stop something else in this environment.").
		WithDocs("/docs/troubleshooting#a-database-resize-is-over-the-limit").
		WithStatus(http.StatusConflict).
		With("resource", resource)
}

// DatabaseMemoryTooSmall is a memory limit below what an engine starts with.
func DatabaseMemoryTooSmall(engine string, minimumMB int) *Problem {
	return New("database.memory_too_small", "That is too little memory for this database").
		WithCause("%s needs a memory limit of at least %d MB.", engine, minimumMB).
		WithImpact("Nothing was changed.").
		WithFix("Give it at least %d MB.", minimumMB).
		WithStatus(http.StatusBadRequest).
		With("engine", engine)
}

// DatabasePasswordNotOffered is a new password asked of an engine that has
// none.
func DatabasePasswordNotOffered(database, engine string) *Problem {
	return New("database.password_not_offered", "This database has no password").
		WithCause("%s runs %s, which has no password to change.", database, engine).
		WithImpact("Nothing was changed.").
		WithFix("Only the apps in its own environment can reach it; that is what keeps it private.").
		WithDocs("/docs/databases#changing-the-password").
		WithStatus(http.StatusConflict).
		With("database", database).With("engine", engine)
}

// DatabasePasswordRejected is a password somebody chose that would break a
// connection string or a statement.
func DatabasePasswordRejected() *Problem {
	return New("database.password_rejected", "That password cannot be used").
		WithCause("A database password here is 16 to 128 letters, digits, dots, dashes, underscores or tildes, and this one is not.").
		WithImpact("Nothing was changed.").
		WithFix("Leave the password out to have a strong one generated, or choose one of those characters: each ends up in a connection string, where anything else would need escaping.").
		WithDocs("/docs/databases#changing-the-password").
		WithStatus(http.StatusBadRequest)
}

// DatabasePasswordChangeRunning is a second password change while one is
// under way.
func DatabasePasswordChangeRunning(database string) *Problem {
	return New("database.password_change_running", "A password change is already under way").
		WithCause("%s is being given a new password.", database).
		WithImpact("Nothing was changed.").
		WithFix("Wait for it to finish; the database's History shows how it is going.").
		WithStatus(http.StatusConflict).
		With("database", database)
}

// DatabasePasswordChangeInterrupted is a new password asked for while an
// earlier change, interrupted, still holds the one it was giving.
func DatabasePasswordChangeInterrupted(database string) *Problem {
	return New("database.password_change_interrupted", "An earlier password change was not finished").
		WithCause("A change of %s's password stopped halfway, and the panel kept the password it was giving it.", database).
		WithImpact("Nothing was changed.").
		WithFix("Change the password again without choosing one: the panel finishes the earlier change first. Then choose yours.").
		WithDocs("/docs/databases#changing-the-password").
		WithStatus(http.StatusConflict).
		With("database", database)
}

// DatabasePasswordFailed is a password change that did not happen, and left
// the old password in place.
func DatabasePasswordFailed(reason string) *Problem {
	return New("database.password_failed", "The password could not be changed").
		WithCause("%s", reason).
		WithImpact("The old password still works, and the apps still use it.").
		WithFix("Read the reason above; the usual ones are a database that stopped answering and a server with no room for the short job that makes the change. Then try again.").
		WithDocs("/docs/databases#changing-the-password").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("reason", reason)
}

// DatabasePasswordStranded is a change the database took and the panel could
// neither finish nor undo. The new password is kept, sealed, so it is not
// lost with the panel's memory.
func DatabasePasswordStranded(reason string) *Problem {
	return New("database.password_stranded", "The new password was taken and could not be passed on").
		WithCause("The database accepted the new password, and then %s. Putting the old one back failed too.", reason).
		WithImpact("Apps may fail to connect until this is finished. The panel kept the new password, sealed, beside the old one.").
		WithFix("Change the password again without choosing one, once the cluster answers: the panel finishes this change with the password it kept.").
		WithDocs("/docs/databases#changing-the-password").
		WithStatus(http.StatusBadGateway).
		With("reason", reason)
}

// ImportNotOffered is a dump sent to an engine the panel cannot load one
// into.
func ImportNotOffered(database, engine string) *Problem {
	return New("import.not_offered", "Skifity does not import into this kind of database").
		WithCause("%s runs %s, and Skifity has no way to load a dump into it.", database, engine).
		WithImpact("Nothing was changed.").
		WithFix("Load your data with the engine's own client through `%s db connect`.", version.Binary).
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusConflict).
		With("database", database).With("engine", engine)
}

// ImportWrongKind is a dump made by one engine's tool sent to another's
// database.
func ImportWrongKind(found, engine, formats string) *Problem {
	return New("import.wrong_kind", "This dump is for another kind of database").
		WithCause("The file is %s, which %s does not load.", found, engine).
		WithImpact("Nothing was changed.").
		WithFix("Import it into a database of the kind it was dumped from, or dump it again in a format this one takes: %s.", formats).
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusBadRequest).
		With("engine", engine)
}

// ImportUnrecognised is a file that is none of the formats an engine takes.
func ImportUnrecognised(engine, formats string) *Problem {
	return New("import.unrecognised", "Skifity could not tell what this file is").
		WithCause("It is not a dump %s can load: %s.", engine, formats).
		WithImpact("Nothing was changed.").
		WithFix("Make the dump with the engine's own tool, as docs/databases.md shows, and send that file as it is or gzipped.").
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusBadRequest).
		With("engine", engine)
}

// ImportFormatInvalid is a format somebody named that the engine does not
// take.
func ImportFormatInvalid(format, engine, formats string) *Problem {
	return New("import.format_invalid", "That is not a format this database imports").
		WithCause("%s is not one of the formats %s imports: %s.", format, engine, formats).
		WithImpact("Nothing was changed.").
		WithFix("Leave the format out to have it detected, or name one of those.").
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusBadRequest).
		With("engine", engine)
}

// ImportEmpty is a file with nothing in it.
func ImportEmpty() *Problem {
	return New("import.empty", "The file is empty").
		WithCause("Nothing arrived, or it decompressed to nothing.").
		WithImpact("Nothing was changed.").
		WithFix("Check that the dump was written completely, then send it again.").
		WithStatus(http.StatusBadRequest)
}

// ImportDamaged is a compressed file that does not decompress to its end: a
// truncated download, most often.
func ImportDamaged(reason string) *Problem {
	return New("import.damaged", "The file is damaged").
		WithCause("It is gzipped and does not decompress to its end: %s.", reason).
		WithImpact("Nothing was changed.").
		WithFix("Send it again. If it fails the same way, the file was cut short when it was made or copied; make the dump again.").
		WithStatus(http.StatusBadRequest)
}

// ImportTooLarge is a file over the size the panel stages.
func ImportTooLarge(limitMB int64) *Problem {
	return New("import.too_large", "The file is too large to import here").
		WithCause("It is more than %d MB, which is as much as the panel takes in one import.", limitMB).
		WithImpact("Nothing was changed.").
		WithFix("Load it with the engine's own client through `%s db connect`, which has no limit.", version.Binary).
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusRequestEntityTooLarge)
}

// ImportBackupFailed is an import that stopped because the backup taken
// before it failed.
func ImportBackupFailed(reason string) *Problem {
	return New("import.backup_failed", "The backup before the import failed").
		WithCause("%s", reason).
		WithImpact("Nothing was imported, so the database is as it was.").
		WithFix("Fix what stopped the backup — the database's Backups list says the same — then import again.").
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("reason", reason)
}

// ImportFailed is a dump the engine's own tool could not load.
func ImportFailed(reason string) *Problem {
	return New("import.failed", "The dump could not be loaded").
		WithCause("%s", reason).
		WithImpact("PostgreSQL loads a dump in one transaction, so nothing of it is left behind; the other engines may have loaded part of it. A backup was taken just before.").
		WithFix("Read the reason above and fix the dump. To put the database back as it was, restore the backup taken just before the import from its Backups.").
		WithDocs("/docs/databases#importing-a-dump").
		WithStatus(http.StatusBadGateway).
		With("reason", reason)
}

// --- log drains ---

// LogDrainInvalid is a drain's settings being refused before anything is
// sent or kept.
func LogDrainInvalid(reason string) *Problem {
	return New("drain.invalid", "Those settings cannot be a log drain").
		WithCause("%s", reason).
		WithImpact("Nothing was saved, and nothing was sent.").
		WithFix("Correct the setting it names. Each kind's settings, and where to find them in the service, are on the log drains page of the documentation.").
		WithDocs("/docs/log-drains#the-kinds").
		WithStatus(http.StatusBadRequest)
}

// LogDrainTestFailed is the test line not being taken by the service. A drain
// is not saved until one is.
func LogDrainTestFailed(drain, reason string) *Problem {
	return New("drain.test_failed", "The service did not take the test line").
		WithCause("The panel sent %s one test line, as the collector will send every line, and it was not taken: %s", drain, reason).
		WithImpact("Nothing was changed: a drain is saved, and a change to one is kept, only once a test line reaches the service.").
		WithFix("Check the address, and the token or key, against the service's own page for sending logs, and that this server can reach it. "+
			"An address on this machine, inside the cluster or at the cloud's metadata service is refused on purpose.").
		WithDocs("/docs/log-drains#the-test").
		WithStatus(http.StatusBadGateway).
		Retry().
		With("drain", drain)
}

// TooManyLogDrains is a team adding a drain past the limit.
func TooManyLogDrains(limit int) *Problem {
	return New("drain.limit", "This team has as many log drains as it can").
		WithCause("A team can have %d log drains.", limit).
		WithImpact("Nothing was saved.").
		WithFix("Remove one it no longer sends to, or limit one drain to several projects instead of having one per project.").
		WithDocs("/docs/log-drains#limits").
		WithStatus(http.StatusConflict)
}

// LogDrainUnreadable is a stored drain whose credentials do not open: the row
// was changed by hand, or the key that sealed them is gone.
func LogDrainUnreadable(drain string) *Problem {
	return New("drain.unreadable", "This drain's credentials cannot be read").
		WithCause("The credentials stored for %s do not open with this panel's key for the address it sends to.", drain).
		WithImpact("The drain is left out of the collector, so it receives nothing. Every other drain is unaffected.").
		WithFix("Edit the drain and enter its token or password again, or remove it and add it again.").
		WithDocs("/docs/troubleshooting#logs-do-not-reach-a-drain").
		WithStatus(http.StatusConflict).
		With("drain", drain)
}

// LogCollectorFailed is the collector's configuration not reaching the
// cluster.
func LogCollectorFailed(reason string) *Problem {
	return New("drain.collector_failed", "The log collector could not be brought up to date").
		WithCause("%s", reason).
		WithImpact("The drains are saved. The collector on each server keeps the configuration it had, or does not run if it never started.").
		WithFix("Check that the cluster is reachable under Servers. The panel tries again every five minutes, and every time a drain is saved.").
		WithDocs("/docs/troubleshooting#logs-do-not-reach-a-drain").
		WithStatus(http.StatusBadGateway).
		Retry()
}

// --- configuration ---

// NotConfigured reports a feature used before its settings were filled in.
func NotConfigured(feature, where string) *Problem {
	return Newf("config.missing", "%s is not set up yet", feature).
		WithCause("%s needs configuration that has not been provided.", feature).
		WithImpact("The action was not performed.").
		WithFix("Open %s and fill it in. Nothing needs to be changed in code or on the server.", where).
		WithDocs("/docs/configuration").
		WithStatus(http.StatusBadRequest).
		With("feature", feature)
}

// ScalingRisk warns about an app that will misbehave when scaled.
// This is a warning, not an error: the user is allowed to proceed.
func ScalingRisk(reason, fix string) *Problem {
	return New("scaling.risk", "This app may not work correctly with more than one instance").
		WithCause("%s", reason).
		WithImpact("With several instances running, some requests will behave differently from others.").
		WithFix("%s", fix).
		WithDocs("/docs/concepts#instances-and-scaling").
		WithSeverity(SeverityWarning).
		WithStatus(http.StatusOK)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// tail keeps the end of a long output, which is where the actual failure is.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-n:]
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

// MasterKeyMissing is a panel that found its database but not its key.
func MasterKeyMissing(path string) *Problem {
	return New("crypto.master_key_missing", "The panel's master key is missing").
		WithCause("The database already holds accounts or secrets, and there is no master key at %s. Starting anyway would create a new key, and a new key opens nothing the old one sealed.", path).
		WithImpact("The panel did not start, and nothing was changed. Every stored secret and every encrypted backup stays unreadable until the key it was sealed with is back.").
		WithFix("Put the master key back at that path. If it is gone, rebuild it from the recovery key you downloaded: `skifity admin restore-key`. Only if you are starting over, with nothing to keep, set SKIFITY_ALLOW_NEW_MASTER_KEY=1 once.").
		WithDocs("/docs/troubleshooting#the-master-key-is-missing")
}

// MasterKeyMismatch is a key that does not open the database it was found next to.
func MasterKeyMismatch(path string) *Problem {
	return New("crypto.master_key_mismatch", "The master key does not match this database").
		WithCause("The key at %s cannot open the secrets stored in the database. It is a different key from the one the database was sealed with: one from another install, or one that was replaced.", path).
		WithImpact("The panel did not start, and nothing was changed. Starting would have left every stored secret unreadable.").
		WithFix("Put the key this database was sealed with at that path, or rebuild it from the recovery key with `skifity admin restore-key`. After restoring a backup of the database, the key to use is the one the panel had when the backup was taken.").
		WithDocs("/docs/troubleshooting#the-master-key-does-not-match")
}
