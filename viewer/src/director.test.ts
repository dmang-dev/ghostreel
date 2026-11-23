// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { Director, GAME_ZOOM, selectReel } from "./director";
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

describe("Director with a point of view", () => {
  // Player 1's town hall at (20, 30) from the start, a second one at (60, 50) later.
  const hall = (id: number, x: number, y: number, born: number, died?: number) => ({ id, owner: 1, type: 0, bornLoop: born, diedLoop: died });
  const model = (units: ReturnType<typeof hall>[]) => ({
    units,
    types: [{ name: "CommandCenter", class: 0 }],
    tracks: units.map((u) => [{ loop: 0, x: [20, 60][u.id], y: [30, 50][u.id] }]),
    loops: 20000,
    tl: { meta: { players: [{ id: 1, name: "A" }, { id: 2, name: "B" }] } },
  }) as unknown as Model;

  it("opens on the player's main base at the game's zoom, not the whole map", () => {
    const d = new Director(model([hall(0, 20, 30, 0)]), [], bounds, { pov: 1 });
    expect(d.target(0)).toMatchObject({ cx: 20, cy: 30, span: GAME_ZOOM });
    expect(new Director(model([hall(0, 20, 30, 0)]), [], bounds).target(0)).toMatchObject({ span: 80 });
  });

  it("goes back to the oldest town hall still standing", () => {
    const d = new Director(model([hall(0, 20, 30, 0, 5000), hall(1, 60, 50, 3000)]), [], bounds, { pov: 1 });
    expect(d.target(4000)).toMatchObject({ cx: 20, cy: 30 });
    expect(d.target(6000)).toMatchObject({ cx: 60, cy: 50 });
  });

  it("frames hotspots close, near the game's zoom", () => {
    const big = { ...hot(2000, 2400, 0.8, 30, 40), r: 20 };
    const d = new Director(model([hall(0, 20, 30, 0)]), [big], bounds, { pov: 1 });
    expect(d.target(2100).span).toBeLessThanOrEqual(GAME_ZOOM * 1.5);
    expect(new Director(empty, [big], bounds).target(2100).span).toBe(56);
  });
});
