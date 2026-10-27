// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"sort"
	"strings"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

func init() { Register(Army{}) }

// Army shows what each player's army was made of: a table per player with
// the units alive at every minute of the clock, and a chart of army supply.
// The army is every finished unit that takes supply, except workers.
type Army struct{}

func (Army) ID() string { return "army" }

var workerNames = map[string]bool{"SCV": true, "Probe": true, "Drone": true}

func (Army) Analyze(tl *pb.Timeline) (*Report, error) {
	loops := tl.GetMeta().GetLoops()
	type armyUnit struct {
		u        *pb.Unit
		from, to uint32
		owner    uint32
	}
	var army []armyUnit
	for _, u := range tl.Units {
		if u.Owner == 0 || workerNames[displayName(typeName(tl, u.Type))] {
			continue
		}
		from := u.BornLoop
		if u.Constructed {
			if u.DoneLoop == nil {
				continue
			}
			from = *u.DoneLoop // warp-ins count once they're in
		}
		costs := supplyCost[typeName(tl, u.Type)] > 0
		for _, m := range u.Morphs {
			costs = costs || supplyCost[typeName(tl, m.Type)] > 0
		}
		if costs && from < endLoop(tl, u) {
			army = append(army, armyUnit{u: u, from: from, to: endLoop(tl, u), owner: u.Owner})
		}
	}
	rep := &Report{Title: "Army"}
	for _, p := range tl.GetMeta().GetPlayers() {
		// Army supply every 5 s, from changes.
		type delta struct {
			loop uint32
			d    float32
		}
		var ds []delta
		for _, a := range army {
			if a.owner != p.Id {
				continue
			}
			cur := supplyCost[nameAt(tl, a.u, a.from)]
			ds = append(ds, delta{a.from, cur})
			for _, m := range a.u.Morphs {
				if m.Loop <= a.from {
					continue
				}
				if m.Loop >= a.to {
					break
				}
				c := supplyCost[typeName(tl, m.Type)]
				ds = append(ds, delta{m.Loop, c - cur})
				cur = c
			}
			ds = append(ds, delta{a.to, -cur})
		}
		if len(ds) == 0 {
			continue
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i].loop < ds[j].loop })
		s := Series{Title: "Army supply", Player: p.Id}
		var v float32
		j := 0
		for l := uint32(0); l <= loops; l += 112 {
			for j < len(ds) && ds[j].loop <= l {
				v += ds[j].d
				j++
			}
			s.Points = append(s.Points, Point{l, v})
		}
		rep.Series = append(rep.Series, s)

		// What it was made of, every minute and at the end.
		t := Table{Title: playerName(tl, p.Id), Player: p.Id, Columns: []string{"Time", "Supply", "Army"}}
		var at []uint32
		for l := uint32(secs(60)); l < loops; l += secs(60) {
			at = append(at, l)
		}
		at = append(at, max(loops, 1)-1)
		for _, l := range at {
			count := map[string]int{}
			sup := map[string]float32{}
			var total float32
			for _, a := range army {
				if a.owner != p.Id || a.from > l || a.to <= l {
					continue
				}
				n := nameAt(tl, a.u, l)
				c := supplyCost[n]
				if c == 0 {
					continue
				}
				d := displayName(n)
				count[d]++
				sup[d] += c
				total += c
			}
			if total == 0 {
				continue
			}
			names := make([]string, 0, len(count))
			for n := range count {
				names = append(names, n)
			}
			sort.Slice(names, func(i, j int) bool {
				if sup[names[i]] != sup[names[j]] {
					return sup[names[i]] > sup[names[j]]
				}
				return names[i] < names[j]
			})
			parts := make([]string, len(names))
			for i, n := range names {
				parts[i] = fmt.Sprintf("%d %s", count[n], n)
			}
			t.Rows = append(t.Rows, Row{Loop: l, Cells: []string{clock(l), formatSupply(total), strings.Join(parts, ", ")}})
		}
		rep.Tables = append(rep.Tables, t)
	}
	return rep, nil
}
