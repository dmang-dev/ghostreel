// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"

	"github.com/OWNER/ghostreel/internal/mapfile"
)

func cmdDumpMap(args []string) error {
	fs := flag.NewFlagSet("dump-map", flag.ExitOnError)
	out := fs.String("o", "", "output JSON (default: <map>.json); the hillshade goes next to it as .png")
	doodads := fs.Bool("doodads", false, "include doodads in the JSON")
	layers := fs.Bool("layers", false, "also write one PNG per t3CellFlags bit, cliff levels and the minimap (debug)")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		return errors.New("dump-map: need exactly one map file")
	}
	path := fs.Arg(0)
	m, err := mapfile.Open(path)
	if err != nil {
		return err
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
