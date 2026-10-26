// SPDX-License-Identifier: Apache-2.0

// Package assets turns the player's own game data into what the 3D viewer
// draws: a unit type name ("Marine") is resolved through the GameData XML
// (actor → model → .m3 file), converted to glTF with its textures and
// animations, and cached on disk. Nothing extracted here is redistributed;
// the cache lives on the player's machine next to the timelines.
package assets

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/dmang-dev/ghostreel/internal/casc"
	"github.com/dmang-dev/ghostreel/internal/dds"
	"github.com/dmang-dev/ghostreel/internal/m3"
)

// Version is part of the cache path; bump it when the conversion changes.
const Version = "a2"

// mods are the dependencies of a ladder map, lowest priority first.
var mods = []string{"core", "liberty", "libertymulti", "swarm", "swarmmulti", "void", "voidmulti"}

// ErrNoModel is returned for unit types without a drawable model.
var ErrNoModel = errors.New("assets: no model for this unit type")

// Library reads models from a game installation. It is safe for concurrent
// use.
type Library struct {
	st       *casc.Storage
	build    string
	cacheDir string
	files    map[string]string // "assets/units/.../x.m3" (lower case) → storage name
	actors   *catalog
	models   *catalog
	buttons  *catalog
	terrain  *catalog          // CTerrainTex: terrain texture layers
	actorOf  map[string]string // unit type → actor id

	mu      sync.Mutex
	pending map[string]*result
}

type result struct {
	done chan struct{}
	data []byte
	err  error
}

// Open reads the game data at installDir (the folder with .build.info).
// Converted models are cached under cacheDir when it is not empty.
func Open(installDir, cacheDir string) (*Library, error) {
	st, err := casc.Open(installDir)
	if err != nil {
		return nil, err
	}
	if err := st.LoadNames(); err != nil {
		st.Close()
		return nil, err
	}
	l := &Library{
		st: st, cacheDir: cacheDir, files: map[string]string{},
		actors: newCatalog(), models: newCatalog(), buttons: newCatalog(), terrain: newCatalog(),
		actorOf: map[string]string{}, pending: map[string]*result{},
	}
	l.build = strings.Fields(st.Config["build-name"] + " unknown")[0]
	prio := map[string]int{}
	for i, m := range mods {
		prio["mods/"+m+".sc2mod/base.sc2assets"] = i + 1
	}
	best := map[string]int{}
	for _, name := range st.Names() {
		i := strings.Index(name, "/assets/")
		if i < 0 {
			continue
		}
		p := prio[name[:i]]
		if p == 0 {
			continue
		}
		key := name[i+1:]
		if p > best[key] {
			best[key], l.files[key] = p, name
		}
	}
	for _, m := range mods {
		dir := "mods/" + m + ".sc2mod/base.sc2data/gamedata/"
		for _, f := range []struct {
			file string
			c    *catalog
			keep func(string) bool
		}{
			{"actordata.xml", l.actors, func(c string) bool { return c == "CActorUnit" }},
			{"modeldata.xml", l.models, func(c string) bool { return c == "CModel" }},
			{"buttondata.xml", l.buttons, func(c string) bool { return c == "CButton" }},
			{"terraintexdata.xml", l.terrain, func(c string) bool { return c == "CTerrainTex" }},
		} {
			b, err := st.ReadFile(dir + f.file)
			if err != nil {
				continue // not every mod has every file
			}
			if err := f.c.add(b, f.keep); err != nil {
				st.Close()
				return nil, fmt.Errorf("assets: %s%s: %w", dir, f.file, err)
			}
		}
	}
	for _, e := range l.actors.entries {
		if e.id == "" || e.isDefault {
			continue
		}
		unit := e.id
		if u := e.tokens["unitName"]; u != "" && !strings.Contains(u, "##") {
			unit = u
		}
		// Prefer the actor named after the unit when several claim it.
		if _, taken := l.actorOf[unit]; !taken || e.id == unit {
			l.actorOf[unit] = e.id
		}
	}
	// Morphed forms (BarracksFlying, RoachBurrowed) usually reuse the base
	// unit's actor, which says so in its creation events.
	created := map[string]string{}
	for _, e := range l.actors.entries {
		if e.id == "" || e.isDefault {
			continue
		}
		for _, unit := range l.actors.createdBy("CActorUnit", e.id) {
			if _, direct := l.actorOf[unit]; !direct {
				if prev, ok := created[unit]; !ok || e.id < prev { // deterministic
					created[unit] = e.id
				}
			}
		}
	}
	for unit, actor := range created {
		l.actorOf[unit] = actor
	}
	return l, nil
}

