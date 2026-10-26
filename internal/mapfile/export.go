// SPDX-License-Identifier: Apache-2.0

package mapfile

import "math"

// Export is the JSON shape frontends load (see docs/MAP_FORMAT_NOTES.md).
// Grids keep the row-0-at-the-bottom layout of Map.
type Export struct {
	Name     string `json:"name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Playable Rect   `json:"playable"`
	Tileset  string `json:"tileset"`
	// TerrainTextures are the terrain's texture layers, base first.
	TerrainTextures []string `json:"terrainTextures,omitempty"`
	// TerrainMasks describes the painted texture layers, which the API
	// serves as raw planes (TextureMasks.Planes); nil when there are none.
	TerrainMasks   *MaskInfo `json:"terrainMasks,omitempty"`
	StartLocations []Point   `json:"startLocations"`
	Objects        []Object  `json:"objects"`
	// Heights at cell corners, (width+1)*(height+1), rounded to 1/100.
	Heights []float64 `json:"heights,omitempty"`
	// Pathable is 1 per walkable cell, width*height.
	Pathable []uint8  `json:"pathable,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// MaskInfo is the size of the texture masks in texels and how the layer
// textures tile: repeats across the map (x, y), then offsets in cells.
type MaskInfo struct {
	Width  int        `json:"width"`
	Height int        `json:"height"`
	Tiling [4]float64 `json:"tiling"`
}

// Export builds the frontend view of the map. Doodads are left out unless
// asked for; ladder maps have thousands and the 2D view doesn't draw them.
func (m *Map) Export(withDoodads bool) *Export {
	e := &Export{
		Name: m.Name, Width: m.Width, Height: m.Height, Playable: m.Playable, Tileset: m.Tileset,
		StartLocations: m.StartLocations, Warnings: m.Warnings, TerrainTextures: m.TerrainTextures,
		Objects: []Object{},
	}
	if m.Masks != nil {
		e.TerrainMasks = &MaskInfo{Width: m.Masks.W, Height: m.Masks.H, Tiling: m.GroundTiling()}
	}
	for _, o := range m.Objects {
		if o.Kind != ObjectDoodad || withDoodads {
			e.Objects = append(e.Objects, o)
		}
	}
	if m.Heights != nil {
		e.Heights = make([]float64, len(m.Heights))
		for i, h := range m.Heights {
			e.Heights[i] = math.Round(float64(h)*100) / 100
		}
	}
	if m.CellFlags != nil {
		e.Pathable = make([]uint8, len(m.CellFlags))
		for i := range m.CellFlags {
			if m.Pathable(i%m.Width, i/m.Width) {
				e.Pathable[i] = 1
			}
		}
	}
	return e
}
