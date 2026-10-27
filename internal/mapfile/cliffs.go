// SPDX-License-Identifier: Apache-2.0

package mapfile

import (
	"errors"
	"fmt"
)

// Cliffs is where the game draws cliff models. The terrain is split into
// cliff cells of 2×2 cells; t3Terrain.xml gives each one a cliff set (a
// CCliff id, through <cliffSetList>) and a variation (<cliffCellList>), and
// the cliff levels at its four corners (from t3HeightMap) pick the piece.
// See docs/MAP_FORMAT_NOTES.md.
type Cliffs struct {
	Sets []string // CCliff ids, by CliffCell.Set
	W, H int      // in cliff cells
	// Cells is W*H, row 0 at the bottom of the map.
	Cells []CliffCell
	// Levels is the cliff level at each cell corner, (2W+1)*(2H+1), row 0
	// at the bottom; a cliff cell's corners are every second vertex.
	Levels []uint8
}

// CliffCell is one cliff cell's cliff set and variation.
type CliffCell struct {
	Set   int // index into Cliffs.Sets; -1 for none
	Var   int // which numbered variation of the piece
	Flags int // t3Terrain.xml's f, not understood
}

func newCliffs(w, h int, t terrainXML, levels []uint8) (*Cliffs, error) {
	if len(t.cliffSets) == 0 {
		return nil, nil // no cliffs
	}
	if len(levels) != (w+1)*(h+1) {
		return nil, errors.New("cliff levels don't cover the map")
	}
	if w%2 != 0 || h%2 != 0 {
		return nil, fmt.Errorf("map %dx%d doesn't split into cliff cells", w, h)
	}
	c := &Cliffs{Sets: t.cliffSets, W: w / 2, H: h / 2, Levels: levels}
	c.Cells = make([]CliffCell, c.W*c.H)
	for i := range c.Cells {
		c.Cells[i].Set = -1
	}
	for _, cc := range t.cliffCells {
		if cc.I < 0 || cc.I >= len(c.Cells) || cc.Cid < 0 || cc.Cid >= len(c.Sets) {
			continue
		}
		c.Cells[cc.I] = CliffCell{Set: cc.Cid, Var: cc.Cvar, Flags: cc.F}
	}
	return c, nil
}

// Corners returns the cliff levels at cliff cell (i, j)'s corners, in the
// order cliff pieces name them: south-west, south-east, north-east,
// north-west.
func (c *Cliffs) Corners(i, j int) [4]int {
	vw := 2*c.W + 1
	at := func(x, y int) int { return int(c.Levels[y*vw+x]) }
	x, y := 2*i, 2*j
	return [4]int{at(x, y), at(x+2, y), at(x+2, y+2), at(x, y+2)}
}

// PieceLetters names a cell's shape the way cliff piece files do: a letter
// per corner (south-west, south-east, north-east, north-west), "a" for the
// lowest level, "b" one above, and so on. Flat cells are "aaaa".
func PieceLetters(levels [4]int) string {
	lo := min(levels[0], levels[1], levels[2], levels[3])
	b := make([]byte, 4)
	for i, l := range levels {
		b[i] = byte('a' + min(l-lo, 25))
	}
	return string(b)
}