// Close releases the game data files.
func (l *Library) Close() error { return l.st.Close() }

// Build is the game data's build name, e.g. "B75689".
func (l *Library) Build() string { return l.build }

// modelID returns the CModel id drawn for a unit type: its actor's model,
// or the unit's own name when no actor says otherwise. death selects the
// actor's normal death model instead.
func (l *Library) modelID(unit string, death bool) (string, bool) {
	actor, ok := l.actorOf[unit]
	if death {
		if !ok {
			return "", false
		}
		return l.actors.field("CActorUnit", actor, "DeathArray[Normal].ModelLink")
	}
	if ok {
		if id, set := l.actors.field("CActorUnit", actor, "Model"); set && id != "" && !strings.Contains(id, "##") {
			return id, true
		}
		return actor, true
	}
	return unit, l.models.has("CModel", unit)
}

// file finds an asset by its path as written in the XML or a model
// ("Assets\Units\Terran\Marine\Marine.m3"), trying the variation and
// low-detail names the game falls back to.
func (l *Library) file(path string) (string, bool) {
	key := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	key = strings.TrimPrefix(key, "/")
	if n, ok := l.files[key]; ok {
		return n, true
	}
	if strings.HasSuffix(key, ".m3") {
		if n, ok := l.files[strings.TrimSuffix(key, ".m3")+"_00.m3"]; ok {
			return n, true
		}
	}
	return "", false
}

// modelFile returns the storage name of a CModel's .m3 file; variation picks
// one of the numbered files of models that have several.
func (l *Library) modelFile(id string, variation int) (string, error) {
	path, ok := l.models.field("CModel", id, "Model")
	if !ok || path == "" || strings.Contains(path, "##") {
		return "", fmt.Errorf("%w (model %q has no file)", ErrNoModel, id)
	}
	if variation > 0 {
		if name, ok := l.file(strings.TrimSuffix(path, ".m3") + fmt.Sprintf("_%02d.m3", variation)); ok {
			return name, nil
		}
	}
	name, ok := l.file(path)
	if !ok {
		return "", fmt.Errorf("%w (%s is not in the game data)", ErrNoModel, path)
	}
	return name, nil
}

// ModelPath returns the storage name of a unit type's .m3 file.
func (l *Library) ModelPath(unit string, death bool) (string, error) {
	id, ok := l.modelID(unit, death)
	if !ok || id == "" {
		return "", ErrNoModel
	}
	return l.modelFile(id, 0)
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// ModelGLB returns a model by its CModel id (map doodads name models
// directly) as binary glTF, converting it on first use.
func (l *Library) ModelGLB(id string, variation int) ([]byte, error) {
	if !safeName.MatchString(id) || variation < 0 || variation > 99 {
		return nil, ErrNoModel
	}
	key := fmt.Sprintf("model.%s.%d", id, variation)
	return l.cached(key, func() (string, []string, error) {
		name, err := l.modelFile(id, variation)
		return name, l.models.list("CModel", id, "RequiredAnims"), err
	})
}

// UnitGLB returns a unit type's model (or its death model) as binary glTF,
// converting it on first use.
func (l *Library) UnitGLB(unit string, death bool) ([]byte, error) {
	if !safeName.MatchString(unit) {
		return nil, ErrNoModel
	}
	key := unit
	if death {
		key += ".death"
	}
	return l.cached(key, func() (string, []string, error) {
		name, err := l.ModelPath(unit, death)
		if err != nil {
			return "", nil, err
		}
		id, _ := l.modelID(unit, death)
		return name, l.models.list("CModel", id, "RequiredAnims"), nil
	})
}

// cached converts a model once, keeping the result on disk under key.
// resolve names the .m3 file and the animation files it requires.
func (l *Library) cached(key string, resolve func() (string, []string, error)) ([]byte, error) {
	return l.once(key, func() ([]byte, error) {
		cache := ""
		if l.cacheDir != "" {
			cache = filepath.Join(l.cacheDir, "assets", l.build+"-"+Version, "models", key+".glb")
			if b, err := os.ReadFile(cache); err == nil {
				return b, nil
			}
		}
		name, anims, err := resolve()
		if err != nil {
			return nil, err
		}
		b, err := l.convert(name, anims)
		if errors.Is(err, m3.ErrNoGeometry) {
			return nil, fmt.Errorf("%w (%s has no geometry)", ErrNoModel, name)
		}
		if err != nil {
			return nil, fmt.Errorf("assets: %s: %w", name, err)
		}
		if cache != "" {
			if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
				os.WriteFile(cache, b, 0o644)
			}
		}
		return b, nil
	})
}

