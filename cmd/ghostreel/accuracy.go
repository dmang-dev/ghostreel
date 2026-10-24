// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/OWNER/ghostreel/internal/accuracy"
	"github.com/OWNER/ghostreel/internal/engine"
	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/timeline"
)

func cmdAccuracy(args []string) error {
	fs := flag.NewFlagSet("accuracy", flag.ExitOnError)
	eng := fs.String("engine", "", "engine ground truth (default: <replay>.engine.pb.gz)")
	top := fs.Int("top", 15, "unit types to list")
	fs.Parse(reorder(fs, args))
	if fs.NArg() == 0 {
		return errors.New("accuracy: need a replay")
	}
	for _, path := range fs.Args() {
		samplesPath := *eng
		if samplesPath == "" || fs.NArg() > 1 {
			samplesPath = strings.TrimSuffix(path, ".SC2Replay") + ".engine.pb.gz"
		}
		if err := accuracyOne(path, samplesPath, *top); err != nil {
			return err
		}
	}
	return nil
}

func accuracyOne(path, samplesPath string, top int) error {
	s, err := engine.ReadSamples(samplesPath)
	if err != nil {
		return err
	}
	r, err := replay.Open(path)
	if err != nil {
		return err
	}
	if r.Info.Hash != s.ReplayHash {
		return fmt.Errorf("%s was recorded from a different replay", samplesPath)
	}
	tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{SkipCommands: true})
	if err != nil {
		return err
	}
	rep := accuracy.Compare(tl, s)
	fmt.Printf("%s — %s, %d engine frames every %d loops\n", r.Info.MapTitle, replay.FormatClock(r.Info.Loops), rep.Frames, s.Step)
	unmatched := 0
	for _, n := range rep.Unmatched {
		unmatched += n
	}
	fmt.Printf("samples: %d matched, %d outside the timeline's lifetime, %d with no timeline unit\n", rep.Matched, rep.Lifecycle, unmatched)
	row := func(name string, st *accuracy.Stats) {
		fmt.Printf("  %-26s %8d %7.2f %7.2f %7.2f %7.1f %6.0f%% %6.0f%%\n",
			name, st.N, st.Mean, st.Median, st.P90, st.Max, st.Within1*100, st.Within5*100)
	}
	fmt.Printf("  %-26s %8s %7s %7s %7s %7s %7s %7s\n", "position error (cells)", "n", "mean", "median", "p90", "max", "≤1", "≤5")
	row("all", &rep.All)
	row("moving units", &rep.Moving)
	classes := make([]string, 0, len(rep.ByClass))
	for c := range rep.ByClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		row("class "+c, rep.ByClass[c])
	}
	types := make([]string, 0, len(rep.ByType))
	for t := range rep.ByType {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return rep.ByType[types[i]].N > rep.ByType[types[j]].N })
	for i, t := range types {
		if i == top {
			break
		}
		row(t, rep.ByType[t])
	}
	if unmatched > 0 {
		fmt.Printf("  no timeline unit for: %v\n", rep.Unmatched)
	}
	fmt.Println()
	return nil
}
