# Lib/

In-house replacements for the third-party libraries the original brief listed. Clean-room code only; nothing copied from Ace3, LibStub, LibDeflate, or any other addon library.

| File | Replaces | Notes |
|---|---|---|
| `Bit.lua` | — | Thin wrapper over the game's `bit.*` so the test harness can swap in a pure-Lua fallback. |
| `Sha256.lua` | a vendored SHA-256 | Pure Lua 5.1. Target: a 100-byte message hashes well under 50 ms; benchmark it. Output must match Go's `crypto/sha256` byte for byte (verified in `tests/addon`). |
| `Base64.lua` | — | Standard alphabet, padded. Used for snapshot/transcript strings. |
| `Serialize.lua` | AceSerializer / LibSerialize | Deterministic encoding of nested tables with strings, numbers, booleans. Deterministic key order so the checksum is stable across Lua and Go. |

Comm (throttling, chunking, lockdown retry), events, timers, and saved variables live in `Core/` because they depend on the addon namespace.
