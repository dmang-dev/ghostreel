// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { Source, UnitClass, type Keyframe, type Unit, type UnitType } from "./gen/timeline/v1/timeline_pb";
import { Mining } from "./mining";

const types = [
  { name: "Nexus", radius: 2.75, footprintW: 5, class: UnitClass.BUILDING },
  { name: "MineralField", radius: 0.6, footprintW: 2, class: UnitClass.RESOURCE },
  { name: "Probe", radius: 0.375, speed: 3.94, class: UnitClass.WORKER },
] as unknown as UnitType[];

const unit = (id: number, owner: number, type: number, extra: Partial<Unit> = {}) =>
  ({ id, owner, type, bornLoop: 0, morphs: [], ownerChanges: [], constructed: false, ...extra }) as unknown as Unit;
const key = (loop: number, x: number, y: number, source = Source.TRACKER) => ({ loop, x, y, facing: 0, source }) as Keyframe;

function setup(probeSource = Source.TRACKER) {
  const units = [unit(0, 2, 0), unit(1, 0, 1), unit(2, 2, 2, { bornLoop: 100 })];
  const tracks = [[key(0, 50, 50)], [key(0, 50, 58)], [key(100, 50, 47, probeSource)]];
  const m = new Mining(units, types, tracks, (u) => u.owner, (t) => types[t]?.class === UnitClass.WORKER);
  return { m, units, tracks };
}

describe("drawn mining", () => {
  it("walks a held worker between its base and a field", () => {
    const { m, units, tracks } = setup();
    expect([...m.players]).toEqual([2]);
    const ys = new Set<number>();
    for (let loop = 200; loop < 400; loop += 7) {
      const out = { x: 50, y: 47, facing: 0 };
      m.apply(units[2], 2, 2, tracks[2], loop, 3.94, out);
      // Always between the base's edge and the field's edge.
      expect(out.x).toBeCloseTo(50, 3);
      expect(out.y).toBeGreaterThan(50 + 2.75 - 0.01);
      expect(out.y).toBeLessThan(58 - 0.6 + 0.01);
      ys.add(Math.round(out.y));
    }
    expect(ys.size).toBeGreaterThan(2); // it moves
  });

  it("eases in from where the worker was placed", () => {
    const { m, units, tracks } = setup();
    const out = { x: 50, y: 47, facing: 0 };
    m.apply(units[2], 2, 2, tracks[2], 100, 3.94, out);
    expect(out.y).toBe(47);
  });

  it("leaves players with orders alone", () => {
    const { m, units, tracks } = setup(Source.COMMAND);
    expect(m.players.size).toBe(0);
    const out = { x: 50, y: 47, facing: 0 };
    m.apply(units[2], 2, 2, tracks[2], 300, 3.94, out);
    expect(out.y).toBe(47);
  });

  it("leaves workers far from a base alone", () => {
    const { m, units } = setup();
    const out = { x: 90, y: 90, facing: 0 };
    m.apply(units[2], 2, 2, [key(100, 90, 90)], 300, 3.94, out);
    expect(out).toEqual({ x: 90, y: 90, facing: 0 });
  });
});
