// SPDX-License-Identifier: Apache-2.0

// Package m3 reads the game's .m3 models: meshes, materials, the skeleton
// and the bone animations. It reads only what a viewer needs (no particles,
// lights, ribbons or physics) and nothing is written back.
//
// The layout follows the community documentation of the format (the
// structures.xml shipped with the m3studio and m3addon Blender add-ons);
// this is an independent implementation, no code is shared with them.
//
// A file is an MD34 header, a table of tagged sections and the sections
// themselves; structures point into sections through 12-byte references
// (count, section index, flags).
package m3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var le = binary.LittleEndian

// Model is a parsed .m3 model.
type Model struct {
	Version   uint32 // MODL structure version
	Name      string // the artist's source file name
	Bones     []Bone
	Vertices  []Vertex
	Meshes    []Mesh
	Materials []Material
	Sequences []Sequence
	// BindInverse[i] is bone i's inverse bind matrix (column-major), the
	// transform from model space into the bone's rest space.
	BindInverse [][16]float32
}

// Bone is one joint of the skeleton with its rest-pose local transform.
type Bone struct {
	Name   string
	Parent int // -1 for roots
	T      [3]float32
	R      [4]float32 // quaternion x, y, z, w
	S      [3]float32
	// Animation ids of the T, R and S channels (matched against tracks) and
	// whether each holds its keys (constant interpolation).
	tid, rid, sid       uint32
	tStep, rStep, sStep bool
}

// Vertex is one skinned vertex in model space (z up).
type Vertex struct {
	Pos     [3]float32
	Normal  [3]float32
	UV      [2]float32
	Joints  [4]uint16 // bone indices
	Weights [4]float32
}

// Mesh is one drawn batch: a run of triangles with one material.
type Mesh struct {
	Indices  []uint32 // into Model.Vertices
	Material int      // into Model.Materials, -1 for none
}

// Material is a standard material reduced to what a simple renderer needs.
type Material struct {
	Name      string
	Diffuse   string // texture path, "" when the color layer has no bitmap
	BlendMode uint32 // 0 opaque, 1 alpha blend, 2 add, 3 alpha add, 4 mod, 5 mod2x
	TwoSided  bool
	Unshaded  bool
}

// Sequence is one named animation ("Stand", "Walk", "Attack", ...).
type Sequence struct {
	Name      string
	StartMs   uint32
	EndMs     uint32
	Looping   bool
	MoveSpeed float32 // ground speed the walk cycle was authored for
	Tracks    []Track
	frequency uint32
}

// Channel says which bone property a track animates.
type Channel uint8

const (
	Translation Channel = iota
	Rotation
	Scale
)

// Track is one animated bone channel within a sequence. Times are in
// milliseconds relative to the sequence start; Values holds 3 floats per key
// for Translation and Scale, 4 (x, y, z, w) for Rotation.
type Track struct {
	Bone    int
	Channel Channel
	Step    bool // constant interpolation
	Times   []float32
	Values  []float32
}

type section struct {
	tag     string
	offset  uint32
	count   uint32
	version uint32
}

type file struct {
	b        []byte
	sections []section
}

type ref struct{ count, index uint32 }

func (f *file) ref(off int) ref {
	return ref{le.Uint32(f.b[off:]), le.Uint32(f.b[off+4:])}
}

// items returns the section a reference points to, checking its tag, and the
// byte offset of its first element. Empty references give ok == false.
func (f *file) items(r ref, tag string) (sec section, ok bool, err error) {
	if r.count == 0 {
		return section{}, false, nil
	}
	if int(r.index) >= len(f.sections) {
		return section{}, false, fmt.Errorf("m3: reference to section %d of %d", r.index, len(f.sections))
	}
	s := f.sections[r.index]
	if tag != "" && s.tag != tag {
		return section{}, false, fmt.Errorf("m3: expected %s section, found %s", tag, s.tag)
	}
	if r.count > s.count {
		return section{}, false, fmt.Errorf("m3: %s reference to %d items, section has %d", tag, r.count, s.count)
	}
	return s, true, nil
}

