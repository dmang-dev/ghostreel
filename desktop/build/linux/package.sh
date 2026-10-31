#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Packages the Linux desktop app, built for WebKitGTK 4.1 (wails build
# -tags webkit2_41), as a .deb and an AppImage. The release workflow runs it:
#
#   bash desktop/build/linux/package.sh TAG BINDIR OUTDIR
#
# BINDIR holds ghostreel-desktop, LICENSE and THIRD_PARTY_NOTICES.txt. It
# writes OUTDIR/ghostreel-desktop_TAG_linux_amd64.deb and .AppImage.
#
# Needs dpkg-dev (dpkg-shlibdeps finds the .deb's dependencies), and
# appimagetool and the AppImage runtime it embeds at the paths in
# APPIMAGETOOL and APPIMAGE_RUNTIME (the workflow downloads pinned,
# checksummed releases of both).
#
# The AppImage holds the app only, not GTK or WebKitGTK: it runs on the
# system's WebKitGTK 4.1, as the .deb does, so the web engine gets the
# distribution's security updates. Both need glibc 2.35 or later (they are
# built on Ubuntu 22.04).
set -euo pipefail

if [ $# -ne 3 ]; then
	echo "usage: $0 TAG BINDIR OUTDIR" >&2
	exit 2
fi
tag=$1
bindir=$(cd "$2" && pwd)
mkdir -p "$3"
out=$(cd "$3" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
icons=$(cd "$here/.." && pwd)
base="ghostreel-desktop_${tag}_linux_amd64"
work=$(mktemp -d)
trap 'rm -rf "${work:?}"' EXIT
umask 022

# The Debian version: v1.3.0-rc1 becomes 1.3.0~rc1, which sorts before
# 1.3.0. A run on a branch (not a tag) gets 0~dev.
v=${tag#v}
if [[ $v =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
	debver=$(printf '%s' "$v" | sed 's/-/~/')
else
	debver="0~dev"
fi

# stage DIR: the files both packages install, under DIR/usr.
stage() {
	local d=$1
	install -Dm755 "$bindir/ghostreel-desktop" "$d/usr/bin/ghostreel-desktop"
	install -Dm644 "$here/ghostreel-desktop.desktop" "$d/usr/share/applications/ghostreel-desktop.desktop"
	install -Dm644 "$here/ghostreel.png" "$d/usr/share/icons/hicolor/512x512/apps/ghostreel.png"
	install -Dm644 "$here/ghostreel-mime.xml" "$d/usr/share/mime/packages/ghostreel.xml"
	install -Dm644 "$icons/replayicon.png" "$d/usr/share/icons/hicolor/512x512/mimetypes/application-x-sc2replay.png"
	install -Dm644 "$bindir/LICENSE" "$d/usr/share/doc/ghostreel-desktop/copyright"
	install -Dm644 "$bindir/THIRD_PARTY_NOTICES.txt" "$d/usr/share/doc/ghostreel-desktop/THIRD_PARTY_NOTICES.txt"
}

if command -v desktop-file-validate >/dev/null; then
	desktop-file-validate "$here/ghostreel-desktop.desktop"
fi

# --- .deb
deb="$work/deb"
stage "$deb"
mkdir -p "$deb/DEBIAN" "$work/shlibs/debian"
# dpkg-shlibdeps reads the package names from a debian/control.
printf 'Source: ghostreel\n\nPackage: ghostreel-desktop\nArchitecture: amd64\n' >"$work/shlibs/debian/control"
depends=$(cd "$work/shlibs" && dpkg-shlibdeps -O -e "$deb/usr/bin/ghostreel-desktop" | sed -n 's/^shlibs:Depends=//p')
case "$depends" in
*libwebkit2gtk-4.1*) ;;
*)
	echo "package.sh: no WebKitGTK 4.1 in the dependencies ($depends); was it built with -tags webkit2_41?" >&2
	exit 1
	;;
esac
(cd "$deb" && find usr -type f -print0 | sort -z | xargs -0 md5sum >DEBIAN/md5sums)
cat >"$deb/DEBIAN/control" <<EOF
Package: ghostreel-desktop
Version: $debver
Architecture: amd64
Maintainer: ghostreel contributors
Installed-Size: $(du -sk --exclude=DEBIAN "$deb" | cut -f1)
Depends: $depends
Section: games
Priority: optional
Homepage: https://github.com/dmang-dev/ghostreel
Description: SC2 replay viewer
 ghostreel plays a .SC2Replay back on the map's terrain without running the
 game: buildings, units, deaths, supply and income, scrubbable to any moment,
 in 2D or 3D. With the game installed, it draws the game's own models and
 textures, read from the install.
 .
 Not affiliated with or endorsed by Blizzard Entertainment.
EOF
# xz rather than Ubuntu's default zstd, which older dpkg can't read.
dpkg-deb --root-owner-group -Zxz --build "$deb" "$out/$base.deb"
dpkg-deb --info "$out/$base.deb"

# --- AppImage
app="$work/ghostreel.AppDir"
stage "$app"
# A script rather than a link to the binary, so the app's argv[0] (and
# with it GTK's program name and window class) stays ghostreel-desktop.
cat >"$app/AppRun" <<'EOF'
#!/bin/sh
here=$(dirname "$(readlink -f "$0")")
exec "$here/usr/bin/ghostreel-desktop" "$@"
EOF
chmod 755 "$app/AppRun"
cp "$here/ghostreel-desktop.desktop" "$app/ghostreel-desktop.desktop"
cp "$here/ghostreel.png" "$app/ghostreel.png"
ln -s ghostreel.png "$app/.DirIcon"
# appimagetool is itself an AppImage; extracting it to run needs no FUSE.
# Without GITHUB_REPOSITORY it doesn't guess update information (and write
# a .zsync file) for a download layout we don't have; it writes into the
# work folder all the same, so only the AppImage reaches OUTDIR.
env -u GITHUB_REPOSITORY -u GITHUB_REF APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64 \
	"${APPIMAGETOOL:?}" --runtime-file "${APPIMAGE_RUNTIME:?}" "$app" "$work/$base.AppImage"
install -m755 "$work/$base.AppImage" "$out/$base.AppImage"
ls -l "$out/$base.deb" "$out/$base.AppImage"
