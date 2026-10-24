// SPDX-License-Identifier: Apache-2.0
//
// The director: a camera that watches the game for you. It follows
// Adjutant's priorities:
//
//   1. a hotspot (internal/analysis/hotspots.go) about to start or under way:
//      frame it, a few seconds early;
//   2. otherwise, where units are dying right now;
//   3. otherwise, the bigger army of one player, alternating every 20 s;
//   4. otherwise, the whole map.
//
// Everything is a function of the replay clock (the loop), not wall time:
// pausing holds the shot, and video export gets the same camera on every run.

import type { Model } from "./model";
import { UnitClass, isAlive, positionAt } from "./model";
import type { Marker } from "./reports";

export interface Shot {
  cx: number;
  cy: number;
  /** Cells visible from top to bottom of the view. */
  span: number;
  /** The hotspot being framed, if any. */
  hotspot?: Marker;
}

const LEAD = 22.4 * 3; // frame a hotspot this many loops before it starts
const TAIL = 22.4 * 2; // and keep it this long after
const COMBAT_WINDOW = 22.4 * 8;
const ARMY_TURN = 22.4 * 20;
const POS_TAU = 22.4 * 1.2; // smoothing time constants (loops)
const SPAN_TAU = 22.4 * 1.8;

export class Director {
  private readonly deaths: { loop: number; x: number; y: number }[] = [];
  private shot?: Shot;
  private lastLoop = -1;

  constructor(
    private readonly model: Model,
    private readonly hotspots: Marker[],
    private readonly bounds: { left: number; bottom: number; right: number; top: number },
  ) {
    const p = { x: 0, y: 0, facing: 0 };
    for (const u of model.units) {
      if (u.diedLoop === undefined || u.owner === 0) continue;
      const t = model.types[u.type];
      if (!t || t.class === UnitClass.HIDDEN || t.name === "Larva" || t.name === "Egg") continue;
      positionAt(model.tracks[u.id], 0, u.diedLoop, p);
      this.deaths.push({ loop: u.diedLoop, x: p.x, y: p.y });
    }
    this.deaths.sort((a, b) => a.loop - b.loop);
  }

  /** Where the camera wants to be at loop (no smoothing). */
  target(loop: number): Shot {
    // 1. Hotspots, the highest scored first.
    let best: Marker | undefined;
    for (const h of this.hotspots) {
      if (loop < h.loop - LEAD || loop > (h.end ?? h.loop) + TAIL) continue;
      if (!best || (h.score ?? 0) > (best.score ?? 0)) best = h;
    }
    if (best && best.x !== undefined && best.y !== undefined) {
      return { cx: best.x, cy: best.y, span: clamp((best.r ?? 6) * 2.8, 18, 70), hotspot: best };
    }
    // 2. Recent deaths.
    let n = 0, sx = 0, sy = 0;
    for (let i = upperBound(this.deaths, loop) - 1; i >= 0 && loop - this.deaths[i].loop < COMBAT_WINDOW; i--) {
      sx += this.deaths[i].x;
      sy += this.deaths[i].y;
      n++;
    }
    if (n >= 2) return { cx: sx / n, cy: sy / n, span: 30 };
    // 3. An army, alternating between players.
    const players = this.model.tl.meta?.players ?? [];
    if (players.length) {
      const order = Math.floor(loop / ARMY_TURN);
      for (let k = 0; k < players.length; k++) {
        const army = this.armyCenter(players[(order + k) % players.length].id, loop);
        if (army) return { ...army, span: 34 };
      }
    }
    // 4. The whole map.
    const b = this.bounds;
    return { cx: (b.left + b.right) / 2, cy: (b.bottom + b.top) / 2, span: b.top - b.bottom };
  }

  /** The center of a player's densest group of army units, if it has six. */
  private armyCenter(player: number, loop: number): { cx: number; cy: number } | undefined {
    const pts: { x: number; y: number }[] = [];
    const p = { x: 0, y: 0, facing: 0 };
    for (const u of this.model.units) {
      if (u.owner !== player || !isAlive(u, loop)) continue;
      const t = this.model.types[u.type];
      if (!t || t.class !== UnitClass.ARMY || !t.speed || t.name === "Larva" || t.name === "Egg") continue;
      positionAt(this.model.tracks[u.id], t.speed, loop, p);
      pts.push({ x: p.x, y: p.y });
    }
    if (pts.length < 6) return undefined;
    // The unit with the most others within 12 cells anchors the group.
    let anchor = pts[0], most = -1;
    for (const a of pts) {
      let c = 0;
      for (const b of pts) if ((a.x - b.x) ** 2 + (a.y - b.y) ** 2 < 144) c++;
      if (c > most) [anchor, most] = [a, c];
    }
    let sx = 0, sy = 0, n = 0;
    for (const b of pts) {
      if ((anchor.x - b.x) ** 2 + (anchor.y - b.y) ** 2 < 144) {
        sx += b.x;
        sy += b.y;
        n++;
      }
    }
    return { cx: sx / n, cy: sy / n };
  }

  /**
   * The camera at loop, eased from where it was. A seek (or the first call)
   * cuts straight to the target.
   */
  shotAt(loop: number): Shot {
    const t = this.target(loop);
    const prev = this.shot;
    const dt = loop - this.lastLoop;
    this.lastLoop = loop;
    if (!prev || dt < 0 || dt > 22.4 * 4) {
      this.shot = t;
      return t;
    }
    const kp = 1 - Math.exp(-dt / POS_TAU);
    const ks = 1 - Math.exp(-dt / SPAN_TAU);
    this.shot = {
      cx: prev.cx + (t.cx - prev.cx) * kp,
      cy: prev.cy + (t.cy - prev.cy) * kp,
      span: prev.span + (t.span - prev.span) * ks,
      hotspot: t.hotspot,
    };
    return this.shot;
  }

  /** Forget the camera so the next shot cuts (after a seek or a reel cut). */
  reset(): void {
    this.shot = undefined;
    this.lastLoop = -1;
  }
}

/**
 * The highlight reel: the best hotspots that don't overlap (with a gap), at
 * most n, in game order. As in Adjutant's scorer.
 */
export function selectReel(hotspots: Marker[], n: number, gap = 22.4 * 10): Marker[] {
  const ranked = [...hotspots].filter((h) => (h.score ?? 0) > 0).sort((a, b) => (b.score ?? 0) - (a.score ?? 0));
  const picked: Marker[] = [];
  for (const h of ranked) {
    if (picked.length >= n) break;
    const overlaps = picked.some((p) => !((h.end ?? h.loop) + gap < p.loop || (p.end ?? p.loop) + gap < h.loop));
    if (!overlaps) picked.push(h);
  }
  return picked.sort((a, b) => a.loop - b.loop);
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

function upperBound(a: { loop: number }[], loop: number): number {
  let lo = 0, hi = a.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (a[mid].loop > loop) hi = mid;
    else lo = mid + 1;
  }
  return lo;
}
