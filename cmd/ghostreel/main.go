// SPDX-License-Identifier: Apache-2.0

// Command ghostreel inspects SC2 replays and maps and builds playback timelines.
package main

import (
	"fmt"
	"os"
)

const usage = `ghostreel — replay viewer toolkit

Usage:
  ghostreel info <replay>...                      summarize replays
  ghostreel dump-timeline <replay> -o out.pb      build a timeline (--json for JSON)
  ghostreel dump-map <map|replay> -o map.json     decode a map (also writes a hillshade PNG)
  ghostreel serve [--addr :8080]                  serve the viewer and its API

Run "ghostreel <command> -h" for a command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"info":          cmdInfo,
		"dump-timeline": cmdDumpTimeline,
		"dump-map":      cmdDumpMap,
		"serve":         cmdServe,
	}
	cmd, ok := cmds[os.Args[1]]
	if !ok {
		if os.Args[1] != "-h" && os.Args[1] != "--help" && os.Args[1] != "help" {
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		}
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
