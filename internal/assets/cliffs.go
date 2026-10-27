// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dmang-dev/ghostreel/internal/mapfile"
)

// Cliffs: the game draws cliffs as models, one piece per cliff cell (2×2
// cells), chosen by the cliff levels at the cell's corners. A cliff set
// (CCliff) names a folder of pieces (CCliffMesh) and a material. Pieces are
// named by their corners, south-west, south-east, north-east, north-west,
// "a" the lowest level ("cliffmade11_aaab_00.m3"); only one rotation of
// each shape is stored, so a cell takes the stored rotation turned in
// quarter turns. Pieces are in world units, 2 cells across, with each
// letter at its CliffHeights value above letter "a" (a=-8, b=0, c=2, d=4
// by default: one cliff level is 2 units, and the wall runs on 8 units
// below the top's lower neighbour, hidden by the ground or showing where
// the ground falls away). See docs/MAP_FORMAT_NOTES.md.

// CliffPiece is one placed cliff piece.
type CliffPiece struct {
	Set  int     `json:"s"` // index into CliffLayout.Sets
	Name string  `json:"n"` // corner letters as stored, "aaab"
	Var  int     `json:"v"` // file variation
	X    float64 `json:"x"` // cliff cell centre, in cells
	Y    float64 `json:"y"`
	Z    float64 `json:"z"` // height of the piece's origin
	Rot  int     `json:"r"` // quarter turns counter-clockwise
}

// CliffSet is how a map's cliff set is drawn.
type CliffSet struct {
	Name string `json:"name"` // CCliff id
	Mesh string `json:"mesh"` // CCliffMesh id: the folder of pieces
}

// CliffLayout is every cliff piece of a map.
type CliffLayout struct {
	Sets   []CliffSet   `json:"sets"`
	Pieces []CliffPiece `json:"pieces"`
}

