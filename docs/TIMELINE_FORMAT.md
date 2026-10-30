# Timeline format (`timeline.v1`)

A timeline is a replay reconstructed for playback. Every frontend reads it, whichever reconstruction produced it. The schema is [`proto/timeline/v1/timeline.proto`](../proto/timeline/v1/timeline.proto). This page explains the conventions the schema can't express.

Files are binary protobuf (`.pb`). `dump-timeline --json` writes the protobuf JSON mapping for debugging. Readers must check `schema_version` and refuse values they don't know.

## Units and coordinates

- **Loops** are game loops. At "Faster" (every ladder game), 22.4 loops are one second of the in-game clock. Show times to users as `loop / 22.4`. Game data such as unit speed is per *game* second (16 loops).
- **Positions** are map cells as floats, origin at the bottom-left, y up (the game's convention). Map grids from `dump-map` use the same orientation.
- **Players** are 1-based IDs matching `Meta.players[].id`. 0 is neutral (resources, rocks).
- **Colors** are `0xRRGGBB`.

## Units

`units[i].id == i`. A unit exists from `born_loop` (inclusive) to `died_loop` (exclusive): at its death loop it is gone. Units placed as construction sites have `constructed = true`, and `done_loop` once finished.

`type` is the type at birth, an index into `unit_types`. `morphs` lists later type changes (larva → egg → drone, Barracks → BarracksFlying, …). Owner changes work the same way. Use the last morph or owner change at or before the loop.

`creator` is the unit this one came out of: the building that trained it, the egg a Zerg unit hatched from, the hatchery that spawned a larva. The replay names it only for units born complete, so it is unset for construction sites and warp-ins (and in timelines written before it was added). `creator_ability` is the ability that made the unit (`BarracksTrain`, `WarpGateTrain`, `CalldownMULE`), when the replay says.

`killer` is the unit that dealt the killing blow, when the replay says and that unit is in the timeline; `killer_player` is its owner. Counting the units that name a unit as their killer gives its kill count.

`unit_types[].class` says how to draw a type. `UNIT_CLASS_HIDDEN` types are UI-only and never appear in `units`. `known = false` means the sizes are defaults for a type the game-data table doesn't list. `speed` is cells per game second, 0 for things that never move.

## Tracks and interpolation

Each unit with any position has one track. Its keyframes are sorted by loop, at most one per loop. A keyframe's `source` says how it was obtained (`TRACKER`, `COMMAND`, `ENGINE`). Tracker-tier keyframes come from births, periodic samples and deaths.

To place a unit at loop L, find the keyframes k0 ≤ L < k1. Then:

1. If there is no k1, or k0 and k1 are at the same spot, stay at k0.
2. Otherwise travel time = distance / (speed / 16) loops (use 2.5 cells/s when speed is 0), and departure = max(k0.loop, k1.loop − travel).
3. Before departure, stay at k0. After it, move linearly to arrive at k1 exactly at k1.loop, facing the direction of travel.

A track may also carry `vitals`: health, shields and energy, each with its maximum (a maximum of 0: the unit has none). Only engine-tier timelines have them, from an engine recording made with them; replays themselves record no hit points. A key holds from its loop until the next. Keys are stored when a value moves by more than 1% of its maximum or a maximum changes (upgrades, morphs), so slow energy regeneration gives a key every 1% or so. Timelines written before vitals existed have none and still read.

`facing` on keyframes is derived from movement (no replay stream records it): radians, counter-clockwise from +x. Exact-source timelines may space keyframes densely, and then this rule reduces to plain linear interpolation.

The reference implementation is `timeline.PositionAt`. Frontends must match it so Go and TypeScript agree.

## Snapshots

`snapshots[i]` is at loop `i * meta.snapshot_interval` (160 by default) and lists the alive unit IDs plus each player's latest stats. To seek to loop L, start from snapshot `L / interval`, apply `unit_born`/`unit_died` events after its loop up to and including L, then evaluate tracks. The cost doesn't depend on L.

## Events

`events` is loop-ordered and holds one of: `unit_born`, `unit_done`, `unit_died` (with `killer` as on the unit), `morph`, `upgrade`, `player_stats` (every 160 loops per player), `chat`, `ping` (a minimap ping: player and point), `command`. Commands carry the raw, build-specific ability link (-1 for a right-click "smart" command) and the target point or unit. Ability names need per-build game data and aren't resolved yet. Commands are left out when the timeline was built with `--no-commands`.

`player_stats` carries the resources lost and killed both as minerals and gas (`lost_minerals`, …) and by kind, minerals and gas together: `lost_army`, `lost_economy` (workers and town halls), `lost_technology` (other buildings and research), and the same for `killed_*`. `friendly_fire` is the value of a player's own units they killed, `in_progress` the value of what they are building or researching.

## Cameras

`cameras` has one entry per player whose camera moved (computer and API players have none): `keys` of loop and the point the camera was centered on, holding until the next key. Scrolling sends an update every few loops; a key is kept when the view jumps or has moved at least a cell since the last. Observers' cameras are left out. Empty when the replay's game events weren't decoded.

## About the game

`meta` says, when the replay records it: `start_time` (Unix seconds, UTC), `map_author` (only a name; Blizzard's maps give a profile handle, which is dropped), `region` (Battle.net games), `ladder` and `competitive` (ranked), and `observers`. Each player may have `selected_race` (the lobby pick, `Random` when random), `handicap`, `clan`, `toon` (Battle.net profile, `region-S2-realm-id`), `mmr`, `apm` (the game's own count), `human`, and for computer players `difficulty` in the lobby's words (`Elite` is the API's `VeryHard`) and `ai_build`.

## Actions

`actions` counts each player's actions, for APM, in bins of `bin_loops` game loops (112, which is 5 s on the clock): `players[].actions[i]` is the number in loops `[i × bin_loops, (i + 1) × bin_loops)`. An action is a command (including the retargets the game records when a player repeats an order), a selection change or a control-group update; camera moves don't count. `commands[i]` counts just the commands. The arrays end at a player's last action, so a player may have fewer bins than the game has.

Counts are kept even when commands are left out of the event feed. `actions` is unset when the timeline was built without the replay's game events (the tracker tier with commands left out), and in timelines written before it was added.

## Fidelity

`meta.fidelity` is the best source used anywhere in the file. A tracker-tier timeline knows exactly when units appear and die, and where buildings stand. It knows where units were only when a sample caught them (mostly in fights) and at death. Frontends should say so.

## Tiers in one file

A timeline can mix keyframe sources. `meta.fidelity` is the best one used:

- **`SOURCE_COMMAND`:** commanded units' tracks are rebuilt from their orders. A keyframe sits at each leg boundary (departure, arrival, every corner of a path), so between two keyframes the unit either holds still or moves in a straight line at its speed. The interpolation rule above gives that motion exactly. Birth, sample and death keyframes keep `SOURCE_TRACKER`: they are observations, and the viewer's "last known position" counts only those (and engine frames). Units of players without recorded orders keep their tracker-tier tracks.
- **`SOURCE_ENGINE`:** units the engine recorded get one keyframe per engine frame (every 16 loops by default) with the real facing, plus their tracker death keyframe.
