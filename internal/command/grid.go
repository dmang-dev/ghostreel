// SPDX-License-Identifier: Apache-2.0

package command

import (
	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
	"github.com/dmang-dev/ghostreel/internal/pathing"
)

// Grid builds the terrain grid for a replay: the map's pathable cells
// (playable area minus cliff edges). Structures, including the neutral ones
// present from the start, are added and removed by Apply as they appear,
// die, lift off or lower.
func Grid(m *mapfile.Map) *pathing.Grid {
	g := pathing.New(m.Width, m.Height)
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			if !m.Pathable(x, y) {
				g.Set(x, y, false)
			}
		}
	}
	g.SetLevels(m.CliffLevels)
	return g
}

// blocks reports whether a unit of this type occupies its footprint.
func blocks(t *pb.UnitType) bool {
	return t.FootprintW > 0 && !t.Air && t.Speed == 0 && t.Name != "SupplyDepotLowered" &&
		t.Class != pb.UnitClass_UNIT_CLASS_HIDDEN
}

type occupancy struct {
	x, y float64
	w, h int
}

// updateFootprint makes the grid match a structure's current state.
func (b *builder) updateFootprint(u *pb.Unit, typ *pb.UnitType, alive bool, loop int64) {
	g := b.opts.Grid
	if g == nil {
		return
	}
	if o, ok := b.occupied[u.Id]; ok {
		g.Vacate(o.x, o.y, o.w, o.h)
		delete(b.occupied, u.Id)
	}
	if !alive || !blocks(typ) {
		return
	}
	tr := b.tracks[u.Id]
	if tr == nil || len(tr.Keyframes) == 0 {
		return
	}
	// Where the structure stands now (a landed building may have moved).
	k := tr.Keyframes[0]
	for _, kk := range tr.Keyframes {
		if int64(kk.Loop) <= loop {
			k = kk
		}
	}
	o := occupancy{float64(k.X), float64(k.Y), int(typ.FootprintW), int(typ.FootprintH)}
	g.Occupy(o.x, o.y, o.w, o.h)
	b.occupied[u.Id] = o
}
