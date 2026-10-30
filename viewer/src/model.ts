// SPDX-License-Identifier: Apache-2.0
//
// Timeline model: decodes a timeline and answers "what does the game look
// like at loop L". This mirrors internal/timeline/seek.go; the two are kept
// in step by golden files (see model.test.ts). Evaluating every unit per
// frame costs O(units), independent of L, so the viewer doesn't need the
// snapshots that the Go side uses for its alive set.

import { fromBinary } from "@bufbuild/protobuf";
import {
  type CameraKey,
  type Keyframe,
  type PlayerStats,
  type Timeline,
  type Unit,
  type UnitType,
  type Vitals,
  TimelineSchema,
  SchemaVersion,
  UnitClass,
  UnitState as UnitStateKind,
  Source,
} from "./gen/timeline/v1/timeline_pb";
import { Mining } from "./mining";

export const LOOPS_PER_CLOCK_SECOND = 22.4;
export const LOOPS_PER_GAME_SECOND = 16;
/** Speed used for units of unknown speed (cells per game second). */
export const DEFAULT_SPEED = 2.5;
/** Build time assumed for a type no building of which finished (~45 s on the clock). */
const DEFAULT_BUILD_LOOPS = 1000;

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
  /** Travelling between keyframes at this loop. */
  moving?: boolean;
  /** The last keyframe says the unit is attacking. */
  attacking?: boolean;
}

/** Float32 rounding, so results compare exactly with the Go side's float32 fields. */
const f32 = Math.fround;

