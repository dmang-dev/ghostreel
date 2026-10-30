// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

// Lighting is a map's lighting: an ambient color and the key, fill and back
// directional lights. The map names a terrain type (MapInfo); the terrain
// names its lighting (CTerrain Lighting, by default its own id), a CLight
// entry. The map's own TerrainData.xml and LightData.xml apply on top.
type Lighting struct {
	ID      string     `json:"id"`
	Ambient [3]float64 `json:"ambient"`
	Key     Light      `json:"key"`
	Fill    Light      `json:"fill"`
	Back    Light      `json:"back"`
}

// Light is a directional light. Direction points from the light toward the
// scene, in game axes (x east, y north, z up).
type Light struct {
	Color      [3]float64 `json:"color"`
	Multiplier float64    `json:"multiplier"`
	Direction  [3]float64 `json:"direction"`
}

// lightSet holds CLight entries as flat fields ("Ambient", "Key.Color",
// "Key.Direction") of their first time-of-day entry; parent links are kept
// under "parent".
type lightSet map[string]map[string]string

// add reads a LightData.xml; later files override fields of earlier ones.
func (s lightSet) add(data []byte) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var cur map[string]string
	light := ""  // the DirectionalLight being read
	tod := false // inside the first ToDInfoArray
	for {
		tok, err := d.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			attr := func(name string) string {
				for _, a := range t.Attr {
					if a.Name.Local == name {
						return a.Value
					}
				}
				return ""
			}
			switch t.Name.Local {
			case "CLight":
				id := attr("id")
				cur = s[id]
				if cur == nil {
					cur = map[string]string{}
					s[id] = cur
				}
				if p := attr("parent"); p != "" {
					cur["parent"] = p
				}
			case "ToDInfoArray":
				tod = cur != nil && (attr("index") == "0" || attr("index") == "")
				if tod && attr("AmbientColor") != "" {
					cur["Ambient"] = attr("AmbientColor")
				}
			case "DirectionalLight":
				if !tod {
					continue
				}
				light = attr("index")
				for _, f := range []string{"Color", "ColorMultiplier", "Direction"} {
					if v := attr(f); v != "" {
						cur[light+"."+f] = v
					}
				}
			case "Color", "ColorMultiplier", "Direction":
				if tod && light != "" {
					if v := attr("value"); v != "" {
						cur[light+"."+t.Name.Local] = v
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "CLight":
				cur, tod = nil, false
			case "ToDInfoArray":
				tod = false
			case "DirectionalLight":
				light = ""
			}
		}
	}
}

// resolve merges an entry with its parents, nearest first.
func (s lightSet) resolve(id string) map[string]string {
	out := map[string]string{}
	seen := map[string]bool{}
	for e := s[id]; e != nil && !seen[id] && len(seen) < 16; e = s[id] {
		seen[id] = true
		for k, v := range e {
			if _, set := out[k]; !set {
				out[k] = v
			}
		}
		id = e["parent"]
		if id == "" {
			break
		}
	}
	return out
}

// Lighting returns the lighting of the terrain type a map names, with the
// map's TerrainData.xml and LightData.xml applied (nil for none); nil when
// neither the game data nor the map defines it.
func (l *Library) Lighting(terrain string, mapTerrain, mapLights []byte) *Lighting {
	d := l.loadData()
	// The terrain's Lighting, looked up through the map's own terrain types
	// and then the game's (a map's terrain may derive from a game one). The
	// default, ##id##, names the terrain itself.
	mt := newCatalog()
	if mapTerrain != nil {
		_ = mt.add(mapTerrain, func(c string) bool { return c == "CTerrain" })
	}
	id, cur := terrain, terrain
	for i := 0; i < 16 && cur != ""; i++ {
		e := mt.entries["CTerrain/"+cur]
		if e == nil {
			if v, ok := d.terrains.rawField("CTerrain", cur, "Lighting"); ok {
				id = v
			}
			break
		}
		if v, ok := e.fields["Lighting"]; ok {
			id = v
			break
		}
		cur = e.parent
	}
	id = strings.ReplaceAll(id, "##id##", terrain)
	set := lightSet{}
	for k, v := range d.lights {
		m := make(map[string]string, len(v))
		for f, x := range v {
			m[f] = x
		}
		set[k] = m
	}
	if mapLights != nil {
		set.add(mapLights)
	}
	f := set.resolve(id)
	if len(f) == 0 {
		return nil
	}
	lt := &Lighting{ID: id, Ambient: vec3(f["Ambient"])}
	for _, p := range []struct {
		name string
		dst  *Light
	}{{"Key", &lt.Key}, {"Fill", &lt.Fill}, {"Back", &lt.Back}} {
		p.dst.Color = vec3(f[p.name+".Color"])
		p.dst.Direction = vec3(f[p.name+".Direction"])
		p.dst.Multiplier = 1
		if m, err := strconv.ParseFloat(f[p.name+".ColorMultiplier"], 64); err == nil {
			p.dst.Multiplier = m
		}
	}
	return lt
}

func vec3(s string) [3]float64 {
	var v [3]float64
	for i, p := range strings.SplitN(s, ",", 3) {
		v[i], _ = strconv.ParseFloat(strings.TrimSpace(p), 64)
	}
	return v
}
