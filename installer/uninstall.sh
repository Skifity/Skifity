#!/bin/sh
# Removes Skifity from this server.
#
#   skifity-uninstall            remove the panel, keep k3s and your data
#   skifity-uninstall --all      remove k3s too, and everything running on it
#   skifity-uninstall --purge    also delete the database and the master key
#   skifity-uninstall --dry-run  print what would happen and change nothing
#
# The default is deliberately conservative. Deleting the master key makes every
# stored secret and every existing backup unreadable for ever, so that needs
# --purge and a typed confirmation; removing k3s deletes every app and volume
# on the cluster, so that needs a typed confirmation too.
#
# It says what it could not do rather than what it meant to: a step that fails
# is reported, the rest still run, and the exit status is not zero. Running it
# again is safe, and is how a removal that stopped halfway is finished.
#
# POSIX sh, like the installer.

set -eu

NAMESPACE="skifity-system"
# Builds, the builder and the image registry live in their own namespace. This
# and the two above must match internal/kube; a Go test checks that they do.
BUILDS_NAMESPACE="skifity-builds"
# Every environment the panel creates carries this label on its namespace,
# which is how --all can say how many there are before it deletes them.
ENVIRONMENT_LABEL="skifity.com/project-id"
CONFIG_DIR="/etc/skifity"
DATA_DIR="/var/lib/skifity"
KUBECONFIG_PATH="/etc/rancher/k3s/k3s.yaml"
# What the installer put on this machine.
REGISTRIES_PATH="/etc/rancher/k3s/registries.yaml"
# The rule that keeps the registry's NodePort closed, and the unit that repeats
# it at boot (guard_registry_port in install.sh).
REGISTRY_NODE_PORT=30500
REGISTRY_GUARD_UNIT="/etc/systemd/system/skifity-registry-guard.service"
CLI_PATH="/usr/local/bin/skifity"
UNINSTALLER_PATH="/usr/local/bin/skifity-uninstall"
# What k3s's own installer left to remove it with.
K3S_UNINSTALL="/usr/local/bin/k3s-uninstall.sh"
K3S_AGENT_UNINSTALL="/usr/local/bin/k3s-agent-uninstall.sh"
# Held by the installer while it runs; an uninstall must not start under it.
LOCK_DIR="/run/skifity-install.lock"
LOG_FILE="/var/log/skifity-uninstall.log"
# Where a question is asked and answered, for the same reason as in the
# installer: stdin is not always a person.
TTY_DEV="/dev/tty"

REMOVE_K3S=0
PURGE=0
DRY_RUN="${SKIFITY_DRY_RUN:-0}"
ASSUME_YES="${SKIFITY_ASSUME_YES:-0}"

# State the steps hand to each other. up, down or none: whether a cluster is
# here and answering.
CLUSTER_STATE="none"
ENVIRONMENTS=""
FAILURES=0

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m'); YELLOW=$(printf '\033[33m')
	RED=$(printf '\033[31m'); GREEN=$(printf '\033[32m'); RESET=$(printf '\033[0m')
else
	BOLD=''; DIM=''; YELLOW=''; RED=''; GREEN=''; RESET=''
fi

log() {
	printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" >>"$LOG_FILE" 2>/dev/null || true
}

step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; log "STEP $*"; }
ok() { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; log "OK $*"; }
note() { printf '  %s%s%s\n' "$DIM" "$*" "$RESET"; log "NOTE $*"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$*"; log "WARN $*"; }

have() { command -v "$1" >/dev/null 2>&1; }

usage() {
	printf 'Usage: %s [--all] [--purge] [--dry-run] [--yes]\n\n' "$(basename "$0")"
	printf '  (no options)  remove the panel; k3s, your apps and your data stay\n'
	printf '  --all         also remove k3s, and with it every app and volume on this cluster\n'
	printf '  --purge       also delete %s and %s, including the master key\n' "$DATA_DIR" "$CONFIG_DIR"
	printf '  --dry-run     print what would happen and change nothing\n'
	printf '  -y, --yes     do not ask; for a script that has already decided\n'
	printf '  -h, --help    print this and exit\n\n'
	printf 'Without a terminal and without --yes it asks nothing and removes nothing.\n'
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--all) REMOVE_K3S=1 ;;
		--purge) PURGE=1 ;;
		--dry-run | -n) DRY_RUN=1 ;;
		--yes | -y) ASSUME_YES=1 ;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			printf 'Unknown option: %s\n\n' "$1" >&2
			usage >&2
			exit 2
			;;
		esac
		shift
	done
}

