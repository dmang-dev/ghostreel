// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"math"
	"sort"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
	"github.com/OWNER/ghostreel/internal/gamedata"
	"github.com/OWNER/ghostreel/internal/replay"
)

// UnitState is one unit as it appears at a given loop.
type UnitState struct {
	ID           uint32
	Type         uint32 // index into Timeline.UnitTypes, after morphs
	Owner        uint32
	X, Y, Facing float32
	Constructing bool
}

// State is the whole game at one loop.
type State struct {
	Loop  uint32
	Units []UnitState            // sorted by ID
	Stats map[uint32]*pb.PlayerStats // latest per player at or before Loop
}

// Index wraps a timeline with lookup structures for seeking.
type Index struct {
	TL     *pb.Timeline
	tracks []*pb.Track // by unit id; nil when the unit has no keyframes
	// edges are births and deaths sorted by loop, for moving from a snapshot
	// to the requested loop.
	edges     []edge
	statLoops []uint32 // loops of PlayerStats events, parallel to stats
	stats     []*pb.PlayerStats
}

type edge struct {
	loop uint32
	id   uint32
	born bool
}

// NewIndex prepares tl for seeking. tl must not be modified afterwards.
func NewIndex(tl *pb.Timeline) *Index {
	ix := &Index{TL: tl, tracks: make([]*pb.Track, len(tl.Units))}
	for _, t := range tl.Tracks {
		if int(t.UnitId) < len(ix.tracks) {
			ix.tracks[t.UnitId] = t
		}
	}
	for _, u := range tl.Units {
		ix.edges = append(ix.edges, edge{u.BornLoop, u.Id, true})
		if u.DiedLoop != nil {
			ix.edges = append(ix.edges, edge{*u.DiedLoop, u.Id, false})
		}
	}
	sort.SliceStable(ix.edges, func(i, j int) bool { return ix.edges[i].loop < ix.edges[j].loop })
	for _, e := range tl.Events {
		if ps := e.GetPlayerStats(); ps != nil {
			ix.statLoops = append(ix.statLoops, e.Loop)
			ix.stats = append(ix.stats, ps)
		}
	}
	return ix
}

// Alive reports whether a unit exists at loop: born at or before it and not
// yet dead. A unit is gone from its death loop on.
func Alive(u *pb.Unit, loop uint32) bool {
	return u.BornLoop <= loop && (u.DiedLoop == nil || *u.DiedLoop > loop)
}

// Seek returns the state at loop, starting from the nearest snapshot at or
// before it so the cost doesn't grow with the loop number.
func (ix *Index) Seek(loop uint32) State {
	tl := ix.TL
	interval := tl.Meta.GetSnapshotInterval()
	if len(tl.Snapshots) == 0 || interval == 0 {
		return ix.StateFromZero(loop)
	}
	// Snapshot si sits at si*interval <= loop.
	si := min(int(loop/interval), len(tl.Snapshots)-1)
	snap := tl.Snapshots[si]
	alive := make(map[uint32]bool, len(snap.Alive))
	for _, id := range snap.Alive {
		alive[id] = true
	}
	// Apply births and deaths after the snapshot, up to and including loop.
	i := sort.Search(len(ix.edges), func(i int) bool { return ix.edges[i].loop > snap.Loop })
	for ; i < len(ix.edges) && ix.edges[i].loop <= loop; i++ {
		if ix.edges[i].born {
			alive[ix.edges[i].id] = true
		} else {
			delete(alive, ix.edges[i].id)
		}
	}
	ids := make([]uint32, 0, len(alive))
	for id := range alive {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ix.stateOf(loop, ids)
}

// StateFromZero computes the state at loop by checking every unit. It is the
// reference Seek is tested against.
func (ix *Index) StateFromZero(loop uint32) State {
	var ids []uint32
	for _, u := range ix.TL.Units {
		if Alive(u, loop) {
			ids = append(ids, u.Id)
		}
	}
	return ix.stateOf(loop, ids)
}

func (ix *Index) stateOf(loop uint32, ids []uint32) State {
	st := State{Loop: loop, Units: make([]UnitState, 0, len(ids)), Stats: map[uint32]*pb.PlayerStats{}}
	for _, id := range ids {
		u := ix.TL.Units[id]
		typ := TypeAt(u, loop)
		us := UnitState{
			ID: id, Type: typ, Owner: OwnerAt(u, loop),
			Constructing: u.Constructed && (u.DoneLoop == nil || *u.DoneLoop > loop),
		}
		us.X, us.Y, us.Facing = PositionAt(ix.tracks[id], ix.speedOf(typ), loop)
		st.Units = append(st.Units, us)
	}
	n := sort.Search(len(ix.statLoops), func(i int) bool { return ix.statLoops[i] > loop })
	for i := 0; i < n; i++ {
		st.Stats[ix.stats[i].Player] = ix.stats[i]
	}
	return st
}

func (ix *Index) speedOf(typ uint32) float32 {
	if int(typ) < len(ix.TL.UnitTypes) {
		return ix.TL.UnitTypes[typ].Speed
	}
	return 0
}

// TypeAt is the unit's type at loop, after any morphs up to it.
func TypeAt(u *pb.Unit, loop uint32) uint32 {
	t := u.Type
	for _, m := range u.Morphs {
		if m.Loop > loop {
			break
		}
		t = m.Type
	}
	return t
}

// OwnerAt is the unit's owner at loop.
func OwnerAt(u *pb.Unit, loop uint32) uint32 {
	o := u.Owner
	for _, c := range u.OwnerChanges {
		if c.Loop > loop {
			break
		}
		o = c.Owner
	}
	return o
}

// PositionAt evaluates a track at loop.
//
// Between two keyframes the unit holds its earlier position and then travels
// to the next one at its type's speed, arriving exactly on time. Tracker
// samples are sparse (often minutes apart), and spreading the move over the
// whole gap would show units drifting slowly across the map; holding until
// the last moment is closer to how units actually move between fights.
// speed is in cells per game second; 0 means "unknown", which uses the
// default speed.
func PositionAt(t *pb.Track, speed float32, loop uint32) (x, y, facing float32) {
	if t == nil || len(t.Keyframes) == 0 {
		return 0, 0, 0
	}
	ks := t.Keyframes
	i := sort.Search(len(ks), func(i int) bool { return ks[i].Loop > loop }) // first key after loop
	if i == 0 {
		return ks[0].X, ks[0].Y, ks[0].Facing
	}
	k0 := ks[i-1]
	if i == len(ks) || k0.Loop == loop {
		return k0.X, k0.Y, k0.Facing
	}
	k1 := ks[i]
	dx, dy := float64(k1.X-k0.X), float64(k1.Y-k0.Y)
	dist := math.Hypot(dx, dy)
	if dist < 1e-6 {
		return k0.X, k0.Y, k0.Facing
	}
	if speed <= 0 {
		speed = gamedata.DefaultSpeed
	}
	travel := dist / (float64(speed) / replay.LoopsPerGameSecond) // loops needed
	depart := math.Max(float64(k0.Loop), float64(k1.Loop)-travel)
	if float64(loop) <= depart {
		return k0.X, k0.Y, k0.Facing
	}
	f := (float64(loop) - depart) / (float64(k1.Loop) - depart)
	heading := float32(math.Atan2(dy, dx))
	return k0.X + float32(dx*f), k0.Y + float32(dy*f), heading
}
