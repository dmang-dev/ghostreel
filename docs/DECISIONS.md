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

## Phase 5 (2026-10-24): depth

### Analysis

- Reports gain **series** (values over time), drawn as charts with a cursor at the current moment; clicking a chart seeks. New plugins: **economy** (income, workers, army value, supply, resources lost, from the stats events every 10 s), **expansions** (town halls placed after the start: where, when started and finished), and **supply blocks** (spans with no free supply below 200, accurate to the 10 s stats interval). APM is not done: the timeline doesn't keep every command.

### Terrain

- **`t3TextureMasks` is decoded.** It holds 8 layers, each 4 texels per cell across and 8 up (on all 13 maps tried). A layer is 32×32-byte tiles in strips two tiles high: down the strip first, then across, strips from the bottom up. Two measurements found this. First, byte-difference autocorrelation showed 1 byte per texel and 32-byte rows. Second, matching tile edges showed that the next tile (+1) continues upward and +2 continues to the right. The 8 weights of a texel sum to 255. The Phase 3 guesses (64×64 blocks, BC4) were wrong.
- The server composes the ground from those masks and the layer textures (`CTerrainTex`), tiled as `uvtiling` says, at up to 8 pixels per cell, in about 0.3–0.8 s per map. Composed images matched each map's own minimap by eye. The viewer offers the result as **Ground textures** in 2D and 3D, the default when it's available. It needs the game's data, so the browser build doesn't have it.
- **Creep** is drawn around finished hatcheries (12 cells) and creep tumors (10), grown over 12 s and kept to walkable ground. It is an estimate: the game spreads creep cell by cell and lets it recede slowly.
- **Cliff meshes** are not done: the game builds cliffs from tile-set cliff models placed by rules not decoded here, so the heightfield's walls remain.

### Command tier, round 3: nothing kept

Two changes were measured on the five engine recordings and **reverted**, because neither lowered the error:

- **Mining for players without orders** (the built-in AI). The AI's workers stay on the tracker tier, which holds each worker where it was last seen. That is close to right for workers mining at their birth base: the median error for the AI's Probes on Persephone is 4.5 cells. Sending new workers to mine at their nearest base raised the AI player's moving-unit error in all five games (for example 17.7 → 18.5 cells). Adding the AI's saturation behaviour (new workers to the emptiest base, transfers when a base finishes) made it worse still (17.7 → 21.4; the Probe median 4.5 → 24.2): the AI's real transfers don't follow that rule closely enough.
- **Siege Tanks on Ultralove** (mean 44.6 cells under the command tier vs 15.0 for the tracker tier). The engine shows ten tanks jammed between a mineral line, supply depots, a Bunker and a Barracks. Several held attack-move orders for minutes without moving. The pathing grid shows a clear route; what stops them is the other tanks, and the replay has no trace of that. Unit-on-unit blocking stays out of reach.

Also found: in the Ley Lines games, the large SCV and Medivac errors belong to the **AI** (also Terran), which the command tier doesn't model. The commanded player's numbers stand as in round 2 (moving units, mean): 4.32 / 6.12 / 2.91 / 3.37 / 2.62.

## Hotspots, director camera, video export (2026-10-25)

Requested after reviewing Adjutant, the SC2 highlight generator. Adjutant does this with a live game client: burnysc2 drives the observer camera while FFmpeg screen-captures the window. That needs the replay's exact game version installed, the map in the game's `Maps` folder, and a desktop to capture. ghostreel already reconstructs and draws the game, so all three pieces moved in-house:

