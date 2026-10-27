// SPDX-License-Identifier: Apache-2.0

// Package server exposes the app service over HTTP for the viewer. The same
// handler backs `ghostreel serve` and the desktop app's asset server, so the
// viewer talks to one API everywhere.
//
//	GET  /api/config               {network, openPaths, pick, pickFolder, settings, update}
//	GET  /api/library              replays in the configured folders
//	POST /api/library/open         {"name": "..."} → Summary
//	POST /api/open                 body = replay bytes, X-Filename header → Summary
//	POST /api/open-path            {"path": "..."} → Summary (desktop only: OpenPaths)
//	POST /api/pick                 native file dialog → {"path"}, "" if cancelled (desktop only)
//	POST /api/pick-folder          native folder dialog → {"path"}, "" if cancelled (desktop only)
//	GET  /api/settings             Settings plus {gameDataStatus} (desktop only)
//	PUT  /api/settings             Settings → the same as GET, or 400 with a message for the user
//	GET  /api/update               UpdateStatus (desktop only)
//	POST /api/update/check         look for a newer release now → UpdateStatus
//	POST /api/update/apply         download, verify and install it; the app restarts
//	GET  /api/r/{id}/timeline      binary timeline (application/x-protobuf)
//	GET  /api/r/{id}/map           map JSON (404 when no map)
//	GET  /api/r/{id}/minimap       PNG (404 when none)
//	GET  /api/r/{id}/doodads       JSON list of the map's doodads (404 when no map)
//	GET  /api/r/{id}/analysis      JSON reports of the analysis plugins (build orders, battles, ...)
//	POST /api/r/{id}/map           body = map bytes, X-Filename header → Summary
//	POST /api/r/{id}/download-map  fetch the map from Blizzard's depot (user-initiated) → Summary
//	POST /api/r/{id}/find-map      look for a missing map again (after the settings change) → Summary
//	GET  /api/startup              Summary of a replay opened at launch, or null
//	GET  /api/assets               {available, build}: whether 3D models can be served
//	GET  /api/assets/unit/{type}   binary glTF of a unit type's model (404 when none)
//	GET  /api/assets/death/{type}  binary glTF of its death model (404 when none)
//	GET  /api/assets/icon/{type}   PNG command-card icon (404 when none)
//	GET  /api/assets/model/{id}    binary glTF of a model by id, ?v= variation (doodads)
//	GET  /api/assets/terrain/{id}  PNG terrain texture layer (map JSON's terrainTextures)
//	GET  /api/r/{id}/ground        JPEG of the map's painted ground, from the game's textures
//	GET  /api/r/{id}/masks         gzipped texture-mask planes (map JSON's terrainMasks)
//	GET  /api/r/{id}/cliffs        JSON of the map's cliff pieces (assets.CliffLayout)
//	GET  /api/assets/cliff/{mesh}/{piece}  binary glTF of a cliff piece, ?v= variation
//	GET  /api/assets/cliffmaterial/{set}   binary glTF of a cliff set's material model
package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dmang-dev/ghostreel/internal/app"
	"github.com/dmang-dev/ghostreel/internal/assets"
	"github.com/dmang-dev/ghostreel/internal/casc"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
	"github.com/dmang-dev/ghostreel/internal/replay"
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
	// Startup, if set, is a replay already loaded at launch (desktop "Open
	// with"); the viewer shows it on start.
	Startup *app.Loaded
	// OpenPaths lets the viewer open replays by their path on disk, so maps
	// kept beside them are found. Only the desktop app sets it: its viewer
	// is the local user's own, while `serve` may be reached by any page.
	OpenPaths bool
	// PickReplay, if set, shows a native open dialog and returns the chosen
	// path ("" if cancelled), so the viewer gets a path, not just bytes.
	PickReplay func() (string, error)
	// PickFolder, if set, shows a native folder dialog (for the settings).
	PickFolder func() (string, error)
	// Settings, if set, backs the settings page (the desktop app's).
	Settings SettingsStore
	// Updater, if set, checks for and installs new releases (desktop app).
	Updater Updater

	assets atomic.Pointer[assets.Library]

	groundMu sync.Mutex
	ground   map[*mapfile.Map][]byte // composed ground images, per decoded map
}

