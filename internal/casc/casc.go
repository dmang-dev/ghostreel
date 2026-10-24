// SPDX-License-Identifier: Apache-2.0

// Package casc reads files from a local StarCraft II installation's CASC
// storage (SC2Data/), the format Blizzard games keep their game data in.
// It is used only by the asset extractor (cmd/ghostreel-assets); the viewer
// never needs a game install.
//
// The chain from a file name to its bytes:
//
//	.build.info             the active build's config key
//	SC2Data/config/…        build config: the ENCODING and ROOT files' keys
//	ROOT (MNDX format)      file name  -> content key (CKey)
//	ENCODING                CKey       -> encoded key (EKey)
//	SC2Data/data/*.idx      EKey       -> archive number, offset, size
//	SC2Data/data/data.NNN   the archive; each blob is BLTE-encoded
//
// Layouts follow CascLib by Ladislav Zezula (MIT), in particular
// CascStructs.h, CascIndexFiles.cpp and CascRootFile_Mndx.cpp.
package casc

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Key is a 16-byte content or encoded key.
type Key [16]byte

func (k Key) String() string { return hex.EncodeToString(k[:]) }

// ParseKey parses a hex key.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return k, fmt.Errorf("bad key %q", s)
	}
	copy(k[:], b)
	return k, nil
}

type location struct {
	archive int
	offset  int64
	size    uint32
}

// Storage is an open CASC storage.
type Storage struct {
	dir     string            // SC2Data
	Config  map[string]string // build config
	index   map[[9]byte]location
	enc     map[Key]Key // CKey -> EKey
	archMu  sync.Mutex
	archive map[int]*os.File

	namesMu sync.Mutex
	byName  map[string]Key // normalized name -> CKey, from the ROOT file
	names   []string       // sorted
}

// Open opens the storage of an install folder (the one holding .build.info).
func Open(installDir string) (*Storage, error) {
	data := filepath.Join(installDir, "SC2Data")
	if _, err := os.Stat(data); err != nil {
		return nil, fmt.Errorf("no SC2Data folder in %s", installDir)
	}
	buildKey, err := activeBuildKey(filepath.Join(installDir, ".build.info"))
	if err != nil {
		return nil, err
	}
	s := &Storage{dir: data, archive: map[int]*os.File{}}
	if s.Config, err = readConfig(filepath.Join(data, "config", buildKey[0:2], buildKey[2:4], buildKey)); err != nil {
		return nil, fmt.Errorf("build config: %w", err)
	}
	if err := s.loadIndexes(); err != nil {
		return nil, err
	}
	encFields := strings.Fields(s.Config["encoding"])
	if len(encFields) < 2 {
		return nil, errors.New("build config has no encoding key")
	}
	encEKey, err := ParseKey(encFields[1])
	if err != nil {
		return nil, err
	}
	encData, err := s.ReadEKey(encEKey)
	if err != nil {
		return nil, fmt.Errorf("ENCODING: %w", err)
	}
	if s.enc, err = parseEncoding(encData); err != nil {
		return nil, fmt.Errorf("ENCODING: %w", err)
	}
	return s, nil
}

// Close closes the archive files.
func (s *Storage) Close() error {
	s.archMu.Lock()
	defer s.archMu.Unlock()
	for _, f := range s.archive {
		f.Close()
	}
	s.archive = map[int]*os.File{}
	return nil
}

// RootKey is the CKey of the ROOT file.
func (s *Storage) RootKey() (Key, error) {
	return ParseKey(strings.TrimSpace(s.Config["root"]))
}

// Files returns every CKey the ENCODING file lists.
func (s *Storage) Files() []Key {
	out := make([]Key, 0, len(s.enc))
	for k := range s.enc {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}

// ReadCKey reads a file by content key.
func (s *Storage) ReadCKey(ckey Key) ([]byte, error) {
	ekey, ok := s.enc[ckey]
	if !ok {
		return nil, fmt.Errorf("content key %s not in ENCODING", ckey)
	}
	return s.ReadEKey(ekey)
}

// LoadNames reads the ROOT file (MNDX format) and builds the name -> CKey
// table that Names and ReadFile use. Like CascLib, it keeps only the names
// whose content key the ENCODING file lists. Calling it again is a no-op.
func (s *Storage) LoadNames() error {
	s.namesMu.Lock()
	defer s.namesMu.Unlock()
	if s.byName != nil {
		return nil
	}
	rootKey, err := s.RootKey()
	if err != nil {
		return fmt.Errorf("ROOT: %w", err)
	}
	root, err := s.ReadCKey(rootKey)
	if err != nil {
		return fmt.Errorf("ROOT: %w", err)
	}
	entries, err := ParseMNDX(root)
	if err != nil {
		return fmt.Errorf("ROOT: %w", err)
	}
	byName := make(map[string]Key, len(entries))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, ok := s.enc[e.CKey]; !ok {
			continue
		}
		n := normalizeName(e.Name)
		if _, dup := byName[n]; !dup {
			names = append(names, e.Name)
		}
		byName[n] = e.CKey
	}
	sort.Strings(names)
	s.byName, s.names = byName, names
	return nil
}

