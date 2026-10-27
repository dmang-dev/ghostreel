// SPDX-License-Identifier: Apache-2.0
//
// Mining for players whose orders the replay doesn't hold: the built-in AI
// (it plays inside the game, so it issues no commands) or any player when a
// timeline has only tracker positions. Their workers would otherwise stand
// where they were last seen, usually beside a base, for most of the game.
//
// A worker of such a player that the timeline holds still near one of its
// owner's finished bases walks a gather round trip here instead, between
// the base and a mineral field or refinery of that base, the way the
// command tier moves commanded workers (internal/command). Which field is
// a guess. This only changes what is drawn: the timeline, and the measured
// positions, stay as they are (docs/DECISIONS.md explains why the estimate
// isn't in the timeline).

import { Source, type Keyframe, type Unit, type UnitType } from "./gen/timeline/v1/timeline_pb";

const TOWNHALLS = new Set(["CommandCenter", "OrbitalCommand", "PlanetaryFortress", "Nexus", "Hatchery", "Lair", "Hive"]);
const REFINERY = /^(Assimilator|Refinery|Extractor)(Rich)?$/;
const CYCLE = 112; // loops per gather round trip (the command tier's, measured on 4.10)
const BASE_R = 2.75; // a town hall's radius, where workers drop off
const NEAR_BASE = 10; // cells: a worker held this close to a base mines from it
const FIELD_REACH = 10; // cells: a base's fields and refineries
const BLEND = 22; // loops to ease into and out of the trip
const STILL = 0.5; // cells: two keyframes this close mean "standing still"
const GAS_PER_REFINERY = 3;
const WORKERS_PER_BASE = 16 + 6;
const LOOPS_PER_GAME_SECOND = 16;

interface Site {
  u: Unit;
  x: number;
  y: number;
  r: number;
}

interface Out {
  x: number;
  y: number;
  facing: number;
  moving?: boolean;
}

/** Stable pseudo-random 0..1 per unit, so each worker keeps its field and pace. */
function hash01(id: number, salt: number): number {
  let h = Math.imul(id ^ salt, 0x9e3779b1);
  h ^= h >>> 15;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  return (h >>> 0) / 4294967296;
}

export class Mining {
  /** Players whose workers are animated. */
  readonly players = new Set<number>();
  private readonly halls: Site[] = [];
  private readonly fields: Site[] = [];
  private readonly refineries: Site[] = [];

  constructor(
    units: readonly Unit[],
    private readonly types: readonly UnitType[],
    tracks: readonly (Keyframe[] | undefined)[],
    private readonly ownerAt: (u: Unit, loop: number) => number,
    private readonly isWorker: (type: number) => boolean,
  ) {
    const owners = new Set<number>();
    const ordered = new Set<number>();
    for (const u of units) {
      if (u.owner !== 0) owners.add(u.owner);
      const keys = tracks[u.id];
      if (keys?.some((k) => k.source === Source.COMMAND || k.source === Source.ENGINE)) ordered.add(u.owner);
      const k = keys?.[0];
      if (!k) continue;
      const t = types[u.type];
      const name = t?.name ?? "";
      const site = { u, x: k.x, y: k.y, r: Math.max(t?.radius ?? 0, (t?.footprintW ?? 0) / 2) };
      // A hall may be born as one (Nexus) or become one (a Hatchery morphs on).
      const hall = TOWNHALLS.has(name) || u.morphs.some((m) => TOWNHALLS.has(types[m.type]?.name ?? ""));
      if (u.owner === 0 && name.includes("MineralField")) this.fields.push(site);
      else if (u.owner !== 0 && REFINERY.test(name)) this.refineries.push({ ...site, r: Math.max(site.r, 1.5) });
      else if (u.owner !== 0 && hall) this.halls.push(site);
    }
    for (const p of owners) if (!ordered.has(p)) this.players.add(p);
  }

