# backend/

Source of truth for FairShare: the ledger, snapshot issuance, transcript replay and settlement, the HTTP API for the sync client, the Discord bot that also administers the server, and the public community website. One Go binary, one SQLite file. Everything implements [docs/protocol.md](../docs/protocol.md).

## Layout

```
cmd/fairshare/       main: bot, API, website and (optionally) the demo site in one process
cmd/fairshare-demo/  builds a separate database with an invented guild for the demo site
internal/
  config/            env + .env loader
  store/             SQLite schema, migrations, ledger append, audit log, guild config
  snapshot/          canonical snapshot text, checksum, paste string, Data.lua
  replay/            transcript parsing, auction rules, transcript comparison, settlement deltas
  people/            persons, characters, Battle.net, credit, ledger/standings, API tokens, addon keys
  bans/              ban issue/lift with identity propagation
  raids/             events, signups, raid channels, open raid, upload, settle/dispute/override
  discord/           the bot: panels, onboarding, server setup, signups, uploads, scheduler
  content/           the FAQ, rules, terms and getting-started text shared by the website and the bot
  verify/            Battle.net verification through Discord Connections (OAuth)
  api/               HTTP API for the sync client
  web/               the community website (read-only pages from the same database)
assets/emoji/        class and role icons the bot uploads as application emoji
```

Tests: `go test ./backend/...`.

## Running it

From the repo root, with a `.env` next to `go.mod` (copy `.env.example`):

```
go run ./backend/cmd/fairshare
```

The first run creates the database and runs migrations; later runs migrate on start. The log shows `connected as <bot>` once Discord accepts the token, then the listeners.

| Variable | Default | Meaning |
|---|---|---|
| `FAIRSHARE_DISCORD_TOKEN` | | Bot token from the Developer Portal. Empty runs the API and website without the bot. |
| `FAIRSHARE_DISCORD_APP_ID` | | Application id. |
| `FAIRSHARE_DISCORD_GUILD_ID` | | The one server the bot serves; interactions from any other server are refused. |
| `FAIRSHARE_DISCORD_CLIENT_SECRET` | | OAuth2 client secret. Turns on **Verify Battle.net** (below). Empty leaves only the typed, unverified tag. |
| `FAIRSHARE_DB_PATH` | `./data/fairshare.db` | The live ledger. |
| `FAIRSHARE_HTTP_ADDR` | `127.0.0.1:8080` | Website and API. Loopback by default: the API carries bearer tokens, so expose it only through a TLS-terminating reverse proxy that forwards here. |
| `FAIRSHARE_TRUSTED_PROXY` | unset | `1` when that proxy overwrites `X-Forwarded-For`; the per-address rate limit then keys on it. |
| `FAIRSHARE_OFFICER_ROLE_NAME` | `Officer` | Name of the officer role until Setup server has recorded its id. |
| `FAIRSHARE_DEMO_DB_PATH` | `./data/demo.db` | The demo database. While it exists the live process also serves the demo site. |
| `FAIRSHARE_DEMO_HTTP_ADDR` | `127.0.0.1:8081` | Address for the demo site; `off` stops serving it. |
| `FAIRSHARE_DISCORD_INVITE` | unset | Invite the standalone demo (`-demo`) links to; alongside the bot it borrows the live server's. |

Everything else officers change from the bot's panel: floors, credit, the split, the website address and Discord invite, the timezone, and so on. It is stored in `guild_config`.

**Running the bot twice is a mistake.** One token is one identity: two processes using it answer interactions at random and keep separate ledgers. Run the real bot in one place and use a second Discord application for development.

## The Discord bot

Three slash commands exist only as entry points: `/fairshare` (personal panel), `/fairshare-admin` (officer panel), `/fairshare-upload` (transcript upload). Everything else is buttons, menus and modals, and every finished action answers with the panel again, refreshed.

