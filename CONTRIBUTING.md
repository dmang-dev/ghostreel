# Contributing to ghostreel

Thanks for helping. This guide covers setting up, the rules that keep the project shippable, and how to add an analysis plugin.

## Ground rules

- **No Blizzard assets in the repository, ever.** No replays, maps, models, textures, icons, sounds or game data files. That includes test fixtures and screenshots that show map art. Tests read real files from the git-ignored `testdata/` folder and skip when they're absent. Assets are read from the user's own install at run time.
- **Don't put "StarCraft" in the project's name or branding.** Describing what the tool reads is fine.
- **The Go core stays cgo-free.** It also compiles to WebAssembly for the browser build; CI checks `GOOS=js GOARCH=wasm`. Native and UI dependencies belong in `desktop/`, a separate module.
- **User-facing times use the in-game clock** (loops ÷ 22.4).
- **Network access is opt-in.** Nothing contacts Blizzard's servers unless the user asks for a map download.
- Code is Apache-2.0; by contributing you agree to license your work under it. Ported or vendored code needs a compatible license (MIT, BSD, Apache) and a credit in its package docs. GPL code can be used as format *documentation* but must not be copied (see `internal/m3`).

## Setup

You need Go (the version in `go.mod`), Node 20+, and for regenerating protobuf code [`buf`](https://buf.build) and `protoc-gen-go`. For the desktop app you also need the [Wails v2 CLI](https://wails.io) (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0`), and on Linux GTK 3 and WebKitGTK 4.0 development packages.

```bash
cd viewer && npm ci && cd ..
make build                    # the CLI
make test                     # Go tests (make test-short skips the slow ones)
cd viewer && npm test         # viewer tests, checked against golden output from Go
```

To run the viewer with hot reload, start `go run ./cmd/ghostreel serve` in one terminal and `cd viewer && npm run dev` in another. Vite proxies `/api` to the server.

### Test data

Put your own files in `testdata/` (git-ignored):

| Folder | What | Used by |
| --- | --- | --- |
| `testdata/replays/` | `.SC2Replay` files | replay, timeline, analysis, server tests |
| `testdata/replays/engine/` | replays with `.engine.pb.gz` recordings | accuracy tests |
| `testdata/maps/` | `.SC2Map` files | map and command-tier tests |
| `testdata/casc/` | a game install's `SC2Data` folder and `.build.info` | CASC, model and asset tests |

Tests that need them say so in their skip message. Golden files (`viewer/src/testdata`) are regenerated with `GHOSTREEL_UPDATE_GOLDEN=1 go test ./internal/timeline -run Golden`.

## Where things are

| Path | What |
| --- | --- |
| `internal/replay` | Replay decoding (via s2prot) into simple events |
| `internal/timeline` | The timeline format: building, seeking, simplifying |
| `internal/command`, `internal/pathing`, `internal/selection` | The command tier |
| `internal/engine`, `cmd/ghostreel-engine` | The engine-tier exporter |
| `internal/mapfile`, `internal/mapcache` | Map decoding and lookup |
| `internal/casc`, `internal/m3`, `internal/dds`, `internal/assets` | Game data, models and textures from the local install |
| `internal/analysis` | Analysis plugins (build orders, battles, economy, APM, production, army, supply blocks…) |
| `internal/app`, `internal/server` | The service and HTTP API every frontend uses |
| `cmd/ghostreel-wasm` | The same API compiled to WebAssembly for the browser build |
| `viewer/` | TypeScript + three.js viewer |
| `desktop/` | Wails desktop app (its own Go module) |
| `proto/` | The timeline schema ([docs/TIMELINE_FORMAT.md](docs/TIMELINE_FORMAT.md)) |

Design history and the reasons behind choices are in [docs/DECISIONS.md](docs/DECISIONS.md). Please add to it when you make a choice others would wonder about.

## Changing things

- **The timeline format:** edit `proto/timeline/v1/timeline.proto`, run `make proto`, and keep `viewer/src/model.ts` in step with `internal/timeline/seek.go` (the golden tests enforce it). Bump `CacheVersion` in `internal/app` whenever builders' output changes, so cached timelines are rebuilt. Incompatible changes bump `SchemaVersion`.
- **Accuracy work on the command tier:** measure with `ghostreel accuracy` against engine recordings and keep a change only if the error goes down. Record the numbers in DECISIONS.md.
- **The viewer:** it talks to the core only through `/api/...` (`viewer/src/api.ts`). New endpoints go in `internal/server` so all three frontends get them.
- **Style:** `gofmt` and `go vet` clean; `npm run typecheck` clean. Match the surrounding code: comments explain *why*, names are plain words.
- **Commits:** small and focused, with a subject line under about 60 characters and a body that says why.

## Writing an analysis plugin

Analysis plugins read a finished timeline and report what they find. The viewer draws every report without knowing the plugin:

- **markers** (a moment or span, optionally a place) go under the scrubber and on the minimap;
- **tables** go in the events panel as a tab, and clicking a row seeks to it;
- **series** (values over time) are drawn as charts, one line per player.

A plugin implements `analysis.Plugin` and registers itself:

```go
package expansions

import (
	"fmt"

	pb "github.com/dmang-dev/ghostreel/gen/timeline/v1"
	"github.com/dmang-dev/ghostreel/internal/analysis"
)

func init() { analysis.Register(Plugin{}) }

type Plugin struct{}

func (Plugin) ID() string { return "expansions" }

func (Plugin) Analyze(tl *pb.Timeline) (*analysis.Report, error) {
	rep := &analysis.Report{Title: "Expansions"}
	t := analysis.Table{Title: "Expansions", Columns: []string{"Time", "Player", "Type"}}
	for _, u := range tl.Units {
		name := tl.UnitTypes[u.Type].Name
		if u.BornLoop > 0 && (name == "Nexus" || name == "Hatchery" || name == "CommandCenter") {
			secs := int(float64(u.BornLoop) / 22.4) // the in-game clock
			when := fmt.Sprintf("%d:%02d", secs/60, secs%60)
			t.Rows = append(t.Rows, analysis.Row{Loop: u.BornLoop, Cells: []string{when, fmt.Sprint(u.Owner), name}})
		}
	}
	rep.Tables = []analysis.Table{t}
	return rep, nil
}
```

Include it in a build by importing the package for its side effect, for example in `internal/app`: `import _ "github.com/dmang-dev/ghostreel/internal/analysis/expansions"`. Then:

- Keep `Analyze` pure. Don't modify the timeline, and don't touch the network or the file system (plugins also run in the browser).
- A plugin that returns an error or panics shows up as a failed tab; the others still run.
- Add a test with a small hand-built timeline (see `internal/analysis/analysis_test.go`), and log output on real replays.

The built-in `buildorder.go` and `battles.go` are worked examples.

## Releases

Push a tag `vX.Y.Z`. `.github/workflows/release.yml` then builds:

- CLI archives for Windows, macOS (Intel and Apple Silicon) and Linux (x86-64 and ARM), with the viewer;
- the browser build;
- the desktop app: a Windows installer plus a portable zip, a macOS universal app, and a Linux binary.

It publishes them as a GitHub release with SHA-256 checksums and `latest.json`, the update manifest the desktop app reads. A tag with a suffix (`v1.3.0-rc1`) makes a pre-release, which installed apps don't offer; use one to try a build before everyone gets it. Running the workflow by hand (Actions → release → Run workflow) only builds the artifacts. The builds are not code-signed; adding signing needs certificates held by maintainers.

### Update signing

`latest.json` lists each file's size and SHA-256 and is signed (`latest.json.sig`, Ed25519). The app accepts a manifest only if the signature matches `update.PublicKey` (`internal/update`), and a download only if it matches the manifest. The private key is the repository secret `GHOSTREEL_UPDATE_KEY`; without it the release still goes out, with a warning, but apps won't see it.

- Make a key: `go run ./cmd/releasetool keygen -out <file outside the repo>`. It prints the public key, which goes in `update.PublicKey`. Keep the file somewhere safe, and set the secret with `gh secret set GHOSTREEL_UPDATE_KEY < <file>`.
- Changing the key means apps built with the old public key can't verify new releases; their users have to download one new version by hand.
- The updater reads `https://github.com/dmang-dev/ghostreel/releases/latest/download/latest.json` without signing in, so it only works once the repository is public. `GHOSTREEL_UPDATE_URL` points an app at another manifest for testing.
