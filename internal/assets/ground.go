// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"

	"github.com/OWNER/ghostreel/internal/mapfile"
)

// maxGround bounds the composed ground image (pixels per side).
const maxGround = 2048

// GroundJPEG paints a map's ground the way the game blends it: at every
// point, each texture layer's texture (tiled across the map as
// t3Terrain.xml says) weighted by its painted mask (the weights sum to
// 255). The image covers the whole map, north up, at up to 8 pixels per
// cell. Layers whose texture isn't in the game data are left out.
func (l *Library) GroundJPEG(m *mapfile.Map) ([]byte, error) {
	if m.Masks == nil || len(m.TerrainTextures) == 0 {
		return nil, ErrNoModel
	}
	img, err := l.composeGround(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (l *Library) composeGround(m *mapfile.Map) (*image.NRGBA, error) {
	mk := m.Masks
	texs := make([]*image.NRGBA, len(mk.Layers))
	found := false
	for i := range mk.Layers {
		if i >= len(m.TerrainTextures) || m.TerrainTextures[i] == "" {
			continue
		}
		path, ok := l.terrain.field("CTerrainTex", m.TerrainTextures[i], "Texture")
		if !ok {
			continue
		}
		if img := l.texture(path); img != nil {
			texs[i] = toNRGBA(img)
			found = true
		}
	}
	if !found {
		return nil, errors.New("assets: none of the map's terrain textures is in the game data")
	}
	ppc := 8
	for ppc > 1 && (m.Width*ppc > maxGround || m.Height*ppc > maxGround) {
		ppc /= 2
	}
	w, h := m.Width*ppc, m.Height*ppc
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Texels per cell in the masks (4 across, 8 up on every map seen).
	su, sv := float64(mk.W)/float64(m.Width), float64(mk.H)/float64(m.Height)
	tx, ty, ox, oy := mk.Tiling[0], mk.Tiling[1], mk.Tiling[2], mk.Tiling[3]
	if tx == 0 || ty == 0 {
		tx, ty = float64(m.Width)*3/32, float64(m.Height)*3/32 // a repeat every ~10.7 cells
	}
	weights := make([]float64, len(mk.Layers))
	for py := 0; py < h; py++ {
		y := float64(m.Height) - (float64(py)+0.5)/float64(ppc) // cells, up
		v := min(max(int(y*sv), 0), mk.H-1)
		// Texture coordinates: repeats across the map, plus the offset.
		tvf := y/float64(m.Height)*ty + oy/float64(m.Height)*ty
		for px := 0; px < w; px++ {
			x := (float64(px) + 0.5) / float64(ppc)
			u := min(max(int(x*su), 0), mk.W-1)
			tuf := x/float64(m.Width)*tx + ox/float64(m.Width)*tx
			var r, g, b, sum float64
			for i, layer := range mk.Layers {
				weights[i] = float64(layer[v*mk.W+u])
				t := texs[i]
				if t == nil || weights[i] == 0 {
					continue
				}
				c := sampleWrap(t, tuf, tvf)
				r += float64(c.R) * weights[i]
				g += float64(c.G) * weights[i]
				b += float64(c.B) * weights[i]
				sum += weights[i]
			}
			if sum == 0 {
				for _, t := range texs {
					if t != nil {
						c := sampleWrap(t, tuf, tvf)
						r, g, b, sum = float64(c.R), float64(c.G), float64(c.B), 1
						break
					}
				}
			}
			o := out.PixOffset(px, py)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r/sum), uint8(g/sum), uint8(b/sum), 255
		}
	}
	return out, nil
}

// sampleWrap reads a repeating texture at (u, v) in repeats, bilinearly.
func sampleWrap(t *image.NRGBA, u, v float64) color.NRGBA {
	tw, th := t.Rect.Dx(), t.Rect.Dy()
	fx := (u-math.Floor(u))*float64(tw) - 0.5
	fy := (1-(v-math.Floor(v)))*float64(th) - 0.5 // texture rows run down
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	ax, ay := fx-float64(x0), fy-float64(y0)
	at := func(x, y int) [3]float64 {
		x, y = ((x%tw)+tw)%tw, ((y%th)+th)%th
		o := t.PixOffset(t.Rect.Min.X+x, t.Rect.Min.Y+y)
		return [3]float64{float64(t.Pix[o]), float64(t.Pix[o+1]), float64(t.Pix[o+2])}
	}
	c00, c10, c01, c11 := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
	var out [3]uint8
	for k := 0; k < 3; k++ {
		top := c00[k]*(1-ax) + c10[k]*ax
		bot := c01[k]*(1-ax) + c11[k]*ax
		out[k] = uint8(top*(1-ay) + bot*ay + 0.5)
	}
	return color.NRGBA{out[0], out[1], out[2], 255}
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	n := image.NewNRGBA(img.Bounds())
	draw.Draw(n, n.Rect, img, img.Bounds().Min, draw.Src)
	return n
}
