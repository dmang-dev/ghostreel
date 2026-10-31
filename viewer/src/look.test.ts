// SPDX-License-Identifier: Apache-2.0

import * as THREE from "three";
import { describe, expect, it } from "vitest";
import { attachTarget, parseLook } from "./look";

describe("parseLook", () => {
  it("is the plain model without extras", () => {
    expect(parseLook(undefined)).toEqual({ scale: [1, 1, 1], groups: [], attach: [], points: {} });
    expect(parseLook({ scale: "big", groups: 3, attach: [{}] })).toEqual({ scale: [1, 1, 1], groups: [], attach: [], points: {} });
  });

  it("reads what the server writes", () => {
    const lk = parseLook({
      scale: [0.8, 0.8, 0.8],
      groups: ["A", "Burrow"],
      attach: [{ model: "StormSphereAttachment", point: "Ref_Weapon", offset: [0, -0.02, 0.25], scale: [1, 1, 1] }, { model: "Glow" }],
      attachments: { "Ref_Weapon": "Ref_Weapon", "Ref_Target": "Vol_Target" },
    });
    expect(lk.scale).toEqual([0.8, 0.8, 0.8]);
    expect(lk.groups).toEqual(["a", "burrow"]);
    expect(lk.attach).toEqual([
      { model: "StormSphereAttachment", point: "Ref_Weapon", offset: [0, -0.02, 0.25], scale: [1, 1, 1] },
      { model: "Glow", point: "", offset: [0, 0, 0], scale: [1, 1, 1] },
    ]);
    expect(lk.points.Ref_Target).toBe("Vol_Target");
  });
});

describe("attachTarget", () => {
  it("finds the point's bone as GLTFLoader names it", () => {
    const space = new THREE.Group();
    const bone = new THREE.Bone();
    bone.name = "Ref_Weapon_01"; // "Ref_Weapon 01" in the file
    space.add(new THREE.Bone().add(bone));
    const lk = parseLook({ attachments: { "Ref_Weapon 01": "Ref_Weapon 01" } });
    expect(attachTarget(lk, space, "Ref_Weapon 01")).toBe(bone);
    expect(attachTarget(lk, space, "")).toBe(space);
    expect(attachTarget(lk, space, "Ref_Head")).toBeUndefined();
  });
});
