#!/bin/sh
# Gzips one release binary for the panel's image to carry.
#
# GoReleaser runs this after each binary it builds (.goreleaser.yaml,
# builds[].hooks.post), with the binary's path, its build target and its
# extension:
#
#   sh scripts/release-cli.sh /abs/path/skifity darwin_arm64 ""
#
# /api/cli/download serves a laptop the CLI for its own platform, from
# skifity-<os>-<arch>[.exe].gz in the image. The image built by `make image`
# has always carried them; the release image carried none, so on every
# released install the panel refused a Mac or a Windows laptop its CLI.
#
# The copies go to dist/cli/<image platform>/, one directory per platform the
# image is built for, each holding every CLI except that platform's own: the
# panel serves its own platform from its own binary, and a copy would only
# make the image larger. Dockerfile.release copies dist/cli/$TARGETPLATFORM.
# The same layout the root Dockerfile builds.
set -eu

binary="$1"
target="$2"
ext="${3:-}"

# A target is darwin_arm64, linux_amd64_v1 or linux_arm64_v8.0: the part after
# the architecture is a microarchitecture level, which the name leaves out.
os="${target%%_*}"
rest="${target#*_}"
arch="${rest%%_*}"

[ -n "$os" ] && [ -n "$arch" ] && [ "$os" != "$target" ] || {
	echo "release-cli.sh: cannot read an OS and architecture from target '$target'" >&2
	exit 1
}
[ -s "$binary" ] || {
	echo "release-cli.sh: $binary is missing or empty" >&2
	exit 1
}

# The platforms Dockerfile.release is built for; .goreleaser.yaml's
# dockers_v2.platforms names the same two.
for image in linux/amd64 linux/arm64; do
	[ "$image" = "$os/$arch" ] && continue
	mkdir -p "dist/cli/$image"
	gzip -9 -c "$binary" >"dist/cli/$image/skifity-$os-$arch$ext.gz"
done
