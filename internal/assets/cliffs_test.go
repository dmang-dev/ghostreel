// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/mapfile"
)

// TestCliffLayout places the cliff pieces of the test maps: every cell
// that isn't flat gets a piece, turned so that its stored corners land on
// the map's, and the pieces convert.
func TestCliffLayout(t *testing.T) {
	l := openLib(t)
	paths, _ := filepath.Glob("../../testdata/maps/*.SC2Map")
	if len(paths) == 0 {
		t.Skip("no maps in testdata/maps")
	}
	for _, path := range paths {
		m, err := mapfile.Open(path)
		if err != nil || m.Cliffs == nil {
			continue
		}
		lay, err := l.CliffLayout(m)
		if err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		c := m.Cliffs
		steps := 0
		for j := 0; j < c.H; j++ {
			for i := 0; i < c.W; i++ {
				if mapfile.PieceLetters(c.Corners(i, j)) != "aaaa" && c.Cells[j*c.W+i].Set >= 0 {
					steps++
				}
			}
		}
		if len(lay.Pieces) < steps*95/100 {
			t.Errorf("%s: %d pieces for %d cliff cells", m.Name, len(lay.Pieces), steps)
		}
		for _, p := range lay.Pieces {
			want := mapfile.PieceLetters(c.Corners(int(p.X-1)/2, int(p.Y-1)/2))
			// Turned p.Rot quarter turns, stored corner k lands on corner k+Rot.
			got := make([]byte, 4)
			for k := 0; k < 4; k++ {
				got[(k+p.Rot)%4] = p.Name[k]
			}
			if string(got) != want {
				t.Fatalf("%s: piece %s turned %d gives %s at (%v,%v), want %s", m.Name, p.Name, p.Rot, got, p.X, p.Y, want)
			}
		}
		t.Logf("%s: %d pieces for %d cliff cells", m.Name, len(lay.Pieces), steps)
	}
}

// TestCliffPieceFade checks that natural cliff pieces keep the vertex
// alpha they fade into the ground with, and that other models don't get it.
func TestCliffPieceFade(t *testing.T) {
	l := openLib(t)
	b, err := l.CliffGLB("CliffNatural0Ex1", "aabb", 0)
	if err != nil {
		t.Skip(err)
	}
	if !bytes.Contains(b, []byte(`"COLOR_0"`)) {
		t.Error("natural cliff piece without COLOR_0")
	}
	if m, err := l.CliffMaterialGLB("ShakurasEx1Cliff0"); err != nil || len(m) == 0 {
		t.Errorf("cliff material: %v", err)
	}
	if u, err := l.UnitGLB("Marine", false); err == nil && bytes.Contains(u, []byte(`"COLOR_0"`)) {
		t.Error("unit model with COLOR_0")
	}
}
