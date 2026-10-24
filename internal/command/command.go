// SPDX-License-Identifier: Apache-2.0

// Package command upgrades a tracker-tier timeline to the command tier: it
// follows players' selections and orders and moves their units accordingly
// (around cliffs, workers mining), snapping to the replay's position samples
// whenever one arrives.
//
// Each unit carries a plan: a list of timed straight legs. Because the plan
// fixes a unit's position at any loop, events can be applied in order without
// stepping time: an event at loop t first commits the plan up to t as
// keyframes, then replaces or extends it.
package command

import (
	"math"
	"sort"
	"strings"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
	"github.com/OWNER/ghostreel/internal/pathing"
	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/selection"
	"github.com/OWNER/ghostreel/internal/timeline"
)

// Options tune the command tier.
type Options struct {
	// Grid is the walkable map; nil moves ground units in straight lines.
	Grid *pathing.Grid
	// MiningCycle is a worker's full gather round trip in loops (measured
	// against the engine: 112 for SCVs on 4.10).
	MiningCycle float64
	// Formation is the largest offset (cells) a unit keeps from its group's
	// center when a group is ordered to a point.
	Formation float64
}

func (o *Options) defaults() {
	if o.MiningCycle == 0 {
		o.MiningCycle = 112
	}
	if o.Formation == 0 {
		o.Formation = 2
	}
}

type pt = pathing.Point

type leg struct {
	t0, t1 float64
	a, b   pt
}

type agent struct {
	id     uint32
	owner  int
	typ    *pb.UnitType
	speed  float64 // cells per loop; 0 = can't move
	legs   []leg
	keys   []*pb.Keyframe
	dead   bool
	mining *mineJob
	// goal is where the current plan ends, for re-planning after a sample.
	goal      *pt
	stopAt    float64 // distance from goal to stop at
	commanded bool
}

type mineJob struct {
	field, drop pt // where the worker stands to mine and to drop off
	nextLegAt   float64
	toField     bool
}

type builder struct {
	tl     *pb.Timeline
	opts   Options
	byTag  map[uint32]*pb.Unit
	bySlot map[uint32]*pb.Unit
	agents map[uint32]*agent
	sel    *selection.Tracker
	tracks map[uint32]*pb.Track
	// players with game events (their units can be commanded)
	active map[int]bool
	// workers sent to each mineral field by autoMine, to spread them out
	assigned map[uint32]int
	// rally points of production structures and larvae, by tag
	rally map[replay.Tag]rallyPoint
	// producer of each unit, to follow larva -> hatchery for rallies
	creator map[replay.Tag]replay.Tag
	// structure footprints currently blocking the grid
	occupied map[uint32]occupancy
}

type rallyPoint struct {
	p    pt
	unit *pb.Unit // set when rallied to a unit (a mineral field, say)
}

// Apply rebuilds the tracks of commandable units in tl (built by
// timeline.Build from the same events) at command-tier fidelity.
func Apply(tl *pb.Timeline, events []replay.Event, opts Options) {
	opts.defaults()
	b := &builder{
		tl: tl, opts: opts,
		byTag: map[uint32]*pb.Unit{}, bySlot: map[uint32]*pb.Unit{},
		agents: map[uint32]*agent{}, sel: selection.New(),
		tracks: map[uint32]*pb.Track{}, active: map[int]bool{}, assigned: map[uint32]int{},
		rally: map[replay.Tag]rallyPoint{}, creator: map[replay.Tag]replay.Tag{},
		occupied: map[uint32]occupancy{},
	}
	for _, u := range tl.Units {
		b.byTag[u.Tag] = u
	}
	for _, t := range tl.Tracks {
		b.tracks[t.UnitId] = t
	}
	for i := range events {
		if events[i].Kind == replay.KindCommand && events[i].Player > 0 {
			b.active[events[i].Player] = true
		}
	}
	if len(b.active) == 0 {
		return
	}
	for i := range events {
		b.apply(&events[i])
	}
	end := float64(tl.Meta.Loops)
	for _, a := range b.agents {
		if !a.dead {
			b.advance(a, end)
		}
		timeline.SetFacing(a.keys)
		b.tracks[a.id].Keyframes = a.keys
	}
	tl.Meta.Fidelity = pb.Source_SOURCE_COMMAND
}

