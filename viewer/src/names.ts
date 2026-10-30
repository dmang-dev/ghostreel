// SPDX-License-Identifier: Apache-2.0
//
// Display names and stats for the game's internal ids ("SiegeTankSieged",
// "TerranInfantryWeaponsLevel1"). With game data the server supplies the
// game's own names and unit stats; without it, ids are spaced into words.

import * as api from "./api";

const units = new Map<string, string>();
const upgrades = new Map<string, string>();
const abilities = new Map<string, string>();
const stats = new Map<string, api.UnitInfo>();

/** "SiegeTankSieged" → "Siege Tank Sieged", "MineralField750" → "Mineral Field 750". */
export function spaced(id: string): string {
  return id
    .replace(/([a-z])([A-Z0-9])/g, "$1 $2")
    .replace(/([A-Z])([A-Z][a-z])/g, "$1 $2")
    .replace(/([0-9])([A-Za-z])/g, "$1 $2")
    .replace(/_/g, " ");
}

export const unitName = (id: string) => units.get(id) || spaced(id);
export const upgradeName = (id: string) => upgrades.get(id) || spaced(id);
export const abilityName = (id: string) => abilities.get(id) || spaced(id);

/** The game data's stats for a unit type, once loaded. */
export const unitStats = (id: string): api.UnitInfo | undefined => stats.get(id);

/**
 * Loads names and stats for the ids a replay uses. Returns whether the
 * server had game data. Names already loaded are kept.
 */
export async function loadNames(unitIds: string[], upgradeIds: string[], abilityIds: string[]): Promise<boolean> {
  const missing = (ids: string[], have: Map<string, unknown>) => [...new Set(ids)].filter((id) => id && !have.has(id));
  const info = await api.fetchInfo(missing(unitIds, stats), missing(upgradeIds, upgrades), missing(abilityIds, abilities));
  if (!info) return false;
  for (const [id, u] of Object.entries(info.units ?? {})) {
    stats.set(id, u);
    if (u.name) units.set(id, u.name);
  }
  for (const [id, n] of Object.entries(info.upgrades ?? {})) upgrades.set(id, n);
  for (const [id, n] of Object.entries(info.abilities ?? {})) abilities.set(id, n);
  return true;
}
