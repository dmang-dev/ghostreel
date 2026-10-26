// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dmang-dev/ghostreel/internal/casc"
	"github.com/dmang-dev/ghostreel/internal/gamedata"
	"github.com/dmang-dev/ghostreel/internal/mapfile"
)

const cascDir = "../../testdata/casc"

var (
	libOnce sync.Once
	lib     *Library
	libErr  error
)

func openLib(t *testing.T) *Library {
	t.Helper()
	if _, err := os.Stat(cascDir + "/.build.info"); err != nil {
		t.Skip("no game data in testdata/casc")
	}
	libOnce.Do(func() { lib, libErr = Open(cascDir, "") })
	if libErr != nil {
		t.Fatal(libErr)
	}
	return lib
}

// Neutral types common on ladder maps.
var neutrals = []string{
	"MineralField", "MineralField750", "LabMineralField", "LabMineralField750", "RichMineralField",
	"RichMineralField750", "VespeneGeyser", "RichVespeneGeyser", "SpacePlatformGeyser",
	"ProtossVespeneGeyser", "PurifierVespeneGeyser", "ShakurasVespeneGeyser", "XelNagaTower",
	"DestructibleRockEx1DiagonalHugeBLUR", "UnbuildableRocksDestructible",
}

func TestModelPaths(t *testing.T) {
	l := openLib(t)
	t.Logf("build %s, %d asset files, %d actors", l.Build(), len(l.files), len(l.actorOf))
	for _, u := range append(gamedata.Names(), neutrals...) {
		p, err := l.ModelPath(u, false)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		t.Logf("%-28s %s", u, p)
	}
}

// rigid models have no standing animation: the Interceptor has no skeletal
// animation at all, and the sieged tank rests in its bind pose (it only
// animates to fire).
var rigid = map[string]bool{"Interceptor": true, "SiegeTankSieged": true}

// hovers lists ground units that glide instead of walking.
var hovers = map[string]bool{"Disruptor": true, "DisruptorPhased": true}

// TestLadderModels is the Phase 3 exit gate: every ladder unit type converts
// to a model with a standing animation, and every mobile one can walk.
func TestLadderModels(t *testing.T) {
	l := openLib(t)
	if testing.Short() {
		t.Skip("short")
	}
	var missing []string
	defer func() {
		if len(missing) > 0 {
			t.Logf("not verifiable, model files absent from this game data: %v", missing)
		}
	}()
	for _, u := range gamedata.Names() {
		b, err := l.UnitGLB(u, false)
		if errors.Is(err, casc.ErrNotLocal) {
			missing = append(missing, u)
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		g, err := parseGLB(b)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		info := gamedata.Stub{}.Unit(0, u)
		if !g.hasAnim("Stand") && !rigid[u] {
			t.Errorf("%s: no Stand animation (%v)", u, g.animNames())
		}
		if info.Speed > 0 && info.Class != gamedata.ClassBuilding && !info.Air && !hovers[u] && !g.hasAnim("Walk") {
			t.Errorf("%s: moves but has no Walk animation (%v)", u, g.animNames())
		}
		t.Logf("%-24s %7d bytes, %d textures, anims %v", u, len(b), g.textures, g.animNames())
	}
}

func TestTerrainTextures(t *testing.T) {
	l := openLib(t)
	var ok int
	ids := []string{"AiurTempleJungle6", "CharRock", "Elsecaro1", "ShakurasSandDark", "AgriaRock", "KorhalCity6"}
	for _, id := range ids {
		b, err := l.TerrainPNG(id)
		if errors.Is(err, casc.ErrNotLocal) {
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		ok++
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		// Opaque, so browsers don't premultiply the colors away.
		r := img.Bounds()
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
					t.Fatalf("%s: alpha %d at %d,%d", id, a>>8, x, y)
				}
			}
		}
		t.Logf("%s: %dx%d, %d bytes", id, r.Dx(), r.Dy(), len(b))
	}
	if ok == 0 {
		t.Error("no terrain texture converted")
	}
}

func TestGround(t *testing.T) {
	l := openLib(t)
	paths, _ := filepath.Glob("../../testdata/maps/*.SC2Map")
	if len(paths) == 0 {
		t.Skip("no maps in testdata/maps")
	}
	for _, p := range paths {
		m, err := mapfile.Open(p)
		if err != nil || m.Masks == nil {
			continue
		}
		start := time.Now()
		b, err := l.GroundJPEG(m)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		t.Logf("%s: %d KiB in %v", filepath.Base(p), len(b)/1024, time.Since(start).Round(time.Millisecond))
		if out := os.Getenv("GHOSTREEL_GROUND_OUT"); out != "" {
			os.WriteFile(filepath.Join(out, filepath.Base(p)+".jpg"), b, 0o644)
		}
	}
}
