// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"sort"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

// supplyPoint is a player's supply from Loop until the next point.
type supplyPoint struct {
	Loop       uint32
	Used, Made float32 // Made is not capped at 200
}

// supplyCurves reconstructs each player's supply at every loop from births,
// deaths and morphs: a unit takes its supply from when its production
// started until it is gone (see unitLifetimes), and a supply structure gives
// its supply from when it finishes. The replay's own stats say the same only
// every 10 s.
func supplyCurves(tl *pb.Timeline) map[uint32][]supplyPoint {
	type delta struct {
		loop       uint32
		used, made float32
	}
	deltas := map[uint32][]delta{}
	lt := unitLifetimes(tl)
	for _, u := range tl.Units {
		if u.Owner == 0 {
			continue
		}
		// Used: from the production start, changing with morphs.
		name := typeName(tl, u.Type)
		start, end := lt.start[u.Id], lt.end[u.Id]
		if start < end {
			cur := supplyCost[name]
			add := func(l uint32, v float32) {
				if v != 0 {
					deltas[u.Owner] = append(deltas[u.Owner], delta{loop: l, used: v})
				}
			}
			add(start, cur)
			for _, m := range u.Morphs {
				if m.Loop >= end {
					break
				}
				c := supplyCost[typeName(tl, m.Type)]
				add(max(m.Loop, start), c-cur)
				cur = c
			}
			if end < tl.GetMeta().GetLoops() || u.DiedLoop != nil {
				add(end, -cur)
			}
		}
		// Made: from when it is finished.
		end = endLoop(tl, u)
		ready := u.BornLoop
		if u.Constructed {
			if u.DoneLoop == nil {
				continue
			}
			ready = *u.DoneLoop
		}
		if ready >= end {
			continue
		}
		cur := float32(0)
		set := func(l uint32, n string) {
			if v := supplyProvided[n]; v != cur {
				deltas[u.Owner] = append(deltas[u.Owner], delta{loop: max(l, ready), made: v - cur})
				cur = v
			}
		}
		set(ready, nameAt(tl, u, ready))
		for _, m := range u.Morphs {
			if m.Loop >= end {
				break
			}
			if m.Loop > ready {
				set(m.Loop, typeName(tl, m.Type))
			}
		}
		if u.DiedLoop != nil && cur != 0 {
			deltas[u.Owner] = append(deltas[u.Owner], delta{loop: end, made: -cur})
		}
	}
	out := map[uint32][]supplyPoint{}
	for p, ds := range deltas {
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].loop < ds[j].loop })
		var pts []supplyPoint
		var used, made float32
		for i, d := range ds {
			used += d.used
			made += d.made
			if i+1 < len(ds) && ds[i+1].loop == d.loop {
				continue
			}
			if n := len(pts); n > 0 && pts[n-1].Used == used && pts[n-1].Made == made {
				continue
			}
			pts = append(pts, supplyPoint{d.loop, used, made})
		}
		out[p] = pts
	}
	return out
}

// supplyAt is the supply at loop on a curve.
func supplyAt(pts []supplyPoint, loop uint32) supplyPoint {
	i := sort.Search(len(pts), func(i int) bool { return pts[i].Loop > loop })
	if i == 0 {
		return supplyPoint{Loop: loop}
	}
	return pts[i-1]
}

// supplyMatches reports how many of the replay's stats samples the curves
// agree with (supply used within 1, supply made equal), and of how many.
func supplyMatches(tl *pb.Timeline, curves map[uint32][]supplyPoint) (ok, n int) {
	for _, e := range tl.Events {
		ps := e.GetPlayerStats()
		if ps == nil || ps.Player == 0 || e.Loop == 0 {
			continue
		}
		n++
		s := supplyAt(curves[ps.Player], e.Loop)
		d := s.Used - ps.SupplyUsed
		if d <= 1 && d >= -1 && min(s.Made, 200) == ps.SupplyMade {
			ok++
		}
	}
	return ok, n
}

func init() { Register(SupplyBlocks{}) }

// SupplyBlocks finds the spans when a player had no free supply (and wasn't
// maxed out), so couldn't start new units. The edges come from the supply
// reconstructed at every loop (supplyCurves), and the replay's stats, every
// 10 s, check it. The game takes supply when a unit is queued, so the stats
// also count units waiting behind others, which the reconstruction can't
// see: a block the stats show is kept even when the reconstruction misses
// it, and a block they contradict is dropped.
type SupplyBlocks struct{}

func (SupplyBlocks) ID() string { return "supply-blocks" }

// MinSupplyBlock is the shortest block reported (1 s on the clock).
const MinSupplyBlock = 22

type supplySpan struct {
	start, end uint32
	used, made float32
	fromStats  bool // found by a stats sample, not on the curve
	confirmed  bool // a stats sample saw it
	dropped    bool // contradicted by a stats sample
}

