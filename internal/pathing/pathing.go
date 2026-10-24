// SPDX-License-Identifier: Apache-2.0

// Package pathing finds ground paths over a map's walkable cells: A* on an
// 8-connected grid (no corner cutting), shortened to the waypoints where the
// path turns. It is used by the command tier to move units around cliffs
// instead of through them.
package pathing

import (
	"container/heap"
	"math"
)

// Point is a position in cells.
type Point struct{ X, Y float64 }

// Grid marks walkable cells. Cell (x, y) covers [x, x+1) × [y, y+1).
type Grid struct {
	W, H int
	walk []bool
	base []bool // terrain walkability before structures (for Unblock)
	// cache of recent cell-to-cell paths; bot games repeat the same moves.
	cache map[[4]int][]Point
}

// New returns a grid of the given size with every cell walkable.
func New(w, h int) *Grid {
	g := &Grid{W: w, H: h, walk: make([]bool, w*h), cache: map[[4]int][]Point{}}
	for i := range g.walk {
		g.walk[i] = true
	}
	g.base = append([]bool(nil), g.walk...)
	return g
}

// Set marks one cell of the terrain.
func (g *Grid) Set(x, y int, walkable bool) {
	if x >= 0 && y >= 0 && x < g.W && y < g.H {
		g.walk[y*g.W+x] = walkable
		g.base[y*g.W+x] = walkable
		g.cache = map[[4]int][]Point{}
	}
}

// Block marks a rectangle of cells unwalkable, given its center and size in
// cells (a structure footprint).
func (g *Grid) Block(cx, cy float64, w, h int) {
	x0 := int(math.Floor(cx - float64(w)/2 + 0.01))
	y0 := int(math.Floor(cy - float64(h)/2 + 0.01))
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			g.Set(x, y, false)
		}
	}
}

// Walkable reports whether a cell can be walked on.
func (g *Grid) Walkable(x, y int) bool {
	return x >= 0 && y >= 0 && x < g.W && y < g.H && g.walk[y*g.W+x]
}

// Path returns waypoints from a to b, excluding a and ending at b (or at the
// nearest walkable cell to b). It returns just [b] when the straight line is
// clear or no path exists.
func (g *Grid) Path(a, b Point) []Point {
	if g.lineClear(a, b) {
		return []Point{b}
	}
	sx, sy, ok1 := g.nearestWalkable(int(a.X), int(a.Y))
	gx, gy, ok2 := g.nearestWalkable(int(b.X), int(b.Y))
	if !ok1 || !ok2 {
		return []Point{b}
	}
	key := [4]int{sx, sy, gx, gy}
	cells, ok := g.cache[key]
	if !ok {
		cells = g.astar(sx, sy, gx, gy)
		if len(g.cache) > 4096 {
			g.cache = map[[4]int][]Point{}
		}
		g.cache[key] = cells
	}
	if cells == nil {
		return []Point{b}
	}
	end := b
	if !g.Walkable(int(b.X), int(b.Y)) {
		end = Point{float64(gx) + 0.5, float64(gy) + 0.5}
	}
	// String-pull: from the current point, jump to the farthest visible waypoint.
	out := []Point{}
	cur := a
	pts := append(append([]Point(nil), cells...), end)
	for i := 0; i < len(pts); {
		j := len(pts) - 1
		for j > i && !g.lineClear(cur, pts[j]) {
			j--
		}
		out = append(out, pts[j])
		cur = pts[j]
		i = j + 1
	}
	return out
}

// lineClear walks the cells a segment crosses (supercover DDA).
func (g *Grid) lineClear(a, b Point) bool {
	x, y := int(math.Floor(a.X)), int(math.Floor(a.Y))
	ex, ey := int(math.Floor(b.X)), int(math.Floor(b.Y))
	dx, dy := b.X-a.X, b.Y-a.Y
	stepX, stepY := 1, 1
	if dx < 0 {
		stepX = -1
	}
	if dy < 0 {
		stepY = -1
	}
	tDeltaX, tDeltaY := math.Inf(1), math.Inf(1)
	tMaxX, tMaxY := math.Inf(1), math.Inf(1)
	if dx != 0 {
		tDeltaX = math.Abs(1 / dx)
		next := math.Floor(a.X)
		if stepX > 0 {
			next++
		}
		tMaxX = (next - a.X) / dx
	}
	if dy != 0 {
		tDeltaY = math.Abs(1 / dy)
		next := math.Floor(a.Y)
		if stepY > 0 {
			next++
		}
		tMaxY = (next - a.Y) / dy
	}
	// The start cell may be blocked (a unit standing at a building's edge);
	// only cells after it must be walkable.
	for n := 0; n < 4*(g.W+g.H); n++ {
		if x == ex && y == ey {
			return g.Walkable(x, y) || n == 0
		}
		if tMaxX < tMaxY {
			tMaxX += tDeltaX
			x += stepX
		} else {
			tMaxY += tDeltaY
			y += stepY
		}
		if !g.Walkable(x, y) {
			return false
		}
	}
	return true
}

