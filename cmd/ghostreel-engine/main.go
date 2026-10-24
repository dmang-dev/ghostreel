// SPDX-License-Identifier: Apache-2.0

// Command ghostreel-engine records ground truth for replays: it plays each
// one back in a real SC2 client of the matching build (through Blizzard's
// s2client API) and writes the exact state of every unit every few loops to
// <replay>.engine.pb.gz. The rest of ghostreel never needs the game; this
// tool is optional and only runs where a client is installed.
//
// SC2 is started as a direct child of this process and killed on exit. On
// shared hosts, run it under `nice` and use --max-load so it backs off while
// the machine is busy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/OWNER/ghostreel/internal/engine"
	"github.com/OWNER/ghostreel/internal/replay"
)

func main() {
	home, _ := os.UserHomeDir()
	defDir := filepath.Join(home, "StarCraftII")
	if runtime.GOOS == "windows" {
		defDir = `C:\Program Files (x86)\StarCraft II`
	}
	dir := flag.String("sc2", defDir, "SC2 install folder (holding Versions/ and Maps/)")
	outDir := flag.String("o", "", "output folder (default: next to each replay)")
	step := flag.Uint("step", 16, "loops between recorded frames (16 = 0.7 s on the in-game clock)")
	observe := flag.Uint("observe", 1, "player whose unit orders are recorded")
	maxLoad := flag.Float64("max-load", 0, "pause while the 1-minute load average is above this (Linux; 0 = off)")
	port := flag.Int("port", 0, "API port (default: first free in 5800-5899)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: ghostreel-engine [flags] replay.SC2Replay...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *dir, *outDir, uint32(*step), uint32(*observe), *maxLoad, *port, flag.Args()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir, outDir string, step, observe uint32, maxLoad float64, port int, paths []string) error {
	var game *engine.Game
	var client *engine.Client
	gameBuild := 0
	defer func() {
		if game != nil {
			game.Stop()
		}
	}()
	failed := 0
	for _, path := range paths {
		r, err := replay.OpenWith(path, replay.Options{InfoOnly: true})
		if err != nil {
			log.Printf("%s: %v", path, err)
			failed++
			continue
		}
		// One client per base build; consecutive replays of the same build reuse it.
		if game == nil || gameBuild != r.Info.BaseBuild {
			if game != nil {
				game.Stop()
				game = nil
			}
			game, client, err = engine.Launch(ctx, engine.LaunchOptions{
				Dir: dir, BaseBuild: r.Info.BaseBuild, Port: port, Log: log.Printf,
			})
			if err != nil {
				log.Printf("%s: %v", path, err)
				failed++
				continue
			}
			gameBuild = r.Info.BaseBuild
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		start := time.Now()
		lastLog := time.Now()
		samples, err := engine.Export(ctx, client, data, engine.ExportOptions{
			Step:           step,
			ObservedPlayer: observe,
			BeforeStep:     func(loop uint32) error { return waitForLoad(ctx, maxLoad) },
			Progress: func(loop, total uint32) {
				if time.Since(lastLog) > 15*time.Second {
					lastLog = time.Now()
					log.Printf("%s: %s / %s", filepath.Base(path), replay.FormatClock(int64(loop)), replay.FormatClock(int64(total)))
				}
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("%s: %v", path, err)
			failed++
			// The client may be in a bad state; start a fresh one next time.
			game.Stop()
			game = nil
			continue
		}
		out := strings.TrimSuffix(path, filepath.Ext(path)) + ".engine.pb.gz"
		if outDir != "" {
			out = filepath.Join(outDir, filepath.Base(out))
		}
		if err := engine.WriteSamples(out, samples); err != nil {
			return err
		}
		fi, _ := os.Stat(out)
		log.Printf("%s: %d frames in %s → %s (%d KiB)", filepath.Base(path), len(samples.Frames),
			time.Since(start).Round(time.Second), out, fi.Size()/1024)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d replays failed", failed, len(paths))
	}
	return nil
}

// waitForLoad blocks while the host's 1-minute load average exceeds max,
// giving up after 20 minutes.
func waitForLoad(ctx context.Context, max float64) error {
	if max <= 0 {
		return nil
	}
	deadline := time.Now().Add(20 * time.Minute)
	warned := false
	for {
		load, err := loadAverage()
		if err != nil || load <= max {
			return nil
		}
		if !warned {
			log.Printf("load average %.1f > %.1f: pausing", load, max)
			warned = true
		}
		if time.Now().After(deadline) {
			return errors.New("host stayed busy for 20 minutes; giving up")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
}

func loadAverage() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0, errors.New("empty /proc/loadavg")
	}
	return strconv.ParseFloat(f[0], 64)
}
