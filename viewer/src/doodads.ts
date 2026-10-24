// SPDX-License-Identifier: Apache-2.0
//
// The map's doodads (rocks, plants, wreckage, buildings of the setting) in
// the 3D view. Ladder maps place thousands, of a few dozen kinds, and none of
// them move, so each part of each kind is one instanced mesh in its rest
// pose: a few hundred draw calls for the whole map.

import * as THREE from "three";
import type { Template, UnitModels } from "./models";

export interface Doodad {
  Type: string;
  X: number;
  Y: number;
  Z: number;
  Rotation?: number;
  Scale?: number[];
  Variation?: number;
}

/** Fills group with a map's doodads, kind by kind as their models arrive. */
export async function buildDoodads(group: THREE.Group, list: Doodad[], models: UnitModels): Promise<void> {
  const kinds = new Map<string, Doodad[]>();
  for (const d of list) {
    const key = `model/${encodeURIComponent(d.Type)}${d.Variation ? `?v=${d.Variation}` : ""}`;
    let k = kinds.get(key);
    if (!k) kinds.set(key, (k = []));
    k.push(d);
  }
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion();
  const z = new THREE.Vector3(0, 0, 1);
  const pos = new THREE.Vector3();
  const scl = new THREE.Vector3();
  // A few kinds at a time: each is a server-side conversion on first use.
  const queue = [...kinds];
  const worker = async () => {
    for (let next = queue.shift(); next; next = queue.shift()) {
      const [key, ds] = next;
      const tpl = await models.template(key);
      if (!tpl) continue;
      for (const part of parts(tpl)) {
        const mesh = new THREE.InstancedMesh(part.geometry, part.material, ds.length);
        mesh.frustumCulled = false;
        ds.forEach((d, i) => {
          q.setFromAxisAngle(z, d.Rotation ?? 0);
          const s = d.Scale;
          pos.set(d.X, d.Y, d.Z);
          scl.set(s?.[0] ?? 1, s?.[1] ?? 1, s?.[2] ?? 1);
          m.compose(pos, q, scl).multiply(part.local);
          mesh.setMatrixAt(i, m);
        });
        group.add(mesh);
      }
    }
  };
  await Promise.all([worker(), worker(), worker(), worker()]);
}

/** A template's meshes with their transforms relative to its root, in rest pose. */
function parts(tpl: Template): { geometry: THREE.BufferGeometry; material: THREE.Material | THREE.Material[]; local: THREE.Matrix4 }[] {
  const out: { geometry: THREE.BufferGeometry; material: THREE.Material | THREE.Material[]; local: THREE.Matrix4 }[] = [];
  tpl.root.updateMatrixWorld(true);
  const inv = tpl.root.matrixWorld.clone().invert();
  tpl.root.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (!mesh.isMesh || !mesh.visible) return;
    // Skinned vertices are stored in the bind pose, in the mesh's space.
    out.push({ geometry: mesh.geometry, material: mesh.material, local: inv.clone().multiply(mesh.matrixWorld) });
  });
  return out;
}
