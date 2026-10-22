# Contributing

Thanks for looking. FairShare is a small project run by one person for one raid
community, so expect slow replies and opinionated answers. Bug reports with a transcript
line or a screenshot are the most useful thing you can send.

## Ground rules

- **No secrets or player data, ever.** Not in code, fixtures, docs, screenshots or issue
  text: no bot tokens, Discord server/channel/user IDs, real character names, Battle.net
  tags, ledgers or SavedVariables dumps. Test fixtures use obviously fake names. A pull
  request that includes any of these will be closed and the history rewritten.
- **The addon has no third-party code.** Nothing from Ace3, LibStub, LibDeflate or any
  other addon library, not even a copied helper. Everything under `addon/FairShare/Lib/`
  is written for this repo. The Go side may use well-known modules; add them to
  `THIRD_PARTY_NOTICES.md`.
- **GUI only.** The addon has no slash commands and the bot uses buttons, menus and
  modals; slash commands exist only as the three entry points Discord requires.
- **No clipped text.** Every label is bounded on the right and either wraps or shows
  the full text on hover. `TestNoUnboundedText` enforces it offline; Dev → Audit text
  checks it in game.
- **Security findings**: if you find a way to game the ledger or the bot, open an issue
  marked security rather than a pull request, so it can be fixed before it is described
  in public.

## Before you open a pull request

```
go build ./...          # backend and client, from the repo root
go test ./...
cd tests/addon          # the addon's offline suite (gopher-lua)
go test ./...
```

One command per line so it works in Windows PowerShell as well as bash.

Both must pass. Keep LF line endings, 4-space indent in Lua, tabs in Go
(`.editorconfig` does this for most editors). One change per pull request, with the
"why" in the description. If a change alters a wire message, a rule or a default, update
`docs/protocol.md` in the same pull request, and say in the description what it
replaced and why.

## Licence

By contributing you agree that your contribution is licensed under the MIT licence in
`LICENSE`, the same as the rest of the project.
