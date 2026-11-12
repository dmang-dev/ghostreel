// SPDX-License-Identifier: Apache-2.0
//
// What the game's actor data adds to a unit's model, as the server writes it
// into the unit's glTF extras (internal/assets/actor.go): the scale it is
// drawn at, the animation groups that pick a variant of each clip (the
// Planetary Fortress is the Command Center's model with group A), the
// animation it plays instead of Stand (the Thor's "Cover Stand"), other
// models attached to its attachment points (the Tempest's charge sphere),
// and the upgrades that change it (served as their own files).

import * as THREE from "three";

/** A model drawn on another, at one of the host's attachment points. */
export interface Attachment {
  model: string; // CModel id, served at /api/assets/model/<id>
  point: string; // the host's attachment point ("Ref_Weapon"); "" for its origin
  offset: [number, number, number]; // in the point's space
  scale: [number, number, number]; // the attached model's own
}

/** The animation a unit plays when it isn't doing anything else. */
export interface Idle {
  anim: string[]; // the clip's words, lower case ("cover", "stand")
  hold: boolean; // played once: show its last frame
}

export interface Look {
  scale: [number, number, number];
  groups: string[]; // lower case
  attach: Attachment[];
  idle?: Idle;
  /** Upgrades that change the look, as the server spells them (unit/<type>?up=a,b). */
  upgrades: string[];
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
  const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
  const it = (e.idle && typeof e.idle === "object" ? e.idle : {}) as Record<string, unknown>;
  const words = strings(it.anim).map((w) => w.toLowerCase());
  const idle = words.length ? { anim: words, hold: it.hold === true } : undefined;
  return { scale: vec3(e.scale, 1), groups, attach, points, idle, upgrades: strings(e.upgrades) };
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
