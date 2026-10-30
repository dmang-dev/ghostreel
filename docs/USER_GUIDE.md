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

The browser build runs entirely on your machine. The replay is decoded in the page and is never uploaded anywhere. Drop a `.SC2Replay` on the page, then drop the map file to get terrain (see [Maps](#maps)). Anyone can host it: it is a folder of static files (`ghostreel_…_web.zip`) that needs no server setup. The page downloads the core compressed (about 3.4 MB) and unpacks it itself.

Long games take longer in the browser than in the app: the 30-minute games we tested take about a minute after the map is added, while a typical ladder game takes a few seconds.

### Command line

```bash
ghostreel serve --replays "/path/to/StarCraft II/Accounts/…/Replays/Multiplayer"
```

Then open <http://127.0.0.1:8080/>. Other commands: `ghostreel info`, `dump-timeline`, `dump-map`, `accuracy`, `assets`, `version`; run `ghostreel` for the list.

## What you're seeing

A replay stores what players clicked, not where units were, so movement is reconstructed. Births, deaths, buildings, upgrades and player stats are always exact. The badge at the top says how positions were filled in:

- **Command tier** (the default): units follow their players' orders along the map's paths. They mine, walk to rally points, stop at enemies on attack-move, and use Stimpack and creep speed. The estimate snaps to the real position whenever the replay recorded one. Players without recorded orders (the built-in AI) fall back to the tracker tier; their workers are drawn mining at the base they stand by (which field is a guess).
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
| H | Health bars: damaged units, all units, none |
| C | Follow each player's camera in turn, then none |
| I | About this game: the map, when it was played, the players |

The minimap moves the camera when clicked. Bars under the scrubber show deaths; yellow bands are battles; ticks at the top are upgrades (short) and expansions (long).

## Game details

**Info** (or **I**) shows what the replay and map say about the game: the map's title, description and picture, when and where the game was played (ladder or custom, region), observers, and for each player their race (and whether they picked Random), result, APM, MMR, handicap, Battle.net profile, and for computer players their difficulty and build.

The event feed says who killed what ("Marine killed by Siege Tank"). The inspect panel shows a unit's kills so far, what killed it, and what made it (the Barracks that trained it, a Calldown MULE); click a named unit to select it.

The minimap shows where each player was looking, as a dashed box in their color, and pings as rings. **C** makes the view follow a player's camera; drag or zoom to take over again. Computer players and bots have no camera.

On the map, Xel'Naga towers show their vision range, solid in a player's color while one of their units holds the tower. Inhibitor and acceleration zones show as tinted circles (their sizes are approximate).

With StarCraft II installed, ghostreel also uses the game's own names ("Siege Tank", "Infantry Weapons Level 1") and shows each unit's stats in the inspect panel: health, shields, armor, cost, build time, attributes, and each weapon's damage, bonuses, range and cooldown. The selected unit's weapon ranges show as red dashed rings. Mineral fields and geysers show how much they held at the start. The stats are those of the installed game, which may differ from the version the replay was played on. Without the game, names are spaced-out ids ("Siege Tank Sieged") and there are no stats.

## Analysis: build orders, battles, economy, APM

Press **B** (or use the tabs on the events panel) to switch between:

- **APM:** each player's actions per minute over the game, and commands alone, as charts (each point is the rate over the minute before it), with each player's average for the whole game below. Actions are commands, selection changes and control-group changes; camera moves don't count. Bots make thousands of actions a minute, and the built-in AI records none, so it has no line. APM needs the replay's game events, which the tracker tier skips: on the tracker tier there is no APM tab.
- **Army:** each player's army supply over the game, and what the army was made of at every minute. Workers and units still in production don't count.
- **Production:** how long each production building stood idle, with totals per player and a chart of how many were idle at once. A building is busy while it trains a unit, builds an add-on or upgrades (Orbital Command, Planetary Fortress), and idle the rest of the time it stands finished and on the ground; time lifted off counts as neither. Idle time includes supply blocks. Warp gates are counted together, each warp-in keeping one busy for its cooldown. A reactor's second slot isn't counted. For Zerg, production is larvae: a hatchery counts as idle while it holds three or more, since it stops making new ones. Train times are the current patch's, so Chrono Boost can make a Protoss building look idle for a few seconds.
- **Upgrades:** when each player's research finished, for the whole game.

- **Build order:** each player's structures, units, morphs and research for the first ten minutes, with supply. Structures appear when placed, units and research when finished (replays don't record when they were queued). Repeats a moment apart share a line ("Zergling ×6").
- **Battles:** clusters of deaths close together in time and space, with each side's losses. They are also shown as yellow bands under the scrubber and as rings on the minimap while they happen.
- **Economy:** charts of income, workers, army value, supply and resources lost for each player, with a line at the current moment. Click a chart to jump there.
- **Expansions:** when and where each player took a new base, and when it finished.
- **Supply blocks:** when each player had no free supply, to the moment. ghostreel follows each player's supply as units start and die and supply buildings finish, and checks it against the stats the replay records every 10 s. The game also takes supply for units waiting in a queue, which the replay doesn't show; when the stats see a block the reconstruction misses, it is kept, with edges as close as the stats allow. Blocks shorter than a second are left out.

Click any row to jump there. These come from analysis plugins; see [CONTRIBUTING.md](../CONTRIBUTING.md) to write your own.

## Maps

The desktop app and `ghostreel serve` look for a replay's map in this order:

1. A map file you pick (**Pick map file…**, or `--map` on the command line).
2. The Battle.net cache, where the game keeps every map you've played: `C:\ProgramData\Blizzard Entertainment\Battle.net\Cache` on Windows, `/Users/Shared/Blizzard/Battle.net/Cache` on macOS.
3. Maps ghostreel downloaded before.
4. For local and bot games, which record only the map's file name: next to the replay, then in a `maps` folder beside it, then your SC2 `Maps` folder and the folders in `--maps` / `GHOSTREEL_MAPS` (in the desktop app, also the **Map folders** in **Settings**).

Finding a map next to the replay needs to know where the replay is: the desktop app knows for **Open replay…**, files dropped on the window, the library and "Open with". A replay uploaded to `ghostreel serve` or the browser build has no folder, so only the other places are searched.

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

## Health, shields and energy

Replays don't record hit points, so ordinary replays have no health bars; the inspect panel says "not in the replay". A replay with an engine recording (see the README's `ghostreel-engine`) made by a version that keeps health, shields and energy shows bars above units in 2D, 3D and exported video, in the game's colours: shields blue, health green to red, energy purple. By default only damaged units and the selected one have bars; **H** switches between damaged units, all units and none, and the inspect panel shows the numbers.