func (b *builder) unitType(u *pb.Unit, loop int64) *pb.UnitType {
	return b.tl.UnitTypes[timeline.TypeAt(u, uint32(loop))]
}

func mobile(t *pb.UnitType) bool {
	return t.Speed > 0 && t.Class != pb.UnitClass_UNIT_CLASS_HIDDEN
}

func (b *builder) apply(e *replay.Event) {
	b.sel.Apply(e)
	t := float64(e.Loop)
	switch e.Kind {
	case replay.KindUnitBorn, replay.KindUnitInit:
		u := b.byTag[uint32(e.Tag)]
		if u == nil {
			return
		}
		b.bySlot[e.Tag.Index()] = u
		if e.Creator != 0 {
			b.creator[e.Tag] = e.Creator
		}
		typ := b.unitType(u, e.Loop)
		b.updateFootprint(u, typ, true, e.Loop)
		if !b.active[int(u.Owner)] || !mobile(typ) {
			return
		}
		if a := b.newAgent(u, typ, t); a != nil {
			b.onBirth(a, e, typ, t)
		}

	case replay.KindUnitDied:
		if u := b.byTag[uint32(e.Tag)]; u != nil {
			b.updateFootprint(u, nil, false, e.Loop)
		}
		if a := b.agents[b.unitID(e.Tag)]; a != nil && !a.dead {
			b.advance(a, t)
			// The death position is known exactly.
			b.setKey(a, t, pt{X: float64(cellCenter(e.X)), Y: float64(cellCenter(e.Y))}, pb.UnitState_UNIT_STATE_DEAD, pb.Source_SOURCE_TRACKER)
			a.dead = true
		}

	case replay.KindUnitTypeChange:
		u := b.byTag[uint32(e.Tag)]
		if u == nil {
			return
		}
		typ := b.unitType(u, e.Loop)
		b.updateFootprint(u, typ, true, e.Loop)
		if !b.active[int(u.Owner)] {
			return
		}
		a := b.agents[u.Id]
		switch {
		case a == nil && mobile(typ):
			// A building lifting off or a crawler uprooting.
			b.newAgent(u, typ, t)
		case a != nil:
			b.advance(a, t)
			a.typ, a.speed = typ, float64(typ.Speed)/replay.LoopsPerGameSecond
			if !mobile(typ) {
				b.stop(a, t)
			}
		}

	case replay.KindUnitPositions:
		for _, s := range e.Positions {
			u := b.bySlot[s.Index]
			if u == nil {
				continue
			}
			if a := b.agents[u.Id]; a != nil && !a.dead {
				b.sample(a, t, pt{X: float64(cellCenter(s.X)), Y: float64(cellCenter(s.Y))})
			}
		}

	case replay.KindCommand:
		if e.HasTarget {
			b.setRally(e)
			b.command(e, t)
		}
	}
}

func cellCenter(v float64) float32 { return float32(v + 0.5) }

func (b *builder) unitID(tag replay.Tag) uint32 {
	if u := b.byTag[uint32(tag)]; u != nil {
		return u.Id
	}
	return math.MaxUint32
}

func (b *builder) newAgent(u *pb.Unit, typ *pb.UnitType, t float64) *agent {
	tr := b.tracks[u.Id]
	if tr == nil || len(tr.Keyframes) == 0 {
		return nil
	}
	// Start from the tracker position at t (the birth spot, or where a
	// building stood when it lifted off).
	x, y, _ := timeline.PositionAt(tr, typ.Speed, uint32(t))
	a := &agent{id: u.Id, owner: int(u.Owner), typ: typ, speed: float64(typ.Speed) / replay.LoopsPerGameSecond}
	if t > float64(u.BornLoop) {
		// Keep the tracker keyframes up to now (a lifted building's history).
		for _, k := range tr.Keyframes {
			if float64(k.Loop) < t {
				a.keys = append(a.keys, k)
			}
		}
	}
	b.setKey(a, t, pt{X: float64(x), Y: float64(y)}, pb.UnitState_UNIT_STATE_IDLE, pb.Source_SOURCE_TRACKER)
	b.agents[u.Id] = a
	return a
}

