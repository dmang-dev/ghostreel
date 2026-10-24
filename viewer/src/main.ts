// SPDX-License-Identifier: Apache-2.0
//
// Viewer entry point: loading, playback loop and the DOM panels around the
// three.js scene.

import "./style.css";
import * as THREE from "three";
import * as api from "./api";
import { LOOPS_PER_CLOCK_SECOND, Model, UnitClass, decodeTimeline, formatClock, isAlive, ownerAt, typeAt } from "./model";
import { Director } from "./director";
import { buildDoodads } from "./doodads";
import { canExport, exportVideo, frameCount, plan, type ExportOptions } from "./exporter";
import type { Marker } from "./reports";
import { UnitModels } from "./models";
import { ReportsView } from "./reports";
import { Scene } from "./scene";
import { blankGrid, hillshade, minimapTerrain } from "./terrain";

const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

const stage = $("stage");
const scene = new Scene(stage);
// The scene's canvases go under the panels.
stage.prepend(scene.canvas, stage.querySelector("canvas.labels")!);

// Analysis plugins' reports share the events panel.
const reports = new ReportsView($("feed-tabs"), $("report"), $("feed-list"), (l) => seek(l), playerColor);

// Game models for the 3D view, if the server can read the game's data. It
// opens the data in the background at startup, so ask again for a while.
const unitModels = new UnitModels("");
scene.models = unitModels;
const assetsReady = (async () => {
  for (let i = 0; i < 10 && !(await unitModels.init()); i++) await new Promise((r) => setTimeout(r, 1500));
  return unitModels.available;
})();

interface Loaded {
  summary: api.Summary;
  model: Model;
  map: api.MapData | null;
  minimap: ImageBitmap | null;
  width: number;
  height: number;
  playable: api.MapRect;
  shaded: HTMLCanvasElement;
  art: HTMLCanvasElement | null;
  /** The painted ground from the game's textures, once the server has made it. */
  ground: HTMLCanvasElement | null;
}

let cur: Loaded | undefined;
let loop = 0;
let playing = false;
let speed = 1;
let selected: number | undefined;
let scrubbing = false;
type TerrainMode = "shade" | "minimap" | "ground";
let terrainMode: TerrainMode = "shade";

// ---------------------------------------------------------------- loading

async function load(open: () => Promise<api.Summary>, label: string): Promise<void> {
  setPlaying(false);
  showError("");
  $("loading-text").textContent = `Loading ${label}…`;
  $("loading").hidden = false;
  try {
    const summary = await open();
    $("loading-text").textContent = summary.fromCache ? "Loading cached timeline…" : "Building timeline…";
    const [tlBytes, map, minimap] = await Promise.all([
      api.fetchTimeline(summary.id),
      api.fetchMap(summary.id),
      api.fetchMinimap(summary.id),
    ]);
    const t0 = performance.now();
    const tl = decodeTimeline(tlBytes);
    const t1 = performance.now();
    const model = new Model(tl);
    const t2 = performance.now();
    show(summary, model, map, minimap);
    const t3 = performance.now();
    console.info(`${label}: decode ${(t1 - t0).toFixed(0)} ms, model ${(t2 - t1).toFixed(0)} ms, scene ${(t3 - t2).toFixed(0)} ms`);
  } catch (e) {
    showError(`Could not open ${label}: ${(e as Error).message}`);
  } finally {
    $("loading").hidden = true;
  }
}

function show(summary: api.Summary, model: Model, map: api.MapData | null, minimap: ImageBitmap | null): void {
  const meta = model.tl.meta!;
  const width = map?.width ?? meta.mapWidth;
  const height = map?.height ?? meta.mapHeight;
  const playable = map?.playable ?? { Left: 0, Bottom: 0, Right: width, Top: height };
  const shaded = map ? hillshade(map) : blankGrid(width, height);
  const art = map && minimap ? minimapTerrain(map, minimap) : null;
  cur = { summary, model, map, minimap, width, height, playable, shaded, art, ground: null };

  scene.setWorld(model, width, height, terrainMode === "minimap" && art ? art : shaded, map?.heights, map?.pathable);
  if (terrainMode === "ground") setTerrainMode("shade");
  $<HTMLOptionElement>("terrain-mode-ground").hidden = true;
  if (map) void assetsReady.then((ok) => ok && loadGround(summary.id));
  doodadsFor = "";
  void loadDoodads();
  reports.loops = model.loops;
  reports.set([]);
  setHotspots([]);
  setDirector(false);
  api.fetchAnalysis(summary.id).then(
    (r) => {
      if (cur?.summary.id !== summary.id) return;
      reports.set(r);
      drawMarks();
      setHotspots(r.find((x) => x.plugin === "hotspots")?.markers ?? []);
    },
    (e) => console.warn("analysis:", e),
  );
  buildFogOptions(model);
  scene.fit(playable.Left, playable.Bottom, playable.Right, playable.Top);
  selected = undefined;
  loop = 0;
  feedShownUpTo = -1;

  const players = meta.players.map((p) => `${p.name} (${p.race[0] ?? "?"})`).join(" vs ");
  $("title").textContent = `${meta.mapTitle} — ${players} — ${formatClock(meta.loops)}`;
  $("title").title = `${summary.name}\nSC2 ${meta.version}` +
    (meta.protocolApproxFrom ? `\nDecoded with the protocol of build ${meta.protocolApproxFrom} (not exact)` : "");
  showTier(summary.fidelity);
  $("empty").hidden = true;
  $("feed").hidden = false;
  $("minimap").hidden = false;
  $("play").removeAttribute("disabled");
  const scrub = $<HTMLInputElement>("scrub");
  scrub.max = String(meta.loops);
  scrub.disabled = false;
  $("mapbanner").hidden = summary.hasMap;
  $("maperror").textContent = summary.mapError ?? "";
  $("downloadmap").hidden = !summary.canDownload;
  buildPlayers();
  drawMarks();
  sizeMinimap();
  updatePanels(true);
}

