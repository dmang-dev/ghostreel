// SPDX-License-Identifier: Apache-2.0

package mapfile

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"strconv"
	"strings"
)

var le = binary.LittleEndian

// checkMagic verifies a 4-byte tag. The game writes the tags byte-reversed
// ("MapI" is stored as "IpaM"); the strings here are as stored.
func checkMagic(d []byte, magic string) error {
	if len(d) < 8 || string(d[:4]) != magic {
		return fmt.Errorf("bad magic, want %q", magic)
	}
	return nil
}

// parseMapInfo reads dimensions, fog, tileset and camera bounds.
//
//	0x00 "IpaM"  u32 version
//	     u32 ×2  (version >= 0x18; meaning unknown)
//	     u32 width, u32 height        (cells)
//	     … preview-image settings (layout varies by version) …
//	     cstring fog ("Dark"), cstring tileset
//	     u32 camera left, bottom, right, top
//
// The camera bounds are the playable area. The bytes between the size and
// the fog string vary, so the parser scans for the first pair of printable
// NUL-terminated strings followed by plausible bounds.
func parseMapInfo(d []byte, mp *Map) error {
	if err := checkMagic(d, "IpaM"); err != nil {
		return err
	}
	ver := le.Uint32(d[4:])
	off := 8
	if ver >= 0x18 {
		off += 8
	}
	if len(d) < off+8 {
		return errors.New("truncated")
	}
	mp.Width, mp.Height = int(le.Uint32(d[off:])), int(le.Uint32(d[off+4:]))
	if mp.Width <= 0 || mp.Height <= 0 || mp.Width > 1024 || mp.Height > 1024 {
		return fmt.Errorf("implausible size %dx%d", mp.Width, mp.Height)
	}
	mp.Playable = Rect{0, 0, mp.Width, mp.Height}
	for i := off + 8; i < len(d)-24; i++ {
		fog, n1, ok := cstring(d, i)
		if !ok || len(fog) < 3 {
			continue
		}
		tileset, n2, ok := cstring(d, i+n1)
		if !ok || len(tileset) < 3 {
			continue
		}
		b := i + n1 + n2
		if b+16 > len(d) {
			break
		}
		r := Rect{int(le.Uint32(d[b:])), int(le.Uint32(d[b+4:])), int(le.Uint32(d[b+8:])), int(le.Uint32(d[b+12:]))}
		if r.Left < r.Right && r.Bottom < r.Top && r.Right <= mp.Width && r.Top <= mp.Height {
			mp.Fog, mp.Tileset, mp.Playable = fog, tileset, r
			return nil
		}
	}
	mp.Warnings = append(mp.Warnings, "MapInfo: camera bounds not found; using the whole map")
	return nil
}

// cstring reads a printable NUL-terminated string at off; n includes the NUL.
func cstring(d []byte, off int) (s string, n int, ok bool) {
	for i := off; i < len(d) && i-off < 128; i++ {
		c := d[i]
		if c == 0 {
			return string(d[off:i]), i - off + 1, i > off
		}
		if c < 0x20 || c > 0x7e {
			return "", 0, false
		}
	}
	return "", 0, false
}

// parseSyncHeightMap reads t3SyncHeightMap: "SMAP", u32 version, u32 w, u32 h
// (vertices), header padding up to len - w*h*4, then one u32 per vertex in
// fixed point with 15 fractional bits. That gives heights relative to the
// map's base level (2 units per cliff level, as in the game); see
// groundBase for the base.
func parseSyncHeightMap(d []byte, w, h int) ([]float32, error) {
	if err := checkMagic(d, "SMAP"); err != nil {
		return nil, err
	}
	fw, fh := int(le.Uint32(d[8:])), int(le.Uint32(d[12:]))
	if fw != w || fh != h {
		return nil, fmt.Errorf("grid %dx%d, want %dx%d vertices", fw, fh, w, h)
	}
	hdr := len(d) - w*h*4
	if hdr < 16 {
		return nil, fmt.Errorf("size %d too small for %dx%d", len(d), w, h)
	}
	out := make([]float32, w*h)
	for i := range out {
		out[i] = float32(le.Uint32(d[hdr+i*4:])) / 32768
	}
	return out, nil
}

// unmarshalXML decodes map XML, which declares us-ascii or utf-8; both read
// fine as UTF-8.
func unmarshalXML(d []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(d))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return dec.Decode(v)
}

type terrainXML struct {
	bias, scale, standard float64 // t3HeightMap quantization
	textures              []string
	tiling                [4]float64 // texture repeats across the map (x, y) and offsets
}

