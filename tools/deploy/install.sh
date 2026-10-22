#!/usr/bin/env bash
# Installs a FairShare bundle on the VM (backend/README.md, Deploying). Run as root from
# the directory the bundle was extracted into:
#
#   mkdir -p ~/bundle && tar -xzf ~/fairshare-bundle.tar.gz -C ~/bundle && sudo bash ~/bundle/install.sh && rm -r ~/bundle
#
# It keeps the previous binary as fairshare.prev, installs the emoji and, when the bundle
# carries one, the demo database (never over a live one), then restarts the service if it
# exists. Nothing here touches .env.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
APP=/opt/fairshare
USER=fairshare

if [ "$(id -u)" -ne 0 ]; then
	echo "run with sudo" >&2
	exit 1
fi
if ! id "$USER" >/dev/null 2>&1; then
	echo "user $USER does not exist: create it first: useradd --system --home /opt/fairshare --shell /usr/sbin/nologin fairshare" >&2
	exit 1
fi
install -d -o "$USER" -g "$USER" -m 750 "$APP" "$APP/data"
install -d -o "$USER" -g "$USER" -m 755 "$APP/assets/emoji"

if [ -f "$APP/fairshare" ]; then
	cp -p "$APP/fairshare" "$APP/fairshare.prev"
fi
install -o "$USER" -g "$USER" -m 755 "$HERE/fairshare" "$APP/fairshare"
echo "installed binary (previous kept as fairshare.prev)"

if ls "$HERE"/emoji/*.png >/dev/null 2>&1; then
	install -o "$USER" -g "$USER" -m 644 "$HERE"/emoji/*.png "$APP/assets/emoji/"
	echo "installed $(ls "$HERE"/emoji/*.png | wc -l) emoji"
fi

if [ -f "$HERE/demo.db" ]; then
	if [ -f "$APP/data/demo.db" ] && systemctl is-active --quiet fairshare; then
		systemctl stop fairshare # the running process holds demo.db open
		STOPPED=1
	fi
	rm -f "$APP/data/demo.db" "$APP/data/demo.db-wal" "$APP/data/demo.db-shm"
	install -o "$USER" -g "$USER" -m 644 "$HERE/demo.db" "$APP/data/demo.db"
	echo "installed demo database"
fi

# LoadState is "loaded" only when a unit file exists; list-unit-files' text layout varies
# between systemd versions and once missed a unit that was running.
if [ "$(systemctl show -p LoadState --value fairshare.service 2>/dev/null)" = loaded ]; then
	systemctl restart fairshare
	sleep 2
	systemctl --no-pager --lines=5 status fairshare || true
else
	echo "no fairshare.service yet: create it (step 8), then: sudo systemctl enable --now fairshare"
fi

if [ -f "$HERE/update.sh" ]; then
	install -m 755 "$HERE/update.sh" /usr/local/bin/fairshare-update
	echo "installed fairshare-update (sudo fairshare-update installs the newest build from GitHub)"
fi
rm -rf "$HERE/fairshare" "$HERE/emoji" "$HERE/demo.db" "$HERE/install.sh" "$HERE/update.sh"
echo "done"
