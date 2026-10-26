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
}

/** mapfile.Export */
export interface MapData {
  name: string;
  width: number;
  height: number;
  playable: MapRect;
  tileset: string;
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
