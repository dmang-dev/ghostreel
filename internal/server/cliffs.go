// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/dmang-dev/ghostreel/internal/assets"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
)

var (
	cliffMu     sync.Mutex
	cliffLayout = map[*mapfile.Map][]byte{} // JSON per decoded map
)

// cliffs serves where a replay's map draws its cliff pieces. The pieces
// come from the game data, so this needs it as well as the map.
func (s *Server) cliffs(w http.ResponseWriter, r *http.Request) {
	lib := s.assets.Load()
	l := s.App.Get(r.PathValue("id"))
	if lib == nil || l == nil || l.MapFile() == nil {
		httpError(w, http.StatusNotFound, errors.New("no map or no game data"))
		return
	}
	m := l.MapFile()
	cliffMu.Lock()
	b := cliffLayout[m]
	cliffMu.Unlock()
	if b == nil {
		lay, err := lib.CliffLayout(m)
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, assets.ErrNoModel) {
				code = http.StatusNotFound
			}
			httpError(w, code, err)
			return
		}
		if b, err = json.Marshal(lay); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		cliffMu.Lock()
		cliffLayout[m] = b
		cliffMu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}