# did reports a change that actually happened. In a dry run the "would run"
# lines have already said what was planned, so claiming it is done would be a
# lie.
did() { [ "$DRY_RUN" = "1" ] || ok "$1"; }

# run executes a command, or prints it when this is a dry run. Every change
# below goes through it, so --dry-run genuinely cannot touch anything. What the
# command says goes to the log, and its exit status is returned.
run() {
	if [ "$DRY_RUN" = "1" ]; then
		printf '  %swould run:%s %s\n' "$DIM" "$RESET" "$*"
		return 0
	fi
	log "RUN $*"
	"$@" >>"$LOG_FILE" 2>&1
}

# attempt runs one change and says so when it did not work, instead of
# swallowing the failure. The rest of the removal still goes ahead — a stuck
# namespace should not leave the master key behind when it was asked for — and
# the failure is counted, so the last line and the exit status tell the truth.
attempt() {
	what=$1
	shift
	if run "$@"; then
		return 0
	fi
	FAILURES=$((FAILURES + 1))
	warn "Could not ${what}. ${LOG_FILE} has what it said."
	return 1
}

tty_usable() { (exec <"$TTY_DEV") 2>/dev/null; }

# ask_phrase reads one line from the terminal and succeeds when it is exactly
# what was asked for.
ask_phrase() {
	printf 'Type %s%s%s to confirm: ' "$BOLD" "$1" "$RESET" >>"$TTY_DEV"
	answer=""
	read -r answer <"$TTY_DEV" || answer=""
	[ "$answer" = "$1" ]
}

# --- what is here -----------------------------------------------------------

# detect_cluster works out whether there is a cluster to take the panel out of,
# and whether it is answering. A k3s that is stopped used to be skipped without
# a word, and the run ended by saying the panel was removed.
detect_cluster() {
	CLUSTER_STATE="none"
	[ -f "$KUBECONFIG_PATH" ] && have kubectl || return 0
	CLUSTER_STATE="down"
	KUBECONFIG="$KUBECONFIG_PATH" kubectl get --raw /readyz --request-timeout=10s >/dev/null 2>&1 || return 0
	CLUSTER_STATE="up"
	export KUBECONFIG="$KUBECONFIG_PATH"
	ENVIRONMENTS=$(kubectl get namespaces -l "$ENVIRONMENT_LABEL" -o name --request-timeout=15s 2>/dev/null | wc -l | tr -d ' ')
}

# A running install and an uninstall would undo each other.
refuse_while_installing() {
	[ -d "$LOCK_DIR" ] || return 0
	holder=$(cat "$LOCK_DIR/pid" 2>/dev/null || true)
	if [ -n "$holder" ] && kill -0 "$holder" 2>/dev/null; then
		printf 'The installer is running on this server, as process %s.\n' "$holder" >&2
		printf 'Let it finish, or stop it, before removing anything. Nothing was changed.\n' >&2
		exit 1
	fi
}

# safe_dir refuses to be pointed at a place nobody means to delete: it has to
# be an absolute path at least two levels down, with no .. in it.
safe_dir() {
	case "$1" in
	*..*) return 1 ;;
	/?*/?*) return 0 ;;
	*) return 1 ;;
	esac
}

