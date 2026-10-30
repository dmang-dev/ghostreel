// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"net/http"
	"strings"
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
