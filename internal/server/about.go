// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dmang-dev/ghostreel/internal/mapfile"
)

// preview serves the screenshot a map carries, if any.
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	l := s.App.Get(r.PathValue("id"))
	if l == nil || l.MapFile() == nil || len(l.MapFile().Preview) == 0 {
		httpError(w, http.StatusNotFound, errors.New("no map preview"))
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(l.MapFile().Preview)
}

// loadingScreen serves a map's loading screen image from the game data.
func (s *Server) loadingScreen(w http.ResponseWriter, r *http.Request) {
	lib := s.assets.Load()
	l := s.App.Get(r.PathValue("id"))
	if lib == nil || l == nil || l.MapFile() == nil || l.MapFile().LoadingScreen == "" {
		httpError(w, http.StatusNotFound, errors.New("no loading screen or no game data"))
		return
	}
	b, err := lib.LoadingScreenPNG(l.MapFile().LoadingScreen)
	if err != nil {
		httpError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(b)
}

// lighting serves the map's lighting, which needs the game data's lights.
func (s *Server) lighting(w http.ResponseWriter, r *http.Request) {
	lib := s.assets.Load()
	l := s.App.Get(r.PathValue("id"))
	if lib == nil || l == nil || l.MapFile() == nil {
		httpError(w, http.StatusNotFound, errors.New("no map or no game data"))
		return
	}
	m := l.MapFile()
	lt := lib.Lighting(m.Tileset, m.TerrainData, m.LightData)
	if lt == nil {
		httpError(w, http.StatusNotFound, errors.New("neither the game data nor the map has the lighting of terrain "+m.Tileset))
		return
	}
	writeJSON(w, lt)
}

// water serves the map's water and lava: where it lies and how each kind
// looks, from the game's WaterData (gameWater, nil without game data) with
// the map's own on top.
func (s *Server) water(gameWater func() [][]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := s.App.Get(r.PathValue("id"))
		if l == nil || l.MapFile() == nil {
			httpError(w, http.StatusNotFound, errors.New("no map"))
			return
		}
		m := l.MapFile()
		areas := m.Water
		if areas == nil {
			areas = []mapfile.WaterRect{}
		}
		var ids []string
		seen := map[string]bool{}
		for _, a := range areas {
			if !seen[a.Type] {
				seen[a.Type] = true
				ids = append(ids, a.Type)
			}
		}
		var docs [][]byte
		if gameWater != nil {
			docs = append(docs, gameWater()...)
		}
		docs = append(docs, m.WaterData)
		writeJSON(w, map[string]any{"areas": areas, "types": mapfile.ResolveWater(ids, docs...)})
	}
}

// info serves unit stats and display names from the game data for the
// comma-separated units, upgrades and abilities in the query.
func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	lib := s.assets.Load()
	if lib == nil {
		httpError(w, http.StatusNotFound, errors.New("no game data"))
		return
	}
	list := func(k string) []string {
		var out []string
		for _, v := range strings.Split(r.URL.Query().Get(k), ",") {
			if v = strings.TrimSpace(v); v != "" && len(out) < 2000 {
				out = append(out, v)
			}
		}
		return out
	}
	writeJSON(w, lib.Info(list("units"), list("upgrades"), list("abilities")))
}