/** Replaces the current replay's map (picked file or depot download), keeping the playback position. */
async function replaceMap(get: (id: string) => Promise<api.Summary>, label: string): Promise<void> {
  if (!cur) return;
  const c = cur;
  $("loading-text").textContent = label;
  $("loading").hidden = false;
  try {
    const summary = await get(c.summary.id);
    // The command tier depends on the map, so the timeline may have changed too.
    const [tlBytes, map, minimap] = await Promise.all([
      api.fetchTimeline(summary.id), api.fetchMap(summary.id), api.fetchMinimap(summary.id),
    ]);
    const at = loop;
    show(summary, new Model(decodeTimeline(tlBytes)), map, minimap);
    loop = at;
  } catch (e) {
    showError(`That map didn't work: ${(e as Error).message}`);
  } finally {
    $("loading").hidden = true;
  }
}

const pickMap = (file: File) => replaceMap((id) => api.setMapFile(id, file), `Reading ${file.name}…`);

function showError(msg: string): void {
  $("error").textContent = msg;
  $("error").hidden = !msg;
}

// ---------------------------------------------------------------- playback

function setPlaying(p: boolean): void {
  playing = p && !!cur;
  if (playing && cur && loop >= cur.model.loops) loop = 0;
  $("play").textContent = playing ? "❚❚" : "▶";
}

function seek(to: number): void {
  if (!cur) return;
  loop = Math.min(Math.max(0, to), cur.model.loops);
}

function setSpeed(s: number): void {
  speed = s;
  $<HTMLSelectElement>("speed").value = String(s);
}

const SPEEDS = [0.5, 1, 2, 4, 8, 16];

let last = performance.now();
let fpsFrames = 0;
let fpsTime = 0;
let frameMs = 0;
let panelsAt = 0;

function tick(now: number): void {
  // Keep real-time pace even at low frame rates; only a long stall (tab in
  // the background) is clipped so playback doesn't jump ahead.
  const dt = Math.min((now - last) / 1000, 1);
  last = now;
  if (cur && playing) {
    loop += dt * LOOPS_PER_CLOCK_SECOND * speed;
    if (loop >= cur.model.loops) {
      loop = cur.model.loops;
      setPlaying(false);
    }
  }
  if (exporting) {
    requestAnimationFrame(tick);
    return; // the exporter drives the scene
  }
  if (directorOn && director) frameShot();
  const t0 = performance.now();
  scene.render(loop, selected);
  frameMs = frameMs * 0.9 + (performance.now() - t0) * 0.1;

  if (cur) {
    $("clock").textContent = `${formatClock(loop)} / ${formatClock(cur.model.loops)}`;
    if (!scrubbing) $<HTMLInputElement>("scrub").value = String(Math.floor(loop));
    if (now - panelsAt > 200) {
      panelsAt = now;
      updatePanels(false);
    }
    drawMinimap();
  }

  fpsFrames++;
  fpsTime += dt;
  if (fpsTime >= 0.5) {
    $("fps").textContent = `${Math.round(fpsFrames / fpsTime)} fps · ${frameMs.toFixed(1)} ms/frame · ${scene.drawnUnits().length} units`;
    fpsFrames = 0;
    fpsTime = 0;
  }
  requestAnimationFrame(tick);
}
requestAnimationFrame(tick);

// ---------------------------------------------------------------- panels

const playerEls = new Map<number, HTMLElement>();

function hex(c: number): string {
  return `#${c.toString(16).padStart(6, "0")}`;
}

