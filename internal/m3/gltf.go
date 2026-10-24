// SPDX-License-Identifier: Apache-2.0

package m3

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"sort"
)

// GLBOptions controls the glTF export.
type GLBOptions struct {
	// Texture returns a decoded texture by path, or nil when it is not
	// available (the material then goes without it).
	Texture func(path string) image.Image
	// Sequences, when non-nil, selects the animations to keep by name.
	Sequences func(name string) bool
}

// The model's root node turns the game's z-up space into glTF's y-up; a
// z-up renderer undoes it by rotating the loaded scene +90° about x.
var zUpToYUp = [4]float32{-math.Sqrt2 / 2, 0, 0, math.Sqrt2 / 2}

type glbWriter struct {
	bin       bytes.Buffer
	views     []map[string]any
	accessors []map[string]any
}

const (
	glFloat         = 5126
	glUnsignedShort = 5123
	glUnsignedInt   = 5125
	glArrayBuffer   = 34962
	glElementBuffer = 34963
)

// view appends data to the binary chunk (4-byte aligned) as a buffer view.
func (w *glbWriter) view(data []byte, target int) int {
	for w.bin.Len()%4 != 0 {
		w.bin.WriteByte(0)
	}
	v := map[string]any{"buffer": 0, "byteOffset": w.bin.Len(), "byteLength": len(data)}
	if target != 0 {
		v["target"] = target
	}
	w.bin.Write(data)
	w.views = append(w.views, v)
	return len(w.views) - 1
}

func (w *glbWriter) accessor(view, componentType, count int, typ string, extra map[string]any) int {
	a := map[string]any{"bufferView": view, "componentType": componentType, "count": count, "type": typ}
	for k, v := range extra {
		a[k] = v
	}
	w.accessors = append(w.accessors, a)
	return len(w.accessors) - 1
}

