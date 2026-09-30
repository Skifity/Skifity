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
# --purge and a typed confirmation.

set -eu

NAMESPACE="skifity-system"
# Builds, the builder and the image registry live in their own namespace.
BUILDS_NAMESPACE="skifity-builds"
CONFIG_DIR="/etc/skifity"
DATA_DIR="/var/lib/skifity"
KUBECONFIG_PATH="/etc/rancher/k3s/k3s.yaml"

REMOVE_K3S=0
PURGE=0
DRY_RUN="${SKIFITY_DRY_RUN:-0}"
ASSUME_YES="${SKIFITY_ASSUME_YES:-0}"

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m')
	RED=$(printf '\033[31m'); GREEN=$(printf '\033[32m'); RESET=$(printf '\033[0m')
else
	BOLD=''; DIM=''; RED=''; GREEN=''; RESET=''
fi

step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; }
ok() { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
note() { printf '  %s%s%s\n' "$DIM" "$*" "$RESET"; }

usage() {
	printf 'Usage: %s [--all] [--purge] [--yes]\n\n' "$(basename "$0")"
	printf '  --all    also remove k3s, and with it everything else running on this cluster\n'
	printf '  --purge  also delete %s and %s, including the master key\n' "$DATA_DIR" "$CONFIG_DIR"
	printf '  --dry-run  print what would happen and change nothing\n'
	printf '  --yes    do not ask\n'
}

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

if [ "$DRY_RUN" != "1" ] && [ "$(id -u)" != "0" ]; then
	printf 'This has to run as root. Try: sudo %s\n' "$0" >&2
	exit 1
fi

# run executes a command, or prints it when this is a dry run. Every change
# below goes through it, so --dry-run genuinely cannot touch anything.
run() {
	if [ "$DRY_RUN" = "1" ]; then
		printf '  %swould run:%s %s\n' "$DIM" "$RESET" "$*"
		return 0
	fi
	"$@"
}

# did reports a change that actually happened. In a dry run the "would run"
# lines have already said what was planned, so claiming it is done would be a
# lie.
did() { [ "$DRY_RUN" = "1" ] || ok "$1"; }

if [ "$DRY_RUN" = "1" ]; then
	printf '\n%sRemoving Skifity%s %s(dry run: nothing will be changed)%s\n\n' "$BOLD" "$RESET" "$DIM" "$RESET"
else
	printf '\n%sRemoving Skifity%s\n\n' "$BOLD" "$RESET"
fi
printf '  The panel will be removed.\n'
[ "$REMOVE_K3S" = "1" ] &&
	printf '  %sk3s will be removed, and every app running on this cluster with it.%s\n' "$RED" "$RESET"
[ "$PURGE" = "1" ] &&
	printf '  %sThe database and the master key will be deleted. Every stored secret,\n  and every backup taken with that key, becomes unreadable for ever.%s\n' "$RED" "$RESET"
printf '\n'

if [ "$ASSUME_YES" != "1" ] && [ "$DRY_RUN" != "1" ]; then
	if [ "$PURGE" = "1" ]; then
		printf 'Type %sdelete my data%s to confirm: ' "$BOLD" "$RESET"
		read -r answer || answer=""
		[ "$answer" = "delete my data" ] || {
			printf 'Nothing was changed.\n'
			exit 1
		}
	else
		printf 'Carry on? [y/N] '
		read -r answer || answer=""
		case "$answer" in
		y | Y | yes | YES) ;;
		*)
			printf 'Nothing was changed.\n'
			exit 1
			;;
		esac
	fi
fi

if [ -f "$KUBECONFIG_PATH" ] && command -v kubectl >/dev/null 2>&1; then
	export KUBECONFIG="$KUBECONFIG_PATH"

	step "Removing the panel"
	# The namespace goes last: deleting it first would leave the ClusterRoleBinding
	# pointing at a ServiceAccount that no longer exists.
	run kubectl delete ingress skifity-panel -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
	run kubectl delete deployment skifity-panel -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
	run kubectl delete service skifity-panel -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
	run kubectl delete clusterrolebinding skifity-panel --ignore-not-found >/dev/null 2>&1 || true
	run kubectl delete namespace "$NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
	# The builder and the images it produced. Apps keep running: their images
	# are already pulled onto the nodes that run them.
	run kubectl delete namespace "$BUILDS_NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
	did "Panel removed"

	if [ "$REMOVE_K3S" != "1" ]; then
		note "Your apps are still running. Their namespaces were not touched."
		note "Run again with --all to remove k3s and everything on it."
	fi
fi

if [ "$REMOVE_K3S" = "1" ]; then
	step "Removing k3s"
	if [ -x /usr/local/bin/k3s-uninstall.sh ]; then
		run /usr/local/bin/k3s-uninstall.sh >/dev/null 2>&1 || true
		did "k3s removed"
	elif [ -x /usr/local/bin/k3s-agent-uninstall.sh ]; then
		run /usr/local/bin/k3s-agent-uninstall.sh >/dev/null 2>&1 || true
		did "k3s agent removed"
	else
		note "k3s does not look installed by the k3s installer; nothing to remove."
	fi
fi

if [ "$PURGE" = "1" ]; then
	step "Deleting the panel's data"
	run rm -rf "$DATA_DIR" "$CONFIG_DIR"
	run rm -f /etc/rancher/k3s/registries.yaml
	did "$DATA_DIR and $CONFIG_DIR deleted"
else
	step "Keeping your data"
	note "$DATA_DIR still holds the database, and $CONFIG_DIR the master key."
	note "Installing again picks up exactly where this left off."
fi

run rm -f /usr/local/bin/skifity

if [ "$DRY_RUN" = "1" ]; then
	printf '\n%sNothing was changed.%s Run without --dry-run to do it for real.\n\n' "$BOLD" "$RESET"
else
	printf '\n%sSkifity has been removed.%s\n\n' "$BOLD" "$RESET"
fi
