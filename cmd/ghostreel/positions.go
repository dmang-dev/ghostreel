// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"sort"

	"github.com/OWNER/ghostreel/internal/replay"
)

// printPositionStats reports how much UnitPositions sampling a replay holds:
// how often samples come, and which unit types ever get one. It exists to
// check the tracker tier's assumptions against real files.
func printPositionStats(r *replay.Replay) {
	type unitInfo struct {
		typ     string
		owner   int
		sampled bool
	}
	byIndex := map[uint32]*unitInfo{}
	var all []*unitInfo
	var loops []int64
	samples, unknown := 0, 0
	maxFrac := 0.0
	for _, e := range r.Events {
		switch e.Kind {
		case replay.KindUnitBorn, replay.KindUnitInit:
			u := &unitInfo{typ: e.UnitType, owner: e.Player}
			byIndex[e.Tag.Index()] = u
			all = append(all, u)
		case replay.KindUnitTypeChange:
			if u := byIndex[e.Tag.Index()]; u != nil {
				u.typ = e.UnitType
			}
		case replay.KindUnitDied:
			delete(byIndex, e.Tag.Index())
		case replay.KindUnitPositions:
			loops = append(loops, e.Loop)
			samples += len(e.Positions)
			for _, p := range e.Positions {
				if u := byIndex[p.Index]; u != nil {
					u.sampled = true
				} else {
					unknown++
				}
			}
			alive := 0
			for _, u := range byIndex {
				if u.owner != 0 {
					alive++
				}
			}
			if alive > 0 {
				if f := float64(len(e.Positions)) / float64(alive); f > maxFrac {
					maxFrac = f
				}
			}
		}
	}
	fmt.Printf("Positions: %d events, %d samples (%d for unknown slots)\n", len(loops), samples, unknown)
	if len(loops) > 1 {
		gaps := map[int64]int{}
		for i := 1; i < len(loops); i++ {
			gaps[loops[i]-loops[i-1]]++
		}
		fmt.Printf("  interval (loops: count): %v\n", gaps)
		fmt.Printf("  largest sample covered %.0f%% of owned live units\n", maxFrac*100)
	}
	type row struct {
		typ            string
		total, sampled int
	}
	rows := map[string]*row{}
	totalOwned, sampledOwned := 0, 0
	for _, u := range all {
		if u.owner == 0 {
			continue
		}
		rw := rows[u.typ]
		if rw == nil {
			rw = &row{typ: u.typ}
			rows[u.typ] = rw
		}
		rw.total++
		totalOwned++
		if u.sampled {
			rw.sampled++
			sampledOwned++
		}
	}
	if totalOwned > 0 {
		fmt.Printf("  owned units ever sampled: %d of %d (%.0f%%)\n", sampledOwned, totalOwned,
			100*float64(sampledOwned)/float64(totalOwned))
	}
	list := make([]*row, 0, len(rows))
	for _, rw := range rows {
		list = append(list, rw)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].total > list[j].total })
	for i, rw := range list {
		if i == 15 {
			break
		}
		fmt.Printf("  %-24s %3d/%-3d sampled\n", rw.typ, rw.sampled, rw.total)
	}
}