function buildPlayers(): void {
  const box = $("players");
  box.replaceChildren();
  playerEls.clear();
  for (const p of cur?.model.tl.meta?.players ?? []) {
    const el = document.createElement("div");
    el.className = "player";
    el.style.setProperty("--pc", hex(p.color));
    box.append(el);
    playerEls.set(p.id, el);
  }
}

function playerName(id: number): string {
  if (id === 0) return "Neutral";
  return cur?.model.tl.meta?.players.find((p) => p.id === id)?.name ?? `Player ${id}`;
}

function playerColor(id: number): string {
  const p = cur?.model.tl.meta?.players.find((p) => p.id === id);
  return p ? hex(p.color) : "#888";
}

/** Player color lifted toward white so dark colors stay readable on dark panels. */
function textColor(id: number): string {
  const c = parseInt(playerColor(id).slice(1), 16);
  const mix = (v: number) => Math.round(v + (255 - v) * 0.45);
  return `rgb(${mix(c >> 16)}, ${mix((c >> 8) & 255)}, ${mix(c & 255)})`;
}

const fmt = (n: number) => n.toLocaleString("en-US");

function updatePanels(force: boolean): void {
  if (!cur) return;
  const { model } = cur;
  for (const p of model.tl.meta?.players ?? []) {
    const el = playerEls.get(p.id)!;
    const s = model.statsAt(p.id, loop);
    const result = loop >= model.loops && p.result && p.result !== "Unknown" ? ` · ${p.result}` : "";
    el.innerHTML = `<div class="name"><span>${escapeHTML(p.name)}</span><span class="race">${p.race}${result}</span></div>` +
      (s
        ? `<div class="grid">
            <span>Supply</span><span>${Math.round(s.supplyUsed)}/${Math.round(s.supplyMade)}</span>
            <span>Workers</span><span>${s.workers}</span>
            <span>Minerals</span><span>${fmt(s.minerals)}</span>
            <span>Gas</span><span>${fmt(s.vespene)}</span>
            <span>Income</span><span>${fmt(s.mineralRate)}</span>
            <span>Gas inc.</span><span>${fmt(s.vespeneRate)}</span>
            <span>Army</span><span>${fmt(s.armyMinerals + s.armyVespene)}</span>
            <span>Lost</span><span>${fmt(s.lostMinerals + s.lostVespene)}</span>
          </div>`
        : "");
  }
  updateFeed(force);
  reports.update(loop);
  updateInspect();
}

let feedShownUpTo = -1;

function updateFeed(force: boolean): void {
  if (!cur) return;
  const feed = cur.model.feed;
  // Number of items at or before the current loop.
  let lo = 0;
  let hi = feed.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (feed[mid].loop > loop) hi = mid;
    else lo = mid + 1;
  }
  if (lo === feedShownUpTo && !force) return;
  feedShownUpTo = lo;
  const list = $("feed-list");
  list.replaceChildren();
  for (let i = lo - 1; i >= Math.max(0, lo - 60); i--) {
    const it = feed[i];
    const li = document.createElement("li");
    li.innerHTML = `<span class="t">${formatClock(it.loop)}</span><span class="dot" style="background:${playerColor(it.player)}"></span><span></span>`;
    li.lastElementChild!.textContent = it.text;
    li.onclick = () => {
      seek(it.loop);
      if (it.unitId !== undefined) select(it.unitId, true);
    };
    list.append(li);
  }
}

function select(id: number | undefined, center = false): void {
  selected = id;
  if (id !== undefined && center && cur) {
    const d = scene.drawnUnits().find((u) => u.id === id);
    const keys = cur.model.tracks[id];
    const pos = d ?? (keys?.length ? keys[keys.length - 1] : undefined);
    if (pos) {
      scene.view.cx = pos.x;
      scene.view.cy = pos.y;
      scene.updateCamera();
    }
  }
  updateInspect();
}

