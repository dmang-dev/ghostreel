// SPDX-License-Identifier: Apache-2.0

// Package dds decodes the DirectDraw Surface textures the game ships: DXT1,
// DXT3 and DXT5 block compression and uncompressed 32-bit color. Only one mip
// level is decoded.
package dds

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
)

var le = binary.LittleEndian

// Info describes a texture without decoding it.
type Info struct {
	Width, Height int
	Mips          int
	Format        string // "DXT1", "DXT3", "DXT5", "BGRA8", ...
}

const headerSize = 128 // "DDS " + 124-byte header

func parseHeader(b []byte) (Info, int, error) {
	if len(b) < headerSize || string(b[:4]) != "DDS " {
		return Info{}, 0, errors.New("dds: not a DDS file")
	}
	in := Info{Height: int(le.Uint32(b[12:])), Width: int(le.Uint32(b[16:])), Mips: int(le.Uint32(b[28:]))}
	if in.Mips == 0 {
		in.Mips = 1
	}
	pfFlags := le.Uint32(b[80:])
	data := headerSize
	switch {
	case pfFlags&0x4 != 0: // FOURCC
		in.Format = string(b[84:88])
		if in.Format == "DX10" {
			return in, 0, errors.New("dds: DX10 textures are not supported")
		}
	case pfFlags&0x40 != 0 && le.Uint32(b[88:]) == 32: // RGB, 32 bits
		if le.Uint32(b[92:]) == 0x00ff0000 && le.Uint32(b[100:]) == 0xff {
			in.Format = "BGRA8"
		} else {
			in.Format = "RGBA8"
		}
	case pfFlags&0x40 != 0 && le.Uint32(b[88:]) == 24:
		in.Format = "BGR8"
	case pfFlags&0x20000 != 0 && le.Uint32(b[88:]) == 16: // luminance + alpha
		in.Format = "LA8"
	default:
		return in, 0, fmt.Errorf("dds: unsupported pixel format flags %#x, %d bits", pfFlags, le.Uint32(b[88:]))
	}
	if in.Width <= 0 || in.Height <= 0 || in.Width > 1<<14 || in.Height > 1<<14 {
		return in, 0, fmt.Errorf("dds: bad size %dx%d", in.Width, in.Height)
	}
	return in, data, nil
}

// DecodeInfo reads the header.
func DecodeInfo(b []byte) (Info, error) {
	in, _, err := parseHeader(b)
	return in, err
}

func levelSize(format string, w, h int) int {
	bw, bh := (w+3)/4, (h+3)/4
	switch format {
	case "DXT1":
		return bw * bh * 8
	case "DXT3", "DXT5":
		return bw * bh * 16
	case "BGR8":
		return w * h * 3
	case "LA8":
		return w * h * 2
	default:
		return w * h * 4
	}
}

// Decode decodes the first mip level no larger than maxSize on either side
// (0 means the full size), falling back to the smallest stored level.
func Decode(b []byte, maxSize int) (*image.NRGBA, error) {
	in, off, err := parseHeader(b)
	if err != nil {
		return nil, err
	}
	switch in.Format {
	case "DXT1", "DXT3", "DXT5", "BGRA8", "RGBA8", "BGR8", "LA8":
	default:
		return nil, fmt.Errorf("dds: unsupported format %q", in.Format)
	}
	w, h := in.Width, in.Height
	for lvl := 0; lvl < in.Mips-1 && maxSize > 0 && (w > maxSize || h > maxSize); lvl++ {
		off += levelSize(in.Format, w, h)
		w, h = max(w/2, 1), max(h/2, 1)
	}
	n := levelSize(in.Format, w, h)
	if off+n > len(b) {
		return nil, errors.New("dds: truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	src := b[off : off+n]
	switch in.Format {
	case "BGR8":
		for i := 0; i < w*h; i++ {
			p := src[3*i : 3*i+3]
			img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = p[2], p[1], p[0], 255
		}
	case "LA8":
		for i := 0; i < w*h; i++ {
			l := src[2*i]
			img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = l, l, l, src[2*i+1]
		}
	case "BGRA8", "RGBA8":
		for i := 0; i < w*h; i++ {
			p := src[4*i : 4*i+4]
			if in.Format == "BGRA8" {
				img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = p[2], p[1], p[0], p[3]
			} else {
				copy(img.Pix[4*i:], p)
			}
		}
	default:
		decodeBlocks(img, src, in.Format)
	}
	return img, nil
}

func decodeBlocks(img *image.NRGBA, src []byte, format string) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	bsize := 16
	if format == "DXT1" {
		bsize = 8
	}
	var px [16]color.NRGBA
	i := 0
	for by := 0; by < (h+3)/4; by++ {
		for bx := 0; bx < (w+3)/4; bx++ {
			blk := src[i : i+bsize]
			i += bsize
			switch format {
			case "DXT1":
				colorBlock(&px, blk, true)
			case "DXT3":
				colorBlock(&px, blk[8:], false)
				for k := 0; k < 16; k++ {
					a := (blk[k/2] >> (4 * (k % 2))) & 0xf
					px[k].A = a * 17
				}
			case "DXT5":
				colorBlock(&px, blk[8:], false)
				alphaBlock(&px, blk)
			}
			for k := 0; k < 16; k++ {
				x, y := bx*4+k%4, by*4+k/4
				if x < w && y < h {
					o := img.PixOffset(x, y)
					c := px[k]
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c.R, c.G, c.B, c.A
				}
			}
		}
	}
}

func rgb565(v uint16) [3]int {
	r, g, b := int(v>>11&31), int(v>>5&63), int(v&31)
	return [3]int{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2}
}

func colorBlock(px *[16]color.NRGBA, b []byte, dxt1 bool) {
	c0, c1 := le.Uint16(b), le.Uint16(b[2:])
	p0, p1 := rgb565(c0), rgb565(c1)
	var pal [4]color.NRGBA
	mix := func(a, b [3]int, wa, wb, d int) color.NRGBA {
		return color.NRGBA{uint8((a[0]*wa + b[0]*wb) / d), uint8((a[1]*wa + b[1]*wb) / d), uint8((a[2]*wa + b[2]*wb) / d), 255}
	}
	pal[0], pal[1] = mix(p0, p1, 1, 0, 1), mix(p0, p1, 0, 1, 1)
	if c0 > c1 || !dxt1 {
		pal[2], pal[3] = mix(p0, p1, 2, 1, 3), mix(p0, p1, 1, 2, 3)
	} else {
		pal[2], pal[3] = mix(p0, p1, 1, 1, 2), color.NRGBA{}
	}
	bits := le.Uint32(b[4:])
	for k := 0; k < 16; k++ {
		px[k] = pal[bits>>(2*k)&3]
	}
}

func alphaBlock(px *[16]color.NRGBA, b []byte) {
	a0, a1 := int(b[0]), int(b[1])
	var pal [8]int
	pal[0], pal[1] = a0, a1
	if a0 > a1 {
		for k := 1; k < 7; k++ {
			pal[k+1] = ((7-k)*a0 + k*a1) / 7
		}
	} else {
		for k := 1; k < 5; k++ {
			pal[k+1] = ((5-k)*a0 + k*a1) / 5
		}
		pal[6], pal[7] = 0, 255
	}
	bits := uint64(b[2]) | uint64(b[3])<<8 | uint64(b[4])<<16 | uint64(b[5])<<24 | uint64(b[6])<<32 | uint64(b[7])<<40
	for k := 0; k < 16; k++ {
		px[k].A = uint8(pal[bits>>(3*k)&7])
	}
}
