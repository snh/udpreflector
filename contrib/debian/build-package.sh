#!/bin/sh
set -eu

architecture=${1:-}
case "$architecture" in
	amd64|arm64) ;;
	*)
		echo "usage: $0 <amd64|arm64>" >&2
		exit 2
		;;
esac

if ! command -v dpkg-deb >/dev/null 2>&1; then
	echo "dpkg-deb is required to build Debian packages" >&2
	exit 1
fi

script_directory=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH='' cd -- "$script_directory/../.." && pwd)
output_directory=${OUTPUT_DIR:-"$repository_root/dist"}

if [ -z "${BUILD_IDENTIFIER:-}" ]; then
	commit=$(git -C "$repository_root" rev-parse --short=7 HEAD 2>/dev/null || printf unknown)
	dirty=$(test -z "$(git -C "$repository_root" status --porcelain 2>/dev/null)" || printf '%s' -dirty)
	BUILD_IDENTIFIER=$(date -u +%Y%m%d)-$commit$dirty
fi

package_version=${PACKAGE_VERSION:-$BUILD_IDENTIFIER}
maintainer=${MAINTAINER:-"Steven Honson <tasmaritime@steven.honson.au>"}
binary=${BINARY:-"$output_directory/udpreflector-linux-$architecture-$BUILD_IDENTIFIER"}

case "$output_directory" in
	/*) ;;
	*) output_directory="$repository_root/$output_directory" ;;
esac
case "$binary" in
	/*) ;;
	*) binary="$repository_root/$binary" ;;
esac

if command -v dpkg >/dev/null 2>&1 && ! dpkg --validate-version "$package_version"; then
	echo "invalid Debian package version: $package_version" >&2
	exit 1
fi

mkdir -p "$output_directory"
if [ -z "${BINARY:-}" ]; then
	CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" "${MAKE:-make}" -C "$repository_root" build \
		BINARY="$binary" BUILD_IDENTIFIER="$BUILD_IDENTIFIER"
fi
if [ ! -x "$binary" ]; then
	echo "package binary is missing or not executable: $binary" >&2
	exit 1
fi

package_root=$(mktemp -d "${TMPDIR:-/tmp}/udpreflector-deb.XXXXXX")
trap 'rm -rf "$package_root"' EXIT HUP INT TERM
chmod 0755 "$package_root"

install -Dm0755 "$binary" "$package_root/usr/bin/udpreflector"
install -Dm0644 "$repository_root/contrib/systemd/udpreflector@.service" \
	"$package_root/usr/lib/systemd/system/udpreflector@.service"
install -Dm0644 "$repository_root/contrib/systemd/example.conf" \
	"$package_root/usr/share/doc/udpreflector/examples/udpreflector.conf"
install -Dm0644 "$repository_root/README.md" \
	"$package_root/usr/share/doc/udpreflector/README.md"
install -Dm0644 "$repository_root/LICENSE" \
	"$package_root/usr/share/doc/udpreflector/copyright"
install -Dm0644 "$script_directory/udpreflector.1" \
	"$package_root/usr/share/man/man1/udpreflector.1"
gzip -9n "$package_root/usr/share/man/man1/udpreflector.1"
install -Dm0644 "$script_directory/lintian-overrides" \
	"$package_root/usr/share/lintian/overrides/udpreflector"
install -d -m0755 "$package_root/etc/udpreflector"
install -d -m0755 "$package_root/DEBIAN"
install -m0755 "$script_directory/postinst" "$package_root/DEBIAN/postinst"
install -m0755 "$script_directory/postrm" "$package_root/DEBIAN/postrm"

cat >"$package_root/usr/share/doc/udpreflector/changelog.Debian" <<EOF
udpreflector ($package_version) unstable; urgency=medium

  * Build $BUILD_IDENTIFIER.

 -- $maintainer  $(date -R)
EOF
gzip -9n "$package_root/usr/share/doc/udpreflector/changelog.Debian"

installed_size=$(du -sk "$package_root/usr" "$package_root/etc" | awk '{ total += $1 } END { print total }')
cat >"$package_root/DEBIAN/control" <<EOF
Package: udpreflector
Version: $package_version
Section: net
Priority: optional
Architecture: $architecture
Maintainer: $maintainer
Installed-Size: $installed_size
Description: Dual-stack UDP packet reflector
 Listens on a configured range of UDP ports, learns recently active remote
 endpoints, and forwards each packet to the other active peers on its port.
EOF

(
	cd "$package_root"
	find etc usr -type f -print | LC_ALL=C sort | xargs md5sum
) >"$package_root/DEBIAN/md5sums"

package="$output_directory/udpreflector_${package_version}_${architecture}.deb"
dpkg-deb --root-owner-group --build "$package_root" "$package"
printf '%s\n' "$package"
