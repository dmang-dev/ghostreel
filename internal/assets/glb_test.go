// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
)

type glbInfo struct {
	anims    []string
	textures int
	extras   glbExtras
}

// glbExtras are the file's extras: the model's attachment points and the
// unit's look.
type glbExtras struct {
	Attachments map[string]string
	Scale       []float64
	Groups      []string
	Attach      []attachment
}

func parseGLB(b []byte) (*glbInfo, error) {
	if len(b) < 20 || string(b[:4]) != "glTF" || int(binary.LittleEndian.Uint32(b[8:])) != len(b) {
		return nil, errors.New("bad GLB header")
	}
	n := binary.LittleEndian.Uint32(b[12:])
	var doc struct {
		Animations []struct{ Name string }
		Textures   []json.RawMessage
		Extras     glbExtras
	}
	if err := json.Unmarshal(b[20:20+n], &doc); err != nil {
		return nil, err
	}
	g := &glbInfo{textures: len(doc.Textures), extras: doc.Extras}
	for _, a := range doc.Animations {
		g.anims = append(g.anims, a.Name)
	}
	return g, nil
}

func (g *glbInfo) hasAnim(prefix string) bool {
	for _, a := range g.anims {
		if a == prefix || strings.HasPrefix(a, prefix+" ") {
			return true
		}
	}
	return false
}

func (g *glbInfo) animNames() []string { return g.anims }
