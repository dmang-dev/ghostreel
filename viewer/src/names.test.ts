// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { spaced, unitName } from "./names";

describe("spaced", () => {
  it("splits the game's ids into words", () => {
    expect(spaced("SiegeTankSieged")).toBe("Siege Tank Sieged");
    expect(spaced("TerranInfantryWeaponsLevel1")).toBe("Terran Infantry Weapons Level 1");
    expect(spaced("MineralField750")).toBe("Mineral Field 750");
    expect(spaced("XelNagaTower")).toBe("Xel Naga Tower");
    expect(spaced("SCV")).toBe("SCV");
    expect(spaced("KD8Charge")).toBe("KD8 Charge");
  });

  it("falls back to spacing without game data", () => {
    expect(unitName("CommandCenter")).toBe("Command Center");
  });
});
