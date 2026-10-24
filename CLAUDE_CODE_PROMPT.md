# Claude Code build prompt — SC2 Replay Viewer

Paste everything below the line into Claude Code, run from the project folder that contains `PLAN.md`. That folder becomes the repository root.

**Before you run it:**

- Install Go (current stable, ≥ 1.22), Node (LTS), `buf`, the Wails CLI, and on Windows the WebView2 runtime.
- Put a replay corpus in `testdata/replays/`: several patches from 2.0.8 on, at least one full pro 1v1, plus one pre-2.0.8 replay if you have it.
- Put the matching maps in `testdata/maps/`, or make sure they are in your Battle.net cache.

---

You are building an open-source StarCraft II replay viewer. The full design is in `PLAN.md` in this directory — read it completely before writing any code, and treat it as the source of truth for architecture, data sources, fidelity tiers, and phase gates. Facts marked "(verify)" in the plan were written from memory: confirm each against the real library or a real replay before relying on it. If you find something in the plan that is wrong (a file name inside an MPQ, an s2prot API detail, a coordinate convention), verify it, fix your approach, and note the correction in `docs/DECISIONS.md`.

## Your scope for this session: Phase 0, then Phase 1

Build Phase 0 (foundations) and Phase 1 (2D viewer on the tracker tier). Do **not** start the command tier, engine tier, CASC extractor or 3D work — leave clean interfaces for them instead.

**Stop after the Phase 0 gate** (end of step 7 below): report what works, what you verified or corrected from the plan, and what's open, then wait for my go-ahead before starting the viewer.

## Hard constraints