func (f *file) str(off int) (string, error) {
	r := f.ref(off)
	s, ok, err := f.items(r, "CHAR")
	if !ok || err != nil {
		return "", err
	}
	end := int(s.offset) + int(r.count)
	if end > len(f.b) {
		return "", errors.New("m3: string out of range")
	}
	b := f.b[s.offset:end]
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return string(b), nil
}

// u32s, u16s and f32s decode a reference to an integer or float array.
func (f *file) u32s(off int, tag string) ([]uint32, error) {
	r := f.ref(off)
	s, ok, err := f.items(r, tag)
	if !ok || err != nil {
		return nil, err
	}
	out := make([]uint32, r.count)
	for i := range out {
		out[i] = le.Uint32(f.b[int(s.offset)+4*i:])
	}
	return out, nil
}

func (f *file) u16s(off int, tag string) ([]uint16, error) {
	r := f.ref(off)
	s, ok, err := f.items(r, tag)
	if !ok || err != nil {
		return nil, err
	}
	out := make([]uint16, r.count)
	for i := range out {
		out[i] = le.Uint16(f.b[int(s.offset)+2*i:])
	}
	return out, nil
}

func (f *file) f32(off int) float32 { return math.Float32frombits(le.Uint32(f.b[off:])) }

// element size of each structure version we read; elements are packed.
var sizes = map[string]map[uint32]int{
	"MODL": {20: 748, 21: 760, 23: 784, 24: 796, 25: 808, 26: 820, 28: 844, 29: 856, 30: 868},
	"BONE": {1: 160},
	"DIV_": {2: 52},
	"REGN": {2: 28, 3: 36, 4: 40, 5: 48},
	"BAT_": {1: 14},
	"MATM": {0: 8},
	"MAT_": {15: 268, 16: 280, 17: 280, 18: 280, 19: 340, 20: 352},
	"LAYR": {20: 356, 21: 356, 22: 356, 23: 428, 24: 436, 25: 468, 26: 464},
	"SEQS": {1: 96, 2: 92},
	"STC_": {4: 204},
	"STG_": {0: 24},
	"IREF": {0: 64},
	"SD3V": {0: 32},
	"SD4Q": {0: 32},
}

// elem returns the offset of element i of the section r points to.
func (f *file) elems(r ref, tag string) (offs []int, version uint32, err error) {
	s, ok, err := f.items(r, tag)
	if !ok || err != nil {
		return nil, 0, err
	}
	size, known := sizes[tag][s.version]
	if !known {
		return nil, 0, fmt.Errorf("m3: unsupported %s version %d", tag, s.version)
	}
	if int(s.offset)+size*int(r.count) > len(f.b) {
		return nil, 0, fmt.Errorf("m3: %s section out of range", tag)
	}
	offs = make([]int, r.count)
	for i := range offs {
		offs[i] = int(s.offset) + i*size
	}
	return offs, s.version, nil
}

