// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dmang-dev/ghostreel/internal/replay"
)

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	counts := fs.Bool("counts", false, "list event counts per s2prot event type")
	positions := fs.Bool("positions", false, "report position-sample coverage (debug)")
	asJSON := fs.Bool("json", false, "print one JSON object per replay (for scripts), with its map file if found locally")
	rf := addResolverFlags(fs)
	fs.Parse(reorder(fs, args))
	if fs.NArg() == 0 {
		return errors.New("info: no replay given")
	}
	failed := 0
	enc := json.NewEncoder(os.Stdout)
	for i, path := range fs.Args() {
		if *asJSON {
			if err := enc.Encode(infoJSON(path, rf)); err != nil {
				return err
			}
			continue
		}
		if i > 0 {
			fmt.Println()
		}
		r, err := replay.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			failed++
			continue
		}
		printInfo(&r.Info, *counts)
		if *positions {
			printPositionStats(r)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d replays failed", failed, fs.NArg())
	}
	return nil
}

// replayJSON is what `info -json` prints: enough for a script to choose
// replays by version, race and map.
type replayJSON struct {
	Path      string       `json:"path"`
	Error     string       `json:"error,omitempty"`
	Version   string       `json:"version,omitempty"`
	BaseBuild int          `json:"baseBuild,omitempty"`
	Loops     int          `json:"loops,omitempty"`
	Map       string       `json:"map,omitempty"`
	MapFile   string       `json:"mapFile,omitempty"` // empty when not found locally
	Players   []playerJSON `json:"players,omitempty"`
}

type playerJSON struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Race  string `json:"race"`
	Human bool   `json:"human"`
}

func infoJSON(path string, rf *resolverFlags) replayJSON {
	out := replayJSON{Path: path}
	r, err := replay.OpenWith(path, replay.Options{InfoOnly: true})
	if err != nil {
		out.Error = err.Error()
		return out
	}
	in := &r.Info
	out.Version, out.BaseBuild, out.Loops, out.Map = in.Version, in.BaseBuild, int(in.Loops), in.MapTitle
	for _, p := range in.Players {
		out.Players = append(out.Players, playerJSON{p.ID, p.Name, p.Race, p.Human})
	}
	if mp, _, err := rf.resolver().Resolve(context.Background(), in, rf.manual); err == nil {
		out.MapFile = mp
	}
	return out
}

func printInfo(in *replay.Info, counts bool) {
	fmt.Printf("File:      %s\n", in.Path)
	ver := fmt.Sprintf("%s (base build %d)", in.Version, in.BaseBuild)
	if in.ProtocolApproxFrom != 0 {
		ver += fmt.Sprintf(" — decoded with protocol %d, not exact", in.ProtocolApproxFrom)
	}
	fmt.Printf("Version:   %s\n", ver)
	fmt.Printf("Duration:  %s in-game (%d loops, %s)\n", replay.FormatClock(in.Loops), in.Loops, in.Speed)
	fmt.Printf("Map:       %s (%d×%d)\n", in.MapTitle, in.MapWidth, in.MapHeight)
	switch {
	case in.MapHandle != nil:
		fmt.Printf("Map file:  %s (depot %s)\n", in.MapHandle.FileName(), in.MapHandle.Region)
	case in.MapFileName != "":
		fmt.Printf("Map file:  %s (local)\n", in.MapFileName)
	}
	fmt.Println("Players:")
	for _, p := range in.Players {
		fmt.Printf("  %d. %-20s %-8s %-8s #%02x%02x%02x\n", p.ID, p.Name, p.Race, p.Result, p.Color[0], p.Color[1], p.Color[2])
	}
	if !in.HasTracker {
		fmt.Println("Playback:  unsupported — " + replay.ErrNoTrackerEvents.Error())
	}
	if len(in.DecodeErrors) > 0 {
		fmt.Printf("Warning:   incomplete decode of %s\n", strings.Join(in.DecodeErrors, ", "))
	}
	total := map[string]int{}
	for k, n := range in.EventCounts {
		total[strings.SplitN(k, "/", 2)[0]] += n
	}
	fmt.Printf("Events:    %d tracker, %d game, %d message\n", total["tracker"], total["game"], total["message"])
	if counts {
		keys := make([]string, 0, len(in.EventCounts))
		for k := range in.EventCounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-36s %d\n", k, in.EventCounts[k])
		}
	}
}