// SetAssets makes 3D models available once the game data has been opened
// (which takes a moment, so callers may do it in the background).
func (s *Server) SetAssets(l *assets.Library) { s.assets.Store(l) }

// Settings are what the desktop app's settings page edits.
type Settings struct {
	// GameData is the StarCraft II folder; "" finds the install.
	GameData string `json:"gameData"`
	// MapFolders are searched for local maps, after the defaults.
	MapFolders []string `json:"mapFolders"`
	// CheckUpdates: look for a new release when the app starts. Unset
	// until the user has been asked; left as it is when a change omits it.
	CheckUpdates *bool `json:"checkUpdates,omitempty"`
}

// UpdateStatus is where the desktop app's updater stands.
type UpdateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	NotesURL  string `json:"notesUrl,omitempty"`
	// CanApply: this copy can install the update itself (otherwise the
	// viewer links to the release).
	CanApply bool `json:"canApply"`
	// State is "idle", "checking", "downloading", "installing" or "error".
	State    string  `json:"state"`
	Progress float64 `json:"progress,omitempty"` // 0-1 while downloading
	Error    string  `json:"error,omitempty"`
	Checked  string  `json:"checked,omitempty"` // RFC 3339, the last check
}

// Updater checks GitHub releases and installs a newer one.
type Updater interface {
	Status() UpdateStatus
	// Check looks for a newer release and returns the new status.
	Check(ctx context.Context) UpdateStatus
	// Apply starts downloading and installing the update in the
	// background; the app quits and restarts when it's ready.
	Apply() error
}

// SettingsStore keeps and applies the settings.
type SettingsStore interface {
	Settings() Settings
	// Apply checks, saves and applies new settings. Its error is shown
	// to the user as is.
	Apply(Settings) error
	// GameDataStatus says which game data is in use, or why there is none.
	GameDataStatus() string
}

// Summary is what the viewer needs to know after loading a replay.
type Summary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MapTitle  string `json:"mapTitle"`
	Duration  string `json:"duration"`
	Version   string `json:"version"`
	Approx    int    `json:"protocolApproxFrom,omitempty"`
	HasMap    bool   `json:"hasMap"`
	MapHow    string `json:"mapHow,omitempty"`
	MapError  string `json:"mapError,omitempty"`
	FromCache bool   `json:"fromCache"`
	// Fidelity is the tier the timeline was built at: tracker, command or engine.
	Fidelity string `json:"fidelity"`
	// CanDownload: the map is missing but Blizzard's depot has it.
	CanDownload bool     `json:"canDownload"`
	Players     []string `json:"players"`
}