// modlField gives a MODL field's offset for a version: the fields before
// bone_rests, with the version that introduced each.
func modlField(version uint32, name string) int {
	fields := []struct {
		name  string
		size  int
		since uint32
	}{
		{"model_name", 12, 0}, {"flags", 4, 0}, {"sequences", 12, 0}, {"stc", 12, 0}, {"stg", 12, 0},
		{"bone_anim_sets", 12, 0}, {"split_count", 4, 0}, {"sts", 12, 0}, {"bones", 12, 0},
		{"skin_bone_count", 4, 0}, {"vertex_flags", 4, 0}, {"vertices", 12, 0}, {"divisions", 12, 0},
		{"bone_lookup", 12, 0}, {"boundings", 28, 0}, {"collision_boundings", 28, 0},
		{"collision_faces", 12, 0}, {"collision_verts", 12, 0}, {"collision_face_normals", 12, 0},
		{"attachment_points", 12, 0}, {"attachment_points_addon", 12, 0}, {"lights", 12, 0},
		{"shadow_boxes", 12, 21}, {"cameras", 12, 0}, {"cameras_addon", 12, 0},
		{"material_references", 12, 0}, {"materials_standard", 12, 0}, {"materials_displacement", 12, 0},
		{"materials_composite", 12, 0}, {"materials_terrain", 12, 0}, {"materials_volume", 12, 0},
		{"materials_hair", 12, 0}, {"materials_creep", 12, 0}, {"materials_volumenoise", 12, 25},
		{"materials_splatterrainbake", 12, 26}, {"materials_reflection", 12, 28},
		{"materials_lensflare", 12, 29}, {"materials_buffer", 12, 30}, {"particle_systems", 12, 0},
		{"particle_copies", 12, 0}, {"ribbons", 12, 0}, {"projections", 12, 0}, {"forces", 12, 0},
		{"warps", 12, 0}, {"view_volumes", 12, 0}, {"physics_rigidbodies", 12, 0},
		{"physics_constraints", 12, 0}, {"physics_joints", 12, 0}, {"physics_cloths", 12, 28},
		{"ik_two_joints", 12, 0}, {"ik_ccd", 12, 24}, {"ik_joints", 12, 0}, {"one_bone_solvers", 12, 0},
		{"turret_parts", 12, 0}, {"turrets", 12, 0}, {"bone_rests", 12, 0},
	}
	off := 0
	for _, fd := range fields {
		if version < fd.since {
			continue
		}
		if fd.name == name {
			return off
		}
		off += fd.size
	}
	panic("m3: unknown MODL field " + name)
}

// Parse reads a model from the bytes of a .m3 file.
func Parse(b []byte) (m *Model, err error) {
	defer func() {
		// Every access is bounds-checked against the section table, but a
		// corrupt file can still index past the end inside a structure.
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("m3: malformed file: %v", r)
		}
	}()
	if len(b) < 24 || string(b[:4]) != "43DM" {
		return nil, errors.New("m3: not an MD34 file")
	}
	f := &file{b: b}
	io, n := le.Uint32(b[4:]), le.Uint32(b[8:])
	if int(io)+16*int(n) > len(b) {
		return nil, errors.New("m3: section table out of range")
	}
	for i := 0; i < int(n); i++ {
		o := int(io) + 16*i
		t := []byte{b[o+3], b[o+2], b[o+1], b[o]} // tags are stored reversed
		f.sections = append(f.sections, section{string(t), le.Uint32(b[o+4:]), le.Uint32(b[o+8:]), le.Uint32(b[o+12:])})
	}
	mods, version, err := f.elems(f.ref(12), "MODL")
	if err != nil {
		return nil, err
	}
	if len(mods) != 1 {
		return nil, errors.New("m3: no model")
	}
	mo := mods[0]
	field := func(name string) int { return mo + modlField(version, name) }
	m = &Model{Version: version}
	if m.Name, err = f.str(field("model_name")); err != nil {
		return nil, err
	}
	if err := f.bones(m, field("bones"), field("bone_rests")); err != nil {
		return nil, err
	}
	if err := f.meshes(m, field); err != nil {
		return nil, err
	}
	if err := f.materials(m, field("material_references"), field("materials_standard")); err != nil {
		return nil, err
	}
	if err := f.sequences(m, field("sequences"), field("stc"), field("stg")); err != nil {
		return nil, err
	}
	return m, nil
}