function updateInspect(): void {
  const box = $("inspect");
  if (!cur || selected === undefined) {
    box.hidden = true;
    return;
  }
  const { model } = cur;
  const u = model.units[selected];
  const t = typeAt(u, loop);
  const owner = ownerAt(u, loop);
  const alive = isAlive(u, loop);
  const d = scene.drawnUnits().find((x) => x.id === selected);
  const lastSeen = model.lastSeen(selected, loop);
  const status = !alive
    ? u.bornLoop > loop ? `not yet born (${formatClock(u.bornLoop)})` : `died ${formatClock(u.diedLoop!)}`
    : u.constructed && (u.doneLoop === undefined || u.doneLoop > loop) ? "under construction" : "alive";
  const type = model.types[t];
  const mobile = type && (type.speed > 0 || type.air) && type.class !== UnitClass.RESOURCE;
  let note = "";
  if (alive && mobile && lastSeen !== undefined && loop - lastSeen > 1) {
    note = `Position estimated: last known ${formatClock(lastSeen)} (${Math.round((loop - lastSeen) / LOOPS_PER_CLOCK_SECOND)} s ago).`;
  }
  box.hidden = false;
  box.innerHTML = `
    <h3>${escapeHTML(model.typeName(t))}</h3>
    <div class="row"><span>Owner</span><span style="color:${textColor(owner)}">${escapeHTML(playerName(owner))}</span></div>
    <div class="row"><span>Status</span><span>${status}</span></div>
    <div class="row"><span>Born</span><span>${formatClock(u.bornLoop)}</span></div>
    ${u.diedLoop !== undefined && u.diedLoop <= loop ? `<div class="row"><span>Died</span><span>${formatClock(u.diedLoop)}${u.killerPlayer ? ` (by ${escapeHTML(playerName(u.killerPlayer))})` : ""}</span></div>` : ""}
    ${d ? `<div class="row"><span>Position</span><span>${d.x.toFixed(1)}, ${d.y.toFixed(1)}</span></div>` : ""}
    <div class="row"><span>Unit</span><span>#${u.id} · tag ${u.tag >>> 18}/${u.tag & 0x3ffff}</span></div>
    ${note ? `<div class="note">${note}</div>` : ""}
    <button id="center-sel">Center</button>`;
  $("center-sel").onclick = () => select(selected, true);
}

function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

// ---------------------------------------------------------------- scrubber marks

function drawMarks(): void {
  const c = $<HTMLCanvasElement>("marks");
  const dpr = Math.min(window.devicePixelRatio, 2);
  c.width = c.clientWidth * dpr;
  c.height = c.clientHeight * dpr;
  const ctx = c.getContext("2d")!;
  ctx.clearRect(0, 0, c.width, c.height);
  if (!cur || cur.model.loops === 0) return;
  const { model } = cur;
  // Battles: deaths per bucket, as red bars.
  const bins = Math.max(1, Math.floor(c.width / (3 * dpr)));
  const counts = new Array(bins).fill(0);
  for (const l of model.deathLoops) counts[Math.min(bins - 1, Math.floor((l / model.loops) * bins))]++;
  const max = Math.max(4, ...counts);
  const bw = c.width / bins;
  ctx.fillStyle = "rgba(255,107,94,0.75)";
  counts.forEach((n, i) => {
    if (!n) return;
    const h = (n / max) * c.height * 0.9;
    ctx.fillRect(i * bw, c.height - h, Math.max(1, bw - 1), h);
  });
  // Spans the analysis plugins found (battles...): a band across the middle.
  ctx.fillStyle = "rgba(255,214,102,0.35)";
  for (const m of reports.markers()) {
    if (m.end === undefined) continue;
    const x0 = (m.loop / model.loops) * c.width;
    const x1 = (m.end / model.loops) * c.width;
    ctx.fillRect(x0, c.height * 0.3, Math.max(2 * dpr, x1 - x0), c.height * 0.4);
  }
  // Upgrades and expansions: ticks at the top in player color.
  for (const it of model.feed) {
    const isUpgrade = it.text.startsWith("Upgrade:");
    const isExpansion = /^(Hatchery|Nexus|CommandCenter) started$/.test(it.text);
    if (!isUpgrade && !isExpansion) continue;
    ctx.fillStyle = playerColor(it.player);
    const x = (it.loop / model.loops) * c.width;
    ctx.fillRect(x, 0, Math.max(1, dpr), (isExpansion ? 9 : 5) * dpr);
  }
}
new ResizeObserver(drawMarks).observe($("marks"));

// ---------------------------------------------------------------- minimap

let minimapScale = 1;
let minimapAt = 0;
let minimapBase: HTMLCanvasElement | undefined;

function sizeMinimap(): void {
  if (!cur) return;
  const c = $<HTMLCanvasElement>("minimap");
  const p = cur.playable;
  const pw = p.Right - p.Left;
  const ph = p.Top - p.Bottom;
  minimapScale = 200 / Math.max(pw, ph);
  c.width = Math.round(pw * minimapScale);
  c.height = Math.round(ph * minimapScale);
  // Terrain of the playable area, pre-scaled once.
  minimapBase = document.createElement("canvas");
  minimapBase.width = c.width;
  minimapBase.height = c.height;
  const src = terrainImage();
  const sx = src.width / cur.width;
  minimapBase.getContext("2d")!.drawImage(src, p.Left * sx, (cur.height - p.Top) * sx, pw * sx, ph * sx, 0, 0, c.width, c.height);
}