- **Hotspots** (`internal/analysis/hotspots.go`) port Adjutant's detectors to the timeline (it's MIT, the user's own project): battles, army wipes (6+ army units in 3 s), worker raids (4+), base kills, high-value kills (merged within 5 s), loaded transports (2+ of the owner's units dying with them), siege breaks, multi-prong attacks (two fights 40+ cells apart at once), first blood, and the end of the game. Scores use Adjutant's weights, normalized to the game's best. One change: fights are weighed by the **resources each side lost** (player stats before and after), not just by deaths. A hotspot is **major** at 1,500+ resources lost or 25+ deaths, or when it scores 0.6+ (early fights lose less but matter as much). Battles and hotspots share one death clustering.
- **Director** (`viewer/src/director.ts`) uses Adjutant's camera priorities: hotspot (3 s early, 2 s after; higher score wins), then recent deaths (8 s), then the densest army group of each player in turn (20 s each), then the whole map. It is a pure function of the loop with exponential easing (1.2 s for position, 1.8 s for zoom) and cuts on seeks, so playback, scrubbing and export all get the same camera.
- **Video export** (`viewer/src/exporter.ts`) steps the replay clock frame by frame at a fixed size (the scene gains `beginExport`/`endExport`). It composes the view, the labels and an overlay (players, clock, caption, minimap), then encodes with **WebCodecs**, trying H.264, then VP9, then AV1. `mp4-muxer` (MIT) writes the MP4. There is no FFmpeg and no server work, and it runs faster than real time where rendering allows. Checked end to end in headless Chrome on the software renderer: a 5-clip reel at 720p and 2× rendered 1,092 frames in 99 s and played back as a 1280×720, 36.4 s MP4. **Not covered:** the Linux desktop app (WebKitGTK has no WebCodecs; a MediaRecorder/WebM fallback is possible) and audio.

## Sharper 3D ground (2026-10-26)

Reported: the 3D terrain looked low quality. Three causes, in order of size:

- **The game data here has only the small textures.** On the Linux headless package that `testdata/casc` comes from, every terrain texture tried is in the storage only as its 64×64 `.lvl0` copy; the full `.dds` files are listed but not stored locally (the same gap as the unit textures, above). With layers repeating about every 10 cells, that's about 6 texels per cell, so the ground blurs up close however it is drawn. A full Windows or macOS install has the full-size textures. This could not be checked here; there is no full install on the development machine (Phase 6, track B).
- **The ground was baked into one image** at up to 8 pixels per cell, then stretched over the map. Now the server also serves the masks (`GET /api/r/{id}/masks`, two RGBA planes gzipped; the size and `uvtiling` come in the map JSON as `terrainMasks`). The viewer blends the 8 layer textures by those masks on the GPU (`viewer/src/ground.ts`), so the ground is as sharp as the textures, in 2D and 3D. Layers with no weight at a pixel aren't sampled. The baked image stays as the fallback until the layers load, and for the minimap. Layer textures are served opaque: their alpha holds something else, and browsers premultiply it into the colours.
- **The heightfield had one vertex per cell**, so cliffs were stair-stepped walls with the ground image smeared down them. It now has 2 vertices per cell, smoothed by one [1 2 1] pass (edges round off by about half a cell; cliffs stay steep), and units, doodads and picking use the same surface. Steep faces take the textures projected from the side, greyed and darkened toward the game's rock, instead of smeared top-down. The cliffs are still not the game's cliff meshes.

A light procedural grain (value noise, every 3 cells) keeps low-detail textures from reading as flat blur; mipmaps fade it with distance. Cost, measured on the development machine's software renderer (no GPU) at 1280×720: Ground textures take 6–27% longer per frame than Shaded, down from 3.3× before the layers were skipped where unweighted. The creep overlay now draws only while there is creep. Also: per-replay data (timeline, map, minimap, doodads, ground, masks) is now sent `no-cache`. It used to be cached for an hour, but it changes under the same id when the map is picked or downloaded and when ghostreel is upgraded.

**Checked against a full install (2026-10-29).** A Windows install of 5.0.16 (base build 97563), read from a network share. The game-data tests can point at any install with `GHOSTREEL_TEST_GAMEDATA`. Everything passed: CASC and MNDX names, all 6,012 models converted with none failing, ladder units, terrain textures, ground and cliffs. Its terrain textures come through at 512×512, ghostreel's size cap, instead of 64×64, and the ground is sharp up close. The grain now scales with texture size: full for 64 px, none from 256 px, since full-size textures have their own detail. What the sharp textures show next: edges between layers step along the masks' texel grid (4×8 per cell) on diagonals.

