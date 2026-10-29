// SPDX-License-Identifier: Apache-2.0 AND MIT

// MNDX root file parser.
//
// This file is a Go port of CascRootFile_Mndx.cpp from CascLib, Copyright (c)
// Ladislav Zezula 2014, MIT license (https://github.com/ladislav-zezula/CascLib;
// the license text is in third_party/casclib/LICENSE).
// The type and field names below follow CascLib's so the two can be read side
// by side; only the parts needed to enumerate every name (CascLib's
// TMndxHandler::Load, LoadFileNames and the empty-mask TFileNameDatabase
// search) are ported, not the name lookup by search mask or the debug dumps.
//
// An MNDX root holds three "MAR" file-name databases (package names, file
// names with the package stripped, full names) and a table of CKey entries.
// Each MAR database is a LOUDS-style succinct trie: the CollisionTable bit
// array encodes the tree shape (each node's children are a run of 1 bits
// ending in a 0), FileNameIndexes marks the nodes that end a file name, and
// each node's label is a single character or a path fragment, found either
// through a hash table or through the LoBits/HiBits tables. Fragments may
// themselves be stored in a nested ("child") database whose labels are read
// walking up from a node to the root.

package casc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// RootEntry is one file of the root: its full name and content key.
type RootEntry struct {
	Name string
	CKey Key
	Size uint32 // content (decoded) size in bytes
}

const (
	mndxSignature = 0x58444E4D // 'MNDX'
	marSignature  = 0x0052414D // 'MAR\0'

	marPackageNames  = 0 // MAR with package names only
	marStrippedNames = 1 // MAR with names where packages were stripped
	marFullNames     = 2 // MAR with full file names
	marCount         = 3

	mndxLastCKeyEntry = 0x80000000 // flag ending a group of same-named entries
	invalidIndex      = 0xFFFFFFFF

	marInfoSize   = 20 // FILE_MAR_INFO
	ckeyEntrySize = 24 // MNDX_CKEY_ENTRY: flags, CKey, content size
)

var errMndxCorrupt = errors.New("MNDX: corrupt root file")

