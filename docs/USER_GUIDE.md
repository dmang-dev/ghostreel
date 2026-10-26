# ghostreel user guide

ghostreel plays StarCraft II replays back without the game: units, buildings, deaths, supply and income on the map's terrain, in 2D or 3D, scrubbable to any moment. It is not affiliated with Blizzard Entertainment.

## Getting it

Pick one of three ways to run it. All of them read the same replays and show the same viewer.

| | Desktop app | In the browser | Command line (`ghostreel serve`) |
| --- | --- | --- | --- |
| Install | Installer (Windows), app (macOS), binary (Linux) | Nothing; open the page | Unzip and run |
| Finds maps by itself | Yes | No: drop the map file | Yes |
| Game models in 3D | Yes, with the game installed | No (shapes) | Yes, with the game installed |
| Lists your replay folders | Yes | No | With `--replays` |
| Exact positions from `ghostreel-engine` | Yes | No | Yes |

Downloads are on the project's releases page. The builds are not code-signed yet, so Windows SmartScreen may ask "Run anyway?", and on macOS you right-click the app and choose **Open** the first time.

### Desktop app

- **Windows:** run `ghostreel-desktop_…_setup.exe`. It associates `.SC2Replay` files, so double-clicking a replay opens it. A portable `.zip` is there too.
- **macOS:** unzip and move `ghostreel.app` to Applications.
- **Linux:** unpack the `.tar.gz` and run `ghostreel-desktop`. It needs GTK 3 and WebKitGTK 4.0 (`libwebkit2gtk-4.0`).

Open a replay with **Open replay…**, by dropping it on the window, or from the **Library** menu, which lists your game's replay folders.

### In the browser