// pos is where the plan puts the agent at t (t must not be before the plan's start).
func (a *agent) pos(t float64) pt {
	for _, l := range a.legs {
		if t <= l.t1 {
			if l.t1 <= l.t0 || t <= l.t0 {
				return l.a
			}
			f := (t - l.t0) / (l.t1 - l.t0)
			return pt{X: l.a.X + (l.b.X-l.a.X)*f, Y: l.a.Y + (l.b.Y-l.a.Y)*f}
		}
	}
	if n := len(a.keys); n > 0 && len(a.legs) == 0 {
		return pt{X: float64(a.keys[n-1].X), Y: float64(a.keys[n-1].Y)}
	}
	return a.legs[len(a.legs)-1].b
}

// setKey adds (or replaces, at the same loop) a keyframe.
func (b *builder) setKey(a *agent, t float64, p pt, state pb.UnitState, src pb.Source) {
	k := &pb.Keyframe{Loop: uint32(math.Round(t)), X: float32(p.X), Y: float32(p.Y), State: state, Source: src}
	if n := len(a.keys); n > 0 && a.keys[n-1].Loop >= k.Loop {
		k.Loop = a.keys[n-1].Loop
		a.keys[n-1] = k
		return
	}
	a.keys = append(a.keys, k)
}

// advance commits the plan up to t: keyframes at every leg boundary before t
// and one at t. Mining jobs extend the plan as needed.
func (b *builder) advance(a *agent, t float64) {
	b.extendMining(a, t)
	for len(a.legs) > 0 {
		l := a.legs[0]
		if l.t0 > t {
			break
		}
		moving := l.a != l.b
		state := pb.UnitState_UNIT_STATE_IDLE
		if moving {
			state = pb.UnitState_UNIT_STATE_MOVING
		}
		b.setKey(a, l.t0, l.a, state, pb.Source_SOURCE_COMMAND)
		if l.t1 > t {
			// Partially done: split the leg at t.
			p := a.pos(t)
			b.setKey(a, t, p, state, pb.Source_SOURCE_COMMAND)
			a.legs[0] = leg{t0: t, t1: l.t1, a: p, b: l.b}
			return
		}
		b.setKey(a, l.t1, l.b, pb.UnitState_UNIT_STATE_IDLE, pb.Source_SOURCE_COMMAND)
		a.legs = a.legs[1:]
	}
}

// current returns the agent's position at t after committing the plan.
func (b *builder) current(a *agent, t float64) pt {
	b.advance(a, t)
	if len(a.legs) > 0 {
		return a.pos(t)
	}
	k := a.keys[len(a.keys)-1]
	return pt{X: float64(k.X), Y: float64(k.Y)}
}

func (b *builder) stop(a *agent, t float64) {
	b.advance(a, t)
	a.legs, a.mining, a.goal = nil, nil, nil
}

// moveTo replaces (or, queued, extends) the plan with a path to goal,
// stopping stopAt cells short of it.
func (b *builder) moveTo(a *agent, t float64, goal pt, stopAt float64, queued bool) {
	if a.speed <= 0 {
		return
	}
	var from pt
	start := t
	if queued && len(a.legs) > 0 && a.mining == nil {
		last := a.legs[len(a.legs)-1]
		from, start = last.b, last.t1
	} else {
		from = b.current(a, t)
		a.legs = nil
	}
	a.mining = nil
	g := goal
	a.goal, a.stopAt = &g, stopAt
	b.appendPath(a, from, start, goal, stopAt)
}

func (b *builder) appendPath(a *agent, from pt, start float64, goal pt, stopAt float64) {
	var path []pt
	if a.typ.Air || b.opts.Grid == nil {
		path = []pt{goal}
	} else {
		if !b.opts.Grid.Reachable(from, goal) {
			// Walled in (or an unreachable target): the unit stays put.
			a.goal = nil
			return
		}
		path = b.opts.Grid.Path(from, goal)
	}
	if stopAt > 0 {
		path = shorten(from, path, stopAt)
	}
	cur, tt := from, start
	for _, p := range path {
		d := math.Hypot(p.X-cur.X, p.Y-cur.Y)
		if d < 1e-6 {
			continue
		}
		dt := d / a.speed
		a.legs = append(a.legs, leg{t0: tt, t1: tt + dt, a: cur, b: p})
		cur, tt = p, tt+dt
	}
}

