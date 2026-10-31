// SPDX-License-Identifier: Apache-2.0
//
// Where creep lies at a moment, as discs around its sources. An estimate:
// replays don't record creep. A source's creep grows to its full radius over
// CREEP_GROWTH after the source finishes, and once the source dies it
// recedes from the edge inward at CREEP_RECEDE, the way the game's creep
// shrinks back cell by cell rather than vanishing. Creep another source
// still covers stays, since the scene draws the union of the discs.

import type { Keyframe, Unit, UnitType } from "./gen/timeline/v1/timeline_pb";
import { typeAt } from "./model";

/** Creep radius (cells) around each source when fully spread. */
export const CREEP_RADIUS: Record<string, number> = {
  Hatchery: 12, Lair: 12, Hive: 12, CreepTumor: 10, CreepTumorBurrowed: 10, CreepTumorQueen: 10,
};
/** Loops creep takes to spread to its full radius (12 s on the clock). */
export const CREEP_GROWTH = 22.4 * 12;
/** Cells per loop dead creep recedes: a hatchery's in about 30 s, a tumor's in 25. */
export const CREEP_RECEDE = 0.4 / 22.4;
/** Radius a just-finished source starts from, as a share of its full radius. */
const CREEP_START = 0.15;

export interface CreepDisc {
  x: number;
  y: number;
  r: number;
}

/** Radius of a live source's creep at loop, spreading since start (rounded to half cells). */
export function grownRadius(full: number, start: number, loop: number): number {
  return Math.round(full * Math.min(1, Math.max(CREEP_START, (loop - start) / CREEP_GROWTH)) * 2) / 2;
}

/** When a source's creep starts to spread: on finishing, or on birth for tumors. */
export function creepStart(u: Unit, loop: number): number {
  return u.constructed ? (u.doneLoop ?? loop) : u.bornLoop;
}

/** A creep source that died, with the creep it had spread by then. */
export interface CreepDeath extends CreepDisc {
  id: number;
  died: number;
}

/**
 * Every creep source that died with creep around it, in order of death.
 * Buildings that died unfinished spread none.
 */
export function creepDeaths(units: readonly Unit[], types: readonly UnitType[], tracks: readonly (Keyframe[] | undefined)[]): CreepDeath[] {
  const out: CreepDeath[] = [];
  for (const u of units) {
    if (u.diedLoop === undefined) continue;
    const at = u.diedLoop - 1;
    const full = CREEP_RADIUS[types[typeAt(u, at)]?.name ?? ""];
    if (!full) continue;
    if (u.constructed && (u.doneLoop === undefined || u.doneLoop > at)) continue;
    // Creep sources don't move: the last key is where it stood.
    const keys = tracks[u.id];
    const k = keys?.[keys.length - 1];
    if (!k) continue;
    out.push({ id: u.id, died: u.diedLoop, x: k.x, y: k.y, r: grownRadius(full, creepStart(u, at), at) });
  }
  return out.sort((a, b) => a.died - b.died);
}

/** Loops a death's creep takes to recede completely. */
function recedeLoops(r: number): number {
  return r / CREEP_RECEDE;
}

/** The longest any death's creep lasts, so callers can skip older deaths. */
export const MAX_RECEDE_LOOPS = recedeLoops(Math.max(...Object.values(CREEP_RADIUS)));

/**
 * Appends the creep still receding at loop from sources that died before it,
 * rounded to half cells like grownRadius.
 */
export function recedingCreep(deaths: readonly CreepDeath[], loop: number, out: (CreepDisc & { id: number })[]): void {
  // Deaths are sorted: start at the first one recent enough to matter.
  let lo = 0;
  let hi = deaths.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (deaths[mid].died < loop - MAX_RECEDE_LOOPS) lo = mid + 1;
    else hi = mid;
  }
  for (let i = lo; i < deaths.length && deaths[i].died <= loop; i++) {
    const d = deaths[i];
    const r = Math.round((d.r - (loop - d.died) * CREEP_RECEDE) * 2) / 2;
    if (r > 0) out.push({ id: d.id, x: d.x, y: d.y, r });
  }
}
