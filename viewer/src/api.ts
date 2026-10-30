// SPDX-License-Identifier: Apache-2.0
//
// Client for the ghostreel HTTP API (internal/server). The same API backs
// `ghostreel serve` and the desktop app, so the viewer has one code path.

import type { Doodad } from "./doodads";
import type { Report } from "./reports";
import { wasmFetch } from "./wasm";

export type { Doodad };

/** The browser-only build runs the Go core in the page (see wasm.ts). */
export const BROWSER_ONLY = import.meta.env.VITE_BACKEND === "wasm";

/** fetch() for the API: the server's, or the in-page core's. */
export function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  return BROWSER_ONLY ? wasmFetch(path, init) : fetch(path, init);
}

export interface Summary {
  id: string;
  name: string;
  mapTitle: string;
  duration: string;
  version: string;
  protocolApproxFrom?: number;
  hasMap: boolean;
  mapHow?: string;
  mapError?: string;
  fromCache: boolean;
  /** The map is missing but Blizzard's depot has it. */
  canDownload: boolean;
  /** Tier the timeline was built at. */
  fidelity: "tracker" | "command" | "engine";
  players: string[];
}

export interface MapRect {
  Left: number;
  Bottom: number;
  Right: number;
  Top: number;
}

export interface MapObject {
  Kind: "unit" | "point" | "doodad";
  Type: string;
  Name?: string;
  X: number;
  Y: number;
  Z: number;
  Rotation?: number;
  Player?: number;
  /** Units: the starting minerals or gas when the map sets them (else the type's default). */
  Resources?: number;
}

/** mapfile.Export */
export interface MapData {
  name: string;
  width: number;
  height: number;
  playable: MapRect;
  /** The map's lighting (a CLight id). */
  tileset: string;
  /** What the map says about itself (English when it has it). */
  title?: string;
  description?: string;
  /** What the map is for: "1v1", "2". */
  players?: string;
  /** The map carries a screenshot (fetchPreview). */
  hasPreview?: boolean;
  /** Terrain texture layers (ids for /api/assets/terrain), base first. */
  terrainTextures?: string[];
  /** The painted texture masks (served by fetchMasks): size in texels, and texture repeats across the map (x, y) then offsets in cells. */
  terrainMasks?: { width: number; height: number; tiling: number[] };
  startLocations: { X: number; Y: number }[] | null;
  objects: MapObject[];
  heights?: number[];
  pathable?: number[];
  warnings?: string[];
}

export interface LibraryEntry {
  name: string;
  size: number;
}

async function check(resp: Response): Promise<Response> {
  if (resp.ok) return resp;
  let msg = `${resp.status} ${resp.statusText}`;
  try {
    const body = await resp.json();
    if (body?.error) msg = body.error;
  } catch {
    // not JSON
  }
  throw new Error(msg);
}

export interface Config {
  network: boolean;
  /** The desktop app: replays can be opened by path (openReplayPath). */
  openPaths?: boolean;
  /** A native open dialog is available (pickReplay). */
  pick?: boolean;
  /** A native folder dialog is available (pickFolder). */
  pickFolder?: boolean;
  /** The settings page is available (the desktop app). */
  settings?: boolean;
  /** The app can check for and install new releases (getUpdate). */
  update?: boolean;
}

/** Where the desktop app's updater stands (desktop/update.go). */
export interface UpdateStatus {
  current: string;
  latest?: string;
  available: boolean;
  notesUrl?: string;
  /** This copy can install the update itself; otherwise link to the release. */
  canApply: boolean;
  state: "idle" | "checking" | "downloading" | "installing" | "error";
  progress?: number;
  error?: string;
  checked?: string;
}

export async function getUpdate(): Promise<UpdateStatus> {
  return (await check(await apiFetch("/api/update"))).json();
}

/** Looks for a newer release now. */
export async function checkUpdate(): Promise<UpdateStatus> {
  return (await check(await apiFetch("/api/update/check", { method: "POST" }))).json();
}

