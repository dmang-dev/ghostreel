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
| … | cstring fog type (e.g. `Dark`), cstring terrain type (e.g. `Elsecaro`, a `CTerrain` id) |
| … | u32 camera left, bottom, right, top = **playable area** |
| … | optional cstring: the loading screen image, a game-data path (`Assets\Textures\….dds`); absent for the default |

The parser scans for the two printable strings followed by bounds inside the map, rather than decoding the preview block. Check: Abyssal Reef gives 24, 4, 176, 140, which is its known 152×136 playable area.

The terrain type names the map's lighting: `CTerrain` has a `Lighting` field (default `##id##`, the terrain's own id) naming a `CLight`. Usually the two match (`HybridLabXelNaga`, `Elsecaro`); Ultralove's `RedstoneEx1` uses `Avernus` in 4.10 and `Redstone` in later versions. A map may define its own terrain types and adjust lights in `Base.SC2Data\GameData\TerrainData.xml` and `LightData.xml`, which apply over the game's. A `CLight`'s first `ToDInfoArray` holds `AmbientColor` and three `DirectionalLight`s (`Key`, `Fill`, `Back`) whose `Color`, `ColorMultiplier` and `Direction` appear as attributes or as child elements.

## `DocumentHeader`

The map's title and description, in each language the map has. After a header and the dependency list (`bnet:Void (Mod)/0.0/999,file:Mods/Void.SC2Mod`) come string entries:

| Field | |
| --- | --- |
| u16 + bytes | key, e.g. `DocInfo/Name`, `DocInfo/DescLong`, `DocInfo/DescShort` (`1v1`, or a player count) |
| 4 bytes | locale, reversed (`SUne` is enUS) |
| u16 + bytes | UTF-8 value |

The parser scans for keys rather than decoding the header. Values use the game's text markup: `<n/>` is a line break; other tags (colors, images) are dropped. Maps may also carry screenshots as JPEG files at the archive root (`Screenshot-01.jpg`); the first is used as the map's picture.

## `t3SyncHeightMap` (gameplay heights)

`SMAP`, version 0x66, u32 w, u32 h (**vertices**: cells + 1 each way), header up to `len − w·h·4` (64 bytes so far). The header contains three u32 4096 values. Then one u32 per vertex, value ÷ 32768 = height in game units **above an unrecorded base** (2 units per cliff level). (Phase 0 read ÷ 4096, which made terrain 8 times too tall; fixed in Phase 3.) The base comes from the doodads: they are placed at absolute heights, most of them on the ground, so the most common gap between a doodad's height and the sync map is the base (when at least a quarter of 50 or more doodads agree). Otherwise it comes from the visual height map below. Command target points then match within about 0.05 on every map checked. The visual height map alone is off by a constant on some maps (Ultralove, by 5.9).

`t3HeightMap` (`HMAP`, version 0x65) is the visual height map: a 32-byte header, then 6 bytes per vertex: u16 (unknown), u16 quantized height, u16 cliff level. Height = q × `quantizeScale` + `standardHeight` − `quantizeBias`, from `<vertData>` in `t3Terrain.xml`. It is used to find the sync map's base (the sync map is what the game uses for gameplay) and for the cliff levels (see Cliffs).

## `t3Terrain.xml` and `t3TextureMasks`

`<heightMap tileSet=…>` holds `<vertData>` (above) and `<textureList>`, 64 `CTerrainTex` ids, 8 per texture set; set 0 is the terrain's own. `t3TextureMasks` (`MASK`, version 0x66, 64-byte header; u32 at 16 = texel height) holds 8 layers, one weight byte per texel. Each layer is 4 texels per cell across and 8 up. The weights of a texel sum to 255, and layer *i* paints texture *i* of `textureList`. A layer is stored as 32×32-byte tiles in strips two tiles high: tile *t* of a strip is column *t*/2, row *t*%2. Strips run left to right; strip rows run from the bottom of the map up; layers follow one another. (The u32 at 12 is twice the layer width.) Textures repeat across the map as `uvtiling` on `<heightMap>` says: x and y repeats, then offsets. (Maps without `uvtiling` are drawn with a repeat every 32/3 cells.) A layer texture's alpha channel is the surface's height (for blending layers by height), not coverage. The API serves the layers as `GET /api/r/{id}/masks`: gzip of two RGBA planes of the mask's size (layers 0–3, then 4–7), rows from the bottom of the map; the map JSON's `terrainMasks` gives the size and tiling.

## Cliffs (`t3Terrain.xml`, `t3HeightMap`, the game's cliff data)

The game draws cliffs as models, one piece per **cliff cell** of 2×2 cells:

- `t3Terrain.xml`'s `<cliffSetList>` names the map's cliff sets (`CCliff` ids, such as `ProtossBaseManmadeCliffs`), and `<cliffCellList num=…>` has a `<cc i= f= cid= cvar=/>` per painted cliff cell: `i` is the cell index (row-major, rows from the bottom of the map), `cid` the cliff set, `cvar` the piece variation, and `f` flags not understood. A 248×248 map has 124×124 cliff cells.
- The cliff level at each vertex is the third u16 of `t3HeightMap` (0–3 on the maps seen). It follows the ground: on Pylon AIE every vertex of level *L* ≥ 1 is at height 2(*L* − 1). A cliff cell's corners are every second vertex.
- In the game data, `CliffData.xml`'s `CCliff` gives the set's `CliffMesh` (a `CCliffMesh` id; `ShakurasEx1Cliff0` uses `CliffNatural0Ex1`) and `CliffMaterial` (default `Assets\Cliffs\##id##_Material\##id##_Material.m3`, a small model whose material the pieces are drawn with). `CliffMeshData.xml`'s `CCliffMesh` gives `ModelPath` (`Assets\Cliffs\##id##\`) and `CliffHeights` by index.
- Pieces are `<mesh>_<corners>_<nn>.m3`, the corners lettered south-west, south-east, north-east, north-west, "a" for the cell's lowest level ("cliffnatural0ex1_aaab_02.m3"). Only one rotation of each shape is stored (the smallest in letter order), so a cell takes the stored shape turned by quarter turns. Pieces are 2 units across (−1 to 1) and in world units vertically: letter *k* stands at `CliffHeights[k]` − `CliffHeights[0]` (0, 8, 10, 12 with the usual −8, 0, 2, 4), so "b" is 8 above "a". The piece's "b" goes at the ground height of the level above the cell's lowest; the wall runs on 6 units below the lower ground, hidden, except where the ground falls away into space.
- Natural cliff pieces carry vertex colors whose alpha fades the piece into the ground: 0 along the top, 255 on the wall. Manmade pieces have none and are opaque.

The API places the pieces (`GET /api/r/{id}/cliffs`) and serves them as glTF (`/api/assets/cliff/{mesh}/{corners}?v=`, `/api/assets/cliffmaterial/{set}`); this needs the game data as well as the map.

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

- `ObjectUnit`: `UnitType`, `Position="x,y,z"`, optional `Rotation` (radians), `Player`, `Variation`, `Resources`. Minerals, geysers, rocks, Xel'Naga towers. Positions follow the footprint convention: a 2×1 mineral field sits at an integer x and a .5 y. `Resources` overrides how much a mineral field or geyser holds (Ley Lines' gold-rimmed fields hold 150); without it the amount is the type's, from its resource behavior's `Contents` in the game data.
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
