# A look at the panel

Every screen, from the real binary. These are captured by a test rather than
taken by hand — `web/tests/screenshots.spec.ts` — so a picture here can never
show a screen that no longer exists: if a page is renamed or a tab disappears,
the capture fails instead of quietly keeping the old image.

The panel below has a project in it, three apps, a domain, some variables and
autoscaling turned on. It has no servers, because the machine these were taken
on has no cluster — so where a screen says there are no instances, that is the
truth about a panel nothing has been attached to yet, not a placeholder.

## Setting up

| | |
|---|---|
| ![First run: the setup token, an email address, a name, a password and a team name](images/setup.png) | **First run.** The installer prints a token; nothing else can create the first account. |
| ![The recovery key, shown once, with a checkbox confirming it has been saved](images/recovery-key.png) | **The recovery key**, shown once. It opens the encrypted settings if the master key is ever lost. The key in the picture is blurred, and was a throwaway. |
| ![The sign-in page with email, password and a single sign-on button](images/sign-in.png) | **Signing in.** With an identity provider configured, the button below the form appears. |

## The shell

| | |
|---|---|
| ![The overview: projects, apps, cluster health and recent activity](images/overview.png) | **Overview.** What is running, what changed, and what needs attention. |
| ![The same overview in dark mode](images/overview-dark.png) | **Dark**, which is what most people will see it in. |
| ![The overview in Russian](images/overview-russian.png) | **Russian.** Five languages, and a missing string fails the build rather than falling back to English. |
| ![The overview in Indonesian](images/overview-indonesian.png) | **Indonesian.** |

## Projects and apps

| | |
|---|---|
| ![The projects list](images/projects.png) | **Projects.** A project holds environments; an environment holds apps and databases. |
| ![A project's canvas, showing its apps and how they connect](images/project.png) | **A project**, drawn. Apps, databases, and what is linked to what. |
| ![The new app form: a Git repository, a branch and a builder](images/new-app.png) | **A new app**, from a Git repository or a container image. |

### One app, tab by tab

| | |
|---|---|
| ![An app's overview: its status, domains and recent deployments](images/app-overview.png) | **Overview.** |
| ![The deployments list with commits and outcomes](images/app-deployments.png) | **Deployments**, each with the commit it came from and a one-click rollback. |
| ![Live logs](images/app-logs.png) | **Logs**, streamed. |
| ![Environment variables, with secret values hidden](images/app-variables.png) | **Variables.** A secret is encrypted and never shown again — and changing one does not rebuild the image. |
| ![Domains, with certificate status](images/app-domains.png) | **Domains.** A certificate is requested as soon as DNS points here. |
| ![The scaling tab: autoscaling, targets, scale to zero, and what to fix first](images/app-scaling.png) | **Scaling.** "Before you scale" reads the app's own configuration and names what would break with a second instance. |
| ![Volumes and their backups](images/app-storage.png) | **Storage.** Volumes, and the backups of them. |
| ![App settings: resources, health path, builder](images/app-settings.png) | **Settings.** |
| ![The Kubernetes objects this app becomes](images/app-advanced.png) | **Advanced**, which is the only place the word Kubernetes appears. |

## Everything else

| | |
|---|---|
| ![The databases list](images/databases.png) | **Databases.** Postgres, MySQL and Redis, provisioned and backed up by the panel. |
| ![The servers list](images/servers.png) | **Servers.** |
| ![Adding a server: an IP address, a user and a key or password](images/add-server.png) | **Adding one** is an address and a way in. Everything after that is automatic, and every step says what it is doing. |
| ![The template catalogue, grouped by category](images/templates.png) | **Templates.** 282 applications, every image on a version that was checked to exist. |
| ![The activity log](images/activity.png) | **Activity.** Who did what, and when. |
| ![The account page: password, two-factor, sessions and API tokens](images/account.png) | **Account.** Two-factor, active sessions, and API tokens for the CLI. |

## Settings

| | |
|---|---|
| ![General settings, grouped](images/settings-general.png) | **General**, and every other group: domains, storage, DNS, email, the registry, the cluster, and single sign-on. |
| ![Git accounts and the GitHub App fields](images/settings-git.png) | **Git.** Connected accounts, or a GitHub App for an organisation. |
| ![Notification channels](images/settings-notifications.png) | **Notifications.** Email, Slack, Discord, or a webhook. |
| ![Cluster components and their status](images/settings-components.png) | **Components.** cert-manager, CloudNativePG, KEDA, Longhorn — installed on first use, not up front. |
| ![Team members and their roles](images/settings-members.png) | **Members.** |
| ![Security: the recovery key and key rotation](images/settings-security.png) | **Security.** The recovery key, and rotating the master key without downtime. |
| ![The audit log](images/settings-audit.png) | **Audit.** |

---

Taking these again after a change to the panel:

```sh
make build
SKIFITY_SCREENSHOTS=1 npm --prefix web run test:e2e -- screenshots
```
