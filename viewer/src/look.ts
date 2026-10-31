// SPDX-License-Identifier: Apache-2.0
//
// What the game's actor data adds to a unit's model, as the server writes it
// into the unit's glTF extras (internal/assets/actor.go): the scale it is
// drawn at, the animation groups that pick a variant of each clip (the
// Planetary Fortress is the Command Center's model with group A), and other
// models attached to its attachment points (the Tempest's charge sphere).

import * as THREE from "three";

/** A model drawn on another, at one of the host's attachment points. */
export interface Attachment {
  model: string; // CModel id, served at /api/assets/model/<id>
  point: string; // the host's attachment point ("Ref_Weapon"); "" for its origin
  offset: [number, number, number]; // in the point's space
  scale: [number, number, number]; // the attached model's own
}

export interface Look {
  scale: [number, number, number];
  groups: string[]; // lower case
  attach: Attachment[];
  /** The model's attachment points: name → the bone (node name) it rides on. */
  points: Record<string, string>;
}

const vec3 = (v: unknown, def: number): [number, number, number] =>
  Array.isArray(v) && v.length === 3 && v.every((x) => typeof x === "number") ? [v[0], v[1], v[2]] : [def, def, def];

/** Reads a look from a glTF file's top-level extras; anything missing is the plain model. */
export function parseLook(extras: unknown): Look {
  const e = (extras && typeof extras === "object" ? extras : {}) as Record<string, unknown>;
  const groups = Array.isArray(e.groups) ? e.groups.filter((g): g is string => typeof g === "string").map((g) => g.toLowerCase()) : [];
  const attach: Attachment[] = [];
  for (const a of Array.isArray(e.attach) ? e.attach : []) {
    if (!a || typeof a.model !== "string") continue;
    attach.push({ model: a.model, point: typeof a.point === "string" ? a.point : "", offset: vec3(a.offset, 0), scale: vec3(a.scale, 1) });
  }
  const points: Record<string, string> = {};
  if (e.attachments && typeof e.attachments === "object") {
    for (const [k, v] of Object.entries(e.attachments)) if (typeof v === "string") points[k] = v;
  }
  return { scale: vec3(e.scale, 1), groups, attach, points };
}

/**
 * The object an attachment rides on in a loaded model: the bone of its
 * attachment point (GLTFLoader turns spaces in node names into
 * underscores), or the model's own space for the origin. undefined when the
 * model has no such point.
 */
export function attachTarget(look: Look, modelSpace: THREE.Object3D, point: string): THREE.Object3D | undefined {
  if (!point) return modelSpace;
  const bone = look.points[point];
  if (!bone) return undefined;
  return modelSpace.getObjectByName(THREE.PropertyBinding.sanitizeNodeName(bone));
}
