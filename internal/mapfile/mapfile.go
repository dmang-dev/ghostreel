// SPDX-License-Identifier: Apache-2.0

// Package mapfile decodes the parts of an SC2 map archive (.SC2Map or a
// depot .s2ma, both MPQ) that a replay viewer needs: dimensions and playable
// bounds, the gameplay height map, cliff levels, cell flags, placed objects
// and the minimap image. The formats are undocumented; what is known about
// them is written up in docs/MAP_FORMAT_NOTES.md.
//
// Grids are row-major with row 0 at y = 0 (the bottom of the map, as in game
// coordinates), so cell (x, y) is at index y*Width + x.
package mapfile

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"

	"github.com/icza/mpq"
)

// Map is a decoded map. Missing optional parts are left empty and listed in
// Warnings rather than failing the whole decode.
type Map struct {
	Name          string // file name without extension
	Width, Height int    // in cells
	Playable      Rect
	Tileset, Fog  string

	// Heights holds terrain heights at cell corners: (Width+1)*(Height+1)
	// values in game units.
	Heights []float32
	// CliffLevels holds one value per cell (Width*Height).
	CliffLevels []uint16
	// CellFlags holds the raw t3CellFlags byte per cell (Width*Height).
	CellFlags []uint8

	Objects        []Object
	StartLocations []Point
	Minimap        image.Image

	Warnings []string
}

// Rect is an axis-aligned area in cells.
type Rect struct {
	Left, Bottom, Right, Top int
}

// Point is a position in game units.
type Point struct{ X, Y float64 }

// ObjectKind tells which Objects element an Object came from.
type ObjectKind string

const (
	ObjectUnit   ObjectKind = "unit"   // minerals, geysers, rocks, towers…
	ObjectDoodad ObjectKind = "doodad" // decoration
	ObjectPoint  ObjectKind = "point"  // start locations, pathing helpers
)

// Object is one entry of the map's Objects file.
type Object struct {
	Kind     ObjectKind
	Type     string // UnitType for units, Type for doodads and points
	Name     string `json:",omitempty"`
	X, Y, Z  float64
	Rotation float64 `json:",omitempty"` // radians
	Player   int     `json:",omitempty"`
}

// Open decodes the map at path.
func Open(path string) (*Map, error) {
	m, err := mpq.NewFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer m.Close()
	mp, err := Decode(m)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	mp.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return mp, nil
}

// ReadFile opens a file from a map archive by name.
type ReadFile interface {
	FileByName(name string) ([]byte, error)
}

// Decode decodes a map from an open archive. Only MapInfo is required.
func Decode(a ReadFile) (*Map, error) {
	get := func(name string) []byte {
		d, err := a.FileByName(name)
		if err != nil {
			return nil
		}
		return d
	}
	info := get("MapInfo")
	if info == nil {
		return nil, errors.New("no MapInfo: not an SC2 map")
	}
	mp := &Map{}
	if err := parseMapInfo(info, mp); err != nil {
		return nil, fmt.Errorf("MapInfo: %w", err)
	}
	warn := func(part string, err error) {
		mp.Warnings = append(mp.Warnings, fmt.Sprintf("%s: %v", part, err))
	}
	if d := get("t3SyncHeightMap"); d == nil {
		warn("t3SyncHeightMap", errors.New("missing"))
	} else if h, err := parseSyncHeightMap(d, mp.Width+1, mp.Height+1); err != nil {
		warn("t3SyncHeightMap", err)
	} else {
		mp.Heights = h
	}
	if d := get("t3SyncCliffLevel"); d == nil {
		warn("t3SyncCliffLevel", errors.New("missing"))
	} else if c, err := parseCliffLevels(d, mp.Width, mp.Height); err != nil {
		warn("t3SyncCliffLevel", err)
	} else {
		mp.CliffLevels = c
	}
	if d := get("t3CellFlags"); d == nil {
		warn("t3CellFlags", errors.New("missing"))
	} else if f, err := parseCellFlags(d, mp.Width, mp.Height); err != nil {
		warn("t3CellFlags", err)
	} else {
		mp.CellFlags = f
	}
	if d := get("Objects"); d == nil {
		warn("Objects", errors.New("missing"))
	} else if objs, err := parseObjects(d); err != nil {
		warn("Objects", err)
	} else {
		mp.Objects = objs
		for _, o := range objs {
			if o.Kind == ObjectPoint && o.Type == "StartLoc" {
				mp.StartLocations = append(mp.StartLocations, Point{o.X, o.Y})
			}
		}
	}
	if d := get("Minimap.tga"); d == nil {
		warn("Minimap.tga", errors.New("missing"))
	} else if img, err := decodeTGA(d); err != nil {
		warn("Minimap.tga", err)
	} else {
		mp.Minimap = img
	}
	return mp, nil
}

// HeightAt returns the height at a cell's center (average of its corners).
func (m *Map) HeightAt(x, y int) float32 {
	if m.Heights == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0
	}
	w := m.Width + 1
	return (m.Heights[y*w+x] + m.Heights[y*w+x+1] + m.Heights[(y+1)*w+x] + m.Heights[(y+1)*w+x+1]) / 4
}

// Pathable reports whether ground units can walk on a cell: inside the
// playable bounds and not a cliff edge. Rocks and doodad blockers are not in
// this grid (they come from Objects); see docs/MAP_FORMAT_NOTES.md.
func (m *Map) Pathable(x, y int) bool {
	if !m.inPlayable(x, y) {
		return false
	}
	if m.CellFlags == nil {
		return true
	}
	return m.CellFlags[y*m.Width+x]&flagUnpathable == 0
}

// flagUnpathable (bit 0) marks cliff-edge cells. Bit 1 is set on nearly every
// cell of the maps studied; bits 2-7 were never set.
const flagUnpathable = 0x01
