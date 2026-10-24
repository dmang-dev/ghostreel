# ghostreel

An open-source StarCraft II replay viewer. It plays a `.SC2Replay` back on the map's terrain without running the game: buildings, units, deaths, supply and income, scrubbable to any moment.

ghostreel is a working name. The project is not affiliated with or endorsed by Blizzard Entertainment.

> **What you're seeing.** A replay stores player inputs, not unit positions, so a viewer has to reconstruct movement. Births, deaths, buildings and player stats are always exact. How positions are filled in depends on the *tier* (the badge at the top of the viewer says which one you're seeing):
>
> - **Command tier** (the default): units follow the players' recorded orders along the map's paths, workers mine, new units walk to their rally point, and the estimate snaps to the real position whenever the replay sampled one. Measured against the real game, the commanded players' moving units are off by a median of 2–3.5 cells (mean 3–8). Players with no recorded orders (the built-in AI) fall back to the tracker tier.
> - **Tracker tier:** positions are known only when the replay sampled them (every ~11 s, mostly in fights) and at death; in between, units hold their last known position. Median error on moving units is 5–22 cells.
> - **Engine tier:** exact positions, recorded by playing the replay in the real game with `ghostreel-engine` (see below).
>
> The inspect panel shows when a unit's position was last actually observed.

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
go run ./cmd/ghostreel dump-timeline my.SC2Replay -o my.pb   # build a timeline (--tier tracker|command, --json)
go run ./cmd/ghostreel dump-map my.SC2Replay                 # find and decode the replay's map (+ hillshade PNG)
go run ./cmd/ghostreel accuracy my.SC2Replay                 # compare the tiers with an engine recording
```

### Exact positions with `ghostreel-engine`

Where a StarCraft II client of the replay's exact game version is installed, `ghostreel-engine` plays the replay back through Blizzard's [s2client API](https://github.com/Blizzard/s2client-proto) and records every unit's position every 16 loops:

```bash
go build -o ghostreel-engine ./cmd/ghostreel-engine
./ghostreel-engine --sc2 ~/StarCraftII my.SC2Replay          # writes my.engine.pb.gz
```

Keep `my.engine.pb.gz` next to `my.SC2Replay`, and the viewer and `dump-timeline` use it automatically (engine tier). On a shared machine, run the exporter under `nice` and pass `--max-load` so it pauses while the host is busy. The client of the exact build is needed; Blizzard's Linux package is 4.10 (base build 75689).

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
| `internal/selection`, `internal/pathing`, `internal/command` | The command tier: selections, paths, following orders |
| `internal/engine`, `cmd/ghostreel-engine` | The engine-tier exporter (s2client API) |
| `internal/accuracy` | Measures a timeline against engine ground truth |
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

- **Phase 2, accuracy (done):** the `ghostreel-engine` exporter for exact positions and ground truth, and the command tier. Next for the command tier: modelling attack-moves that stop at enemies, speed upgrades (Stimpack, creep), and ground truth for current game versions (needs a Windows or macOS client).
- **Phase 3, 3D:** a local asset extractor for unit models, terrain textures and animations, and a 3D view with fog of war.
- **Phase 4:** a browser-only build (WebAssembly), installers and plugin hooks.
