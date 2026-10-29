// SPDX-License-Identifier: Apache-2.0
//
// Checks the TypeScript model against golden output from the Go
// implementation (internal/timeline/golden_test.go). If this fails after a
// Go change, regenerate the golden files and port the change here.

import { readFileSync } from "node:fs";
import { create } from "@bufbuild/protobuf";
import { TimelineSchema } from "./gen/timeline/v1/timeline_pb";
import { describe, expect, it } from "vitest";
import { Model, decodeTimeline, formatClock, positionAt } from "./model";
import type { Keyframe } from "./gen/timeline/v1/timeline_pb";

const dir = new URL("./testdata/", import.meta.url);
const tl = decodeTimeline(new Uint8Array(readFileSync(new URL("fixture.pb", dir))));
const golden: {
  loop: number;
  units: { id: number; type: number; owner: number; x: number; y: number; facing: number; constructing: boolean }[];
  workers: Record<string, number>;
}[] = JSON.parse(readFileSync(new URL("fixture.states.json", dir), "utf8"));

describe("model matches the Go implementation", () => {
  // Drawn mining (mining.ts) is viewer-only, so it's off for the comparison.
  const model = new Model(tl, { mining: false });
  for (const g of golden) {
    it(`loop ${g.loop}`, () => {
      const got = model.stateAt(g.loop);
      expect(got.map((u) => u.id)).toEqual(g.units.map((u) => u.id));
      got.forEach((u, i) => {
        const w = g.units[i];
        expect(u.type).toBe(w.type);
        expect(u.owner).toBe(w.owner);
        expect(u.constructing).toBe(w.constructing);
        expect(u.x).toBeCloseTo(w.x, 4);
        expect(u.y).toBeCloseTo(w.y, 4);
        expect(u.facing).toBeCloseTo(w.facing, 4);
      });
      for (const [p, workers] of Object.entries(g.workers)) {
        expect(model.statsAt(Number(p), g.loop)?.workers).toBe(workers);
      }
    });
  }
});

describe("positionAt", () => {
  const k = (loop: number, x: number) => ({ loop, x, y: 0, facing: 0 }) as Keyframe;
  it("holds, then travels at speed", () => {
    const keys = [k(0, 0), k(1000, 20)];
    const out = { x: 0, y: 0, facing: 0 };
    positionAt(keys, 2, 840, out);
    expect(out.x).toBe(0);
    positionAt(keys, 2, 920, out);
    expect(out.x).toBeCloseTo(10, 4);
    positionAt(keys, 2, 5000, out);
    expect(out.x).toBe(20);
  });
});

describe("formatClock", () => {
  it("truncates like the game clock", () => {
    expect(formatClock(22)).toBe("0:00");
    expect(formatClock(5174)).toBe("3:50");
    expect(formatClock(80640)).toBe("1:00:00");
  });
});

describe("vitalsAt", () => {
  it("holds each key until the next", () => {
    const tl = create(TimelineSchema, {
      meta: { loops: 1000 },
      units: [{ id: 0, bornLoop: 0 }, { id: 1, bornLoop: 0 }],
      tracks: [
        {
          unitId: 0,
          keyframes: [{ loop: 0, x: 1, y: 1 }],
          vitals: [
            { loop: 100, health: 45, healthMax: 45 },
            { loop: 300, health: 20, healthMax: 45 },
          ],
        },
        { unitId: 1, keyframes: [{ loop: 0, x: 2, y: 2 }] },
      ],
    });
    const m = new Model(tl);
    expect(m.hasVitals).toBe(true);
    expect(m.vitalsAt(0, 50)).toBeUndefined();
    expect(m.vitalsAt(0, 100)?.health).toBe(45);
    expect(m.vitalsAt(0, 299)?.health).toBe(45);
    expect(m.vitalsAt(0, 900)?.health).toBe(20);
    expect(m.vitalsAt(1, 500)).toBeUndefined();
    expect(new Model(create(TimelineSchema, { meta: { loops: 10 } })).hasVitals).toBe(false);
  });
});
