// SPDX-License-Identifier: Apache-2.0

package accuracy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmang-dev/ghostreel/internal/command"
	"github.com/dmang-dev/ghostreel/internal/engine"
	"github.com/dmang-dev/ghostreel/internal/mapcache"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
	"github.com/dmang-dev/ghostreel/internal/replay"
	"github.com/dmang-dev/ghostreel/internal/timeline"
)

// Ground-truth recordings (from ghostreel-engine) are never committed; these
// tests run on whatever is in testdata/replays/engine and skip otherwise.
func groundTruth(t *testing.T) []string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "replays", "engine", "*.engine.pb.gz"))
	if len(paths) == 0 {
		t.Skip("no engine recordings in testdata/replays/engine")
	}
	return paths
}

func open(t *testing.T, samplesPath string) (*replay.Replay, *mapfile.Map) {
	t.Helper()
	r, err := replay.Open(strings.TrimSuffix(samplesPath, ".engine.pb.gz") + ".SC2Replay")
	if err != nil {
		t.Fatal(err)
	}
	res := &mapcache.Resolver{MapDirs: []string{filepath.Join("..", "..", "testdata", "maps")}}
	mp, _, err := res.Resolve(context.Background(), &r.Info, "")
	if err != nil {
		t.Skipf("map for %s not in testdata/maps", filepath.Base(samplesPath))
	}
	m, err := mapfile.Open(mp)
	if err != nil {
		t.Fatal(err)
	}
	return r, m
}

// TestPathabilityAgainstEngine compares mapfile.Pathable with the game's own
// pathing grid. Blocking cells the game lets units walk on is what breaks
// pathing (it can seal a ramp), so that error must stay rare.
func TestPathabilityAgainstEngine(t *testing.T) {
	for _, p := range groundTruth(t) {
		s, err := engine.ReadSamples(p)
		if err != nil {
			t.Fatal(err)
		}
		if s.Pathing == nil {
			continue // recorded before grids were exported
		}
		_, m := open(t, p)
		g := s.Pathing
		walkable, wronglyBlocked := 0, 0
		for y := m.Playable.Bottom; y < m.Playable.Top; y++ {
			for x := m.Playable.Left; x < m.Playable.Right; x++ {
				i := y*int(g.Width) + x
				if g.Bits[i/8]&(0x80>>(i%8)) == 0 {
					continue
				}
				walkable++
				if !m.Pathable(x, y) {
					wronglyBlocked++
				}
			}
		}
		share := float64(wronglyBlocked) / float64(walkable)
		t.Logf("%s: %d of %d walkable cells blocked (%.1f%%)", m.Name, wronglyBlocked, walkable, 100*share)
		if share > 0.03 {
			t.Errorf("%s: %.1f%% of walkable cells marked unpathable", m.Name, 100*share)
		}
	}
}

// TestCommandTierBeatsTracker guards the command tier's main result: for the
// player whose orders are recorded, moving units are at least 2.5 times as
// accurate as with the tracker tier alone.
func TestCommandTierBeatsTracker(t *testing.T) {
	for _, p := range groundTruth(t) {
		s, err := engine.ReadSamples(p)
		if err != nil {
			t.Fatal(err)
		}
		r, m := open(t, p)
		tracker, err := timeline.Build(&r.Info, r.Events, timeline.Options{SkipCommands: true})
		if err != nil {
			t.Fatal(err)
		}
		cmd, _ := timeline.Build(&r.Info, r.Events, timeline.Options{SkipCommands: true})
		command.Apply(cmd, r.Events, command.Options{Grid: command.Grid(m)})
		player := int32(s.ObservedPlayer)
		before := Compare(tracker, s).MovingByOwner[player]
		after := Compare(cmd, s).MovingByOwner[player]
		t.Logf("%s: moving error %.2f -> %.2f cells (median %.2f -> %.2f)",
			filepath.Base(p), before.Mean, after.Mean, before.Median, after.Median)
		if after.Mean > before.Mean*0.4 {
			t.Errorf("%s: command tier %.2f is not under 40%% of tracker %.2f", filepath.Base(p), after.Mean, before.Mean)
		}
		if sr := CheckSelection(r.Events, s); sr.Precision < 0.9 {
			t.Errorf("%s: selection precision %.2f", filepath.Base(p), sr.Precision)
		}
	}
}