func summarize(l *app.Loaded) Summary {
	s := Summary{
		ID: l.ID, Name: l.Name, MapTitle: l.Info.MapTitle, Duration: replay.FormatClock(l.Info.Loops),
		Version: l.Info.Version, Approx: l.Info.ProtocolApproxFrom, HasMap: l.Map != nil,
		MapHow: string(l.MapHow), MapError: l.MapErr, FromCache: l.FromCache, CanDownload: l.CanDownload(), Fidelity: l.Fidelity,
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
		writeJSON(w, map[string]any{
			"network":    s.App.Resolver != nil && s.App.Resolver.AllowNetwork,
			"openPaths":  s.OpenPaths,
			"pick":       s.PickReplay != nil,
			"pickFolder": s.PickFolder != nil,
			"settings":   s.Settings != nil,
			"update":     s.Updater != nil,
		})
	})
	mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) {
		if s.Updater == nil {
			httpError(w, http.StatusNotFound, errors.New("no updater here"))
			return
		}
		writeJSON(w, s.Updater.Status())
	})
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		if s.Updater == nil {
			httpError(w, http.StatusNotFound, errors.New("no updater here"))
			return
		}
		writeJSON(w, s.Updater.Check(r.Context()))
	})
	mux.HandleFunc("POST /api/update/apply", func(w http.ResponseWriter, r *http.Request) {
		if s.Updater == nil {
			httpError(w, http.StatusNotFound, errors.New("no updater here"))
			return
		}
		if err := s.Updater.Apply(); err != nil {
			httpError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, s.Updater.Status())
	})
	mux.HandleFunc("POST /api/pick-folder", func(w http.ResponseWriter, r *http.Request) {
		if s.PickFolder == nil {
			httpError(w, http.StatusNotFound, errors.New("no folder dialog here"))
			return
		}
		p, err := s.PickFolder()
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": p})
	})
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("PUT /api/settings", s.settings)
	mux.HandleFunc("GET /api/library", s.library)
	mux.HandleFunc("POST /api/library/open", s.libraryOpen)
	mux.HandleFunc("POST /api/open", s.open)
	mux.HandleFunc("POST /api/open-path", s.openPath)
	mux.HandleFunc("POST /api/pick", func(w http.ResponseWriter, r *http.Request) {
		if s.PickReplay == nil {
			httpError(w, http.StatusNotFound, errors.New("no file dialog here"))
			return
		}
		p, err := s.PickReplay()
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": p})
	})
	mux.HandleFunc("GET /api/r/{id}/timeline", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Timeline, "application/x-protobuf"
	}))
	mux.HandleFunc("GET /api/r/{id}/map", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Map, "application/json"
	}))
	mux.HandleFunc("GET /api/r/{id}/minimap", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Minimap, "image/png"
	}))
	mux.HandleFunc("GET /api/r/{id}/doodads", s.blob(func(l *app.Loaded) ([]byte, string) {
		return l.Doodads, "application/json"
	}))
	mux.HandleFunc("GET /api/r/{id}/analysis", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.App.Analysis(r.PathValue("id"))
		if err != nil {
			httpError(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
	mux.HandleFunc("POST /api/r/{id}/map", s.setMap)
	mux.HandleFunc("POST /api/r/{id}/download-map", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.App.DownloadMap(r.Context(), r.PathValue("id"))
		if err != nil {
			httpError(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, summarize(l))
	})
	mux.HandleFunc("POST /api/r/{id}/find-map", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.App.FindMap(r.Context(), r.PathValue("id"))
		if err != nil {
			httpError(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, summarize(l))
	})
	mux.HandleFunc("GET /api/startup", func(w http.ResponseWriter, r *http.Request) {
		if s.Startup == nil {
			writeJSON(w, nil)
			return
		}
		// Later loads may have replaced it (map picked, map downloaded).
		l := s.App.Get(s.Startup.ID)
		if l == nil {
			l = s.Startup
		}
		writeJSON(w, summarize(l))
	})
	mux.HandleFunc("GET /api/assets", func(w http.ResponseWriter, r *http.Request) {
		l := s.assets.Load()
		if l == nil {
			writeJSON(w, map[string]any{"available": false})
			return
		}
		writeJSON(w, map[string]any{"available": true, "build": l.Build()})
	})
	mux.HandleFunc("GET /api/assets/unit/{type}", s.asset("model/gltf-binary", func(l *assets.Library, t string) ([]byte, error) {
		return l.UnitGLB(t, false)
	}))
	mux.HandleFunc("GET /api/assets/death/{type}", s.asset("model/gltf-binary", func(l *assets.Library, t string) ([]byte, error) {
		return l.UnitGLB(t, true)
	}))
	mux.HandleFunc("GET /api/assets/icon/{type}", s.asset("image/png", (*assets.Library).IconPNG))
	mux.HandleFunc("GET /api/assets/terrain/{type}", s.asset("image/png", (*assets.Library).TerrainPNG))
	mux.HandleFunc("GET /api/r/{id}/ground", s.groundImage)
	mux.HandleFunc("GET /api/r/{id}/masks", s.masks)
	mux.HandleFunc("GET /api/r/{id}/cliffs", s.cliffs)
	mux.HandleFunc("GET /api/assets/cliff/{mesh}/{piece}", func(w http.ResponseWriter, r *http.Request) {
		v, _ := strconv.Atoi(r.URL.Query().Get("v"))
		mesh := r.PathValue("mesh")
		s.asset("model/gltf-binary", func(l *assets.Library, piece string) ([]byte, error) {
			return l.CliffGLB(mesh, piece, v)
		}, "piece")(w, r)
	})
	mux.HandleFunc("GET /api/assets/cliffmaterial/{type}", s.asset("model/gltf-binary", (*assets.Library).CliffMaterialGLB))
	mux.HandleFunc("GET /api/assets/model/{type}", func(w http.ResponseWriter, r *http.Request) {
		v, _ := strconv.Atoi(r.URL.Query().Get("v"))
		s.asset("model/gltf-binary", func(l *assets.Library, id string) ([]byte, error) {
			return l.ModelGLB(id, v)
		})(w, r)
	})
	if s.Static != nil {
		mux.Handle("/", http.FileServerFS(s.Static))
	}
	return mux
}

// asset serves one converted asset, named by the path value param ("type"
// when not given). Models are immutable for a game build, so the viewer may
// cache them.
func (s *Server) asset(mime string, get func(*assets.Library, string) ([]byte, error), param ...string) http.HandlerFunc {
	name := "type"
	if len(param) > 0 {
		name = param[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		l := s.assets.Load()
		if l == nil {
			httpError(w, http.StatusNotFound, errors.New("no game data: 3D models are unavailable"))
			return
		}
		b, err := get(l, r.PathValue(name))
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, assets.ErrNoModel) || errors.Is(err, os.ErrNotExist) || errors.Is(err, casc.ErrNotLocal) {
				code = http.StatusNotFound
			}
			httpError(w, code, err)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(b)
	}
}

// groundImage composes a replay's map ground once per map.
func (s *Server) groundImage(w http.ResponseWriter, r *http.Request) {
	lib := s.assets.Load()
	l := s.App.Get(r.PathValue("id"))
	if lib == nil || l == nil || l.MapFile() == nil {
		httpError(w, http.StatusNotFound, errors.New("no map or no game data"))
		return
	}
	m := l.MapFile()
	s.groundMu.Lock()
	b := s.ground[m]
	s.groundMu.Unlock()
	if b == nil {
		var err error
		if b, err = lib.GroundJPEG(m); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, assets.ErrNoModel) {
				code = http.StatusNotFound
			}
			httpError(w, code, err)
			return
		}
		s.groundMu.Lock()
		if s.ground == nil {
			s.ground = map[*mapfile.Map][]byte{}
		}
		s.ground[m] = b
		s.groundMu.Unlock()
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache") // as blob: depends on the map in use
	w.Write(b)
}

// masks serves the map's texture masks, for blending the ground textures
// on the GPU. They're map data, so no game data is needed.
func (s *Server) masks(w http.ResponseWriter, r *http.Request) {
	l := s.App.Get(r.PathValue("id"))
	if l == nil || l.MapFile() == nil || l.MapFile().Masks == nil {
		httpError(w, http.StatusNotFound, errors.New("no texture masks for this replay"))
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-cache") // as blob: depends on the map in use
	z, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	z.Write(l.MapFile().Masks.Planes())
	z.Close()
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

// settings reads (GET) or changes (PUT) the desktop app's settings.
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil {
		httpError(w, http.StatusNotFound, errors.New("no settings here"))
		return
	}
	if r.Method == http.MethodPut {
		var in Settings
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := s.Settings.Apply(in); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
	}
	cur := s.Settings.Settings()
	if cur.MapFolders == nil {
		cur.MapFolders = []string{}
	}
	writeJSON(w, struct {
		Settings
		GameDataStatus string `json:"gameDataStatus"`
	}{cur, s.Settings.GameDataStatus()})
}

// openPath opens a replay by its absolute path (desktop only).
func (s *Server) openPath(w http.ResponseWriter, r *http.Request) {
	if !s.OpenPaths {
		httpError(w, http.StatusNotFound, errors.New("opening by path is not enabled"))
		return
	}
	var req struct{ Path string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if !filepath.IsAbs(req.Path) || !strings.EqualFold(filepath.Ext(req.Path), ".SC2Replay") {
		httpError(w, http.StatusBadRequest, errors.New("not a replay path"))
		return
	}
	l, err := s.App.LoadFile(r.Context(), req.Path)
	if err != nil {
		code := http.StatusUnprocessableEntity
		if errors.Is(err, os.ErrNotExist) {
			code = http.StatusNotFound
		}
		httpError(w, code, err)
		return
	}
	writeJSON(w, summarize(l))
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
		// Not cached: these change when the replay's map is picked or
		// downloaded, and when ghostreel is upgraded, under the same id.
		w.Header().Set("Cache-Control", "no-cache")
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
