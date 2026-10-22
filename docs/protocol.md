# FairShare protocol and formats

The contract shared by the addon (Lua), the backend and bot (Go), and the sync client (Go). Every byte format here is canonical: two implementations given the same data must produce identical bytes, because checksums are computed over them. When this file and code disagree, fix the code.

## 1. Names

WoW: Forever is one realm and characters have a first and optional last name (`Thrall Doomhammer`). A character's identity everywhere in FairShare is the **bare in-game name**, exactly as the game displays it: Title Case per word, single spaces, no realm. The addon strips any `-Realm` suffix the client appends to sender names. The backend normalizes typed names the same way (trim, collapse spaces, Title Case per word) and never asks for a realm. Names may contain spaces, never commas, tabs, newlines or hyphens, so every CSV and tab format below stays unambiguous.

## 2. Addon message wire format

Prefix `FAIRSHARE`. Fields joined with `^`. Inside a field, `~` → `~0` and `^` → `~1`. A message body is `TYPE^field1^field2...`. Bodies under 255 bytes are sent as `=` + body. Longer bodies are split: `!<seq>:<i>:<n>^<slice>` and reassembled per sender and seq. (See `Core/Comm.lua`.)

Only the group leader may send `SNAP`, `ROWS`, `OPEN`, `CLOSE`, `RESULT`, `KILL`. Clients ignore those from anyone else and log a `note`.

