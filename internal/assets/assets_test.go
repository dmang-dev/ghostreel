// SPDX-License-Identifier: Apache-2.0

package assets

import (
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/OWNER/ghostreel/internal/casc"
	"github.com/OWNER/ghostreel/internal/gamedata"
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
