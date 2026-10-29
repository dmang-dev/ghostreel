// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
)

func init() { Register(APM{}) }

// APM charts each player's actions per minute over the game, from the
// action counts the timeline keeps: commands, selection changes and
// control-group updates, but not camera moves. Each point is the rate over
// the minute before it (less at the start). A table gives each player's
// average over the whole game, as the game's score screen does.
type APM struct{}

func (APM) ID() string { return "apm" }

// apmWindow is the span each chart point averages over (1 minute).
const apmWindow = 60 * 22.4

func (APM) Analyze(tl *pb.Timeline) (*Report, error) {
	rep := &Report{Title: "APM"}
	ac := tl.GetActions()
	if ac == nil || ac.BinLoops == 0 {
		return rep, nil // built without game events: nothing counted
	}
	bin := ac.BinLoops
	loops := tl.GetMeta().GetLoops()
	bins := int((loops + bin - 1) / bin)
	window := max(1, int(apmWindow/float64(bin)+0.5))
	perMinute := func(n uint32, binsCounted int) float32 {
		return float32(float64(n) * apmWindow / float64(binsCounted*int(bin)))
	}
	summary := Table{Title: "Average", Columns: []string{"Player", "APM", "Commands/min", "Actions"}}
	for _, p := range ac.Players {
		if p.Player == 0 {
			continue
		}
		apm := Series{Title: "APM", Player: p.Player}
		cpm := Series{Title: "Commands", Player: p.Player, Unit: "/min"}
		var sumA, sumC, totA, totC uint32
		at := func(v []uint32, i int) uint32 {
			if i < len(v) {
				return v[i]
			}
			return 0
		}
		for i := 0; i < bins; i++ {
			sumA += at(p.Actions, i)
			sumC += at(p.Commands, i)
			if i >= window {
				sumA -= at(p.Actions, i-window)
				sumC -= at(p.Commands, i-window)
			}
			n := min(i+1, window)
			l := min(uint32(i+1)*bin, loops)
			apm.Points = append(apm.Points, Point{l, perMinute(sumA, n)})
			cpm.Points = append(cpm.Points, Point{l, perMinute(sumC, n)})
		}
		for i := range p.Actions {
			totA += p.Actions[i]
			totC += at(p.Commands, i)
		}
		if totA == 0 {
			continue // the built-in AI records no actions
		}
		rep.Series = append(rep.Series, apm, cpm)
		minutes := float64(loops) / apmWindow
		summary.Rows = append(summary.Rows, Row{Loop: 0, Cells: []string{
			playerName(tl, p.Player),
			fmt.Sprint(int(float64(totA)/minutes + 0.5)),
			fmt.Sprint(int(float64(totC)/minutes + 0.5)),
			fmt.Sprint(totA),
		}})
	}
	// Both charts list the players in the same order: APM lines, then
	// commands.
	var apms, cpms []Series
	for _, s := range rep.Series {
		if s.Title == "APM" {
			apms = append(apms, s)
		} else {
			cpms = append(cpms, s)
		}
	}
	rep.Series = append(apms, cpms...)
	if len(summary.Rows) > 0 {
		rep.Tables = []Table{summary}
	}
	return rep, nil
}