**Layer edges (2026-10-29).** Two changes. First, the masks are read through a cubic B-spline, with a wobble of about a texel from noise, so edges curve instead of stepping. Smoothing alone only made the edges soft: the raw masks do have texel-level detail, but what shows is the shape painted into them. Second, the layers are now **blended by height**. A terrain texture's alpha isn't coverage: it's a height map (raised tiles, low grooves, grainy grass, standing pebbles; 0–65 on the textures checked). Where layers overlap, the one standing highest for its weight wins, within a small depth: score = weight + 0.5 × height, and layers within 0.3 of the top score show. Grass now breaks over paving in tufts, and dirt patches follow the texture, instead of cross-fading. The viewer asks for `?height=1`, which puts the height, scaled to each texture's own range, in alpha as 128–255. A browser that premultiplies alpha then loses at most a bit of colour; plain requests stay opaque.

## Maps beside replays, desktop settings (2026-10-27)

- **Local maps are looked for next to the replay**, then in a `maps` folder beside it (any case, not recursive), before the configured map folders. Bot harnesses keep their maps that way (GhostProtocol's `data/replays/maps`). The replay's path travels in `replay.Info.Path`, which `app.LoadFile` now sets; uploads have none.
- **The desktop app opens replays by path.** Its **Open replay…** shows a native dialog (`POST /api/pick`) and dropped files come through Wails' file drop with their paths; both open with `POST /api/open-path`. That endpoint exists only when `Server.OpenPaths` is set, which only the desktop app does: `ghostreel serve` can be reached by any web page on the machine, so it doesn't open arbitrary paths.
- **A missing map is looked for again** when a replay already loaded is opened a second time (now perhaps by path) and after the settings change (`POST /api/r/{id}/find-map`).
- **Settings page (desktop only):** the StarCraft II folder and extra map folders, in `settings.json` under the user config folder. The folder is checked for `.build.info` (accepting its parent too), its `Maps` folder joins the search, and the game data reopens in the background; the viewer polls `/api/settings` and reloads the replay on screen once the models are available. Extra map folders are set with `Resolver.SetExtraMapDirs`, which is safe while lookups run.

## Releases and self-update (2026-10-27)

- **Tags publish releases.** The release workflow used to make a draft for review. The updater only sees published releases (GitHub's `/releases/latest` skips drafts and pre-releases), so a `vX.Y.Z` tag now publishes, and a suffixed tag (`-rc1`) makes a pre-release for trying builds first.
- **Signed manifest, after evi-ai's desktop updater.** Each release has `latest.json` (version, release page, and per file: role, URL, size, SHA-256) and `latest.json.sig`, an Ed25519 signature. The app trusts a manifest only with a valid signature for the public key built into it (`internal/update`), and a download only if its size and hash match. That covers a tampered release asset and a man in the middle, with nothing but Go's standard library. The private key is the `GHOSTREEL_UPDATE_KEY` secret; `cmd/releasetool` makes keys and manifests.
- **Applying an update (Windows).** An installed copy (its `uninstall.exe` is beside it) downloads the new installer and runs it silently into the same folder (`/S /D=`) through `cmd /C start`, so Windows shows its usual elevation prompt. It waits for the app to exit, then starts the new version. A portable copy renames its running exe aside (Windows allows that), writes the new one in its place, starts it, and the next start deletes the old one. macOS and Linux link to the release instead. Builds from source (`version` = `dev`) never replace themselves.
- **Asked, not assumed.** ghostreel made no network requests unless asked, so the app asks once whether to check on start. The answer is in the desktop settings (`checkUpdates`), with **Check now** there.
- **Not checked end to end.** Check, download, verification, the swap decision and quitting are tested against a local fake release (`desktop/update_test.go`), and a manifest made with the real key verifies. Running a real installer over an installed copy, and the portable swap, can only be tried with two published releases. The repository is private, so the unauthenticated manifest URL returns 404 until it's public.

## AI workers, construction, cliffs (2026-10-27)

- **The built-in AI's workers are drawn mining.** The AI issues no commands, so its workers stood where they were last seen all game. The viewer (`viewer/src/mining.ts`) now walks any worker of a player without command or engine positions that the timeline holds still within 10 cells of one of its finished bases on a gather round trip (112 loops, the command tier's) to a field or refinery of that base: 3 per refinery out of 22 per base go to gas. Which field is a guess. It is drawn only, eased in and out over a second around real samples; the timeline and the measured positions are unchanged (in the timeline it raised the error, as above), and the Go parity test runs without it.
- **Buildings under construction** played their birth clip on the game clock, so they looked finished at once. Construction now follows progress, from the unit's birth to its finish or, for one cancelled or unfinished, its type's median build time in this game (else 1000 loops). Terran and Zerg buildings step through the game's build stages A to D, each its start clip then its looping stand clip; each stage is taken as a quarter of the build time, an estimate. Protoss buildings have no build clips (warping in is a material effect in the game), so they are drawn translucent and glowing blue, solid when done.
- **Resource models.** Model files with variations exist only as numbered files, and game versions differ in the numbers they ship: the lookup now tries `_00` to `_09`, and a mineral field or geyser whose own model is missing borrows a related one rather than falling back to a box. Reported from a current Windows install, which isn't available here, so this is the likely cause, not a confirmed one.
- **Cliff meshes.** The game's cliff pieces are now drawn, placed as `MAP_FORMAT_NOTES.md` describes: on every test map every cliff cell that isn't flat gets a piece whose turned corners match the map's (`internal/assets/cliffs_test.go`). Checked by eye on Pylon AIE and Berlingrad, in the game data here (Linux 4.10, 64-pixel textures); not compared with the game itself. In cliff cells the ground steps (each heightfield vertex takes its nearest corner's height, the lower on ties) instead of sloping, so it stays behind the walls, and gets no rock look there; a faint groove remains behind some wall tops. Ties to the higher corner were tried: they close the groove but raise the ground in front of the walls, hiding them.
- **Floating doodads: the ground was drawn too low on some maps.** The ground's base (the sync height map stores heights above an unrecorded zero) came from the visual height map, which is off by a constant on some maps: on Ultralove the doodads, and the command targets of a replay there, sit 5.90 above the ground drawn. Doodads are placed at absolute heights and mostly on the ground, so the base now comes from them (the most common gap between doodad and sync heights, when at least a quarter of 50 or more agree), else from the visual map as before. Command targets now match within 0.05 on Abyssal Reef, Ley Lines, Persephone, Torches, Ultralove and Pylon (3 points there); on Ultralove 74% of doodads, before none, stand within 0.3 of the ground. Doodads still above the ground after this are meant to be (props in the air, debris over a space map's void).
- Not done: ramps (the game builds them from `<rampList>`, also in `t3Terrain.xml`) keep the smoothed heightfield; the `f` flags of cliff cells aren't understood.

