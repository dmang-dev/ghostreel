// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { surfaceOpacity, waterCells, waterSheets } from "./water";

// A 4×2 map: ground at 10 on the left half, 6 on the right.
const w = 4, h = 2;
const corners = new Float32Array((w + 1) * (h + 1));
for (let y = 0; y <= h; y++) for (let x = 0; x <= w; x++) corners[y * (w + 1) + x] = x <= 1 ? 10 : x >= 3 ? 6 : 8;

const types = {
  Pool: { height: 7.5, color: [0.2, 0.4, 0.6, 0.3] },
  Hidden: { height: 3, color: [1, 1, 1, 0.3] },
  Lava: { height: 12, color: [0.82, 0.29, 0.09, 0.3], lava: true },
};

describe("water", () => {
  it("colors only cells whose ground is below the surface", () => {
    const cells = waterCells([{ type: "Pool", x0: 0, y0: 0, x1: 4, y1: 2 }], types, corners, w, h)!;
    const alpha = (x: number, y: number) => cells[(y * w + x) * 4 + 3];
    // Cell ground (mean of corners): 10, 9, 7, 6. The surface at 7.5 covers the right two.
    expect([0, 1, 2, 3].map((x) => alpha(x, 0) > 0)).toEqual([false, false, true, true]);
    expect([0, 1, 2, 3].map((x) => alpha(x, 1) > 0)).toEqual([false, false, true, true]);
    expect(Array.from(cells.slice((0 * w + 3) * 4, (0 * w + 3) * 4 + 3))).toEqual([51, 102, 153]);
  });

  it("shows nothing for water under the ground or of unknown type", () => {
    expect(waterCells([{ type: "Hidden", x0: 0, y0: 0, x1: 4, y1: 2 }], types, corners, w, h)).toBeUndefined();
    expect(waterCells([{ type: "Nope", x0: 0, y0: 0, x1: 4, y1: 2 }], types, corners, w, h)).toBeUndefined();
  });

  it("clips areas to the map", () => {
    const cells = waterCells([{ type: "Lava", x0: 3, y0: 1, x1: 9, y1: 9 }], types, corners, w, h)!;
    let n = 0;
    for (let i = 3; i < cells.length; i += 4) if (cells[i]) n++;
    expect(n).toBe(1);
  });

  it("draws lava nearly opaque and keeps water see-through", () => {
    expect(surfaceOpacity(types.Lava)).toBeGreaterThan(0.9);
    expect(surfaceOpacity(types.Pool)).toBeGreaterThanOrEqual(0.5);
    expect(surfaceOpacity(types.Pool)).toBeLessThan(0.9);
  });

  it("builds one quad per area at the surface height", () => {
    const { positions, indices } = waterSheets([{ type: "Pool", x0: 1, y0: 2, x1: 3, y1: 5 }, { type: "Pool", x0: 0, y0: 0, x1: 1, y1: 1 }], 7.5);
    expect(positions.length).toBe(24);
    expect(Array.from(positions.slice(0, 12))).toEqual([1, 2, 7.5, 3, 2, 7.5, 3, 5, 7.5, 1, 5, 7.5]);
    expect(Array.from(indices)).toEqual([0, 1, 2, 0, 2, 3, 4, 5, 6, 4, 6, 7]);
  });
});
