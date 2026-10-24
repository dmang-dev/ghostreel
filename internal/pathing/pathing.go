// SPDX-License-Identifier: Apache-2.0

// Package pathing finds ground paths over a map's walkable cells: A* on an
// 8-connected grid (no corner cutting, no steps across cliff levels),
// shortened to the waypoints where the path turns. It is used by the command
// tier to move units around cliffs and buildings instead of through them.
//
// Big units (radius over half a cell) need room: for them a cell is passable
// only if some 2×2 block of walkable cells contains it, so they don't squeeze
// through one-cell gaps between buildings.
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
	base []bool // terrain walkability before structures (for Vacate)
	// levels are optional cliff levels per cell; see SetLevels
	levels []uint16
	// cache of recent cell-to-cell paths; bot games repeat the same moves.
	cache map[pathKey]cachedPath

	// version changes whenever walkability does; connected components (for
	// cheap reachability) are recomputed lazily per unit size.
	version     uint64
	comp        [2][]int32
	compVersion [2]uint64

	// A* scratch buffers, reused between searches (stamp marks valid entries).
	gs    []float64
	from  []int32
	seen  []uint32
	done  []uint32
	stamp uint32
}

type pathKey struct {
	sx, sy, gx, gy int
	big            bool
}

// New returns a grid of the given size with every cell walkable.
func New(w, h int) *Grid {
	g := &Grid{W: w, H: h, walk: make([]bool, w*h), cache: map[pathKey]cachedPath{}}
	for i := range g.walk {
		g.walk[i] = true
	}
	g.base = append([]bool(nil), g.walk...)
	return g
}

// clearCache marks the grid changed. Cached paths are kept and revalidated
// on use (structures appear and vanish constantly; most paths survive).
func (g *Grid) clearCache() { g.version++ }

type cachedPath struct {
	cells   []Point
	version uint64
}

// stillPassable reports whether every cell of a cached path is still open.
func (g *Grid) stillPassable(cells []Point, big bool) bool {
	for _, c := range cells {
		if !g.passable(int(c.X), int(c.Y), big) {
			return false
		}
	}
	return true
}

// Set marks one cell of the terrain.
func (g *Grid) Set(x, y int, walkable bool) {
	if x >= 0 && y >= 0 && x < g.W && y < g.H {
		g.walk[y*g.W+x] = walkable
		g.base[y*g.W+x] = walkable
		g.clearCache()
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

// passable is Walkable for a unit of the given size class.
func (g *Grid) passable(x, y int, big bool) bool {
	if !g.Walkable(x, y) {
		return false
	}
	if !big {
		return true
	}
	for oy := -1; oy <= 0; oy++ {
		for ox := -1; ox <= 0; ox++ {
			if g.Walkable(x+ox, y+oy) && g.Walkable(x+ox+1, y+oy) && g.Walkable(x+ox, y+oy+1) && g.Walkable(x+ox+1, y+oy+1) {
				return true
			}
		}
	}
	return false
}

// Path returns waypoints from a to b for a small unit; see PathFor.
func (g *Grid) Path(a, b Point) []Point { return g.PathFor(a, b, false) }

// Reachable reports whether a small unit can get from a to b.
func (g *Grid) Reachable(a, b Point) bool { return g.ReachableFor(a, b, false) }

// PathFor returns waypoints from a to b, excluding a and ending at b (or at
// the nearest passable cell to b). It returns just [b] when the straight line
// is clear or no path exists. big selects the 2×2 clearance rule.
func (g *Grid) PathFor(a, b Point, big bool) []Point {
	if g.lineClear(a, b, big) {
		return []Point{b}
	}
	cells, gx, gy, ok := g.cells(a, b, big)
	if !ok || cells == nil {
		return []Point{b}
	}
	end := b
	if !g.passable(int(b.X), int(b.Y), big) {
		end = Point{float64(gx) + 0.5, float64(gy) + 0.5}
	}
	// String-pull: from the current point, jump to the farthest visible waypoint.
	out := []Point{}
	cur := a
	pts := append(append([]Point(nil), cells...), end)
	for i := 0; i < len(pts); {
		j := len(pts) - 1
		for j > i && !g.lineClear(cur, pts[j], big) {
			j--
		}
		out = append(out, pts[j])
		cur = pts[j]
		i = j + 1
	}
	return out
}

// ReachableFor reports whether a path exists between two points: both lie
// (after snapping to the nearest passable cell) in the same connected region.
func (g *Grid) ReachableFor(a, b Point, big bool) bool {
	if g.lineClear(a, b, big) {
		return true
	}
	sx, sy, ok1 := g.nearestPassable(int(a.X), int(a.Y), big)
	gx, gy, ok2 := g.nearestPassable(int(b.X), int(b.Y), big)
	if !ok1 || !ok2 {
		return false
	}
	comp := g.components(big)
	c := comp[sy*g.W+sx]
	return c >= 0 && c == comp[gy*g.W+gx]
}

// neighbors calls fn for each cell reachable in one step from (cx, cy),
// with the step's cost. Diagonal steps may not cut past blocked corners.
func (g *Grid) neighbors(cx, cy int, big bool, fn func(nx, ny int, cost float64)) {
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := cx+dx, cy+dy
			if !g.passable(nx, ny, big) || !g.canStep(cx, cy, nx, ny) {
				continue
			}
			if dx != 0 && dy != 0 && (!g.passable(cx+dx, cy, big) || !g.passable(cx, cy+dy, big)) {
				continue
			}
			cost := 1.0
			if dx != 0 && dy != 0 {
				cost = math.Sqrt2
			}
			fn(nx, ny, cost)
		}
	}
}