func (f *file) bones(m *Model, bonesOff, restsOff int) error {
	offs, _, err := f.elems(f.ref(bonesOff), "BONE")
	if err != nil {
		return err
	}
	for _, o := range offs {
		name, err := f.str(o + 4)
		if err != nil {
			return err
		}
		bn := Bone{Name: name, Parent: int(int16(le.Uint16(f.b[o+20:])))}
		// location, rotation, scale: an 8-byte animation header (interpolation,
		// flags, id) then the default value.
		bn.tid = le.Uint32(f.b[o+24+4:])
		bn.rid = le.Uint32(f.b[o+60+4:])
		bn.sid = le.Uint32(f.b[o+104+4:])
		bn.tStep = le.Uint16(f.b[o+24:]) == 0
		bn.rStep = le.Uint16(f.b[o+60:]) == 0
		bn.sStep = le.Uint16(f.b[o+104:]) == 0
		for i := 0; i < 3; i++ {
			bn.T[i] = f.f32(o + 32 + 4*i)
			bn.S[i] = f.f32(o + 112 + 4*i)
		}
		for i := 0; i < 4; i++ {
			bn.R[i] = f.f32(o + 68 + 4*i)
		}
		if bn.Parent >= len(offs) {
			return fmt.Errorf("m3: bone %q has parent %d of %d", name, bn.Parent, len(offs))
		}
		m.Bones = append(m.Bones, bn)
	}
	rests, _, err := f.elems(f.ref(restsOff), "IREF")
	if err != nil {
		return err
	}
	for _, o := range rests {
		var mat [16]float32
		for i := range mat {
			mat[i] = f.f32(o + 4*i)
		}
		m.BindInverse = append(m.BindInverse, mat)
	}
	if len(m.BindInverse) != len(m.Bones) && len(m.BindInverse) > 0 {
		return fmt.Errorf("m3: %d bind matrices for %d bones", len(m.BindInverse), len(m.Bones))
	}
	return nil
}

// MergeAnimations adds the sequences of an animation-only file (.m3a) that
// the model lacks, matching bones by name.
func (m *Model) MergeAnimations(a *Model) {
	byName := map[string]int{}
	for i, b := range m.Bones {
		byName[b.Name] = i
	}
	have := map[string]bool{}
	for _, sq := range m.Sequences {
		have[sq.Name] = true
	}
	for _, sq := range a.Sequences {
		if have[sq.Name] {
			continue
		}
		var tracks []Track
		for _, tr := range sq.Tracks {
			if bi, ok := byName[a.Bones[tr.Bone].Name]; ok {
				tr.Bone = bi
				tracks = append(tracks, tr)
			}
		}
		if len(tracks) > 0 {
			sq.Tracks = tracks
			m.Sequences = append(m.Sequences, sq)
			have[sq.Name] = true
		}
	}
}

// vertexLayout returns the size of one vertex and the byte offsets of the
// parts we read (-1 when absent) for a model's vertex_flags.
type vertexLayout struct {
	size, weights, lookups, influences int
	normal, normalF, uv, uvF           int
}

func layoutFor(flags uint32) vertexLayout {
	l := vertexLayout{weights: -1, lookups: -1, normal: -1, normalF: -1, uv: -1, uvF: -1}
	add := func(n int) int { o := l.size; l.size += n; return o }
	if flags&0x1 != 0 {
		add(12) // position
	}
	if flags&0x20 != 0 {
		l.influences += 2
	}
	if flags&0x40 != 0 {
		l.influences += 2
	}
	if l.influences > 0 {
		l.weights = add(l.influences)
		l.lookups = add(l.influences)
	}
	if flags&0x80 != 0 {
		l.normalF = add(12)
	}
	if flags&0x800000 != 0 {
		l.normal = add(4) // 3 bytes + sign
	}
	for _, bit := range []uint32{0x100, 0x200, 0x400, 0x800, 0x1000} {
		if flags&bit != 0 {
			add(4) // vertex color and unknowns
		}
	}
	for i, bit := range []uint32{0x2000, 0x4000, 0x8000, 0x10000} {
		if flags&bit != 0 {
			o := add(8)
			if i == 0 {
				l.uvF = o
			}
		}
	}
	uvs := 0
	for _, bit := range []uint32{0x20000, 0x40000, 0x80000, 0x100000, 0x40000000} {
		if flags&bit != 0 {
			uvs++
		}
	}
	if uvs > 0 {
		l.uv = add(4 * uvs)
	}
	if flags&0x200000 != 0 {
		o := add(12)
		if l.normalF < 0 && l.normal < 0 {
			l.normalF = o
		}
	}
	for _, s := range []struct {
		bit uint32
		n   int
	}{{0x400000, 12}, {0x1000000, 4}, {0x2000000, 4}, {0x4000000, 12}, {0x8000000, 12}, {0x10000000, 4}, {0x20000000, 4}} {
		if flags&s.bit != 0 {
			add(s.n)
		}
	}
	return l
}

