// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/OWNER/ghostreel/internal/accuracy"
	"github.com/OWNER/ghostreel/internal/command"
	"github.com/OWNER/ghostreel/internal/engine"
	"github.com/OWNER/ghostreel/internal/mapfile"
	"github.com/OWNER/ghostreel/internal/pathing"
	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/timeline"
)

func cmdAccuracy(args []string) error {
	fs := flag.NewFlagSet("accuracy", flag.ExitOnError)
	eng := fs.String("engine", "", "engine ground truth (default: <replay>.engine.pb.gz)")
	top := fs.Int("top", 12, "unit types to list")
	rf := addResolverFlags(fs)
	fs.Parse(reorder(fs, args))
	if fs.NArg() == 0 {
		return errors.New("accuracy: need a replay")
	}
	var total [2]accuracy.Stats
	var all [2][]float64
	for _, path := range fs.Args() {
		samplesPath := *eng
		if samplesPath == "" || fs.NArg() > 1 {
			samplesPath = strings.TrimSuffix(path, ".SC2Replay") + ".engine.pb.gz"
		}
		reps, err := accuracyOne(path, samplesPath, *top, rf)
		if err != nil {
			return err
		}
		for i, r := range reps {
			all[i] = append(all[i], r.Moving.Mean)
			total[i].N += r.Moving.N
		}
	}
	if fs.NArg() > 1 {
		fmt.Println("moving-unit mean error per replay (cells):")
		for i, name := range []string{"tracker", "command"} {
			fmt.Printf("  %-8s %v\n", name, all[i])
		}
	}
	return nil
}

// accuracyOne prints the tracker and command tiers' errors for one replay.
func accuracyOne(path, samplesPath string, top int, rf *resolverFlags) ([]*accuracy.Report, error) {
	s, err := engine.ReadSamples(samplesPath)
	if err != nil {
		return nil, err
	}
	r, err := replay.Open(path)
	if err != nil {
		return nil, err
	}
	if r.Info.Hash != s.ReplayHash {
		return nil, fmt.Errorf("%s was recorded from a different replay", samplesPath)
	}
	tracker, err := timeline.Build(&r.Info, r.Events, timeline.Options{SkipCommands: true})
	if err != nil {
		return nil, err
	}
	cmdTL, err := timeline.Build(&r.Info, r.Events, timeline.Options{SkipCommands: true})
	if err != nil {
		return nil, err
	}
	var grid *pathing.Grid
	res := rf.resolver()
	res.Log = func(string, ...any) {}
	if mp, _, err := res.Resolve(context.Background(), &r.Info, rf.manual); err == nil {
		if m, err := mapfile.Open(mp); err == nil {
			grid = command.Grid(m)
		}
	}
	if grid == nil {
		log.Printf("%s: map not found; command tier moves in straight lines", path)
	}
	command.Apply(cmdTL, r.Events, command.Options{Grid: grid})

	fmt.Printf("%s — %s on %s, engine frames every %d loops\n", r.Info.Players[0].Name+" vs "+r.Info.Players[1].Name,
		replay.FormatClock(r.Info.Loops), r.Info.MapTitle, s.Step)
	reps := []*accuracy.Report{accuracy.Compare(tracker, s), accuracy.Compare(cmdTL, s)}
	rep := reps[0]
	unmatched := 0
	for _, n := range rep.Unmatched {
		unmatched += n
	}
	fmt.Printf("samples: %d matched, %d outside the timeline's lifetime, %d with no timeline unit\n", rep.Matched, rep.Lifecycle, unmatched)
	if sr := accuracy.CheckSelection(r.Events, s); sr.Commands > 0 {
		fmt.Printf("selection tracking (player %d): %d commands checked, %.0f%% exact, precision %.0f%%, recall %.0f%% (point targets %.0f%%)\n",
			s.ObservedPlayer, sr.Commands, 100*float64(sr.Exact)/float64(sr.Commands), 100*sr.Precision, 100*sr.Recall, 100*sr.PointRecall)
	}
	fmt.Printf("  %-30s %8s %7s %7s %7s %6s %6s\n", "position error (cells)", "n", "mean", "median", "p90", "≤1", "≤5")
	row := func(name string, st *accuracy.Stats) {
		if st == nil || st.N == 0 {
			return
		}
		fmt.Printf("  %-30s %8d %7.2f %7.2f %7.2f %5.0f%% %5.0f%%\n", name, st.N, st.Mean, st.Median, st.P90, st.Within1*100, st.Within5*100)
	}
	for i, tier := range []string{"tracker", "command"} {
		row(tier+" · moving units", &reps[i].Moving)
	}
	for _, p := range r.Info.Players {
		for i, tier := range []string{"tracker", "command"} {
			row(fmt.Sprintf("%s · moving, %s", tier, p.Name), reps[i].MovingByOwner[int32(p.ID)])
		}
	}
	for _, c := range []string{"worker", "army"} {
		for i, tier := range []string{"tracker", "command"} {
			row(tier+" · "+c, reps[i].ByClass[c])
		}
	}
	types := make([]string, 0, len(rep.ByType))
	for t, st := range rep.ByType {
		if st.Max > 0 { // skip things that never move
			types = append(types, t)
		}
	}
	sort.Slice(types, func(i, j int) bool { return rep.ByType[types[i]].N > rep.ByType[types[j]].N })
	for i, t := range types {
		if i == top {
			break
		}
		for k, tier := range []string{"tracker", "command"} {
			row(tier+" · "+t, reps[k].ByType[t])
		}
	}
	fmt.Println()
	return reps, nil
}
