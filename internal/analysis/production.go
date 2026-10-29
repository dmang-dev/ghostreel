// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

func init() { Register(Production{}) }

// Production finds how long each player's production stood idle.
//
// A Terran or Protoss production building (and town hall) is busy while it
// trains a unit (its train time before the unit appears; the replay names
// the building), builds an add-on, or upgrades (Orbital Command, Planetary
// Fortress), and idle the rest of the time it is finished, on the ground and
// alive. Warp gates are counted together: each warp-in keeps one gate busy
// for its cooldown. Zerg production is larvae: a hatchery is idle while it
// holds three or more, since it stops making new ones.
//
// The table lists each building and the totals; the chart shows how many
// were idle over the game. A reactor's second slot is not counted.
type Production struct{}

func (Production) ID() string { return "production" }

// span is the loops [a, b).
type span struct{ a, b uint32 }

// spans is a sorted, non-overlapping set of spans.
type spans []span

// union merges overlapping spans.
func union(in []span) spans {
	sort.Slice(in, func(i, j int) bool { return in[i].a < in[j].a })
	var out spans
	for _, s := range in {
		if s.b <= s.a {
			continue
		}
		if n := len(out); n > 0 && s.a <= out[n-1].b {
			out[n-1].b = max(out[n-1].b, s.b)
			continue
		}
		out = append(out, s)
	}
	return out
}

// minus is the parts of s outside o.
func (s spans) minus(o spans) spans {
	var out spans
	j := 0
	for _, x := range s {
		a := x.a
		for j < len(o) && o[j].b <= a {
			j++
		}
		for k := j; k < len(o) && o[k].a < x.b; k++ {
			if o[k].a > a {
				out = append(out, span{a, o[k].a})
			}
			a = max(a, o[k].b)
		}
		if a < x.b {
			out = append(out, span{a, x.b})
		}
	}
	return out
}

func (s spans) length() uint32 {
	var n uint32
	for _, x := range s {
		n += x.b - x.a
	}
	return n
}

// producer is one production building's (or a player's warp gates')
// time on the ground and idle.
type producer struct {
	player     uint32
	name       string
	ready      uint32
	kind       string // "army", "workers" or "larvae"
	avail      uint32
	idleSum    uint32 // totals only
	idle       spans
	idleWeight []int32 // for warp gates: idle gates per idle span (else 1)
}

