// SPDX-License-Identifier: Apache-2.0

package timeline

import (
	"math"
	"reflect"
	"testing"

	pb "github.com/OWNER/ghostreel/gen/timeline/v1"
	"github.com/OWNER/ghostreel/internal/replay"
)

// fixture is a small hand-written game covering every event kind the builder
// handles, including slot reuse, a morph into a flying building, a hidden
// unit and a command that targets a unit.
func fixture() (*replay.Info, []replay.Event) {
	info := &replay.Info{
		Hash: "fixture", Version: "5.0.8.86383", BaseBuild: 86383, Loops: 1000,
		MapTitle: "Fixture", MapWidth: 64, MapHeight: 64, HasTracker: true,
		Players: []replay.Player{{ID: 1, Name: "A", Color: [3]byte{255, 0, 0}}, {ID: 2, Name: "B"}},
	}
	tag := replay.MakeTag
	ev := []replay.Event{
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(1, 1), UnitType: "MineralField", Player: 0, X: 10, Y: 10},
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(2, 1), UnitType: "SCV", Player: 1, X: 12, Y: 12},
		{Loop: 0, Kind: replay.KindUnitBorn, Tag: tag(3, 1), UnitType: "BeaconArmy", Player: 1, X: 0, Y: 0},
		{Loop: 0, Kind: replay.KindUpgrade, Player: 1, Name: "RewardDanceOverlord", Count: 1},
		{Loop: 1, Kind: replay.KindPlayerStats, Player: 1, Stats: &replay.PlayerStats{SupplyUsed: 12, Workers: 12}},
		{Loop: 50, Kind: replay.KindUnitInit, Tag: tag(4, 1), UnitType: "Barracks", Player: 1, X: 20, Y: 20},
		{Loop: 100, Kind: replay.KindUnitBorn, Tag: tag(5, 1), UnitType: "Marine", Player: 1, X: 20, Y: 18},
		{Loop: 161, Kind: replay.KindPlayerStats, Player: 1, Stats: &replay.PlayerStats{SupplyUsed: 14, Workers: 13}},
		{Loop: 240, Kind: replay.KindUnitPositions, Positions: []replay.PositionSample{{Index: 5, X: 30, Y: 18}, {Index: 4, X: 99, Y: 99}}},
		{Loop: 250, Kind: replay.KindCommand, Player: 1, AbilityLink: 3, HasTarget: true, X: 40.5, Y: 18.25, TargetTag: tag(5, 1)},
		{Loop: 300, Kind: replay.KindUnitDied, Tag: tag(2, 1), X: 13, Y: 12, KillerPlayer: 2},
		{Loop: 300, Kind: replay.KindUnitBorn, Tag: tag(2, 2), UnitType: "Zergling", Player: 2, X: 13, Y: 12},
		{Loop: 400, Kind: replay.KindChat, Player: 2, Text: "gg"},
		{Loop: 480, Kind: replay.KindUnitPositions, Positions: []replay.PositionSample{{Index: 5, X: 30, Y: 30}, {Index: 2, X: 20, Y: 12}}},
		{Loop: 500, Kind: replay.KindUnitDone, Tag: tag(4, 1)},
		{Loop: 600, Kind: replay.KindUnitTypeChange, Tag: tag(4, 1), UnitType: "BarracksFlying"},
		{Loop: 700, Kind: replay.KindUnitDied, Tag: tag(5, 1), X: 31, Y: 34, KillerPlayer: 2},
		{Loop: 720, Kind: replay.KindUnitPositions, Positions: []replay.PositionSample{{Index: 4, X: 40, Y: 40}}},
		{Loop: 800, Kind: replay.KindUnitOwnerChange, Tag: tag(2, 2), Player: 1},
	}
	return info, ev
}