| Type | Sender | Fields | Meaning |
|---|---|---|---|
| `PING` | anyone | token, version | who is here |
| `PONG` | anyone | token, version | reply |
| `SNAP` | leader | raidID, checksum, issuedAt, offspecPct, floorsCSV, bannedCSV | raid context. floorsCSV = `KEY=value,KEY=value` sorted by key. bannedCSV = names sorted, comma-joined, may be empty |
| `ROWS` | leader | one field per row: `Name,owner,balance,credit` | rows for everyone currently in the group (resent for late joiners with only their row) |
| `OPEN` | leader | auctionID, kind, itemLink, floor, deadline, slotKey | kind = `loot` or `test`. floor = integer effective for main spec, must equal the snapshot floor for `slotKey` (the floor-table key the leader's client used: an equip location or `DEFAULT`); clients refuse to open and the backend flags `floor_mismatch` otherwise. deadline = server time |
| `COMMIT` | bidder | auctionID, hash, tag | hash = sha256 hex of `auctionID|Name|bid|offspec|salt`. tag (optional) = sha256 hex of `addonKey|auctionID|hash`, see §11 |
| `CLOSE` | leader | auctionID, committersCSV | names sorted ascending, comma-joined, may be empty |
| `REVEAL` | bidder | auctionID, bid, offspec, salt, tag | bid: 1-9 digits; offspec `0`/`1`; salt 32 lowercase hex chars. tag (optional) = sha256 hex of `addonKey|auctionID|bid|offspec|salt` |
| `RESULT` | leader | auctionID, winner, price | winner empty string when no valid bid |
| `KILL` | leader | encounterID, encounterName, rosterCSV | sent after every successful `ENCOUNTER_END`; roster = everyone in the group at that moment, sorted |
| `TRANSFER` | leader | auctionID, fromName, toName | the winner hands the item to someone else at the same price, on the books (§3.7) |
| `VOID` | leader | auctionID, reason | strikes a finished loot sale from the books (§3.8); the item is auctioned again under a new auctionID |
| `LEAD` | game group leader | newLeaderName | claims FairShare leadership after the leader is lost (§3.9) |
| `ACK` | any client | newLeaderName, tag | backs a LEAD; sent on RAID so the transcript carries the quorum. tag (optional) = sha256 hex of `addonKey|LEAD|newLeaderName` |
| `ABANDON` | leader | auctionID, reason | strikes an auction that never got its RESULT (§3.10); settles nothing |
| `SYNC` | any client, by whisper to the leader | highestAuctionN, unfinishedAuctionID, phase | answer to SNAP and LEAD; not recorded in transcripts |

auctionID = `<raidID>-<n>`, n counting from 1 per raid on the leader's client. Test auctions use the same numbering.

## 3. Auction rules (identical in Lua and Go)

Inputs: the `SNAP` context, the `ROWS` rows, one `OPEN`, the set of `COMMIT`s received before `CLOSE`, the `CLOSE` list, the `REVEAL`s received before `RESULT`, and the `RESULT`.

1. **Eligibility.** A reveal is valid only if all hold: the sender committed before CLOSE; `sha256(auctionID|sender|bid|offspec|salt)` equals that commit; the sender has a row; the sender is not in the banned list; bid is a non-negative integer; bid ≤ balance + credit; bid ≥ the sender's applicable floor. Otherwise it is recorded with a reason and excluded.
2. **Applicable floor.** Main spec: `floor`. Off-spec: `ceil(floor × (100 − offspecPct) / 100)`.
3. **Winner.** Highest valid bid. Ties: let `T = sha256(concat of the salts of all valid reveals, ordered by bidder name ascending)`. Each tied bidder scores `sha256(T .. "|" .. name)`; the lowest hex string wins.
4. **Price.** If two or more valid bids: `max(secondHighestBid, winnerFloor)`. If one: `winnerFloor`. If none: no sale, winner empty, price 0.
5. **Flags.** `close_mismatch` (my committer list ≠ CLOSE list), `result_mismatch` (my winner/price ≠ RESULT), `late_commit:<name>`, `invalid_reveal:<name>:<reason>`, `noreveal:<name>` (committed, never revealed), `unauthorized:<type>:<name>`.
6. **Test auctions** (`kind=test`) run all of the above and are never settled.
7. **Transfers.** A `TRANSFER` is valid only if: the auction is a finished `loot` auction (`not_done`, `not_loot`); `fromName` is its winner as recomputed by the replay (`not_winner`); `toName` has a row (`unregistered`), is not banned (`banned`), and has balance + credit ≥ the sale price (`over_max`); and no earlier valid transfer exists for the auction (`duplicate`). A valid transfer makes `toName` the effective winner: the `purchase −P` ledger entry goes to `toName`, shares and pool are unchanged. An invalid one is ignored and flagged `bad_transfer:<toName>:<reason>`. Trading a won item outside this mechanism remains a ban offense. They report, per group member: no addon (never PONGed/committed), unregistered (no row), banned, committed but didn't reveal, or OK.

8. **Voids.** A `VOID` is valid only after the auction's RESULT (`void_before_result`) and once per auction (`duplicate_void`). A valid void keeps the auction in the record with its bids and result but produces no ledger entries: no purchase, no shares, no pool. It is a normal leader action, not a dispute, so it carries no flag. The reason is free text (the addon sends `re-run`). Selling a second copy of an item that dropped twice does not void anything: it is simply another auction with its own id.

9. **Leadership.** The first leader-only message fixes the leader. The leader may change only through a `LEAD` from the new leader followed by `ACK`s: when the claimant plus distinct `ACK`s from roster members reach half the roster (the latest `KILL` roster when there is one, else everyone given rows), leader-only messages are accepted from the new leader from that line on. Clients send `ACK` only for whoever the game says leads the group, so the quorum attests the game's own promotion. Leader-only messages from anyone else, including the claimant before the quorum, are flagged `leader_change:<type>:<name>` as before. On every SNAP and LEAD each client whispers `SYNC` to the leader with the highest auction number it knows and any auction still open; the leader numbers on from the maximum, so ids never collide after a crash.

10. **Abandoned auctions.** An auction whose leader was lost before its RESULT is closed by `ABANDON` from the current leader: valid only before any RESULT (`abandon_after_result`), it keeps the auction in the record, produces no ledger entries and carries no flag. Without it an auction with no RESULT is flagged `no_result`. Clients also give up on such an auction by themselves after a grace period, so the queue is never blocked; the ABANDON is what settles it for the record.

11. **Concurrent auctions and the running cap.** Any number of auctions may be open at once; every message names its auction. Eligibility against credit is judged when the RESULT is announced, not when the bid is placed: a reveal is valid only if `bid ≤ balance + credit − spent`, where spent is what the bidder already owes for loot sales whose RESULT came earlier in the transcript (the transferee pays after a TRANSFER; voided and abandoned sales count for nothing). A reveal past that is set aside as `invalid_reveal:<name>:over_credit` (the addon says `over_max`) and the winner and price are computed from the rest, so the leader and every client reach the same answer. The leader therefore announces results one at a time, in order, waiting for each to echo back before computing the next. Clients also refuse to let a raider commit more than their cap across their own open bids, but that is a courtesy, not the rule.

### Settlement (backend only)

For a `loot` auction with winner W, price P, and attendees A = the roster of the `KILL` immediately preceding the OPEN (fallback: the ROWS roster) **plus every standby member of the raid** (signups officers left on standby for the event the raid was opened from), deduplicated, N = |A|:

- `pool = floor(P × poolPct / 100)` with poolPct default 10
- `share = floor((P − pool) / N)`
- `remainder = P − pool − share × N` → added to pool
- Ledger: W `purchase −P`; every a ∈ A `share +share` (W included); the raid's pool account `pool +(pool + remainder)`.

Sum is zero by construction. Shares-per-kill and the full-run bonus are separate ledger reasons (`share`), credited by the backend from `KILL` rosters at settlement using config values.

## 4. Snapshot (canonical text)

Issued by the backend per raid. Lines joined with `\n`, no trailing newline, UTF-8, no CR.

```
FAIRSHARE SNAPSHOT 1
raid=<raidID>
issued=<unix seconds>
offspec=<percent integer>
starts=<unix seconds>        (optional: the scheduled start)
party=1                      (optional: a 5-player party counts as the raid group)
floor:<KEY>=<integer>        (one per line, sorted by KEY byte order)
ban:<Name>                   (one per line, sorted)
roster:<Name>\t<role>\t<status>   (optional, one per line, sorted by Name)
row:<Name>\t<owner>\t<balance>\t<credit>   (one per line, sorted by Name)
```

- `starts` is the event's scheduled start (an ad-hoc raid's is the moment it was opened). The leader's addon uses a snapshot only from 3 hours before `starts` to 12 hours after; outside that window it never broadcasts it, so an old raid cannot start recording in some later group. Absent in snapshots issued before this line existed, when `issued` stands in.
- `party=1` is present when the officers' **5-player groups** switch (config `allowParty`) was on when the raid was opened, and always for sandbox test raids. Without it the raid runs only in a raid group: the leader's addon does not broadcast to a 5-player party, and a party never counts. With it, a party counts and so do dungeon bosses, for testing before raids unlock.
- Kills (`KILL`), loot lines and pending loot are recorded only inside an instance of the raid's kind: a raid instance, or a dungeon when `party=1`. Leaving the group ends the raid on that client (nothing more is recorded; what was recorded stays). Developer testing (a `dev` raid id, or Developer mode while solo) is exempt from all of this.
- `KEY` is the item's `itemEquipLoc` (`INVTYPE_HEAD`, `INVTYPE_WEAPON`, `INVTYPE_2HWEAPON`, …) plus `DEFAULT`, which must be present.
- `owner` is the Discord display name (no tabs, newlines or commas; the backend replaces them with spaces).
- `roster:<Name>\t<role>\t<status>` lines (one per confirmed or standby signup, sorted by Name) are present when officers confirmed a roster for the event this raid was opened from. `role` is one of `tank`, `healer`, `melee`, `ranged`; `status` is `confirmed` or `standby`. They sit between the `ban:` lines and the `row:` lines. The addon uses them for one-click invites and shows who is missing; the backend uses `standby` for share eligibility. Absent when there is no roster.
- `balance` is an integer, may be negative. `credit` is a non-negative integer.
- **checksum** = SHA-256 hex of the exact text. **short** = first 8 hex chars.
- **Paste string** = base64 of the text.
- **Data.lua** (written by the sync client into the addon folder):