func (Production) Analyze(tl *pb.Timeline) (*Report, error) {
	pos := map[uint32][2]float32{} // first known position, by unit
	for _, t := range tl.Tracks {
		if len(t.Keyframes) > 0 {
			pos[t.UnitId] = [2]float32{t.Keyframes[0].X, t.Keyframes[0].Y}
		}
	}
	lt := unitLifetimes(tl)
	children := map[uint32][]*pb.Unit{}
	busy := map[uint32][]span{}
	larvae := map[uint32][]span{} // by hatchery
	var hatcheries []*pb.Unit
	for _, u := range tl.Units {
		if u.Creator != nil {
			children[*u.Creator] = append(children[*u.Creator], u)
		}
		if typeName(tl, u.Type) == "Hatchery" && u.Owner != 0 {
			hatcheries = append(hatcheries, u)
		}
	}
	// Add-ons keep their building busy while they're built. The replay
	// doesn't link them, so they're matched by place: an add-on sits 2.5
	// cells right of and 0.5 below its building's center.
	for _, u := range tl.Units {
		name := typeName(tl, u.Type)
		if !u.Constructed || !(strings.HasSuffix(name, "Reactor") || strings.HasSuffix(name, "TechLab")) {
			continue
		}
		p, ok := pos[u.Id]
		if !ok {
			continue
		}
		end := endLoop(tl, u)
		if u.DoneLoop != nil {
			end = min(end, *u.DoneLoop)
		}
		for _, b := range tl.Units {
			bp, ok := pos[b.Id]
			if !ok || b.Owner != u.Owner || b.BornLoop > u.BornLoop || endLoop(tl, b) <= u.BornLoop {
				continue
			}
			if _, ok := producers[nameAt(tl, b, u.BornLoop)]; !ok {
				continue
			}
			if math.Abs(float64(bp[0]+2.5-p[0])) < 0.6 && math.Abs(float64(bp[1]-0.5-p[1])) < 0.6 {
				busy[b.Id] = append(busy[b.Id], span{u.BornLoop, end})
				break
			}
		}
	}
	// Larvae without a named hatchery (the first ones) go to the nearest.
	for _, u := range tl.Units {
		if typeName(tl, u.Type) != "Larva" {
			continue
		}
		end := endLoop(tl, u)
		if len(u.Morphs) > 0 {
			end = min(end, u.Morphs[0].Loop)
		}
		h := uint32(math.MaxUint32)
		if u.Creator != nil {
			h = *u.Creator
		} else if p, ok := pos[u.Id]; ok {
			best := float32(36) // within 6 cells
			for _, c := range hatcheries {
				cp, ok := pos[c.Id]
				if !ok || c.Owner != u.Owner || c.BornLoop > u.BornLoop {
					continue
				}
				if d := (cp[0]-p[0])*(cp[0]-p[0]) + (cp[1]-p[1])*(cp[1]-p[1]); d < best {
					best, h = d, c.Id
				}
			}
		}
		if h != math.MaxUint32 {
			larvae[h] = append(larvae[h], span{u.BornLoop, end})
		}
	}

	var all []*producer
	gates := map[uint32][]span{}   // warp gate time, by player
	warpins := map[uint32][]span{} // warp gate cooldowns, by player
	for _, u := range tl.Units {
		if u.Owner == 0 {
			continue
		}
		ready := u.BornLoop
		if u.Constructed {
			if u.DoneLoop == nil {
				continue
			}
			ready = *u.DoneLoop
		}
		end := endLoop(tl, u)
		if cd, ok := warpCooldown[typeName(tl, u.Type)]; ok && u.Constructed {
			warpins[u.Owner] = append(warpins[u.Owner], span{u.BornLoop, u.BornLoop + cd})
		}
		// The spans when it is a production building, by type.
		var avail []span
		first := ""
		cur, from := nameAt(tl, u, ready), ready
		flush := func(to uint32) {
			if to <= from {
				return
			}
			if cur == "WarpGate" {
				gates[u.Owner] = append(gates[u.Owner], span{from, to})
			} else if _, ok := producers[cur]; ok {
				avail = append(avail, span{from, to})
				if first == "" {
					first = cur
				}
			}
		}
		for _, m := range u.Morphs {
			if m.Loop <= ready {
				continue
			}
			if m.Loop >= end {
				break
			}
			flush(m.Loop)
			cur, from = typeName(tl, m.Type), m.Loop
		}
		flush(end)
		name := typeName(tl, u.Type)
		isHatch := name == "Hatchery" || name == "Lair" || name == "Hive"
		if len(avail) == 0 && !isHatch {
			continue
		}
		p := &producer{player: u.Owner, ready: ready}
		if isHatch {
			// Idle while holding 3+ larvae.
			p.name, p.kind = name, "larvae"
			p.avail = end - ready
			p.idle = atLeast(larvae[u.Id], 3, span{ready, end})
			all = append(all, p)
			continue
		}
		p.name, p.kind = first, "army"
		if producers[first] {
			p.kind = "workers"
		}
		b := busy[u.Id]
		for _, c := range children[u.Id] {
			if trainTime[typeName(tl, c.Type)] > 0 {
				b = append(b, span{lt.start[c.Id], c.BornLoop})
			}
		}
		for _, m := range u.Morphs {
			if t, ok := upgradeTime[typeName(tl, m.Type)]; ok {
				b = append(b, span{m.Loop - min(m.Loop, t), m.Loop})
			}
		}
		av := union(avail)
		p.avail = av.length()
		p.idle = av.minus(union(b))
		all = append(all, p)
	}
	for player, g := range gates {
		p := &producer{player: player, name: "Warp gates", kind: "army", ready: math.MaxUint32}
		for _, s := range g {
			p.avail += s.b - s.a
			p.ready = min(p.ready, s.a)
		}
		p.idle, p.idleWeight = idleGates(g, warpins[player])
		all = append(all, p)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ready < all[j].ready })

	rep := &Report{Title: "Production"}
	loops := tl.GetMeta().GetLoops()
	for _, pl := range tl.GetMeta().GetPlayers() {
		t := Table{Title: playerName(tl, pl.Id), Player: pl.Id, Columns: []string{"Built", "Production", "Idle", "Share"}}
		totals := map[string]*producer{}
		var rows []Row
		type delta struct {
			loop uint32
			d    int32
		}
		var deltas []delta
		for _, p := range all {
			if p.player != pl.Id {
				continue
			}
			idle := p.idleLength()
			tot := totals[p.kind]
			if tot == nil {
				tot = &producer{kind: p.kind}
				totals[p.kind] = tot
			}
			tot.avail += p.avail
			tot.idleSum += idle
			rows = append(rows, Row{Loop: p.ready, Cells: []string{clock(p.ready), p.name, clock(idle), share(idle, p.avail)}})
			for i, s := range p.idle {
				w := int32(1)
				if p.idleWeight != nil {
					w = p.idleWeight[i]
				}
				deltas = append(deltas, delta{s.a, w}, delta{s.b, -w})
			}
		}
		for _, k := range []struct{ kind, label string }{{"army", "All army production"}, {"workers", "All town halls"}, {"larvae", "All hatcheries (3+ larvae)"}} {
			if tot := totals[k.kind]; tot != nil {
				t.Rows = append(t.Rows, Row{Loop: 0, Cells: []string{"", k.label, clock(tot.idleSum), share(tot.idleSum, tot.avail)}})
			}
		}
		t.Rows = append(t.Rows, rows...)
		if len(rows) == 0 {
			continue
		}
		rep.Tables = append(rep.Tables, t)
		// The chart: idle buildings every 5 s.
		sort.Slice(deltas, func(i, j int) bool { return deltas[i].loop < deltas[j].loop })
		s := Series{Title: "Idle production", Player: pl.Id}
		n, j := int32(0), 0
		for l := uint32(0); l <= loops; l += 112 {
			for j < len(deltas) && deltas[j].loop <= l {
				n += deltas[j].d
				j++
			}
			s.Points = append(s.Points, Point{l, float32(n)})
		}
		rep.Series = append(rep.Series, s)
	}
	return rep, nil
}