func (f *file) meshes(m *Model, field func(string) int) error {
	vflags := le.Uint32(f.b[field("vertex_flags"):])
	if vflags&1 == 0 {
		return nil // no geometry
	}
	l := layoutFor(vflags)
	vr := f.ref(field("vertices"))
	vs, ok, err := f.items(vr, "U8__")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if int(vr.count)%l.size != 0 {
		return fmt.Errorf("m3: %d vertex bytes is not a multiple of %d (flags %#x)", vr.count, l.size, vflags)
	}
	lookup, err := f.u16s(field("bone_lookup"), "U16_")
	if err != nil {
		return err
	}
	nv := int(vr.count) / l.size
	raw := make([]Vertex, nv)
	luts := make([][4]uint8, nv)
	for i := range raw {
		o := int(vs.offset) + i*l.size
		v := &raw[i]
		for k := 0; k < 3; k++ {
			v.Pos[k] = f.f32(o + 4*k)
		}
		for k := 0; k < l.influences && k < 4; k++ {
			v.Weights[k] = float32(f.b[o+l.weights+k]) / 255
			luts[i][k] = f.b[o+l.lookups+k]
		}
		switch {
		case l.normal >= 0:
			for k := 0; k < 3; k++ {
				v.Normal[k] = float32(f.b[o+l.normal+k])/255*2 - 1
			}
		case l.normalF >= 0:
			for k := 0; k < 3; k++ {
				v.Normal[k] = f.f32(o + l.normalF + 4*k)
			}
		}
		switch {
		case l.uv >= 0:
			v.UV[0] = float32(int16(le.Uint16(f.b[o+l.uv:]))) / 2048
			v.UV[1] = float32(int16(le.Uint16(f.b[o+l.uv+2:]))) / 2048
		case l.uvF >= 0:
			v.UV[0], v.UV[1] = f.f32(o+l.uvF), f.f32(o+l.uvF+4)
		}
	}
	divs, _, err := f.elems(f.ref(field("divisions")), "DIV_")
	if err != nil {
		return err
	}
	used := make([]bool, nv)
	for _, d := range divs {
		faces, err := f.u16s(d, "U16_")
		if err != nil {
			return err
		}
		regs, rver, err := f.elems(f.ref(d+12), "REGN")
		if err != nil {
			return err
		}
		type region struct {
			v0, nv, f0, nf int
			lut0, nlut     int
			uvMul, uvOff   float32
			hidden         bool
		}
		rs := make([]region, len(regs))
		for i, o := range regs {
			r := &rs[i]
			p := o + 4
			if rver >= 3 {
				p += 4
				r.v0, r.nv = int(le.Uint32(f.b[p:])), int(le.Uint32(f.b[p+4:]))
				p += 8
			} else {
				r.v0, r.nv = int(le.Uint16(f.b[p:])), int(le.Uint16(f.b[p+2:]))
				p += 4
			}
			r.f0, r.nf = int(le.Uint32(f.b[p:])), int(le.Uint32(f.b[p+4:]))
			p += 8
			r.lut0, r.nlut = int(le.Uint16(f.b[p+2:])), int(le.Uint16(f.b[p+4:]))
			p += 8 + 4 // bone_count, lut0, nlut, unknown; lookups used, unknown, root bone
			r.uvMul = 16
			if rver >= 4 {
				r.hidden = le.Uint32(f.b[p:])&1 != 0
				p += 4
			}
			if rver >= 5 {
				r.uvMul, r.uvOff = f.f32(p), f.f32(p+4)
			}
			if r.v0+r.nv > nv || r.f0+r.nf > len(faces) || r.lut0+r.nlut > len(lookup) {
				return fmt.Errorf("m3: region %d out of range", i)
			}
		}
		bats, _, err := f.elems(f.ref(d+24), "BAT_")
		if err != nil {
			return err
		}
		for _, o := range bats {
			ri := int(le.Uint16(f.b[o+4:]))
			mat := int(le.Uint16(f.b[o+10:]))
			if ri >= len(rs) || rs[ri].hidden {
				continue
			}
			r := rs[ri]
			idx := make([]uint32, r.nf)
			for k := range idx {
				vi := r.v0 + int(faces[r.f0+k])
				if vi >= r.v0+r.nv {
					return fmt.Errorf("m3: face index out of region %d", ri)
				}
				idx[k] = uint32(vi)
				if !used[vi] {
					used[vi] = true
					v := &raw[vi]
					if l.uv >= 0 && (r.uvMul != 16 || r.uvOff != 0) {
						// Regions since v5 carry their own UV scale.
						u0 := float32(int16(le.Uint16(f.b[int(vs.offset)+vi*l.size+l.uv:])))
						u1 := float32(int16(le.Uint16(f.b[int(vs.offset)+vi*l.size+l.uv+2:])))
						v.UV[0] = u0*r.uvMul/32768 + r.uvOff
						v.UV[1] = u1*r.uvMul/32768 + r.uvOff
					}
					for k := 0; k < 4; k++ {
						if v.Weights[k] == 0 {
							continue
						}
						li := int(luts[vi][k])
						if li >= r.nlut {
							return fmt.Errorf("m3: bone lookup %d out of region %d", li, ri)
						}
						v.Joints[k] = lookup[r.lut0+li]
					}
				}
			}
			m.Meshes = append(m.Meshes, Mesh{Indices: idx, Material: mat})
		}
	}
	m.Vertices = raw
	return nil
}

