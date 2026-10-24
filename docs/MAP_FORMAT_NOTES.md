# SC2 map format notes

What `internal/mapfile` knows about the files inside a map archive. All of it was worked out by probing real maps (seven `.SC2Map` files and five depot `.s2ma` files: AIE ladder maps, a 2022 ESL map pool, and an older map from mpyq's test suite). Treat anything not listed as unknown. All integers are little-endian. Each binary file starts with a 4-byte tag stored byte-reversed ("MapI" appears as `IpaM`) and a u32 version.

Grid convention used throughout: row-major, row 0 at y = 0 (the bottom of the map), so cell (x, y) is at `y * width + x`. Rendering the grids this way lines up with the start locations in `Objects` and with `Minimap.tga`.

## Archive

Maps are MPQ archives, read with `icza/mpq`. Locked ladder maps may lack `(listfile)`, so every file is opened by its known name. All maps tested opened without compression problems, including mpyq's "last sector compression" depot file.

## `MapInfo`

| Offset | Field |
| --- | --- |
| 0x00 | `IpaM` |
| 0x04 | u32 version (seen: 0x22, 0x27) |
| 0x08 | u32 ×2, unknown (present when version ≥ 0x18) |
| 0x10 | u32 width, u32 height (cells; equals the replay's `mapSizeX/Y`) |
| 0x18 | preview-image settings; the layout varies, ~10–20 bytes |
| … | cstring fog type (e.g. `Dark`), cstring tileset (e.g. `Elsecaro`) |
| … | u32 camera left, bottom, right, top = **playable area** |

The parser scans for the two printable strings followed by bounds inside the map, rather than decoding the preview block. Check: Abyssal Reef gives 24, 4, 176, 140, which is its known 152×136 playable area.

## `t3SyncHeightMap` (gameplay heights)

`SMAP`, version 0x66, u32 w, u32 h (**vertices**: cells + 1 each way), header up to `len − w·h·4` (64 bytes so far). The header contains three u32 4096 values, which are probably the scale. Then one u32 per vertex, value ÷ 4096 = height in game units (flat ground ≈ 8–16). This matches the z of command target points.

`t3HeightMap` (`HMAP`, version 0x65) is the visual height map with 6 bytes per vertex in an unknown layout. It isn't used. The sync map is what the game uses for gameplay.

## `t3SyncCliffLevel`

`CLIF`, version 0x64, u32 w, u32 h (cells), header up to `len − w·h·2` (32 bytes), one u16 per cell. Flat tiers are multiples of 64 (0, 64, 128, 192, 256, 320 seen). Values in between, in steps of 8 (72, 80, … 120), are probably ramps between two tiers. Every map tested follows this pattern.

## `t3CellFlags`

`LFCT`, version 0x66, a 32-byte header with u32 w, h (cells) at 0x18, then one byte per cell.

- **Bit 0** marks cliff-edge cells, which are unpathable. It matches the cliff outlines in the hillshade exactly.
- **Bit 1** is set on almost every cell. Its meaning is unknown.
- **Bits 2–7** were never set in any map tested.

So "pathable" here means inside the playable bounds and bit 0 clear. Rocks, buildings and doodad pathing footprints are not in this grid. The command tier will need them from `Objects` plus footprint data, and possibly `PaintedPathingLayer` (present in some maps, not yet decoded).

`dump-map --layers` writes one PNG per bit, for checking this on new maps.

## `Objects` (XML)

`<PlacedObjects Version="27">` contains three element types:

- `ObjectUnit`: `UnitType`, `Position="x,y,z"`, optional `Rotation` (radians), `Player`, `Variation`. Minerals, geysers, rocks, Xel'Naga towers. Positions follow the footprint convention: a 2×1 mineral field sits at an integer x and a .5 y.
- `ObjectPoint`: `Type` (`StartLoc`, `BlockPathing`, …) and `Name`. Start locations come from here.
- `ObjectDoodad`: decoration, thousands per ladder map. Left out of the JSON export unless `--doodads` is given.

## `Minimap.tga`

Uncompressed true-color TGA (type 2, 24 bpp, descriptor 0x20 = top-left origin). Seen sizes: 256×256, 256×128, 128×256, 1024×1024.

It shows **only the playable area**, at `s = max(imageW, imageH) / 256` pixels per cell, centered in the image. The content box starts at `((imageW − playableW·s)/2, (imageH − playableH·s)/2)`. This held to the pixel on all twelve maps tested. Examples: Curious Minds, 132×124 playable in 256×128, content at (62, 2). Magannatha, 140×140 playable in 1024×1024 (s = 4), content at (232, 232).

It is the user's own Blizzard art: it is only read locally and never committed.

## Export JSON (`dump-map`)

`name, width, height, playable{Left,Bottom,Right,Top}, tileset, startLocations[], objects[]` (units and points), `heights[]` ((w+1)·(h+1) corner heights, 2 decimals), `pathable[]` (w·h, 1 = walkable), `warnings[]`. A hillshade PNG (4 px per cell, playable area highlighted) is written next to it.

## Correction (Phase 2): bit 0 is a band, not "unpathable"

Comparing with the game's own pathing grid (exported by `ghostreel-engine`) showed that bit 0 marks a **band** of cells around each cliff, and the band's outer ring is walkable on both the high and the low side. The cliff itself is the band's interior. `Map.Pathable` now treats a bit-0 cell as unwalkable only when all four of its neighbors also carry bit 0. On Ultralove this blocks 220 of 11,642 walkable cells (1.9%), down from 1,428, and ramps stay open. Some maps also set bit 2 near cliffs; its meaning is unknown.
