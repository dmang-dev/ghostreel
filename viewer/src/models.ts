// SPDX-License-Identifier: Apache-2.0
//
// Unit models for the 3D view. The server converts the player's own game
// models to glTF on request (/api/assets/unit/<type>); this module loads one
// template per unit type and gives each unit on screen its own skinned copy.
//
// Animations follow the replay clock, not the wall clock: a unit's pose is a
// function of the loop shown, so scrubbing and pausing look right and two
// viewers of the same loop see the same frame.

import * as THREE from "three";
import { GLTFLoader } from "three/examples/jsm/loaders/GLTFLoader.js";
import * as SkeletonUtils from "three/examples/jsm/utils/SkeletonUtils.js";

/** Extras the exporter (internal/m3) writes on each animation. */
interface ClipInfo {
  clip: THREE.AnimationClip;
  words: string[]; // lower-case name tokens
  looping: boolean;
  moveSpeed: number; // cells per second the walk cycle was authored for
}

/** A loaded model, shared by every unit of a type. */
export interface Template {
  root: THREE.Object3D; // z-up, facing +x, origin on the ground
  clips: ClipInfo[];
}

/** What one unit should show this frame. */
export interface Pose {
  /** "stand", "walk", "attack", "build" or "death". */
  action: string;
  /** Seconds since the action began (replay clock). */
  time: number;
  /** Walking speed in cells per second, for matching the walk cycle. */
  speed: number;
}

// The game's models face -y in their own space; the timeline's facing is
// measured from +x, so a quarter turn lines them up.
const MODEL_FACING = Math.PI / 2;

export class UnitModels {
  available = false;
  private readonly loader = new GLTFLoader();
  private readonly templates = new Map<string, Template | null>();
  private readonly pending = new Map<string, Promise<Template | null>>();
  /** Called when a template finishes loading, so the view can redraw. */
  onLoad?: () => void;

  constructor(private readonly base: string) {}

  /** Asks the server whether it has game data to convert models from. */
  async init(): Promise<boolean> {
    try {
      const r = await fetch(`${this.base}/api/assets`);
      this.available = r.ok && (await r.json()).available === true;
    } catch {
      this.available = false;
    }
    return this.available;
  }

  /**
   * The template for a unit type (or its death model): undefined while it
   * loads (or before the first request), null when there is none.
   */
  get(type: string, death = false): Template | null | undefined {
    if (!this.available) return null;
    const key = death ? `death/${type}` : `unit/${type}`;
    const t = this.templates.get(key);
    if (t === undefined) void this.template(key);
    return t;
  }

  /**
   * Loads a template by API path under /api/assets ("unit/Marine",
   * "model/Rock?v=2"), once; null when the server has none.
   */
  template(key: string): Promise<Template | null> {
    const done = this.templates.get(key);
    if (done !== undefined) return Promise.resolve(done);
    let p = this.pending.get(key);
    if (!p) {
      p = this.load(key);
      this.pending.set(key, p);
    }
    return p;
  }

  private async load(key: string): Promise<Template | null> {
    let tpl: Template | null = null;
    try {
      const r = await fetch(`${this.base}/api/assets/${key}`);
      if (r.ok) tpl = await this.parse(await r.arrayBuffer());
    } catch (e) {
      console.warn(`model ${key}:`, e);
    }
    this.templates.set(key, tpl);
    this.pending.delete(key);
    this.onLoad?.();
    return tpl;
  }