/** Starts downloading and installing it; the app restarts when it's ready. */
export async function applyUpdate(): Promise<UpdateStatus> {
  return (await check(await apiFetch("/api/update/apply", { method: "POST" }))).json();
}

export interface Settings {
  /** The StarCraft II folder; "" finds the install. */
  gameData: string;
  mapFolders: string[];
  /** Look for new releases at start; unset until the user has been asked. */
  checkUpdates?: boolean;
  /** Which game data is in use, or why there is none (read-only). */
  gameDataStatus?: string;
}

export async function getSettings(): Promise<Settings> {
  return (await check(await apiFetch("/api/settings"))).json();
}

/** Saves the settings; rejects with a message for the user when they don't check out. */
export async function saveSettings(s: Settings): Promise<Settings> {
  const resp = await apiFetch("/api/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ gameData: s.gameData, mapFolders: s.mapFolders, checkUpdates: s.checkUpdates }),
  });
  return (await check(resp)).json();
}

/** Shows the desktop app's folder dialog; resolves to the path, "" if cancelled. */
export async function pickFolder(): Promise<string> {
  return (await (await check(await apiFetch("/api/pick-folder", { method: "POST" }))).json()).path ?? "";
}

/** Looks for a replay's missing map again (after the map folders change). */
export async function findMap(id: string): Promise<Summary> {
  return (await check(await apiFetch(`/api/r/${id}/find-map`, { method: "POST" }))).json();
}

export async function config(): Promise<Config> {
  return (await check(await apiFetch("/api/config"))).json();
}

/** Shows the desktop app's open dialog; resolves to the path, "" if cancelled. */
export async function pickReplay(): Promise<string> {
  return (await (await check(await apiFetch("/api/pick", { method: "POST" }))).json()).path ?? "";
}

/** Opens a replay by its path on disk (desktop app), so maps beside it are found. */
export async function openReplayPath(path: string): Promise<Summary> {
  const resp = await apiFetch("/api/open-path", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path }),
  });
  return (await check(resp)).json();
}

export async function openReplayFile(file: File): Promise<Summary> {
  const resp = await apiFetch("/api/open", {
    method: "POST",
    headers: { "X-Filename": encodeURIComponent(file.name) },
    body: file,
  });
  return (await check(resp)).json();
}

export async function library(): Promise<LibraryEntry[]> {
  return (await (await check(await apiFetch("/api/library"))).json()) ?? [];
}

