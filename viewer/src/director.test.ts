// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { Director, selectReel } from "./director";
import type { Model } from "./model";
import type { Marker } from "./reports";

const hot = (loop: number, end: number, score: number, x = 10, y = 10): Marker => ({
  loop, end, score, x, y, r: 6, label: `Battle: at ${loop}`, kind: "hotspot", tier: score > 0.6 ? "major" : "minor",
});

// A model with no units: only hotspots and the map bounds matter.
const empty = { units: [], types: [], tracks: [], loops: 20000, tl: { meta: { players: [] } } } as unknown as Model;
const bounds = { left: 0, bottom: 0, right: 100, top: 80 };

describe("selectReel", () => {
  it("takes the best hotspots that don't overlap, in game order", () => {
    const hs = [hot(1000, 1200, 0.5), hot(1100, 1300, 0.9), hot(5000, 5200, 0.7), hot(9000, 9100, 0.1)];
    const reel = selectReel(hs, 2);
    expect(reel.map((h) => h.loop)).toEqual([1100, 5000]);
  });
});

describe("Director", () => {
  it("frames a hotspot a little before it starts, else the whole map", () => {
    const d = new Director(empty, [hot(2000, 2400, 0.8, 30, 40)], bounds);
    expect(d.target(1000)).toMatchObject({ cx: 50, cy: 40, span: 80 });
    const shot = d.target(1950);
    expect(shot).toMatchObject({ cx: 30, cy: 40 });
    expect(shot.hotspot?.loop).toBe(2000);
    expect(d.target(2420).hotspot).toBeDefined(); // still there just after
    expect(d.target(3000).hotspot).toBeUndefined();
  });

  it("prefers the higher-scored of two hotspots", () => {
    const d = new Director(empty, [hot(2000, 2400, 0.3, 10, 10), hot(2100, 2300, 0.9, 70, 60)], bounds);
    expect(d.target(2200)).toMatchObject({ cx: 70, cy: 60 });
  });

  it("eases between shots during playback and cuts on a seek", () => {
    const d = new Director(empty, [hot(2000, 2400, 0.8, 30, 40)], bounds);
    d.shotAt(1900); // whole map
    const eased = d.shotAt(1940); // hotspot in range (3 s lead): moving, not there yet
    expect(eased.cx).toBeGreaterThan(30);
    expect(eased.cx).toBeLessThan(50);
    const cut = d.shotAt(2200 + 200); // jump: cut straight to it
    expect(cut.cx).toBeCloseTo(30);
  });
});