// shorten trims the end of a path so it stops dist short of its goal.
func shorten(from pt, path []pt, dist float64) []pt {
	for len(path) > 0 {
		end := path[len(path)-1]
		prev := from
		if len(path) > 1 {
			prev = path[len(path)-2]
		}
		seg := math.Hypot(end.X-prev.X, end.Y-prev.Y)
		if seg > dist {
			f := (seg - dist) / seg
			path[len(path)-1] = pt{X: prev.X + (end.X-prev.X)*f, Y: prev.Y + (end.Y-prev.Y)*f}
			return path
		}
		dist -= seg
		path = path[:len(path)-1]
	}
	return path
}

// sample snaps the agent to a known position and resumes its job from there.
func (b *builder) sample(a *agent, t float64, p pt) {
	b.advance(a, t)
	b.setKey(a, t, p, pb.UnitState_UNIT_STATE_IDLE, pb.Source_SOURCE_TRACKER)
	a.legs = nil
	switch {
	case a.mining != nil:
		a.mining.nextLegAt = t
		a.mining.toField = true
		// Resume the cycle from the true position: head to the field first.
		b.mineLeg(a, p, t)
	case a.goal != nil && math.Hypot(a.goal.X-p.X, a.goal.Y-p.Y) > a.stopAt+0.5:
		b.appendPath(a, p, t, *a.goal, a.stopAt)
	}
}

// command applies a targeted command to the issuing player's selection.
func (b *builder) command(e *replay.Event, t float64) {
	var group []*agent
	for _, tag := range b.sel.Selected(e.Player) {
		u := b.byTag[uint32(tag)]
		if u == nil {
			continue
		}
		if a := b.agents[u.Id]; a != nil && !a.dead && a.speed > 0 && a.owner == e.Player {
			group = append(group, a)
		}
	}
	if len(group) == 0 {
		return
	}
	target := pt{X: e.X, Y: e.Y}
	var tu *pb.Unit
	if e.TargetTag != 0 {
		tu = b.byTag[uint32(e.TargetTag)]
	}
	if tu != nil {
		tt := b.unitType(tu, e.Loop)
		if isHarvestable(tt.Name) {
			for _, a := range group {
				if a.typ.Class == pb.UnitClass_UNIT_CLASS_WORKER {
					b.startMining(a, t, tu, tt)
				} else {
					b.moveTo(a, t, target, float64(a.typ.Radius+tt.Radius), e.Queued)
				}
			}
			return
		}
		enemy := int(tu.Owner) != e.Player && tu.Owner != 0
		for _, a := range group {
			stop := float64(a.typ.Radius + tt.Radius)
			if enemy {
				stop += attackRange(a.typ.Name)
			}
			b.moveTo(a, t, target, stop, e.Queued)
		}
		return
	}
	// Point target: keep a loose formation around the group's center.
	cx, cy := 0.0, 0.0
	pos := make([]pt, len(group))
	for i, a := range group {
		pos[i] = b.current(a, t)
		cx += pos[i].X
		cy += pos[i].Y
	}
	cx /= float64(len(group))
	cy /= float64(len(group))
	for i, a := range group {
		off := pt{X: pos[i].X - cx, Y: pos[i].Y - cy}
		if d := math.Hypot(off.X, off.Y); d > b.opts.Formation {
			off.X, off.Y = off.X*b.opts.Formation/d, off.Y*b.opts.Formation/d
		}
		b.moveTo(a, t, pt{X: target.X + off.X, Y: target.Y + off.Y}, 0, e.Queued)
	}
}

func isHarvestable(name string) bool {
	return strings.Contains(name, "MineralField") || strings.HasPrefix(name, "Refinery") ||
		strings.HasPrefix(name, "Assimilator") || strings.HasPrefix(name, "Extractor")
}

