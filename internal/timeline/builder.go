// SPDX-License-Identifier: Apache-2.0

// Package timeline builds, stores and seeks playback timelines. Build turns
// normalized replay events into a tracker-tier timeline; Index answers "what
// does the game look like at loop L" in time independent of L.
package timeline

import (
	"fmt"
	"math"
	"sort"
	"strings"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
	"github.com/OWNER/ghostreel/internal/gamedata"
	"github.com/OWNER/ghostreel/internal/replay"
)

// DefaultSnapshotInterval is the loop spacing of snapshots (~7 s on the clock).
const DefaultSnapshotInterval = 160

// Options control Build.
type Options struct {
	Data             gamedata.Provider // defaults to gamedata.Stub
	SnapshotInterval uint32            // defaults to DefaultSnapshotInterval
	Generator        string
	// SkipCommands leaves player commands out of the event feed; they are
	// most of the file size in bot games.
	SkipCommands bool
}

type unitBuild struct {
	pb      *pb.Unit
	keys    []*pb.Keyframe
	cur     gamedata.Unit // current type's data
	hidden  bool          // never emitted (UI-only types)
	emitted uint32        // id in the output when !hidden
}

type builder struct {
	opts      Options
	baseBuild int
	tl        *pb.Timeline
	typeIndex map[string]uint32
	byTag     map[replay.Tag]*unitBuild
	bySlot    map[uint32]*unitBuild
	all       []*unitBuild
}

// Build reconstructs a tracker-tier timeline. Events must be loop-ordered,
// as replay.Open returns them.
func Build(info *replay.Info, events []replay.Event, opts Options) (*pb.Timeline, error) {
	if !info.HasTracker {
		return nil, replay.ErrNoTrackerEvents
	}
	if opts.Data == nil {
		opts.Data = gamedata.Stub{}
	}
	if opts.SnapshotInterval == 0 {
		opts.SnapshotInterval = DefaultSnapshotInterval
	}
	b := &builder{
		opts:      opts,
		baseBuild: info.BaseBuild,
		tl:        &pb.Timeline{SchemaVersion: pb.SchemaVersion_SCHEMA_VERSION_1, Meta: meta(info, opts)},
		typeIndex: map[string]uint32{},
		byTag:     map[replay.Tag]*unitBuild{},
		bySlot:    map[uint32]*unitBuild{},
	}
	var last int64
	for i := range events {
		e := &events[i]
		if e.Loop < last {
			return nil, fmt.Errorf("events out of order at index %d (loop %d after %d)", i, e.Loop, last)
		}
		last = e.Loop
		b.apply(e)
	}
	b.finish()
	return b.tl, nil
}

func meta(info *replay.Info, opts Options) *pb.Meta {
	m := &pb.Meta{
		ReplayHash:         info.Hash,
		Version:            info.Version,
		BaseBuild:          uint32(info.BaseBuild),
		ProtocolApproxFrom: uint32(info.ProtocolApproxFrom),
		Loops:              uint32(info.Loops),
		MapTitle:           info.MapTitle,
		MapFileName:        info.MapFileName,
		MapWidth:           uint32(info.MapWidth),
		MapHeight:          uint32(info.MapHeight),
		Fidelity:           pb.Source_SOURCE_TRACKER,
		SnapshotInterval:   opts.SnapshotInterval,
		Generator:          opts.Generator,
	}
	if info.MapHandle != nil {
		m.MapHash = info.MapHandle.Digest
		m.MapFileName = ""
	}
	for _, p := range info.Players {
		m.Players = append(m.Players, &pb.Player{
			Id: uint32(p.ID), Name: p.Name, Race: p.Race, Result: p.Result,
			Color:  uint32(p.Color[0])<<16 | uint32(p.Color[1])<<8 | uint32(p.Color[2]),
			StartX: float32(p.StartX), StartY: float32(p.StartY),
		})
	}
	return m
}

