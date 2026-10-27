// SPDX-License-Identifier: Apache-2.0
//
// The map's cliffs as the game draws them: a model piece per cliff cell
// (2×2 cells) where its corners are at different cliff levels, chosen and
// placed by the server (internal/assets/cliffs.go), in the cliff set's
// material. Pieces don't move, so each kind is one instanced mesh.

import * as THREE from "three";
import type { CliffLayout } from "./api";
import { ignoreMapAlpha, type Template, type UnitModels } from "./models";

// Templates are turned to face +x like units (models.ts); pieces are laid
// out in the files' own axes, so turn that back.
const TEMPLATE_TURN = Math.PI / 2;

/** Adds a map's cliff pieces to group, kind by kind as their models arrive. */
export async function buildCliffs(group: THREE.Group, layout: CliffLayout, models: UnitModels): Promise<void> {
  const kinds = new Map<string, CliffLayout["pieces"]>();
  for (const p of layout.pieces) {
    const mesh = layout.sets[p.s]?.mesh;
    if (!mesh) continue;
    const key = `cliff/${encodeURIComponent(mesh)}/${p.n}${p.v ? `?v=${p.v}` : ""}|${p.s}`;
    let k = kinds.get(key);
    if (!k) kinds.set(key, (k = []));
    k.push(p);
  }
  // Each set's material, from its material model.
  const materials = new Map<number, Promise<THREE.Material | undefined>>();
  const materialOf = (s: number) => {
    let p = materials.get(s);
    if (!p) {
      p = models.template(`cliffmaterial/${encodeURIComponent(layout.sets[s].name)}`).then((t) => (t ? firstMaterial(t) : undefined));
      materials.set(s, p);
    }
    return p;
  };
  const m = new THREE.Matrix4();
  const q = new THREE.Quaternion();
  const up = new THREE.Vector3(0, 0, 1);
  const pos = new THREE.Vector3();
  const one = new THREE.Vector3(1, 1, 1);
  const queue = [...kinds];
  const worker = async () => {
    for (let next = queue.shift(); next; next = queue.shift()) {
      const [key, pieces] = next;
      const set = pieces[0].s;
      const [tpl, material] = await Promise.all([models.template(key.split("|")[0]), materialOf(set)]);
      if (!tpl) continue;
      for (const part of parts(tpl)) {
        const mesh = new THREE.InstancedMesh(part.geometry, material ? faded(material, part.geometry) : part.material, pieces.length);
        mesh.frustumCulled = false;
        pieces.forEach((p, i) => {
          q.setFromAxisAngle(up, (p.r * Math.PI) / 2 - TEMPLATE_TURN);
          pos.set(p.x, p.y, p.z);
          m.compose(pos, q, one).multiply(part.local);
          mesh.setMatrixAt(i, m);
        });
        group.add(mesh);
      }
    }
  };
  await Promise.all([worker(), worker(), worker(), worker()]);
}

// Natural cliffs fade into the ground at their top and bottom by their
// vertex alpha (white color, so only the alpha counts). Their texture's
// alpha is a mask the game combines differently; here it would hide the
// walls, so it's ignored.
const fadedCopies = new WeakMap<THREE.Material, THREE.Material>();
function faded(material: THREE.Material, geometry: THREE.BufferGeometry): THREE.Material {
  if (!geometry.getAttribute("color")) return material;
  let m = fadedCopies.get(material);
  if (!m) {
    m = material.clone();
    m.userData = { ...material.userData };
    (m as THREE.MeshStandardMaterial).vertexColors = true;
    m.transparent = true;
    m.depthWrite = false;
    m.opacity = 1;
    ignoreMapAlpha(m);
    fadedCopies.set(material, m);
  }
  return m;
}

function firstMaterial(tpl: Template): THREE.Material | undefined {
  let out: THREE.Material | undefined;
  tpl.root.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (!out && mesh.isMesh && mesh.visible) out = Array.isArray(mesh.material) ? mesh.material[0] : mesh.material;
  });
  return out;
}

/** A template's meshes with their transforms relative to its root. */
function parts(tpl: Template): { geometry: THREE.BufferGeometry; material: THREE.Material | THREE.Material[]; local: THREE.Matrix4 }[] {
  const out: { geometry: THREE.BufferGeometry; material: THREE.Material | THREE.Material[]; local: THREE.Matrix4 }[] = [];
  tpl.root.updateMatrixWorld(true);
  const inv = tpl.root.matrixWorld.clone().invert();
  tpl.root.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (!mesh.isMesh || !mesh.visible) return;
    out.push({ geometry: mesh.geometry, material: mesh.material, local: inv.clone().multiply(mesh.matrixWorld) });
  });
  return out;
}
