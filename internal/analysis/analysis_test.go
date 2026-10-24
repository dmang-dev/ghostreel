// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/replay"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

func u32(v uint32) *uint32 { return &v }

// game builds a small two-player timeline. Unit types: 0 Marine (army),
// 1 Barracks (building), 2 SCV (worker), 3 Larva, 4 OrbitalCommand,
// 5 BarracksFlying.
func game() *pb.Timeline {
	tl := &pb.Timeline{
		Meta: &pb.Meta{Loops: 20000, Players: []*pb.Player{{Id: 1, Name: "Ann"}, {Id: 2, Name: "Bo"}}},
		UnitTypes: []*pb.UnitType{
			{Name: "Marine", Class: pb.UnitClass_UNIT_CLASS_ARMY, Speed: 2.25},
			{Name: "Barracks", Class: pb.UnitClass_UNIT_CLASS_BUILDING},
			{Name: "SCV", Class: pb.UnitClass_UNIT_CLASS_WORKER, Speed: 2.8},
			{Name: "Larva", Class: pb.UnitClass_UNIT_CLASS_ARMY},
			{Name: "OrbitalCommand", Class: pb.UnitClass_UNIT_CLASS_BUILDING},
			{Name: "BarracksFlying", Class: pb.UnitClass_UNIT_CLASS_BUILDING},
		},
	}
	return tl
}

func addUnit(tl *pb.Timeline, owner, typ, born uint32, x, y float32, died *uint32) *pb.Unit {
	u := &pb.Unit{Id: uint32(len(tl.Units)), Owner: owner, Type: typ, BornLoop: born, DiedLoop: died}
	tl.Units = append(tl.Units, u)
	keys := []*pb.Keyframe{{Loop: born, X: x, Y: y, Source: pb.Source_SOURCE_TRACKER}}
	if died != nil {
		keys = append(keys, &pb.Keyframe{Loop: *died, X: x, Y: y, State: pb.UnitState_UNIT_STATE_DEAD, Source: pb.Source_SOURCE_TRACKER})
	}
	tl.Tracks = append(tl.Tracks, &pb.Track{UnitId: u.Id, Keyframes: keys})
	return u
}

func event(tl *pb.Timeline, loop uint32, kind any) {
	e := &pb.Event{Loop: loop}
	switch k := kind.(type) {
	case *pb.Event_UnitBorn:
		e.Kind = k
	case *pb.Event_Morph:
		e.Kind = k
	case *pb.Event_Upgrade:
		e.Kind = k
	case *pb.Event_PlayerStats:
		e.Kind = k
	}
	tl.Events = append(tl.Events, e)
}

func TestBuildOrder(t *testing.T) {
	tl := game()
	event(tl, 100, &pb.Event_PlayerStats{PlayerStats: &pb.PlayerStats{Player: 1, SupplyUsed: 14}})
	scv := addUnit(tl, 1, 2, 200, 10, 10, nil)
	event(tl, 200, &pb.Event_UnitBorn{UnitBorn: &pb.UnitBorn{UnitId: scv.Id}})
	rax := addUnit(tl, 1, 1, 300, 20, 20, nil)
	rax.Constructed = true
	event(tl, 300, &pb.Event_UnitBorn{UnitBorn: &pb.UnitBorn{UnitId: rax.Id}})
	larva := addUnit(tl, 1, 3, 310, 5, 5, nil)
	event(tl, 310, &pb.Event_UnitBorn{UnitBorn: &pb.UnitBorn{UnitId: larva.Id}})
	event(tl, 400, &pb.Event_PlayerStats{PlayerStats: &pb.PlayerStats{Player: 1, SupplyUsed: 16}})
	m := addUnit(tl, 1, 0, 500, 20, 18, nil)
	event(tl, 500, &pb.Event_UnitBorn{UnitBorn: &pb.UnitBorn{UnitId: m.Id}})
	cc := addUnit(tl, 1, 1, 0, 30, 30, nil) // starting building: not listed at birth
	event(tl, 600, &pb.Event_Morph{Morph: &pb.UnitMorph{UnitId: cc.Id, Type: 4}})
	event(tl, 650, &pb.Event_Morph{Morph: &pb.UnitMorph{UnitId: rax.Id, Type: 5}}) // lift-off: not listed
	event(tl, 700, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 1, Name: "Stimpack", Count: 1}})
	event(tl, 710, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 1, Name: "SprayTerran", Count: 1}})
	event(tl, 99999, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 1, Name: "TooLate", Count: 1}})

	rep, err := BuildOrder{Until: 20000}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tables) != 2 || rep.Tables[0].Player != 1 {
		t.Fatalf("tables = %+v", rep.Tables)
	}
	var got []string
	for _, r := range rep.Tables[0].Rows {
		got = append(got, strings.Join(r.Cells, " "))
	}
	want := []string{"0:13 14 Barracks", "0:22 16 Marine", "0:26 16 OrbitalCommand", "0:31 16 Stimpack (done)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("build order\n got %q\nwant %q", got, want)
	}
}