func (b *builder) typeOf(name string) (uint32, gamedata.Unit) {
	u := b.opts.Data.Unit(b.baseBuild, name)
	if id, ok := b.typeIndex[name]; ok {
		return id, u
	}
	id := uint32(len(b.tl.UnitTypes))
	b.typeIndex[name] = id
	b.tl.UnitTypes = append(b.tl.UnitTypes, &pb.UnitType{
		Name: name, Class: pbClass(u.Class), Radius: float32(u.Radius),
		FootprintW: uint32(u.FootprintW), FootprintH: uint32(u.FootprintH),
		Air: u.Air, Movable: u.Movable, Known: u.Known, Speed: float32(u.Speed),
	})
	return id, u
}

func pbClass(c gamedata.Class) pb.UnitClass {
	switch c {
	case gamedata.ClassWorker:
		return pb.UnitClass_UNIT_CLASS_WORKER
	case gamedata.ClassBuilding:
		return pb.UnitClass_UNIT_CLASS_BUILDING
	case gamedata.ClassResource:
		return pb.UnitClass_UNIT_CLASS_RESOURCE
	case gamedata.ClassNeutral:
		return pb.UnitClass_UNIT_CLASS_NEUTRAL
	case gamedata.ClassHidden:
		return pb.UnitClass_UNIT_CLASS_HIDDEN
	}
	return pb.UnitClass_UNIT_CLASS_ARMY
}

// cellCenter converts a tracker coordinate to a position. Tracker events
// truncate positions to whole cells, so the true position lies in [v, v+1):
// the cell center is the unbiased estimate. Structures with an even footprint
// are centered on a cell corner, which the integer already is.
func cellCenter(v float64, footprint int) float32 {
	if footprint > 0 && footprint%2 == 0 {
		return float32(v)
	}
	return float32(v + 0.5)
}

func (b *builder) addKey(u *unitBuild, loop int64, x, y float64, state pb.UnitState) {
	k := &pb.Keyframe{
		Loop:   uint32(loop),
		X:      cellCenter(x, u.cur.FootprintW),
		Y:      cellCenter(y, u.cur.FootprintH),
		State:  state,
		Source: pb.Source_SOURCE_TRACKER,
	}
	if n := len(u.keys); n > 0 && u.keys[n-1].Loop == k.Loop {
		// One keyframe per loop: a death or later sample at the same loop wins.
		u.keys[n-1] = k
		return
	}
	u.keys = append(u.keys, k)
}

// pinned reports whether the unit's position is fixed by its placement right
// now. Liftable and uprootable buildings are pinned while grounded (their
// grounded type has no speed) and free once they morph into their mobile type.
func pinned(u *unitBuild) bool { return u.cur.IsStructure() && u.cur.Speed == 0 }