// parseTerrainXML reads what t3Terrain.xml says about the visual height map
// (<vertData quantizeBias quantizeScale standardHeight>) and the texture
// layers (<textureList>, 8 per texture set; the first set is the terrain's).
func parseTerrainXML(d []byte) (terrainXML, error) {
	var x struct {
		HeightMap struct {
			UVTiling string `xml:"uvtiling,attr"`
			VertData struct {
				Bias     float64 `xml:"quantizeBias,attr"`
				Scale    float64 `xml:"quantizeScale,attr"`
				Standard float64 `xml:"standardHeight,attr"`
			} `xml:"vertData"`
			Textures []struct {
				I    int    `xml:"i,attr"`
				Name string `xml:"name,attr"`
			} `xml:"textureList>texture"`
		} `xml:"heightMap"`
	}
	if err := unmarshalXML(d, &x); err != nil {
		return terrainXML{}, err
	}
	v := x.HeightMap.VertData
	t := terrainXML{bias: v.Bias, scale: v.Scale, standard: v.Standard}
	// Layer i is texture i; keep the slots even when empty.
	names := make([]string, 8)
	found := false
	for _, tex := range x.HeightMap.Textures {
		if tex.I >= 0 && tex.I < 8 {
			names[tex.I] = tex.Name
			found = found || tex.Name != ""
		}
	}
	if found {
		t.textures = names
	}
	for i, f := range strings.Fields(x.HeightMap.UVTiling) {
		if i < 4 {
			t.tiling[i], _ = strconv.ParseFloat(f, 64)
		}
	}
	return t, nil
}

// TextureMasks are the terrain's painted texture layers: weight 0-255 of
// layer L's texture at texel (u, v) is Layers[L][v*W+u], v = 0 at the
// bottom of the map. Texels aren't square: there are W/width per cell
// across and H/height per cell up (4 and 8 on the maps seen so far).
type TextureMasks struct {
	W, H   int
	Layers [][]uint8
	// Tiling is how many times layer textures repeat across the map (x, y),
	// then their offsets, from t3Terrain.xml's uvtiling.
	Tiling [4]float64
}

// parseTextureMasks reads t3TextureMasks: "MASK", version 0x66, u32 at 12
// and 16 (texel width x2 and height), 64-byte header, then 8 layers one
// after another. A layer is 32x32-byte tiles in strips two tiles high: down
// the strip first, then across, strips bottom to top.
func parseTextureMasks(d []byte) (*TextureMasks, error) {
	if err := checkMagic(d, "MASK"); err != nil {
		return nil, err
	}
	const layers, hdr, tile = 8, 64, 32
	if len(d) < hdr {
		return nil, errors.New("truncated")
	}
	h := int(le.Uint32(d[16:]))
	if h <= 0 || h%(2*tile) != 0 || (len(d)-hdr)%(layers*h) != 0 {
		return nil, fmt.Errorf("unexpected size %d for height %d", len(d), h)
	}
	w := (len(d) - hdr) / layers / h
	if w%tile != 0 {
		return nil, fmt.Errorf("unexpected layer width %d", w)
	}
	m := &TextureMasks{W: w, H: h}
	tilesAcross := w / tile
	per := w * h
	for l := 0; l < layers; l++ {
		src := d[hdr+l*per : hdr+(l+1)*per]
		out := make([]uint8, per)
		for t := 0; t < per/(tile*tile); t++ {
			strip, r := t/(2*tilesAcross), t%(2*tilesAcross)
			tx, ty := r/2, strip*2+r%2
			for y := 0; y < tile; y++ {
				copy(out[(ty*tile+y)*w+tx*tile:][:tile], src[t*tile*tile+y*tile:][:tile])
			}
		}
		m.Layers = append(m.Layers, out)
	}
	return m, nil
}

// parseVisualHeights reads t3HeightMap: "HMAP", u32 version, u32 w, u32 h
// (vertices), header up to len - w*h*6, then 6 bytes per vertex: u16 (not
// understood), u16 quantized height, u16 cliff level. Height = q * scale +
// standardHeight - bias, with the parameters from t3Terrain.xml.
func parseVisualHeights(d []byte, w, h int, t terrainXML) ([]float32, error) {
	if err := checkMagic(d, "HMAP"); err != nil {
		return nil, err
	}
	if t.scale == 0 {
		return nil, fmt.Errorf("no quantization in t3Terrain.xml")
	}
	fw, fh := int(le.Uint32(d[8:])), int(le.Uint32(d[12:]))
	if fw != w || fh != h {
		return nil, fmt.Errorf("grid %dx%d, want %dx%d vertices", fw, fh, w, h)
	}
	hdr := len(d) - w*h*6
	if hdr < 16 {
		return nil, fmt.Errorf("size %d too small for %dx%d", len(d), w, h)
	}
	out := make([]float32, w*h)
	for i := range out {
		out[i] = float32(float64(le.Uint16(d[hdr+i*6+2:]))*t.scale + t.standard - t.bias)
	}
	return out, nil
}

