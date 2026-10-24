# ghostreel

An open-source StarCraft II replay viewer. It plays a `.SC2Replay` back on the map's terrain without running the game: buildings, units, deaths, supply and income, scrubbable to any moment.

ghostreel is a working name. The project is not affiliated with or endorsed by Blizzard Entertainment.

> **What you're seeing.** A replay stores player inputs, not unit positions, so a viewer has to reconstruct movement. This release uses the *tracker tier*: when units are born and die, what buildings exist and where, and player stats are all exact. Unit **positions** are exact only when the replay sampled them (every ~11 s, and only for units in fights) and at death. In between, units hold their last known position and travel to the next one at their speed. Workers and units walking across the map are mostly not sampled. The inspect panel shows when a unit's position was last actually known. More accurate tiers are planned (see [Roadmap](#roadmap)).

## Running it

### Desktop app (Windows, macOS, Linux)

```bash
cd desktop
wails build
```

The app is `desktop/build/bin/ghostreel-desktop(.exe)`. Open a replay with **Open replay…**, drag one onto the window, or pick one from your SC2 replay folders in the **Library** menu. You can also pass a replay on the command line, so "Open with" works.

### In a browser

```bash
cd viewer && npm ci && npm run build && cd ..
go run ./cmd/ghostreel serve --replays "/path/to/your/Replays/Multiplayer"
```

Then open <http://127.0.0.1:8080/>. The server only listens on localhost by default.

### Command line

```bash
go run ./cmd/ghostreel info my.SC2Replay                     # summary; --counts, --positions for detail
go run ./cmd/ghostreel dump-timeline my.SC2Replay -o my.pb   # build a timeline (--json, --no-commands)
go run ./cmd/ghostreel dump-map my.SC2Replay                 # find and decode the replay's map (+ hillshade PNG)
```

## Maps

ghostreel looks for a replay's map in this order:

1. A map file you pick (**Pick map file…** in the viewer, `--map` on the command line).
2. The Battle.net cache, where the game keeps every map you've played.
3. ghostreel's own cache of maps it downloaded.
4. For local and bot games, which record only a file name: your SC2 `Maps` folder and any folders in `--maps` / `GHOSTREEL_MAPS`.

If none of these has it, the replay still plays on a blank grid. For ladder and custom maps, **Download from Blizzard** fetches the map from Blizzard's public map server; the command-line equivalent is `--network`. The file is checked against the hash recorded in the replay. Nothing is downloaded unless you ask.

## Building from source

Requirements:
- Go 1.22 or newer
- Node 20 or newer
- [`buf`](https://buf.build): `go install github.com/bufbuild/buf/cmd/buf@latest`
- `protoc-gen-go`: `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`
- For the desktop app, the [Wails v2 CLI](https://wails.io) and, on Windows, the WebView2 runtime (preinstalled on Windows 11)

| Task | Command |
| --- | --- |
| Build the CLI | `make build` (or `go build ./cmd/ghostreel`) |
| Go tests | `make test` (`-short` skips the slow full-corpus seek check) |
| Viewer tests (checks it against the Go code) | `cd viewer && npm test` |
| Regenerate code from `proto/` | `make proto` |
| Viewer dev server with hot reload | `ghostreel serve` in one terminal, `cd viewer && npm run dev` in another |
| Seek benchmark | `make bench` |
| Desktop app | `make desktop` |

Tests that need real replays and maps look in `testdata/replays/` and `testdata/maps/`, and skip when those folders are empty.

**Layout:**

| Path | Contents |
| --- | --- |
| `cmd/ghostreel` | The CLI |
| `internal/replay` | Reads replays with [s2prot](https://github.com/icza/s2prot) and turns them into simple events |
| `internal/timeline` | Builds the playback timeline and seeks in it |
| `internal/mapfile` | Decodes map files |
| `internal/mapcache` | Finds a replay's map |
| `internal/gamedata` | Unit sizes and speeds (a small built-in table for now) |
| `internal/app`, `internal/server` | The layer and HTTP API that both frontends use |
| `proto/` | The timeline format ([docs](docs/TIMELINE_FORMAT.md)) |
| `viewer/` | The TypeScript + three.js viewer |
| `desktop/` | The Wails desktop app, a separate Go module |

Design notes are in [`PLAN.md`](PLAN.md), [`docs/DECISIONS.md`](docs/DECISIONS.md) and [`docs/MAP_FORMAT_NOTES.md`](docs/MAP_FORMAT_NOTES.md).

## Assets and legal

The code is Apache-2.0. StarCraft II's art, data and maps belong to Blizzard and are **never** part of this repository:

- Maps are read from your own game install or cache, or downloaded from Blizzard's map server at your request. They are stored only in your local cache.
- The viewer draws units as plain shapes with type names. Unit models and icons will come from your own install, extracted locally, in a later release.
- Replays and maps used by tests stay in the git-ignored `testdata/` folder.
- Screenshots are not committed if they show map art. To make your own, open one of your replays and switch the terrain to "Shaded terrain", which is computed from height data and contains no Blizzard art.

## Roadmap

- **Phase 2, accuracy:** an exporter that records exact unit positions by running the matching game build (a ground truth for measuring error). Then a *command tier* that follows players' orders over the map's pathing grid, including workers mining.
- **Phase 3, 3D:** a local asset extractor for unit models, terrain textures and animations, and a 3D view with fog of war.
- **Phase 4:** a browser-only build (WebAssembly), installers and plugin hooks.