// attackRange is a rough weapon range for units that stop short of their
// target; unlisted units close to melee range.
func attackRange(name string) float64 {
	switch name {
	case "Marine", "Hellion", "Reaper", "Cyclone", "Sentry", "Hydralisk", "Liberator", "Queen":
		return 5
	case "Marauder", "Ghost", "Stalker", "Immortal", "Banshee", "Battlecruiser", "Thor":
		return 6
	case "SiegeTank", "Colossus", "Tempest":
		return 7
	case "VikingFighter", "Carrier":
		return 8
	case "Roach", "Adept", "Ravager", "Mutalisk", "Phoenix", "Corruptor":
		return 4
	case "Archon", "Infestor", "Viper", "HighTemplar", "Oracle", "VoidRay":
		return 3
	}
	return 0.1
}

// ---------------------------------------------------------------- mining

var townhalls = map[string]bool{
	"CommandCenter": true, "OrbitalCommand": true, "PlanetaryFortress": true,
	"Nexus": true, "Hatchery": true, "Lair": true, "Hive": true,
}

// nearestTownhall finds the owner's closest finished base at loop t.
func (b *builder) nearestTownhall(owner int, p pt, t float64) (pt, bool) {
	best, bestD := pt{}, math.Inf(1)
	for _, u := range b.tl.Units {
		if int(u.Owner) != owner || !timeline.Alive(u, uint32(t)) {
			continue
		}
		typ := b.unitType(u, int64(t))
		if !townhalls[typ.Name] || (u.Constructed && (u.DoneLoop == nil || float64(*u.DoneLoop) > t)) {
			continue
		}
		tr := b.tracks[u.Id]
		if tr == nil || len(tr.Keyframes) == 0 {
			continue
		}
		k := tr.Keyframes[0]
		if d := math.Hypot(float64(k.X)-p.X, float64(k.Y)-p.Y); d < bestD {
			best, bestD = pt{X: float64(k.X), Y: float64(k.Y)}, d
		}
	}
	return best, !math.IsInf(bestD, 1)
}

// autoMine sends a new worker to the mineral field nearest its base.
func (b *builder) autoMine(a *agent, t float64) {
	p := b.current(a, t)
	th, ok := b.nearestTownhall(a.owner, p, t)
	if !ok || math.Hypot(th.X-p.X, th.Y-p.Y) > 8 {
		return
	}
	var best *pb.Unit
	bestScore := math.Inf(1)
	for _, u := range b.tl.Units {
		if u.Owner != 0 || !timeline.Alive(u, uint32(t)) {
			continue
		}
		typ := b.unitType(u, int64(t))
		if !strings.Contains(typ.Name, "MineralField") {
			continue
		}
		k := b.tracks[u.Id].Keyframes[0]
		// Fields within reach of this base; the least used one wins.
		d := math.Hypot(float64(k.X)-th.X, float64(k.Y)-th.Y)
		if d > 10 {
			continue
		}
		if score := float64(b.assigned[u.Id])*100 + d; score < bestScore {
			best, bestScore = u, score
		}
	}
	if best != nil {
		b.assigned[best.Id]++
		b.startMining(a, t, best, b.unitType(best, int64(t)))
	}
}

func (b *builder) startMining(a *agent, t float64, field *pb.Unit, ft *pb.UnitType) {
	tr := b.tracks[field.Id]
	if tr == nil || len(tr.Keyframes) == 0 {
		return
	}
	fk := tr.Keyframes[0]
	fp := pt{X: float64(fk.X), Y: float64(fk.Y)}
	th, ok := b.nearestTownhall(a.owner, fp, t)
	if !ok {
		b.moveTo(a, t, fp, float64(a.typ.Radius+ft.Radius), false)
		return
	}
	// Stand at the field's and the base's edges, on the line between them.
	dx, dy := th.X-fp.X, th.Y-fp.Y
	d := math.Max(math.Hypot(dx, dy), 1e-6)
	ux, uy := dx/d, dy/d
	fieldR := float64(ft.Radius) + float64(a.typ.Radius)
	const baseR = 2.75
	job := &mineJob{
		field: pt{X: fp.X + ux*fieldR, Y: fp.Y + uy*fieldR},
		drop:  pt{X: th.X - ux*(baseR+float64(a.typ.Radius)), Y: th.Y - uy*(baseR+float64(a.typ.Radius))},
	}
	from := b.current(a, t)
	a.legs, a.goal = nil, nil
	a.mining = job
	a.commanded = true
	b.mineLeg(a, from, t)
}