  /**
   * Moves out along a gather trip if u is a worker of an animated player
   * that the timeline holds still near a base at loop. speed is in cells
   * per game second.
   */
  apply(u: Unit, type: number, owner: number, keys: readonly Keyframe[] | undefined, loop: number, speed: number, out: Out): void {
    if (!this.players.has(owner) || !this.isWorker(type) || !keys?.length || speed <= 0) return;
    // The keyframes around loop: still between them?
    let lo = 0;
    let hi = keys.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (keys[mid].loop > loop) hi = mid;
      else lo = mid + 1;
    }
    if (lo === 0) return;
    const k0 = keys[lo - 1];
    const k1 = keys[lo];
    if (k1 && Math.hypot(k1.x - k0.x, k1.y - k0.y) > STILL) return;
    const end = k1?.loop ?? u.diedLoop ?? Infinity;

    const hall = this.nearestHall(owner, k0.x, k0.y, loop);
    if (!hall) return;
    const site = this.siteFor(u.id, owner, hall, loop);
    if (!site) return;

    // The trip: the field's edge and the base's edge, on the line between them.
    const r = this.types[type]?.radius ?? 0.375;
    const dx = hall.x - site.x;
    const dy = hall.y - site.y;
    const d = Math.max(Math.hypot(dx, dy), 1e-6);
    const ux = dx / d;
    const uy = dy / d;
    const fx = site.x + ux * (site.r + r);
    const fy = site.y + uy * (site.r + r);
    const bx = hall.x - ux * (BASE_R + r);
    const by = hall.y - uy * (BASE_R + r);
    const perLoop = speed / LOOPS_PER_GAME_SECOND;
    const travel = Math.max(Math.hypot(fx - bx, fy - by) / perLoop, 1);
    const dwell = Math.max(8, CYCLE - 2 * travel);
    const period = 2 * travel + dwell;
    const t = (loop + hash01(u.id, 7) * period) % period;
    let ax: number, ay: number, facing: number, moving: boolean;
    if (t < travel) {
      const f = t / travel;
      ax = bx + (fx - bx) * f; ay = by + (fy - by) * f;
      facing = Math.atan2(fy - by, fx - bx); moving = true;
    } else if (t < travel + dwell) {
      ax = fx; ay = fy;
      facing = Math.atan2(-uy, -ux); moving = false;
    } else {
      const f = (t - travel - dwell) / travel;
      ax = fx + (bx - fx) * f; ay = fy + (by - fy) * f;
      facing = Math.atan2(by - fy, bx - fx); moving = true;
    }
    // Ease in after the worker was last placed, and out before it moves on.
    const w = Math.min(1, (loop - k0.loop) / BLEND, (end - loop) / BLEND);
    if (w <= 0) return;
    out.x = k0.x + (ax - k0.x) * w;
    out.y = k0.y + (ay - k0.y) * w;
    out.facing = facing;
    out.moving = moving && w > 0.5;
  }

  private nearestHall(owner: number, x: number, y: number, loop: number): Site | undefined {
    let best: Site | undefined;
    let bestD = NEAR_BASE;
    for (const h of this.halls) {
      if (!this.ready(h.u, loop) || this.ownerAt(h.u, loop) !== owner) continue;
      const d = Math.hypot(h.x - x, h.y - y);
      if (d < bestD) {
        best = h;
        bestD = d;
      }
    }
    return best;
  }

  /** A finished, living building (or a field) at loop. */
  private ready(u: Unit, loop: number): boolean {
    if (u.bornLoop > loop || (u.diedLoop !== undefined && u.diedLoop <= loop)) return false;
    return !u.constructed || (u.doneLoop !== undefined && u.doneLoop <= loop);
  }

  /** The field or refinery this worker gathers from at this base. */
  private siteFor(id: number, owner: number, hall: Site, loop: number): Site | undefined {
    const near = (s: Site) => Math.hypot(s.x - hall.x, s.y - hall.y) <= FIELD_REACH && this.ready(s.u, loop);
    const gas = this.refineries.filter((s) => near(s) && this.ownerAt(s.u, loop) === owner);
    const pick = hash01(id, 3);
    if (gas.length && pick < (gas.length * GAS_PER_REFINERY) / WORKERS_PER_BASE) {
      return gas[Math.floor(hash01(id, 5) * gas.length)];
    }
    const fields = this.fields.filter(near);
    if (!fields.length) return gas[0];
    return fields[Math.floor(hash01(id, 11) * fields.length)];
  }
}
