// SPDX-License-Identifier: Apache-2.0

import * as THREE from "three";
import { describe, expect, it } from "vitest";
import { buildPose, constructionPose, pickClip, type ClipInfo } from "./models";

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

  it("plays the variant of an animation group where there is one", () => {
    // The Command Center's model, drawn as a Planetary Fortress (group A).
    const cc = clips("Stand", "Stand Work", "Stand A", "Stand Work A", "Fly Stand", "Fly Stand B", "Fly Walk B", "Attack");
    expect(pickClip(cc, "stand", ["a"])?.clip.name).toBe("Stand A");
    expect(pickClip(cc, "stand")?.clip.name).toBe("Stand");
    expect(pickClip(cc, "attack", ["a"])?.clip.name).toBe("Attack"); // no variant: the plain one
    expect(pickClip(cc, "walk", ["fly", "b"])?.clip.name).toBe("Fly Walk B");
    expect(pickClip(cc, "stand", ["fly", "b"])?.clip.name).toBe("Fly Stand B");
    // A rooted Spine Crawler stands burrowed but attacks with the plain clip.
    const spine = clips("Stand", "Burrow", "Stand Burrow", "Attack", "Attack End");
    expect(pickClip(spine, "stand", ["burrow"])?.clip.name).toBe("Stand Burrow");
    expect(pickClip(spine, "attack", ["burrow"])?.clip.name).toBe("Attack");
  });

  it("stands in the unit's idle animation where it has one", () => {
    const thor = clips("Stand", "Stand 01", "Cover Start", "Cover", "Cover Stand", "Walk", "Attack");
    const idle = { anim: ["cover", "stand"], hold: false };
    expect(pickClip(thor, "stand", [], idle)?.clip.name).toBe("Cover Stand");
    expect(pickClip(thor, "walk", [], idle)?.clip.name).toBe("Walk"); // only standing changes
    expect(pickClip(thor, "attack", [], idle)?.clip.name).toBe("Attack");
    // A numbered clip is a variation of the same animation, so "Burrow 01"
    // answers Burrow before "Burrow Stand" does.
    const mine = clips("Stand", "Burrow 01", "Burrow Stand", "Unburrow");
    expect(pickClip(mine, "stand", [], { anim: ["burrow"], hold: true })?.clip.name).toBe("Burrow 01");
    // A model without the idle clip stands as usual.
    expect(pickClip(clips("Stand", "Walk"), "stand", [], idle)?.clip.name).toBe("Stand");
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

describe("constructionPose", () => {
  const clip = (name: string, duration: number, looping: boolean) =>
    ({ clip: { name, duration } as THREE.AnimationClip, words: name.toLowerCase().split(/\s+/), looping, moveSpeed: 0 }) as ClipInfo;
  // TerranBuild's clips, as the converter names them.
  const scaffold = [
    clip("Birth A Start", 2, false), clip("Birth A Stand", 3, true),
    clip("Birth B Start", 2, false), clip("Birth B Stand", 3, true),
    clip("Birth C Start", 2, false), clip("Birth C Stand", 3, true),
    clip("Birth D Start", 2, false),
  ];

  it("steps scaffolding through stages A to C, leaving D for the finish", () => {
    // 60 s build: 20 s per stage.
    expect(constructionPose(scaffold, 0.01, 60)).toMatchObject({ info: { clip: { name: "Birth A Start" } }, time: 0.6 });
    const a = constructionPose(scaffold, 0.2, 60)!; // 12 s in: 10 s into A's stand, which loops every 3 s
    expect(a.info.clip.name).toBe("Birth A Stand");
    expect(a.time).toBeCloseTo(1, 5);
    expect(constructionPose(scaffold, 0.34, 60)?.info.clip.name).toBe("Birth B Start");
    expect(constructionPose(scaffold, 0.99, 60)?.info.clip.name).toBe("Birth C Stand");
  });

  it("does the same for a cocoon's Build stages", () => {
    const cocoon = [clip("Build A Start", 1, false), clip("Build A Stand", 1, true), clip("Build B Stand", 1, true), clip("Build D Start", 1, false), clip("GLstand", 1, true)];
    expect(constructionPose(cocoon, 0.1, 40)?.info.clip.name).toBe("Build A Stand");
    expect(constructionPose(cocoon, 0.9, 40)?.info.clip.name).toBe("Build B Stand");
  });

  it("starts, loops and ends a warp-in matrix", () => {
    const matrix = [clip("Stand Build Start", 2, false), clip("Stand Build", 4, true), clip("Stand Build End", 3, false), clip("GLstand", 1, true)];
    expect(constructionPose(matrix, 0.02, 50)).toMatchObject({ info: { clip: { name: "Stand Build Start" } }, time: 1 });
    const mid = constructionPose(matrix, 0.5, 50)!; // 25 s: 23 s into the loop
    expect(mid.info.clip.name).toBe("Stand Build");
    expect(mid.time).toBeCloseTo(3, 5);
    const end = constructionPose(matrix, 0.98, 50)!; // 1 s left: 2 s into the 3 s end
    expect(end.info.clip.name).toBe("Stand Build End");
    expect(end.time).toBeCloseTo(2, 5);
  });

  it("gives up on a model with none of these", () => {
    expect(constructionPose([clip("Stand", 1, true)], 0.5, 40)).toBeUndefined();
  });
});