func (l *Library) once(key string, f func() ([]byte, error)) ([]byte, error) {
	l.mu.Lock()
	r := l.pending[key]
	if r == nil {
		r = &result{done: make(chan struct{})}
		l.pending[key] = r
		l.mu.Unlock()
		r.data, r.err = f()
		close(r.done)
	} else {
		l.mu.Unlock()
		<-r.done
	}
	return r.data, r.err
}

// maxTexture bounds texture size in the exported models (pixels per side).
const maxTexture = 512

// skipSequence drops animations a replay never shows.
var skipSequence = regexp.MustCompile(`(?i)dance|victory|cheer|portrait|talk|fidget|cinematic`)

// convert reads a model and the animation files it requires and converts
// them to glTF.
func (l *Library) convert(name string, anims []string) ([]byte, error) {
	b, err := l.st.ReadFile(name)
	if err != nil {
		return nil, err
	}
	m, err := m3.Parse(b)
	if err != nil {
		return nil, err
	}
	for _, path := range anims {
		an, ok := l.file(path)
		if !ok {
			continue
		}
		ab, err := l.st.ReadFile(an)
		if err != nil {
			continue
		}
		a, err := m3.Parse(ab)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", an, err)
		}
		m.MergeAnimations(a)
	}
	return m.GLB(m3.GLBOptions{
		Texture:   l.texture,
		Sequences: func(s string) bool { return !skipSequence.MatchString(s) },
	})
}

// texture decodes a texture. Installations without full-quality textures
// (the Linux headless build) still ship the low-detail .lvl0 copy.
func (l *Library) texture(path string) image.Image {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	for _, cand := range []string{base + ".dds", base + ".lvl0"} {
		name, ok := l.file(cand)
		if !ok {
			continue
		}
		b, err := l.st.ReadFile(name)
		if err != nil {
			continue
		}
		if img, err := dds.Decode(b, maxTexture); err == nil {
			return img
		}
	}
	return nil
}

// TerrainPNG returns a terrain texture layer (a CTerrainTex id, as a map's
// t3Terrain.xml names them) as an opaque PNG. The alpha channel holds
// something other than coverage, and browsers that premultiply it would
// darken the colors.
func (l *Library) TerrainPNG(id string) ([]byte, error) {
	if !safeName.MatchString(id) {
		return nil, ErrNoModel
	}
	path, ok := l.terrain.field("CTerrainTex", id, "Texture")
	if !ok || path == "" {
		return nil, ErrNoModel
	}
	img := l.texture(path)
	if img == nil {
		return nil, fmt.Errorf("%w (%s is not in the game data)", ErrNoModel, path)
	}
	rgba := image.NewNRGBA(img.Bounds()) // a copy: img may be shared
	draw.Draw(rgba, rgba.Rect, img, img.Bounds().Min, draw.Src)
	for i := 3; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i] = 255
	}
	var out bytes.Buffer
	if err := png.Encode(&out, rgba); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (l *Library) encodePNG(path string) ([]byte, error) {
	img := l.texture(path)
	if img == nil {
		return nil, fmt.Errorf("%w (%s is not in the game data)", ErrNoModel, path)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// IconPNG returns a unit type's command-card icon as PNG.
func (l *Library) IconPNG(unit string) ([]byte, error) {
	if !safeName.MatchString(unit) {
		return nil, ErrNoModel
	}
	path, ok := l.buttons.field("CButton", unit, "Icon")
	if !ok || path == "" {
		return nil, ErrNoModel
	}
	return l.encodePNG(path)
}

// FindInstall returns the game installation folder (the one holding
// .build.info): $GHOSTREEL_GAMEDATA, else the platform's default install
// location. It returns "" when none is found.
func FindInstall() string {
	cands := filepath.SplitList(os.Getenv("GHOSTREEL_GAMEDATA"))
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if p := os.Getenv(env); p != "" {
				cands = append(cands, filepath.Join(p, "StarCraft II"))
			}
		}
	case "darwin":
		cands = append(cands, "/Applications/StarCraft II")
	default:
		if home, err := os.UserHomeDir(); err == nil {
			cands = append(cands, filepath.Join(home, "StarCraftII"))
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(filepath.Join(c, ".build.info")); err == nil {
			return c
		}
	}
	return ""
}
