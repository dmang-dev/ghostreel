// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { CREEP_GROWTH, CREEP_RECEDE, creepDeaths, grownRadius, recedingCreep, type CreepDisc } from "./creep";
import type { Keyframe, Unit, UnitType } from "./gen/timeline/v1/timeline_pb";

const types = [
  { name: "Hatchery" },
  { name: "CreepTumorBurrowed" },
  { name: "Zergling" },
  { name: "CreepTumor" },
] as unknown as UnitType[];

const unit = (id: number, type: number, extra: Partial<Unit> = {}) =>
  ({ id, owner: 2, type, bornLoop: 0, morphs: [], ownerChanges: [], constructed: false, ...extra }) as unknown as Unit;
const at = (x: number, y: number) => [{ loop: 0, x, y, facing: 0 }] as Keyframe[];

describe("creep", () => {
  it("grows from a sliver to its full radius", () => {
    expect(grownRadius(12, 1000, 1000)).toBe(2); // 15% of 12 = 1.8, to the half cell
    expect(grownRadius(12, 1000, 1000 + CREEP_GROWTH / 2)).toBe(6);
    expect(grownRadius(12, 1000, 1000 + CREEP_GROWTH * 3)).toBe(12);
  });

  it("finds dead sources with the creep they had spread", () => {
    const units = [
      // A finished hatchery killed long after it finished: full creep.
      unit(0, 0, { constructed: true, bornLoop: 0, doneLoop: 1000, diedLoop: 5000 }),
      // A hatchery killed while building: none.
      unit(1, 0, { constructed: true, bornLoop: 0, diedLoop: 900 }),
      // A tumor that burrowed and died soon after: partly spread, typed as it died.
      unit(2, 3, { bornLoop: 2000, diedLoop: 2000 + CREEP_GROWTH / 2 + 1, morphs: [{ loop: 2050, type: 1 }] as Unit["morphs"] }),
      // Not a creep source.
      unit(3, 2, { diedLoop: 3000 }),
      // Still alive.
      unit(4, 0, { constructed: true, doneLoop: 100 }),
    ];
    const deaths = creepDeaths(units, types, [at(10, 10), at(30, 30), at(50, 50), at(70, 70), at(90, 90)]);
    expect(deaths).toEqual([
      { id: 2, died: 2000 + CREEP_GROWTH / 2 + 1, x: 50, y: 50, r: 5 },
      { id: 0, died: 5000, x: 10, y: 10, r: 12 },
    ]);
  });

  it("recedes from the edge after its source dies, then is gone", () => {
    const deaths = [{ id: 0, died: 5000, x: 10, y: 10, r: 12 }];
    const at = (loop: number) => {
      const out: (CreepDisc & { id: number })[] = [];
      recedingCreep(deaths, loop, out);
      return out.map((d) => d.r);
    };
    expect(at(4999)).toEqual([]); // still alive: the scene draws it as a live source
    expect(at(5000)).toEqual([12]);
    expect(at(5000 + 6 / CREEP_RECEDE)).toEqual([6]);
    expect(at(5000 + 12 / CREEP_RECEDE + 1)).toEqual([]);
    // About 30 s on the clock for a hatchery's creep.
    expect(12 / CREEP_RECEDE / 22.4).toBeCloseTo(30, 0);
  });

  it("skips deaths too old to matter", () => {
    const deaths = [
      { id: 0, died: 100, x: 0, y: 0, r: 12 },
      { id: 1, died: 20000, x: 0, y: 0, r: 10 },
    ];
    const out: (CreepDisc & { id: number })[] = [];
    recedingCreep(deaths, 20100, out);
    expect(out.map((d) => d.id)).toEqual([1]);
  });
});
