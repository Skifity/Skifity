#!/usr/bin/env bash
# Collects what a cluster says about itself, into one directory.
#
# verify.sh says which check failed and what it expected. This is the other
# half of a report: the state the check failed against. It is what CI uploads
# after every run on a real cluster, and what to attach when something on a
# server of your own does not work.
#
#   sudo bash test/cluster/collect.sh /tmp/skifity-report
#
# It reads and never changes anything. It leaves Secrets out entirely — not
# their names, not their values — and the panel's own log is already redacted
# by internal/logging. The installer's log can hold what kubectl printed, so
# read the directory before you send it anywhere.

set -uo pipefail

OUT="${1:-/tmp/skifity-report}"
mkdir -p "$OUT"
export KUBECONFIG="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"

# run writes what one command printed to a file named for it, and carries on
# whatever happened: a report that stops at the first thing that fails is a
# report of the first thing that failed.
run() {
	local name="$1"
	shift
	{
		printf '$ %s\n\n' "$*"
		"$@" 2>&1
	} >"$OUT/$name.txt" 2>&1 || true
}

kube() { command -v kubectl >/dev/null 2>&1 && kubectl --request-timeout=20s "$@"; }

# The machine.
run machine sh -c 'uname -a; echo; uptime; echo; free -m; echo; df -h; echo; cat /etc/os-release'
# What decides whether a rootless BuildKit may make its user namespace: Ubuntu
# from 23.10 restricts it for unconfined programs unless a profile allows it.
run userns sh -c 'sysctl kernel.apparmor_restrict_unprivileged_userns kernel.unprivileged_userns_clone user.max_user_namespaces 2>&1; ls /etc/apparmor.d 2>&1 | grep -i -e rootless -e buildkit'
run memory-pressure sh -c 'dmesg 2>/dev/null | grep -iE "out of memory|oom|killed process" | tail -50'
run listening sh -c 'ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null'
run iptables sh -c 'iptables-save 2>/dev/null | grep -vE "^#" | head -2000'

# k3s itself.
run k3s-journal sh -c 'journalctl -u k3s --no-pager -n 4000 2>/dev/null'
k3s_config() {
	for file in /etc/rancher/k3s/config.yaml /etc/rancher/k3s/config.yaml.d/*.yaml /etc/rancher/k3s/registries.yaml; do
		[ -f "$file" ] && printf '== %s\n%s\n\n' "$file" "$(cat "$file")"
	done
}
run k3s-config k3s_config

# The cluster, as Kubernetes sees it.
run nodes kube get nodes -o wide
run node-describe kube describe nodes
run pods kube get pods -A -o wide
run workloads kube get deploy,statefulset,daemonset,job,cronjob -A -o wide
run services kube get svc,endpoints,ingress -A -o wide
run storage kube get pvc,pv -A -o wide
run events kube get events -A --sort-by=.lastTimestamp
run network-policies kube get networkpolicies -A -o yaml
run quotas kube get resourcequota,limitrange -A -o yaml
run certificates kube get certificates,certificaterequests,orders,challenges,clusterissuers -A -o wide

# Every pod that is not plainly fine, described: the reason a pod is stuck is
# in its events, and the events are gone an hour later.
kube get pods -A --no-headers 2>/dev/null |
	awk '{split($3, ready, "/"); if (($4 != "Running" && $4 != "Completed") || ready[1] != ready[2]) print $1, $2}' |
	while read -r namespace pod; do
		run "pod-$namespace-$pod" kube -n "$namespace" describe pod "$pod"
	done

# The logs of the parts of the platform, current and, where a container
# restarted, the one before.
logs() {
	local namespace="$1" selector="$2" name="$3"
	run "logs-$name" kube -n "$namespace" logs -l "$selector" --all-containers --tail=2000 --prefix
	run "logs-$name-previous" kube -n "$namespace" logs -l "$selector" --all-containers --tail=500 --prefix --previous
}
logs skifity-system app.kubernetes.io/component=panel panel
logs skifity-builds app.kubernetes.io/name=skifity-registry registry
logs cert-manager app.kubernetes.io/instance=cert-manager cert-manager
logs kube-system app.kubernetes.io/name=traefik traefik
logs kube-system k8s-app=kube-dns coredns
logs cnpg-system app.kubernetes.io/name=cloudnative-pg cloudnative-pg

# Every build and one-off Job's pods, which are where a failed deployment says
# what it was doing.
kube get pods -A --no-headers -l 'job-name' 2>/dev/null | awk '{print $1, $2}' |
	while read -r namespace pod; do
		run "job-$namespace-$pod" kube -n "$namespace" logs "$pod" --all-containers --tail=500
	done

# What Skifity wrote about itself.
for file in /var/log/skifity-install.log /var/log/skifity-verify.log /var/log/skifity-upgrade.log; do
	[ -f "$file" ] && cp "$file" "$OUT/" 2>/dev/null
done

chmod -R a+rX "$OUT" 2>/dev/null || true
echo "Collected into $OUT ($(find "$OUT" -type f | wc -l | tr -d ' ') files)"
