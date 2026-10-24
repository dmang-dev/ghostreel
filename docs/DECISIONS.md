# Decisions and corrections

Non-obvious choices, and every place the implementation deviates from or corrects `PLAN.md`. Newest last.

## Phase 0 (2026-10-23)

### Facts checked against real files

The plan marked several facts "(verify)". Each was checked against the corpus in `testdata/` (replays from base builds 15405, 75689, 86383, 95299 and 96883; eight maps, four of them depot `.s2ma` files).

| Plan said | Found | Evidence |
| --- | --- | --- |
| Position samples cover only units that recently dealt or took damage | **Confirmed.** Samples arrive every 240 loops (~10.7 s on the in-game clock), and only when there is something to report. Quiet stretches have no event at all (gaps up to 5040 loops). Buildings are never sampled. Workers rarely are (SCV 2/47, Probe 2/65); fighting army units usually are (Ghost 87/93, Hydralisk 45/50). One pro TvT had 7 samples in the whole game. | `ghostreel info --positions` |
| Position samples are coarse | They are whole cells, the same resolution as born/died events. They are not quantized to 4 cells. | Samples at (125,22) next to a start location at (120,24) on a 152-wide map |
| Neutral units are born at loop 0 | **Confirmed.** Minerals, geysers and rocks appear as `UnitBorn` with `controlPlayerId 0`. They match the map's `Objects` file one for one. | `TestResourcesMatchReplay` |
| Depot server is `http://<region>.depot.battle.net:1119/` | **Corrected.** s2prot (and the live depot) use `https://<region>-s2-depot.classic.blizzard.com/`. Four maps were downloaded from the EU host over HTTPS. | `mapcache` against the live depot |
| Battle.net cache is split by hash prefix | Implemented as `Cache/ab/cd/<hash>.s2ma`. The layout is still unverified, since no machine here has a populated cache. | — |
| The map is one of the cache handles | It is the **last** `s2ma` handle that isn't standard data. Other non-standard handles (dependency mods) come before it. | Five ESL replays |
| — | Games started from a local map file (API/bot games, editor tests) have **no cache handles**, only a file name (`AbyssalReefAIE.SC2Map`). The resolver searches map folders by name for these. | All bot replays |
| Tags pack as index << 18 \| recycle | **Confirmed.** Game-event tag 50069505 is tracker unit 191/1. | `TestTagRoundTrip` |
| Command targets are fixed point | ÷4096 (12 fractional bits). Camera targets are ÷256. Player-stats supply values are ×4096. | Probe output |
| Locked ladder maps have no file list | Every map tested (including depot downloads) opened by known file name with `icza/mpq`. No compression problems were found. | `TestRealMaps` |

### Choices

