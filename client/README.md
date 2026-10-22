# client/ — fairshare-sync

Desktop sync executable for raid leaders: pulls the snapshot into `Data.lua`, sends live updates during the raid, and finishes the raid. Go, standard library only. Part of the root module `github.com/JPWill/FairShare`.

## Using it

1. Download `fairshare-sync.exe` from the newest `client-v*` release and check it against `SHA256SUMS` (`certutil -hashfile fairshare-sync.exe SHA256`). It is not code-signed, so Windows warns about an unknown publisher.
2. In Discord, `/fairshare` → **Sync client token**. The token is shown once; pressing the button again replaces it.
3. Run the exe. On Windows it opens a small window; first run asks for the backend address (`https://fairsharewow.com`), the token, and your World of Warcraft folder (it guesses). Settings are kept in `%APPDATA%\FairShare\config.json`, readable only by you, with the log in `sync.log` beside it.
4. Before a raid, press **Sync now** (or just leave it running): it writes `Interface\AddOns\FairShare\Data.lua` with the current snapshot. The addon reads that file only when the UI loads, so `/reload` in game after a sync; the window and the addon both say when one is needed.
5. During the raid every `/reload` writes SavedVariables, and the client uploads each as a live update so Discord shows the raid in progress. Nothing settles until you press **Finish raid**, here or on the bot's personal panel.
6. **Upload as witness** sends any transcript you hold for a raid you attended but did not lead.

Console use: `-setup` asks the questions again, `-once` syncs and exits, `-finish` finishes the current raid, `-cli` runs the watch loop without the window.

Security: the token is hashed at rest on the backend and sent as a bearer token over HTTPS only; it lets the client act as you (fetch snapshots for raids you lead, send live updates and finish those raids, upload transcripts of raids you attended) and nothing more. If it leaks, issue a new one and the old one stops working.

## Layout

```
cmd/fairshare-sync/main.go     flags; the window by default on Windows, else watch / once / finish
internal/app/                  the window's controller: one goroutine runs every sync step, status + log for display
internal/gui/                  the Win32 window and settings window (Windows only; icon.png is the FairShare wheel)
internal/config/               config.json, uploads.json, WoW folder detection, interactive setup
internal/api/                  backend HTTP API (protocol §7): snapshot, live update, witness upload, finish
internal/datafile/             Data.lua rendering and atomic write (protocol §4)
internal/luatable/             tolerant parser for SavedVariables Lua table literals
internal/transcript/           canonical transcript text from SavedVariables (protocol §5/§6)
internal/sync/                 pull / live update / finish loop
```

Tests: `go test ./client/...` — byte-exact golden for Data.lua, a realistic SavedVariables fixture for the parser, transcript rebuilding, the API calls against a fake backend.

## Notes for the addon side

The transcript header needs a snapshot checksum and addon version. The client reads `FairShareDB.transcript[raidID].snapshot` and `.version` when present, and falls back to `FairShareDB.snapshot.checksum` only when `FairShareDB.snapshot.raidID` matches. The addon should record both fields in each transcript entry when the raid starts so the header is right even after a later import.