// parseCliffLevels reads t3SyncCliffLevel: "CLIF", u32 version, u32 w, u32 h
// (cells), header padding up to len - w*h*2, then one u16 per cell.
func parseCliffLevels(d []byte, w, h int) ([]uint16, error) {
	if err := checkMagic(d, "CLIF"); err != nil {
		return nil, err
	}
	fw, fh := int(le.Uint32(d[8:])), int(le.Uint32(d[12:]))
	if fw != w || fh != h {
		return nil, fmt.Errorf("grid %dx%d, want %dx%d", fw, fh, w, h)
	}
	hdr := len(d) - w*h*2
	if hdr < 16 {
		return nil, fmt.Errorf("size %d too small for %dx%d", len(d), w, h)
	}
	out := make([]uint16, w*h)
	for i := range out {
		out[i] = le.Uint16(d[hdr+i*2:])
	}
	return out, nil
}

// parseCellFlags reads t3CellFlags: "LFCT", u32 version, then the cell grid
// size as u32 w, u32 h at offset 0x18, and one byte per cell after a 32-byte
// header (the header length is derived from the file size).
func parseCellFlags(d []byte, w, h int) ([]uint8, error) {
	if err := checkMagic(d, "LFCT"); err != nil {
		return nil, err
	}
	if len(d) < 0x20 {
		return nil, errors.New("truncated")
	}
	fw, fh := int(le.Uint32(d[0x18:])), int(le.Uint32(d[0x1c:]))
	if fw != w || fh != h {
		return nil, fmt.Errorf("grid %dx%d, want %dx%d", fw, fh, w, h)
	}
	hdr := len(d) - w*h
	if hdr < 0x20 {
		return nil, fmt.Errorf("size %d too small for %dx%d", len(d), w, h)
	}
	return append([]uint8(nil), d[hdr:]...), nil
}

type xmlObjects struct {
	Units   []xmlObject `xml:"ObjectUnit"`
	Doodads []xmlObject `xml:"ObjectDoodad"`
	Points  []xmlObject `xml:"ObjectPoint"`
}

type xmlObject struct {
	Position  string  `xml:"Position,attr"`
	Rotation  float64 `xml:"Rotation,attr"`
	UnitType  string  `xml:"UnitType,attr"`
	Type      string  `xml:"Type,attr"`
	Name      string  `xml:"Name,attr"`
	Player    int     `xml:"Player,attr"`
	Scale     string  `xml:"Scale,attr"`
	Variation int     `xml:"Variation,attr"`
}

// parseObjects reads the Objects XML (<PlacedObjects>).
func parseObjects(d []byte) ([]Object, error) {
	var x xmlObjects
	if err := unmarshalXML(d, &x); err != nil {
		return nil, err
	}
	var out []Object
	add := func(kind ObjectKind, list []xmlObject) {
		for _, o := range list {
			typ := o.Type
			if kind == ObjectUnit {
				typ = o.UnitType
			}
			obj := Object{Kind: kind, Type: typ, Name: o.Name, Rotation: o.Rotation, Player: o.Player}
			p := strings.Split(o.Position, ",")
			if len(p) >= 2 {
				obj.X, _ = strconv.ParseFloat(p[0], 64)
				obj.Y, _ = strconv.ParseFloat(p[1], 64)
			}
			if len(p) >= 3 {
				obj.Z, _ = strconv.ParseFloat(p[2], 64)
			}
			if kind == ObjectDoodad {
				obj.Variation = o.Variation
				if sc := strings.Split(o.Scale, ","); len(sc) == 3 && o.Scale != "1,1,1" {
					obj.Scale = make([]float64, 3)
					for i := range sc {
						obj.Scale[i], _ = strconv.ParseFloat(sc[i], 64)
					}
				}
			}
			out = append(out, obj)
		}
	}
	add(ObjectUnit, x.Units)
	add(ObjectPoint, x.Points)
	add(ObjectDoodad, x.Doodads)
	return out, nil
}

// decodeTGA decodes the uncompressed true-color TGA files maps use for
// Minimap.tga (image type 2, 24 or 32 bits per pixel).
func decodeTGA(d []byte) (image.Image, error) {
	if len(d) < 18 {
		return nil, errors.New("truncated header")
	}
	idLen, cmapType, imgType := int(d[0]), d[1], d[2]
	cmapLen, cmapBits := int(le.Uint16(d[5:])), int(d[7])
	w, h := int(le.Uint16(d[12:])), int(le.Uint16(d[14:]))
	bpp, desc := int(d[16]), d[17]
	if imgType != 2 || (bpp != 24 && bpp != 32) {
		return nil, fmt.Errorf("unsupported TGA type %d at %d bpp", imgType, bpp)
	}
	off := 18 + idLen
	if cmapType != 0 {
		off += cmapLen * ((cmapBits + 7) / 8)
	}
	px := bpp / 8
	if len(d) < off+w*h*px {
		return nil, errors.New("truncated pixel data")
	}
	topDown := desc&0x20 != 0
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := y
		if !topDown {
			row = h - 1 - y
		}
		for x := 0; x < w; x++ {
			p := d[off+(row*w+x)*px:]
			c := color.NRGBA{R: p[2], G: p[1], B: p[0], A: 255}
			if px == 4 {
				c.A = p[3]
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img, nil
}
