// SPDX-License-Identifier: Apache-2.0

package accuracy

import (
	"math"
	"sort"

	enginev1 "github.com/OWNER/ghostreel/gen/engine/v1"
	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/selection"
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
// selection with the units the engine reports carrying an order to that
// target in the next frame. Commands followed by another command before that
// frame are skipped (the order may already have been replaced).
func CheckSelection(events []replay.Event, s *enginev1.Samples) SelectionReport {
	var rep SelectionReport
	player := int(s.ObservedPlayer)
	frames := s.Frames
	frameAfter := func(loop int64) *enginev1.Frame {
		i := sort.Search(len(frames), func(i int) bool { return int64(frames[i].Loop) > loop })
		if i == len(frames) {
			return nil
		}
		return frames[i]
	}
	// Loops of this player's commands, to skip commands quickly overridden.
	var cmdLoops []int64
	for i := range events {
		if events[i].Kind == replay.KindCommand && events[i].Player == player {
			if n := len(cmdLoops); n == 0 || cmdLoops[n-1] != events[i].Loop {
				cmdLoops = append(cmdLoops, events[i].Loop)
			}
		}
	}
	nextCmdLoop := func(after int64) int64 {
		i := sort.Search(len(cmdLoops), func(i int) bool { return cmdLoops[i] > after })
		if i == len(cmdLoops) {
			return math.MaxInt64
		}
		return cmdLoops[i]
	}
	// Bots often give the same order to several units in one loop, one unit
	// per command. Commands with the same target in the same loop are judged
	// together: the union of their selections against the ordered units.
	type key struct {
		tag  replay.Tag
		x, y int64
	}
	type pending struct {
		e   *replay.Event
		got map[uint32]bool
	}
	var batch []*pending
	batchKeys := map[key]*pending{}
	var precSum, recSum float64
	flush := func() {
		for _, p := range batch {
			judge(p.e, p.got, frameAfter, nextCmdLoop, player, &rep, &precSum, &recSum)
		}
		batch, batchKeys = nil, map[key]*pending{}
	}
	tr := selection.New()
	for i := range events {
		e := &events[i]
		if len(batch) > 0 && e.Loop != batch[0].e.Loop {
			flush()
		}
		tr.Apply(e)
		if e.Kind != replay.KindCommand || e.Player != player || !e.HasTarget || e.Queued {
			continue
		}
		k := key{e.TargetTag, int64(math.Round(e.X * 2)), int64(math.Round(e.Y * 2))}
		p := batchKeys[k]
		if p == nil {
			p = &pending{e: e, got: map[uint32]bool{}}
			batchKeys[k] = p
			batch = append(batch, p)
		}
		for _, t := range tr.Selected(player) {
			p.got[uint32(t)] = true
		}
	}
	flush()
	if rep.Commands > 0 {
		rep.Precision = precSum / float64(rep.Commands)
		rep.Recall = recSum / float64(rep.Commands)
	}
	if rep.pointN > 0 {
		rep.PointRecall /= float64(rep.pointN)
	}
	return rep
}

// judge scores one (batched) command against the next engine frame.
func judge(e *replay.Event, got map[uint32]bool, frameAfter func(int64) *enginev1.Frame,
	nextCmdLoop func(int64) int64, player int, rep *SelectionReport, precSum, recSum *float64) {
	f := frameAfter(e.Loop)
	if f == nil || nextCmdLoop(e.Loop) < int64(f.Loop) {
		return
	}
	want := map[uint32]bool{}
	for _, u := range f.Units {
		if int(u.Owner) != player {
			continue
		}
		for _, o := range u.Orders {
			if matchesOrder(e, o) {
				want[EngineTag(u.Tag)] = true
				break
			}
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

func matchesOrder(e *replay.Event, o *enginev1.Order) bool {
	if e.TargetTag != 0 {
		return EngineTag(o.TargetTag) == uint32(e.TargetTag)
	}
	return o.HasPoint && math.Hypot(float64(o.TargetX)-e.X, float64(o.TargetY)-e.Y) < 0.5
}
