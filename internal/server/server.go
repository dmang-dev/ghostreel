// SPDX-License-Identifier: Apache-2.0

// Package server exposes the app service over HTTP for the viewer. The same
// handler backs `ghostreel serve` and the desktop app's asset server, so the
// viewer talks to one API everywhere.
//
//	GET  /api/config               {network}
//	GET  /api/library              replays in the configured folders
//	POST /api/library/open         {"name": "..."} → Summary
//	POST /api/open                 body = replay bytes, X-Filename header → Summary
//	GET  /api/r/{id}/timeline      binary timeline (application/x-protobuf)
//	GET  /api/r/{id}/map           map JSON (404 when no map)
//	GET  /api/r/{id}/minimap       PNG (404 when none)
//	POST /api/r/{id}/map           body = map bytes, X-Filename header → Summary
package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OWNER/ghostreel/internal/app"
	"github.com/OWNER/ghostreel/internal/replay"
)

// MaxUpload bounds uploaded replays and maps.
const MaxUpload = 128 << 20

// Server serves the API and, optionally, static viewer files.
type Server struct {
	App *app.Service
	// Library lists folders whose replays the viewer may open by name.
	Library []string
	// Static serves the built viewer; nil serves only the API.
	Static fs.FS
}

// Summary is what the viewer needs to know after loading a replay.
type Summary struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MapTitle  string   `json:"mapTitle"`
	Duration  string   `json:"duration"`
	Version   string   `json:"version"`
	Approx    int      `json:"protocolApproxFrom,omitempty"`
	HasMap    bool     `json:"hasMap"`
	MapHow    string   `json:"mapHow,omitempty"`
	MapError  string   `json:"mapError,omitempty"`
	FromCache bool     `json:"fromCache"`
	Players   []string `json:"players"`
}

func summarize(l *app.Loaded) Summary {
	s := Summary{
		ID: l.ID, Name: l.Name, MapTitle: l.Info.MapTitle, Duration: replay.FormatClock(l.Info.Loops),
		Version: l.Info.Version, Approx: l.Info.ProtocolApproxFrom, HasMap: l.Map != nil,
		MapHow: string(l.MapHow), MapError: l.MapErr, FromCache: l.FromCache,
	}
	for _, p := range l.Info.Players {
		s.Players = append(s.Players, p.Name)
	}
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"network": s.App.Resolver != nil && s.App.Resolver.AllowNetwork})
	})
	mux.HandleFunc("GET /api/library", s.library)
	mux.HandleFunc("POST /api/library/open", s.libraryOpen)
	mux.HandleFunc("POST /api/open", s.open)
	mux.HandleFunc("GET /api/r/{id}/timeline", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Timeline, "application/x-protobuf"
	}))
	mux.HandleFunc("GET /api/r/{id}/map", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Map, "application/json"
	}))
	mux.HandleFunc("GET /api/r/{id}/minimap", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Minimap, "image/png"
	}))
	mux.HandleFunc("POST /api/r/{id}/map", s.setMap)
	if s.Static != nil {
		mux.Handle("/", http.FileServerFS(s.Static))
	}
	return mux
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var out []entry
	seen := map[string]bool{}
	for _, dir := range s.Library {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".SC2Replay") || seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true
			var size int64
			if fi, err := e.Info(); err == nil {
				size = fi.Size()
			}
			out = append(out, entry{e.Name(), size})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out)
}

// libraryOpen opens a replay by name from a library folder. Only plain file
// names are accepted, so requests can't reach outside those folders.
func (s *Server) libraryOpen(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" || req.Name != filepath.Base(req.Name) || strings.ContainsAny(req.Name, `/\`) {
		httpError(w, http.StatusBadRequest, errors.New("invalid name"))
		return
	}
	for _, dir := range s.Library {
		p := filepath.Join(dir, req.Name)
		if _, err := os.Stat(p); err == nil {
			l, err := s.App.LoadFile(r.Context(), p)
			if err != nil {
				httpError(w, http.StatusUnprocessableEntity, err)
				return
			}
			writeJSON(w, summarize(l))
			return
		}
	}
	httpError(w, http.StatusNotFound, errors.New("no such replay in the library"))
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxUpload+1))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if len(data) > MaxUpload {
		httpError(w, http.StatusRequestEntityTooLarge, errors.New("replay too large"))
		return
	}
	l, err := s.App.LoadBytes(r.Context(), filename(r, "replay.SC2Replay"), data)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, summarize(l))
}

func (s *Server) setMap(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxUpload+1))
	if err != nil || len(data) > MaxUpload {
		httpError(w, http.StatusBadRequest, errors.New("could not read map upload"))
		return
	}
	l, err := s.App.SetMap(r.Context(), r.PathValue("id"), filename(r, "map.SC2Map"), data)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, summarize(l))
}

func (s *Server) blob(pick func(*app.Loaded) ([]byte, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := s.App.Get(r.PathValue("id"))
		if l == nil {
			httpError(w, http.StatusNotFound, errors.New("replay not loaded"))
			return
		}
		data, ctype := pick(l)
		if data == nil {
			httpError(w, http.StatusNotFound, errors.New("not available"))
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(data)
	}
}

func filename(r *http.Request, def string) string {
	name, err := url.PathUnescape(r.Header.Get("X-Filename"))
	if err != nil || name == "" {
		return def
	}
	return filepath.Base(name)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