func buildFixture(t testing.TB) *pb.Timeline {
	t.Helper()
	info, ev := fixture()
	tl, err := Build(info, ev, Options{Generator: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func unitByName(t *testing.T, tl *pb.Timeline, name string) *pb.Unit {
	t.Helper()
	for _, u := range tl.Units {
		if tl.UnitTypes[u.Type].Name == name {
			return u
		}
	}
	t.Fatalf("no unit of type %s", name)
	return nil
}

func TestBuildFixture(t *testing.T) {
	tl := buildFixture(t)

	if got := len(tl.Units); got != 5 { // mineral, SCV, barracks, marine, zergling; beacon hidden
		t.Fatalf("got %d units, want 5", got)
	}
	for _, e := range tl.Events {
		if u := e.GetUpgrade(); u != nil {
			t.Errorf("cosmetic upgrade %q kept", u.Name)
		}
	}

	scv := unitByName(t, tl, "SCV")
	if scv.DiedLoop == nil || *scv.DiedLoop != 300 || scv.KillerPlayer != 2 {
		t.Errorf("SCV death = %v killer %d, want 300 by 2", scv.DiedLoop, scv.KillerPlayer)
	}
	ling := unitByName(t, tl, "Zergling")
	if ling.Tag != uint32(replay.MakeTag(2, 2)) || ling.Owner != 2 {
		t.Errorf("reused slot: zergling tag %d owner %d", ling.Tag, ling.Owner)
	}
	// The loop-480 sample for slot 2 belongs to the zergling, not the dead SCV.
	ix := NewIndex(tl)
	if x, y, _ := PositionAt(ix.tracks[ling.Id], 100, 480); x != 20.5 || y != 12.5 {
		t.Errorf("zergling at 480 = (%v,%v), want (20.5,12.5)", x, y)
	}
	if OwnerAt(ling, 799) != 2 || OwnerAt(ling, 800) != 1 {
		t.Error("owner change not applied at loop 800")
	}

	rax := unitByName(t, tl, "Barracks")
	if !rax.Constructed || rax.DoneLoop == nil || *rax.DoneLoop != 500 {
		t.Errorf("barracks constructed=%v done=%v", rax.Constructed, rax.DoneLoop)
	}
	keys := ix.tracks[rax.Id].Keyframes
	// Pinned while grounded: the bogus loop-240 sample is ignored. Flying from
	// loop 600, so the 720 sample is kept.
	if len(keys) != 2 || keys[0].X != 20.5 || keys[1].Loop != 720 || keys[1].X != 40.5 {
		t.Errorf("barracks keyframes = %v", keys)
	}
	if got := tl.UnitTypes[TypeAt(rax, 650)].Name; got != "BarracksFlying" {
		t.Errorf("barracks type at 650 = %s", got)
	}

	marine := unitByName(t, tl, "Marine")
	mk := ix.tracks[marine.Id].Keyframes
	if len(mk) != 4 || mk[3].State != pb.UnitState_UNIT_STATE_DEAD || mk[3].X != 31.5 || mk[3].Y != 34.5 {
		t.Errorf("marine keyframes = %v", mk)
	}
	// Facing points along the next move: born (20,18) -> (30,18) is +x.
	if mk[0].Facing != 0 {
		t.Errorf("marine initial facing %v, want 0", mk[0].Facing)
	}

	var cmd *pb.Command
	for _, e := range tl.Events {
		if c := e.GetCommand(); c != nil {
			cmd = c
		}
	}
	if cmd == nil || !cmd.HasTargetUnit || cmd.TargetUnitId != marine.Id || cmd.X != 40.5 {
		t.Errorf("command = %v", cmd)
	}

	mineral := unitByName(t, tl, "MineralField")
	if mineral.Owner != 0 || tl.UnitTypes[mineral.Type].Class != pb.UnitClass_UNIT_CLASS_RESOURCE {
		t.Errorf("mineral owner %d class %v", mineral.Owner, tl.UnitTypes[mineral.Type].Class)
	}
	// MineralField is 2x1: x on a corner, y centered.
	if k := ix.tracks[mineral.Id].Keyframes[0]; k.X != 10 || k.Y != 10.5 {
		t.Errorf("mineral at (%v,%v), want (10,10.5)", k.X, k.Y)
	}
}

func TestBuildRejectsPreTrackerReplays(t *testing.T) {
	info, ev := fixture()
	info.HasTracker = false
	if _, err := Build(info, ev, Options{}); err != replay.ErrNoTrackerEvents {
		t.Fatalf("err = %v, want ErrNoTrackerEvents", err)
	}
}

func TestSeekMatchesReplayFromZero(t *testing.T) {
	tl := buildFixture(t)
	ix := NewIndex(tl)
	for loop := uint32(0); loop <= tl.Meta.Loops+50; loop++ {
		assertSameState(t, ix, loop)
	}
}

func assertSameState(t testing.TB, ix *Index, loop uint32) {
	t.Helper()
	got, want := ix.Seek(loop), ix.StateFromZero(loop)
	if !reflect.DeepEqual(got.Units, want.Units) {
		t.Fatalf("loop %d: seek units %v\nwant %v", loop, got.Units, want.Units)
	}
	if len(got.Stats) != len(want.Stats) {
		t.Fatalf("loop %d: seek has %d stats, want %d", loop, len(got.Stats), len(want.Stats))
	}
	for p, s := range want.Stats {
		if got.Stats[p] != s {
			t.Fatalf("loop %d: player %d stats differ", loop, p)
		}
	}
}

func TestSeekLifecycle(t *testing.T) {
	ix := NewIndex(buildFixture(t))
	count := func(loop uint32) int { return len(ix.Seek(loop).Units) }
	if n := count(0); n != 2 { // mineral + SCV
		t.Errorf("loop 0: %d units, want 2", n)
	}
	if n := count(299); n != 4 { // + barracks + marine
		t.Errorf("loop 299: %d units, want 4", n)
	}
	if n := count(300); n != 4 { // SCV out, zergling in
		t.Errorf("loop 300: %d units, want 4", n)
	}
	if n := count(700); n != 3 { // marine dead
		t.Errorf("loop 700: %d units, want 3", n)
	}
	st := ix.Seek(100)
	for _, u := range st.Units {
		if ix.TL.UnitTypes[u.Type].Name == "Barracks" && !u.Constructing {
			t.Error("barracks should be under construction at loop 100")
		}
	}
	if s := ix.Seek(200).Stats[1]; s == nil || s.Workers != 13 {
		t.Errorf("stats at 200 = %v, want the loop-161 snapshot", s)
	}
}

func TestPositionHoldThenTravel(t *testing.T) {
	tr := &pb.Track{Keyframes: []*pb.Keyframe{
		{Loop: 0, X: 0, Y: 0},
		{Loop: 1000, X: 20, Y: 0},
	}}
	// speed 2 cells per game second = 0.125 cells/loop -> 160 loops of travel.
	const speed = 2
	cases := []struct {
		loop uint32
		x    float32
	}{{0, 0}, {500, 0}, {840, 0}, {920, 10}, {1000, 20}, {2000, 20}}
	for _, c := range cases {
		if x, _, _ := PositionAt(tr, speed, c.loop); math.Abs(float64(x-c.x)) > 1e-4 {
			t.Errorf("loop %d: x = %v, want %v", c.loop, x, c.x)
		}
	}
	// A gap too short for the speed just interpolates over the whole gap.
	short := &pb.Track{Keyframes: []*pb.Keyframe{{Loop: 0, X: 0}, {Loop: 10, X: 100}}}
	if x, _, _ := PositionAt(short, speed, 5); math.Abs(float64(x-50)) > 1e-4 {
		t.Errorf("short gap at 5: x = %v, want 50", x)
	}
	if x, y, _ := PositionAt(nil, speed, 5); x != 0 || y != 0 {
		t.Error("nil track should give the origin")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	tl := buildFixture(t)
	data, err := Marshal(tl)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Units) != len(tl.Units) || len(back.Events) != len(tl.Events) || len(back.Snapshots) != len(tl.Snapshots) {
		t.Fatal("round trip lost data")
	}
	if _, err := MarshalJSON(tl); err != nil {
		t.Fatal(err)
	}
	back.SchemaVersion = 99
	data, _ = Marshal(back)
	if _, err := Unmarshal(data); err == nil {
		t.Error("unknown schema version accepted")
	}
}

func TestSimplifyKeepsShape(t *testing.T) {
	k := func(loop uint32, x float32, src pb.Source) *pb.Keyframe {
		return &pb.Keyframe{Loop: loop, X: x, Source: src}
	}
	cmd, trk := pb.Source_SOURCE_COMMAND, pb.Source_SOURCE_TRACKER
	// Constant motion at 1 cell per 16 loops (speed 1), sampled every 16
	// loops, then a hold, then an observation.
	var ks []*pb.Keyframe
	for i := uint32(0); i <= 10; i++ {
		ks = append(ks, k(i*16, float32(i), cmd))
	}
	ks = append(ks, k(400, 10, cmd), k(500, 10, cmd), k(600, 12, trk))
	speed := func(uint32) float32 { return 1 }
	out := simplifyKeys(ks, speed, SimplifyTolerance)
	if len(out) >= len(ks) || out[len(out)-1].Source != trk {
		t.Fatalf("simplified %d -> %d keys: %v", len(ks), len(out), out)
	}
	for loop := uint32(0); loop <= 600; loop += 4 {
		ax, _, _ := PositionAt(&pb.Track{Keyframes: ks}, 1, loop)
		bx, _, _ := PositionAt(&pb.Track{Keyframes: out}, 1, loop)
		if math.Abs(float64(ax-bx)) > SimplifyTolerance+1e-3 {
			t.Fatalf("loop %d: simplified x=%v, original %v", loop, bx, ax)
		}
	}
}
