package errdoc

import (
	"fmt"
	"net/http"

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
		WithFix("Check the app logs for a crash on startup. The usual causes are a missing environment variable, a health check path that does not exist yet, and a port mismatch between the app and the configured port.").
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
