// SPDX-License-Identifier: Apache-2.0

package mapfile

import "testing"

func TestPieceLetters(t *testing.T) {
	for _, c := range []struct {
		levels [4]int
		want   string
	}{
		{[4]int{1, 1, 1, 1}, "aaaa"},
		{[4]int{1, 1, 1, 2}, "aaab"},
		{[4]int{2, 1, 1, 2}, "baab"},
		{[4]int{0, 1, 2, 3}, "abcd"},
	} {
		if got := PieceLetters(c.levels); got != c.want {
			t.Errorf("%v: %s, want %s", c.levels, got, c.want)
		}
	}
}

func TestCliffCorners(t *testing.T) {
	// A 4x2 map: two cliff cells; vertex levels 5 across, 3 up, row 0 at the bottom.
	levels := []uint8{
		0, 0, 1, 1, 1,
		0, 0, 1, 1, 1,
		0, 0, 2, 2, 1,
	}
	c, err := newCliffs(4, 2, terrainXML{cliffSets: []string{"Natural"}, cliffCells: []cliffCellXML{{I: 1, Cid: 0, Cvar: 3}}}, levels)
	if err != nil {
		t.Fatal(err)
	}
	if c.W != 2 || c.H != 1 || c.Cells[0].Set != -1 || c.Cells[1] != (CliffCell{Set: 0, Var: 3}) {
		t.Fatalf("cells %+v", c.Cells)
	}
	// South-west, south-east, north-east, north-west.
	if got := c.Corners(0, 0); got != [4]int{0, 1, 2, 0} {
		t.Errorf("corners (0,0) %v", got)
	}
	if got := c.Corners(1, 0); got != [4]int{1, 1, 1, 2} {
		t.Errorf("corners (1,0) %v", got)
	}
	if c, err := newCliffs(4, 2, terrainXML{}, levels); c != nil || err != nil {
		t.Errorf("no cliff sets: %v %v", c, err)
	}
}

// TestRealCliffs checks the cliff grid against the terrain on real maps:
// cliff levels follow the ground heights.
func TestRealCliffs(t *testing.T) {
	for _, path := range realMaps(t) {
		m, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		c := m.Cliffs
		if c == nil {
			continue
		}
		// Each level's heights cluster: within a level, heights vary less
		// than the 2 units between levels.
		lo, hi := map[uint8]float32{}, map[uint8]float32{}
		for i, l := range c.Levels {
			h := m.Heights[i]
			if v, ok := lo[l]; !ok || h < v {
				lo[l] = h
			}
			if v, ok := hi[l]; !ok || h > v {
				hi[l] = h
			}
		}
		for l := range lo {
			if l > 0 && hi[l]-lo[l] > 1.5 {
				t.Errorf("%s: level %d spans heights %.1f..%.1f", m.Name, l, lo[l], hi[l])
			}
		}
		steps := 0
		for j := 0; j < c.H; j++ {
			for i := 0; i < c.W; i++ {
				if PieceLetters(c.Corners(i, j)) != "aaaa" {
					steps++
				}
			}
		}
		t.Logf("%s: %v, %d of %d cliff cells not flat", m.Name, c.Sets, steps, c.W*c.H)
	}
}
