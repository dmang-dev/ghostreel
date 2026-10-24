// SPDX-License-Identifier: Apache-2.0
//
// Timeline model: decodes a timeline and answers "what does the game look
// like at loop L". This mirrors internal/timeline/seek.go; the two are kept
// in step by golden files (see model.test.ts). Evaluating every unit per
// frame costs O(units), independent of L, so the viewer doesn't need the
// snapshots that the Go side uses for its alive set.

import { fromBinary } from "@bufbuild/protobuf";
import {
  type Keyframe,
  type PlayerStats,
  type Timeline,
  type Unit,
  type UnitType,
  TimelineSchema,
  SchemaVersion,
  UnitClass,
} from "./gen/timeline/v1/timeline_pb";

export const LOOPS_PER_CLOCK_SECOND = 22.4;
export const LOOPS_PER_GAME_SECOND = 16;
/** Speed used for units of unknown speed (cells per game second). */
export const DEFAULT_SPEED = 2.5;

export { UnitClass };
export type { Timeline, Unit, UnitType, PlayerStats };

/** Formats a loop the way the in-game clock shows it (truncating). */
export function formatClock(loop: number): string {
  const s = Math.floor(loop / LOOPS_PER_CLOCK_SECOND);
  const pad = (n: number) => String(n).padStart(2, "0");
  if (s >= 3600) return `${Math.floor(s / 3600)}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`;
  return `${Math.floor(s / 60)}:${pad(s % 60)}`;
}

export function decodeTimeline(bytes: Uint8Array): Timeline {
  const tl = fromBinary(TimelineSchema, bytes);
  if (tl.schemaVersion !== SchemaVersion.SCHEMA_VERSION_1) {
    throw new Error(`unsupported timeline schema version ${tl.schemaVersion}`);
  }
  return tl;
}

/** One unit at one loop. */
export interface UnitState {
  id: number;
  type: number;
  owner: number;
  x: number;
  y: number;
  facing: number;
  constructing: boolean;
}

/** Float32 rounding, so results compare exactly with the Go side's float32 fields. */
const f32 = Math.fround;

/** Position on a track at loop: hold, then travel to arrive on time (see seek.go). */
export function positionAt(
  keys: readonly Keyframe[] | undefined,
  speed: number,
  loop: number,
  out: { x: number; y: number; facing: number },
): void {
  if (!keys || keys.length === 0) {
    out.x = out.y = out.facing = 0;
    return;
  }
  // First key after loop.
  let lo = 0;
  let hi = keys.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (keys[mid].loop > loop) hi = mid;
    else lo = mid + 1;
  }
  const i = lo;
  if (i === 0) {
    out.x = keys[0].x; out.y = keys[0].y; out.facing = keys[0].facing;
    return;
  }
  const k0 = keys[i - 1];
  if (i === keys.length || k0.loop === loop) {
    out.x = k0.x; out.y = k0.y; out.facing = k0.facing;
    return;
  }
  const k1 = keys[i];
  const dx = f32(k1.x - k0.x);
  const dy = f32(k1.y - k0.y);
  const dist = Math.hypot(dx, dy);
  if (dist < 1e-6) {
    out.x = k0.x; out.y = k0.y; out.facing = k0.facing;
    return;
  }
  if (speed <= 0) speed = DEFAULT_SPEED;
  const travel = dist / (f32(speed) / LOOPS_PER_GAME_SECOND);
  const depart = Math.max(k0.loop, k1.loop - travel);
  if (loop <= depart) {
    out.x = k0.x; out.y = k0.y; out.facing = k0.facing;
    return;
  }
  const f = (loop - depart) / (k1.loop - depart);
  out.x = f32(k0.x + f32(dx * f));
  out.y = f32(k0.y + f32(dy * f));
  out.facing = f32(Math.atan2(dy, dx));
}

export function typeAt(u: Unit, loop: number): number {
  let t = u.type;
  for (const m of u.morphs) {
    if (m.loop > loop) break;
    t = m.type;
  }
  return t;
}

export function ownerAt(u: Unit, loop: number): number {
  let o = u.owner;
  for (const c of u.ownerChanges) {
    if (c.loop > loop) break;
    o = c.owner;
  }
  return o;
}

export function isAlive(u: Unit, loop: number): boolean {
  return u.bornLoop <= loop && (u.diedLoop === undefined || u.diedLoop > loop);
}

/** Feed entry derived from timeline events. */
export interface FeedItem {
  loop: number;
  player: number;
  text: string;
  unitId?: number;
}

/** Units too common or transient to list in the event feed. */
const QUIET_TYPES = new Set([
  "Larva", "Egg", "Broodling", "LocustMP", "LocustMPFlying", "Interceptor", "AdeptPhaseShift",
  "CreepTumor", "CreepTumorBurrowed", "CreepTumorQueen", "MULE", "AutoTurret", "BanelingCocoon",
  "Changeling", "ChangelingMarine", "ChangelingMarineShield", "ChangelingZealot", "ChangelingZergling",
  "ChangelingZerglingWings",
]);

