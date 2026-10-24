// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
)

func init() {
	Register(Economy{})
	Register(SupplyBlocks{})
}

// Economy charts each player's income, workers, army value and supply from
// the replay's periodic player stats (every 10 s on the clock).
type Economy struct{}

func (Economy) ID() string { return "economy" }

func (Economy) Analyze(tl *pb.Timeline) (*Report, error) {
	type metric struct {
		title, unit string
		value       func(*pb.PlayerStats) float32
	}
	metrics := []metric{
		{"Income", "/min", func(s *pb.PlayerStats) float32 { return float32(s.MineralRate + s.VespeneRate) }},
		{"Workers", "", func(s *pb.PlayerStats) float32 { return float32(s.Workers) }},
		{"Army value", "", func(s *pb.PlayerStats) float32 { return float32(s.ArmyMinerals + s.ArmyVespene) }},
		{"Supply", "", func(s *pb.PlayerStats) float32 { return s.SupplyUsed }},
		{"Resources lost", "", func(s *pb.PlayerStats) float32 { return float32(s.LostMinerals + s.LostVespene) }},
	}
	series := map[[2]uint32]*Series{} // metric index, player
	for _, e := range tl.Events {
		ps := e.GetPlayerStats()
		if ps == nil || ps.Player == 0 {
			continue
		}
		for i, m := range metrics {
			k := [2]uint32{uint32(i), ps.Player}
			s := series[k]
			if s == nil {
				s = &Series{Title: m.title, Player: ps.Player, Unit: m.unit}
				series[k] = s
			}
			s.Points = append(s.Points, Point{e.Loop, m.value(ps)})
		}
	}
	rep := &Report{Title: "Economy"}
	for i := range metrics {
		for _, p := range tl.GetMeta().GetPlayers() {
			if s := series[[2]uint32{uint32(i), p.Id}]; s != nil {
				rep.Series = append(rep.Series, *s)
			}
		}
	}
	return rep, nil
}

// SupplyBlocks finds the spans when a player had no free supply (and wasn't
// maxed out), so couldn't start new units. It reads the periodic player
// stats, so a block shorter than one stats interval (10 s) can be missed and
// edges are accurate only to that interval.
type SupplyBlocks struct{}

func (SupplyBlocks) ID() string { return "supply-blocks" }

func (SupplyBlocks) Analyze(tl *pb.Timeline) (*Report, error) {
	type state struct {
		blocked bool
		start   uint32
		supply  float32
	}
	open := map[uint32]*state{}
	var blocks []Marker
	rep := &Report{Title: "Supply blocks"}
	byPlayer := map[uint32][]Row{}
	close := func(p uint32, st *state, end uint32) {
		blocks = append(blocks, Marker{Loop: st.start, End: end, Player: p, Kind: "supply-block",
			Label: fmt.Sprintf("%s supply blocked at %d", playerName(tl, p), int(st.supply))})
		byPlayer[p] = append(byPlayer[p], Row{Loop: st.start, Cells: []string{clock(st.start), clock(end - st.start), fmt.Sprint(int(st.supply))}})
		st.blocked = false
	}
	for _, e := range tl.Events {
		ps := e.GetPlayerStats()
		if ps == nil || ps.Player == 0 {
			continue
		}
		st := open[ps.Player]
		if st == nil {
			st = &state{}
			open[ps.Player] = st
		}
		blocked := ps.SupplyMade > 0 && ps.SupplyMade < 200 && ps.SupplyUsed >= ps.SupplyMade
		switch {
		case blocked && !st.blocked:
			st.blocked, st.start, st.supply = true, e.Loop, ps.SupplyUsed
		case !blocked && st.blocked:
			close(ps.Player, st, e.Loop)
		}
	}
	for p, st := range open {
		if st.blocked {
			close(p, st, tl.GetMeta().GetLoops())
		}
	}
	rep.Markers = blocks
	for _, p := range tl.GetMeta().GetPlayers() {
		rep.Tables = append(rep.Tables, Table{Title: playerName(tl, p.Id), Player: p.Id,
			Columns: []string{"Time", "Length", "Supply"}, Rows: byPlayer[p.Id]})
	}
	return rep, nil
}