func f32bytes(fs []float32) []byte {
	b := make([]byte, 4*len(fs))
	for i, f := range fs {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func (w *glbWriter) floats(fs []float32, typ string, target int, minmax bool) int {
	extra := map[string]any{}
	n := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4, "MAT4": 16}[typ]
	if minmax && len(fs) > 0 {
		lo, hi := make([]float32, n), make([]float32, n)
		copy(lo, fs[:n])
		copy(hi, fs[:n])
		for i := n; i < len(fs); i++ {
			lo[i%n] = min(lo[i%n], fs[i])
			hi[i%n] = max(hi[i%n], fs[i])
		}
		extra["min"], extra["max"] = lo, hi
	}
	return w.accessor(w.view(f32bytes(fs), target), glFloat, len(fs)/n, typ, extra)
}

// GLB converts the model to a binary glTF 2.0 file: one skinned mesh with a
// primitive per batch, the skeleton, and one animation per sequence.
// Materials carry the game's blend mode in extras.blendMode; for opaque
// materials the texture's alpha channel is the team-color mask, not opacity.
func (m *Model) GLB(opt GLBOptions) ([]byte, error) {
	if len(m.Vertices) == 0 || len(m.Meshes) == 0 {
		return nil, errors.New("m3: model has no geometry")
	}
	w := &glbWriter{}
	nodes := []map[string]any{{"name": "root", "rotation": zUpToYUp, "children": []int{}}}
	// Bones are nodes 1..n; the mesh node follows them.
	children := make([][]int, len(m.Bones))
	var roots []int
	for i, b := range m.Bones {
		if b.Parent >= 0 {
			children[b.Parent] = append(children[b.Parent], i+1)
		} else {
			roots = append(roots, i+1)
		}
	}
	for i, b := range m.Bones {
		n := map[string]any{"name": b.Name, "translation": b.T, "rotation": normQuat(b.R), "scale": b.S}
		if len(children[i]) > 0 {
			n["children"] = children[i]
		}
		nodes = append(nodes, n)
	}
	meshNode := len(nodes)
	nodes[0]["children"] = append(roots, meshNode)

	// Vertex attributes.
	nv := len(m.Vertices)
	pos := make([]float32, 0, 3*nv)
	nrm := make([]float32, 0, 3*nv)
	uv := make([]float32, 0, 2*nv)
	wts := make([]float32, 0, 4*nv)
	jnt := make([]byte, 0, 8*nv)
	for _, v := range m.Vertices {
		pos = append(pos, v.Pos[:]...)
		n := v.Normal
		if l := float32(math.Sqrt(float64(n[0]*n[0] + n[1]*n[1] + n[2]*n[2]))); l > 1e-6 {
			n = [3]float32{n[0] / l, n[1] / l, n[2] / l}
		} else {
			n = [3]float32{0, 0, 1}
		}
		nrm = append(nrm, n[:]...)
		uv = append(uv, v.UV[:]...)
		ws, js := v.Weights, v.Joints
		sum := ws[0] + ws[1] + ws[2] + ws[3]
		if sum <= 0 {
			ws, js, sum = [4]float32{1, 0, 0, 0}, [4]uint16{}, 1
		}
		for k := 0; k < 4; k++ {
			wts = append(wts, ws[k]/sum)
			jnt = binary.LittleEndian.AppendUint16(jnt, js[k])
		}
	}
	attrs := map[string]int{
		"POSITION":   w.floats(pos, "VEC3", glArrayBuffer, true),
		"NORMAL":     w.floats(nrm, "VEC3", glArrayBuffer, false),
		"TEXCOORD_0": w.floats(uv, "VEC2", glArrayBuffer, false),
		"WEIGHTS_0":  w.floats(wts, "VEC4", glArrayBuffer, false),
		"JOINTS_0":   w.accessor(w.view(jnt, glArrayBuffer), glUnsignedShort, nv, "VEC4", nil),
	}

	// Materials and textures.
	var materials, textures, images []map[string]any
	for _, mt := range m.Materials {
		gm := map[string]any{
			"name":        mt.Name,
			"doubleSided": mt.TwoSided,
			"extras":      map[string]any{"blendMode": mt.BlendMode, "unshaded": mt.Unshaded, "alphaLayer": mt.Alpha != ""},
		}
		pbr := map[string]any{"metallicFactor": 0, "roughnessFactor": 0.8}
		if mt.BlendMode != 0 {
			gm["alphaMode"] = "BLEND"
		}
		addTexture := func(img image.Image) int {
			var buf bytes.Buffer
			if png.Encode(&buf, img) != nil {
				return -1
			}
			images = append(images, map[string]any{"bufferView": w.view(buf.Bytes(), 0), "mimeType": "image/png"})
			textures = append(textures, map[string]any{"source": len(images) - 1, "sampler": 0})
			return len(textures) - 1
		}
		load := func(path string) image.Image {
			if path == "" || opt.Texture == nil {
				return nil
			}
			return opt.Texture(path)
		}
		if diff := load(mt.Diffuse); diff != nil {
			// Blended materials take their opacity from the alpha layer; the
			// diffuse alpha means something else (the team-color mask on
			// opaque ones).
			if mt.BlendMode != 0 {
				if a := load(mt.Alpha); a != nil {
					diff = withAlpha(diff, a, mt.AlphaChannel)
				}
			}
			if i := addTexture(diff); i >= 0 {
				pbr["baseColorTexture"] = map[string]any{"index": i}
			}
		}
		if em := load(mt.Emissive); em != nil {
			if i := addTexture(em); i >= 0 {
				gm["emissiveTexture"] = map[string]any{"index": i}
				gm["emissiveFactor"] = []float32{1, 1, 1}
			}
		}
		gm["pbrMetallicRoughness"] = pbr
		materials = append(materials, gm)
	}

	var prims []map[string]any
	for _, me := range m.Meshes {
		idx := make([]byte, 4*len(me.Indices))
		for i, x := range me.Indices {
			binary.LittleEndian.PutUint32(idx[4*i:], x)
		}
		p := map[string]any{
			"attributes": attrs,
			"indices":    w.accessor(w.view(idx, glElementBuffer), glUnsignedInt, len(me.Indices), "SCALAR", nil),
		}
		if me.Material >= 0 {
			p["material"] = me.Material
		}
		prims = append(prims, p)
	}

	joints := make([]int, len(m.Bones))
	ibm := make([]float32, 0, 16*len(m.Bones))
	for i := range m.Bones {
		joints[i] = i + 1
		ibm = append(ibm, m.BindInverse[i][:]...)
	}
	skin := map[string]any{"joints": joints, "inverseBindMatrices": w.floats(ibm, "MAT4", 0, false), "skeleton": 0}
	nodes = append(nodes, map[string]any{"name": "mesh", "mesh": 0, "skin": 0})

	// Animations.
	paths := map[Channel]string{Translation: "translation", Rotation: "rotation", Scale: "scale"}
	var anims []map[string]any
	for _, sq := range m.Sequences {
		if opt.Sequences != nil && !opt.Sequences(sq.Name) {
			continue
		}
		var samplers, channels []map[string]any
		for _, tr := range sq.Tracks {
			width := 3
			if tr.Channel == Rotation {
				width = 4
			}
			times, vals := cleanKeys(tr.Times, tr.Values, width)
			if len(times) == 0 {
				continue
			}
			if tr.Channel == Rotation {
				for k := 0; k < len(vals); k += 4 {
					q := normQuat([4]float32{vals[k], vals[k+1], vals[k+2], vals[k+3]})
					copy(vals[k:], q[:])
				}
			}
			interp := "LINEAR"
			if tr.Step {
				interp = "STEP"
			}
			typ := "VEC3"
			if width == 4 {
				typ = "VEC4"
			}
			samplers = append(samplers, map[string]any{
				"input":         w.floats(times, "SCALAR", 0, true),
				"output":        w.floats(vals, typ, 0, false),
				"interpolation": interp,
			})
			channels = append(channels, map[string]any{
				"sampler": len(samplers) - 1,
				"target":  map[string]any{"node": tr.Bone + 1, "path": paths[tr.Channel]},
			})
		}
		if len(channels) == 0 {
			continue
		}
		anims = append(anims, map[string]any{
			"name": sq.Name, "samplers": samplers, "channels": channels,
			"extras": map[string]any{"durationMs": sq.EndMs - sq.StartMs, "looping": sq.Looping, "moveSpeed": sq.MoveSpeed},
		})
	}

	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0", "generator": "ghostreel m3"},
		"scene":       0,
		"scenes":      []map[string]any{{"nodes": []int{0}}},
		"nodes":       nodes,
		"meshes":      []map[string]any{{"name": m.Name, "primitives": prims}},
		"skins":       []map[string]any{skin},
		"accessors":   w.accessors,
		"bufferViews": w.views,
	}
	if len(materials) > 0 {
		doc["materials"] = materials
	}
	if len(images) > 0 {
		doc["images"], doc["textures"] = images, textures
		doc["samplers"] = []map[string]any{{"magFilter": 9729, "minFilter": 9987, "wrapS": 10497, "wrapT": 10497}}
	}
	if len(anims) > 0 {
		doc["animations"] = anims
	}
	for w.bin.Len()%4 != 0 {
		w.bin.WriteByte(0)
	}
	doc["buffers"] = []map[string]any{{"byteLength": w.bin.Len()}}
	js, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	var out bytes.Buffer
	total := 12 + 8 + len(js) + 8 + w.bin.Len()
	out.Grow(total)
	put := func(v uint32) { out.Write(binary.LittleEndian.AppendUint32(nil, v)) }
	out.WriteString("glTF")
	put(2)
	put(uint32(total))
	put(uint32(len(js)))
	out.WriteString("JSON")
	out.Write(js)
	put(uint32(w.bin.Len()))
	out.WriteString("BIN\x00")
	out.Write(w.bin.Bytes())
	return out.Bytes(), nil
}