export class Model {
  readonly tl: Timeline;
  readonly units: Unit[];
  readonly types: UnitType[];
  readonly loops: number;
  readonly tracks: (Keyframe[] | undefined)[];
  /** PlayerStats events by player, in loop order. */
  private readonly stats = new Map<number, { loops: number[]; stats: PlayerStats[] }>();
  /** Loops at which non-quiet units died, sorted (for battle markers). */
  readonly deathLoops: number[] = [];
  readonly feed: FeedItem[] = [];

  constructor(tl: Timeline) {
    this.tl = tl;
    this.units = tl.units;
    this.types = tl.unitTypes;
    this.loops = tl.meta?.loops ?? 0;
    this.tracks = new Array(tl.units.length);
    for (const t of tl.tracks) this.tracks[t.unitId] = t.keyframes;
    for (const e of tl.events) {
      const k = e.kind;
      switch (k.case) {
        case "playerStats": {
          let s = this.stats.get(k.value.player);
          if (!s) this.stats.set(k.value.player, (s = { loops: [], stats: [] }));
          s.loops.push(e.loop);
          s.stats.push(k.value);
          break;
        }
        case "unitDied": {
          const u = this.units[k.value.unitId];
          const name = this.typeName(typeAt(u, e.loop));
          const cls = this.types[typeAt(u, e.loop)]?.class;
          if (u.owner !== 0 && !QUIET_TYPES.has(name) && cls !== UnitClass.HIDDEN) {
            this.deathLoops.push(e.loop);
            this.feed.push({ loop: e.loop, player: ownerAt(u, e.loop), text: `${name} died`, unitId: u.id });
          }
          break;
        }
        case "unitBorn": {
          const u = this.units[k.value.unitId];
          const t = this.types[u.type];
          if (u.owner !== 0 && u.constructed && e.loop > 0) {
            this.feed.push({ loop: e.loop, player: u.owner, text: `${t.name} started`, unitId: u.id });
          } else if (u.owner !== 0 && e.loop > 0 && t.class === UnitClass.ARMY && !QUIET_TYPES.has(t.name)) {
            this.feed.push({ loop: e.loop, player: u.owner, text: `${t.name} trained`, unitId: u.id });
          }
          break;
        }
        case "unitDone": {
          const u = this.units[k.value.unitId];
          this.feed.push({ loop: e.loop, player: ownerAt(u, e.loop), text: `${this.typeName(typeAt(u, e.loop))} finished`, unitId: u.id });
          break;
        }
        case "upgrade":
          if (e.loop > 0) this.feed.push({ loop: e.loop, player: k.value.player, text: `Upgrade: ${k.value.name}` });
          break;
        case "chat":
          this.feed.push({ loop: e.loop, player: k.value.player, text: `“${k.value.text}”` });
          break;
      }
    }
  }

  typeName(t: number): string {
    return this.types[t]?.name ?? "?";
  }

  /** Latest stats for player at or before loop. */
  statsAt(player: number, loop: number): PlayerStats | undefined {
    const s = this.stats.get(player);
    if (!s) return undefined;
    let lo = 0;
    let hi = s.loops.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (s.loops[mid] > loop) hi = mid;
      else lo = mid + 1;
    }
    return lo > 0 ? s.stats[lo - 1] : undefined;
  }

  /** Every unit alive at loop, sorted by ID. Allocates; the renderer uses evalUnit. */
  stateAt(loop: number): UnitState[] {
    const out: UnitState[] = [];
    for (const u of this.units) {
      if (!isAlive(u, loop)) continue;
      const s: UnitState = { id: u.id, type: 0, owner: 0, x: 0, y: 0, facing: 0, constructing: false };
      this.evalUnit(u, loop, s);
      out.push(s);
    }
    return out;
  }

  /** Fills out with the unit's state at loop (the unit must be alive). */
  evalUnit(u: Unit, loop: number, out: UnitState): void {
    const t = typeAt(u, loop);
    out.id = u.id;
    out.type = t;
    out.owner = ownerAt(u, loop);
    out.constructing = u.constructed && (u.doneLoop === undefined || u.doneLoop > loop);
    positionAt(this.tracks[u.id], this.types[t]?.speed ?? 0, loop, out);
  }

  /** Loop of the last keyframe at or before loop (when the unit was last actually seen). */
  lastSeen(id: number, loop: number): number | undefined {
    const keys = this.tracks[id];
    if (!keys) return undefined;
    let best: number | undefined;
    for (const k of keys) {
      if (k.loop > loop) break;
      best = k.loop;
    }
    return best;
  }
}
