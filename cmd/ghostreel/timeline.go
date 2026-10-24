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
	noCmds := fs.Bool("no-commands", false, "leave player commands out (skips decoding game events; much faster on long games)")
	fs.Parse(reorder(fs, args))
	if fs.NArg() != 1 {
		return errors.New("dump-timeline: need exactly one replay")
	}
	path := fs.Arg(0)
	r, err := replay.OpenWith(path, replay.Options{SkipGameEvents: *noCmds})
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
// Whether a flag consumes the next argument is looked up in fs.
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBool(f) && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, pos...)
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