// mineLeg plans the walk to the field; extendMining adds the rest lazily.
func (b *builder) mineLeg(a *agent, from pt, t float64) {
	j := a.mining
	j.toField = true
	j.nextLegAt = t
	b.appendMineMove(a, from, j.field)
}

func (b *builder) appendMineMove(a *agent, from, to pt) {
	j := a.mining
	d := math.Hypot(to.X-from.X, to.Y-from.Y)
	dt := d / a.speed
	if d < 1e-6 {
		dt = 0
	}
	a.legs = append(a.legs, leg{t0: j.nextLegAt, t1: j.nextLegAt + dt, a: from, b: to})
	j.nextLegAt += dt
}

// extendMining unrolls gather trips until the plan reaches past t.
func (b *builder) extendMining(a *agent, t float64) {
	j := a.mining
	if j == nil || a.speed <= 0 {
		return
	}
	travel := math.Hypot(j.field.X-j.drop.X, j.field.Y-j.drop.Y) / a.speed
	dwell := math.Max(8, b.opts.MiningCycle-2*travel)
	for j.nextLegAt <= t {
		if j.toField {
			// At the field: mine, then carry back.
			a.legs = append(a.legs, leg{t0: j.nextLegAt, t1: j.nextLegAt + dwell, a: j.field, b: j.field})
			j.nextLegAt += dwell
			b.appendMineMove(a, j.field, j.drop)
			j.toField = false
		} else {
			b.appendMineMove(a, j.drop, j.field)
			j.toField = true
		}
	}
	sort.SliceStable(a.legs, func(i, k int) bool { return a.legs[i].t0 < a.legs[k].t0 })
}

// ---------------------------------------------------------------- births and rallies

// onBirth sends a new unit where the game would: to its producer's rally
// point if one was set (following larva and eggs back to their hatchery),
// otherwise workers go mining and other units wait.
func (b *builder) onBirth(a *agent, e *replay.Event, typ *pb.UnitType, t float64) {
	if typ.Name == "MULE" {
		// MULEs are dropped onto a mineral line and mine the field they land at.
		b.mineNearest(a, t)
		return
	}
	for c, hops := e.Creator, 0; c != 0 && hops < 3; c, hops = b.creator[c], hops+1 {
		r, ok := b.rally[c]
		if !ok {
			continue
		}
		if r.unit != nil {
			rt := b.unitType(r.unit, e.Loop)
			if isHarvestable(rt.Name) && typ.Class == pb.UnitClass_UNIT_CLASS_WORKER {
				b.startMining(a, t, r.unit, rt)
				return
			}
		}
		b.moveTo(a, t, r.p, 0, false)
		return
	}
	if typ.Class == pb.UnitClass_UNIT_CLASS_WORKER {
		// Workers without a known rally are almost always rallied to minerals.
		b.autoMine(a, t)
	}
}

// setRally records a rally point for the structures (and larvae) in a
// player's selection when it gets a targeted command.
func (b *builder) setRally(e *replay.Event) {
	r := rallyPoint{p: pt{X: e.X, Y: e.Y}}
	if e.TargetTag != 0 {
		r.unit = b.byTag[uint32(e.TargetTag)]
	}
	for _, tag := range b.sel.Selected(e.Player) {
		u := b.byTag[uint32(tag)]
		if u == nil || int(u.Owner) != e.Player {
			continue
		}
		typ := b.unitType(u, e.Loop)
		if !mobile(typ) || typ.Name == "Larva" || typ.Name == "Egg" {
			b.rally[tag] = r
		}
	}
}

// mineNearest sends a worker to the mineral field closest to where it is.
func (b *builder) mineNearest(a *agent, t float64) {
	p := b.current(a, t)
	var best *pb.Unit
	bestD := 6.0
	for _, u := range b.tl.Units {
		if u.Owner != 0 || !timeline.Alive(u, uint32(t)) {
			continue
		}
		if !strings.Contains(b.unitType(u, int64(t)).Name, "MineralField") {
			continue
		}
		k := b.tracks[u.Id].Keyframes[0]
		if d := math.Hypot(float64(k.X)-p.X, float64(k.Y)-p.Y); d < bestD {
			best, bestD = u, d
		}
	}
	if best != nil {
		b.startMining(a, t, best, b.unitType(best, int64(t)))
	}
}
