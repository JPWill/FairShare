#!/usr/bin/env bash
# fairshare-update: install the newest backend build published by GitHub Actions
# (.github/workflows/backend-build.yml), or a tagged one. Runs on the VM; nothing is
# needed on your PC beyond git push.
#
#   sudo fairshare-update                 # the rolling backend-latest release
#   sudo fairshare-update backend-v1.2.0  # a specific tag
#
# Downloads the bundle and its SHA256SUMS from the public release, verifies the sum,
# then runs the bundle's install.sh (previous binary kept as fairshare.prev, service
# restarted). The demo database and .env are never touched by this path.
set -euo pipefail

REPO="JPWill/FairShare"
TAG="${1:-backend-latest}"
ASSET="fairshare-backend-linux-amd64.tar.gz"
BASE="https://github.com/$REPO/releases/download/$TAG"

if [ "$(id -u)" -ne 0 ]; then
	echo "run with sudo" >&2
	exit 1
fi
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"
echo "fetching $TAG"
curl -fsSL --retry 3 -o "$ASSET" "$BASE/$ASSET"
curl -fsSL --retry 3 -o SHA256SUMS "$BASE/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
mkdir bundle
tar -xzf "$ASSET" -C bundle
if [ -f bundle/update.sh ] && ! cmp -s bundle/update.sh /usr/local/bin/fairshare-update; then
	install -m 755 bundle/update.sh /usr/local/bin/fairshare-update
	echo "updated fairshare-update itself"
fi
bash bundle/install.sh
if [ -x /opt/fairshare/fairshare ]; then
	echo "installed: $(/opt/fairshare/fairshare -version 2>/dev/null || echo "$TAG")"
fi