- **Timeline-capable builds start at 2.0.8 (base build 25604).** `info` works on everything (tested on a 1.0.2 replay). `dump-timeline` refuses older replays with a clear message instead of producing an empty timeline.
- **Unknown builds borrow the nearest older protocol.** s2prot (even its newest master commit) lacks base build 96883, which some of our replays use. `replay.ensureProtocol` registers the nearest older known protocol as a stand-in, and the result is flagged everywhere as "decoded with protocol N, not exact". Decoding 96883 this way had no stream errors. The s2prot dependency is pinned to master (`v1.5.2-0.20260901…`) for builds up to 97563.
- **The builder never sees s2prot types.** `replay.Event` is our own normalized event, so the builder is tested with hand-written fixtures.
- **Hold-then-travel interpolation instead of straight lines.** With samples minutes apart, spreading a move over the whole gap shows units crawling across the map. A unit instead holds its last known position and leaves just in time to reach the next keyframe at its type's speed. This replaces the plan's "straight-line interpolation".
- **No pathing clamp in the tracker tier.** Straight lines between sparse samples can't be made path-correct by clamping, and air units must not be clamped at all. Pathing waits for the command tier.
- **Workers "parked near their base" needs no special case.** Workers are born at their base and are rarely sampled, so hold-then-travel already keeps them there.
- **Tracker coordinates get +0.5.** Tracker x,y are truncated to whole cells, so the cell center is the unbiased estimate. Structures with an even footprint are centered on a cell corner and stay on the integer. This makes resource positions match the map's `Objects` exactly.
- **Death positions are keyframes.** `UnitDied` carries x,y, which gives one more real position per unit for free.
- **Pinned vs movable buildings.** A building is pinned while its current type has no speed. Terran buildings switch to `*Flying` types and crawlers to `*Uprooted`, which have speeds, so samples move them then. **Gap:** a landing or re-rooting position is only known if a later sample arrives. Taking it from the land/root command, as `PLAN.md` suggests, requires knowing which unit the command was issued to. That needs selection tracking, so it moves to the command tier.
- **UI-only units are dropped.** Tracker events include `Beacon*`, `InvisibleTargetDummy` (over 1500 in one bot game) and `BroodlingEscort`. They are tracked internally (so unit slots stay correct) but never written to the timeline.
- **Cosmetic upgrades are dropped** (`Reward*`, `Spray*`, `GameHeart*`).
- **Snapshots hold only the alive set and latest stats.** Positions come from a binary search per track, so snapshots don't need them. `Seek` is checked against a from-zero reference on every loop of the fixture and of every corpus replay (every 37th loop on games over 20,000 loops).
- **Game events are optional.** They are over 90% of decode time on long bot games. `replay.Options.SkipGameEvents` / `dump-timeline --no-commands` cut a 78-minute replay from 21 s to 1.9 s. Player→user ID mapping comes from `PlayerSetup` tracker events (base build 27950+), with lobby slots as a fallback.
- **Proto package is `timeline.v1`** (not `ghostreel.timeline.v1`), because buf's lint requires the package to match the `proto/timeline/v1/` directory the prompt specifies. Go code generates into `gen/timeline/v1`, TypeScript into `viewer/src/gen`. protoc-gen-es is run through `node` so `buf generate` works on Windows.
- **Pathability is cliff edges plus playable bounds.** `t3CellFlags` bit 0 marks cliff-edge cells. Rocks and other blockers come from `Objects`. See `MAP_FORMAT_NOTES.md`.
- **Network access is off by default.** Depot downloads need `--network`, are logged, and are rejected unless the content's SHA-256 matches the cache handle.

## Phase 1 (2026-10-23)

- **One HTTP API for both frontends.** `internal/server` serves `ghostreel serve` and is also plugged into Wails' asset server as its handler. The viewer has no Wails bindings and runs the same code in a browser and in the desktop app. Files reach the core as uploads (file picker, drag-and-drop), which also works in WebView2.
- **Wails v2 (2.17.0), not v3.** v3 is still in beta (3.0.0-beta.34).
- **The desktop app is its own Go module** (`desktop/go.mod`, with a `replace` to the root). Wails needs cgo and GTK on Linux, and keeping it out of the root module keeps `go test ./...` and the WebAssembly build of the core free of UI and native code.
- **The viewer evaluates every unit each frame** rather than using snapshots. The TypeScript model is a direct port of `seek.go`. Evaluating all 3,402 units of a 78-minute game takes 0.07 ms, independent of the loop, so snapshots would only add complexity there. Go and TypeScript are kept in step by golden files: `internal/timeline/golden_test.go` writes the synthetic fixture's timeline and the expected state at 341 loops, and `viewer/src/model.test.ts` replays them.
- **Commands are left out of timelines by default** in `serve` and the desktop app. Decoding game events is most of the load time on long games, and the viewer doesn't show commands yet. `serve --commands` keeps them.
- **Depot downloads are per-replay and explicit.** The viewer shows **Download from Blizzard** when a missing map is on the depot. `--network` stays the command-line switch. Timelines are cached in `<user cache>/ghostreel/timelines/<replay sha256>-tl1.pb`; bump `app.CacheVersion` when the builder's output changes.
- **A map is checked before use.** A picked or found map whose size differs from the replay's is rejected with a message, instead of drawing units in the wrong places.
- **`Minimap.tga` shows only the playable area** (corrects the Phase 0 note). See `MAP_FORMAT_NOTES.md`.
- **Unit labels, not icons,** and footprint/radius shapes from the stub table, until the Phase 3 extractor exists.
- **No spoilers in the inspect panel:** death time and killer appear only once the death has happened.
- **Minimap images load with `createImageBitmap`.** `HTMLImageElement.decode()` stalled indefinitely in a background tab and froze loading.

