// SPDX-License-Identifier: Apache-2.0

package accuracy

import (
	"math"
	"sort"

	enginev1 "github.com/dmang-dev/ghostreel/gen/engine/v1"
	"github.com/dmang-dev/ghostreel/internal/replay"
	"github.com/dmang-dev/ghostreel/internal/selection"
)

// SelectionReport says how well the selection tracker attributes commands to
// units, judged by the orders the engine shows right after each command.
type SelectionReport struct {
	Commands int // commands checked
	// PointRecall is Recall over point-target commands only (unit targets can
	// match units that picked the same target on their own).
	PointRecall float64
	pointN      int
	Exact       int     // commands whose tracked selection equals the units given the order
	Precision   float64 // mean share of tracked units that did get the order
	Recall      float64 // mean share of ordered units the tracker had selected
}

// CheckSelection replays the selection tracker over a replay's events and
// compares, for each targeted command of the observed player, the tracked
// selection with the units the engine reports newly carrying an order to that
// target in the next frame. Commands followed by another command before that
// frame are skipped (the order may already have been replaced).
//
// Bots order one unit per command, often several to the same spot in one
// loop or a few loops apart, so a unit's order in the next frame may come
// from any command since the previous frame. A command is judged together
// with every command of the player in that span whose target matches it
// (the same unit, or a point within half a cell): the union of their tracked
// selections against the ordered units. Units that already carried the order
// in the previous frame, and weren't selected, are left out.
func CheckSelection(events []replay.Event, s *enginev1.Samples) SelectionReport {
	var rep SelectionReport
	player := int(s.ObservedPlayer)
	frames := s.Frames
	// frameAfter is the index of the first frame after loop.
	frameAfter := func(loop int64) int {
		return sort.Search(len(frames), func(i int) bool { return int64(frames[i].Loop) > loop })
	}
	type cmd struct {
		e   *replay.Event
		sel []replay.Tag
	}
	var cmds []cmd
	tr := selection.New()
	for i := range events {
		e := &events[i]
		tr.Apply(e)
		if e.Kind == replay.KindCommand && e.Player == player {
			c := cmd{e: e}
			if e.HasTarget && !e.Queued {
				c.sel = tr.Selected(player)
			}
			cmds = append(cmds, c)
		}
	}
	sameTarget := func(a, b *replay.Event) bool {
		if a.TargetTag != 0 || b.TargetTag != 0 {
			return a.TargetTag == b.TargetTag
		}
		return math.Hypot(a.X-b.X, a.Y-b.Y) < 0.5
	}
	var precSum, recSum float64
	for ci, c := range cmds {
		e := c.e
		if !e.HasTarget || e.Queued {
			continue
		}
		fi := frameAfter(e.Loop)
		if fi == len(frames) {
			continue
		}
		next := ci + 1
		for next < len(cmds) && cmds[next].e.Loop == e.Loop {
			next++
		}
		if next < len(cmds) && cmds[next].e.Loop < int64(frames[fi].Loop) {
			continue
		}
		// Only the first command of a matching group in a loop is judged.
		first := true
		for k := ci - 1; k >= 0 && cmds[k].e.Loop == e.Loop; k-- {
			if cmds[k].sel != nil && sameTarget(cmds[k].e, e) {
				first = false
				break
			}
		}
		if !first {
			continue
		}
		var from int64 = -1
		if fi > 0 {
			from = int64(frames[fi-1].Loop)
		}
		got := map[uint32]bool{}
		for k := ci; k >= 0 && cmds[k].e.Loop > from; k-- {
			if cmds[k].sel != nil && sameTarget(cmds[k].e, e) {
				for _, t := range cmds[k].sel {
					got[uint32(t)] = true
				}
			}
		}
		for k := ci + 1; k < len(cmds) && cmds[k].e.Loop == e.Loop; k++ {
			if cmds[k].sel != nil && sameTarget(cmds[k].e, e) {
				for _, t := range cmds[k].sel {
					got[uint32(t)] = true
				}
			}
		}
		judge(e, got, frames, fi, player, &rep, &precSum, &recSum)
	}
	if rep.Commands > 0 {
		rep.Precision = precSum / float64(rep.Commands)
		rep.Recall = recSum / float64(rep.Commands)
	}
	if rep.pointN > 0 {
		rep.PointRecall /= float64(rep.pointN)
	}
	return rep
}

// judge scores one (grouped) command against frame fi.
func judge(e *replay.Event, got map[uint32]bool, frames []*enginev1.Frame, fi, player int,
	rep *SelectionReport, precSum, recSum *float64) {
	had := map[uint32]bool{}
	if fi > 0 {
		for _, u := range frames[fi-1].Units {
			if int(u.Owner) == player && hasOrder(e, u) {
				had[EngineTag(u.Tag)] = true
			}
		}
	}
	want := map[uint32]bool{}
	for _, u := range frames[fi].Units {
		if int(u.Owner) != player || !hasOrder(e, u) {
			continue
		}
		t := EngineTag(u.Tag)
		if got[t] || !had[t] {
			want[t] = true
		}
	}
	if len(want) == 0 {
		return // finished instantly, or a command without a lasting order
	}
	rep.Commands++
	inter := 0
	for t := range want {
		if got[t] {
			inter++
		}
	}
	if len(got) > 0 {
		*precSum += float64(inter) / float64(len(got))
	}
	*recSum += float64(inter) / float64(len(want))
	if e.TargetTag == 0 {
		rep.PointRecall += float64(inter) / float64(len(want))
		rep.pointN++
	}
	if inter == len(want) && inter == len(got) {
		rep.Exact++
	}
}

func hasOrder(e *replay.Event, u *enginev1.UnitSample) bool {
	for _, o := range u.Orders {
		if matchesOrder(e, o) {
			return true
		}
	}
	return false
}

func matchesOrder(e *replay.Event, o *enginev1.Order) bool {
	if e.TargetTag != 0 {
		return EngineTag(o.TargetTag) == uint32(e.TargetTag)
	}
	return o.HasPoint && math.Hypot(float64(o.TargetX)-e.X, float64(o.TargetY)-e.Y) < 0.5
}
