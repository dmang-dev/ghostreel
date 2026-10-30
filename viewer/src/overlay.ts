// SPDX-License-Identifier: Apache-2.0
//
// Marks drawn over the scene on its label layer: minimap pings, Xel'Naga
// towers' vision and who holds them, inhibitor and acceleration zones, and
// the selected unit's weapon ranges. Circles are projected point by point,
// so they lie on the ground in 3D as well as 2D.

import { LOOPS_PER_CLOCK_SECOND, type Model } from "./model";
import type { DrawnUnit, Scene } from "./scene";
import { unitStats } from "./names";

/** How long a ping shows, in loops. */
export const PING_LOOPS = 4 * LOOPS_PER_CLOCK_SECOND;

/** A tower is held by a player with a ground unit this close (cells). */
const TOWER_HOLD = 2.5;

/**
 * Zone radii in cells by size: approximate. The game's come from behavior
 * and effect data the catalog reader doesn't follow yet.
 */
const ZONE_RADIUS: Record<string, number> = { Small: 4, Medium: 5, Large: 6 };

export interface OverlayInput {
  scene: Scene;
  model: Model;
  loop: number;
  selected?: number;
  color: (player: number) => string;
}

/** Draws a circle of radius r cells around (x, y) on the ground. */
function circle(ctx: CanvasRenderingContext2D, scene: Scene, x: number, y: number, r: number): void {
  const n = Math.max(24, Math.min(96, Math.round(r * 6)));
  ctx.beginPath();
  for (let i = 0; i <= n; i++) {
    const a = (i / n) * Math.PI * 2;
    const [sx, sy] = scene.worldToScreen(x + Math.cos(a) * r, y + Math.sin(a) * r);
    if (i === 0) ctx.moveTo(sx, sy);
    else ctx.lineTo(sx, sy);
  }
}

export function drawOverlay(ctx: CanvasRenderingContext2D, o: OverlayInput): void {
  const { scene, model, loop } = o;
  const drawn = scene.drawnUnits();
  ctx.save();

  // Towers and zones are neutral units placed on the map.
  for (const d of drawn) {
    if (d.owner !== 0 || d.hidden) continue;
    const name = model.typeName(d.type);
    if (name === "XelNagaTower") {
      const holders = new Set<number>();
      for (const u of drawn) {
        if (u.owner !== 0 && !u.air && !u.w && Math.hypot(u.x - d.x, u.y - d.y) <= TOWER_HOLD) holders.add(u.owner);
      }
      const r = unitStats(name)?.sight || model.types[d.type]?.sight || 22;
      circle(ctx, scene, d.x, d.y, r);
      if (holders.size === 1) {
        const [p] = holders;
        ctx.strokeStyle = o.color(p);
        ctx.globalAlpha = 0.8;
        ctx.setLineDash([]);
      } else {
        ctx.strokeStyle = holders.size ? "#ffffff" : "#b5ecff";
        ctx.globalAlpha = 0.45;
        ctx.setLineDash([6, 6]);
      }
      ctx.lineWidth = 1.5;
      ctx.stroke();
      continue;
    }
    const zone = /^(Inhibitor|Acceleration)Zone(?:Flying)?(Small|Medium|Large)$/.exec(name);
    if (zone) {
      circle(ctx, scene, d.x, d.y, ZONE_RADIUS[zone[2]]);
      const inhibit = zone[1] === "Inhibitor";
      ctx.globalAlpha = 0.18;
      ctx.fillStyle = inhibit ? "#ff9a3d" : "#3dd6ff";
      ctx.fill();
      ctx.globalAlpha = 0.6;
      ctx.strokeStyle = ctx.fillStyle;
      ctx.setLineDash([]);
      ctx.lineWidth = 1;
      ctx.stroke();
    }
  }

  // The selected unit's weapon ranges, measured from its edge as the game does.
  const sel = o.selected !== undefined ? drawn.find((d) => d.id === o.selected) : undefined;
  if (sel && !sel.hidden) drawRanges(ctx, scene, model, sel);

  // Pings: rings that grow and fade.
  ctx.setLineDash([]);
  for (const p of model.pings) {
    if (p.loop > loop || loop - p.loop > PING_LOOPS) continue;
    const f = (loop - p.loop) / PING_LOOPS;
    ctx.strokeStyle = o.color(p.player);
    ctx.lineWidth = 2;
    for (const k of [0, 0.35]) {
      const g = (f + k) % 1;
      ctx.globalAlpha = (1 - g) * (1 - f * 0.5);
      circle(ctx, scene, p.x, p.y, 0.8 + g * 4);
      ctx.stroke();
    }
  }
  ctx.restore();
}

function drawRanges(ctx: CanvasRenderingContext2D, scene: Scene, model: Model, d: DrawnUnit): void {
  const stats = unitStats(model.typeName(d.type));
  if (!stats?.weapons?.length) return;
  const edge = d.w ? d.w / 2 : d.r;
  const ranges = new Set<number>();
  for (const w of stats.weapons) if (w.range && w.range >= 1) ranges.add(w.range);
  ctx.setLineDash([4, 4]);
  ctx.lineWidth = 1.2;
  ctx.strokeStyle = "#ff6b5e";
  ctx.globalAlpha = 0.85;
  for (const r of ranges) {
    circle(ctx, scene, d.x, d.y, r + edge);
    ctx.stroke();
  }
  if (stats.sight) {
    ctx.strokeStyle = "#ffffff";
    ctx.globalAlpha = 0.25;
    circle(ctx, scene, d.x, d.y, stats.sight);
    ctx.stroke();
  }
  ctx.setLineDash([]);
}
