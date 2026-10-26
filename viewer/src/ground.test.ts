// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { fineHeights, SUB } from "./ground";

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
