// SPDX-License-Identifier: Apache-2.0
//
// Client for the ghostreel HTTP API (internal/server). The same API backs
// `ghostreel serve` and the desktop app, so the viewer has one code path.

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

export type { Doodad } from "./doodads";
import type { Doodad } from "./doodads";

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
  const resp = await fetch("/api/open", {
    method: "POST",
    headers: { "X-Filename": encodeURIComponent(file.name) },
    body: file,
  });
  return (await check(resp)).json();
}

export async function library(): Promise<LibraryEntry[]> {
  return (await (await check(await fetch("/api/library"))).json()) ?? [];
}

export async function openFromLibrary(name: string): Promise<Summary> {
  const resp = await fetch("/api/library/open", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  return (await check(resp)).json();
}

/** Asks the server to fetch the map from Blizzard's depot (user-initiated). */
export async function downloadMap(id: string): Promise<Summary> {
  return (await check(await fetch(`/api/r/${id}/download-map`, { method: "POST" }))).json();
}

/** A replay the desktop app was launched with, or null. */
export async function startup(): Promise<Summary | null> {
  const resp = await fetch("/api/startup");
  return resp.ok ? resp.json() : null;
}

export async function setMapFile(id: string, file: File): Promise<Summary> {
  const resp = await fetch(`/api/r/${id}/map`, {
    method: "POST",
    headers: { "X-Filename": encodeURIComponent(file.name) },
    body: file,
  });
  return (await check(resp)).json();
}

export async function fetchTimeline(id: string): Promise<Uint8Array> {
  return new Uint8Array(await (await check(await fetch(`/api/r/${id}/timeline`))).arrayBuffer());
}

/** The map's doodads, for the 3D view (empty when there is no map). */
export async function fetchDoodads(id: string): Promise<Doodad[]> {
  const resp = await fetch(`/api/r/${id}/doodads`);
  if (resp.status === 404) return [];
  return (await (await check(resp)).json()) ?? [];
}

export async function fetchMap(id: string): Promise<MapData | null> {
  const resp = await fetch(`/api/r/${id}/map`);
  if (resp.status === 404) return null;
  return (await check(resp)).json();
}

/**
 * Loads the minimap PNG, or null when the map has none. Uses createImageBitmap
 * rather than an <img>: HTMLImageElement.decode() can stall in background tabs.
 */
export async function fetchMinimap(id: string): Promise<ImageBitmap | null> {
  const resp = await fetch(`/api/r/${id}/minimap`);
  if (!resp.ok) return null;
  return createImageBitmap(await resp.blob());
}