func (f *file) materials(m *Model, refsOff, stdOff int) error {
	mrefs, _, err := f.elems(f.ref(refsOff), "MATM")
	if err != nil {
		return err
	}
	std, mver, err := f.elems(f.ref(stdOff), "MAT_")
	if err != nil {
		return err
	}
	// Map material references to our list; non-standard materials (creep,
	// displacement, ...) become -1 and their batches are dropped.
	remap := make([]int, len(mrefs))
	for i, o := range mrefs {
		typ, idx := le.Uint32(f.b[o:]), int(le.Uint32(f.b[o+4:]))
		remap[i] = -1
		if typ != 1 || idx >= len(std) {
			continue
		}
		o := std[idx]
		name, err := f.str(o)
		if err != nil {
			return err
		}
		flags := le.Uint32(f.b[o+16:])
		mt := Material{Name: name, BlendMode: le.Uint32(f.b[o+20:]), TwoSided: flags&0x8 != 0, Unshaded: flags&0x10 != 0}
		diff := o + 52
		if mver >= 20 {
			diff += 12
		}
		if lays, _, err := f.elems(f.ref(diff), "LAYR"); err != nil {
			return err
		} else if len(lays) > 0 {
			if mt.Diffuse, err = f.str(lays[0] + 4); err != nil {
				return err
			}
		}
		remap[i] = len(m.Materials)
		m.Materials = append(m.Materials, mt)
	}
	kept := m.Meshes[:0]
	for _, me := range m.Meshes {
		if me.Material < len(remap) && remap[me.Material] >= 0 {
			me.Material = remap[me.Material]
			kept = append(kept, me)
		}
	}
	m.Meshes = kept
	return nil
}

