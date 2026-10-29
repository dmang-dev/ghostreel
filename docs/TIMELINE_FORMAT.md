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

`events` is loop-ordered and holds one of: `unit_born`, `unit_done`, `unit_died`, `morph`, `upgrade`, `player_stats` (every 160 loops per player), `chat`, `command`. Commands carry the raw, build-specific ability link (-1 for a right-click "smart" command) and the target point or unit. Ability names need per-build game data and aren't resolved yet. Commands are left out when the timeline was built with `--no-commands`.

## Fidelity

`meta.fidelity` is the best source used anywhere in the file. A tracker-tier timeline knows exactly when units appear and die, and where buildings stand. It knows where units were only when a sample caught them (mostly in fights) and at death. Frontends should say so.

## Tiers in one file

A timeline can mix keyframe sources. `meta.fidelity` is the best one used:

- **`SOURCE_COMMAND`:** commanded units' tracks are rebuilt from their orders. A keyframe sits at each leg boundary (departure, arrival, every corner of a path), so between two keyframes the unit either holds still or moves in a straight line at its speed. The interpolation rule above gives that motion exactly. Birth, sample and death keyframes keep `SOURCE_TRACKER`: they are observations, and the viewer's "last known position" counts only those (and engine frames). Units of players without recorded orders keep their tracker-tier tracks.
- **`SOURCE_ENGINE`:** units the engine recorded get one keyframe per engine frame (every 16 loops by default) with the real facing, plus their tracker death keyframe.