# --- the plan and the question ----------------------------------------------

print_plan() {
	if [ "$DRY_RUN" = "1" ]; then
		printf '\n%sRemoving Skifity%s %s(dry run: nothing will be changed)%s\n\n' "$BOLD" "$RESET" "$DIM" "$RESET"
	else
		printf '\n%sRemoving Skifity%s\n\n' "$BOLD" "$RESET"
	fi
	printf '  The panel will be removed.\n'
	if [ "$REMOVE_K3S" = "1" ]; then
		printf '  %sk3s will be removed, and every app running on this cluster with it.%s\n' "$RED" "$RESET"
		case "$ENVIRONMENTS" in
		"") ;;
		0) printf '  %sNo environments were found on it.%s\n' "$DIM" "$RESET" ;;
		1) printf '  %s1 environment is on it, with its apps, databases and volumes.%s\n' "$RED" "$RESET" ;;
		*) printf '  %s%s environments are on it, with their apps, databases and volumes.%s\n' "$RED" "$ENVIRONMENTS" "$RESET" ;;
		esac
	fi
	if [ "$PURGE" = "1" ]; then
		printf '  %sThe database and the master key will be deleted. Every stored secret,\n  and every backup taken with that key, becomes unreadable for ever.%s\n' "$RED" "$RESET"
	fi
	printf '\n'
}

# confirm_removal asks, on the terminal, in proportion to what cannot be undone:
# a yes for the panel, a typed phrase for k3s and everything on it, and a longer
# one for the data. With nobody to ask, nothing is removed — an uninstaller that
# guesses yes is the wrong kind to have.
confirm_removal() {
	[ "$DRY_RUN" = "1" ] && return 0
	[ "$ASSUME_YES" = "1" ] && return 0
	if ! tty_usable; then
		printf 'There is nobody to ask: no terminal is attached.\n' >&2
		printf 'Run it again with --yes to go ahead, or --dry-run to see what it would do.\n' >&2
		printf 'Nothing was changed.\n' >&2
		exit 1
	fi
	if [ "$PURGE" = "1" ]; then
		ask_phrase "delete my data" || declined
	elif [ "$REMOVE_K3S" = "1" ]; then
		ask_phrase "remove k3s" || declined
	else
		printf 'Carry on? [y/N] ' >>"$TTY_DEV"
		answer=""
		read -r answer <"$TTY_DEV" || answer=""
		case "$answer" in
		y | Y | yes | YES) ;;
		*) declined ;;
		esac
	fi
}

declined() {
	printf 'Nothing was changed.\n'
	exit 1
}

# --- removing it ------------------------------------------------------------

remove_panel() {
	step "Removing the panel"
	case "$CLUSTER_STATE" in
	none)
		note "There is no cluster here, so there is nothing of the panel to take out of one."
		return 0
		;;
	down)
		if [ "$REMOVE_K3S" = "1" ]; then
			note "k3s is not answering, and is about to be removed with everything in it."
		else
			FAILURES=$((FAILURES + 1))
			warn "k3s is not answering, so the panel's objects could not be removed."
			note "Start it with:  systemctl start k3s   and run this again, or use --all to remove k3s itself."
		fi
		return 0
		;;
	esac

	failed_before=$FAILURES
	# The namespace goes last: deleting it first would leave the ClusterRoleBinding
	# pointing at a ServiceAccount that no longer exists.
	attempt "delete the panel's Ingress" kubectl delete ingress skifity-panel -n "$NAMESPACE" --ignore-not-found --timeout=120s || true
	attempt "delete the panel's Deployment" kubectl delete deployment skifity-panel -n "$NAMESPACE" --ignore-not-found --timeout=180s || true
	attempt "delete the panel's Service" kubectl delete service skifity-panel -n "$NAMESPACE" --ignore-not-found --timeout=120s || true
	attempt "delete the panel's ClusterRoleBinding" kubectl delete clusterrolebinding skifity-panel --ignore-not-found --timeout=120s || true
	attempt "delete the namespace ${NAMESPACE}" kubectl delete namespace "$NAMESPACE" --ignore-not-found --timeout=240s || true
	# The builder and the images it produced. Apps keep running: their images
	# are already pulled onto the nodes that run them.
	attempt "delete the namespace ${BUILDS_NAMESPACE}" kubectl delete namespace "$BUILDS_NAMESPACE" --ignore-not-found --timeout=240s || true
	if [ "$FAILURES" = "$failed_before" ]; then
		did "Panel removed"
	fi

	if [ "$REMOVE_K3S" != "1" ]; then
		note "Your apps are still running. Their namespaces were not touched."
		note "Run again with --all to remove k3s and everything on it."
	fi
}