// components labels connected regions of passable cells (-1 = blocked),
// recomputing only when the grid changed since the last call.
func (g *Grid) components(big bool) []int32 {
	k := 0
	if big {
		k = 1
	}
	if g.comp[k] != nil && g.compVersion[k] == g.version {
		return g.comp[k]
	}
	comp := g.comp[k]
	if len(comp) != g.W*g.H {
		comp = make([]int32, g.W*g.H)
	}
	// Passability per cell, computed once for this labeling.
	pass := make([]bool, g.W*g.H)
	for i := range comp {
		comp[i] = -1
		pass[i] = g.passable(i%g.W, i/g.W, big)
	}
	var label int32
	queue := make([]int, 0, 1024)
	for start := range comp {
		if comp[start] != -1 || !pass[start] {
			continue
		}
		comp[start] = label
		queue = append(queue[:0], start)
		for len(queue) > 0 {
			i := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			cx, cy := i%g.W, i/g.W
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := cx+dx, cy+dy
					if (dx == 0 && dy == 0) || nx < 0 || ny < 0 || nx >= g.W || ny >= g.H {
						continue
					}
					n := ny*g.W + nx
					if comp[n] != -1 || !pass[n] || !g.canStep(cx, cy, nx, ny) {
						continue
					}
					// Same corner rule as the search.
					if dx != 0 && dy != 0 && (!pass[cy*g.W+nx] || !pass[ny*g.W+cx]) {
						continue
					}
					comp[n] = label
					queue = append(queue, n)
				}
			}
		}
		label++
	}
	g.comp[k], g.compVersion[k] = comp, g.version
	return comp
}

// cells runs (or reuses) A* between the passable cells nearest a and b.
func (g *Grid) cells(a, b Point, big bool) ([]Point, int, int, bool) {
	sx, sy, ok1 := g.nearestPassable(int(a.X), int(a.Y), big)
	gx, gy, ok2 := g.nearestPassable(int(b.X), int(b.Y), big)
	if !ok1 || !ok2 {
		return nil, 0, 0, false
	}
	key := pathKey{sx, sy, gx, gy, big}
	if c, ok := g.cache[key]; ok && c.cells != nil && (c.version == g.version || g.stillPassable(c.cells, big)) {
		g.cache[key] = cachedPath{c.cells, g.version}
		return c.cells, gx, gy, true
	}
	cells := g.astar(sx, sy, gx, gy, big)
	if len(g.cache) > 8192 {
		g.cache = map[pathKey]cachedPath{}
	}
	g.cache[key] = cachedPath{cells, g.version}
	return cells, gx, gy, true
}