// ParseMNDX parses an MNDX root file into all of its entries. Each name is
// the package name, a slash, and the package-stripped file name, as CascLib
// builds it (TMndxHandler::MakeFileName). Entries come in the trie's
// enumeration order; a name present in several packages yields one entry
// per package.
func ParseMNDX(root []byte) (entries []RootEntry, err error) {
	// The structures are walked by index; a corrupt file could index past an
	// array, which in Go panics rather than reading garbage. Report that as a
	// format error.
	defer func() {
		if r := recover(); r != nil {
			entries, err = nil, fmt.Errorf("%w (%v)", errMndxCorrupt, r)
		}
	}()

	le := binary.LittleEndian
	// FILE_MNDX_HEADER
	if len(root) < 12 || le.Uint32(root) != mndxSignature {
		return nil, errors.New("not an MNDX root file")
	}
	headerVersion := le.Uint32(root[4:])
	formatVersion := le.Uint32(root[8:])
	if formatVersion < 1 || formatVersion > 2 {
		return nil, fmt.Errorf("MNDX: unsupported format version %d", formatVersion)
	}
	pos := 12
	if headerVersion == 2 {
		pos += 8 // field_1C, field_20
	}
	if len(root) < pos+0x1C {
		return nil, errMndxCorrupt
	}
	h := root[pos:]
	var (
		marInfoOffset = int(le.Uint32(h[0:]))
		marInfoCount  = int(le.Uint32(h[4:]))
		marInfoSz     = int(le.Uint32(h[8:]))
		ckeyOffset    = int(le.Uint32(h[12:]))
		ckeyCount     = int(le.Uint32(h[16:]))
		fileNameCount = int(le.Uint32(h[20:]))
		ckeyEntrySz   = int(le.Uint32(h[24:]))
		marFiles      [marCount]*fileNameDatabase
		capture       = func(off, n int) []byte {
			if off < 0 || n < 0 || off+n > len(root) {
				return nil
			}
			return root[off : off+n]
		}
	)
	if marInfoCount > marCount || marInfoSz != marInfoSize {
		return nil, errMndxCorrupt
	}
	for i := 0; i < marInfoCount; i++ {
		info := capture(marInfoOffset+marInfoSz*i, marInfoSize)
		if info == nil {
			return nil, errMndxCorrupt
		}
		// MarIndex, MarDataSize, MarDataSizeHi, MarDataOffset, MarDataOffsetHi
		data := capture(int(le.Uint32(info[12:])), int(le.Uint32(info[4:])))
		if data == nil {
			return nil, errMndxCorrupt
		}
		db, err := loadMAR(data)
		if err != nil {
			return nil, fmt.Errorf("MNDX: MAR %d: %w", i, err)
		}
		marFiles[i] = db
	}
	if marFiles[marPackageNames] == nil || marFiles[marStrippedNames] == nil || marFiles[marFullNames] == nil {
		return nil, errors.New("MNDX: missing MAR databases")
	}
	if ckeyEntrySz != ckeyEntrySize {
		return nil, errMndxCorrupt
	}

	// The CKey entries: all files, those of one name grouped together, the
	// last of each group flagged.
	stripped := marFiles[marStrippedNames]
	if int(stripped.FileNameIndexes.ValidItemCount) != fileNameCount {
		return nil, errMndxCorrupt
	}
	ckeys := capture(ckeyOffset, ckeyCount*ckeyEntrySz)
	if ckeys == nil {
		return nil, errMndxCorrupt
	}
	flagsAt := func(i int) uint32 { return le.Uint32(ckeys[i*ckeyEntrySize:]) }

	// FileNameIndexToCKeyIndex: the first entry of each name's group.
	groupStart := make([]int, 1, fileNameCount+1)
	for i := 0; i < ckeyCount; i++ {
		if len(groupStart) > fileNameCount {
			break
		}
		if flagsAt(i)&mndxLastCKeyEntry != 0 {
			groupStart = append(groupStart, i+1)
		}
	}
	if len(groupStart)-1 != fileNameCount {
		return nil, errMndxCorrupt
	}

	// LoadPackageNames: enumerate the package MAR; a name's index is its
	// package index. Gaps stay empty, like CascLib's zero-filled array.
	var packages []string
	pkgSearch := newMndxSearch(marFiles[marPackageNames])
	for {
		name, idx, ok := pkgSearch.next()
		if !ok {
			break
		}
		for len(packages) <= int(idx) {
			packages = append(packages, "")
		}
		packages[idx] = string(name)
	}

	// LoadFileNames: enumerate the stripped names, and for each, every CKey
	// entry of its group, prefixed with the entry's package.
	entries = make([]RootEntry, 0, ckeyCount)
	search := newMndxSearch(stripped)
	for {
		name, idx, ok := search.next()
		if !ok {
			break
		}
		if int(idx) >= fileNameCount {
			continue
		}
		for e := groupStart[idx]; e < ckeyCount; e++ {
			rec := ckeys[e*ckeyEntrySize : (e+1)*ckeyEntrySize]
			flags := le.Uint32(rec)
			if pkg := int(flags & 0x00FFFFFF); pkg < len(packages) {
				var ent RootEntry
				ent.Name = packages[pkg] + "/" + string(name)
				copy(ent.CKey[:], rec[4:20])
				ent.Size = le.Uint32(rec[20:])
				entries = append(entries, ent)
			}
			if flags&mndxLastCKeyEntry != 0 {
				break
			}
		}
	}
	return entries, nil
}

//-----------------------------------------------------------------------------
// Byte stream (TByteStream)

type byteStream struct {
	b   []byte
	err error
}

func (s *byteStream) take(n int) []byte {
	if s.err != nil {
		return nil
	}
	if n < 0 || n > len(s.b) {
		s.err = errMndxCorrupt
		return nil
	}
	out := s.b[:n:n]
	s.b = s.b[n:]
	return out
}