func normQuat(q [4]float32) [4]float32 {
	l := float32(math.Sqrt(float64(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])))
	if l < 1e-6 {
		return [4]float32{0, 0, 0, 1}
	}
	return [4]float32{q[0] / l, q[1] / l, q[2] / l, q[3] / l}
}

// cleanKeys converts times to seconds and drops keys whose time does not
// increase (glTF requires strictly increasing inputs), keeping the later one.
func cleanKeys(ms, vals []float32, width int) ([]float32, []float32) {
	type key struct {
		t float32
		i int
	}
	ks := make([]key, len(ms))
	for i, t := range ms {
		ks[i] = key{max(t, 0) / 1000, i}
	}
	sort.SliceStable(ks, func(a, b int) bool { return ks[a].t < ks[b].t })
	var ts, vs []float32
	for _, k := range ks {
		v := vals[k.i*width : k.i*width+width]
		if n := len(ts); n > 0 && k.t <= ts[n-1] {
			copy(vs[(n-1)*width:], v)
			continue
		}
		ts = append(ts, k.t)
		vs = append(vs, v...)
	}
	return ts, vs
}

// withAlpha returns color's RGB with its alpha taken from one channel of
// another texture, sampled nearest-neighbour when the sizes differ.
func withAlpha(color, alpha image.Image, channel byte) image.Image {
	cb, ab := color.Bounds(), alpha.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, cb.Dx(), cb.Dy()))
	for y := 0; y < cb.Dy(); y++ {
		ay := ab.Min.Y + y*ab.Dy()/cb.Dy()
		for x := 0; x < cb.Dx(); x++ {
			r, g, b, a := color.At(cb.Min.X+x, cb.Min.Y+y).RGBA()
			if a > 0 { // un-premultiply
				r, g, b = r*0xffff/a, g*0xffff/a, b*0xffff/a
			}
			ar, ag, abl, aa := alpha.At(ab.Min.X+x*ab.Dx()/cb.Dx(), ay).RGBA()
			v := map[byte]uint32{'R': ar, 'G': ag, 'B': abl, 'A': aa}[channel]
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(v>>8)
		}
	}
	return out
}
