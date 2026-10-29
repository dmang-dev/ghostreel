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

func TestAPM(t *testing.T) {
	tl := game()
	tl.Meta.Loops = 2688 // two minutes
	// Player 1: 10 actions (4 commands) every 5 s: 120 APM, 48 commands a
	// minute. Player 2 (the AI) did nothing.
	p1 := &pb.PlayerActions{Player: 1}
	for i := 0; i < 24; i++ {
		p1.Actions = append(p1.Actions, 10)
		p1.Commands = append(p1.Commands, 4)
	}
	tl.Actions = &pb.ActionCounts{BinLoops: 112, Players: []*pb.PlayerActions{p1, {Player: 2}}}
	rep, err := APM{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Series) != 2 || rep.Series[0].Title != "APM" || rep.Series[1].Title != "Commands" {
		t.Fatalf("series = %+v", rep.Series)
	}
	for _, s := range rep.Series {
		want := float32(120)
		if s.Title == "Commands" {
			want = 48
		}
		if len(s.Points) != 24 || s.Points[0].Value != want || s.Points[23].Value != want || s.Points[23].Loop != 2688 {
			t.Errorf("%s = %+v", s.Title, s.Points)
		}
	}
	if got := strings.Join(rep.Tables[0].Rows[0].Cells, " "); len(rep.Tables[0].Rows) != 1 || got != "Ann 120 48 240" {
		t.Errorf("average = %q", got)
	}
	// A burst: 60 actions in the first 5 s of the second minute, then none.
	p1.Actions = make([]uint32, 13)
	p1.Actions[12] = 60
	rep, _ = APM{}.Analyze(tl)
	pts := rep.Series[0].Points
	if pts[11].Value != 0 || pts[12].Value != 60 || pts[23].Value != 60 {
		t.Errorf("burst = %v %v %v", pts[11].Value, pts[12].Value, pts[23].Value)
	}
	// Timelines without action counts give no APM.
	tl.Actions = nil
	if rep, _ = (APM{}).Analyze(tl); len(rep.Series)+len(rep.Tables) != 0 {
		t.Errorf("APM without counts: %+v", rep)
	}
}

func TestArmy(t *testing.T) {
	tl := game()
	tl.Meta.Loops = 3000
	unit(tl, 1, "SCV", worker, 0, nil)
	for i := 0; i < 3; i++ {
		unit(tl, 1, "Marine", army, 100, nil)
	}
	tank := unit(tl, 1, "SiegeTank", army, 1000, nil)
	tank.Morphs = []*pb.Morph{{Loop: 1200, Type: typeIndex(tl, "SiegeTankSieged", army)}}
	unit(tl, 1, "Marine", army, 200, u32(1300)) // dead by 1:00
	unit(tl, 1, "MULE", army, 500, nil)         // no supply: not army
	z := unit(tl, 1, "Zealot", army, 1300, nil) // warping in until 1400
	z.Constructed, z.DoneLoop = true, u32(1400)
	rep, err := Army{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tables) != 1 {
		t.Fatalf("tables = %+v", rep.Tables)
	}
	got := rows(rep.Tables[0])
	if strings.Join(got, "\n") != "1:00 | 6 | 3 Marine, 1 SiegeTank\n2:00 | 8 | 3 Marine, 1 SiegeTank, 1 Zealot\n2:13 | 8 | 3 Marine, 1 SiegeTank, 1 Zealot" {
		t.Errorf("army rows = %q", got)
	}
	s := rep.Series[0]
	if s.Title != "Army supply" || s.Points[1].Value != 3 || s.Points[2].Value != 4 || s.Points[13].Value != 8 {
		t.Errorf("army supply = %+v", s.Points[:14])
	}
}

func TestUpgrades(t *testing.T) {
	tl := game()
	event(tl, 0, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 1, Name: "SprayTerran", Count: 1}})
	event(tl, 3000, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 1, Name: "ShieldWall", Count: 1}})
	event(tl, 4000, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 2, Name: "TerranInfantryWeaponsLevel2", Count: 1}})
	event(tl, 4100, &pb.Event_Upgrade{Upgrade: &pb.Upgrade{Player: 2, Name: "zerglingmovementspeed", Count: 1}})
	rep, err := Upgrades{}.Analyze(tl)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rows(rep.Tables[0]), ";") + " / " + strings.Join(rows(rep.Tables[1]), ";")
	if got != "2:13 | Combat Shield / 2:58 | Terran Infantry Weapons 2;3:03 | Metabolic Boost" {
		t.Errorf("upgrades = %q", got)
	}
}

// TestNewReportsReal logs APM, production, army and upgrades on the replays
// in testdata, and checks the APM is in a human range for the pro games.
func TestNewReportsReal(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "replays", "2022*.SC2Replay"))
	if len(paths) == 0 {
		t.Skip("no replays in testdata/replays")
	}
	for _, path := range paths[:min(len(paths), 3)] {
		r, err := replay.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{SkipCommands: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Log(filepath.Base(path))
		for _, p := range []Plugin{APM{}, Production{}, Army{}, Upgrades{}} {
			rep, err := p.Analyze(tl)
			if err != nil {
				t.Fatal(err)
			}
			for _, tb := range rep.Tables {
				for i, row := range rows(tb) {
					if i < 4 {
						t.Logf("  %s %s: %s", rep.Title, tb.Title, row)
					}
				}
			}
			if p.ID() != "apm" {
				continue
			}
			if len(rep.Tables) != 1 || len(rep.Tables[0].Rows) != 2 {
				t.Fatalf("APM table = %+v", rep.Tables)
			}
			for _, row := range rep.Tables[0].Rows {
				if apm := row.Cells[1]; len(apm) != 3 || apm < "100" || apm > "600" {
					t.Errorf("%s: APM %s", row.Cells[0], apm)
				}
			}
		}
	}
}
