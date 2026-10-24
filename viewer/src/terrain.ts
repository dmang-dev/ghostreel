// SPDX-License-Identifier: Apache-2.0
//
// Terrain images drawn from map data: a hillshade (same lighting as the Go
// side's dump-map PNG), the map's own minimap, or a plain grid when the map
// is missing. Canvas row 0 is the top of the map (high y).

import type { MapData } from "./api";

const PX = 4; // pixels per cell
const RELIEF = 6; // slope exaggeration in the hillshade

export function hillshade(map: MapData): HTMLCanvasElement {
  const { width: w, height: h, heights, pathable, playable: p } = map;
  const c = document.createElement("canvas");
  c.width = w * PX;
  c.height = h * PX;
  const ctx = c.getContext("2d")!;
  if (!heights || heights.length !== (w + 1) * (h + 1)) return blankGrid(w, h, p);

  const vw = w + 1;
  const at = (x: number, y: number) => {
    x = Math.min(Math.max(x, 0), w - 1);
    y = Math.min(Math.max(y, 0), h - 1);
    return (heights[y * vw + x] + heights[y * vw + x + 1] + heights[(y + 1) * vw + x] + heights[(y + 1) * vw + x + 1]) / 4;
  };
  let lo = Infinity;
  let hi = -Infinity;
  for (let y = p.Bottom; y < p.Top; y++)
    for (let x = p.Left; x < p.Right; x++) {
      const v = at(x, y);
      if (v < lo) lo = v;
      if (v > hi) hi = v;
    }
  const span = Math.max(hi - lo, 0.5);
  const img = ctx.createImageData(w, h);
  const [lx, ly, lz] = [-0.5, 0.5, 0.7071];
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      // Slopes exaggerated so cliffs (2 units high) read clearly.
      const dzdx = ((at(x + 1, y) - at(x - 1, y)) * RELIEF) / 2;
      const dzdy = ((at(x, y + 1) - at(x, y - 1)) * RELIEF) / 2;
      const n = Math.hypot(dzdx, dzdy, 1);
      const shade = Math.max(0.15, (-dzdx * lx - dzdy * ly + lz) / n);
      const t = Math.min(1, Math.max(0, (at(x, y) - lo) / span));
      let r = (60 + 140 * t) * shade;
      let g = (70 + 120 * t) * shade;
      let b = (85 + 70 * t) * shade;
      const inside = x >= p.Left && x < p.Right && y >= p.Bottom && y < p.Top;
      if (!inside) {
        r /= 3; g /= 3; b /= 3;
      } else if (pathable && pathable[y * w + x] === 0) {
        r *= 0.55; g *= 0.55; b *= 0.6;
      }
      const i = ((h - 1 - y) * w + x) * 4;
      img.data[i] = r;
      img.data[i + 1] = g;
      img.data[i + 2] = b;
      img.data[i + 3] = 255;
    }
  }
  // Draw at 1 px per cell, then scale up smoothly.
  const small = document.createElement("canvas");
  small.width = w;
  small.height = h;
  small.getContext("2d")!.putImageData(img, 0, 0);
  ctx.imageSmoothingEnabled = true;
  ctx.drawImage(small, 0, 0, w * PX, h * PX);
  return c;
}

/**
 * The map's own minimap art placed on the world. Minimap.tga shows only the
 * playable area, at max(imageW, imageH) / 256 pixels per cell, centered in
 * the image (see docs/MAP_FORMAT_NOTES.md).
 */
export function minimapTerrain(map: MapData, img: ImageBitmap): HTMLCanvasElement {
  const { width: w, height: h, playable: p } = map;
  const c = document.createElement("canvas");
  c.width = w * PX;
  c.height = h * PX;
  const ctx = c.getContext("2d")!;
  ctx.fillStyle = "#0b0d10";
  ctx.fillRect(0, 0, c.width, c.height);
  const pw = p.Right - p.Left;
  const ph = p.Top - p.Bottom;
  const s = Math.max(img.width, img.height) / 256;
  const sx = (img.width - pw * s) / 2;
  const sy = (img.height - ph * s) / 2;
  ctx.imageSmoothingEnabled = true;
  ctx.drawImage(img, sx, sy, pw * s, ph * s, p.Left * PX, (h - p.Top) * PX, pw * PX, ph * PX);
  return c;
}

/** Placeholder terrain for replays whose map couldn't be found. */
export function blankGrid(w: number, h: number, p?: { Left: number; Bottom: number; Right: number; Top: number }): HTMLCanvasElement {
  const c = document.createElement("canvas");
  c.width = w * PX;
  c.height = h * PX;
  const ctx = c.getContext("2d")!;
  ctx.fillStyle = "#14181d";
  ctx.fillRect(0, 0, c.width, c.height);
  if (p) {
    ctx.fillStyle = "#1c2229";
    ctx.fillRect(p.Left * PX, (h - p.Top) * PX, (p.Right - p.Left) * PX, (p.Top - p.Bottom) * PX);
  }
  ctx.strokeStyle = "rgba(255,255,255,0.06)";
  ctx.lineWidth = 1;
  for (let x = 0; x <= w; x += 8) {
    ctx.beginPath();
    ctx.moveTo(x * PX + 0.5, 0);
    ctx.lineTo(x * PX + 0.5, c.height);
    ctx.stroke();
  }
  for (let y = 0; y <= h; y += 8) {
    ctx.beginPath();
    ctx.moveTo(0, c.height - y * PX + 0.5);
    ctx.lineTo(c.width, c.height - y * PX + 0.5);
    ctx.stroke();
  }
  return c;
}