The browser build runs entirely on your machine. The replay is decoded in the page and is never uploaded anywhere. Drop a `.SC2Replay` on the page, then drop the map file to get terrain (see [Maps](#maps)). Anyone can host it: it is a folder of static files (`ghostreel_…_web.zip`), and the server only has to serve `.wasm` files as `application/wasm`.

Long games take longer in the browser than in the app: the 30-minute games we tested take about a minute after the map is added, while a typical ladder game takes a few seconds.

### Command line

```bash
ghostreel serve --replays "/path/to/StarCraft II/Accounts/…/Replays/Multiplayer"
```

Then open <http://127.0.0.1:8080/>. Other commands: `ghostreel info`, `dump-timeline`, `dump-map`, `accuracy`, `assets`, `version`; run `ghostreel` for the list.

## What you're seeing

A replay stores what players clicked, not where units were, so movement is reconstructed. Births, deaths, buildings, upgrades and player stats are always exact. The badge at the top says how positions were filled in:

- **Command tier** (the default): units follow their players' orders along the map's paths. They mine, walk to rally points, stop at enemies on attack-move, and use Stimpack and creep speed. The estimate snaps to the real position whenever the replay recorded one. Players without recorded orders (the built-in AI) fall back to the tracker tier.
- **Tracker tier:** positions are known only when the replay sampled them (about every 11 s, mostly in fights) and at deaths. In between, units hold their last known position. This is used when the map is missing.
- **Engine tier:** exact positions, recorded by playing the replay in the real game with `ghostreel-engine`. Keep the resulting `.engine.pb.gz` next to the replay.

Click a unit to inspect it; the panel says when its position was last actually observed.

## Controls

| Key | Action |
| --- | --- |
| Space / P | Play / pause |
| ← / → | Back / forward 5 s (Shift: 30 s) |
| + / − | Faster / slower (1× is real time at Faster speed) |
| Home / End | Start / end |
| Click | Inspect a unit; drag to pan, scroll to zoom |
| Esc | Clear the selection |
| 3 | 2D / 3D (in 3D, right-drag or Shift-drag to rotate and tilt) |
| V | Fog of war: cycle through each player's vision |
| B | Events / build order / battles |
| D | Director camera on or off |
| L / M / F | Labels / map art / frame-rate counter |

The minimap moves the camera when clicked. Bars under the scrubber show deaths; yellow bands are battles; ticks at the top are upgrades (short) and expansions (long).

## Analysis: build orders, battles, economy

Press **B** (or use the tabs on the events panel) to switch between:

- **Build order:** each player's structures, units, morphs and research for the first ten minutes, with supply. Structures appear when placed, units and research when finished (replays don't record when they were queued). Repeats a moment apart share a line ("Zergling ×6").
- **Battles:** clusters of deaths close together in time and space, with each side's losses. They are also shown as yellow bands under the scrubber and as rings on the minimap while they happen.
- **Economy:** charts of income, workers, army value, supply and resources lost for each player, with a line at the current moment. Click a chart to jump there.
- **Expansions:** when and where each player took a new base, and when it finished.
- **Supply blocks:** when each player had no free supply. These are accurate to about 10 s, the interval at which the replay records stats.

Click any row to jump there. These come from analysis plugins; see [CONTRIBUTING.md](../CONTRIBUTING.md) to write your own.

## Maps

The desktop app and `ghostreel serve` look for a replay's map in this order:

1. A map file you pick (**Pick map file…**, or `--map` on the command line).
2. The Battle.net cache, where the game keeps every map you've played: `C:\ProgramData\Blizzard Entertainment\Battle.net\Cache` on Windows, `/Users/Shared/Blizzard/Battle.net/Cache` on macOS.
3. Maps ghostreel downloaded before.
4. For local and bot games: your SC2 `Maps` folder, and folders in `--maps` / `GHOSTREEL_MAPS`.

If none has it, the replay plays on a blank grid. For ladder and custom maps, **Download from Blizzard** fetches the map from Blizzard's map server and checks it against the hash in the replay. Nothing is downloaded unless you click it.

In the browser, none of that is possible: browsers can't read your Battle.net cache, and Blizzard's map server doesn't allow web pages to fetch from it. Drop the map file after the replay instead. For ladder maps it is the `.s2ma` in the Battle.net cache. The **Where ghostreel looked** details name the file the replay expects.

## Hotspots, the director, and video export

**Hotspots** (a tab in the events panel) are the moments worth watching: battles, army wipes, worker raids, bases destroyed, high-value kills, loaded transports shot down, siege lines broken, fights in two places at once, first blood and the end. Each has a tier, **major** or **minor**: major means big in resources lost or among the game's best moments.

**Director** (button, or **D**) puts the camera on autopilot. It moves to each hotspot a few seconds before it starts, with a caption saying what's happening (gold bar for major, blue for minor). Between hotspots it follows wherever units are dying, then each player's main army in turn. Drag or zoom to take the camera back. The director follows the replay clock, so it works at any speed and while scrubbing.

**Export video** saves an MP4 of the game as you see it: terrain, units, labels, an overlay with each player's supply, income and army, the clock, the minimap, and the director's captions.

- **Highlight reel:** the top 5, 8 or 12 hotspots that don't overlap, each with a title card and a few seconds before and after.
- **Whole game:** start to finish.
- **Options:** speed 1×–8× (1× is real time), 720p or 1080p, and the director's camera or the current view.

The video is made in the app, frame by frame, as fast as your computer can draw it, and then saved as a download. A graphics card helps; on a machine without one it managed about 11 frames a second, roughly a third of real time at 30 fps. It needs WebCodecs, which Chrome, Edge, the Windows app and (recent) macOS have. The Linux app's WebKitGTK doesn't, so on Linux use `ghostreel serve` with Chrome or the browser build.

## 3D

Press **3**. With StarCraft II installed on the same computer, units are the game's own models with their animations, and the map shows its rocks, plants and wreckage, and the ground painted with the map's own textures. The terrain menu calls this **Ground textures**, and it works in 2D too. How sharp the ground looks depends on your install: a full Windows or macOS install has the game's full-size textures, while slimmed-down copies (such as Blizzard's Linux package) have only small versions, which look soft up close. Zerg creep shows as a purple layer around hatcheries and creep tumors; it is an estimate of how far it spread. The models are converted from your install the first time they're needed and kept in ghostreel's cache folder; nothing is downloaded or shared. If the game is installed somewhere unusual, point to the folder that contains `.build.info`, using `--gamedata` or the `GHOSTREEL_GAMEDATA` environment variable. To convert everything up front and see what's missing, run `ghostreel assets`.

Without the game (and in the browser build), units are plain shapes in team colors, which is also what you see while a model loads and when zoomed far out.

Known gaps: cliffs are plain walls rather than the game's cliff models, and there is no water.

## Privacy

ghostreel makes no network requests unless you ask for a map download. The server only listens on your own computer by default. Replays, maps and converted models stay in your local cache:

| OS | Cache |
| --- | --- |
| Windows | `%LocalAppData%\ghostreel` |
| macOS | `~/Library/Caches/ghostreel` |
| Linux | `~/.cache/ghostreel` |

Delete that folder to clear it.

## Troubleshooting

- **"Decoded with the protocol of build …"**: the replay is from a game version newer than ghostreel knows. It is decoded with the nearest older version, which usually works. Update ghostreel when a new version is out.
- **Units stand still:** the replay is on the tracker tier (no map, or AI players). Add the map.
- **The map doesn't match:** a map of the right name but a different size was found. Pick the right file.
- **3D shows shapes only:** no game installation was found. Check the server's log line "3D models from …", or set `--gamedata`.
