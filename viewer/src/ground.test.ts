// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type * as THREE from "three";
import { fineHeights, grainFor, stepCliffs, SUB } from "./ground";

/** Corner heights for a w x h map from f(x, y). */
function corners(w: number, h: number, f: (x: number, y: number) => number): number[] {
  const out: number[] = [];
  for (let y = 0; y <= h; y++) for (let x = 0; x <= w; x++) out.push(f(x, y));
  return out;
}

describe("fineHeights", () => {
  it("has SUB vertices per cell", () => {
    expect(fineHeights(corners(6, 4, () => 0), 6, 4)).toHaveLength((6 * SUB + 1) * (4 * SUB + 1));
  });

  it("keeps flat ground flat", () => {
    for (const v of fineHeights(corners(5, 5, () => 8), 5, 5)) expect(v).toBeCloseTo(8, 5);
  });

  it("turns a cliff into a rising slope that keeps both levels", () => {
    const w = 12, h = 4;
    const fh = fineHeights(corners(w, h, (x) => (x <= 6 ? 8 : 10)), w, h);
    const fw = w * SUB + 1;
    const row = Array.from(fh.subarray(2 * fw, 3 * fw));
    for (let i = 1; i < row.length; i++) expect(row[i]).toBeGreaterThanOrEqual(row[i - 1] - 1e-6);
    expect(row[0]).toBeCloseTo(8, 5);
    expect(row[row.length - 1]).toBeCloseTo(10, 5);
    // Rounded: the edge starts a little before the top corner of the step.
    expect(row[6 * SUB]).toBeGreaterThan(8);
    expect(row[6 * SUB]).toBeLessThan(9);
  });
});

describe("stepCliffs", () => {
  it("steps the ground in cliff cells instead of sloping it", () => {
    // 4x2 cells: the east half 2 units up; one cliff cell between them.
    const w = 4, h = 2;
    const corners = new Float32Array((w + 1) * (h + 1));
    for (let y = 0; y <= h; y++) for (let x = 0; x <= w; x++) corners[y * (w + 1) + x] = x >= 2 ? 2 : 0;
    const fine = fineHeights(corners, w, h);
    const mask = new Float32Array(fine.length);
    stepCliffs(fine, corners, w, h, [[0, 0]], mask);
    const fw = w * SUB + 1;
    const row = SUB; // a row through the middle of the cell
    const xs = Array.from({ length: 2 * SUB + 1 }, (_, i) => fine[row * fw + i]);
    // Low up to the middle (ties go low), then the high side's height.
    expect(xs.slice(0, SUB + 1).every((v) => v === 0)).toBe(true);
    expect(xs.slice(SUB + 1).every((v) => v === 2)).toBe(true);
    expect(mask[row * fw]).toBe(1);
    expect(mask[row * fw + 3 * SUB]).toBe(0); // outside the cell
  });
});

describe("grainFor", () => {
  const tex = (width: number) => ({ image: { width } }) as unknown as THREE.Texture;
  it("is full for the small copies and off for full-size textures", () => {
    expect(grainFor([tex(64), tex(64)])).toBe(1);
    expect(grainFor([tex(512), undefined, tex(1024)])).toBe(0);
    expect(grainFor([tex(512), tex(64)])).toBe(1); // the smallest decides
    expect(grainFor([tex(160)])).toBeCloseTo(0.5);
    expect(grainFor([undefined])).toBe(0);
  });
});
