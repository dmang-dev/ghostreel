// SPDX-License-Identifier: Apache-2.0

package pathing

import (
	"math"
	"testing"
)

// A wall at x=5 from y=0..8 with a gap at the top (y=9).
func walled() *Grid {
	g := New(10, 10)
	for y := 0; y < 9; y++ {
		g.Set(5, y, false)
	}
	return g
}

func TestStraightWhenClear(t *testing.T) {
	g := New(10, 10)
	p := g.Path(Point{1, 1}, Point{8, 8})
	if len(p) != 1 || p[0] != (Point{8, 8}) {
		t.Fatalf("path = %v, want straight to the goal", p)
	}
}

func TestAroundWall(t *testing.T) {
	g := walled()
	a, b := Point{2.5, 1.5}, Point{8.5, 1.5}
	p := g.Path(a, b)
	if p[len(p)-1] != b {
		t.Fatalf("path ends at %v, want %v", p[len(p)-1], b)
	}
	// Every leg must be walkable, and the path must go through the gap.
	cur := a
	top := 0.0
	for _, q := range p {
		if !g.lineClear(cur, q, Small) {
			t.Fatalf("leg %v -> %v crosses the wall", cur, q)
		}
		top = math.Max(top, q.Y)
		cur = q
	}
	if top < 9 {
		t.Fatalf("path %v never reaches the gap", p)
	}
	if len(p) > 4 {
		t.Errorf("path not smoothed: %v", p)
	}
}

func TestUnreachableFallsBackToGoal(t *testing.T) {
	g := walled()
	g.Set(5, 9, false) // close the gap
	b := Point{8.5, 1.5}
	if p := g.Path(Point{2.5, 1.5}, b); len(p) != 1 || p[0] != b {
		t.Fatalf("path = %v, want just the goal", p)
	}
}

func TestBlockFootprint(t *testing.T) {
	g := New(10, 10)
	g.Block(5, 5, 2, 2) // even footprint centered on a corner: cells 4..5
	for _, c := range [][2]int{{4, 4}, {5, 5}, {4, 5}, {5, 4}} {
		if g.Walkable(c[0], c[1]) {
			t.Errorf("cell %v should be blocked", c)
		}
	}
	if !g.Walkable(3, 4) || !g.Walkable(6, 5) {
		t.Error("neighbors blocked")
	}
	g.Block(2.5, 7.5, 3, 3) // odd footprint centered on a cell: cells 1..3
	if g.Walkable(1, 6) || g.Walkable(3, 8) || !g.Walkable(4, 7) {
		t.Error("3x3 footprint wrong")
	}
}

func TestCliffLevelsBlockSteps(t *testing.T) {
	// Two plateaus (levels 64 and 128) meeting along x=5, joined by a ramp
	// cell at the top row.
	g := New(10, 10)
	lv := make([]uint16, 100)
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			lv[y*10+x] = 64
			if x >= 5 {
				lv[y*10+x] = 128
			}
		}
	}
	lv[9*10+5] = 96 // ramp
	g.SetLevels(lv)
	p := g.Path(Point{2.5, 1.5}, Point{8.5, 1.5})
	top := 0.0
	for _, q := range p {
		top = math.Max(top, q.Y)
	}
	if top < 9 {
		t.Fatalf("path %v crossed the cliff instead of using the ramp", p)
	}
}

func TestBigUnitsNeedRoom(t *testing.T) {
	// A wall at x=5 with a one-cell gap at y=5 and a two-cell gap at y=8..9.
	g := New(10, 12)
	for y := 0; y < 12; y++ {
		if y != 5 && y != 8 && y != 9 {
			g.Set(5, y, false)
		}
	}
	a, b := Point{2.5, 5.5}, Point{8.5, 5.5}
	small := g.PathFor(a, b, Small)
	if len(small) != 1 {
		t.Errorf("small unit should go straight through the gap: %v", small)
	}
	big := g.PathFor(a, b, Big)
	top := 0.0
	for _, q := range big {
		top = math.Max(top, q.Y)
	}
	if top < 8 {
		t.Errorf("big unit squeezed through the 1-cell gap: %v", big)
	}
	g.Set(5, 8, false) // now the wide gap is only 1 cell too
	if g.ReachableFor(a, b, Big) {
		t.Error("big unit should be walled in")
	}
}

func TestHugeUnitsNeedMoreRoom(t *testing.T) {
	// A diagonal wall (x+y = 12) with a hole one cell wide at (6, 6).
	g := New(14, 14)
	for x := 0; x <= 12; x++ {
		if x != 6 {
			g.Set(x, 12-x, false)
		}
	}
	a, b := Point{3, 3}, Point{10, 10}
	if !g.ReachableFor(a, b, Big) {
		t.Error("a big unit fits through the diagonal hole (1.4 cells)")
	}
	if g.ReachableFor(a, b, Huge) {
		t.Error("a huge unit should not fit through the diagonal hole")
	}
	g.Set(7, 5, true) // widen the hole to two cells (2.8 cells across)
	if !g.ReachableFor(a, b, Huge) {
		t.Fatal("a huge unit fits through the wider hole")
	}
	// The path runs through the hole and keeps clear of the wall.
	path := g.PathFor(a, b, Huge)
	cur := a
	for _, q := range path {
		if !g.lineClear(cur, q, Huge) {
			t.Errorf("segment %v-%v is not clear", cur, q)
		}
		cur = q
	}
	if cur != b {
		t.Errorf("path ends at %v, want %v", cur, b)
	}
	// An axis-aligned two-cell gap is wide enough for a huge unit.
	g = New(10, 10)
	for y := 0; y < 10; y++ {
		if y != 4 && y != 5 {
			g.Set(5, y, false)
		}
	}
	if !g.ReachableFor(Point{2, 5}, Point{8, 5}, Huge) {
		t.Error("a huge unit fits through a straight two-cell gap")
	}
}

func TestStartOnWallTakesGoalSide(t *testing.T) {
	g := New(10, 10)
	for y := 0; y < 10; y++ {
		g.Set(5, y, false)
	}
	// Standing on the wall itself, the unit belongs to the goal's side.
	if !g.ReachableFor(Point{5.5, 5.5}, Point{8.5, 5.5}, Small) {
		t.Error("right side should be reachable from the wall")
	}
	if !g.ReachableFor(Point{5.5, 5.5}, Point{1.5, 5.5}, Small) {
		t.Error("left side should be reachable from the wall")
	}
}
