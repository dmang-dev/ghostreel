// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/assets"
	"github.com/dmang-dev/ghostreel/internal/casc"
	"github.com/dmang-dev/ghostreel/internal/gamedata"
)

// cmdAssets converts unit models from the local game data into the cache the
// viewer reads (the server also converts on demand; this warms the cache and
// reports what is missing).
func cmdAssets(args []string) error {
	fs := flag.NewFlagSet("assets", flag.ExitOnError)
	gameData := fs.String("gamedata", assets.FindInstall(), "game installation (the folder with .build.info)")
	cache := fs.String("cache", app.New().CacheDir, "ghostreel cache folder")
	out := fs.String("o", "", "also write each model to <dir>/<type>.glb")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ghostreel assets [flags] [unit type...]   (default: every ladder unit type)")
		fs.PrintDefaults()
	}
	fs.Parse(reorder(fs, args))
	if *gameData == "" {
		return errors.New("no game installation found; pass --gamedata")
	}
	start := time.Now()
	l, err := assets.Open(*gameData, *cache)
	if err != nil {
		return err
	}
	defer l.Close()
	fmt.Printf("game data %s, build %s (opened in %v)\n", *gameData, l.Build(), time.Since(start).Round(time.Millisecond))
	types := fs.Args()
	if len(types) == 0 {
		types = gamedata.Names()
	}
	var ok, absent, failed int
	for _, t := range types {
		b, err := l.UnitGLB(t, false)
		switch {
		case errors.Is(err, casc.ErrNotLocal):
			absent++
			fmt.Printf("  %-26s not in this installation's data\n", t)
			continue
		case err != nil:
			failed++
			fmt.Printf("  %-26s %v\n", t, err)
			continue
		}
		ok++
		if *out != "" {
			if err := os.MkdirAll(*out, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(*out, t+".glb"), b, 0o644); err != nil {
				return err
			}
		}
	}
	fmt.Printf("%d converted, %d absent from the data, %d without a model (%v)\n", ok, absent, failed, time.Since(start).Round(time.Millisecond))
	return nil
}
