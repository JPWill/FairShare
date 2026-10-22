# Third-party notices

FairShare itself is MIT licensed (see `LICENSE`). The Go backend, Discord bot and sync
client are built with the open-source modules below; their licences require that these
notices travel with any binary built from them, so this file ships with every release.

The in-game addon (`addon/FairShare/`) contains no third-party code. Every library in it
(`Lib/`) was written for this repo.

World of Warcraft, its item data, textures and the Blizzard UI templates the addon calls at
runtime belong to Blizzard Entertainment and are not part of this project.

## Go modules used by the backend, bot and sync client

| Module | Licence | Used for |
|---|---|---|
| github.com/bwmarrin/discordgo | BSD 3-Clause | Discord API (bot) |
| modernc.org/sqlite (and its modernc.org/libc, mathutil, memory dependencies) | BSD 3-Clause | SQLite without a C compiler (backend) |
| github.com/gorilla/websocket | BSD 2-Clause | Discord gateway, via discordgo |
| github.com/google/uuid | BSD 3-Clause | via modernc.org/sqlite |
| github.com/dustin/go-humanize | MIT | via modernc.org/sqlite |
| github.com/mattn/go-isatty | MIT | via modernc.org/sqlite |
| github.com/ncruces/go-strftime | MIT | via modernc.org/sqlite |
| github.com/remyoudompheng/bigfft | BSD 3-Clause | via modernc.org/mathutil |
| golang.org/x/crypto, golang.org/x/sys | BSD 3-Clause | via discordgo and modernc.org/sqlite |

The offline addon test suite (`tests/addon/`, never shipped) also uses
github.com/yuin/gopher-lua (MIT).

The full licence text of each module is in its source tree, which `go mod download`
fetches into the module cache; `go.sum` pins the exact versions.