  private async parse(buf: ArrayBuffer): Promise<Template> {
    const gltf = await this.loader.parseAsync(buf, "");
    const extras = animationExtras(buf);
    // glTF is y-up; undo the exporter's root rotation to stand the model on
    // our z-up ground, and turn it to face +x.
    const root = new THREE.Group();
    const inner = gltf.scene;
    inner.rotation.set(Math.PI / 2, 0, 0);
    const turn = new THREE.Group();
    turn.rotation.z = MODEL_FACING;
    turn.add(inner);
    root.add(turn);
    root.traverse((o) => {
      const m = o as THREE.SkinnedMesh;
      if (m.isMesh) {
        m.frustumCulled = false; // skinned bounds don't follow the pose
        const mats = materialsOf(m).map(adaptMaterial);
        if (mats.every((x) => x === null)) {
          m.visible = false;
          return;
        }
        const fallback = mats.find((x) => x !== null)!;
        const out = mats.map((x) => x ?? fallback);
        m.material = Array.isArray(m.material) ? out : out[0];
        for (const mat of out) if (mat.userData.fixedAlpha) ignoreMapAlpha(mat);
      }
    });
    const clips = gltf.animations.map((clip, i): ClipInfo => ({
      clip,
      words: clip.name.toLowerCase().split(/\s+/),
      looping: extras[i]?.looping ?? true,
      moveSpeed: extras[i]?.moveSpeed ?? 0,
    }));
    return { root, clips };
  }

  /** A unit's own copy of a template. */
  instance(tpl: Template, team: THREE.Color): UnitInstance {
    return new UnitInstance(tpl, team);
  }
}

/** One unit's model: a skinned clone with its own mixer and team color. */
export class UnitInstance {
  readonly root: THREE.Object3D;
  private readonly mixer: THREE.AnimationMixer;
  private current?: { info: ClipInfo; action: THREE.AnimationAction };
  private readonly team = new THREE.Color();
  private readonly teamUniform = { value: new THREE.Color() };
  lastSeen = 0; // frame counter, for evicting units that left the screen

  constructor(private readonly tpl: Template, team: THREE.Color) {
    this.root = SkeletonUtils.clone(tpl.root);
    this.mixer = new THREE.AnimationMixer(this.root);
    this.root.traverse((o) => {
      const m = o as THREE.Mesh;
      if (!m.isMesh) return;
      // Per-unit materials so each carries its team color.
      const mats = materialsOf(m).map((mat) => {
        const c = mat.clone();
        c.userData = { ...mat.userData };
        // (clone() keeps neither shader hook, so reapply them)
        if (mat.userData.teamColor) withTeamColor(c, this.teamUniform);
        if (mat.userData.fixedAlpha) ignoreMapAlpha(c);
        return c;
      });
      m.material = Array.isArray(m.material) ? mats : mats[0];
    });
    this.setTeam(team);
  }

  setTeam(c: THREE.Color): void {
    if (this.team.equals(c)) return;
    this.team.copy(c);
    this.teamUniform.value.copy(c);
  }

  /** Poses the model for this frame. */
  pose(p: Pose): void {
    const info = pickClip(this.tpl.clips, p.action);
    if (!info) {
      if (this.current) {
        this.current.action.stop();
        this.current = undefined;
      }
      return;
    }
    if (this.current?.info !== info) {
      this.current?.action.stop(); // unbinding restores bones the next clip doesn't animate
      const action = this.mixer.clipAction(info.clip);
      action.play();
      this.current = { info, action };
    }
    const dur = info.clip.duration;
    let t = p.time;
    if (p.action === "walk" && info.moveSpeed > 0 && p.speed > 0) {
      t *= THREE.MathUtils.clamp(p.speed / info.moveSpeed, 0.5, 2);
    }
    if (dur > 0) t = info.looping && p.action !== "death" ? t % dur : Math.min(t, dur - 1e-3);
    this.current.action.time = t;
    this.mixer.update(0);
  }

  dispose(): void {
    this.mixer.stopAllAction();
    this.root.traverse((o) => {
      const m = o as THREE.Mesh;
      if (m.isMesh) for (const mat of materialsOf(m)) mat.dispose();
    });
  }
}

const SKIP = new Set(["start", "end", "cover", "flail", "unload", "spell", "morph"]);