func blocked(used, made float32) bool { return made > 0 && made < 200 && used >= made }

func (SupplyBlocks) Analyze(tl *pb.Timeline) (*Report, error) {
	curves := supplyCurves(tl)
	last := tl.GetMeta().GetLoops()
	stats := map[uint32][]supplyPoint{}
	for _, e := range tl.Events {
		if ps := e.GetPlayerStats(); ps != nil && ps.Player != 0 && e.Loop > 0 {
			stats[ps.Player] = append(stats[ps.Player], supplyPoint{e.Loop, ps.SupplyUsed, ps.SupplyMade})
		}
	}
	rep := &Report{Title: "Supply blocks"}
	for _, p := range tl.GetMeta().GetPlayers() {
		spans := blockSpans(curves[p.Id], stats[p.Id], last)
		t := Table{Title: playerName(tl, p.Id), Player: p.Id, Columns: []string{"Time", "Length", "Supply"}}
		for _, s := range spans {
			supply := fmt.Sprintf("%s/%d", formatSupply(s.used), int(s.made))
			rep.Markers = append(rep.Markers, Marker{Loop: s.start, End: s.end, Player: p.Id, Kind: "supply-block",
				Label: fmt.Sprintf("%s supply blocked at %s", playerName(tl, p.Id), supply)})
			t.Rows = append(t.Rows, Row{Loop: s.start, Cells: []string{clock(s.start), clock(s.end - s.start), supply}})
		}
		rep.Tables = append(rep.Tables, t)
	}
	sort.SliceStable(rep.Markers, func(i, j int) bool { return rep.Markers[i].Loop < rep.Markers[j].Loop })
	return rep, nil
}

// blockSpans finds one player's supply blocks on the reconstructed curve,
// checked against the stats samples.
func blockSpans(curve, samples []supplyPoint, last uint32) []supplySpan {
	var spans []supplySpan
	for i, pt := range curve {
		if !blocked(pt.Used, pt.Made) {
			continue
		}
		if n := len(spans); n > 0 && spans[n-1].end == pt.Loop {
			spans[n-1].end = next(curve, i, last)
			continue
		}
		spans = append(spans, supplySpan{start: pt.Loop, end: next(curve, i, last), used: pt.Used, made: pt.Made})
	}
	prev := uint32(0)
	for k, s := range samples {
		var in *supplySpan
		for i := range spans {
			if spans[i].start <= s.Loop && s.Loop < spans[i].end {
				in = &spans[i]
			}
		}
		switch {
		case blocked(s.Used, s.Made) && in == nil:
			// Units waiting in queues. Blocked since a unit last started, if
			// one did after the previous sample, until more supply is ready
			// or a sample says otherwise.
			i := sort.Search(len(curve), func(i int) bool { return curve[i].Loop > s.Loop })
			start, end := s.Loop, last
			for j := i - 1; j > 0 && curve[j].Loop > prev; j-- {
				if curve[j].Used > curve[j-1].Used {
					start = curve[j].Loop
					break
				}
			}
			for j := max(i, 1); j < len(curve); j++ {
				if curve[j].Made > curve[j-1].Made {
					end = curve[j].Loop
					break
				}
			}
			for _, n := range samples[k+1:] {
				if !blocked(n.Used, n.Made) {
					end = min(end, n.Loop)
					break
				}
			}
			spans = append(spans, supplySpan{start: start, end: end, used: s.Used, made: s.Made, fromStats: true})
		case blocked(s.Used, s.Made):
			in.confirmed = true
		case in != nil && !in.fromStats && s.Loop >= in.start+2*MinSupplyBlock:
			// Contradicted: wrong unless a sample saw it, and then it ended.
			if in.confirmed {
				in.end = s.Loop
			} else {
				in.dropped = true
			}
		}
		prev = s.Loop
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out []supplySpan
	for _, s := range spans {
		if s.dropped {
			continue
		}
		if n := len(out); n > 0 && s.start <= out[n-1].end+MinSupplyBlock {
			out[n-1].end = max(out[n-1].end, s.end)
			continue
		}
		out = append(out, s)
	}
	kept := out[:0]
	for _, s := range out {
		if s.end-s.start >= MinSupplyBlock {
			kept = append(kept, s)
		}
	}
	return kept
}

// next is the loop of the curve point after i, or last.
func next(curve []supplyPoint, i int, last uint32) uint32 {
	if i+1 < len(curve) {
		return curve[i+1].Loop
	}
	return last
}

// formatSupply shows supply the way the game does: whole, or with a half
// for zerglings.
func formatSupply(v float32) string {
	if v == float32(int(v)) {
		return fmt.Sprint(int(v))
	}
	return fmt.Sprintf("%.1f", v)
}
