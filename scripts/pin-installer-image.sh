#!/bin/sh
# Pin the installer a release attaches to the image that release pushed.
#
#   sh scripts/pin-installer-image.sh REFERENCE INSTALLER OUTPUT
#
# REFERENCE is the image as the release published it, by tag and digest:
# ghcr.io/owner/name:v1.2.3@sha256:<64 hex digits>. The release workflow runs
# this once the image is pushed, because only then is the digest known, and
# attaches OUTPUT in place of installer/install.sh. A tag can be moved by
# anybody who can push to the registry; a digest cannot, so an installer pinned
# this way installs the image its release built, whatever the tag says later.
#
# It refuses rather than guesses: a reference that is not a tag and a digest,
# an installer without exactly one empty RELEASED_IMAGE line to fill, or a
# result that would not install REFERENCE when it runs.
set -eu

if [ $# -ne 3 ]; then
	echo "usage: $0 REFERENCE INSTALLER OUTPUT" >&2
	exit 2
fi
ref=$1
in=$2
out=$3

if ! printf '%s' "$ref" | grep -Eq '^[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$'; then
	echo "\"$ref\" is not an image by tag and digest, such as ghcr.io/owner/name:v1.2.3@sha256:<64 hex digits>." >&2
	exit 1
fi

count=$(grep -c '^RELEASED_IMAGE=""$' "$in" || true)
if [ "$count" != 1 ]; then
	echo "$in has ${count:-no} empty RELEASED_IMAGE lines, not one, so there is nothing to fill in; is it pinned already?" >&2
	exit 1
fi

tmp="$out.pinning"
trap 'rm -f "$tmp"' EXIT
# The reference holds no | and no &, which the pattern above makes sure of, so
# sed takes it as it is.
sed "s|^RELEASED_IMAGE=\"\"\$|RELEASED_IMAGE=\"$ref\"|" "$in" >"$tmp"

# The file this makes is the one people run, so it is read back the way it
# will run: it has to parse, and left to itself it has to install REFERENCE.
# Nothing from this shell's environment may change what it picks. The script
# is in single quotes so the inner shell expands it.
sh -n "$tmp"
# shellcheck disable=SC2016
got=$(env -i PATH="$PATH" sh -c 'SKIFITY_INSTALLER_LIB=1 . "$1" && printf "%s" "$IMAGE"' pin "$tmp")
if [ "$got" != "$ref" ]; then
	echo "Pinned, $in would install \"$got\", not \"$ref\": its RELEASED_VERSION or PROJECT_REPO names another image." >&2
	exit 1
fi

chmod 0644 "$tmp"
mv "$tmp" "$out"
trap - EXIT
echo "$out installs $ref"