## Phase 2 (2026-10-24)

### Engine tier

- **`ghostreel-engine` is written in Go, over the s2client API.** Blizzard's `s2client-proto` is vendored unmodified under `third_party/` (MIT) and generated into `internal/engine/s2api`. The exporter starts SC2 as its own direct child (shared hosts may kill SC2 processes with unexpected parents), picks a free port in 5800–5899, plays the replay with fog disabled and the raw interface, and records every unit every 16 loops (0.7 s on the in-game clock). `--max-load` pauses it while the host is busy. Output is gzip-compressed `engine.v1.Samples` (`<replay>.engine.pb.gz`), about 1–5 MiB per game.
- **Ground truth exists only for 4.10 (base 75689).** That is the only client on the sc2-headless VM, the newest Linux package Blizzard published. Pro replays (5.0.x) can't be checked until a Windows client with those versions is available. The five games measured are bot (Terran) vs built-in AI on Ley Lines, Torches, Ultralove and Persephone.
- **Engine tags carry the replay tag in their low 32 bits.** Confirmed on all 209,298 samples of the first game: every one matched a timeline unit.
- **Engine recordings become `SOURCE_ENGINE` keyframes** (`timeline.ApplyEngine`). The app uses them automatically when `<replay>.engine.pb.gz` sits next to a replay opened from disk.
- **Unit speeds in the stub table match the engine's game data exactly** (SCV 2.8125, Marine 2.25, Stalker 2.9531…). A worker's gather round trip is 112 loops.

### Corrections

- **Pathability (corrects Phase 0).** `t3CellFlags` bit 0 marks a *band* around cliffs, and the band's outer ring is walkable on both sides. Treating every bit-0 cell as unpathable blocked 1,428 walkable cells on Ultralove, including the ramps, and sealed main bases. Only cells whose four neighbors all carry the bit are cliff. Against the game's own pathing grid (now exported too), 1.9% of walkable cells are still wrongly blocked, and every ladder map's start locations connect.
- **Repeated commands are target updates.** A replay stores a repeated command as `CmdUpdateTargetPoint`/`CmdUpdateTargetUnit`, reusing the previous `Cmd`'s ability. Bot games have 17× more of those than full commands, so Phase 0, which ignored them, missed most orders.

### Command tier