```lua
FairShareData = {
    version = 1,
    snapshot = "<text with \n and \" and \\ escaped as Lua string escapes>",
    checksum = "<64 hex>",
    writtenAt = <unix seconds>,
}
```

The addon parses the same text whether it came from Data.lua or a paste, verifies the checksum, and keeps the newest by `issued`.

## 5. Transcript (canonical text)

Written by the addon on export. One per raid per client.

```
FAIRSHARE TRANSCRIPT 1
raid=<raidID>
snapshot=<checksum>
player=<Name>
version=<addon version>
<t>\t<dir>\t<sender>\t<body>      (one per line, in order recorded)
```

- `t` = `GetServerTime()` integer at receipt.
- `dir` ∈ `in` (an addon message received, including our own echoed back), `loot` (sender = looter, body = `LOOT^<itemLink>`), `note` (local observation, sender = player, body free text with no tabs).
- `body` is the decoded message body (`TYPE^fields…`, still escaped per §2).
- **Paste string** = base64 of the text. The sync client reads it from SavedVariables instead (`FairShareDB.transcript[raidID].lines`).

Replay (backend): take only `in` lines, group by auctionID, apply §3 exactly, compare with each `RESULT`. Two transcripts match when their sequences of `in` lines with `TYPE ∈ {SNAP, ROWS, OPEN, COMMIT, CLOSE, REVEAL, RESULT, KILL}` are identical ignoring `t`. An upload is accepted only from the raid's leader or from the owner of a character that has a row in the issued snapshot, and never from a banned person. Settle when `requiredUploads` (default 1) distinct uploaders' transcripts match and no auction carries a *disputing* flag (§9); with 1 required the leader's own upload is the one that settles, with 2 or more one uploader must not be the leader. Uploads are append-only: a later upload never replaces the text a raid was settled on (`settled_upload_id`). A raid that is already settled still accepts uploads: a match is recorded as confirmation, a difference marks the raid `contested` and notifies officers, who can void it (every ledger line of the raid is reversed) and settle again from a chosen transcript. (Sandbox mode: always 1.)