- **Never commit or ship Blizzard assets** (models, textures, icons, maps, replays you didn't create). Add `testdata/replays/`, `testdata/maps/` and any asset cache dirs to `.gitignore`. Tests that need real replays must skip cleanly when the files are absent.
- Code license: **Apache-2.0** (matches s2prot). Add `LICENSE` and SPDX headers.
- Do not put "StarCraft" in the project/binary name. Use the working name `ghostreel` (rename-able later); module path `github.com/OWNER/ghostreel` with `OWNER` as a placeholder.
- Go: current stable (≥ 1.22). Frontend: TypeScript + Vite + three.js (orthographic camera for 2D). Desktop shell: Wails (use v2 unless v3 is stable by now; record the choice in `DECISIONS.md`). Timeline schema: Protobuf, with `buf` generating Go and TypeScript via `@bufbuild/protobuf` (protobuf-es).
- Keep the Go core free of UI and native (cgo) dependencies so it can later compile to WASM.
- Timelines need tracker events, so the minimum supported build for `dump-timeline` is 2.0.8. Older replays must get a clear "unsupported for playback" message, never a crash.
- Every user-facing time (CLI output, scrubber, event feed) uses the in-game clock: loops ÷ 22.4.

## Repository layout

The repository root is this folder (no nested `ghostreel/` directory).

```
cmd/ghostreel/          # CLI: info, dump-timeline, dump-map, serve
internal/replay/        # wraps s2prot/rep; tag decoding; loop/time helpers; normalizes events
internal/mapfile/       # SC2Map/.s2ma parsing: MapInfo, t3HeightMap, t3CellFlags/pathing, Objects, Minimap.tga
internal/mapcache/      # resolve map from replay cache handle: local Battle.net cache -> depot download -> manual path
internal/timeline/      # builder (tracker tier), snapshots, seek, (de)serialization
internal/gamedata/      # per-build unit metadata (radius, footprint, is_building, can_move); stub table + loader interface
proto/timeline/v1/      # timeline.proto
viewer/                 # Vite + TS + three.js frontend
desktop/                # Wails app wiring the Go core to the viewer
docs/                   # DECISIONS.md, TIMELINE_FORMAT.md, MAP_FORMAT_NOTES.md
testdata/               # gitignored real files + small checked-in synthetic fixtures
```

## Work in this order, verifying each step before moving on

### Phase 0

1. **Scaffold**: `git init`, `go mod init`, add `github.com/icza/s2prot` and `github.com/icza/mpq`, Makefile targets (`build`, `test`, `lint`, `proto`, `viewer`, `desktop`), GitHub Actions running `go vet`, `go test ./...`, and the viewer typecheck/build.
2. **Replay layer** (`internal/replay`). `ghostreel info <file.SC2Replay>` prints build, duration (in-game clock), map title, players (name, race, color, result), and event counts; it works on every build, including pre-2.0.8. Implement unit-tag decoding (index + recycle) shared by tracker and game events, and loop↔seconds helpers: 16 loops per game second internally, 22.4 per displayed second. Convert s2prot events into a small normalized event type of your own, so the builder never depends on s2prot types. Unit-test tag decoding and time math.
3. **Game data stub** (`internal/gamedata`): a small checked-in table (unit name → radius, footprint, is_building, can_move, display color class) covering the common ladder units, behind an interface that a future CASC extractor can replace. Mark movable buildings: Terran buildings that lift off and spine/spore crawlers. Unknown units get defaults and render as a circle, never crash.
4. **Timeline schema** (`proto/timeline/v1/timeline.proto`) with `Meta`, `Unit`, `Keyframe` (loop, x, y, facing, state, source enum: TRACKER / COMMAND / ENGINE), `Track`, `Snapshot`, `Event` (oneof: unit_born, unit_died, morph, upgrade, player_stats, chat, command). Include a schema version field. Document it in `docs/TIMELINE_FORMAT.md`, including that facing is derived from movement direction, since no stream records it.
5. **Tracker-tier builder** (`internal/timeline`), taking the normalized events from step 2:
   - Units come from born/init events. Buildings stay fixed, except movable ones, which take their new position from the land/root command target in game events or from the next position sample.
   - Keyframes come from position samples, plus morphs and deaths.
   - Fill gaps with plain linear interpolation, no pathing clamp. Workers with no samples sit in a fixed spot near their nearest base.
   - Snapshots hold the alive set and aggregates every 160 loops; positions come from a binary search on each track. `Seek(loop)` starts from the nearest snapshot.
   - `ghostreel dump-timeline <replay> -o out.pb` (plus `--json` for debugging).
   - First, measure and record in `DECISIONS.md` how sparse position samples really are on a real replay: which units get them, how often, and at what resolution.
   - Tests use checked-in synthetic event fixtures: seeking to any loop yields identical state to replaying from zero. Add a Go benchmark for seek time on a real full game.
6. **Map parsing** (`internal/mapfile`): open the map MPQ by known file names (locked ladder maps have no file list) and decode MapInfo (dimensions, playable bounds), height map, pathing/cell flags, and the `Objects` file (minerals, geysers, destructibles). Check early that `icza/mpq` handles the compression used by current ladder maps. These formats are not formally documented — study `icza/scelight` and `sc2reader` for reference, write what you learn in `docs/MAP_FORMAT_NOTES.md`, and start with whatever subset gives correct terrain + resources. Cross-check resource positions against neutral units born at loop 0 in the tracker events. `ghostreel dump-map <map> -o map.json` plus a hillshade PNG for eyeballing.
7. **Map resolution** (`internal/mapcache`): read the cache handles from the replay's details/init data and pick the map's handle out of the dependency entries. Look for the file in the platform's Battle.net cache, which is split into hash-prefix subfolders (`Cache/ab/cd/<hash>.s2ma`; Windows/macOS paths; Linux/Wine configurable). Otherwise download it from the regional depot and cache it, otherwise accept `--map <path>`. Network access must be optional and clearly logged.

**Phase 0 gate — stop here and report** (see "Phase gates" below).

### Phase 1

8. **Viewer** (`viewer/`): load a timeline + map JSON/binary, draw hillshaded terrain and the minimap layer, resources, buildings as footprint rectangles, units as radius circles in player color with a type label. Add play/pause, a scrubber, speed 0.5×–16×, player panels (supply, minerals/gas income, workers, army value from player_stats), an event feed, and click-to-inspect. If no map is available, draw units on a blank grid sized from the replay. Show which fidelity tier produced the data, and a toggleable FPS readout. Target 60 fps for a full 1v1 on mid-range hardware; use instanced meshes.
9. **Dev server**: `ghostreel serve` serves the built viewer plus a small HTTP API (load a replay, get its timeline and map) so the viewer can be developed and tested in a browser without Wails.
10. **Desktop app** (`desktop/`): Wails app with an open-file dialog and drag-and-drop for `.SC2Replay`. It calls the Go core and caches timelines by replay hash under `os.UserCacheDir()/ghostreel`. If the map can't be found, it explains why, offers a "pick map file" button, and meanwhile plays on the blank grid.
11. **Docs**: README with build/run instructions, the asset/legal policy, the tracker tier's motion limits, and screenshots (generated from the user's own replays, not committed if they contain Blizzard art).

## Phase gates (check these before declaring done)

- **Phase 0:** `ghostreel info` succeeds on every replay in `testdata/replays/`. `dump-timeline` succeeds on every replay from 2.0.8 on and refuses older ones cleanly. If the corpus is missing or thin, say so and test with what's available.
- **Phase 1:**
  - The seek benchmark shows any seek on a full pro 1v1 in under one second.
  - The seek-vs-replay test passes.
  - The viewer's FPS readout holds 60 fps on that game in `serve` mode.
  - Give me a short manual checklist to run in the desktop app (open, drag-and-drop, scrub, missing-map fallback). You can't watch the app yourself.

## Working style

- Make small, reviewable commits with clear messages.
- When a format detail is uncertain, write a tiny probe (a test or a `cmd/` debug flag) against a real file rather than guessing.
- Keep `docs/DECISIONS.md` updated with each non-obvious choice and any deviation from `PLAN.md`.
- At the end, summarize what works, what's stubbed, known gaps, and the concrete next steps for Phase 2 (engine-tier exporter first, then command tier).