function drawMinimap(): void {
  const now = performance.now();
  if (!cur || !minimapBase || now - minimapAt < 100) return;
  minimapAt = now;
  const c = $<HTMLCanvasElement>("minimap");
  const ctx = c.getContext("2d")!;
  const p = cur.playable;
  const toX = (x: number) => (x - p.Left) * minimapScale;
  const toY = (y: number) => (p.Top - y) * minimapScale;
  ctx.drawImage(minimapBase, 0, 0);
  for (const d of scene.drawnUnits()) {
    ctx.fillStyle = d.owner === 0 ? "#b5ecff" : playerColor(d.owner);
    const s = d.w ? Math.max(2, d.w * minimapScale) : 2;
    ctx.fillRect(toX(d.x) - s / 2, toY(d.y) - s / 2, s, s);
  }
  // Plugin markers happening now (battles): a ring where they are.
  ctx.strokeStyle = "#ffd666";
  ctx.lineWidth = 1.5;
  for (const m of reports.markers()) {
    if (!m.r || loop < m.loop || loop > (m.end ?? m.loop) + 112) continue;
    ctx.beginPath();
    ctx.arc(toX(m.x ?? 0), toY(m.y ?? 0), Math.max(4, m.r * minimapScale), 0, Math.PI * 2);
    ctx.stroke();
  }
  const [x0, y0] = scene.screenToWorld(0, 0);
  const [x1, y1] = scene.screenToWorld(stage.clientWidth, stage.clientHeight);
  ctx.strokeStyle = "#ffffffcc";
  ctx.lineWidth = 1;
  ctx.strokeRect(toX(x0) + 0.5, toY(y0) + 0.5, toX(x1) - toX(x0), toY(y1) - toY(y0));
}

{
  const c = $<HTMLCanvasElement>("minimap");
  const moveTo = (e: PointerEvent) => {
    if (!cur) return;
    const r = c.getBoundingClientRect();
    scene.view.cx = cur.playable.Left + (e.clientX - r.left) / minimapScale;
    scene.view.cy = cur.playable.Top - (e.clientY - r.top) / minimapScale;
    scene.updateCamera();
    minimapAt = 0;
  };
  c.addEventListener("pointerdown", (e) => {
    c.setPointerCapture(e.pointerId);
    moveTo(e);
  });
  c.addEventListener("pointermove", (e) => {
    if (e.buttons) moveTo(e);
  });
}

// ---------------------------------------------------------------- inputs

// A click selects the unit under it; a click on empty ground clears the selection.
scene.onClick = (px, py) => {
  if (cur) select(scene.pick(px, py));
};

$("open").onclick = () => $<HTMLInputElement>("file").click();
$<HTMLInputElement>("file").onchange = (e) => {
  const f = (e.target as HTMLInputElement).files?.[0];
  if (f) load(() => api.openReplayFile(f), f.name);
  (e.target as HTMLInputElement).value = "";
};
$("pickmap").onclick = () => $<HTMLInputElement>("mapfile").click();
$("downloadmap").onclick = () => replaceMap(api.downloadMap, "Downloading the map from Blizzard…");
$<HTMLInputElement>("mapfile").onchange = (e) => {
  const f = (e.target as HTMLInputElement).files?.[0];
  if (f) pickMap(f);
  (e.target as HTMLInputElement).value = "";
};
$("play").onclick = () => setPlaying(!playing);
$<HTMLSelectElement>("speed").onchange = (e) => setSpeed(Number((e.target as HTMLSelectElement).value));
{
  const scrub = $<HTMLInputElement>("scrub");
  scrub.addEventListener("pointerdown", () => (scrubbing = true));
  scrub.addEventListener("pointerup", () => (scrubbing = false));
  scrub.addEventListener("input", () => seek(Number(scrub.value)));
}
$<HTMLInputElement>("labels").onchange = (e) => (scene.showLabels = (e.target as HTMLInputElement).checked);
$<HTMLSelectElement>("terrain-mode").onchange = (e) => setTerrainMode((e.target as HTMLSelectElement).value as TerrainMode);
$("help-btn").onclick = () => ($("help").hidden = !$("help").hidden);

function terrainImage(): HTMLCanvasElement {
  if (terrainMode === "minimap" && cur?.art) return cur.art;
  if (terrainMode === "ground" && cur?.ground) return cur.ground;
  return cur!.shaded;
}

function setTerrainMode(mode: TerrainMode): void {
  terrainMode = mode;
  $<HTMLSelectElement>("terrain-mode").value = mode;
  if (!cur) return;
  if (mode === "minimap" && !cur.art) {
    showError("This map has no minimap image.");
    return;
  }
  // The painted ground already has texture; the tiled detail is for the others.
  scene.setTerrain(terrainImage(), mode !== "ground");
  sizeMinimap();
}

/** The map's painted ground, composed by the server from the game's textures. */
function loadGround(id: string): void {
  const img = new Image();
  img.onload = () => {
    if (cur?.summary.id !== id) return;
    const c = document.createElement("canvas");
    c.width = img.width;
    c.height = img.height;
    c.getContext("2d")!.drawImage(img, 0, 0);
    cur.ground = c;
    $<HTMLOptionElement>("terrain-mode-ground").hidden = false;
    if (terrainMode === "shade") setTerrainMode("ground");
  };
  img.src = `/api/r/${id}/ground`;
}