func (b *builder) apply(e *replay.Event) {
	switch e.Kind {
	case replay.KindUnitBorn, replay.KindUnitInit:
		if old := b.bySlot[e.Tag.Index()]; old != nil && old.pb.DiedLoop == nil {
			// A slot is only reused after its unit died; if the death event
			// is missing, end the old unit here so it can't linger.
			b.kill(old, e.Loop, 0, nil)
		}
		typ, data := b.typeOf(e.UnitType)
		u := &unitBuild{
			pb: &pb.Unit{
				Tag: uint32(e.Tag), Owner: uint32(e.Player), Type: typ, BornLoop: uint32(e.Loop),
				Constructed: e.Kind == replay.KindUnitInit,
			},
			cur:    data,
			hidden: data.Class == gamedata.ClassHidden,
		}
		b.byTag[e.Tag], b.bySlot[e.Tag.Index()] = u, u
		b.all = append(b.all, u)
		state := pb.UnitState_UNIT_STATE_IDLE
		if u.pb.Constructed {
			state = pb.UnitState_UNIT_STATE_CONSTRUCTING
		}
		b.addKey(u, e.Loop, e.X, e.Y, state)
		if !u.hidden {
			u.emitted = uint32(len(b.tl.Units))
			u.pb.Id = u.emitted
			b.tl.Units = append(b.tl.Units, u.pb)
			b.event(e.Loop, &pb.Event{Kind: &pb.Event_UnitBorn{UnitBorn: &pb.UnitBorn{UnitId: u.emitted}}})
		}

	case replay.KindUnitDone:
		if u := b.byTag[e.Tag]; u != nil && u.pb.DoneLoop == nil {
			l := uint32(e.Loop)
			u.pb.DoneLoop = &l
			if !u.hidden {
				b.event(e.Loop, &pb.Event{Kind: &pb.Event_UnitDone{UnitDone: &pb.UnitDone{UnitId: u.emitted}}})
			}
		}

	case replay.KindUnitDied:
		if u := b.byTag[e.Tag]; u != nil && u.pb.DiedLoop == nil {
			b.kill(u, e.Loop, e.KillerPlayer, e)
		}

	case replay.KindUnitTypeChange:
		if u := b.byTag[e.Tag]; u != nil {
			typ, data := b.typeOf(e.UnitType)
			u.cur = data
			u.pb.Morphs = append(u.pb.Morphs, &pb.Morph{Loop: uint32(e.Loop), Type: typ})
			if !u.hidden {
				b.event(e.Loop, &pb.Event{Kind: &pb.Event_Morph{Morph: &pb.UnitMorph{UnitId: u.emitted, Type: typ}}})
			}
		}

	case replay.KindUnitOwnerChange:
		if u := b.byTag[e.Tag]; u != nil {
			u.pb.OwnerChanges = append(u.pb.OwnerChanges, &pb.OwnerChange{Loop: uint32(e.Loop), Owner: uint32(e.Player)})
		}

	case replay.KindUnitPositions:
		for _, p := range e.Positions {
			u := b.bySlot[p.Index]
			if u == nil || u.pb.DiedLoop != nil || pinned(u) {
				continue
			}
			b.addKey(u, e.Loop, p.X, p.Y, pb.UnitState_UNIT_STATE_IDLE)
		}

	case replay.KindPlayerStats:
		s := e.Stats
		b.event(e.Loop, &pb.Event{Kind: &pb.Event_PlayerStats{PlayerStats: &pb.PlayerStats{
			Player: uint32(e.Player), SupplyUsed: float32(s.SupplyUsed), SupplyMade: float32(s.SupplyMade),
			Minerals: u32(s.Minerals), Vespene: u32(s.Vespene),
			MineralRate: u32(s.MineralRate), VespeneRate: u32(s.VespeneRate), Workers: u32(s.Workers),
			ArmyMinerals: u32(s.ArmyMinerals), ArmyVespene: u32(s.ArmyGas),
			LostMinerals: u32(s.LostMinerals), LostVespene: u32(s.LostGas),
			KilledMinerals: u32(s.KilledMinerals), KilledVespene: u32(s.KilledGas),
		}}})

	case replay.KindUpgrade:
		if cosmeticUpgrade(e.Name) {
			return
		}
		b.event(e.Loop, &pb.Event{Kind: &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: uint32(e.Player), Name: e.Name, Count: int32(e.Count)}}})

	case replay.KindChat:
		b.event(e.Loop, &pb.Event{Kind: &pb.Event_Chat{Chat: &pb.Chat{Player: uint32(e.Player), Text: e.Text}}})

	case replay.KindCommand:
		if b.opts.SkipCommands {
			return
		}
		c := &pb.Command{
			Player: uint32(e.Player), Ability: int32(e.AbilityLink), AbilityCmdIndex: uint32(e.AbilityCmdIndex),
			HasTarget: e.HasTarget, X: float32(e.X), Y: float32(e.Y),
		}
		if e.TargetTag != 0 {
			if u := b.byTag[e.TargetTag]; u != nil && !u.hidden {
				c.TargetUnitId, c.HasTargetUnit = u.emitted, true
			}
		}
		b.event(e.Loop, &pb.Event{Kind: &pb.Event_Command{Command: c}})
	}
}