func (f *file) sequences(m *Model, seqOff, stcOff, stgOff int) error {
	seqs, _, err := f.elems(f.ref(seqOff), "SEQS")
	if err != nil {
		return err
	}
	stcs, _, err := f.elems(f.ref(stcOff), "STC_")
	if err != nil {
		return err
	}
	stgs, _, err := f.elems(f.ref(stgOff), "STG_")
	if err != nil {
		return err
	}
	if len(stgs) != len(seqs) {
		return fmt.Errorf("m3: %d sequences but %d transformation groups", len(seqs), len(stgs))
	}
	// Which bone channel each animation id drives.
	targets := map[uint32]target{}
	for i, b := range m.Bones {
		targets[b.tid] = target{i, Translation, b.tStep}
		targets[b.rid] = target{i, Rotation, b.rStep}
		targets[b.sid] = target{i, Scale, b.sStep}
	}
	for si, o := range seqs {
		name, err := f.str(o + 8)
		if err != nil {
			return err
		}
		sq := Sequence{
			Name: name, StartMs: le.Uint32(f.b[o+20:]), EndMs: le.Uint32(f.b[o+24:]),
			MoveSpeed: f.f32(o + 28), Looping: le.Uint32(f.b[o+32:])&1 == 0, frequency: le.Uint32(f.b[o+36:]),
		}
		idxs, err := f.u32s(stgs[si]+12, "U32_")
		if err != nil {
			return err
		}
		seen := map[uint32]bool{}
		// A group's collections are listed most important first; the first one
		// that animates a channel wins.
		for _, ci := range idxs {
			if int(ci) >= len(stcs) {
				return fmt.Errorf("m3: sequence %q uses collection %d of %d", name, ci, len(stcs))
			}
			if err := f.collection(&sq, stcs[ci], func(id uint32) (target, bool) {
				t, ok := targets[id]
				if !ok || seen[id] {
					return t, false
				}
				seen[id] = true
				return t, true
			}); err != nil {
				return err
			}
		}
		m.Sequences = append(m.Sequences, sq)
	}
	return nil
}

// target is the bone channel an animation id drives.
type target struct {
	bone int
	ch   Channel
	step bool
}

func (f *file) collection(sq *Sequence, o int, want func(uint32) (target, bool)) error {
	ids, err := f.u32s(o+20, "U32_")
	if err != nil {
		return err
	}
	refs, err := f.u32s(o+32, "U32_")
	if err != nil {
		return err
	}
	if len(ids) != len(refs) {
		return errors.New("m3: animation ids and refs differ in length")
	}
	sd3v, _, err := f.elems(f.ref(o+72), "SD3V")
	if err != nil {
		return err
	}
	sd4q, _, err := f.elems(f.ref(o+84), "SD4Q")
	if err != nil {
		return err
	}
	for i, id := range ids {
		kind, idx := refs[i]>>16, int(refs[i]&0xffff)
		var list []int
		var width int
		switch kind {
		case 2:
			list, width = sd3v, 3
		case 3:
			list, width = sd4q, 4
		default:
			continue // events, colors, floats, flags: not bone transforms
		}
		t, ok := want(id)
		if !ok || idx >= len(list) {
			continue
		}
		if (width == 4) != (t.ch == Rotation) {
			continue
		}
		so := list[idx]
		frames, err := f.u32s(so, "I32_")
		if err != nil {
			return err
		}
		kr := f.ref(so + 20)
		ks, ok, err := f.items(kr, "")
		if err != nil {
			return err
		}
		if !ok || int(kr.count) != len(frames) {
			continue
		}
		tr := Track{Bone: t.bone, Channel: t.ch, Step: t.step}
		for k, ms := range frames {
			tr.Times = append(tr.Times, float32(int32(ms)-int32(sq.StartMs)))
			for c := 0; c < width; c++ {
				tr.Values = append(tr.Values, f.f32(int(ks.offset)+(k*width+c)*4))
			}
		}
		sq.Tracks = append(sq.Tracks, tr)
	}
	return nil
}