**Setup server** on the officer panel builds the server and repairs it on every run: roles (Member, Raider, Veteran, Raid Leader, Officer, plus cosmetic Horde and Alliance), categories and channels (Start Here, Community, Signups with one text channel per raid series, Raids, Voice with The Barrens, Raid 1-5 and Dungeon 1-5, Staff), the pinned cards (terms, rules, FAQ, get started), a permanent invite for the website, Discord's rules screening filled from the community rules, and onboarding (default channels and an optional faction question). It masks channel permissions to what the bot's own role holds and names anything it had to leave out. The bot's role must sit above every role it assigns, and the invite needs the voice and Create Invite permissions as well as the text ones.

Newcomers accept the rules in Discord's own dialog; the bot sees the flag clear, records it, grants Member and hands them the Register button by DM or in `#get-started`. Registration is a modal for the character name and two menus for class and roles. Raid leaders create events from their own panel row; each event gets a channel named from its schedule, such as `#tuesday-7pm-mc`.

**Sandbox** (officer panel) switches to a second database and reports every server change in `#fairshare-dev` instead of making it, so a whole raid night can be rehearsed. Press Setup server with the sandbox on to see what it would do before it touches anything.

## The website

Served by the same process at `FAIRSHARE_HTTP_ADDR`: raids with a live view of one in progress, loot history, leaderboards, a page per player, How It Works and the rules. Every page reads the database on each request. Tables sort by column through header links; the Loot and Players search boxes filter as you type (one small first-party script; the pages work without it). The site shows character names, attendance, items, prices and balances, and never Discord ids, Battle.net tags, bans, tokens, keys, dispute notes or bid salts.

**Demo site.** `go run ./backend/cmd/fairshare-demo -db data/demo.db` builds a database with an invented guild and weeks of raids played through the real signup, open, replay and settle code, so every number is consistent. While that file exists the live process serves it, with a "made up" banner, on `FAIRSHARE_DEMO_HTTP_ADDR`; `go run ./backend/cmd/fairshare -demo` serves it alone. The demo refuses a real database and the bot refuses a demo one. Rebuilding it needs the backend stopped, because the running process holds the file.

## Battle.net verification

A typed Battle.net tag proves nothing, so the bot can verify one through Discord instead: members link Battle.net in Discord's own settings (User Settings → Connections), and **Verify Battle.net** on the personal panel sends them through Discord's login on the website with the `connections` scope. The backend reads the linked tag and Discord's verified flag back and stores it; the site never shows it. To turn it on:

1. Developer Portal → the application → **OAuth2** → copy the **Client Secret** into `.env` as `FAIRSHARE_DISCORD_CLIENT_SECRET`.
2. Same tab → **Redirects** → add the website address plus `/oauth/discord/callback` (the address officers set with the panel's Website button; for a local run, `http://127.0.0.1:8080/oauth/discord/callback`).
3. Restart. The panel's button becomes Verify Battle.net; the typed fallback remains.

`creditNeedsBattlenet` (officer-editable, default off) holds unverified members at newcomer credit.

## Deploying

The backend is one static Linux binary. It wants a persistent disk for SQLite and a reverse proxy such as Caddy in front of it for HTTPS, with the website on one hostname and the demo on another, both forwarded to the loopback addresses above.

Every push to `main` that touches the backend makes GitHub Actions test it, build it for linux/amd64 and publish `fairshare-backend-linux-amd64.tar.gz` with its SHA-256 as the rolling `backend-latest` release (a `backend-v*` tag publishes a pinned one). On the server, `tools/deploy/update.sh` installs the newest build: it downloads from the public release, checks the sum, keeps the previous binary as `fairshare.prev`, and restarts the service. `tools/deploy/bundle.ps1` builds the same bundle locally for a first install or when Actions is unavailable, and `tools/deploy/install.sh` places it under `/opt/fairshare`. `.env` is written on the server by hand and never travels in a bundle.