## 6. SavedVariables (addon)

```
FairShareDB.settings           theme, minimap{hide,angle}, sound, minQuality, dev
FairShareDB.snapshot           { text, checksum, issued, raidID, source }   (last good import)
FairShareDB.transcript[raidID] { lines = { "<t>\t<dir>\t<sender>\t<body>", ... }, startedAt, player, snapshot = <checksum>, version = <addon version> }
FairShareDB.auctions[raidID]   { [auctionID] = { id, kind, itemLink, floor, winner, price, bids, invalid, flags, committers } }
```

The sync client reads `transcript[raidID].snapshot` and `.version` for the §5 header.

## 7. Backend HTTP API (used by the sync client)

Base `/api/v1`. Auth: `Authorization: Bearer <token>`; tokens are issued per person from the bot's personal panel and stored hashed.

| Method | Path | Body / result |
|---|---|---|
| GET | `/me` | `{ discordId, displayName }` |
| GET | `/snapshot/current` | the newest unsettled raid led by the caller: `{ raidId, text, checksum, issuedAt }`; 404 if none |
| POST | `/transcripts` | body `{ raidId, text }`: a witness upload; result `{ status: "accepted" \| "settled" \| "disputed", message, uploads, flags }`. With `live: true`: a progress upload from the leader's client mid-raid; validated and replayed the same way, stored as the raid's live view (one per raid, replaced each time), posted to Discord as the live ledger, never settles; result `status: "live"`; 403 unless the caller leads the raid |
| POST | `/raids/{id}/finish` | settles from the raid's live upload through the normal witness rules; result as a witness upload; 403 unless the caller leads the raid, 400 if no live upload exists |
| GET | `/raids/{id}` | status and flags; the settle note only for the raid's leader |

A `409` means a settle lost a race with another upload moments earlier; nothing changed.

`raidId` is accepted as a number or a string and returned as a string. Errors are JSON `{ error: "..." }` with 4xx/5xx. Every authenticated route is rate-limited per token (60 requests a minute, `429` with `Retry-After`), before the token is checked, so guessed tokens are throttled too.

## 8. Config (backend, all overridable in guild_config)

| Key | Default |
|---|---|
| poolPct | 10 |
| newcomerCredit | 100 |
| creditPerRaid | 100 |
| creditCap | 500 |
| sharePerKill | 1 |
| fullRunBonus | 2 |
| norevealPenalty | 25 |
| freePasses | 1 |
| minAccountAgeDays | 30 |
| offspecPct | 0 |
| floor DEFAULT | 10 |
| floor INVTYPE_2HWEAPON | 20 |
| discordInvite | (empty: no link shown; Setup server fills it with a permanent invite) |
| websiteURL | https://fairsharewow.com |

## 9. Backend replay details (decided during implementation)

- The sender of the first `SNAP` whose checksum equals the issued snapshot is the leader, and must be a character registered to the person who opened the raid in the bot (else `leader_not_raid_leader`, disputing); leader-only messages from anyone else are dropped and flagged `leader_change:<TYPE>:<name>`. A transcript with no such SNAP, or an empty `snapshot=` header, is disputing (`no_snap`, `no_snapshot_header`).
- `ROWS` values are verified against the issued snapshot; a mismatch flags `row_mismatch:<name>` and that row is ignored. Owner display names have commas, tabs and newlines replaced by spaces when the snapshot is built, so the `Name,owner,balance,credit` row format is unambiguous.
- Flags come in two classes. **Disputing** flags mean the record itself cannot be trusted and block automatic settlement: `close_mismatch`, `result_mismatch`, `leader_not_raid_leader`, `leader_change`, `no_snap`, `no_snapshot_header`, `snap_*_mismatch`, `row_mismatch`, `duplicate_open|commit|close|reveal|result`, `hash_mismatch`, `floor_mismatch`, `bad_floor`, `bad_auction_id`, `abandon_after_reveal`, `short_deadline`, `early_close`, `early_result`, `test_real_item`, `bad_tag`. **Informational** flags record one person's misconduct or bad luck and are penalised or set aside without stopping the night: `over_credit`, `below_floor`, `noreveal`, `unregistered`, `banned`, `late_commit`, `late_reveal`, `early_reveal`, `bad_bid`, `bad_salt`, `kill_unknown`, `kill_silent`, `untagged`, `unauthorized_snap`. An officer can still settle a disputed raid with a logged reason.
- Attendance is the `KILL` roster intersected with the snapshot rows (`KILL` lines are deduplicated by encounter id); the running cap and every share split are per row owner, so one person's characters share one cap and one share. `noreveal` applies only to auctions that were closed and not abandoned or voided, and the first one per person per raid consumes their free pass instead of the penalty.
- `TRANSFER` checks `over_max` against the recomputed price and `not_winner` against the recomputed winner, never the announced RESULT.
- The pool account id is `pool:<raidID>`. Kill shares and the full-run bonus credit `lifetime_shares` and attendance, never balance (balance moves only through auctions, penalties and adjustments).
- Signups use the person's most recently registered character. Unregistered names in a KILL roster earn nothing.
- Discord entry points: `/fairshare`, `/fairshare-admin`, `/fairshare-upload`.

