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
		if !g.lineClear(cur, q) {
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