// nearestWalkable searches outward in rings for a walkable cell.
func (g *Grid) nearestWalkable(x, y int) (int, int, bool) {
	if g.Walkable(x, y) {
		return x, y, true
	}
	for r := 1; r < 12; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if max(abs(dx), abs(dy)) == r && g.Walkable(x+dx, y+dy) {
					return x + dx, y + dy, true
				}
			}
		}
	}
	return 0, 0, false
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

type node struct {
	i    int
	f, g float64
}

type openSet []node

func (o openSet) Len() int            { return len(o) }
func (o openSet) Less(i, j int) bool  { return o[i].f < o[j].f }
func (o openSet) Swap(i, j int)       { o[i], o[j] = o[j], o[i] }
func (o *openSet) Push(x interface{}) { *o = append(*o, x.(node)) }
func (o *openSet) Pop() interface{} {
	old := *o
	n := old[len(old)-1]
	*o = old[:len(old)-1]
	return n
}

// astar returns cell centers from start (exclusive) to goal (inclusive), or
// nil when unreachable.
func (g *Grid) astar(sx, sy, gx, gy int) []Point {
	start, goal := sy*g.W+sx, gy*g.W+gx
	if start == goal {
		return []Point{{float64(gx) + 0.5, float64(gy) + 0.5}}
	}
	h := func(i int) float64 {
		dx := math.Abs(float64(i%g.W - gx))
		dy := math.Abs(float64(i/g.W - gy))
		return dx + dy + (math.Sqrt2-2)*math.Min(dx, dy) // octile
	}
	gScore := map[int]float64{start: 0}
	from := map[int]int{}
	open := &openSet{{i: start, f: h(start)}}
	closed := map[int]bool{}
	for open.Len() > 0 {
		cur := heap.Pop(open).(node)
		if cur.i == goal {
			var cells []Point
			for i := goal; i != start; i = from[i] {
				cells = append(cells, Point{float64(i%g.W) + 0.5, float64(i/g.W) + 0.5})
			}
			for l, r := 0, len(cells)-1; l < r; l, r = l+1, r-1 {
				cells[l], cells[r] = cells[r], cells[l]
			}
			return cells
		}
		if closed[cur.i] {
			continue
		}
		closed[cur.i] = true
		cx, cy := cur.i%g.W, cur.i/g.W
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				nx, ny := cx+dx, cy+dy
				if !g.Walkable(nx, ny) {
					continue
				}
				// No cutting corners past blocked cells.
				if dx != 0 && dy != 0 && (!g.Walkable(cx+dx, cy) || !g.Walkable(cx, cy+dy)) {
					continue
				}
				cost := 1.0
				if dx != 0 && dy != 0 {
					cost = math.Sqrt2
				}
				ni := ny*g.W + nx
				ng := cur.g + cost
				if old, ok := gScore[ni]; ok && ng >= old {
					continue
				}
				gScore[ni] = ng
				from[ni] = cur.i
				heap.Push(open, node{i: ni, g: ng, f: ng + h(ni)})
			}
		}
	}
	return nil
}

// footprint calls fn for each cell of a w×h footprint centered at (cx, cy).
func (g *Grid) footprint(cx, cy float64, w, h int, fn func(i int)) {
	x0 := int(math.Floor(cx - float64(w)/2 + 0.01))
	y0 := int(math.Floor(cy - float64(h)/2 + 0.01))
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			if x >= 0 && y >= 0 && x < g.W && y < g.H {
				fn(y*g.W + x)
			}
		}
	}
	g.cache = map[[4]int][]Point{}
}

// Occupy blocks a structure's footprint (it can be cleared with Vacate).
func (g *Grid) Occupy(cx, cy float64, w, h int) {
	g.footprint(cx, cy, w, h, func(i int) { g.walk[i] = false })
}

// Vacate clears a structure's footprint back to the terrain underneath.
func (g *Grid) Vacate(cx, cy float64, w, h int) {
	g.footprint(cx, cy, w, h, func(i int) { g.walk[i] = g.base[i] })
}

// Reachable reports whether a path exists between two points.
func (g *Grid) Reachable(a, b Point) bool {
	if g.lineClear(a, b) {
		return true
	}
	sx, sy, ok1 := g.nearestWalkable(int(a.X), int(a.Y))
	gx, gy, ok2 := g.nearestWalkable(int(b.X), int(b.Y))
	if !ok1 || !ok2 {
		return false
	}
	key := [4]int{sx, sy, gx, gy}
	cells, ok := g.cache[key]
	if !ok {
		cells = g.astar(sx, sy, gx, gy)
		g.cache[key] = cells
	}
	return cells != nil
}