func TestBattles(t *testing.T) {
	tl := game()
	// A fight at (50, 50): 4 deaths for Ann, 3 for Bo within a few seconds.
	for i := 0; i < 7; i++ {
		owner := uint32(1 + i%2)
		addUnit(tl, owner, 0, 10, 50+float32(i), 50, u32(5000+uint32(i)*40))
	}
	// Scattered deaths elsewhere and much later: not a battle.
	addUnit(tl, 2, 0, 10, 120, 120, u32(9000))
	addUnit(tl, 2, 0, 10, 10, 120, u32(9050))
	// Larvae dying in a heap are not a fight.
	for i := 0; i < 10; i++ {
		addUnit(tl, 2, 3, 10, 80, 80, u32(12000))
	}
	rep, err := Battles{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Markers) != 1 {
		t.Fatalf("markers = %+v", rep.Markers)
	}
	m := rep.Markers[0]
	if m.Loop != 5000 || m.End != 5240 || m.X < 52 || m.X > 54 || m.Y != 50 {
		t.Errorf("marker = %+v", m)
	}
	row := rep.Tables[0].Rows[0]
	if got := strings.Join(row.Cells, " "); got != "3:43 0:10 4 3" {
		t.Errorf("row = %q", got)
	}
}

type failing struct{ panics bool }

func (f failing) ID() string { return "failing" }
func (f failing) Analyze(*pb.Timeline) (*Report, error) {
	if f.panics {
		panic("boom")
	}
	return nil, errors.New("no luck")
}

func TestRunIsolatesFailures(t *testing.T) {
	if r := runOne(failing{}, game()); r.Error != "no luck" || r.Plugin != "failing" {
		t.Errorf("error report = %+v", r)
	}
	if r := runOne(failing{panics: true}, game()); !strings.Contains(r.Error, "boom") {
		t.Errorf("panic report = %+v", r)
	}
	ids := []string{}
	for _, p := range Plugins() {
		ids = append(ids, p.ID())
	}
	if strings.Join(ids, ",") != "battles,build-order,economy,expansions,supply-blocks" {
		t.Errorf("built-in plugins = %v", ids)
	}
}

// TestRealReplays runs the plugins on whatever replays are in testdata.
func TestRealReplays(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "replays", "2022*.SC2Replay"))
	if len(paths) == 0 {
		t.Skip("no replays in testdata/replays")
	}
	for _, path := range paths {
		r, err := replay.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, rep := range Run(tl) {
			if rep.Error != "" {
				t.Errorf("%s: %s: %s", filepath.Base(path), rep.Plugin, rep.Error)
			}
			rows := 0
			for _, tb := range rep.Tables {
				rows += len(tb.Rows)
			}
			t.Logf("%s: %s: %d markers, %d rows", filepath.Base(path), rep.Plugin, len(rep.Markers), rows)
			if rep.Plugin == "build-order" && len(rep.Tables) > 0 && len(rep.Tables[0].Rows) > 0 {
				for i, row := range rep.Tables[0].Rows {
					if i < 12 {
						t.Logf("    %s", strings.Join(row.Cells, "  "))
					}
				}
			}
			for _, m := range rep.Markers {
				t.Logf("    %s %s", clock(m.Loop), m.Label)
			}
		}
	}
}

func TestSupplyBlocksAndEconomy(t *testing.T) {
	tl := game()
	stat := func(loop uint32, used, made float32, income uint32) {
		event(tl, loop, &pb.Event_PlayerStats{PlayerStats: &pb.PlayerStats{Player: 1, SupplyUsed: used, SupplyMade: made, MineralRate: income, Workers: 12}})
	}
	stat(160, 14, 15, 500)
	stat(320, 15, 15, 600) // blocked
	stat(480, 15, 15, 650) // still blocked
	stat(640, 16, 23, 700) // depot done
	stat(800, 200, 200, 2000)
	rep, err := SupplyBlocks{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Markers) != 1 || rep.Markers[0].Loop != 320 || rep.Markers[0].End != 640 || rep.Markers[0].Player != 1 {
		t.Errorf("supply blocks = %+v", rep.Markers)
	}
	eco, err := Economy{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(eco.Series) != 5 || eco.Series[0].Title != "Income" || len(eco.Series[0].Points) != 5 || eco.Series[0].Points[1].Value != 600 {
		t.Errorf("economy = %+v", eco.Series)
	}
}