## Health, shields and energy (2026-10-29)

PLAN.md Phase 6 track F, steps 1–4. Replays have no hit points, so these come only from the engine tier:

- **Recorded:** `ghostreel-engine` now keeps each unit's health, shields and energy and their maxima (new `UnitSample` fields; older recordings read with none).
- **Timeline:** `Track.vitals`, built by `timeline.ApplyEngine`, keyed when a value moves by more than 1% of its maximum or a maximum changes. A key holds until the next: damage is discrete, so no interpolation. Engine-tier timelines aren't cached, so no cache version change.
- **Viewer:** bars above units on the labels layer (so exported video has them), drawn for damaged and selected units by default, **H** for all or none; none for units hidden by fog; numbers in the inspect panel. Checked with sample values injected in the browser.
- **Not yet:** none of the five recordings has vitals; re-recording them needs a run on the sc2-headless VM, which waits for its owner's go-ahead (step 5).

## Getting ready to be public (2026-10-29)

Checked before making the repository public: the whole history (81 commits, every version of every file) has no tokens, keys, passwords, private addresses, local paths or email addresses (commits use the GitHub noreply address), and no game files; the demo GIFs show only Shaded terrain and stand-ins. Changes made:

- **CascLib's MIT notice.** `internal/casc/mndx.go` is a port of CascLib's MNDX parser; its license now sits in `third_party/casclib/` like the other borrowed code, and the file is marked `Apache-2.0 AND MIT`.
- **Workflow permissions.** Both workflows are read-only by default; in the release workflow only the job that publishes can write, so the build jobs (which run `npm ci` and Go module downloads) can't touch releases. The two third-party actions (`softprops/action-gh-release`, `bufbuild/buf-action`) are pinned to commits rather than tags their owners can move; GitHub's own `actions/*` stay on major tags.
- **Third-party notices in downloads.** `releasetool notices` collects the license files of Go, every Go module linked on each release OS, the viewer's production npm packages and `third_party/`, and every archive ships them. `@bufbuild/protobuf` ships no license file, so its entry gives the declared licenses and the source.
- **`SECURITY.md`** asks for private reports through GitHub's private vulnerability reporting, which has to be turned on in the repository settings once it is public.