// kill ends a unit. e carries the death position when known.
func (b *builder) kill(u *unitBuild, loop int64, killer int, e *replay.Event) {
	l := uint32(loop)
	u.pb.DiedLoop = &l
	u.pb.KillerPlayer = uint32(killer)
	if e != nil && !pinned(u) {
		b.addKey(u, loop, e.X, e.Y, pb.UnitState_UNIT_STATE_DEAD)
	}
	if !u.hidden {
		b.event(loop, &pb.Event{Kind: &pb.Event_UnitDied{UnitDied: &pb.UnitDied{UnitId: u.emitted, KillerPlayer: uint32(killer)}}})
	}
}

func cosmeticUpgrade(name string) bool {
	return strings.HasPrefix(name, "Reward") || strings.HasPrefix(name, "Spray") || strings.HasPrefix(name, "GameHeart")
}

func (b *builder) event(loop int64, e *pb.Event) {
	e.Loop = uint32(loop)
	b.tl.Events = append(b.tl.Events, e)
}

func u32(v int64) uint32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

func (b *builder) finish() {
	for _, u := range b.all {
		if u.hidden || len(u.keys) == 0 {
			continue
		}
		setFacing(u.keys)
		b.tl.Tracks = append(b.tl.Tracks, &pb.Track{UnitId: u.emitted, Keyframes: u.keys})
	}
	b.tl.Snapshots = snapshots(b.tl)
}

// setFacing points each keyframe toward the next position the unit moves to,
// keeping the previous facing while it stands still.
func setFacing(keys []*pb.Keyframe) {
	facing := float32(-math.Pi / 2) // face "down" the map until known, like a fresh unit
	for i, k := range keys {
		for j := i + 1; j < len(keys); j++ {
			dx, dy := keys[j].X-k.X, keys[j].Y-k.Y
			if dx*dx+dy*dy > 0.01 {
				facing = float32(math.Atan2(float64(dy), float64(dx)))
				break
			}
		}
		k.Facing = facing
	}
}

// snapshots records the alive set and latest stats every interval loops.
func snapshots(tl *pb.Timeline) []*pb.Snapshot {
	interval := tl.Meta.SnapshotInterval
	n := tl.Meta.Loops/interval + 1
	// Lifecycle edges sorted by loop, so the alive set can be swept forward.
	type edge struct {
		loop uint32
		id   uint32
		born bool
	}
	edges := make([]edge, 0, 2*len(tl.Units))
	for _, u := range tl.Units {
		edges = append(edges, edge{u.BornLoop, u.Id, true})
		if u.DiedLoop != nil {
			edges = append(edges, edge{*u.DiedLoop, u.Id, false})
		}
	}
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].loop < edges[j].loop })

	alive := map[uint32]bool{}
	latest := map[uint32]*pb.PlayerStats{}
	out := make([]*pb.Snapshot, 0, n)
	ei, evi := 0, 0
	for s := uint32(0); s < n; s++ {
		loop := s * interval
		for ei < len(edges) && edges[ei].loop <= loop {
			if edges[ei].born {
				alive[edges[ei].id] = true
			} else {
				delete(alive, edges[ei].id)
			}
			ei++
		}
		for evi < len(tl.Events) && tl.Events[evi].Loop <= loop {
			if ps := tl.Events[evi].GetPlayerStats(); ps != nil {
				latest[ps.Player] = ps
			}
			evi++
		}
		snap := &pb.Snapshot{Loop: loop, Alive: make([]uint32, 0, len(alive))}
		for id := range alive {
			snap.Alive = append(snap.Alive, id)
		}
		sort.Slice(snap.Alive, func(i, j int) bool { return snap.Alive[i] < snap.Alive[j] })
		for p := uint32(0); p <= uint32(len(tl.Meta.Players)); p++ {
			if ps := latest[p]; ps != nil {
				snap.Stats = append(snap.Stats, ps)
			}
		}
		out = append(out, snap)
	}
	return out
}
