// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/replay"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

// typeIndex adds a unit type to tl (or finds it) and returns its index.
func typeIndex(tl *pb.Timeline, name string, class pb.UnitClass) uint32 {
	for i, t := range tl.UnitTypes {
		if t.Name == name {
			return uint32(i)
		}
	}
	tl.UnitTypes = append(tl.UnitTypes, &pb.UnitType{Name: name, Class: class})
	return uint32(len(tl.UnitTypes) - 1)
}

const (
	army     = pb.UnitClass_UNIT_CLASS_ARMY
	worker   = pb.UnitClass_UNIT_CLASS_WORKER
	building = pb.UnitClass_UNIT_CLASS_BUILDING
)

// unit adds a unit by type name.
func unit(tl *pb.Timeline, owner uint32, name string, class pb.UnitClass, born uint32, died *uint32) *pb.Unit {
	return addUnit(tl, owner, typeIndex(tl, name, class), born, 10, 10, died)
}

// terranStart is a Terran player 1 with a Command Center and 12 SCVs:
// 12/15 supply.
func terranStart() *pb.Timeline {
	tl := game()
	tl.Meta.Loops = 4000
	unit(tl, 1, "CommandCenter", building, 0, nil)
	for i := 0; i < 12; i++ {
		unit(tl, 1, "SCV", worker, 0, nil)
	}
	return tl
}

func TestSupplyCurve(t *testing.T) {
	tl := terranStart()
	cc := tl.Units[0]
	// Three more SCVs, back to back: the last one fills supply at 15/15.
	for i := uint32(1); i <= 3; i++ {
		scv := unit(tl, 1, "SCV", worker, 300*i, nil)
		scv.Creator = u32(cc.Id)
	}
	// A depot placed at 400, done at 1100.
	depot := unit(tl, 1, "SupplyDepot", building, 400, nil)
	depot.Constructed, depot.DoneLoop = true, u32(1100)
	// A marine that dies.
	m := unit(tl, 1, "Marine", army, 2000, u32(2500))
	m.Creator = u32(cc.Id)

	c := supplyCurves(tl)[1]
	for _, tc := range []struct {
		loop       uint32
		used, made float32
	}{
		{0, 12, 15}, {30, 12, 15}, {31, 13, 15}, // 1st SCV started 269 loops before 300
		{331, 14, 15}, {631, 15, 15}, {1100, 15, 23},
		{1597, 16, 23}, {2499, 16, 23}, {2500, 15, 23},
	} {
		got := supplyAt(c, tc.loop)
		if got.Used != tc.used || got.Made != tc.made {
			t.Errorf("at %d: %v/%v, want %v/%v", tc.loop, got.Used, got.Made, tc.used, tc.made)
		}
	}

	rep, err := SupplyBlocks{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Markers) != 1 || rep.Markers[0].Loop != 631 || rep.Markers[0].End != 1100 {
		t.Fatalf("blocks = %+v", rep.Markers)
	}
	if got := strings.Join(rep.Tables[0].Rows[0].Cells, " "); got != "0:28 0:20 15/15" {
		t.Errorf("row = %q", got)
	}
}

func TestSupplyBlocksCheckedByStats(t *testing.T) {
	tl := terranStart()
	stat := func(loop uint32, used, made float32) {
		event(tl, loop, &pb.Event_PlayerStats{PlayerStats: &pb.PlayerStats{Player: 1, SupplyUsed: used, SupplyMade: made}})
	}
	// The curve says 12/15 throughout, but the stats see 15/15 at 800 and
	// 960 (units in queues the timeline can't see), free again at 1120.
	stat(640, 12, 15)
	stat(800, 15, 15)
	stat(960, 15, 15)
	stat(1120, 14, 15)
	// And the curve thinks player 1 is blocked from 2000 (three SCVs too
	// early, say), which a sample at 2200 contradicts.
	for i := uint32(0); i < 3; i++ {
		unit(tl, 1, "SCV", worker, 2269+i, nil).Creator = u32(0)
	}
	stat(2200, 14, 15)
	rep, err := SupplyBlocks{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Markers) != 1 || rep.Markers[0].Loop != 800 || rep.Markers[0].End != 1120 {
		t.Errorf("blocks = %+v", rep.Markers)
	}
}

func TestZergSupply(t *testing.T) {
	tl := game()
	tl.Meta.Loops = 4000
	egg := typeIndex(tl, "Egg", army)
	hatch := unit(tl, 1, "Hatchery", building, 0, nil)
	larva := unit(tl, 1, "Larva", army, 0, nil)
	larva.Creator = u32(hatch.Id)
	larva.Morphs = []*pb.Morph{{Loop: 100, Type: egg}}
	// The drone hatches at 380; its egg was started at 100.
	drone := unit(tl, 1, "Drone", worker, 380, u32(2010))
	drone.Creator = u32(larva.Id)
	// It starts a pool at 1000, which is done at 2000; the replay says the
	// drone died at 2010.
	pool := unit(tl, 1, "SpawningPool", building, 1000, nil)
	pool.Constructed, pool.DoneLoop = true, u32(2000)

	lt := unitLifetimes(tl)
	if lt.start[drone.Id] != 100 || lt.end[drone.Id] != 1000 {
		t.Errorf("drone counts %d-%d, want 100-1000", lt.start[drone.Id], lt.end[drone.Id])
	}
	c := supplyCurves(tl)[1]
	if s := supplyAt(c, 500); s.Used != 1 || s.Made != 6 {
		t.Errorf("at 500: %+v", s)
	}
	if s := supplyAt(c, 1500); s.Used != 0 {
		t.Errorf("at 1500: %+v", s)
	}
}

// TestSupplyReal checks the reconstructed supply against the replays' own
// stats: they should agree (within 1) nearly always. Queued units, which the
// stats count and the timeline can't see, and Chrono Boost account for most
// of the rest.
func TestSupplyReal(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "replays", "2022*.SC2Replay"))
	if len(paths) == 0 {
		t.Skip("no replays in testdata/replays")
	}
	for _, path := range paths {
		r, err := replay.OpenWith(path, replay.Options{SkipGameEvents: true})
		if err != nil {
			t.Fatal(err)
		}
		tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{})
		if err != nil {
			t.Fatal(err)
		}
		ok, n := supplyMatches(tl, supplyCurves(tl))
		t.Logf("%s: supply matches the stats at %d of %d samples", filepath.Base(path), ok, n)
		if ok*100 < n*70 {
			t.Errorf("%s: supply matches only %d of %d samples", filepath.Base(path), ok, n)
		}
	}
}
