// SPDX-License-Identifier: Apache-2.0

package mapfile

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// WaterRect is an area of water (or lava) the map paints, in cells. Its
// surface lies at its type's height, so it shows only where that is above
// the ground; maps often keep areas whose water sits under the terrain.
type WaterRect struct {
	Type string `json:"type"` // a CWater id: "Tarsonis", "Char"
	// Corners, x1 and y1 exclusive.
	X0 float32 `json:"x0"`
	Y0 float32 `json:"y0"`
	X1 float32 `json:"x1"`
	Y1 float32 `json:"y1"`
}

// parseWater decodes t3Water: a 32-byte header ("WATR", version, record
// count at offset 8), then 56-byte records. A record starts with the water
// type's name (NUL-terminated, in 16 bytes) and ends with the rectangle as
// four float32s (x0, y0, x1, y1); the bytes between hold no map data.
func parseWater(b []byte) ([]WaterRect, error) {
	const head, rec = 32, 56
	if len(b) < head || string(b[:4]) != "WATR" {
		return nil, fmt.Errorf("not t3Water")
	}
	n := int(binary.LittleEndian.Uint32(b[8:]))
	if n < 0 || len(b) < head+n*rec {
		return nil, fmt.Errorf("%d records in %d bytes", n, len(b))
	}
	out := make([]WaterRect, 0, n)
	f := func(o int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b[o:])) }
	for i := range n {
		o := head + i*rec
		name := b[o : o+16]
		if j := bytes.IndexByte(name, 0); j >= 0 {
			name = name[:j]
		}
		r := WaterRect{Type: string(name), X0: f(o + 40), Y0: f(o + 44), X1: f(o + 48), Y1: f(o + 52)}
		if r.Type == "" || !(r.X1 > r.X0 && r.Y1 > r.Y0) {
			return nil, fmt.Errorf("record %d: %q %v", i, r.Type, r)
		}
		out = append(out, r)
	}
	return out, nil
}

// WaterType is how a kind of water looks, from WaterData.xml (CWater).
type WaterType struct {
	Height float64    `json:"height"` // surface height, in the terrain's units
	Color  [4]float64 `json:"color"`  // RGB and opacity, 0–1
	Lava   bool       `json:"lava,omitempty"`
}

// defaultWater is the game's own CWater default (core.sc2mod), for when no
// game data is at hand.
var defaultWater = WaterType{Height: 9, Color: [4]float64{0.1, 0.35, 0.5, 0.3}}

// ResolveWater works out each named water type from WaterData.xml catalogs,
// applied in order: the game's (core first) and then the map's own. An
// entry's first state is the one maps use. Types no catalog mentions get
// the default CWater.
func ResolveWater(ids []string, docs ...[]byte) map[string]WaterType {
	base := defaultWater
	fields := map[string][]waterEntry{}
	for _, d := range docs {
		var c struct {
			Entries []waterEntry `xml:"CWater"`
		}
		// The game's catalogs declare us-ascii, which encoding/xml won't
		// read without a charset reader.
		dec := xml.NewDecoder(bytes.NewReader(d))
		dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
		if dec.Decode(&c) != nil {
			continue
		}
		for _, e := range c.Entries {
			if e.Default == "1" && e.ID == "" {
				e.apply(&base)
				continue
			}
			fields[e.ID] = append(fields[e.ID], e)
		}
	}
	out := map[string]WaterType{}
	for _, id := range ids {
		t := base
		for _, e := range fields[id] {
			e.apply(&t)
		}
		out[id] = t
	}
	return out
}

// waterEntry is the part of a CWater entry that says how it looks.
type waterEntry struct {
	ID      string `xml:"id,attr"`
	Default string `xml:"default,attr"`
	States  []struct {
		Index string `xml:"index,attr"`
		// The short form puts fields in attributes: <State index="0" Height="1.05"/>.
		Height    string     `xml:"Height,attr"`
		ColorAttr string     `xml:"Color,attr"`
		Value     []xmlValue `xml:"Height"`
		Color     []xmlValue `xml:"Color"`
	} `xml:"State"`
	IsLava []xmlValue `xml:"IsLava"`
}

type xmlValue struct {
	Value string `xml:"value,attr"`
}

func (e waterEntry) apply(t *WaterType) {
	for _, s := range e.States {
		// A state without an index is a new one appended after the first;
		// only the first (index 0) counts here.
		if s.Index != "0" && !(s.Index == "" && e.Default == "1") {
			continue
		}
		h := s.Height
		if len(s.Value) > 0 {
			h = s.Value[0].Value
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(h), 64); err == nil {
			t.Height = v
		}
		col := s.ColorAttr
		if len(s.Color) > 0 {
			col = s.Color[0].Value
		}
		if col != "" {
			if c := strings.Split(col, ","); len(c) == 4 {
				var rgba [4]float64
				ok := true
				for i := range c {
					v, err := strconv.ParseFloat(strings.TrimSpace(c[i]), 64)
					ok = ok && err == nil
					rgba[i] = v
				}
				if ok {
					t.Color = rgba
				}
			}
		}
		break
	}
	if len(e.IsLava) > 0 {
		t.Lava = e.IsLava[0].Value == "1"
	}
}