// Names returns every file name of the storage, sorted, in the ROOT file's
// spelling: forward slashes, package first (for StarCraft II, lower case,
// e.g. "mods/liberty.sc2mod/base.sc2data/gamedata/unitdata.xml"). It is nil
// until LoadNames has run.
func (s *Storage) Names() []string {
	s.namesMu.Lock()
	defer s.namesMu.Unlock()
	return s.names
}

// ReadFile reads a file by name, loading the names first if needed. The
// lookup ignores case and accepts backslashes for slashes.
func (s *Storage) ReadFile(name string) ([]byte, error) {
	if err := s.LoadNames(); err != nil {
		return nil, err
	}
	s.namesMu.Lock()
	ckey, ok := s.byName[normalizeName(name)]
	s.namesMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, os.ErrNotExist)
	}
	return s.ReadCKey(ckey)
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimLeft(strings.ReplaceAll(name, `\`, "/"), "/"))
}

// ErrNotLocal is returned for files the installation lists but does not
// store (stripped or not yet downloaded).
var ErrNotLocal = errors.New("casc: file not in the local storage")

// ReadEKey reads a file by encoded key.
func (s *Storage) ReadEKey(ekey Key) ([]byte, error) {
	var k9 [9]byte
	copy(k9[:], ekey[:9])
	loc, ok := s.index[k9]
	if !ok {
		return nil, fmt.Errorf("encoded key %s: %w", ekey, ErrNotLocal)
	}
	f, err := s.archiveFile(loc.archive)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, loc.size)
	if _, err := f.ReadAt(blob, loc.offset); err != nil {
		return nil, fmt.Errorf("data.%03d at %d: %w", loc.archive, loc.offset, err)
	}
	// Each blob starts with a 30-byte header span (key, size, checksums).
	const headerSpan = 0x1E
	if len(blob) < headerSpan {
		return nil, errors.New("blob shorter than its header")
	}
	return decodeBLTE(blob[headerSpan:])
}

func (s *Storage) archiveFile(n int) (*os.File, error) {
	s.archMu.Lock()
	defer s.archMu.Unlock()
	if f, ok := s.archive[n]; ok {
		return f, nil
	}
	f, err := os.Open(filepath.Join(s.dir, "data", fmt.Sprintf("data.%03d", n)))
	if err != nil {
		return nil, err
	}
	s.archive[n] = f
	return f, nil
}

// activeBuildKey reads the build config key of the active row of .build.info
// (a pipe-separated table whose header names columns like "Build Key!HEX:16").
func activeBuildKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var cols []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if cols == nil {
			for _, c := range fields {
				cols = append(cols, strings.SplitN(c, "!", 2)[0])
			}
			continue
		}
		row := map[string]string{}
		for i, c := range cols {
			if i < len(fields) {
				row[c] = fields[i]
			}
		}
		if row["Active"] == "1" || row["Active"] == "" {
			if k := row["Build Key"]; len(k) == 32 {
				return k, nil
			}
		}
	}
	return "", fmt.Errorf("%s: no active build", path)
}

func readConfig(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, nil
}

// loadIndexes reads the newest .idx file of each of the 16 buckets
// (named BBVVVVVVVV.idx: bucket, then version, in hex).
func (s *Storage) loadIndexes() error {
	files, _ := filepath.Glob(filepath.Join(s.dir, "data", "*.idx"))
	newest := map[int]string{}
	ver := map[int]int64{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".idx")
		if len(name) != 10 {
			continue
		}
		b, err1 := strconv.ParseInt(name[:2], 16, 32)
		v, err2 := strconv.ParseInt(name[2:], 16, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if cur, ok := ver[int(b)]; !ok || v > cur {
			newest[int(b)], ver[int(b)] = f, v
		}
	}
	if len(newest) == 0 {
		return errors.New("no .idx files in SC2Data/data")
	}
	s.index = map[[9]byte]location{}
	for _, f := range newest {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := parseIndexV2(data, s.index); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

// parseIndexV2 reads an index file: a guarded header block (size, hash,
// FILE_INDEX_HEADER_V2), padding to 16 bytes, then a guarded block of
// 18-byte entries: 9-byte EKey, 5-byte big-endian (archive << segmentBits |
// offset), 4-byte little-endian encoded size.
func parseIndexV2(data []byte, into map[[9]byte]location) error {
	le := binary.LittleEndian
	if len(data) < 0x28+8 {
		return errors.New("index file too short")
	}
	hdrSize := int(le.Uint32(data[0:]))
	hdr := data[8 : 8+hdrSize]
	if hdrSize < 16 || le.Uint16(hdr[0:]) != 7 {
		return fmt.Errorf("unsupported index version %d", le.Uint16(hdr[0:]))
	}
	sizeBytes, offBytes, keyBytes, segBits := int(hdr[4]), int(hdr[5]), int(hdr[6]), uint(hdr[7])
	if sizeBytes != 4 || offBytes != 5 || keyBytes != 9 {
		return errors.New("unexpected index entry layout")
	}
	pos := (8 + hdrSize + 15) &^ 15
	blockSize := int(le.Uint32(data[pos:]))
	pos += 8
	entryLen := keyBytes + offBytes + sizeBytes
	end := min(pos+blockSize, len(data))
	for ; pos+entryLen <= end; pos += entryLen {
		e := data[pos : pos+entryLen]
		var k [9]byte
		copy(k[:], e[:9])
		var packed uint64
		for _, b := range e[9:14] {
			packed = packed<<8 | uint64(b)
		}
		into[k] = location{
			archive: int(packed >> segBits),
			offset:  int64(packed & (1<<segBits - 1)),
			size:    le.Uint32(e[14:]),
		}
	}
	return nil
}

// decodeBLTE decodes a BLTE container: "BLTE", a big-endian header size,
// then (if non-zero) a frame table, then the frames. Each frame starts with
// its mode: 'N' stored, 'Z' zlib.
func decodeBLTE(b []byte) ([]byte, error) {
	be := binary.BigEndian
	if len(b) < 8 || string(b[:4]) != "BLTE" {
		return nil, errors.New("not a BLTE blob")
	}
	hdrSize := int(be.Uint32(b[4:]))
	if hdrSize == 0 {
		return decodeFrame(b[8:])
	}
	if len(b) < 12 || b[8] != 0x0F {
		return nil, errors.New("bad BLTE frame table")
	}
	count := int(b[9])<<16 | int(b[10])<<8 | int(b[11])
	table := b[12:]
	data := b[hdrSize:]
	var out bytes.Buffer
	pos := 0
	for i := 0; i < count; i++ {
		e := table[i*24:]
		encSize := int(be.Uint32(e[0:]))
		if pos+encSize > len(data) {
			return nil, errors.New("BLTE frame past the end")
		}
		f, err := decodeFrame(data[pos : pos+encSize])
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out.Write(f)
		pos += encSize
	}
	return out.Bytes(), nil
}

func decodeFrame(f []byte) ([]byte, error) {
	if len(f) == 0 {
		return nil, nil
	}
	switch f[0] {
	case 'N':
		return f[1:], nil
	case 'Z':
		zr, err := zlib.NewReader(bytes.NewReader(f[1:]))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	case 'E':
		return nil, errors.New("encrypted BLTE frame")
	}
	return nil, fmt.Errorf("unsupported BLTE frame mode %q", f[0])
}

// parseEncoding reads the CKey -> EKey table of the ENCODING file: a header,
// an ESpec string block, a page index, then CKey pages whose entries are a
// key count, a 40-bit content size, the CKey and its EKeys.
func parseEncoding(b []byte) (map[Key]Key, error) {
	be := binary.BigEndian
	if len(b) < 22 || b[0] != 'E' || b[1] != 'N' {
		return nil, errors.New("not an ENCODING file")
	}
	ckeyLen, ekeyLen := int(b[3]), int(b[4])
	pageSize := int(be.Uint16(b[5:])) * 1024
	pageCount := int(be.Uint32(b[9:]))
	especSize := int(be.Uint32(b[18:]))
	pos := 22 + especSize + pageCount*32 // skip ESpec block and page index
	out := make(map[Key]Key, pageCount*pageSize/40)
	for p := 0; p < pageCount; p++ {
		page := b[pos:min(pos+pageSize, len(b))]
		for i := 0; i+6+ckeyLen <= len(page); {
			n := int(page[i])
			if n == 0 {
				break
			}
			var ck, ek Key
			copy(ck[:], page[i+6:i+6+ckeyLen])
			copy(ek[:], page[i+6+ckeyLen:i+6+ckeyLen+ekeyLen])
			out[ck] = ek
			i += 6 + ckeyLen + n*ekeyLen
		}
		pos += pageSize
	}
	return out, nil
}