/** Chooses a clip for an action, preferring the plainest name that fits. */
export function pickClip(clips: ClipInfo[], action: string): ClipInfo | undefined {
  const want: Record<string, string[][]> = {
    stand: [["stand"], ["fly"]],
    walk: [["walk"], ["fly"], ["stand"]],
    attack: [["attack"], ["stand"]],
    build: [["birth"], ["build"], ["stand"]],
    death: [["death"]],
  };
  for (const words of want[action] ?? [["stand"]]) {
    let best: ClipInfo | undefined;
    let bestScore = Infinity;
    for (const c of clips) {
      if (!words.every((w) => c.words.includes(w))) continue;
      // Fewer extra words is plainer; numbered variants and transitions last.
      let score = c.words.length * 10;
      for (const w of c.words) {
        if (SKIP.has(w)) score += 100;
        if (/^\d+$/.test(w)) score += 5;
        if (w === "work") score += 20; // "Stand Work" is for busy structures
      }
      if (score < bestScore) {
        best = c;
        bestScore = score;
      }
    }
    if (best) return best;
  }
  return undefined;
}

function materialsOf(m: THREE.Mesh): THREE.Material[] {
  return Array.isArray(m.material) ? m.material : [m.material];
}

/**
 * Applies the game's material conventions the glTF can't express, returning
 * the material to use (null: draw nothing). For opaque materials the diffuse
 * texture's alpha is the team-color mask (0 = team color, 1 = texture), not
 * opacity. Additive and unshaded materials (glows, energy, holograms) are
 * unlit and show their own texture, the emissive one when there's no color
 * texture.
 */
function adaptMaterial(mat: THREE.Material): THREE.Material | null {
  const blend = (mat.userData?.blendMode as number | undefined) ?? 0;
  const std = mat as THREE.MeshStandardMaterial;
  if (blend === 2 || blend === 3 || mat.userData?.unshaded) {
    const map = std.map ?? std.emissiveMap;
    if (!map) return null;
    const additive = blend === 2 || blend === 3;
    const basic = new THREE.MeshBasicMaterial({
      map, side: mat.side, transparent: additive || blend === 1,
      blending: additive ? THREE.AdditiveBlending : THREE.NormalBlending,
      depthWrite: !(additive || blend === 1),
    });
    basic.userData = { ...mat.userData };
    if (blend === 0) basic.userData.teamColor = true;
    mat.dispose();
    return basic;
  }
  if (blend === 0 && std.map) {
    mat.userData.teamColor = true;
    mat.transparent = false;
  } else if (blend === 1) {
    mat.depthWrite = false;
    // Without an alpha layer the texture's alpha is not opacity (the
    // exporter folds the alpha layer in when there is one).
    if (!mat.userData.alphaLayer) mat.userData.fixedAlpha = true;
  }
  if (std.isMeshStandardMaterial) {
    std.metalness = 0;
    std.roughness = 0.75;
  }
  return mat;
}

/** Mixes the team color into an opaque material where its texture's alpha is low. */
function withTeamColor(mat: THREE.Material, team: { value: THREE.Color }): void {
  mat.onBeforeCompile = (shader) => {
    shader.uniforms.teamColor = team;
    shader.fragmentShader = shader.fragmentShader
      .replace("#include <common>", "#include <common>\nuniform vec3 teamColor;")
      .replace(
        "#include <map_fragment>",
        `#ifdef USE_MAP
  vec4 texel = texture2D( map, vMapUv );
  diffuseColor.rgb *= mix( teamColor, texel.rgb, texel.a );
  diffuseColor.a = 1.0;
#endif`,
      );
  };
  mat.customProgramCacheKey = () => "teamcolor";
}

/** Makes a blended material's opacity come from opacity alone, not its texture. */
function ignoreMapAlpha(mat: THREE.Material): void {
  mat.onBeforeCompile = (shader) => {
    shader.fragmentShader = shader.fragmentShader.replace(
      "#include <map_fragment>",
      "#include <map_fragment>\n  diffuseColor.a = opacity;",
    );
  };
  mat.customProgramCacheKey = () => "fixedalpha";
}

/** Reads each animation's extras (looping, moveSpeed) from a GLB's JSON chunk. */
function animationExtras(buf: ArrayBuffer): { looping?: boolean; moveSpeed?: number }[] {
  try {
    const dv = new DataView(buf);
    const len = dv.getUint32(12, true);
    const json = JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 20, len)));
    return (json.animations ?? []).map((a: { extras?: object }) => a.extras ?? {});
  } catch {
    return [];
  }
}