export async function openFromLibrary(name: string): Promise<Summary> {
  const resp = await apiFetch("/api/library/open", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  return (await check(resp)).json();
}

/** Asks the server to fetch the map from Blizzard's depot (user-initiated). */
export async function downloadMap(id: string): Promise<Summary> {
  return (await check(await apiFetch(`/api/r/${id}/download-map`, { method: "POST" }))).json();
}

/** A replay the desktop app was launched with, or null. */
export async function startup(): Promise<Summary | null> {
  const resp = await apiFetch("/api/startup");
  return resp.ok ? resp.json() : null;
}

export async function setMapFile(id: string, file: File): Promise<Summary> {
  const resp = await apiFetch(`/api/r/${id}/map`, {
    method: "POST",
    headers: { "X-Filename": encodeURIComponent(file.name) },
    body: file,
  });
  return (await check(resp)).json();
}

export async function fetchTimeline(id: string): Promise<Uint8Array> {
  return new Uint8Array(await (await check(await apiFetch(`/api/r/${id}/timeline`))).arrayBuffer());
}

/** Reports of the core's analysis plugins (build orders, battles, ...). */
export async function fetchAnalysis(id: string): Promise<Report[]> {
  return (await (await check(await apiFetch(`/api/r/${id}/analysis`))).json()) ?? [];
}

/** The map's doodads, for the 3D view (empty when there is no map). */
/** Where the game draws a map's cliff pieces (internal/assets/cliffs.go). */
export interface CliffLayout {
  sets: { name: string; mesh: string }[];
  /** s: set, n: corner letters, v: variation, x/y/z: position, r: quarter turns counter-clockwise. */
  pieces: { s: number; n: string; v: number; x: number; y: number; z: number; r: number }[];
}

/** A replay map's cliff pieces, or null without game data. */
export async function fetchCliffs(id: string): Promise<CliffLayout | null> {
  const resp = await apiFetch(`/api/r/${id}/cliffs`);
  if (resp.status === 404) return null;
  return (await check(resp)).json();
}

export async function fetchDoodads(id: string): Promise<Doodad[]> {
  const resp = await apiFetch(`/api/r/${id}/doodads`);
  if (resp.status === 404) return [];
  return (await (await check(resp)).json()) ?? [];
}

export async function fetchMap(id: string): Promise<MapData | null> {
  const resp = await apiFetch(`/api/r/${id}/map`);
  if (resp.status === 404) return null;
  return (await check(resp)).json();
}

/** A map screenshot or loading screen as an object URL, or null. */
async function imageURL(path: string): Promise<string | null> {
  const resp = await apiFetch(path);
  if (!resp.ok) return null;
  return URL.createObjectURL(await resp.blob());
}

/** The screenshot a replay's map carries, as an object URL, or null. */
export const fetchPreview = (id: string) => imageURL(`/api/r/${id}/preview`);

/** The map's loading screen from the game data, as an object URL, or null. */
export const fetchLoadingScreen = (id: string) => imageURL(`/api/r/${id}/loading`);

/** A directional light: color, intensity multiplier, and the direction it shines (game axes, z up). */
export interface Light {
  color: number[];
  multiplier: number;
  direction: number[];
}

/** A map's lighting from the game data (internal/assets/lights.go). */
export interface Lighting {
  id: string;
  ambient: number[];
  key: Light;
  fill: Light;
  back: Light;
}

/** The map's lighting, or null without game data. */
export async function fetchLighting(id: string): Promise<Lighting | null> {
  const resp = await apiFetch(`/api/r/${id}/lighting`);
  if (!resp.ok) return null;
  return resp.json();
}

/** One of a unit's weapons (internal/assets/info.go); times in clock seconds. */
export interface WeaponInfo {
  name?: string;
  range?: number;
  damage?: number;
  /** Hits per attack, when more than one. */
  attacks?: number;
  bonus?: Record<string, number>;
  cooldown?: number;
  air?: boolean;
  ground?: boolean;
}

/** What the game data says about a unit type. */
export interface UnitInfo {
  name?: string;
  life?: number;
  lifeArmor?: number;
  shields?: number;
  shieldArmor?: number;
  energy?: number;
  minerals?: number;
  vespene?: number;
  /** Supply used; negative for supply provided. */
  supply?: number;
  attributes?: string[];
  sight?: number;
  buildTime?: number;
  /** Starting minerals or gas of a resource. */
  resources?: number;
  weapons?: WeaponInfo[];
}

export interface GameInfo {
  units: Record<string, UnitInfo>;
  upgrades: Record<string, string>;
  abilities: Record<string, string>;
}

/** Stats and display names from the game data, or null without it. */
export async function fetchInfo(units: string[], upgrades: string[], abilities: string[]): Promise<GameInfo | null> {
  const q = new URLSearchParams({ units: units.join(","), upgrades: upgrades.join(","), abilities: abilities.join(",") });
  const resp = await apiFetch(`/api/assets/info?${q}`);
  if (!resp.ok) return null;
  return resp.json();
}

/** The map's texture-mask planes (see MapData.terrainMasks), or null. */
export async function fetchMasks(id: string): Promise<Uint8Array | null> {
  const resp = await apiFetch(`/api/r/${id}/masks`);
  if (!resp.ok || !resp.body) return null;
  return new Uint8Array(await new Response(resp.body.pipeThrough(new DecompressionStream("gzip"))).arrayBuffer());
}

/**
 * Loads the minimap PNG, or null when the map has none. Uses createImageBitmap
 * rather than an <img>: HTMLImageElement.decode() can stall in background tabs.
 */
export async function fetchMinimap(id: string): Promise<ImageBitmap | null> {
  const resp = await apiFetch(`/api/r/${id}/minimap`);
  if (!resp.ok) return null;
  return createImageBitmap(await resp.blob());
}
