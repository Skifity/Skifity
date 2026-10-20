package deploy

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// A repository on a Git host of the operator's own.
//
// The build namespace keeps every build off the private ranges and off the
// servers, because a build runs somebody's install scripts. The repository the
// build clones is the one place that was wrong: a Gitea at home, a GitLab on
// the office network or over a VPN, or a Git host on one of the servers at a
// port other than the web's, was an address the clone waited two minutes for
// and then gave up on with a timeout that named nothing. An administrator
// lists the hosts builds may reach (settings.KeyGitPrivateHosts); a build of a
// repository on one of them may reach it on the repository's port and nothing
// else, and a build of a repository on any other is refused before it starts,
// saying why and who can change it.

// lookupGitHost resolves a repository's host. A variable so a test can say
// what a name resolves to without a resolver.
var lookupGitHost = func(ctx context.Context, host string) ([]string, error) {
	if net.ParseIP(host) != nil {
		return []string{host}, nil
	}
	return net.DefaultResolver.LookupHost(ctx, host)
}

// resolveGitHost is what a repository's host resolves to here, or nothing. A
// name that does not resolve is not this step's failure to report: the clone
// resolves the same name a moment later and says what happened in the build's
// log, where whoever reads the failure looks.
func resolveGitHost(ctx context.Context, host string) []string {
	addresses, err := lookupGitHost(ctx, host)
	if err != nil {
		return nil
	}
	return addresses
}

// repositoryHost is the host a build clones from and the port: the address's
// own, or its scheme's.
func repositoryHost(repoURL string) (string, int, bool) {
	parsed, err := url.Parse(repoURL)
	if err != nil || parsed.Hostname() == "" {
		return "", 0, false
	}
	port := 443
	if parsed.Scheme == "http" {
		port = 80
	}
	if text := parsed.Port(); text != "" {
		number, err := strconv.Atoi(text)
		if err != nil || number < 1 || number > 65535 {
			return "", 0, false
		}
		port = number
	}
	return strings.ToLower(parsed.Hostname()), port, true
}

// openPathToSource lets one build reach its repository when the repository's
// host is somewhere builds are kept off and an administrator listed it. It
// answers the policy it applied, or nil when none was needed, and a Problem
// when the host is fenced and not listed.
func (d *Deployer) openPathToSource(ctx context.Context, deployment *store.Deployment, spec builder.JobSpec) (*networkingv1.NetworkPolicy, error) {
	if spec.SourceUpload || spec.RepoURL == "" {
		return nil, nil
	}
	host, port, ok := repositoryHost(spec.RepoURL)
	if !ok {
		return nil, nil
	}
	resolved := resolveGitHost(ctx, host)
	if len(resolved) == 0 {
		return nil, nil
	}
	nodes, err := d.cluster.Client().NodeAddresses(ctx)
	if err != nil {
		return nil, err
	}
	fenced := kube.FencedAddresses(resolved, nodes)
	if port == 80 || port == 443 {
		// The servers' own addresses are open to every build on the web
		// ports already (kube.InternetEgress): a Git host that is an app
		// here, at its public address, needs nothing.
		fenced = slices.DeleteFunc(fenced, func(address string) bool { return !kube.IsReservedAddress(address) })
	}
	if len(fenced) == 0 {
		return nil, nil
	}
	listed, _, err := d.db.GetSetting(ctx, settings.KeyGitPrivateHosts)
	if err != nil {
		return nil, err
	}
	if !settings.GitHostListed(listed, host, port) {
		// A name that also has an address builds reach is cloned from that
		// one, as it was before there was a list.
		if reachableElsewhere(resolved, fenced) {
			return nil, nil
		}
		return nil, errdoc.GitHostPrivate(host, fenced[0], port)
	}
	policy := kube.BuildSourcePolicy(spec.Namespace, spec.Name, spec.DeploymentID, fenced, port)
	if err := d.cluster.Client().Applier().Apply(ctx, policy); err != nil {
		return nil, fmt.Errorf("let the build reach its repository: %w", err)
	}
	d.appendLog(ctx, deployment.ID, fmt.Sprintf(
		"%s is on a network builds are kept off, and is listed under Settings → Git: this build may reach it on port %d.",
		host, port))
	return policy, nil
}

// reachableElsewhere says whether a host resolves to an IPv4 address outside
// the fenced ones, which a build reaches without being let.
func reachableElsewhere(resolved, fenced []string) bool {
	for _, address := range resolved {
		ip, err := netip.ParseAddr(address)
		if err != nil || !ip.Unmap().Is4() {
			continue
		}
		if !slices.Contains(fenced, ip.Unmap().String()) && !kube.IsReservedAddress(ip.Unmap().String()) {
			return true
		}
	}
	return false
}

// ownByBuild makes the build's Job the owner of something made for it, so it
// goes with the Job when the panel is not there to take it away.
func (d *Deployer) ownByBuild(ctx context.Context, spec builder.JobSpec, object metav1.Object) {
	applier := d.cluster.Client().Applier()
	job, err := applier.Get(ctx, "batch/v1", "Job", spec.Namespace, spec.Name)
	if err != nil {
		d.log.Warn("could not read the build to hand it what was made for it", "job", spec.Name, "error", err)
		return
	}
	object.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "batch/v1", Kind: "Job", Name: job.GetName(), UID: job.GetUID(),
	}})
	if err := applier.Apply(ctx, object); err != nil {
		d.log.Warn("could not make the build own what was made for it", "job", spec.Name, "object", object.GetName(), "error", err)
	}
}

// removeForBuild deletes something made for one build once it has finished,
// on a context of its own, because the build's may be what was cancelled.
func (d *Deployer) removeForBuild(ctx context.Context, apiVersion, kind, namespace, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := d.cluster.Client().Applier().Delete(ctx, apiVersion, kind, namespace, name); err != nil && !kube.IsNotFound(err) {
		d.log.Warn("could not remove what was made for a finished build", "kind", kind, "name", name, "error", err)
	}
}
