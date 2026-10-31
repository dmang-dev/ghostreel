// SPDX-License-Identifier: Apache-2.0

package mapfile

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
)

func TestParseWater(t *testing.T) {
	b := make([]byte, 32+2*56)
	copy(b, "WATRn")
	binary.LittleEndian.PutUint32(b[8:], 2)
	put := func(i int, name string, r [4]float32) {
		o := 32 + i*56
		copy(b[o:o+16], name+"\x00junk")
		binary.LittleEndian.PutUint32(b[o+36:], 8) // not map data
		for j, v := range r {
			binary.LittleEndian.PutUint32(b[o+40+4*j:], math.Float32bits(v))
		}
	}
	put(0, "Tarsonis", [4]float32{0, 48, 12, 56})
	put(1, "Char", [4]float32{160, 44, 168, 48})
	w, err := parseWater(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []WaterRect{{"Tarsonis", 0, 48, 12, 56}, {"Char", 160, 44, 168, 48}}
	if len(w) != 2 || w[0] != want[0] || w[1] != want[1] {
		t.Errorf("got %v, want %v", w, want)
	}
	if _, err := parseWater(b[:32+56]); err == nil {
		t.Error("truncated records: no error")
	}
	// An empty list, as most maps have.
	binary.LittleEndian.PutUint32(b[8:], 0)
	if w, err := parseWater(b[:32]); err != nil || len(w) != 0 {
		t.Errorf("empty: %v, %v", w, err)
	}
	if _, err := parseWater(b[:20]); err == nil {
		t.Error("short file: no error")
	}
}

func TestResolveWater(t *testing.T) {
	game := []byte(`<?xml version="1.0" encoding="us-ascii"?>
<Catalog>
    <CWater default="1"><State><Height value="9.0"/><Color value="0.1,0.35,0.5,0.3"/></State></CWater>
    <CWater id="Char"><State index="0"><Height value="9.0"/><Color value="0.82,0.29,0.09,0.3"/></State><IsLava value="1"/></CWater>
    <CWater id="ZerusPool"><State index="0"><Height value="7.6"/></State><State><Height value="7.9"/></State></CWater>
</Catalog>`)
	mapData := []byte(`<?xml version="1.0" encoding="utf-8"?><Catalog>
    <CWater id="Tarsonis"><State index="0"><Height value="11.6"/><Color value="0.24,0.62,0.85,0.3"/></State></CWater>
    <CWater id="Char"/>
    <CWater id="BelShir"><State index="0" Height="1.85"/></CWater>
    <CWater id="ZerusPool"><State index="0" Height="7.7" Color="0.12,0.26,0.34,0.418"/></CWater>
</Catalog>`)
	got := ResolveWater([]string{"Tarsonis", "Char", "ZerusPool", "BelShir", "Unknown"}, game, mapData)
	want := map[string]WaterType{
		"Tarsonis":  {Height: 11.6, Color: [4]float64{0.24, 0.62, 0.85, 0.3}},
		"Char":      {Height: 9, Color: [4]float64{0.82, 0.29, 0.09, 0.3}, Lava: true},
		"ZerusPool": {Height: 7.7, Color: [4]float64{0.12, 0.26, 0.34, 0.418}}, // the map's, in attributes; the game's second state doesn't count
		"BelShir":   {Height: 1.85, Color: [4]float64{0.1, 0.35, 0.5, 0.3}},
		"Unknown":   {Height: 9, Color: [4]float64{0.1, 0.35, 0.5, 0.3}},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %+v, want %+v", id, got[id], w)
		}
	}
	// The game's own first state, not its second.
	if z := ResolveWater([]string{"ZerusPool"}, game)["ZerusPool"]; z.Height != 7.6 {
		t.Errorf("game ZerusPool at %v, want 7.6", z.Height)
	}
	// Without game data, the game's default stands in.
	if w := ResolveWater([]string{"Tarsonis"}, nil)["Tarsonis"]; w != defaultWater {
		t.Errorf("no data: %+v", w)
	}
}

// TestRealWater checks the water of three ladder maps against what the map
// files hold (read by hand from t3Water and WaterData.xml).
func TestRealWater(t *testing.T) {
	cases := []struct {
		file  string
		n     int
		first WaterRect
		types map[string]float64 // height from the map's own WaterData.xml
	}{
		{"LeyLinesAIE_v3.SC2Map", 67, WaterRect{"Agria", 0, 116, 36, 144}, map[string]float64{"Agria": 3.4, "Redstone": 1.05}},
		{"PersephoneAIE_v4.SC2Map", 54, WaterRect{"Tarsonis", 0, 48, 12, 56}, map[string]float64{"Tarsonis": 11.6}},
		{"PylonAIE_v4.SC2Map", 0, WaterRect{}, nil},
	}
	for _, c := range cases {
		path := filepath.Join("..", "..", "testdata", "maps", c.file)
		m, err := Open(path)
		if err != nil {
			t.Logf("%s: %v (skipped)", c.file, err)
			continue
		}
		if len(m.Water) != c.n {
			t.Errorf("%s: %d water rects, want %d", c.file, len(m.Water), c.n)
			continue
		}
		if c.n > 0 && m.Water[0] != c.first {
			t.Errorf("%s: first %v, want %v", c.file, m.Water[0], c.first)
		}
		for _, w := range m.Water {
			if w.X0 < 0 || w.Y0 < 0 || w.X1 > float32(m.Width) || w.Y1 > float32(m.Height) {
				t.Errorf("%s: %v outside the %dx%d map", c.file, w, m.Width, m.Height)
			}
		}
		var ids []string
		for id := range c.types {
			ids = append(ids, id)
		}
		types := ResolveWater(ids, m.WaterData)
		for id, h := range c.types {
			if types[id].Height != h {
				t.Errorf("%s: %s at %v, want %v", c.file, id, types[id].Height, h)
			}
		}
	}
}
