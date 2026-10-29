// SPDX-License-Identifier: Apache-2.0

package analysis

import pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"

func init() { Register(Economy{}) }

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