## 3D

Press **3**. With StarCraft II installed on the same computer, units are the game's own models with their animations, buildings go up the way they do in the game (scaffolding, morphing, warping in), and the map shows its rocks, plants and wreckage, its cliffs built from the game's cliff models, and the ground painted with the map's own textures. The terrain menu calls this **Ground textures**, and it works in 2D too. How sharp the ground looks depends on your install: a full Windows or macOS install has the game's full-size textures, while slimmed-down copies (such as Blizzard's Linux package) have only small versions, which look soft up close. Zerg creep shows as a purple layer around hatcheries and creep tumors; it is an estimate of how far it spread. The models are converted from your install the first time they're needed and kept in ghostreel's cache folder; nothing is downloaded or shared. If the game is installed somewhere unusual, point to the folder that contains `.build.info`, using `--gamedata` or the `GHOSTREEL_GAMEDATA` environment variable. To convert everything up front and see what's missing, run `ghostreel assets`.

Without the game (and in the browser build), units are plain shapes in team colors, which is also what you see while a model loads and when zoomed far out.

The 3D view is lit like the map, with its sun direction and light colors (toned halfway toward white, so models keep their colors), when the game data has the map's lighting.

Known gap: there is no water.

## Privacy

ghostreel makes no network requests unless you ask: for a map download, or, in the desktop app, for update checks (see [Updates](#updates)). The server only listens on your own computer by default. Replays, maps and converted models stay in your local cache:

| OS | Cache |
| --- | --- |
| Windows | `%LocalAppData%\ghostreel` |
| macOS | `~/Library/Caches/ghostreel` |
| Linux | `~/.cache/ghostreel` |

Delete that folder to clear it.

## Troubleshooting

- **"Decoded with the protocol of build …"**: the replay is from a game version newer than ghostreel knows. It is decoded with the nearest older version, which usually works. Update ghostreel when a new version is out.
- **Units stand still:** the replay is on the tracker tier (no map, or AI players). Add the map. The built-in AI's army moves only when the replay sampled it; its workers are drawn mining.
- **The map doesn't match:** a map of the right name but a different size was found. Pick the right file.
- **3D shows shapes only:** no game installation was found. In the desktop app, open **Settings** and choose the StarCraft II folder (the one with `.build.info`, such as `D:\Games\StarCraft II`); the models load without a restart. With `ghostreel serve`, check the log line "3D models from …", or set `--gamedata`.

## Updates

The first time the desktop app starts, it asks whether to look for new versions on GitHub when it starts. If you say yes and one is out, a banner offers **Update and restart**: the app downloads the new version, checks that it's the genuine release (it's signed, and its checksum must match), installs it and starts again. An installed copy runs the new installer, which asks Windows for permission as the first install did; the portable `ghostreel-desktop.exe` replaces itself. On macOS and Linux the banner links to the download instead.

You can change your answer, see your version and check right away in **Settings**. Builds made from source never replace themselves.

## Settings (desktop app)

**Settings** in the toolbar holds two things, saved in `settings.json` in your user config folder (`%AppData%\ghostreel` on Windows):

- **StarCraft II folder:** where the game is installed, if ghostreel doesn't find it in the usual place. Leave it empty to find it automatically. Its `Maps` folder is searched for local maps too.
- **Map folders:** more folders to search for the maps of local and bot games, one per line.