// cliffPieces lists a cliff mesh's pieces: corner letters → number of
// variations.
func (l *Library) cliffPieces(mesh string) map[string]int {
	dir, ok := l.cliffs.field("CCliffMesh", mesh, "ModelPath")
	if !ok || dir == "" {
		return nil
	}
	prefix := strings.ToLower(strings.ReplaceAll(dir, `\`, "/")) + strings.ToLower(mesh) + "_"
	out := map[string]int{}
	for key := range l.files {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok || !strings.HasSuffix(rest, ".m3") {
			continue
		}
		name, num, _ := strings.Cut(strings.TrimSuffix(rest, ".m3"), "_")
		if len(name) != 4 {
			continue
		}
		if v, err := strconv.Atoi(num); err == nil && v+1 > out[name] {
			out[name] = v + 1
		}
	}
	return out
}

// cliffHeight is a cliff mesh's height for a letter (0 = "a"), relative
// to "b".
func (l *Library) cliffHeight(mesh string, letter int) float64 {
	def := []float64{-8, 0, 2, 4}
	if v, ok := l.cliffs.field("CCliffMesh", mesh, fmt.Sprintf("CliffHeights[%d]", letter)); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	if letter < len(def) {
		return def[letter]
	}
	return float64(letter-1) * 2
}

// CliffLayout places a map's cliff pieces. Cells whose shape has no piece
// in their set are left out.
func (l *Library) CliffLayout(m *mapfile.Map) (*CliffLayout, error) {
	c := m.Cliffs
	if c == nil {
		return nil, fmt.Errorf("%w (the map has no cliff data)", ErrNoModel)
	}
	levelH := levelHeights(m)
	out := &CliffLayout{Pieces: []CliffPiece{}}
	pieces := make([]map[string]int, len(c.Sets))
	for i, s := range c.Sets {
		mesh, _ := l.cliffs.field("CCliff", s, "CliffMesh")
		out.Sets = append(out.Sets, CliffSet{Name: s, Mesh: mesh})
		if mesh != "" {
			pieces[i] = l.cliffPieces(mesh)
		}
	}
	for j := 0; j < c.H; j++ {
		for i := 0; i < c.W; i++ {
			cell := c.Cells[j*c.W+i]
			if cell.Set < 0 || len(pieces[cell.Set]) == 0 {
				continue
			}
			corners := c.Corners(i, j)
			letters := mapfile.PieceLetters(corners)
			if letters == "aaaa" {
				continue
			}
			name, rot, n := "", 0, 0
			for k := 0; k < 4; k++ {
				// Turned k quarter turns, the stored piece's corner j lands
				// on corner (j+k) mod 4.
				p := []byte{letters[k%4], letters[(1+k)%4], letters[(2+k)%4], letters[(3+k)%4]}
				if v := pieces[cell.Set][string(p)]; v > 0 {
					name, rot, n = string(p), k, v
					break
				}
			}
			if name == "" {
				continue
			}
			lo := min(corners[0], corners[1], corners[2], corners[3])
			mesh := out.Sets[cell.Set].Mesh
			// Letter "b" sits at the height of the level above the lowest.
			z := levelH(lo+1) - (l.cliffHeight(mesh, 1) - l.cliffHeight(mesh, 0))
			out.Pieces = append(out.Pieces, CliffPiece{
				Set: cell.Set, Name: name, Var: cell.Var % n,
				X: float64(2*i + 1), Y: float64(2*j + 1), Z: z, Rot: rot,
			})
		}
	}
	return out, nil
}

// levelHeights returns the ground height of each cliff level: the most
// common height at corners of that level, or 2 units a level from the
// nearest known one.
func levelHeights(m *mapfile.Map) func(level int) float64 {
	counts := map[int]map[float32]int{}
	if len(m.Heights) == len(m.Cliffs.Levels) {
		for i, lv := range m.Cliffs.Levels {
			if counts[int(lv)] == nil {
				counts[int(lv)] = map[float32]int{}
			}
			counts[int(lv)][m.Heights[i]]++
		}
	}
	known := map[int]float64{}
	for lv, hs := range counts {
		best, bestN := float32(0), -1
		for h, n := range hs {
			if n > bestN {
				best, bestN = h, n
			}
		}
		known[lv] = float64(best)
	}
	return func(level int) float64 {
		if h, ok := known[level]; ok {
			return h
		}
		for d := 1; d < 8; d++ {
			if h, ok := known[level-d]; ok {
				return h + 2*float64(d)
			}
			if h, ok := known[level+d]; ok {
				return h - 2*float64(d)
			}
		}
		return 2 * float64(level-1)
	}
}

// CliffGLB returns a cliff piece (a mesh id, its corner letters and a
// variation) as binary glTF, converting it on first use.
func (l *Library) CliffGLB(mesh, piece string, variation int) ([]byte, error) {
	if !safeName.MatchString(mesh) || len(piece) != 4 || strings.Trim(piece, "abcd") != "" || variation < 0 || variation > 99 {
		return nil, ErrNoModel
	}
	key := fmt.Sprintf("cliff.%s.%s.%d", mesh, piece, variation)
	return l.cached(key, func() (string, []string, error) {
		dir, ok := l.cliffs.field("CCliffMesh", mesh, "ModelPath")
		if !ok || dir == "" {
			return "", nil, ErrNoModel
		}
		name, ok := l.file(fmt.Sprintf("%s%s_%s_%02d.m3", dir, mesh, piece, variation))
		if !ok {
			return "", nil, fmt.Errorf("%w (no piece %s_%s_%02d)", ErrNoModel, mesh, piece, variation)
		}
		return name, nil, nil
	})
}

// CliffMaterialGLB returns a cliff set's material model as binary glTF: a
// small mesh whose material the set's pieces are drawn with.
func (l *Library) CliffMaterialGLB(set string) ([]byte, error) {
	if !safeName.MatchString(set) {
		return nil, ErrNoModel
	}
	return l.cached("cliffmaterial."+set, func() (string, []string, error) {
		path, ok := l.cliffs.field("CCliff", set, "CliffMaterial")
		if !ok || path == "" {
			return "", nil, ErrNoModel
		}
		name, ok := l.file(path)
		if !ok {
			return "", nil, fmt.Errorf("%w (%s is not in the game data)", ErrNoModel, path)
		}
		return name, nil, nil
	})
}