window.addEventListener("keydown", (e) => {
  if (e.target instanceof HTMLInputElement && e.target.type !== "range") return;
  if (e.target instanceof HTMLSelectElement) return;
  const step = (e.shiftKey ? 30 : 5) * LOOPS_PER_CLOCK_SECOND;
  switch (e.key) {
    case " ":
    case "p":
    case "P":
      setPlaying(!playing);
      break;
    case "ArrowLeft":
      seek(loop - step);
      break;
    case "ArrowRight":
      seek(loop + step);
      break;
    case "+":
    case "=":
      setSpeed(SPEEDS[Math.min(SPEEDS.length - 1, SPEEDS.indexOf(speed) + 1)]);
      break;
    case "-":
    case "_":
      setSpeed(SPEEDS[Math.max(0, SPEEDS.indexOf(speed) - 1)]);
      break;
    case "Home":
      seek(0);
      break;
    case "End":
      seek(cur?.model.loops ?? 0);
      break;
    case "Escape":
      select(undefined);
      $("help").hidden = true;
      break;
    case "l":
    case "L":
      scene.showLabels = !scene.showLabels;
      $<HTMLInputElement>("labels").checked = scene.showLabels;
      break;
    case "m":
    case "M":
      setTerrainMode(terrainMode === "shade" ? "minimap" : terrainMode === "minimap" && cur?.ground ? "ground" : "shade");
      break;
    case "b":
    case "B":
      reports.cycle();
      break;
    case "d":
    case "D":
      setDirector(!directorOn);
      break;
    case "3":
      toggle3D();
      break;
    case "v":
    case "V":
      cycleFog();
      break;
    case "f":
    case "F":
      $("fps").hidden = !$("fps").hidden;
      break;
    case "?":
      $("help").hidden = !$("help").hidden;
      break;
    default:
      return;
  }
  e.preventDefault();
});

// Drag and drop: a replay opens; a map file is used for the open replay.
{
  let depth = 0;
  window.addEventListener("dragenter", (e) => {
    if (!e.dataTransfer?.types.includes("Files")) return;
    depth++;
    $("drop").hidden = false;
  });
  window.addEventListener("dragleave", () => {
    if (--depth <= 0) {
      depth = 0;
      $("drop").hidden = true;
    }
  });
  window.addEventListener("dragover", (e) => e.preventDefault());
  window.addEventListener("drop", (e) => {
    e.preventDefault();
    depth = 0;
    $("drop").hidden = true;
    const f = e.dataTransfer?.files[0];
    if (!f) return;
    const name = f.name.toLowerCase();
    if (name.endsWith(".sc2replay")) load(() => api.openReplayFile(f), f.name);
    else if ((name.endsWith(".sc2map") || name.endsWith(".s2ma")) && cur) pickMap(f);
    else showError(`${f.name} is not a replay (.SC2Replay) or map (.SC2Map / .s2ma).`);
  });
}

// Library (only populated when the server was given replay folders).
$("browser-only").hidden = !api.BROWSER_ONLY;
api.library().then((entries) => {
  if (!entries.length) return;
  const sel = $<HTMLSelectElement>("library");
  const list = $("empty-library");
  for (const e of entries) {
    const opt = document.createElement("option");
    opt.value = opt.textContent = e.name;
    sel.append(opt);
    const li = document.createElement("li");
    li.textContent = e.name;
    li.onclick = () => load(() => api.openFromLibrary(e.name), e.name);
    list.append(li);
  }
  sel.hidden = false;
  sel.onchange = () => {
    if (sel.value) load(() => api.openFromLibrary(sel.value), sel.value);
    sel.value = "";
  };
}).catch(() => {
  // No library endpoint (or server down): the file picker still works.
});

// ?replay=<name> opens a library replay directly (handy for links and tests);
// otherwise show the replay the desktop app was launched with, if any.
{
  const name = new URLSearchParams(location.search).get("replay");
  if (name) {
    load(() => api.openFromLibrary(name), name);
  } else {
    api.startup().then((s) => {
      if (s) load(async () => s, s.name);
    }).catch(() => {});
  }
}

