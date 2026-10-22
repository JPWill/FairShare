# tests/

Optional test suites. Nothing here is required to build or run FairShare, and nothing here ships.

- `addon/` — offline test suite for the addon's Lua code. Its own Go module so its one dependency (gopher-lua, a Lua 5.1 interpreter written in Go) never touches the root module, the backend, the client, or the addon.

Run from the repo root:

```
cd tests/addon && go test ./...
```
