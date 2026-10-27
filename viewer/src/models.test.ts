// SPDX-License-Identifier: Apache-2.0

import * as THREE from "three";
import { describe, expect, it } from "vitest";
import { buildPose, pickClip, type ClipInfo } from "./models";

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

describe("buildPose", () => {
  const clip = (name: string, duration: number, looping: boolean) =>
    ({ clip: { name, duration } as THREE.AnimationClip, words: name.toLowerCase().split(/_|\s+/), looping, moveSpeed: 0 }) as ClipInfo;
  const staged = [
    clip("Build_A_Start", 3, false), clip("Build_A_Stand", 2, true),
    clip("Build_B_Start", 3, false), clip("Build_B_Stand", 2, true),
    clip("Build_C_Start", 3, false), clip("Build_C_Stand", 2, true),
    clip("Build_D_Start", 2, false), clip("Stand", 1, true),
  ].map((c) => ({ ...c, words: c.clip.name.toLowerCase().split("_") }));

  it("steps through the stages over the build time", () => {
    // 40 s build: 10 s per stage.
    expect(buildPose(staged, 0.01, 40)).toMatchObject({ info: { clip: { name: "Build_A_Start" } } });
    expect(buildPose(staged, 0.2, 40)).toMatchObject({ info: { clip: { name: "Build_A_Stand" } }, time: 5 });
    expect(buildPose(staged, 0.51, 40)?.info.clip.name).toBe("Build_C_Start");
    // The last stage holds its start clip's end.
    const d = buildPose(staged, 0.99, 40)!;
    expect(d.info.clip.name).toBe("Build_D_Start");
    expect(d.time).toBeCloseTo(2, 2);
  });

  it("plays a birth clip over the build, or gives up", () => {
    const birth = [clip("Birth", 4, false), clip("Stand", 1, true)].map((c) => ({ ...c, words: [c.clip.name.toLowerCase()] }));
    expect(buildPose(birth, 0.5, 40)).toMatchObject({ info: { clip: { name: "Birth" } }, time: 2 });
    expect(buildPose([clip("Stand", 1, true)], 0.5, 40)).toBeUndefined();
  });
});