/** Position on a track at loop: hold, then travel to arrive on time (see seek.go). */
export function positionAt(
  keys: readonly Keyframe[] | undefined,
  speed: number,
  loop: number,
  out: { x: number; y: number; facing: number; moving?: boolean; attacking?: boolean },
): void {
  out.moving = out.attacking = false;
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
  out.attacking = k0.state === UnitStateKind.ATTACKING;
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
  out.moving = true;
  out.attacking = false;
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

/** What a feed entry reports; the viewer words it (with display names). */
export type FeedKind = "died" | "started" | "trained" | "finished" | "upgrade" | "chat" | "ping";

/** Feed entry derived from timeline events. */
export interface FeedItem {
  loop: number;
  player: number;
  kind: FeedKind;
  /** Unit type or upgrade id, or the chat text. */
  subject: string;
  unitId?: number;
  /** Deaths: the unit that dealt the killing blow, when known. */
  killerId?: number;
  /** Pings: where. */
  x?: number;
  y?: number;
}

/** A minimap ping. */
export interface Ping {
  loop: number;
  player: number;
  x: number;
  y: number;
}

/** Units too common or transient to list in the event feed. */
const QUIET_TYPES = new Set([
  "Larva", "Egg", "Broodling", "LocustMP", "LocustMPFlying", "Interceptor", "AdeptPhaseShift",
  "CreepTumor", "CreepTumorBurrowed", "CreepTumorQueen", "MULE", "AutoTurret", "BanelingCocoon",
  "Changeling", "ChangelingMarine", "ChangelingMarineShield", "ChangelingZealot", "ChangelingZergling",
  "ChangelingZerglingWings", "KD8Charge",
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
  /** Drawn mining for workers of players without orders (mining.ts). */
  readonly mining: Mining;
  /** Health, shields and energy by unit, engine tier only (replays don't record them). */
  readonly vitals: (Vitals[] | undefined)[];
  /** Whether any unit has them. */
  readonly hasVitals: boolean;
  /** Typical construction time per type in loops, from buildings that finished. */
  private readonly buildLoops = new Map<number, number>();
  /** Loops at which each unit got a kill (killing blows), sorted, by killer id. */
  private readonly kills = new Map<number, number[]>();
  /** Each player's camera keys, in loop order. */
  private readonly cameras = new Map<number, CameraKey[]>();
  /** Minimap pings, in loop order. */
  readonly pings: Ping[] = [];

  /** opts.mining: false leaves workers where the timeline puts them (tests against Go). */
  constructor(tl: Timeline, opts: { mining?: boolean } = {}) {
    this.tl = tl;
    this.units = tl.units;
    this.types = tl.unitTypes;
    this.loops = tl.meta?.loops ?? 0;
    this.tracks = new Array(tl.units.length);
    this.vitals = new Array(tl.units.length);
    let hasVitals = false;
    for (const t of tl.tracks) {
      this.tracks[t.unitId] = t.keyframes;
      if (t.vitals.length) {
        this.vitals[t.unitId] = t.vitals;
        hasVitals = true;
      }
    }
    this.hasVitals = hasVitals;
    this.mining = new Mining(this.units, this.types, this.tracks, ownerAt, (t) => this.types[t]?.class === UnitClass.WORKER);
    if (opts.mining === false) this.mining.players.clear();
    const durations = new Map<number, number[]>();
    for (const u of this.units) {
      if (!u.constructed || u.doneLoop === undefined) continue;
      let d = durations.get(u.type);
      if (!d) durations.set(u.type, (d = []));
      d.push(u.doneLoop - u.bornLoop);
    }
    for (const [t, d] of durations) this.buildLoops.set(t, d.sort((a, b) => a - b)[d.length >> 1]);
    for (const c of tl.cameras) this.cameras.set(c.player, c.keys);
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
          if (u.killer !== undefined && u.killer !== u.id) {
            let k = this.kills.get(u.killer);
            if (!k) this.kills.set(u.killer, (k = []));
            k.push(e.loop);
          }
          if (u.owner !== 0 && !QUIET_TYPES.has(name) && cls !== UnitClass.HIDDEN) {
            this.deathLoops.push(e.loop);
            this.feed.push({ loop: e.loop, player: ownerAt(u, e.loop), kind: "died", subject: name, unitId: u.id, killerId: u.killer });
          }
          break;
        }
        case "unitBorn": {
          const u = this.units[k.value.unitId];
          const t = this.types[u.type];
          if (u.owner !== 0 && u.constructed && e.loop > 0) {
            this.feed.push({ loop: e.loop, player: u.owner, kind: "started", subject: t.name, unitId: u.id });
          } else if (u.owner !== 0 && e.loop > 0 && t.class === UnitClass.ARMY && !QUIET_TYPES.has(t.name)) {
            this.feed.push({ loop: e.loop, player: u.owner, kind: "trained", subject: t.name, unitId: u.id });
          }
          break;
        }
        case "unitDone": {
          const u = this.units[k.value.unitId];
          this.feed.push({ loop: e.loop, player: ownerAt(u, e.loop), kind: "finished", subject: this.typeName(typeAt(u, e.loop)), unitId: u.id });
          break;
        }
        case "upgrade":
          if (e.loop > 0) this.feed.push({ loop: e.loop, player: k.value.player, kind: "upgrade", subject: k.value.name });
          break;
        case "chat":
          this.feed.push({ loop: e.loop, player: k.value.player, kind: "chat", subject: k.value.text });
          break;
        case "ping":
          this.pings.push({ loop: e.loop, player: k.value.player, x: k.value.x, y: k.value.y });
          this.feed.push({ loop: e.loop, player: k.value.player, kind: "ping", subject: "", x: k.value.x, y: k.value.y });
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
    const speed = this.types[t]?.speed ?? 0;
    positionAt(this.tracks[u.id], speed, loop, out);
    this.mining.apply(u, t, out.owner, this.tracks[u.id], loop, speed || DEFAULT_SPEED, out);
  }

  /** How many units a unit had killed (killing blows) by loop. */
  killsAt(id: number, loop: number): number {
    const k = this.kills.get(id);
    if (!k) return 0;
    let n = 0;
    while (n < k.length && k[n] <= loop) n++;
    return n;
  }

  /** Whether the timeline has any player's camera. */
  get hasCameras(): boolean {
    return this.cameras.size > 0;
  }

  /** Where a player's camera was centered at loop, if the replay recorded it. */
  cameraAt(player: number, loop: number): CameraKey | undefined {
    const keys = this.cameras.get(player);
    if (!keys) return undefined;
    let lo = 0;
    let hi = keys.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (keys[mid].loop > loop) hi = mid;
      else lo = mid + 1;
    }
    return lo > 0 ? keys[lo - 1] : keys[0];
  }

  /** A unit's health, shields and energy at loop (the last key at or before it), if recorded. */
  vitalsAt(id: number, loop: number): Vitals | undefined {
    const keys = this.vitals[id];
    if (!keys) return undefined;
    let lo = 0;
    let hi = keys.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (keys[mid].loop > loop) hi = mid;
      else lo = mid + 1;
    }
    return lo > 0 ? keys[lo - 1] : undefined;
  }

  /**
   * How far a building under construction has come at loop, 0-1: from its
   * own finish when it has one, else its type's usual build time (for one
   * cancelled or still unfinished at the end).
   */
  buildProgress(u: Unit, loop: number): number {
    return Math.min(1, Math.max(0, (loop - u.bornLoop) / this.buildTotal(u)));
  }

  /** A building's whole construction time in loops (see buildProgress). */
  buildTotal(u: Unit): number {
    const total = u.doneLoop !== undefined ? u.doneLoop - u.bornLoop : this.buildLoops.get(u.type) ?? DEFAULT_BUILD_LOOPS;
    return Math.max(total, 1);
  }

  /**
   * Loop of the last observed position at or before loop: a tracker sample,
   * birth, death or engine frame. Command-tier estimates don't count.
   */
  lastSeen(id: number, loop: number): number | undefined {
    const keys = this.tracks[id];
    if (!keys) return undefined;
    let best: number | undefined;
    for (const k of keys) {
      if (k.loop > loop) break;
      if (k.source !== Source.COMMAND) best = k.loop;
    }
    return best;
  }
}