func (p *producer) idleLength() uint32 {
	if p.idleWeight == nil {
		return p.idle.length()
	}
	var n uint32
	for i, s := range p.idle {
		n += (s.b - s.a) * uint32(p.idleWeight[i])
	}
	return n
}

// atLeast is the parts of within where k or more of the spans overlap.
func atLeast(in []span, k int, within span) spans {
	type ev struct {
		loop uint32
		d    int
	}
	var evs []ev
	for _, s := range in {
		evs = append(evs, ev{s.a, 1}, ev{s.b, -1})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].loop != evs[j].loop {
			return evs[i].loop < evs[j].loop
		}
		return evs[i].d < evs[j].d // ends first
	})
	var out []span
	n, from := 0, uint32(0)
	for _, e := range evs {
		was := n >= k
		n += e.d
		switch {
		case !was && n >= k:
			from = e.loop
		case was && n < k:
			out = append(out, span{from, e.loop})
		}
	}
	if n >= k {
		out = append(out, span{from, within.b})
	}
	return union(out).minus(spans{{0, within.a}}).minus(spans{{within.b, math.MaxUint32}})
}

// idleGates is when a player's warp gates outnumber their cooldowns, and by
// how many.
func idleGates(gates, cooldowns []span) (spans, []int32) {
	type ev struct {
		loop        uint32
		gates, busy int32
	}
	var evs []ev
	for _, s := range gates {
		evs = append(evs, ev{s.a, 1, 0}, ev{s.b, -1, 0})
	}
	for _, s := range cooldowns {
		evs = append(evs, ev{s.a, 0, 1}, ev{s.b, 0, -1})
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].loop < evs[j].loop })
	var out spans
	var w []int32
	var g, b int32
	for i, e := range evs {
		g += e.gates
		b += e.busy
		if i+1 < len(evs) && evs[i+1].loop == e.loop {
			continue
		}
		if i+1 < len(evs) {
			if idle := g - b; idle > 0 && evs[i+1].loop > e.loop {
				out = append(out, span{e.loop, evs[i+1].loop})
				w = append(w, idle)
			}
		}
	}
	return out, w
}

// share is part as a percentage of whole.
func share(part, whole uint32) string {
	if whole == 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", int(float64(part)*100/float64(whole)+0.5))
}