// Benchmark hook for the Phase 1 gate: renders `frames` frames spread over the
// whole game and reports the time per frame, independent of the display's
// refresh rate (and of the tab being visible). Run ghostreelBench() in the
// console with a replay open.
declare global {
  interface Window {
    ghostreelBench: (frames?: number) => { frames: number; msPerFrame: number; maxMs: number; maxUnits: number; seekMs: number };
  }
}
window.ghostreelBench = (frames = 600) => {
  if (!cur) throw new Error("open a replay first");
  const total = cur.model.loops;
  let worst = 0;
  let maxUnits = 0;
  const t0 = performance.now();
  for (let i = 0; i < frames; i++) {
    const f0 = performance.now();
    scene.render((i / frames) * total, selected);
    worst = Math.max(worst, performance.now() - f0);
    maxUnits = Math.max(maxUnits, scene.drawnUnits().length);
  }
  const msPerFrame = (performance.now() - t0) / frames;
  // Seek: jump to random loops, including panel updates.
  const s0 = performance.now();
  for (let i = 0; i < 50; i++) {
    seek(Math.random() * total);
    scene.render(loop, selected);
    updatePanels(true);
  }
  const seekMs = (performance.now() - s0) / 50;
  scene.render(loop, selected);
  return { frames, msPerFrame: +msPerFrame.toFixed(2), maxMs: +worst.toFixed(2), maxUnits, seekMs: +seekMs.toFixed(2) };
};

/** Times only the CPU side of a frame (unit evaluation), for the benchmark. */
(window as unknown as { ghostreelEvalBench: () => number }).ghostreelEvalBench = () => {
  if (!cur) throw new Error("open a replay first");
  const m = cur.model;
  const s = { id: 0, type: 0, owner: 0, x: 0, y: 0, facing: 0, constructing: false };
  const t0 = performance.now();
  for (let i = 0; i < 600; i++) {
    const l = (i / 600) * m.loops;
    for (const u of m.units) if (isAlive(u, l)) m.evalUnit(u, l, s);
  }
  return +((performance.now() - t0) / 600).toFixed(3);
};

const TIERS = {
  tracker: {
    label: "Tracker tier · positions approximate",
    help: "Births, deaths and buildings are exact. Unit positions are exact only when the replay sampled them (mostly in fights) and at death; in between, units hold their last known position.",
    cls: "",
  },
  command: {
    label: "Command tier · positions estimated",
    help: "Units follow the players' recorded orders over the map (workers mine, groups move around cliffs), corrected whenever the replay sampled a position. Players without recorded orders (the built-in AI) stay at tracker accuracy.",
    cls: "",
  },
  engine: {
    label: "Engine tier · exact positions",
    help: "Positions were recorded by playing this replay in the real game (ghostreel-engine).",
    cls: "exact",
  },
} as const;

function showTier(fidelity: api.Summary["fidelity"]): void {
  const t = TIERS[fidelity] ?? TIERS.tracker;
  const el = $("tier");
  el.textContent = t.label;
  el.title = t.help;
  el.className = `badge ${t.cls}`;
  el.hidden = false;
}

// ---------------------------------------------------------------- 3D and fog of war

function toggle3D(): void {
  scene.setMode(scene.mode === "2d" ? "3d" : "2d");
  $("mode3d").textContent = scene.mode === "2d" ? "3D" : "2D";
  minimapAt = 0;
  void loadDoodads();
}

// Doodads load the first time a replay is shown in 3D (their models are
// converted on the server on first use, so the 2D view never waits for them).
let doodadsFor = "";
async function loadDoodads(): Promise<void> {
  if (!cur?.map || scene.mode !== "3d" || !unitModels.available || doodadsFor === cur.summary.id) return;
  const id = (doodadsFor = cur.summary.id);
  const ground = cur.map.terrainTextures?.[0];
  if (ground) {
    new THREE.TextureLoader().load(`/api/assets/terrain/${encodeURIComponent(ground)}`, (tex) => {
      if (cur?.summary.id === id) scene.setGroundDetail(tex);
      else tex.dispose();
    });
  }
  try {
    const list = await api.fetchDoodads(id);
    if (cur?.summary.id !== id) return;
    const group = new THREE.Group();
    scene.setDoodads(group);
    await buildDoodads(group, list, unitModels);
  } catch (e) {
    console.warn("doodads:", e);
  }
}
$("mode3d").onclick = toggle3D;

function buildFogOptions(model: Model): void {
  const sel = $<HTMLSelectElement>("fog");
  sel.replaceChildren(new Option("Fog: off", "0"));
  for (const p of model.tl.meta?.players ?? []) sel.append(new Option(`Fog: ${p.name}'s view`, String(p.id)));
  sel.value = "0";
  scene.setFogPlayer(0);
}

function setFog(p: number): void {
  scene.setFogPlayer(p);
  $<HTMLSelectElement>("fog").value = String(p);
}
$<HTMLSelectElement>("fog").onchange = (e) => setFog(Number((e.target as HTMLSelectElement).value));

function cycleFog(): void {
  const ids = [0, ...(cur?.model.tl.meta?.players ?? []).map((p) => p.id)];
  setFog(ids[(ids.indexOf(scene.fogPlayer) + 1) % ids.length]);
}

// Debug handle for the console (and tests): the scene and current replay.
(window as unknown as { ghostreel: unknown }).ghostreel = { scene, seek, get cur() { return cur; }, get loop() { return loop; } };

