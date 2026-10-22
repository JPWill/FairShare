# tools/

Developer utilities that are not part of any shipped component.

- `icons/` — `python tools/icons/make_icons.py --sheet` regenerates the class and role emoji PNGs in `backend/assets/emoji/` (needs Pillow).
- `release/` — `go run ./tools/release` packages `addon/FairShare` into `dist/FairShare-<version>.zip`, excluding `Data.lua` and folder READMEs. Version comes from the TOC.
- `deploy/` — the backend on a Linux server. `bundle.ps1` cross-compiles the backend and packs it with the emoji, the demo database (`-Demo`) and the two scripts into `dist/fairshare-bundle.tar.gz`; `install.sh` places a bundle under `/opt/fairshare` and restarts the service; `update.sh` (installed as `fairshare-update`) fetches the newest build published by GitHub Actions and installs it. See `backend/README.md`.

The offline addon test suite lives in `tests/addon/`, not here.