// lineClear walks the cells a segment crosses (supercover DDA).
func (g *Grid) lineClear(a, b Point, big bool) bool {
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
	// only cells after it must be passable.
	for n := 0; n < 4*(g.W+g.H); n++ {
		if x == ex && y == ey {
			return g.passable(x, y, big) || n == 0
		}
		px, py := x, y
		if tMaxX < tMaxY {
			tMaxX += tDeltaX
			x += stepX
		} else {
			tMaxY += tDeltaY
			y += stepY
		}
		if !g.passable(x, y, big) || !g.canStep(px, py, x, y) {
			return false
		}
	}
	return true
}

// SetLevels gives each cell a cliff level (w*h values). Steps between
// neighboring cells on different levels are then forbidden unless one of the
// two is a ramp (a level that isn't a multiple of 64): this stops paths from
// walking down the walkable rims on either side of a thin cliff.
func (g *Grid) SetLevels(levels []uint16) {
	if len(levels) == g.W*g.H {
		g.levels = levels
		g.clearCache()
	}
}

func (g *Grid) canStep(ax, ay, bx, by int) bool {
	if g.levels == nil || ax < 0 || ay < 0 || ax >= g.W || ay >= g.H || bx < 0 || by < 0 || bx >= g.W || by >= g.H {
		return true
	}
	a, b := g.levels[ay*g.W+ax], g.levels[by*g.W+bx]
	return a == b || a%64 != 0 || b%64 != 0
}

// nearestPassable searches outward in rings for a passable cell.
func (g *Grid) nearestPassable(x, y int, big bool) (int, int, bool) {
	if g.passable(x, y, big) {
		return x, y, true
	}
	for r := 1; r < 12; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if max(abs(dx), abs(dy)) == r && g.passable(x+dx, y+dy, big) {
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
// nil when unreachable. Callers check reachability first (components), so a
// search normally ends at the goal rather than exhausting the map.
func (g *Grid) astar(sx, sy, gx, gy int, big bool) []Point {
	start, goal := sy*g.W+sx, gy*g.W+gx
	if start == goal {
		return []Point{{float64(gx) + 0.5, float64(gy) + 0.5}}
	}
	if n := g.W * g.H; len(g.gs) != n {
		g.gs, g.from = make([]float64, n), make([]int32, n)
		g.seen, g.done = make([]uint32, n), make([]uint32, n)
	}
	g.stamp++
	st := g.stamp
	h := func(i int) float64 {
		dx := math.Abs(float64(i%g.W - gx))
		dy := math.Abs(float64(i/g.W - gy))
		return dx + dy + (math.Sqrt2-2)*math.Min(dx, dy) // octile
	}
	g.gs[start], g.seen[start] = 0, st
	open := &openSet{{i: start, f: h(start)}}
	for open.Len() > 0 {
		cur := heap.Pop(open).(node)
		if cur.i == goal {
			var cells []Point
			for i := goal; i != start; i = int(g.from[i]) {
				cells = append(cells, Point{float64(i%g.W) + 0.5, float64(i/g.W) + 0.5})
			}
			for l, r := 0, len(cells)-1; l < r; l, r = l+1, r-1 {
				cells[l], cells[r] = cells[r], cells[l]
			}
			return cells
		}
		if g.done[cur.i] == st {
			continue
		}
		g.done[cur.i] = st
		g.neighbors(cur.i%g.W, cur.i/g.W, big, func(nx, ny int, cost float64) {
			ni := ny*g.W + nx
			ng := cur.g + cost
			if g.seen[ni] == st && ng >= g.gs[ni] {
				return
			}
			g.gs[ni], g.from[ni], g.seen[ni] = ng, int32(cur.i), st
			heap.Push(open, node{i: ni, g: ng, f: ng + h(ni)})
		})
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
	g.clearCache()
}

// Occupy blocks a structure's footprint (it can be cleared with Vacate).
func (g *Grid) Occupy(cx, cy float64, w, h int) {
	g.footprint(cx, cy, w, h, func(i int) { g.walk[i] = false })
}

// Vacate clears a structure's footprint back to the terrain underneath.
func (g *Grid) Vacate(cx, cy float64, w, h int) {
	g.footprint(cx, cy, w, h, func(i int) { g.walk[i] = g.base[i] })
}