// ---------------------------------------------------------------- director and video export

let hotspots: Marker[] = [];
let director: Director | undefined;
let directorOn = false;
let exporting = false;

function setHotspots(list: Marker[]): void {
  hotspots = list;
  director = cur ? newDirector() : undefined;
}

function newDirector(): Director {
  const p = cur!.playable;
  return new Director(cur!.model, hotspots, { left: p.Left, bottom: p.Bottom, right: p.Right, top: p.Top });
}

function setDirector(on: boolean): void {
  directorOn = on && !!director;
  $("director").classList.toggle("on", directorOn);
  if (!directorOn) $("caption").hidden = true;
  director?.reset();
}
$("director").onclick = () => setDirector(!directorOn);
scene.onUserMove = () => directorOn && setDirector(false);

/** Points the camera where the director wants it and captions the hotspot. */
function frameShot(): void {
  const shot = director!.shotAt(loop);
  scene.view.cx = shot.cx;
  scene.view.cy = shot.cy;
  scene.view.ppc = stage.clientHeight / shot.span;
  scene.updateCamera();
  const cap = $("caption");
  const h = shot.hotspot;
  cap.hidden = !h;
  if (h) {
    const [what, ...rest] = h.label.split(": ");
    cap.querySelector("b")!.textContent = what;
    cap.querySelector("span")!.textContent = rest.join(": ");
    cap.classList.toggle("major", h.tier === "major");
  }
}

function exportOptions(): ExportOptions {
  const [w, h] = $<HTMLSelectElement>("export-size").value.split("x").map(Number);
  return {
    range: $<HTMLSelectElement>("export-range").value as "game" | "reel",
    reelCount: Number($<HTMLSelectElement>("export-count").value),
    speed: Number($<HTMLSelectElement>("export-speed").value),
    width: w,
    height: h,
    fps: 30,
    director: $<HTMLInputElement>("export-director").checked,
  };
}

function updateExportNote(): void {
  $("export-count-row").hidden = $<HTMLSelectElement>("export-range").value !== "reel";
  const note = $("export-note");
  if (!canExport()) {
    note.textContent = "This browser can't encode video. Use Chrome, Edge, or the Windows or macOS app.";
    return;
  }
  if (!cur) return;
  const o = exportOptions();
  if (o.range === "reel" && !hotspots.length) {
    note.textContent = "No hotspots yet (the analysis is still running, or found none).";
    return;
  }
  const frames = frameCount(plan(cur.model, hotspots, o), o);
  note.textContent = `About ${formatClock(Math.round((frames / o.fps) * LOOPS_PER_CLOCK_SECOND))} of video, ${frames} frames.`;
}
for (const id of ["export-range", "export-count", "export-speed", "export-size"]) $(id).addEventListener("change", updateExportNote);

let exportAbort: AbortController | undefined;
$("export-btn").onclick = () => {
  $("export").hidden = !$("export").hidden;
  updateExportNote();
};
$("export-cancel").onclick = () => {
  if (exportAbort) exportAbort.abort();
  else $("export").hidden = true;
};
$("export-start").onclick = async () => {
  if (!cur || exporting) return;
  const opts = exportOptions();
  const saved = { loop, view: { ...scene.view } };
  setPlaying(false);
  exporting = true;
  exportAbort = new AbortController();
  const bar = $<HTMLProgressElement>("export-progress");
  bar.hidden = false;
  bar.value = 0;
  $<HTMLButtonElement>("export-start").disabled = true;
  $("export-cancel").textContent = "Cancel";
  try {
    const blob = await exportVideo(
      {
        scene, model: cur.model, director: opts.director ? newDirector() : undefined, hotspots, playerColor,
        minimap: () => {
          minimapAt = 0;
          drawMinimap();
          return $<HTMLCanvasElement>("minimap");
        },
      },
      opts,
      (f) => (bar.value = f),
      exportAbort.signal,
    );
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    const meta = cur.model.tl.meta;
    a.download = `${meta?.mapTitle ?? "replay"} - ${(meta?.players ?? []).map((p) => p.name).join(" vs ")}${opts.range === "reel" ? " (highlights)" : ""}.mp4`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 60000);
    $("export-note").textContent = `Saved ${(blob.size / 1e6).toFixed(1)} MB.`;
  } catch (e) {
    $("export-note").textContent = (e as Error).name === "AbortError" ? "Cancelled." : `Export failed: ${(e as Error).message}`;
  } finally {
    exporting = false;
    exportAbort = undefined;
    bar.hidden = true;
    $<HTMLButtonElement>("export-start").disabled = false;
    $("export-cancel").textContent = "Close";
    Object.assign(scene.view, saved.view);
    scene.updateCamera();
    seek(saved.loop);
    director?.reset();
  }
};