func (s *byteStream) u32() uint32 {
	if b := s.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (s *byteStream) u64() uint64 {
	if b := s.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}

// array reads a TGenericArray: a 64-bit byte count, the items, and padding
// to a multiple of 8 bytes.
func (s *byteStream) array(itemSize int) []byte {
	n := s.u64()
	if s.err != nil {
		return nil
	}
	if n > 0xFFFFFFFF || n%uint64(itemSize) != 0 {
		s.err = errMndxCorrupt
		return nil
	}
	data := s.take(int(n))
	s.take(int(-uint32(n) & 7))
	return data
}

func (s *byteStream) dwords() []uint32 {
	b := s.array(4)
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return out
}

//-----------------------------------------------------------------------------
// Bit counting

// setBits is CascLib's SETBITS: the number of set bits in the lower 1, 2, 3
// and 4 bytes of a 32-bit value.
type setBits struct{ lower08, lower16, lower24, lower32 uint32 }

func numberOfSetBits(v uint32) setBits {
	b0 := uint32(bits.OnesCount8(uint8(v)))
	b1 := b0 + uint32(bits.OnesCount8(uint8(v>>8)))
	b2 := b1 + uint32(bits.OnesCount8(uint8(v>>16)))
	return setBits{b0, b1, b2, b2 + uint32(bits.OnesCount8(uint8(v>>24)))}
}

// selectTable is CascLib's table_1BA1818: entry [k<<8 | b] is the bit
// position of the k-th (0-based) set bit of byte b, or 7 when b has no more
// than k set bits. It is computed rather than copied; the values are the same.
var selectTable = func() (t [0x800]byte) {
	for k := 0; k < 8; k++ {
		for b := 0; b < 256; b++ {
			t[k<<8|b] = 7
			n := 0
			for bit := 0; bit < 8; bit++ {
				if b&(1<<bit) != 0 {
					if n == k {
						t[k<<8|b] = byte(bit)
						break
					}
					n++
				}
			}
		}
	}
	return
}()

//-----------------------------------------------------------------------------
// TBitEntryArray: an array of fixed-width bit fields

type bitEntryArray struct {
	items        []uint32
	bitsPerEntry uint32
	entryBitMask uint32
	totalEntries uint32
}

func (a *bitEntryArray) load(s *byteStream) {
	a.items = s.dwords()
	a.bitsPerEntry = s.u32()
	if a.bitsPerEntry > 0x20 && s.err == nil {
		s.err = errMndxCorrupt
	}
	a.entryBitMask = s.u32()
	total := s.u64()
	if total > 0xFFFFFFFF && s.err == nil {
		s.err = errMndxCorrupt
	}
	a.totalEntries = uint32(total)
}

func (a *bitEntryArray) item(i uint32) uint32 {
	itemIndex := (i * a.bitsPerEntry) >> 5
	startBit := (i * a.bitsPerEntry) & 0x1F
	var v uint32
	if startBit+a.bitsPerEntry > 0x20 {
		v = a.items[itemIndex+1]<<(0x20-startBit) | a.items[itemIndex]>>startBit
	} else {
		v = a.items[itemIndex] >> startBit
	}
	return v & a.entryBitMask
}

//-----------------------------------------------------------------------------
// TSparseArray: a bit array with rank (GetItemValueAt) and select (GetItem0
// for zero bits, GetItem1 for one bits) support.

// baseVals is CascLib's BASEVALS for one group of 0x200 bits: the number of
// set bits before the group, and add[k] the extra set bits before the k-th
// 0x40-bit block of the group (add[0] is always 0).
type baseVals struct {
	base200 uint32
	add     [8]uint32
}

type sparseArray struct {
	ItemBits       []uint32
	TotalItemCount uint32
	ValidItemCount uint32
	BaseVals       []baseVals
	IndexToItem0   []uint32 // position of every 0x200-th zero bit
	IndexToItem1   []uint32 // position of every 0x200-th one bit
}

func (a *sparseArray) load(s *byteStream) {
	a.ItemBits = s.dwords()
	a.TotalItemCount = s.u32()
	a.ValidItemCount = s.u32()
	if a.ValidItemCount > a.TotalItemCount && s.err == nil {
		s.err = errMndxCorrupt
	}
	raw := s.array(12)
	a.BaseVals = make([]baseVals, len(raw)/12)
	for i := range a.BaseVals {
		r := raw[i*12:]
		w1 := binary.LittleEndian.Uint32(r[4:])
		w2 := binary.LittleEndian.Uint32(r[8:])
		a.BaseVals[i] = baseVals{
			base200: binary.LittleEndian.Uint32(r),
			add: [8]uint32{
				0,
				w1 & 0x7F,          // AddValue40  : 7
				(w1 >> 7) & 0xFF,   // AddValue80  : 8
				(w1 >> 15) & 0xFF,  // AddValueC0  : 8
				(w1 >> 23) & 0x1FF, // AddValue100 : 9
				w2 & 0x1FF,         // AddValue140 : 9
				(w2 >> 9) & 0x1FF,  // AddValue180 : 9
				(w2 >> 18) & 0x1FF, // AddValue1C0 : 9
			},
		}
	}
	a.IndexToItem0 = s.dwords()
	a.IndexToItem1 = s.dwords()
}

func (a *sparseArray) isEmpty() bool { return a.TotalItemCount == 0 }

// isItemPresent reports whether bit i is set. Bits past the array read as
// clear.
func (a *sparseArray) isItemPresent(i uint32) bool {
	w := i >> 5
	return int(w) < len(a.ItemBits) && a.ItemBits[w]&(1<<(i&0x1F)) != 0
}

// itemValueAt is the number of set bits before bit i (rank1).
func (a *sparseArray) itemValueAt(i uint32) uint32 {
	bv := &a.BaseVals[i>>9]
	v := bv.base200 + bv.add[(i>>6)&7]
	if i&0x20 != 0 {
		v += uint32(bits.OnesCount32(a.ItemBits[(i>>5)-1]))
	}
	return v + uint32(bits.OnesCount32(a.ItemBits[i>>5]&(1<<(i&0x1F)-1)))
}

// findGroupItems0 finds the 0x200-bit group holding the i-th zero bit:
// the last group g with g*0x200 - BaseVals[g].base200 <= i.
//
// CascLib's binary-search branch compares against maxGroup instead of the
// midpoint; this is the plain binary search the code intends.
func (a *sparseArray) findGroupItems0(i uint32) uint32 {
	zerosBefore := func(g uint32) uint32 { return g<<9 - a.BaseVals[g].base200 }
	minGroup := a.IndexToItem0[i>>9] >> 9
	maxGroup := (a.IndexToItem0[(i>>9)+1] + 0x1FF) >> 9
	if maxGroup-minGroup < 10 {
		for i >= zerosBefore(minGroup+1) {
			minGroup++
		}
	} else {
		for minGroup+1 < maxGroup {
			mid := (minGroup + maxGroup) >> 1
			if i < zerosBefore(mid) {
				maxGroup = mid
			} else {
				minGroup = mid
			}
		}
	}
	return minGroup
}

// findGroupItems1 finds the 0x200-bit group holding the i-th one bit: the
// last group g with BaseVals[g].base200 <= i.
//
// CascLib's binary-search branch makes a single step (an "if" where a
// "while" is meant); this runs the search to the end.
func (a *sparseArray) findGroupItems1(i uint32) uint32 {
	start := a.IndexToItem1[i>>9] >> 9
	next := (a.IndexToItem1[(i>>9)+1] + 0x1FF) >> 9
	if next-start < 10 {
		for i >= a.BaseVals[start+1].base200 {
			start++
		}
	} else {
		for start+1 < next {
			mid := (start + next) >> 1
			if i < a.BaseVals[mid].base200 {
				next = mid
			} else {
				start = mid
			}
		}
	}
	return start
}

// item0 is the position of the i-th (0-based) zero bit (select0).
func (a *sparseArray) item0(i uint32) uint32 {
	if i&0x1FF == 0 {
		return a.IndexToItem0[i>>9]
	}
	g := a.findGroupItems0(i)
	bv := &a.BaseVals[g]
	// The rank of the wanted zero within the group, then narrowed down to a
	// 64-bit block: the zeros before block k are k*0x40 - add[k].
	edx := i + bv.base200 - g<<9
	dw := g << 4
	if edx < 0x100-bv.add[4] {
		if edx < 0x80-bv.add[2] {
			if edx >= 0x40-bv.add[1] {
				dw += 2
				edx = edx + bv.add[1] - 0x40
			}
		} else {
			if edx < 0xC0-bv.add[3] {
				dw += 4
				edx = edx + bv.add[2] - 0x80
			} else {
				dw += 6
				edx = edx + bv.add[3] - 0xC0
			}
		}
	} else {
		if edx < 0x180-bv.add[6] {
			if edx < 0x140-bv.add[5] {
				dw += 8
				edx = edx + bv.add[4] - 0x100
			} else {
				dw += 10
				edx = edx + bv.add[5] - 0x140
			}
		} else {
			if edx < 0x1C0-bv.add[7] {
				dw += 12
				edx = edx + bv.add[6] - 0x180
			} else {
				dw += 14
				edx = edx + bv.add[7] - 0x1C0
			}
		}
	}
	bitGroup := ^a.ItemBits[dw]
	zeroBits := numberOfSetBits(bitGroup)
	if edx >= zeroBits.lower32 {
		dw++
		bitGroup = ^a.ItemBits[dw]
		edx -= zeroBits.lower32
		zeroBits = numberOfSetBits(bitGroup)
	}
	return selectInWord(bitGroup, edx, zeroBits, dw<<5)
}

// item1 is the position of the i-th (0-based) one bit (select1).
func (a *sparseArray) item1(i uint32) uint32 {
	if i&0x1FF == 0 {
		return a.IndexToItem1[i>>9]
	}
	g := a.findGroupItems1(i)
	bv := &a.BaseVals[g]
	dist := i - bv.base200
	dw := g << 4
	if dist < bv.add[4] {
		if dist < bv.add[2] {
			if dist >= bv.add[1] {
				dist -= bv.add[1]
				dw += 2
			}
		} else {
			if dist < bv.add[3] {
				dist -= bv.add[2]
				dw += 4
			} else {
				dist -= bv.add[3]
				dw += 6
			}
		}
	} else {
		if dist < bv.add[6] {
			if dist < bv.add[5] {
				dist -= bv.add[4]
				dw += 8
			} else {
				dist -= bv.add[5]
				dw += 10
			}
		} else {
			if dist < bv.add[7] {
				dist -= bv.add[6]
				dw += 12
			} else {
				dist -= bv.add[7]
				dw += 14
			}
		}
	}
	bitGroup := a.ItemBits[dw]
	set := numberOfSetBits(bitGroup)
	if dist >= set.lower32 {
		dw++
		bitGroup = a.ItemBits[dw]
		dist -= set.lower32
		set = numberOfSetBits(bitGroup)
	}
	return selectInWord(bitGroup, dist, set, dw<<5)
}

// selectInWord finishes item0/item1: the position of the n-th set bit of
// word w (whose byte-wise counts are c), plus base.
func selectInWord(w, n uint32, c setBits, base uint32) uint32 {
	if n < c.lower16 {
		if n >= c.lower08 {
			w >>= 8
			base += 8
			n -= c.lower08
		}
	} else {
		if n < c.lower24 {
			w >>= 16
			base += 16
			n -= c.lower16
		} else {
			w >>= 24
			base += 24
			n -= c.lower24
		}
	}
	return uint32(selectTable[(w&0xFF)+n<<8]) + base
}

//-----------------------------------------------------------------------------
// TPathFragmentTable

type pathFragmentTable struct {
	PathFragments []byte
	PathMarks     sparseArray // fragment ends, if not zero-terminated
}

// copyPathFragment appends the fragment at off to the search's path.
//
// With PathMarks, the marked character is the fragment's last one; CascLib's
// CopyPathFragment stops before it while its compare functions include it.
// This includes it, consistently with the latter. (SC2's roots use
// zero-terminated fragments, so the branch is not exercised there.)
func (t *pathFragmentTable) copyPathFragment(s *mndxSearch, off uint32) {
	if t.PathMarks.isEmpty() {
		for t.PathFragments[off] != 0 {
			s.buf = append(s.buf, t.PathFragments[off])
			off++
		}
		return
	}
	for {
		s.buf = append(s.buf, t.PathFragments[off])
		if t.PathMarks.isItemPresent(off) {
			return
		}
		off++
	}
}

//-----------------------------------------------------------------------------
// TFileNameDatabase

// hashEntry is CascLib's HASH_ENTRY. FragmentOffset doubles as the child
// database's table index, or, when its upper 24 bits are all set, holds a
// single character in its low byte.
type hashEntry struct {
	NodeIndex      uint32
	NextIndex      uint32
	FragmentOffset uint32
}

func (h *hashEntry) isSingleChar() bool { return h.FragmentOffset&0xFFFFFF00 == 0xFFFFFF00 }

type fileNameDatabase struct {
	CollisionTable         sparseArray // the trie shape, indexed by node
	FileNameIndexes        sparseArray // nodes that end a file name
	CollisionHiBitsIndexes sparseArray // nodes whose label is a fragment, not a char
	LoBitsTable            []byte      // label char, or low 8 bits of the fragment offset
	HiBitsTable            bitEntryArray
	PathFragmentTable      pathFragmentTable
	childDB                *fileNameDatabase
	HashTable              []hashEntry
	HashTableMask          uint32
	field214               uint32 // field_214: upward walks stop at nodes <= this
}

// loadMAR loads a MAR file: the 'MAR\0' signature and a database.
func loadMAR(data []byte) (*fileNameDatabase, error) {
	s := &byteStream{b: data}
	if s.u32() != marSignature {
		return nil, errors.New("bad MAR signature")
	}
	db := &fileNameDatabase{}
	if err := db.load(s); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *fileNameDatabase) load(s *byteStream) error {
	db.CollisionTable.load(s)
	db.FileNameIndexes.load(s)
	db.CollisionHiBitsIndexes.load(s)
	db.LoBitsTable = s.array(1)
	db.HiBitsTable.load(s)
	db.PathFragmentTable.PathFragments = s.array(1)
	db.PathFragmentTable.PathMarks.load(s)
	if s.err != nil {
		return s.err
	}
	// Fragments with no fragment table live in a nested database.
	if db.CollisionHiBitsIndexes.ValidItemCount != 0 && len(db.PathFragmentTable.PathFragments) == 0 {
		db.childDB = &fileNameDatabase{}
		if err := db.childDB.load(s); err != nil {
			return err
		}
	}
	raw := s.array(12)
	db.HashTable = make([]hashEntry, len(raw)/12)
	for i := range db.HashTable {
		r := raw[i*12:]
		db.HashTable[i] = hashEntry{
			binary.LittleEndian.Uint32(r),
			binary.LittleEndian.Uint32(r[4:]),
			binary.LittleEndian.Uint32(r[8:]),
		}
	}
	db.HashTableMask = uint32(len(db.HashTable)) - 1
	db.field214 = s.u32()
	bitMask := s.u32()
	if s.err != nil {
		return s.err
	}
	return checkStruct10(bitMask)
}

// checkStruct10 validates the database's option bits as TStruct10's
// sub_19572E0 does (CascLib keeps the decoded values but never uses them).
func checkStruct10(m uint32) error {
	bad := m&0xFFF00000 != 0
	switch m & 0xF80 {
	case 0x00, 0x80, 0x100, 0x200, 0x400, 0x800:
	default:
		bad = true
	}
	switch m & 0xF000 {
	case 0, 0x1000, 0x2000:
	default:
		bad = true
	}
	switch m & 0xF0000 {
	case 0, 0x10000, 0x20000:
	default:
		bad = true
	}
	if bad {
		return fmt.Errorf("MAR: bad option bits %#x", m)
	}
	return nil
}

func (db *fileNameDatabase) isPathFragmentString(i uint32) bool {
	return db.CollisionHiBitsIndexes.isItemPresent(i)
}

// pathFragmentOffset1 is GetPathFragmentOffset1: the fragment offset (or
// child table index) of node i.
func (db *fileNameDatabase) pathFragmentOffset1(i uint32) uint32 {
	hi := db.CollisionHiBitsIndexes.itemValueAt(i)
	return db.HiBitsTable.item(hi)<<8 | uint32(db.LoBitsTable[i])
}

// pathFragmentOffset2 is GetPathFragmentOffset2: like pathFragmentOffset1,
// but for nodes visited in order, keeping the hi-bits index in *hi.
func (db *fileNameDatabase) pathFragmentOffset2(hi *uint32, i uint32) uint32 {
	if *hi == invalidIndex {
		*hi = db.CollisionHiBitsIndexes.itemValueAt(i)
	} else {
		*hi++
	}
	return db.HiBitsTable.item(*hi)<<8 | uint32(db.LoBitsTable[i])
}

// copyFragment appends the fragment at off, from the child database or the
// fragment table.
func (db *fileNameDatabase) copyFragment(s *mndxSearch, off uint32) {
	if db.childDB != nil {
		db.childDB.copyPathFragmentByIndex(s, off)
	} else {
		db.PathFragmentTable.copyPathFragment(s, off)
	}
}

// copyPathFragmentByIndex appends the path of node t, walking up toward the
// root (the child database stores its fragments reversed).
func (db *fileNameDatabase) copyPathFragmentByIndex(s *mndxSearch, t uint32) {
	for steps := 0; ; steps++ {
		if steps > 1<<16 {
			panic("path fragment loop")
		}
		he := &db.HashTable[t&db.HashTableMask]
		if t == he.NextIndex {
			if !he.isSingleChar() {
				db.copyFragment(s, he.FragmentOffset)
			} else {
				s.buf = append(s.buf, byte(he.FragmentOffset))
			}
			t = he.NodeIndex
			if t == 0 {
				return
			}
		} else {
			if db.isPathFragmentString(t) {
				db.copyFragment(s, db.pathFragmentOffset1(t))
			} else {
				s.buf = append(s.buf, db.LoBitsTable[t])
			}
			if t <= db.field214 {
				return
			}
			t = db.CollisionTable.item1(t) - t - 1 // the parent node
		}
	}
}

//-----------------------------------------------------------------------------
// TMndxSearch / TStruct40 with an empty search mask: a depth-first walk of
// the trie that returns every file name in turn (TFileNameDatabase::DoSearch).

const (
	searchInitializing = 0
	searchSearching    = 2
	searchFinished     = 4
)

// pathStop is CascLib's TPathStop: the enumeration cursor of one trie level.
type pathStop struct {
	LoBitsIndex              uint32 // node being visited at this level
	field4                   uint32 // its bit in CollisionTable
	Count                    uint32 // path length after this node's label
	HiBitsIndex_PathFragment uint32 // running CollisionHiBitsIndexes rank
	field10                  uint32 // running file name index
}

func newPathStop(lo, f4, count uint32) pathStop {
	return pathStop{lo, f4, count, invalidIndex, invalidIndex}
}

type mndxSearch struct {
	db        *fileNameDatabase
	PathStops []pathStop
	buf       []byte // PathBuffer
	ItemCount int    // depth of the walk
	phase     int
}

func newMndxSearch(db *fileNameDatabase) *mndxSearch {
	return &mndxSearch{db: db, phase: searchInitializing}
}

// next returns the next file name (valid until the following call) and its
// file name index, or ok false once the walk is done.
func (s *mndxSearch) next() (name []byte, index uint32, ok bool) {
	db := s.db
	switch s.phase {
	case searchInitializing:
		// BeginSearch; with an empty mask there is no prefix to match.
		s.buf = s.buf[:0]
		s.PathStops = append(s.PathStops[:0], newPathStop(0, 0, 0))
		s.ItemCount = 1
		s.phase = searchSearching
		if db.FileNameIndexes.isItemPresent(0) {
			return s.buf, db.FileNameIndexes.itemValueAt(0), true
		}
		fallthrough
	case searchSearching:
		for {
			if s.ItemCount == len(s.PathStops) {
				// First visit at this depth: start at the first child of the
				// last node of the level above.
				last := &s.PathStops[len(s.PathStops)-1]
				col := db.CollisionTable.item0(last.LoBitsIndex) + 1
				s.PathStops = append(s.PathStops, newPathStop(col-last.LoBitsIndex-1, col, 0))
			}
			p := &s.PathStops[s.ItemCount]
			present := db.CollisionTable.isItemPresent(p.field4)
			p.field4++
			if present {
				s.ItemCount++
				if db.isPathFragmentString(p.LoBitsIndex) {
					db.copyFragment(s, db.pathFragmentOffset2(&p.HiBitsIndex_PathFragment, p.LoBitsIndex))
				} else {
					s.buf = append(s.buf, db.LoBitsTable[p.LoBitsIndex])
				}
				p.Count = uint32(len(s.buf))
				if db.FileNameIndexes.isItemPresent(p.LoBitsIndex) {
					if p.field10 == invalidIndex {
						p.field10 = db.FileNameIndexes.itemValueAt(p.LoBitsIndex)
					} else {
						p.field10++
					}
					return s.buf, p.field10, true
				}
			} else {
				// No more children here: back up to the parent's next sibling.
				if s.ItemCount == 1 {
					s.phase = searchFinished
					return nil, 0, false
				}
				s.PathStops[s.ItemCount-1].LoBitsIndex++
				s.buf = s.buf[:s.PathStops[s.ItemCount-2].Count]
				s.ItemCount--
			}
		}
	}
	return nil, 0, false
}
