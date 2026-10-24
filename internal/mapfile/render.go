// SPDX-License-Identifier: Apache-2.0

package mapfile

import (
	"image"
	"image/color"
	"math"
)

// reliefScale exaggerates slopes in the hillshade.
const reliefScale = 6

// Hillshade renders the height map at scale pixels per cell, lit from the
// north-west, with cliff levels tinting the base color so plateaus read at a
// glance. Image row 0 is the top of the map (high y).
func (m *Map) Hillshade(scale int) image.Image {
	if scale < 1 {
		scale = 1
	}
	img := image.NewNRGBA(image.Rect(0, 0, m.Width*scale, m.Height*scale))
	if m.Heights == nil {
		return img
	}
	lo, hi := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for y := m.Playable.Bottom; y < m.Playable.Top; y++ {
		for x := m.Playable.Left; x < m.Playable.Right; x++ {
			h := m.HeightAt(x, y)
			lo, hi = min(lo, h), max(hi, h)
		}
	}
	span := max(hi-lo, 0.5)
	// Light direction (from north-west, 45° up), normalized.
	lx, ly, lz := -0.5, 0.5, 0.7071
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			// Slopes exaggerated so cliffs (2 units high) read clearly.
			dzdx := (m.HeightAt(min(x+1, m.Width-1), y) - m.HeightAt(max(x-1, 0), y)) * reliefScale / 2
			dzdy := (m.HeightAt(x, min(y+1, m.Height-1)) - m.HeightAt(x, max(y-1, 0))) * reliefScale / 2
			nx, ny, nz := -float64(dzdx), -float64(dzdy), 1.0
			n := math.Sqrt(nx*nx + ny*ny + nz*nz)
			shade := math.Max(0.15, (nx*lx+ny*ly+nz*lz)/n)
			t := float64((m.HeightAt(x, y) - lo) / span)
			t = math.Max(0, math.Min(1, t))
			// Low ground dark slate, high ground warm sand.
			r := (60 + 140*t) * shade
			g := (70 + 120*t) * shade
			b := (85 + 70*t) * shade
			c := color.NRGBA{uint8(r), uint8(g), uint8(b), 255}
			if !m.inPlayable(x, y) {
				c = color.NRGBA{c.R / 3, c.G / 3, c.B / 3, 255}
			}
			fill(img, x, m.Height-1-y, scale, c)
		}
	}
	return img
}

func (m *Map) inPlayable(x, y int) bool {
	p := m.Playable
	return x >= p.Left && x < p.Right && y >= p.Bottom && y < p.Top
}

func fill(img *image.NRGBA, cx, cy, scale int, c color.NRGBA) {
	for dy := 0; dy < scale; dy++ {
		for dx := 0; dx < scale; dx++ {
			img.SetNRGBA(cx*scale+dx, cy*scale+dy, c)
		}
	}
}

// FlagLayer renders cells whose t3CellFlags byte has any of mask's bits set,
// for studying the undocumented flag meanings.
func (m *Map) FlagLayer(mask uint8, scale int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, m.Width*scale, m.Height*scale))
	for y := 0; y < m.Height && m.CellFlags != nil; y++ {
		for x := 0; x < m.Width; x++ {
			c := color.NRGBA{20, 20, 20, 255}
			if m.CellFlags[y*m.Width+x]&mask != 0 {
				c = color.NRGBA{230, 230, 230, 255}
			}
			fill(img, x, m.Height-1-y, scale, c)
		}
	}
	return img
}

// CliffLayer renders cliff levels as gray steps.
func (m *Map) CliffLayer(scale int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, m.Width*scale, m.Height*scale))
	if m.CliffLevels == nil {
		return img
	}
	var hi uint16 = 1
	for _, v := range m.CliffLevels {
		hi = max(hi, v)
	}
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			v := uint8(uint32(m.CliffLevels[y*m.Width+x]) * 255 / uint32(hi))
			fill(img, x, m.Height-1-y, scale, color.NRGBA{v, v, v, 255})
		}
	}
	return img
}
