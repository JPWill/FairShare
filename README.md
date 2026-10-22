# FairShare

Cross-guild PUG raids for World of Warcraft: Forever, with loot that is fair and checkable. Gold-for-loot runs are restricted on Forever; this is the alternative.

Every boss kill belongs to everyone who was there. Loot is auctioned for **shares**, the community's own points: bids are sealed, the winner pays the second-highest bid, and what they pay is split across the raid. Shares follow you to every character. Nothing is minted and nothing is destroyed, so every balance in the community adds up to zero, and every raider's addon checks every auction.

**Raiding with us?** Everything you need is on the website, [fairsharewow.com](https://fairsharewow.com): [How It Works](https://fairsharewow.com/faq), [the rules](https://fairsharewow.com/rules), the public ledger, and the Discord link. Join the Discord, accept the rules, press Register, install the addon, sign up for a raid. The demo at [demo.fairsharewow.com](https://demo.fairsharewow.com) shows the site with a made-up guild.

**Building or running it?** Read on. The contract between the parts is [docs/protocol.md](docs/protocol.md); each part's README is its operating guide.

## The parts

| Part | Path | What it does |
|---|---|---|
| Addon | `addon/FairShare/` | Runs commit-reveal auctions in-game, lets every raider verify every auction, records a transcript. **Zero external libraries. GUI only, no slash commands.** |
| Backend + bot | `backend/` | Source of truth: ledger, snapshots, transcript replay and settlement. The Discord bot also runs the server (announcements, signups, roles, bans) so no other bots are needed. The same process serves the community website: raid history, loot, leaderboards and a page per player. Go, SQLite. |
| Sync client | `client/` | Leader-only desktop executable that writes the current snapshot into the addon folder and uploads transcripts after a raid. Copy-paste strings come first and stay as the fallback. |

Supporting folders:

| Path | Contents |
|---|---|
| `docs/` | `protocol.md` (the wire protocol, snapshot and transcript formats, replay and settlement rules), `rules.md` (the community rules as published; the bot and the website serve the same text), `brand/` (logo artwork). Each part's own README is its operating guide. |
| `tests/addon/` | Optional offline test suite for the addon's Lua (gopher-lua, own Go module, not needed to build or ship anything) |
| `tools/` | Release packaging and the icon generator (see `tools/README.md`) |

## Installing the addon

Copy `addon/FairShare` to `World of Warcraft\_classic_beta_\Interface\AddOns\FairShare\` (beta) or the equivalent live path once Forever ships. One folder, nothing else to install.

## Releases

Three release streams, all from tags: the **addon** from `v*` tags, packaged from `addon/FairShare` alone and uploaded to CurseForge and a GitHub release (`.github/workflows/addon-release.yml`); the **sync client** from `client-v*` tags; the **backend** from every push to `main` as the rolling `backend-latest` release, or from `backend-v*` tags for a pinned one (`backend/README.md` describes running and deploying it).

### Sync client

Built by CI from a `client-v*` tag: the release holds `fairshare-sync.exe`, `SHA256SUMS`, `LICENSE` and `THIRD_PARTY_NOTICES.md`. The exe is not code-signed, so Windows will warn about an unknown publisher; check the download against `SHA256SUMS` before running it (`certutil -hashfile fairshare-sync.exe SHA256` on Windows).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The short version: never commit tokens, Discord IDs, player names, Battle.net tags, or SavedVariables, and the addon contains no third-party code.

## License

MIT, see [LICENSE](LICENSE). Third-party modules used by the Go parts are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The addon has no third-party code. World of Warcraft and its assets belong to Blizzard Entertainment; this project is not affiliated with Blizzard.
