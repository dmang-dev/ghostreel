// SPDX-License-Identifier: Apache-2.0
//
// Water and lava (the map's t3Water areas, GET /api/r/{id}/water). Each
// area is a rectangle whose surface lies at its type's height, so it shows
// only where that is above the ground: maps often keep areas whose water
// sits under the terrain. In 3D the surface is a flat sheet at that height
// and the terrain hides what's below ground; in 2D, which has no depth, the
// cells whose ground is below the surface are colored instead.

import type { WaterArea, WaterType } from "./api";

/** How opaque a surface draws: lava glows through, water lets ground show. */
export function surfaceOpacity(t: WaterType): number {
  return t.lava ? 0.92 : Math.min(0.85, Math.max(0.5, t.color[3] * 2));
}

/**
 * Per-cell RGBA for the 2D view: a type's color where its surface is above
 * the cell's ground (the mean of the cell's corner heights), clear
 * elsewhere. corners holds (w+1)*(h+1) heights, row 0 at y=0. Returns
 * undefined when no cell shows water.
 */
export function waterCells(
  areas: readonly WaterArea[], types: Readonly<Record<string, WaterType>>,
  corners: ArrayLike<number>, w: number, h: number,
): Uint8Array | undefined {
  const out = new Uint8Array(w * h * 4);
  let any = false;
  for (const a of areas) {
    const t = types[a.type];
    if (!t) continue;
    const alpha = Math.round(surfaceOpacity(t) * 255);
    for (let y = Math.max(0, Math.floor(a.y0)); y < Math.min(h, Math.ceil(a.y1)); y++) {
      for (let x = Math.max(0, Math.floor(a.x0)); x < Math.min(w, Math.ceil(a.x1)); x++) {
        const i = y * (w + 1) + x;
        const ground = (corners[i] + corners[i + 1] + corners[i + w + 1] + corners[i + w + 2]) / 4;
        if (ground >= t.height) continue;
        const o = (y * w + x) * 4;
        out[o] = Math.round(t.color[0] * 255);
        out[o + 1] = Math.round(t.color[1] * 255);
        out[o + 2] = Math.round(t.color[2] * 255);
        out[o + 3] = alpha;
        any = true;
      }
    }
  }
  return any ? out : undefined;
}

/**
 * Positions (x, y, z) and triangle indices of the flat sheets of one water
 * type's areas, at its surface height, for the 3D view.
 */
export function waterSheets(areas: readonly WaterArea[], height: number): { positions: Float32Array; indices: Uint32Array } {
  const positions = new Float32Array(areas.length * 12);
  const indices = new Uint32Array(areas.length * 6);
  areas.forEach((a, i) => {
    positions.set([a.x0, a.y0, height, a.x1, a.y0, height, a.x1, a.y1, height, a.x0, a.y1, height], i * 12);
    const v = i * 4;
    indices.set([v, v + 1, v + 2, v, v + 2, v + 3], i * 6);
  });
  return { positions, indices };
}
