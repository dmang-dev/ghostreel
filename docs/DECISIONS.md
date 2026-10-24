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
