// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/dmang-dev/ghostreel/internal/mapcache"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
	"github.com/dmang-dev/ghostreel/internal/replay"
)

// resolverFlags are the map-lookup flags shared by commands that take a replay.
type resolverFlags struct {
	manual   string
	network  bool
	maps     string
	cacheDir string
}

func addResolverFlags(fs *flag.FlagSet) *resolverFlags {
	rf := &resolverFlags{}
	fs.StringVar(&rf.manual, "map", "", "use this map file instead of looking the replay's map up")
	fs.BoolVar(&rf.network, "network", false, "allow downloading maps from Blizzard's depot (logged)")
	fs.StringVar(&rf.maps, "maps", "", "extra map folders, "+string(os.PathListSeparator)+"-separated")
	fs.StringVar(&rf.cacheDir, "cache", "", "ghostreel cache folder: downloaded maps go in <dir>/maps, timelines in <dir>/timelines (default: user cache dir)")
	return rf
}

func (rf *resolverFlags) resolver() *mapcache.Resolver {
	r := mapcache.Default()
	r.AllowNetwork = rf.network
	if rf.maps != "" {
		r.MapDirs = append(filepath.SplitList(rf.maps), r.MapDirs...)
	}
	if rf.cacheDir != "" {
		r.DownloadDir = filepath.Join(rf.cacheDir, "maps")
	}
	r.Log = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	return r
}

func cmdDumpMap(args []string) error {
	fs := flag.NewFlagSet("dump-map", flag.ExitOnError)
	out := fs.String("o", "", "output JSON (default: <map>.json); the hillshade goes next to it as .png")
	doodads := fs.Bool("doodads", false, "include doodads in the JSON")
	layers := fs.Bool("layers", false, "also write one PNG per t3CellFlags bit, cliff levels and the minimap (debug)")
	rf := addResolverFlags(fs)
	fs.Parse(reorder(fs, args))
	if fs.NArg() != 1 {
		return errors.New("dump-map: need exactly one map or replay file")
	}
	path, title := fs.Arg(0), ""
	if strings.EqualFold(filepath.Ext(path), ".SC2Replay") {
		r, err := replay.Open(path)
		if err != nil {
			return err
		}
		mp, how, err := rf.resolver().Resolve(context.Background(), &r.Info, rf.manual)
		if err != nil {
			return err
		}
		fmt.Printf("map for %s: %s (%s)\n", filepath.Base(path), mp, how)
		if *out == "" {
			*out = trimExt(path) + ".map.json"
		}
		path = mp
		title = r.Info.MapTitle // depot files are named by hash
	}
	m, err := mapfile.Open(path)
	if err != nil {
		return err
	}
	if title != "" {
		m.Name = title
	}
	if *out == "" {
		*out = trimExt(path) + ".json"
	}
	data, err := json.Marshal(m.Export(*doodads))
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	base := trimExt(*out)
	if err := writePNG(base+".png", m.Hillshade(4)); err != nil {
		return err
	}
	if *layers {
		for bit := 0; bit < 8; bit++ {
			if err := writePNG(fmt.Sprintf("%s.flag%d.png", base, bit), m.FlagLayer(1<<bit, 2)); err != nil {
				return err
			}
		}
		if err := writePNG(base+".cliff.png", m.CliffLayer(2)); err != nil {
			return err
		}
		if m.Minimap != nil {
			if err := writePNG(base+".minimap.png", m.Minimap); err != nil {
				return err
			}
		}
	}
	units := 0
	for _, o := range m.Objects {
		if o.Kind == mapfile.ObjectUnit {
			units++
		}
	}
	fmt.Printf("%s: %d×%d, playable %v, tileset %s, %d start locations, %d placed units → %s, %s.png\n",
		m.Name, m.Width, m.Height, m.Playable, m.Tileset, len(m.StartLocations), units, *out, base)
	for _, w := range m.Warnings {
		fmt.Println("  warning:", w)
	}
	return nil
}

func trimExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i > strings.LastIndexAny(p, `/\`) {
		return p[:i]
	}
	return p
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
