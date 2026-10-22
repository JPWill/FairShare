# tests/addon — offline addon test suite

A Go test harness that runs the real addon without the game. gopher-lua (a Lua 5.1 interpreter written in Go) loads the files listed in `addon/FairShare/FairShare.toc` with stubs for the WoW API in `stubs/wow.lua`, then drives them: scripted auction logs from `spec/`, virtual raiders answering real messages, and cross-language checks that the Lua SHA-256 and canonical serializer produce byte-identical output to the backend's Go code (the golden snapshot checksum is asserted on both sides).

```
cd tests/addon
go test ./...
```

It is its own Go module, so the gopher-lua dependency never touches the root module, the backend, the client or the addon, and nothing here is needed to build or ship anything.

What it covers: comm chunking and throttling, the commit-reveal state machine and every flag it raises, snapshot import and broadcast, transcripts, the test auction, kill and loot recording, running credit across concurrent auctions, leader loss and takeover, the dev simulator, and the UI's no-clipped-text rule (`TestNoUnboundedText` fails on any long label without a right bound).

Limits: gopher-lua is several times slower than the game's Lua and differs in a few `string.format` and number-formatting corners, so never use it for timing; benchmark SHA-256 in game with `debugprofilestop()`. Scripts attached to frames do not run; the harness calls handlers directly.

Adding a scenario: drop a transcript-style log in `spec/` and assert on the resulting auction state the way the existing specs do, or write a Go test that evaluates Lua through the harness helpers in `harness_test.go`.