## README media may show the game's art (2026-10-29)

Until now committed screenshots had to be art-free (Shaded terrain and stand-in shapes), a rule from Phase 1. With the ground textures, cliffs, doodads and unit models looking like the game, the README now shows them instead: the owner's call. The recordings are screenshots of ghostreel running against a full Windows install (game build B97563), made with the scripted headless browser as before. They are pictures of the program, in a free, unofficial, non-commercial project, which Blizzard's fan content terms allow; no game files (models, textures, maps, replays) go in the repository, as before, and the README credits the art to Blizzard. Without an install the viewer still uses Shaded terrain and stand-ins.

## Analysis: APM, production, army, upgrades, supply to the loop (2026-10-27)

Phase 6, track E. Four new plugins (`apm`, `army`, `production`, `upgrades`) and a rewritten `supply-blocks`. The viewer now shows a report's tables under its chart.

- **Action counts live in the timeline** (`Timeline.actions`): per player, actions and commands in 5 s bins (112 loops). Counting them while building costs nothing, and they survive `SkipCommands`, which the app uses by default, so APM doesn't need the large command feed. An action is a `Cmd` or a retarget (`CmdUpdateTargetPoint/Unit`), a `SelectionDelta` or a `ControlGroupUpdate`, as sc2reader counts them. `CommandManagerState` is left out: in the pro replays it almost always follows a retarget (559 of 617 in one game), so it marks the same keypress. Camera moves are not actions. The pro replays come out at 218-378 APM, in line with those players' usual numbers. Game events are now decoded on every tier but an explicit tracker tier (they already were for the default command tier). On the 78-minute bot game, decoding them takes 15 s instead of 0.4 s.
- **Units keep their creator** (`Unit.creator`): the building that trained a unit, the egg it hatched from, the hatchery that spawned a larva. Production idle time needs it, and the supply reconstruction uses it for exact Zerg start times. Both fields are additive, so older timelines still read. The cache version moved to `tl5` so cached timelines are rebuilt with them, and to `tl8` when this was merged with command tier round 4 (which had used `tl6` and `tl7` on its branch).
- **Supply at every loop** (`supplyCurves`): units take supply from the start of their production until they die, and supply buildings give theirs once finished. The start is the egg's morph for Zerg and the placement for warp-ins (both exact), otherwise the train time before the unit appears. Comparing with the replays' stats led to two corrections. A Drone that starts a building is reported dead only when the building finishes, so it is matched to that building and stops counting when the building is placed. And a Protoss building can't start a unit before its previous one is out, which undoes most of Chrono Boost's error. On the five 2022 pro replays the curve then matches the stats (supply used within 1, supply made exact) at 81-96% of samples, and the Zerg AI in the bot games at 99%. What remains is mostly **queued units**: the game takes supply when a unit is queued, not when it starts, and the replay doesn't record queues. One Terran bot game sits 19 supply under the stats for half an hour, its queues stuck behind lost depots.
- **Supply blocks combine both.** Blocks on the curve give edges to the loop. A stats sample that sees a block the curve misses adds one, from the last unit start since the previous sample to the next supply building finishing or the next unblocked sample. A curve block that a stats sample contradicts, and that no sample confirmed, is dropped. Blocks under 1 s are left out. The pro games' opening blocks now start 3-5 s earlier than the stats showed, and short blocks the stats missed appear (1-2 s at 23/23 in two TvT games).
- **Production idle**, per building: busy while training (the train time before each unit it made, chained as above), building an add-on (matched by position, since the replay doesn't link them) or upgrading to an Orbital Command or Planetary Fortress; idle the rest of its time finished and on the ground. Warp gates are pooled per player because warp-ins don't name their gate. Zerg hatcheries are idle while they hold 3+ larvae. Not modelled: a reactor's second slot, Chrono Boost after an idle gap, and research.
- **Army composition** reports finished units that take supply, other than workers, folding modes (SiegeTankSieged) and cocoons (BanelingCocoon) into the unit. **Upgrades** lists research when it finished. The replay records no start, and estimating one would need per-upgrade research times.
- Game numbers (supply costs, supply given, train times, warp gate cooldowns) are a table in `internal/analysis/unitdata.go`: current-patch values for every build, like `gamedata.Stub`. Units the table doesn't list take no supply.

## Command tier, round 4 (2026-10-27)

Phase 6 track C, items 1–4, measured as before on the five engine recordings (`ghostreel accuracy`): the commanded player's moving units, mean (median), in cells. A change was kept only if it lowered the error without raising another game's mean or median.

| Game | Round 3 (baseline) | Now |
| --- | --- | --- |
| Persephone 13:26 | 4.32 (2.54) | 4.32 (2.53) |
| Ultralove 19:33 | 6.12 (2.57) | 4.52 (2.54) |
| Torches 15:06 | 2.91 (2.20) | 2.88 (2.18) |
| Ley Lines 15:43 | 3.37 (2.04) | 3.18 (2.01) |
| Ley Lines 8:56 | 2.62 (1.92) | 2.62 (1.92) |

### Kept

- **Siege Tanks path with a cell of clearance** (item 3). The Ultralove jam was not tanks blocking each other. Every tank there stopped at the same spot because the main's ramp is too narrow *for a tank* once a Barracks stands at its foot: Marauders (radius 0.56) still walk down it, tanks (0.875) never do. The engine gives up on an unreachable move and leaves the tank where it got stuck. The 2×2 rule for big units lets a unit through a one-cell diagonal gap (1.4 cells wide), so the tier walked the tanks out. Units with a radius over 0.8 now search the corners between cells instead (`pathing.Huge`): a corner is open when its four cells are walkable and on one level, a diagonal step needs the 3×3 cells around the cell it crosses, and string-pulled segments keep 0.875 cells clear. That is a disc of a tank's radius moving through the grid, so a straight two-cell gap still fits. Ultralove's Siege Tank error fell from 44.6 to 12.7 cells.

  | Game | Before | After |
  | --- | --- | --- |
  | Persephone | 4.32 (2.54) | 4.32 (2.53) |
  | Ultralove | 6.12 (2.57) | 4.53 (2.55) |
  | Torches | 2.91 (2.20) | 2.91 (2.20) |
  | Ley Lines 15:43 | 3.37 (2.04) | 3.36 (2.03) |
  | Ley Lines 8:56 | 2.62 (1.92) | 2.62 (1.92) |

  A plain 3×3 block rule was tried first and dropped: it fixed Ultralove (4.69) but sealed tanks inside their own bases elsewhere (Persephone 4.32 → 4.78, Ley Lines 3.37 → 3.65; Persephone's tanks 3.4 → 22.9 cells).
- **Paths start on the goal's side of a wall.** A unit standing on a blocked cell (a Ghost born at the foot of its Barracks, on a cliff edge) snapped to the first passable cell in ring order, which could lie below the cliff and wall it in for good. It now takes, among the nearest passable cells, one in the goal's region. Ley Lines 15:43: 3.36 (2.03) → 3.18 (2.01); the other games unchanged.
- **Workers leave mined-out fields.** When a mineral field runs out, its workers move to the fields left within 8 cells, the least used first, as the game does; before, they shuttled to a field that was gone. Torches 2.91 (2.20) → 2.88 (2.18), Ultralove 4.53 (2.55) → 4.52 (2.54), the others unchanged. Sending them to the nearest field instead raised Torches' median (2.20 → 2.21).
- **Selection tracking is better than it looked** (item 1). On Ultralove 85% recall meant 15% of ordered units missing from the tracked selection, but nearly all of those were ordered by a neighbouring command: the bot orders one unit per command, often to points a fraction of a cell apart, or a few loops before the engine frame. `CheckSelection` judged each command alone. It now judges a command together with every command since the previous frame whose target matches (the same unit, or a point within half a cell), and leaves out units that already carried the order. Recall: Persephone 95% → 97%, Ultralove 85% → 96% (point targets 86% → 97%, exact 74% → 87%), Torches 95% → 97%, Ley Lines 95% → 98% and 98% → 98%. Most of what remains is units taking the same unit target on their own (workers moving to a free field, units acquiring an attacker). No control-group or raw-API case was found where the tracker misses a commanded unit, so the tracker is unchanged; this is a measurement fix and timelines are unchanged.

### Reverted

- **Unreachable goals: go as close as possible.** The engine's tanks stop at the reachable point nearest their target; the tier leaves them where they are. Moving them there (nearest node of the unit's region, `Grid.Closest`): Persephone 4.32 → 4.28, Ultralove 4.53 → 4.22 (2.55 → 2.44), Torches 2.91 → 2.90, but Ley Lines 15:43 3.18 → 3.21. Limited to units whose way is blocked only for their size (a Small unit could pass): Ultralove 4.52 → 4.22 (2.54 → 2.42), Torches 2.880 → 2.877, but Persephone 4.317 → 4.319 and Ley Lines 3.18 → 3.20, 2.622 → 2.626. On Ley Lines a tank born in a pocket of buildings that the corner graph considers sealed walks to the pocket's edge, while the real tank finds its way out. Worth retrying if those false walls are found.
- **Siege Tanks blocking their cells while sieged** (other units path around them): Persephone 4.317 → 4.323, Ultralove 4.52 → 4.61 (2.54 → 2.57), Ley Lines 3.18 → 3.22.
- **Fights: chasing** (item 2). Engaged units closed to weapon range of the nearest enemy instead of holding: with the current acquisition slack (1 cell) nothing changed (Ultralove 4.53 → 4.54, Torches median 2.20 → 2.22); with 3 or 5 cells of slack it got worse (Ultralove 4.64 / 4.68). Enemy positions come from the tracker tier and are too coarse to chase. For reference, turning engagement off costs 0.6 cells over the five games (total of means 17.56 → 18.18), so stopping at enemies still pays.
- **Mining timing.** The engine's SCVs stand still at the field for about 25% of a gather cycle, so they mine for about 44 loops (2.8 game seconds) and spend the rest walking or waiting at the base; the tier stands them at the field for the whole cycle minus a full-speed walk (about 95 of 112 loops at the near fields). A 45-loop dwell with slower trips filling the cycle lowered every mean (Persephone 4.317 → 4.284, Ultralove 4.520 → 4.489, Torches 2.880 → 2.853, Ley Lines 3.178 → 3.147, 2.622 → 2.598) but raised four medians (2.533 → 2.534, 2.541 → 2.544, 2.006 → 2.020, 1.922 → 1.933). Waiting at the base instead of slowing down, or dwelling 52–70 loops, did the same. The cycle length itself (108–120 loops) changes nothing at two decimals. The mining error (about 3 cells, a third to half of the remaining error) is mostly phase: nothing in the replay says where in its trip a worker is.

### Not done

- **Medivac boost** (item 4). The bot never uses it: no command in the five recorded games classifies as afterburners, and the two pro TvT replays here have none either, so there is nothing to measure it against. It stays unapplied.
- **The built-in AI** (item 5) was out of scope; the viewer draws its mining itself now.

## Smaller browser build (2026-10-27)

- **The protocol tables weren't the bulk.** PLAN.md and the Phase 4 notes blamed s2prot's protocol tables for most of `ghostreel.wasm`. Measured from the module's own sections, the 157 protocols are 0.82 MB of source text, 4% of the file. Consecutive builds barely differ, so they shrink to 37 KB gzipped and 18 KB with Brotli. Most of the file was code the browser build can't use.
- **Loading only the replay's protocol: not done.** It would save 0.82 MB raw but under 40 KB on the wire. The cost would be replacing s2prot's `build` package in the wasm build (an overlay over a dependency) and a round trip to the page before each decode.
- **What the browser can't run is compiled out.** The browser build already had no map lookup, engine recordings or game data, but their code was still linked. It now sits behind `runtime.GOOS == "js"` (map lookup in `internal/app`, the asset routes in `internal/server`) or a `js` build tag (`internal/app/engine_js.go`), so the compiler drops it. Other builds are unchanged.

| Step | `ghostreel.wasm` |
| --- | --- |
| Before (already built with `-trimpath -ldflags "-s -w"`) | 21.10 MB |
| Map lookup out: the depot client brought in net/http's client, TLS, x509 and HTTP/2 | −4.77 MB |
| Engine recordings out: package `engine` and the SC2 API's protobuf types | −1.13 MB |
| Asset routes out: packages `assets`, `m3`, `casc`, `dds`, image codecs | −0.80 MB |
| `gamedata` without its one regexp | −0.11 MB |
| After | 14.30 MB |

- **Linker flags were already in place.** `-ldflags "-s -w"` saves 0.33 MB (the name section) and `-trimpath` saves 25 KB, both since Phase 4. Turning inlining off (`-gcflags=all=-l`) made the file bigger (15.2 MB).
- **What's left** (8.8 MB code, 5.6 MB data): the Go runtime (1.2 MB of code), the protobuf runtime for timelines (1.8 MB), `encoding/json` (0.65 MB), our own packages (0.65 MB), and about 0.8 MB of crypto (P-256, ML-DSA, `math/big`) kept alive by the package initializers of `crypto/tls` and `crypto/x509`, which `net/http` always imports. Only dropping `net/http` would remove those, and that would mean not reusing the server's handler, which is the reason the browser build exists. `httptest` and `flag` add 24 KB.
- **The core ships compressed, and the page decodes it.** `npm run build:web` also writes `ghostreel.wasm.gz` (gzip -9) and `ghostreel.wasm.br` (Brotli quality 11, 16 MB window, about 30 s). The worker fetches the best one it can decode with `DecompressionStream`, so the download is small on any static host with no `Content-Encoding` setup. The order is Brotli, then gzip, then the raw file. No browser decodes Brotli this way yet (Chrome 155 offers gzip and deflate), so in practice it fetches the `.gz`. A host may send the `.gz` with `Content-Encoding: gzip` (`vite preview` does), in which case the browser has already decoded it; the worker checks for the wasm magic number before decompressing. A missing or failing variant falls back to the next. `npm run dev:web` skips compression and deletes stale copies, so it always runs the fresh raw file.

| | Raw | gzip -9 | Brotli 11 |
| --- | --- | --- | --- |
| Before | 21.10 MB | 4.78 MB | 3.42 MB |
| After | 14.30 MB | 3.39 MB | 2.42 MB |

- **As transferred.** Before, the worker fetched the raw file, so a host that doesn't compress `.wasm` (as `vite preview` doesn't) sent 21.10 MB, and one that gzips on the fly sent about 4.8 MB. Now Chrome fetches the 3.39 MB `.gz` from any host. The release's web zip grows from about 5 MB to 9.3 MB, because it holds the raw file and both compressed copies.
- **Checked end to end** in headless Chrome against `vite preview`. The page opened a replay and its map through the file pickers, and every replay in `testdata/replays` gave a timeline byte-identical to `ghostreel serve`'s (with its map, where `testdata/maps` has it). The worker's other paths were checked by rewriting responses: a plain `.gz` (decoded by `DecompressionStream`), a missing `.gz` answered with a dev server's HTML fallback, and a 404 (both use the raw file).
- **Hosting needs nothing special.** Serve `dist-web/` as static files. The compressed copies work under any `Content-Type`, with or without `Content-Encoding`; only the raw fallback needs `application/wasm` (for streaming compilation). The `ghostreel.wasm*` names carry no content hash, so they should be revalidated (ETag or `Last-Modified`) rather than cached for a long time. A host that compresses on the fly gains nothing for the `.gz`, which is already compressed.
