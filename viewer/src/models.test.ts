// SPDX-License-Identifier: Apache-2.0

import * as THREE from "three";
import { describe, expect, it } from "vitest";
import { pickClip } from "./models";

const clips = (...names: string[]) =>
  names.map((name) => ({
    clip: new THREE.AnimationClip(name, 1, []),
    words: name.toLowerCase().split(/\s+/),
    looping: true,
    moveSpeed: 0,
  }));

describe("pickClip", () => {
  it("prefers the plainest matching name", () => {
    const cs = clips("Stand 01", "Stand Work", "Stand", "Walk Slow", "Walk", "Attack 01", "Attack");
    expect(pickClip(cs, "stand")?.clip.name).toBe("Stand");
    expect(pickClip(cs, "walk")?.clip.name).toBe("Walk");
    expect(pickClip(cs, "attack")?.clip.name).toBe("Attack");
  });

  it("falls back along the action's list", () => {
    const cs = clips("Stand", "Death");
    expect(pickClip(cs, "walk")?.clip.name).toBe("Stand");
    expect(pickClip(cs, "build")?.clip.name).toBe("Stand");
    expect(pickClip(clips("Fly Start", "Stand Fly", "Fly Walk"), "stand")?.clip.name).toBe("Stand Fly");
  });

  it("avoids transitions when a steady clip exists", () => {
    expect(pickClip(clips("Stand Work Start", "Stand Work"), "stand")?.clip.name).toBe("Stand Work");
    expect(pickClip(clips("Walk Start", "Walk"), "walk")?.clip.name).toBe("Walk");
  });

  it("finds nothing for a rigid model", () => {
    expect(pickClip([], "stand")).toBeUndefined();
  });
});
