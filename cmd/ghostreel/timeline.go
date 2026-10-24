// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/OWNER/ghostreel/internal/replay"
	"github.com/OWNER/ghostreel/internal/timeline"
)

const generator = "ghostreel dev"

func cmdDumpTimeline(args []string) error {
	fs := flag.NewFlagSet("dump-timeline", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: <replay>.pb, or .json with --json)")
	asJSON := fs.Bool("json", false, "write protobuf JSON instead of binary (for debugging)")
	noCmds := fs.Bool("no-commands", false, "leave player commands out of the event feed")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		return errors.New("dump-timeline: need exactly one replay")
	}
	path := fs.Arg(0)
	r, err := replay.Open(path)
	if err != nil {
		return err
	}
	tl, err := timeline.Build(&r.Info, r.Events, timeline.Options{Generator: generator, SkipCommands: *noCmds})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var data []byte
	if *asJSON {
		data, err = timeline.MarshalJSON(tl)
	} else {
		data, err = timeline.Marshal(tl)
	}
	if err != nil {
		return err
	}
	if *out == "" {
		ext := ".pb"
		if *asJSON {
			ext = ".json"
		}
		*out = strings.TrimSuffix(path, ".SC2Replay") + ext
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d units, %d tracks, %d snapshots, %d events → %s (%d KiB)\n",
		path, len(tl.Units), len(tl.Tracks), len(tl.Snapshots), len(tl.Events), *out, len(data)/1024)
	return nil
}

// reorder moves flags before positional arguments so "cmd file -o x" works
// as well as "cmd -o x file" (the flag package stops at the first positional).
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && takesValue(a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

// takesValue lists the CLI's flags that consume the next argument.
func takesValue(flag string) bool {
	switch strings.TrimLeft(flag, "-") {
	case "o", "addr", "map", "cache", "maps":
		return true
	}
	return false
}