- **Selection tracking** follows `SelectionDelta` (all four mask forms; selections kept in the game's subgroup order so masks address the right units) and `ControlGroupUpdate` (set, append, recall, clear, steal). Dead units leave every selection. Scored against engine orders, with commands to the same target in the same loop judged together (bots order one unit per command): 74–96% of commands exact, 94–100% precision, 85–98% recall.
- **Movement uses timed legs, not time steps.** Each unit's plan fixes its position at any loop, so events are applied in order and the plan is committed as keyframes only up to each event.
- **What moves units:**
  - Targeted commands to the selection. Point targets keep a loose formation (offsets of at most 2 cells). Unit targets stop at the sum of the radii, plus a rough weapon range for enemies.
  - Shift-queued commands (`cmdFlags & 0x2`) extend the plan.
  - Rally points: a targeted command on selected buildings or larvae sets one, and new units follow it, traced through `creatorUnitTag` from egg to larva to hatchery.
  - Gather orders, rallies to a mineral, and new workers with no known rally start a mining loop calibrated to 112 loops. MULEs mine the field they land at.
- **Pathing:**
  - A* on the walkable grid, with line-of-sight shortcuts.
  - Every structure, of any owner, blocks its footprint while it stands, except lowered depots and lifted buildings.
  - An unreachable goal leaves the unit where it is; this models units trapped behind walls, which really happens.
  - Air units fly straight.
- **Samples snap.** A tracker sample overrides the estimate at its loop; the leg before it bends toward the truth, which blends the error out. The unit then resumes its goal or mining loop.
- **Players without game events** (the built-in AI) can't be estimated and keep their tracker tracks.
- **Results:** on the commanded player's moving units, mean error goes from 8.7–24.2 cells to 3.2–8.0, and median from 5.1–21.7 to 2.0–3.5 (`ghostreel accuracy`; guarded by `internal/accuracy` tests). The largest remaining errors:
  - units in fights (attack-moves stop at enemies the tier can't see);
  - speed changes (Stimpack, creep, Medivac boost);
  - building positions after a lift-off and landing that no sample catches.
- **The app builds the command tier by default.** It needs game events, so first loads are slower on long games (cached afterwards). The cache key records the tier and whether a map was available; picking or downloading a map rebuilds the timeline.

## Phase 2, second round (2026-10-24): attack-moves, speed, accuracy

Every change below was kept only if it lowered the error against the five engine recordings (`ghostreel accuracy`, `internal/accuracy` tests). Moving-unit error of the commanded player, mean (median), in cells:

| Game | Tracker | Command, round 1 | Command, now |
| --- | --- | --- | --- |
| Persephone 13:26 | 19.47 (13.45) | 7.97 (3.53) | 4.32 (2.54) |
| Ultralove 19:33 | 21.56 (16.40) | 7.86 (2.87) | 6.12 (2.57) |
| Torches 15:06 | 22.99 (17.34) | 5.96 (2.64) | 2.91 (2.20) |
| Ley Lines 15:43 | 24.16 (21.74) | 5.29 (2.38) | 3.37 (2.04) |
| Ley Lines 8:56 | 8.71 (5.11) | 3.19 (1.98) | 2.62 (1.92) |

In order of effect:

- **Rally points only from rally abilities.** A targeted command on a selected building used to set its rally, so a MULE calldown on another base's minerals rerouted every new SCV there. Now only `Rally*` abilities and right-clicks set rallies, and Hatcheries keep separate unit and worker rallies. Worker gather/return error fell from 5.5 to 3.6 / 3.1 cells.
- **No steps across cliff levels.** Neighbouring cells on different flat cliff levels don't connect unless one of them is a ramp. This stops paths from running down the walkable rims on both sides of a thin cliff. Marine attack-move error fell from 9.6 to 5.5.
- **Big units need room.** Units with a radius over 0.5 path only through cells inside a fully walkable 2×2 block, so tanks no longer squeeze through one-cell gaps between buildings.
- **Ability names per build.** `internal/gamedata/abilities_gen.go` maps build-specific ability links to move / patrol / hold / attack / stop / Stimpack / Medivac boost / rally. It is generated (`tools/genabilities`) from sc2reader's LotV tables (MIT, `third_party/sc2reader`), using sc2reader's build ranges. Checked on 4.10: every command classified as move became engine order 16 (Move; 65,010 cases), and every attack-move became order 23 (Attack; 4,105 cases).
- **Attack-moves stop at enemies, carefully.** An attack-moving unit stops when an enemy is within weapon range + radius + 1 cell, and walks on after 16 quiet loops. Using every enemy's tracker-tier position made results *worse*: the AI's units sit where they were born until sampled, so units stopped at phantom enemies. Only enemies the replay observed within 40 loops (samples, births, deaths) and structures count now. This gives a small, consistent gain (five-game total 25.2 → 24.5). Window, slack and resume delay were swept, and the results vary by about 1%.
- **Speed changes.**
  - Stimpack gives +50% for 246 loops. It helps only marginally on these games (the bot rarely stims).
  - Creep gives +30%, or ×2.67 for Queens, for Zerg ground units within 12 cells of a Hatchery/Lair/Hive or 10 of a creep tumor; legs use their time-weighted share on creep. **Creep is not measured:** in every recorded game the Zerg player is the built-in AI, which has no orders to follow.
- **Track simplification.** Command and engine keyframes that `PositionAt` reproduces within 0.25 cells are dropped; observations and deaths stay. Engine timelines shrink about 4× (15 MB → 3.7 MB for a 19-minute game), the extreme bot game's command tier from 11.5 MB to 7 MB, and accuracy is unchanged to two decimals.
- **Pathing speed.** Connected components (recomputed only after the grid changes), slice-based A*, and revalidated rather than discarded path caches. The command tier for a 78-minute bot game with 1.5 M game events dropped from 77 s to about 15 s, plus about 15 s of decoding. Pro games take under a second.

Still open:
- **Siege Tanks on Ultralove** remain the largest single error (25.9 cells on attack orders). The real tanks get stuck at a ramp, most likely behind sieged tanks, which units can't push past. The tier doesn't model units blocking each other.
- **Fights themselves** (kiting, chasing).
- **Medivac boost** is classified but not applied.

## Phase 3 (2026-10-24): 3D

### Deviations from the plan

- **CASC is read in Go, not by a C++ extractor.** PLAN.md put CascLib in a separate C++ CLI to keep native code out of the core. No C++ toolchain was available, and a Go port keeps the core cgo-free and one binary, so `internal/casc` ports the parts of CascLib (MIT) needed: `.build.info`, the build config, ENCODING, the `.idx` indexes, BLTE (plain and zlib frames) and the MNDX root that names files. Encrypted ('E') frames aren't supported; none of the files used here are encrypted.
- **No separate extraction step.** Models convert on demand when the viewer first asks for a unit type (`/api/assets/...`), and the result is cached under the ghostreel cache folder per game build. `ghostreel assets` warms that cache and lists gaps.
- **Format reference.** `internal/m3` follows the structure layouts documented by the m3studio / m3addon Blender add-ons (GPL-2). Only the file format facts were used; no code was copied, so the package stays Apache-2.0.

### Facts checked against real files (build 75689, the Linux package)

- 21,308 `.m3` files; every one of the 2,947 unit and building models present parses. MODL versions 23–29, vertex layout from `vertex_flags`, faces local to regions, bone lookups per region. Bind matrices (IREF) are column-major, the same as glTF. Bones' default transforms are not the bind pose, so animations always drive the pose.
- Models face −y in their own space; the viewer turns them so a facing of 0 points +x like the timeline.
- Unit types resolve through `CActorUnit` (by `unitName` or id; morphed forms such as `BarracksFlying` or `RoachBurrowed` through the base actor's `UnitBirth`/`UnitCreation`/`MorphTo` create events) → `CModel` (template inheritance, `##token##` substitution, numbered `_00` variations) → file. `RequiredAnims` `.m3a` files supply missing sequences, matched by bone name.
- **Exit gate:** every unit type in the ladder table converts with a Stand animation, and every moving ground unit with Walk (`internal/assets` `TestLadderModels`). Exceptions, by data: the Interceptor has no skeletal animation, the sieged tank rests in its bind pose (it animates only to fire), and Disruptors hover rather than walk. The **Archon**'s model is listed but stripped from this package, so it couldn't be checked here; that needs a full Windows or macOS install.
- The headless package strips most full-size textures (about 27% present) but keeps 90% of the low-detail `.lvl0` copies (e.g. 64×32 for the Marine), which the converter falls back to. Expect sharper textures from a full install.
- Materials: for opaque materials the diffuse alpha is the team-color mask. Blended materials take opacity from the alpha layer, folded into the base color's alpha at export. Additive and unshaded layers (glows) are drawn unlit from their own or the emissive texture.
- **Terrain heights were 8× too tall.** `t3SyncHeightMap` is fixed point with 15 fractional bits, not 12. Its zero isn't stored; the visual `t3HeightMap` (6 bytes per vertex: unknown u16, quantized height u16, cliff level u16, dequantized with `quantizeBias`/`quantizeScale`/`standardHeight` from `t3Terrain.xml`) has absolute heights and gives the base. Checked against the heights of command target points in replays: within 0.03–0.14 on average (Abyssal Reef, Ley Lines). The Magannatha replay disagrees by 2 with both height maps and the doodads, so it was most likely played on another version of that map.
- Doodads (`ObjectDoodad`) name `CModel` ids directly; 96% of Persephone's 4,334 resolve. The rest are projected decals and splats with no geometry, which aren't drawn.

### Not done

- **Terrain texture blending.** `t3TextureMasks` is 64×64-texel blocks of 8×8 cells (16 KiB per block, so 8 layers × 2 KiB), but neither plain nor block-compressed layouts decoded cleanly. The 3D ground tiles the base layer (`textureList` entry 0, via `CTerrainTex`) shaded by the terrain image instead.
- Cliff meshes (the heightfield's walls stand in), water, creep, particle effects, attachments (weapons on separate models), per-actor scale, and lighting per tileset.
- **Performance** was only measured on a software renderer (Microsoft Basic Render Driver: 55 ms per frame with 96 skinned models), which says little about a GPU. Models give way to stand-ins below 3 pixels per cell and beyond 400 on screen.

## Phase 4 (2026-10-24): 1.0

### Browser build

- **The browser runs the server's own HTTP handler.** `cmd/ghostreel-wasm` builds the Go core for `js/wasm` and answers the viewer's `/api/...` requests in memory, in a web worker so decoding never freezes the page. The viewer swaps `fetch` for the worker (`apiFetch`, `VITE_BACKEND=wasm`) and changes nothing else. The three frontends (desktop, `serve`, browser) share one API and one code path.
- **Maps in the browser come from the user.** This settles PLAN.md's open risk: a page can read neither the Battle.net cache nor Blizzard's depot (plain HTTP on port 1119, no CORS), and a fetch proxy would mean running a server that sees users' map requests. So the browser build asks the user to drop the map file. To make that possible, the app now keeps decoded maps in memory and decodes picked maps from bytes; no part of map handling needs a file system any more.
- **Size and speed.** `ghostreel.wasm` is 20.6 MB (4.6 MB gzipped), mostly s2prot's protocol tables. A 3-minute pro game opens in about half a second. A 30-minute bot game with 1.5 million game events took about a minute to rebuild its command tier once the map was added, roughly twice native speed.
- **Not in the browser:** game models (no install to read), engine recordings, timeline caching, and replay folders.

### Analysis plugins

- A plugin reads the finished timeline and returns **generic** markers (moments or spans, optionally a place) and tables (rows tied to loops). The viewer draws any report as panel tabs, scrubber bands and minimap rings, so a plugin needs no frontend work. Plugins register from `init` and run on demand (`/api/r/{id}/analysis`, computed once per timeline). A failing or panicking plugin shows as a failed tab and doesn't affect the others.
- **Build orders** list structures when placed and units, morphs and research when finished. Replays don't record queue times, so those are completion times; supply comes from the latest stats event, so it can lag by up to 10 s. Mode switches (lift-off, siege, burrow, warp gate) and spawned units (larvae, broodlings, interceptors, MULEs) are left out, and repeats within 2 s are merged ("Zergling ×6").
- **Battles** are deaths of players' units within 14 cells of a running cluster's center and 15 s of its last death, kept at 6 deaths or more. Larvae, eggs and other spawned units don't count. These thresholds are judgment calls checked by eye on five pro games, not tuned against labelled data.

### Installers and releases

- A version tag runs `.github/workflows/release.yml`, which builds CLI archives for five platforms (with the viewer, which `serve` now also finds next to its executable), the browser build, and the desktop app: an NSIS installer on Windows, a universal app on macOS, a binary on Linux (WebKitGTK 4.0, so built on Ubuntu 22.04). Everything goes into a **draft** release with checksums.
- The Windows installer associates `.SC2Replay` through Wails' `fileAssociations`, with a generated document icon. **Not verified locally:** NSIS couldn't be installed on the development machine (the installer's permission prompt was declined), so the installer step itself first runs in CI. Everything before it (the app build, icon and installer script generation) was checked with `wails build -nsis`.
- **Not done:** code signing (needs maintainers' certificates), opening replays from Finder on macOS (Wails delivers those as events after start-up; the app handles command-line files only), Linux packages (.deb, AppImage).