remove_k3s() {
	step "Removing k3s"
	if [ -x "$K3S_UNINSTALL" ]; then
		if attempt "remove k3s" "$K3S_UNINSTALL"; then
			did "k3s removed"
		fi
	elif [ -x "$K3S_AGENT_UNINSTALL" ]; then
		if attempt "remove the k3s agent" "$K3S_AGENT_UNINSTALL"; then
			did "k3s agent removed"
		fi
	elif have k3s; then
		# Something is here, and k3s's own installer did not put it here, so
		# there is no script to take it out with. Saying "nothing to remove"
		# would be wrong.
		FAILURES=$((FAILURES + 1))
		warn "k3s is installed, but ${K3S_UNINSTALL} is not: it was not installed with k3s's own installer."
		note "Remove it the way it was installed, then run this again."
	else
		note "k3s does not look installed; nothing to remove."
	fi
	remove_registry_guard
}

# remove_registry_guard takes away what keeps the registry's port closed. It
# goes with k3s: the registry it protects is gone, and a rule nobody remembers
# is a rule somebody debugs for an afternoon.
remove_registry_guard() {
	if have iptables; then
		# Until -C says it is gone, so a rule added twice by hand goes too.
		guard_rule="PREROUTING -p tcp --dport ${REGISTRY_NODE_PORT} -m addrtype --dst-type LOCAL ! -i lo -j DROP"
		# shellcheck disable=SC2086 # the rule is words on purpose
		while [ "$DRY_RUN" != "1" ] && iptables -w -t raw -C $guard_rule 2>/dev/null; do
			# shellcheck disable=SC2086
			iptables -w -t raw -D $guard_rule >>"$LOG_FILE" 2>&1 || break
		done
	fi
	if [ -e "$REGISTRY_GUARD_UNIT" ]; then
		if have systemctl; then
			attempt "stop the registry port guard" systemctl disable --now skifity-registry-guard.service || true
		fi
		attempt "remove the registry port guard" rm -f "$REGISTRY_GUARD_UNIT" || true
		if have systemctl && [ "$DRY_RUN" != "1" ]; then systemctl daemon-reload >>"$LOG_FILE" 2>&1 || true; fi
	fi
}

purge_data() {
	step "Deleting the panel's data"
	removed=1
	for dir in "$DATA_DIR" "$CONFIG_DIR"; do
		if ! safe_dir "$dir"; then
			FAILURES=$((FAILURES + 1))
			removed=0
			warn "Refusing to delete \"${dir}\": it is not a directory this uninstaller would ever be pointed at."
			continue
		fi
		attempt "delete ${dir}" rm -rf "$dir" || removed=0
	done
	attempt "delete ${REGISTRIES_PATH}" rm -f "$REGISTRIES_PATH" || removed=0
	if [ "$removed" = 1 ]; then
		did "$DATA_DIR and $CONFIG_DIR deleted"
	fi
}

