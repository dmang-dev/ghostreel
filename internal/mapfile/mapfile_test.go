// SPDX-License-Identifier: Apache-2.0

package mapfile

import (
	"encoding/binary"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OWNER/ghostreel/internal/replay"
)

// fakeArchive serves synthetic files in place of an MPQ.
type fakeArchive map[string][]byte

func (f fakeArchive) FileByName(name string) ([]byte, error) {
	if d, ok := f[name]; ok {
		return d, nil
	}
	return nil, nil
}

func u32s(vals ...uint32) []byte {
	b := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(b[i*4:], v)
	}
	return b
}

// syntheticMap builds a 4×3 map with the header layouts seen in real files.
func syntheticMap() fakeArchive {
	const w, h = 4, 3
	info := append([]byte("IpaM"), u32s(0x27, 0, 0, w, h, 1, 0)...)
	info = append(info, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0) // version-dependent filler
	info = append(info, "Dark\x00Bel'Shir\x00"...)
	info = append(info, u32s(1, 0, 3, 2, 0x2000)...)

	heights := append([]byte("SMAP"), u32s(0x66, w+1, h+1)...)
	heights = append(heights, make([]byte, 64-len(heights))...)
	for i := 0; i < (w+1)*(h+1); i++ {
		heights = append(heights, u32s(uint32(8*4096+i*2048))...) // 8.0 + i/2
	}

	cliffs := append([]byte("CLIF"), u32s(0x64, w, h)...)
	cliffs = append(cliffs, make([]byte, 32-len(cliffs))...)
	for i := 0; i < w*h; i++ {
		cliffs = append(cliffs, byte(i), 0)
	}

	flags := append([]byte("LFCT"), u32s(0x66, 0, 0, 0, 0, w, h)...)
	for i := 0; i < w*h; i++ {
		f := byte(2)
		if i == 5 { // cell (1,1) is a cliff edge
			f |= 1
		}
		flags = append(flags, f)
	}

	objects := []byte(`<?xml version="1.0" encoding="utf-8"?>
<PlacedObjects Version="27">
  <ObjectUnit Id="1" Position="2,1.5,0" UnitType="MineralField"/>
  <ObjectUnit Id="2" Position="1.5,1.5,0" UnitType="DestructibleRock6x6" Player="0" Rotation="1.5"/>
  <ObjectPoint Id="3" Position="1.5,0.5,0" Type="StartLoc" Name="Start Location 001"/>
  <ObjectDoodad Id="4" Position="0.5,0.5,8" Type="Tree"/>
</PlacedObjects>`)

	// 2×1 bottom-up TGA: blue then red.
	tga := []byte{0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 1, 0, 24, 0}
	tga = append(tga, 255, 0, 0, 0, 0, 255)

	return fakeArchive{"MapInfo": info, "t3SyncHeightMap": heights, "t3SyncCliffLevel": cliffs,
		"t3CellFlags": flags, "Objects": objects, "Minimap.tga": tga}
}

