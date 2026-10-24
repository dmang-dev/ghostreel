// SPDX-License-Identifier: Apache-2.0

package command

import (
	"math"
	"testing"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
	"github.com/OWNER/ghostreel/internal/pathing"
	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/timeline"
)

var tag = replay.MakeTag

func selectTags(loop int64, tags ...replay.Tag) replay.Event {
	return replay.Event{Loop: loop, Kind: replay.KindSelection, Player: 1, Selection: &replay.SelectionChange{
		Group: 10, Remove: replay.Mask{Kind: replay.MaskZeroIndices}, AddTags: tags,
	}}
}

func moveTo(loop int64, x, y float64) replay.Event {
	return replay.Event{Loop: loop, Kind: replay.KindCommand, Player: 1, AbilityLink: -1, HasTarget: true, X: x, Y: y}
}

func build(t *testing.T, ev []replay.Event, grid *pathing.Grid) (*pb.Timeline, *timeline.Index) {
	t.Helper()
	info := &replay.Info{Loops: 2000, MapWidth: 64, MapHeight: 64, HasTracker: true,
		Players: []replay.Player{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}}
	tl, err := timeline.Build(info, ev, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	Apply(tl, ev, Options{Grid: grid})
	return tl, timeline.NewIndex(tl)
}

func posOf(ix *timeline.Index, name string, loop uint32) (float32, float32) {
	for _, u := range ix.Seek(loop).Units {
		if ix.TL.UnitTypes[u.Type].Name == name {
			return u.X, u.Y
		}
	}
	return -1, -1
}

func near(x, y float32, wx, wy float64) bool {
	return math.Hypot(float64(x)-wx, float64(y)-wy) < 0.3
}

func TestMoveCommandAndSample(t *testing.T) {
	ev := []replay.Event{
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(1, 1), UnitType: "Marine", Player: 1, X: 10, Y: 10},
		selectTags(100, tag(1, 1)),
		moveTo(100, 30.5, 10.5),
		// A sample says the marine was actually somewhere else at 300.
		{Loop: 300, Kind: replay.KindUnitPositions, Positions: []replay.PositionSample{{Index: 1, X: 20, Y: 20}}},
	}
	tl, ix := build(t, ev, nil)
	if tl.Meta.Fidelity != pb.Source_SOURCE_COMMAND {
		t.Fatal("fidelity not command")
	}
	// Marine speed 2.25 cells/game-second = 0.140625 cells/loop: 20 cells take 142 loops.
	if x, y := posOf(ix, "Marine", 100); !near(x, y, 10.5, 10.5) {
		t.Errorf("at the order: (%v,%v)", x, y)
	}
	if x, y := posOf(ix, "Marine", 171); !near(x, y, 20.5, 10.5) {
		t.Errorf("halfway: (%v,%v), want (20.5,10.5)", x, y)
	}
	if x, y := posOf(ix, "Marine", 242); !near(x, y, 30.5, 10.5) {
		t.Errorf("arrived: (%v,%v), want (30.5,10.5)", x, y)
	}
	// The sample wins at its loop; the marine then heads back to its goal.
	if x, y := posOf(ix, "Marine", 300); !near(x, y, 20.5, 20.5) {
		t.Errorf("at the sample: (%v,%v), want (20.5,20.5)", x, y)
	}
	if x, y := posOf(ix, "Marine", 1500); !near(x, y, 30.5, 10.5) {
		t.Errorf("after the sample: (%v,%v), want back at the goal", x, y)
	}
}

func TestQueuedMove(t *testing.T) {
	q := moveTo(100, 10.5, 30.5)
	q.Queued = true
	ev := []replay.Event{
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(1, 1), UnitType: "Marine", Player: 1, X: 10, Y: 10},
		selectTags(100, tag(1, 1)),
		moveTo(100, 30.5, 10.5),
		q,
	}
	_, ix := build(t, ev, nil)
	// 20 cells east, then 20 cells north and 20 west diagonal back... the queued
	// leg starts when the first ends (loop ~242).
	if x, y := posOf(ix, "Marine", 242); !near(x, y, 30.5, 10.5) {
		t.Errorf("end of first leg: (%v,%v)", x, y)
	}
	if x, y := posOf(ix, "Marine", 1900); !near(x, y, 10.5, 30.5) {
		t.Errorf("end of queued leg: (%v,%v)", x, y)
	}
}

func TestRalliedWorkerMines(t *testing.T) {
	ev := []replay.Event{
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(1, 1), UnitType: "CommandCenter", Player: 1, X: 20, Y: 20},
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(2, 1), UnitType: "MineralField", Player: 0, X: 28, Y: 20},
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(9, 1), UnitType: "Marine", Player: 1, X: 5, Y: 5},
		selectTags(10, tag(1, 1)),
		{Loop: 10, Kind: replay.KindCommand, Player: 1, AbilityLink: -1, HasTarget: true, X: 28, Y: 20.5, TargetTag: tag(2, 1)},
		{Loop: 50, Kind: replay.KindUnitBorn, Tag: tag(3, 1), UnitType: "SCV", Player: 1, X: 22, Y: 20, Creator: tag(1, 1)},
	}
	_, ix := build(t, ev, nil)
	// The SCV shuttles between the CC's edge (x≈23.1) and the field (x≈27).
	minX, maxX := float32(100), float32(0)
	for loop := uint32(200); loop < 1000; loop += 4 {
		x, _ := posOf(ix, "SCV", loop)
		minX, maxX = min(minX, x), max(maxX, x)
	}
	if minX > 24 || maxX < 26.5 || maxX > 27.5 {
		t.Errorf("SCV ranged x %v..%v, want a mining shuttle between ~23 and ~27", minX, maxX)
	}
}

func TestWalledInUnitStays(t *testing.T) {
	g := pathing.New(64, 64)
	for y := 0; y < 64; y++ {
		g.Set(32, y, false) // a wall across the whole map
	}
	ev := []replay.Event{
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(1, 1), UnitType: "Marine", Player: 1, X: 10, Y: 10},
		selectTags(100, tag(1, 1)),
		moveTo(100, 50.5, 10.5),
	}
	_, ix := build(t, ev, g)
	if x, y := posOf(ix, "Marine", 1500); !near(x, y, 10.5, 10.5) {
		t.Errorf("walled-in marine moved to (%v,%v)", x, y)
	}
}

func TestNoGameEventsLeavesTimeline(t *testing.T) {
	ev := []replay.Event{{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(1, 1), UnitType: "Marine", Player: 2, X: 10, Y: 10}}
	tl, _ := build(t, ev, nil)
	if tl.Meta.Fidelity != pb.Source_SOURCE_TRACKER {
		t.Error("a replay without commands should stay tracker-tier")
	}
}