# summary says what is still on this server, in the order somebody would want
# to deal with it, and what to run for each. A removal that leaves things behind
# on purpose should say so, or the next person finds them by accident.
summary() {
	if [ "$DRY_RUN" = "1" ]; then
		printf '\n%sNothing was changed.%s Run without --dry-run to do it for real.\n\n' "$BOLD" "$RESET"
		return 0
	fi
	if [ "$FAILURES" -gt 0 ]; then
		printf '\n%s%sSkifity was not completely removed.%s %s did not work, and each says so above.\n' "$BOLD" "$YELLOW" "$RESET" \
			"$([ "$FAILURES" = 1 ] && printf 'One step' || printf '%s steps' "$FAILURES")"
		printf 'Running this again is safe, and carries on from here. The log is at %s.\n' "$LOG_FILE"
	else
		printf '\n%sSkifity has been removed.%s\n' "$BOLD" "$RESET"
	fi

	if [ "$REMOVE_K3S" != "1" ] || [ "$PURGE" != "1" ]; then
		printf '\n  Still on this server:\n'
		[ "$REMOVE_K3S" = "1" ] || printf '    k3s, and every app on it            skifity-uninstall --all\n'
		[ "$PURGE" = "1" ] || printf '    the database and the master key     skifity-uninstall --purge\n'
		[ "$PURGE" = "1" ] || printf '      (%s and %s)\n' "$DATA_DIR" "$CONFIG_DIR"
	fi
	if [ "$REMOVE_K3S" = "1" ]; then
		printf '\n  %sIf the installer opened ports 80 and 443 in this server'"'"'s firewall, those rules\n  were left as they were: they are yours now.%s\n' "$DIM" "$RESET"
	fi
	printf '\n'
}

main() {
	parse_args "$@"

	if [ "$DRY_RUN" != "1" ] && [ "$(id -u)" != "0" ]; then
		printf 'This has to run as root. Try: sudo %s\n' "$0" >&2
		exit 1
	fi

	# The log can say what kubectl and k3s's uninstaller said, so it is
	# readable by root alone, like the installer's.
	if [ "$DRY_RUN" = "1" ]; then
		LOG_FILE=/dev/null
	elif (umask 077 && : >>"$LOG_FILE") 2>/dev/null; then
		chmod 0600 "$LOG_FILE" 2>/dev/null || true
	else
		LOG_FILE=/dev/null
	fi

	[ "$DRY_RUN" = "1" ] || refuse_while_installing

	# k3s puts kubectl in /usr/local/bin, which sudo's PATH does not always have.
	have kubectl || PATH="/usr/local/bin:$PATH"

	detect_cluster
	print_plan
	confirm_removal
	log "uninstall starting: all=${REMOVE_K3S} purge=${PURGE} cluster=${CLUSTER_STATE}"

	remove_panel
	[ "$REMOVE_K3S" != "1" ] || remove_k3s
	if [ "$PURGE" = "1" ]; then
		purge_data
	else
		step "Keeping your data"
		note "$DATA_DIR still holds the database, and $CONFIG_DIR the master key."
		note "Installing again picks up exactly where this left off."
	fi

	attempt "remove the command line tool" rm -f "$CLI_PATH" || true
	# With k3s and the data both gone nothing is left for this to remove, and a
	# removal that did not finish keeps it, so running it again works.
	if [ "$REMOVE_K3S" = "1" ] && [ "$PURGE" = "1" ] && [ "$FAILURES" = "0" ]; then
		run rm -f "$UNINSTALLER_PATH" || true
	fi

	summary
	log "uninstall finished: ${FAILURES} problem(s)"
	[ "$FAILURES" = "0" ] || exit 1
}

# Sourcing this file with SKIFITY_UNINSTALLER_LIB=1 defines the functions above
# and stops here, so the smoke test can run them against stand-ins. Nothing
# else reads this variable.
if [ "${SKIFITY_UNINSTALLER_LIB:-}" = "1" ]; then
	# shellcheck disable=SC2317
	return 0 2>/dev/null || exit 0
fi

main "$@"