func TestDecodeSynthetic(t *testing.T) {
	m, err := Decode(syntheticMap())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Warnings) > 0 {
		t.Errorf("warnings: %v", m.Warnings)
	}
	if m.Width != 4 || m.Height != 3 || m.Tileset != "Bel'Shir" || m.Fog != "Dark" {
		t.Errorf("info = %dx%d %q %q", m.Width, m.Height, m.Tileset, m.Fog)
	}
	if m.Playable != (Rect{1, 0, 3, 2}) {
		t.Errorf("playable = %v", m.Playable)
	}
	if len(m.Heights) != 20 || m.Heights[0] != 8 || m.Heights[19] != 17.5 {
		t.Errorf("heights = %v", m.Heights)
	}
	if got := m.HeightAt(0, 0); got != (8+8.5+10.5+11)/4 {
		t.Errorf("HeightAt(0,0) = %v", got)
	}
	if len(m.CliffLevels) != 12 || m.CliffLevels[11] != 11 {
		t.Errorf("cliffs = %v", m.CliffLevels)
	}
	if m.Pathable(1, 1) || !m.Pathable(2, 1) || m.Pathable(0, 0) {
		t.Errorf("pathable: (1,1) cliff=%v (2,1)=%v (0,0) outside=%v", m.Pathable(1, 1), m.Pathable(2, 1), m.Pathable(0, 0))
	}
	if len(m.Objects) != 4 || m.Objects[1].Rotation != 1.5 || m.Objects[0].Y != 1.5 {
		t.Errorf("objects = %+v", m.Objects)
	}
	if len(m.StartLocations) != 1 || m.StartLocations[0] != (Point{1.5, 0.5}) {
		t.Errorf("start locations = %v", m.StartLocations)
	}
	if m.Minimap == nil {
		t.Fatal("no minimap")
	}
	// Bottom-up TGA with one row: pixel order is unchanged; BGR → RGB.
	if c := color.NRGBAModel.Convert(m.Minimap.At(0, 0)).(color.NRGBA); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("minimap(0,0) = %v, want blue", c)
	}
	e := m.Export(false)
	if len(e.Objects) != 3 || len(e.Pathable) != 12 || e.Pathable[6] != 1 || e.Pathable[5] != 0 {
		t.Errorf("export objects=%d pathable=%v", len(e.Objects), e.Pathable)
	}
}

func TestDecodeRequiresMapInfo(t *testing.T) {
	if _, err := Decode(fakeArchive{}); err == nil {
		t.Fatal("decoded an archive without MapInfo")
	}
	a := syntheticMap()
	delete(a, "t3CellFlags")
	m, err := Decode(a)
	if err != nil || len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "t3CellFlags") {
		t.Fatalf("missing optional part: err=%v warnings=%v", err, m.Warnings)
	}
}

func realMaps(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, pat := range []string{"*.SC2Map", "*.s2ma"} {
		p, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "maps", pat))
		paths = append(paths, p...)
	}
	if len(paths) == 0 {
		t.Skip("no maps in testdata/maps")
	}
	return paths
}

func TestRealMaps(t *testing.T) {
	for _, path := range realMaps(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			m, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range m.Warnings {
				t.Error(w)
			}
			p := m.Playable
			if p.Right-p.Left >= m.Width && p.Top-p.Bottom >= m.Height {
				t.Logf("playable area is the whole map (%v)", p)
			}
			t.Logf("%dx%d playable %v tileset %s, %d objects, %d start locations",
				m.Width, m.Height, p, m.Tileset, len(m.Objects), len(m.StartLocations))
		})
	}
}

// TestResourcesMatchReplay checks the map's placed minerals and geysers
// against the neutral units a replay on that map reports at loop 0.
func TestResourcesMatchReplay(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	rp, _ := filepath.Glob(filepath.Join(root, "replays", "GhostProtocol_2026-03-*.SC2Replay"))
	mp := filepath.Join(root, "maps", "AbyssalReefAIE.SC2Map")
	if len(rp) == 0 {
		t.Skip("no Abyssal Reef replay in testdata")
	}
	m, err := Open(mp)
	if err != nil {
		t.Skip(err)
	}
	r, err := replay.Open(rp[0])
	if err != nil {
		t.Fatal(err)
	}
	type res struct{ x, y float64 }
	fromMap := map[res]string{}
	for _, o := range m.Objects {
		if o.Kind == ObjectUnit && (strings.Contains(o.Type, "MineralField") || strings.Contains(o.Type, "Geyser")) {
			fromMap[res{math.Floor(o.X), math.Floor(o.Y)}] = o.Type
		}
	}
	matched, total := 0, 0
	for _, e := range r.Events {
		if e.Loop > 0 {
			break
		}
		if e.Kind != replay.KindUnitBorn || e.Player != 0 {
			continue
		}
		if !strings.Contains(e.UnitType, "MineralField") && !strings.Contains(e.UnitType, "Geyser") {
			continue
		}
		total++
		if _, ok := fromMap[res{e.X, e.Y}]; ok {
			matched++
		}
	}
	if total == 0 || matched != total || total != len(fromMap) {
		t.Errorf("replay resources %d, matched in map %d, map resources %d", total, matched, len(fromMap))
	}
}