## 10. Discord signups, roster, events, onboarding

- **Registration** never asks the user to type a class or role: character name is a modal text field (the only free text); class is a select menu of the nine classes; main role(s) a select menu limited to what that class can play (Warrior: tank, melee. Paladin: tank, healer, melee. Hunter: ranged. Rogue: melee. Priest: healer, ranged. Shaman: healer, melee, ranged. Mage: ranged. Warlock: ranged. Druid: tank, healer, melee, ranged). Stored on `character` (class, roles).
- **Onboarding**: when someone joins the server the bot DMs a welcome message with a Register button that runs the same flow, then links Battle.net (optional) and grants Member. If the DM cannot be delivered, the persistent Register message in `#welcome` (created by Setup server, readable by everyone) is the fallback.
- **Events** are created with button grids, not typed times: a day grid labelled "Monday - 19 Oct" with paging and a Repeat cycle (`none`, `weekly`, `biweekly`), then a grid of half-hour start times in the guild's configured timezone (`guild_config.timezone`), then a modal for title, instance, caps, notes. Discord `<t:unix:F>` timestamps render in each viewer's local time. A repeating event spawns its next occurrence when it opens or passes.
- **Signups**: Sign up, pick one of your registered characters, pick a role valid for its class. The embed groups signups by role with class shown, counts per role, and a Standby section. Withdraw removes. Officers press Confirm roster, multi-select the confirmed players (the rest become standby), and the bot posts and pins a Roster embed and locks it. Opening the raid from that event writes `roster:` lines into the snapshot (section 4) and marks standby attendance.

## 11. Trust and verification (what each layer may assume)

The code is public, so nothing a client sends is trusted for being well-formed; every check below exists because the opposite message can be sent by a modified addon.

- **Sender identity** comes from the game server stamp, never from the body. Leader-only messages are accepted only from the FairShare leader, and `LEAD` only from the game's group leader while the recorded leader is absent (not in the group, or silent for 90 s). Messages on the wrong channel (a whisper or party message carrying a raid-wide type) are dropped and noted `off_channel`, so a bid cannot be shown to the leader alone.
- **Bids** are bound to the bidder, the auction and the off-spec flag by the commit hash, and the first commit from a name wins (`duplicate_commit`). A bidder's optional **addon key** (32 hex chars issued by the bot's personal panel, pasted into Settings) signs each `COMMIT`, `REVEAL` and `ACK` with a tag only the bot can recompute, so a leader cannot fabricate bids, reveals, non-reveal penalties or takeover votes for someone else. A tagged line that fails verification is dropped and disputing (`bad_tag`); a line from a person without a key is accepted and noted `untagged`; `requireAddonKey` (config, default off) makes untagged lines disputing once the community has keys.
- **Floors** are the snapshot's, not the leader's: `OPEN` names the floor-table key and every client and the backend compare.
- **Timing** is on the server clock: a short deadline, an early `CLOSE` or an early `RESULT` with unrevealed commits are disputing, so the leader cannot shut a bidder out and have the record blame the bidder.
- **Uploads** are accepted only from the leader or an attendee with a snapshot row, replayed against the stored snapshot, never overwrite earlier uploads, and only the leader's upload (or Finish) settles when one upload is required. Anyone else's upload is kept as a witness and a disagreement marks the raid contested.
- **Dev raids** (`dev` ids) self-whisper only; the simulator cannot inject into a real raid and the sync client never uploads them.

