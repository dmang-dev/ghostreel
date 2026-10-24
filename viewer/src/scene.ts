// SPDX-License-Identifier: Apache-2.0
//
// The game view in three.js, in two modes:
//
//   2D: top-down, orthographic camera, units as flat circles and footprints.
//   3D: perspective camera over a heightfield built from the map's terrain
//       heights. Units are the game's own models when the server can read
//       the player's game data (see models.ts), and stand-in solids
//       (cylinders, footprint boxes; air units float) otherwise or while a
//       model loads.
//
// World units are map cells, x right, y up, z up (heights), so timeline
// coordinates go straight into the scene. Units are instanced meshes: a
// handful of draw calls per frame whatever the unit count.
//
// Fog of war: with fogPlayer set, each frame computes that player's vision
// from its units' sight radii, darkens what it can't see and hides enemy
// units there, like the in-game player view.

import * as THREE from "three";
import { LOOPS_PER_CLOCK_SECOND, Model, UnitClass, isAlive, positionAt, type UnitState } from "./model";
import type { UnitInstance, UnitModels } from "./models";

/** What the renderer drew for one unit this frame; shared with UI and picking. */
export interface DrawnUnit {
  id: number;
  x: number;
  y: number;
  r: number; // pick/selection radius in cells
  w: number; // footprint in cells; 0 for round units
  h: number;
  owner: number;
  type: number;
  air: boolean;
  constructing: boolean;
  hidden: boolean; // in the fog for the chosen player
  facing: number;
  moving: boolean;
  attacking: boolean;
}

export type ViewMode = "2d" | "3d";

const NEUTRAL = {
  mineral: new THREE.Color("#b5ecff"),
  gas: new THREE.Color("#42d392"),
  rock: new THREE.Color("#8d8d84"),
  other: new THREE.Color("#b0b0b0"),
};
const DEATH_WINDOW = 112; // loops a death marker stays visible (5 s on the clock)
const AIR_HEIGHT = 4; // cells above the ground for flying units in 3D
const FOG_DARK = 0.62; // how much unseen ground is darkened
const GROUND_TILE = 8; // cells per repeat of the ground texture
// Creep radius (cells) around each source when fully spread, and the time
// it takes to spread (loops).
const CREEP_RADIUS: Record<string, number> = {
  Hatchery: 12, Lair: 12, Hive: 12, CreepTumor: 10, CreepTumorBurrowed: 10, CreepTumorQueen: 10,
};
const CREEP_GROWTH = 22.4 * 12;
const EVICT_FRAMES = 600; // drop a unit's model after this many frames unused
// Models give way to stand-ins when they'd be a few pixels tall, and past a
// count where skinning every unit costs more than it shows.
const MODEL_MIN_PPC = 3;
const MAX_MODELS = 400;

export class Scene {
  readonly canvas: HTMLCanvasElement;
  private readonly labels: HTMLCanvasElement;
  private readonly renderer: THREE.WebGLRenderer;
  private readonly scene = new THREE.Scene();
  private readonly ortho = new THREE.OrthographicCamera(-1, 1, 1, -1, 0.1, 1000);
  private readonly persp = new THREE.PerspectiveCamera(45, 1, 0.5, 2000);
  private readonly sun = new THREE.DirectionalLight(0xffffff, 1.6);
  private readonly ambient = new THREE.AmbientLight(0xffffff, 0.9);

  private terrain2?: THREE.Mesh;
  private terrain3?: THREE.Mesh;
  private fogMesh2?: THREE.Mesh;
  private fogMesh3?: THREE.Mesh;
  private creepMesh2?: THREE.Mesh;
  private creepMesh3?: THREE.Mesh;
  private creepTex?: THREE.DataTexture;
  private creepData?: Uint8Array;
  private creepKey = "";
  private pathable?: Uint8Array;
  private terrainTex?: THREE.CanvasTexture;
  private groundDetail?: THREE.Texture;
  private useDetail = true;
  private detailGain = 1;
  private fogTex?: THREE.DataTexture;
  private fogData?: Uint8Array;

  private model?: Model;
  private worldW = 1;
  private worldH = 1;
  private heights?: Float32Array; // (w+1)*(h+1) corner heights, row 0 at y=0
  private playerColors = new Map<number, THREE.Color>();

  private meshes2?: Meshes;
  private meshes3?: Meshes;
  private readonly selection2: THREE.Mesh;
  private readonly selection3: THREE.Mesh;

  mode: ViewMode = "2d";
  /** Camera: center in cells, zoom in pixels per cell, and in 3D a heading and tilt (radians). */
  view = { cx: 0, cy: 0, ppc: 4, yaw: 0, pitch: 0.95 };
  /** Player whose vision to show (0 = everything). */
  fogPlayer = 0;
  showLabels = true;
  onViewChange?: () => void;
  /** The user moved the camera (drag, rotate, zoom). */
  onUserMove?: () => void;
  /** Set while exporting video: render at this size, not the window's. */
  private fixed?: { w: number; h: number };
  /** A click (not a drag) at a point in the canvas, in CSS pixels. */
  onClick?: (px: number, py: number) => void;

  /** Game models, when the server has game data. */
  models?: UnitModels;
  private readonly instances = new Map<number, { inst: UnitInstance; type: string; death: boolean }>();
  private doodads?: THREE.Group;
  private frame = 0;

  private drawn: DrawnUnit[] = [];
  private drawnCount = 0;
  private readonly tmp: UnitState = { id: 0, type: 0, owner: 0, x: 0, y: 0, facing: 0, constructing: false, moving: false, attacking: false };
  private readonly dead: DrawnUnit = {
    id: 0, x: 0, y: 0, r: 0, w: 0, h: 0, owner: 0, type: 0, air: false, constructing: false, hidden: false,
    facing: 0, moving: false, attacking: false,
  };
  private readonly m4 = new THREE.Matrix4();
  private readonly q = new THREE.Quaternion();
  private readonly v = new THREE.Vector3();
  private readonly s = new THREE.Vector3();
  private readonly c = new THREE.Color();
  private readonly ray = new THREE.Raycaster();

  constructor(private readonly container: HTMLElement) {
    this.renderer = new THREE.WebGLRenderer({ antialias: true });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    this.renderer.setClearColor(0x0b0d10);
    this.canvas = this.renderer.domElement;
    this.canvas.className = "gl";
    this.labels = document.createElement("canvas");
    this.labels.className = "labels";
    container.append(this.canvas, this.labels);

    this.persp.up.set(0, 0, 1);
    this.sun.position.set(-0.5, 0.6, 1);
    this.scene.add(this.sun, this.ambient);

    const ringMat = new THREE.MeshBasicMaterial({ color: 0xffffff, depthTest: false, transparent: true });
    this.selection2 = new THREE.Mesh(new THREE.RingGeometry(0.62, 0.78, 40), ringMat);
    this.selection2.renderOrder = 10;
    this.selection3 = new THREE.Mesh(new THREE.RingGeometry(0.62, 0.78, 40), ringMat);
    this.selection3.renderOrder = 10;
    this.selection2.visible = this.selection3.visible = false;
    this.scene.add(this.selection2, this.selection3);

    new ResizeObserver(() => this.resize()).observe(container);
    this.attachControls();
    this.resize();
  }

  setWorld(
    model: Model, width: number, height: number, terrain: HTMLCanvasElement,
    heights?: ArrayLike<number> | null, pathable?: ArrayLike<number> | null,
  ): void {
    this.model = model;
    this.worldW = width;
    this.worldH = height;
    this.heights = heights && heights.length === (width + 1) * (height + 1) ? Float32Array.from(heights) : undefined;
    this.playerColors.clear();
    for (const p of model.tl.meta?.players ?? []) this.playerColors.set(p.id, new THREE.Color(p.color));

    for (const ms of [this.meshes2, this.meshes3]) ms?.dispose(this.scene);
    const n = Math.max(model.units.length, 1);
    this.meshes2 = Meshes.flat(n, this.scene);
    this.meshes3 = Meshes.solid(n, this.scene);
    this.drawn = Array.from({ length: n }, () => ({
      id: 0, x: 0, y: 0, r: 0, w: 0, h: 0, owner: 0, type: 0, air: false, constructing: false, hidden: false,
      facing: 0, moving: false, attacking: false,
    }));
    this.clearInstances();
    this.setDoodads(undefined);
    this.setGroundDetail(undefined);

    this.fogData = new Uint8Array(width * height * 4);
    this.fogTex?.dispose();
    this.fogTex = new THREE.DataTexture(this.fogData, width, height, THREE.RGBAFormat);
    this.fogTex.magFilter = this.fogTex.minFilter = THREE.LinearFilter;
    this.fogTex.needsUpdate = true;
    this.pathable = pathable && pathable.length === width * height ? Uint8Array.from(pathable) : undefined;
    this.creepData = new Uint8Array(width * height * 4);
    this.creepTex?.dispose();
    this.creepTex = new THREE.DataTexture(this.creepData, width, height, THREE.RGBAFormat);
    this.creepTex.magFilter = this.creepTex.minFilter = THREE.LinearFilter;
    this.creepTex.needsUpdate = true;
    this.creepKey = "";
    this.setTerrain(terrain);
    this.applyMode();
  }

  /** Sets the terrain image; detail tiles the 3D ground texture over it. */
  setTerrain(canvas: HTMLCanvasElement, detail = true): void {
    this.useDetail = detail;
    for (const m of [this.terrain2, this.terrain3, this.fogMesh2, this.fogMesh3, this.creepMesh2, this.creepMesh3]) {
      if (m) {
        this.scene.remove(m);
        m.geometry.dispose();
        (m.material as THREE.Material).dispose();
      }
    }
    this.terrainTex?.dispose();
    this.terrainTex = new THREE.CanvasTexture(canvas);
    this.terrainTex.colorSpace = THREE.SRGBColorSpace;
    this.terrainTex.anisotropy = 8;

    // 2D: a flat plane.
    const flat = new THREE.PlaneGeometry(this.worldW, this.worldH);
    this.terrain2 = new THREE.Mesh(flat, new THREE.MeshBasicMaterial({ map: this.terrainTex, depthTest: false }));
    this.terrain2.position.set(this.worldW / 2, this.worldH / 2, 0);
    this.terrain2.renderOrder = 0;

    // 3D: the same texture on a heightfield, lit.
    const field = this.heightfield();
    this.terrain3 = new THREE.Mesh(field, new THREE.MeshLambertMaterial({ map: this.terrainTex }));
    this.applyGroundDetail();

    // Fog overlays: black, with the fog texture as per-cell opacity.
    const fogMat = () => new THREE.MeshBasicMaterial({
      color: 0x000000, transparent: true, alphaMap: this.fogTex, depthWrite: false,
      polygonOffset: true, polygonOffsetFactor: -2, polygonOffsetUnits: -2,
    });
    this.fogMesh2 = new THREE.Mesh(flat.clone(), fogMat());
    this.fogMesh2.position.copy(this.terrain2.position);
    this.fogMesh2.renderOrder = 1;
    (this.fogMesh2.material as THREE.Material).depthTest = false;
    this.fogMesh3 = new THREE.Mesh(field.clone(), fogMat());
    this.fogMesh3.renderOrder = 1;
    // Creep: purple over the ground where Zerg structures spread it.
    const creepMat = () => new THREE.MeshLambertMaterial({
      color: 0x4a1f5c, transparent: true, opacity: 0.8, alphaMap: this.creepTex, depthWrite: false,
      polygonOffset: true, polygonOffsetFactor: -1, polygonOffsetUnits: -1,
    });
    this.creepMesh2 = new THREE.Mesh(flat.clone(), new THREE.MeshBasicMaterial({
      color: 0x4a1f5c, transparent: true, opacity: 0.75, alphaMap: this.creepTex, depthTest: false,
    }));
    this.creepMesh2.position.copy(this.terrain2.position);
    this.creepMesh2.renderOrder = 0.5;
    this.creepMesh3 = new THREE.Mesh(field.clone(), creepMat());
    this.creepMesh3.renderOrder = 0.5;
    this.scene.add(this.terrain2, this.terrain3, this.creepMesh2, this.creepMesh3, this.fogMesh2, this.fogMesh3);
    this.applyMode();
  }

  /**
   * Tiles a ground texture (the map's base terrain layer) over the 3D
   * terrain, colored by the terrain image's shading. The game blends several
   * layers by painted masks; those aren't decoded yet, so one layer stands
   * in for all of them.
   */
  setGroundDetail(tex: THREE.Texture | undefined): void {
    this.groundDetail?.dispose();
    this.groundDetail = tex;
    if (tex) {
      tex.wrapS = tex.wrapT = THREE.RepeatWrapping;
      tex.colorSpace = THREE.SRGBColorSpace;
      this.detailGain = Math.min(3, 0.5 / Math.max(averageLuminance(tex.image as CanvasImageSource & { width: number; height: number }), 0.05));
    }
    this.applyGroundDetail();
  }

  private applyGroundDetail(): void {
    const mat = this.terrain3?.material as THREE.MeshLambertMaterial | undefined;
    if (!mat) return;
    const tex = this.useDetail ? this.groundDetail : undefined;
    mat.onBeforeCompile = (shader) => {
      if (!tex) return;
      shader.uniforms.detailMap = { value: tex };
      // One texture repeat per GROUND_TILE cells.
      shader.uniforms.detailRepeat = { value: new THREE.Vector2(this.worldW / GROUND_TILE, this.worldH / GROUND_TILE) };
      shader.uniforms.detailGain = { value: this.detailGain };
      shader.fragmentShader = shader.fragmentShader
        .replace("#include <common>", "#include <common>\nuniform sampler2D detailMap;\nuniform vec2 detailRepeat;\nuniform float detailGain;")
        .replace(
          "#include <map_fragment>",
          `#include <map_fragment>
  // The texture at a steady brightness, with the terrain image's shading
  // (low ground darker, cliffs shaded) applied gently.
  vec3 detail = texture2D( detailMap, vMapUv * detailRepeat ).rgb * detailGain;
  float shade = 0.6 + 0.4 * clamp( dot( diffuseColor.rgb, vec3( 0.299, 0.587, 0.114 ) ) / 0.42, 0.0, 1.6 );
  diffuseColor.rgb = mix( diffuseColor.rgb, detail * shade, 0.85 );`,
        );
    };
    mat.customProgramCacheKey = () => (tex ? "ground-detail" : "ground");
    mat.needsUpdate = true;
  }

  /** A plane with one vertex per terrain corner, raised to the map's heights. */
  private heightfield(): THREE.BufferGeometry {
    const w = this.worldW;
    const h = this.worldH;
    const geo = new THREE.PlaneGeometry(w, h, w, h);
    const pos = geo.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < pos.count; i++) {
      const col = i % (w + 1);
      const row = Math.floor(i / (w + 1)); // PlaneGeometry rows run top to bottom
      const y = h - row;
      pos.setXYZ(i, col, y, this.heights ? this.heights[y * (w + 1) + col] : 0);
    }
    pos.needsUpdate = true;
    geo.computeVertexNormals();
    return geo;
  }

  /** Terrain height at a point (bilinear between corners), 0 without a map. */
  heightAt(x: number, y: number): number {
    const hs = this.heights;
    if (!hs) return 0;
    const w = this.worldW;
    const cx = Math.min(Math.max(x, 0), w - 1e-3);
    const cy = Math.min(Math.max(y, 0), this.worldH - 1e-3);
    const x0 = Math.floor(cx), y0 = Math.floor(cy);
    const fx = cx - x0, fy = cy - y0;
    const at = (xx: number, yy: number) => hs[yy * (w + 1) + xx];
    return (at(x0, y0) * (1 - fx) + at(x0 + 1, y0) * fx) * (1 - fy) + (at(x0, y0 + 1) * (1 - fx) + at(x0 + 1, y0 + 1) * fx) * fy;
  }

  setMode(mode: ViewMode): void {
    this.mode = mode;
    this.applyMode();
    this.updateCamera();
  }

  private applyMode(): void {
    const three = this.mode === "3d";
    if (this.terrain2) this.terrain2.visible = !three;
    if (this.terrain3) this.terrain3.visible = three;
    if (this.creepMesh2) this.creepMesh2.visible = !three;
    if (this.creepMesh3) this.creepMesh3.visible = three;
    this.meshes2?.setVisible(!three);
    this.meshes3?.setVisible(three);
    if (this.doodads) this.doodads.visible = three;
    this.selection2.visible = this.selection3.visible = false;
    this.updateFogVisibility();
  }

  private updateFogVisibility(): void {
    const on = this.fogPlayer !== 0;
    if (this.fogMesh2) this.fogMesh2.visible = on && this.mode === "2d";
    if (this.fogMesh3) this.fogMesh3.visible = on && this.mode === "3d";
  }

  /** Shows a map's doodads in 3D (see doodads.ts); undefined removes them. */
  setDoodads(group: THREE.Group | undefined): void {
    if (this.doodads) {
      this.scene.remove(this.doodads);
      // Geometry and materials belong to the shared model templates.
      this.doodads.traverse((o) => (o as THREE.InstancedMesh).isInstancedMesh && (o as THREE.InstancedMesh).dispose());
    }
    this.doodads = group;
    if (group) {
      group.visible = this.mode === "3d";
      this.scene.add(group);
    }
  }

  setFogPlayer(p: number): void {
    this.fogPlayer = p;
    this.updateFogVisibility();
  }

  /** Fits a rectangle (in cells) to the viewport. */
  fit(left: number, bottom: number, right: number, top: number): void {
    const { w, h } = this.size();
    this.view.cx = (left + right) / 2;
    this.view.cy = (bottom + top) / 2;
    this.view.ppc = Math.max(1, Math.min(w / (right - left), h / (top - bottom)) * 0.95);
    this.updateCamera();
  }

  colorOf(owner: number): THREE.Color {
    return this.playerColors.get(owner) ?? NEUTRAL.other;
  }

  private neutralColor(name: string): THREE.Color {
    if (name.includes("MineralField")) return NEUTRAL.mineral;
    if (name.includes("Geyser")) return NEUTRAL.gas;
    return NEUTRAL.rock;
  }

  /** Draws the game at loop. */
  render(loop: number, selected: number | undefined): void {
    const model = this.model;
    const ms = this.mode === "3d" ? this.meshes3 : this.meshes2;
    if (!model || !ms) {
      this.renderer.render(this.scene, this.camera());
      return;
    }
    // Pass 1: where everything is.
    const s = this.tmp;
    let nDrawn = 0;
    for (const u of model.units) {
      if (!isAlive(u, loop)) continue;
      model.evalUnit(u, loop, s);
      const t = model.types[s.type];
      if (!t || t.class === UnitClass.HIDDEN) continue;
      const dr = this.drawn[nDrawn++];
      dr.id = s.id; dr.x = s.x; dr.y = s.y; dr.owner = s.owner; dr.type = s.type;
      dr.air = t.air; dr.constructing = s.constructing; dr.hidden = false;
      dr.facing = s.facing; dr.moving = !!s.moving; dr.attacking = !!s.attacking;
      if (t.air || !t.footprintW) {
        dr.r = Math.max(t.radius, 0.25);
        dr.w = dr.h = 0;
      } else {
        dr.w = t.footprintW; dr.h = t.footprintH; dr.r = Math.max(t.footprintW, t.footprintH) / 2;
      }
    }
    this.drawnCount = nDrawn;

    this.updateCreep(model, loop);

    // Pass 2: fog of war from the chosen player's units.
    const visible = this.fogPlayer ? this.computeFog(model) : undefined;

    // Pass 3: models and instances.
    let nf = 0, ng = 0, na = 0, nd = 0;
    let selDrawn: DrawnUnit | undefined;
    const three = this.mode === "3d";
    const useModels = three && !!this.models?.available && this.pixelsPerCellAt(this.view.cx, this.view.cy) >= MODEL_MIN_PPC;
    let nModels = 0;
    this.frame++;
    const clock = loop / LOOPS_PER_CLOCK_SECOND;
    for (let i = 0; i < nDrawn; i++) {
      const dr = this.drawn[i];
      const t = model.types[dr.type];
      if (visible && dr.owner !== this.fogPlayer && dr.owner !== 0 && !visible(dr.x, dr.y)) {
        dr.hidden = true;
        continue;
      }
      if (dr.id === selected) selDrawn = dr;
      if (useModels && nModels < MAX_MODELS && this.onScreen(dr)) {
        const action = dr.constructing ? "build" : dr.moving ? "walk" : dr.attacking ? "attack" : "stand";
        // A per-unit phase keeps identical units from moving in lockstep.
        if (this.placeModel(t.name, false, dr, action, clock + (dr.id % 97) * 0.173, t.speed * 1.4)) {
          nModels++;
          continue;
        }
      }
      this.c.copy(dr.owner === 0 ? this.neutralColor(t.name) : this.colorOf(dr.owner));
      if (dr.constructing) this.c.multiplyScalar(0.45);
      const ground = three ? this.heightAt(dr.x, dr.y) : 0;
      if (dr.w) {
        // Structures: a footprint (2D) or a box whose height grows with its size (3D).
        const tall = three ? (dr.owner === 0 ? (t.name.includes("Mineral") ? 1.2 : 1.6) : 1 + dr.w * 0.45) : 1;
        const sz = dr.constructing && three ? tall * 0.5 : tall;
        this.place(ms.footprints, nf, dr.x, dr.y, ground + (three ? sz / 2 : 0), dr.w - 0.15, dr.h - 0.15, sz);
        ms.footprints.setColorAt(nf++, this.c);
      } else {
        const d = dr.r * 2;
        const lift = three ? (dr.air ? AIR_HEIGHT : 0) + dr.r : 0;
        const mesh = dr.air ? ms.air : ms.ground;
        const k = dr.air ? na++ : ng++;
        this.place(mesh, k, dr.x, dr.y, ground + lift, d, d, three ? d : 1);
        mesh.setColorAt(k, this.c);
      }
    }
    // Death markers.
    for (const u of model.units) {
      if (u.diedLoop === undefined || loop < u.diedLoop || loop - u.diedLoop >= DEATH_WINDOW || u.owner === 0) continue;
      const t = model.types[u.type];
      if (!t || t.class === UnitClass.HIDDEN || t.footprintW) continue;
      positionAt(model.tracks[u.id], 0, u.diedLoop, s);
      if (visible && u.owner !== this.fogPlayer && !visible(s.x, s.y)) continue;
      if (useModels) {
        const dr = this.dead;
        dr.id = u.id; dr.x = s.x; dr.y = s.y; dr.owner = u.owner; dr.air = t.air; dr.facing = s.facing;
        const since = (loop - u.diedLoop) / LOOPS_PER_CLOCK_SECOND;
        if (this.onScreen(dr) && this.placeModel(t.name, true, dr, "death", since, 0)) continue;
      }
      const fade = 1 - (loop - u.diedLoop) / DEATH_WINDOW;
      const d = 1 + 1.6 * (1 - fade);
      this.place(ms.deaths, nd, s.x, s.y, three ? this.heightAt(s.x, s.y) + 0.15 : 0, d, d, 1);
      this.c.setRGB(0.95 * fade + 0.05, 0.25 * fade, 0.2 * fade);
      ms.deaths.setColorAt(nd++, this.c);
    }
    ms.commit(nf, ng, na, nd);
    this.sweepInstances(useModels);

    const sel = three ? this.selection3 : this.selection2;
    if (selDrawn && !selDrawn.hidden) {
      const d = Math.max(selDrawn.r * 2, 1) * (selDrawn.w ? 1.2 : 1);
      const z = three ? this.heightAt(selDrawn.x, selDrawn.y) + (selDrawn.air ? AIR_HEIGHT : 0) + 0.1 : 0;
      sel.position.set(selDrawn.x, selDrawn.y, z);
      sel.scale.set(d, d, 1);
      sel.visible = true;
    } else {
      sel.visible = false;
    }
    this.renderer.render(this.scene, this.camera());
    this.drawLabels(selected);
  }

  /**
   * Vision of the fog player: a cell is visible if one of the player's units
   * has it within its sight radius. Updates the fog texture and returns a
   * lookup. (High-ground vision rules are not modelled.)
   */
  private computeFog(model: Model): (x: number, y: number) => boolean {
    const w = this.worldW, h = this.worldH;
    const seen = new Uint8Array(w * h);
    for (let i = 0; i < this.drawnCount; i++) {
      const d = this.drawn[i];
      if (d.owner !== this.fogPlayer) continue;
      const r = model.types[d.type]?.sight || 0;
      if (r <= 0) continue;
      const x0 = Math.max(0, Math.floor(d.x - r)), x1 = Math.min(w - 1, Math.ceil(d.x + r));
      const y0 = Math.max(0, Math.floor(d.y - r)), y1 = Math.min(h - 1, Math.ceil(d.y + r));
      const r2 = r * r;
      for (let y = y0; y <= y1; y++) {
        const dy = y + 0.5 - d.y;
        for (let x = x0; x <= x1; x++) {
          const dx = x + 0.5 - d.x;
          if (dx * dx + dy * dy <= r2) seen[y * w + x] = 1;
        }
      }
    }
    const data = this.fogData!;
    const dark = Math.round(FOG_DARK * 255);
    for (let i = 0; i < w * h; i++) {
      // alphaMap reads the green channel; the texture's row 0 is y = 0.
      data[i * 4 + 1] = seen[i] ? 0 : dark;
      data[i * 4 + 3] = 255;
    }
    this.fogTex!.needsUpdate = true;
    return (x, y) => {
      const xi = Math.floor(x), yi = Math.floor(y);
      return xi >= 0 && yi >= 0 && xi < w && yi < h && seen[yi * w + xi] === 1;
    };
  }

  /** Whether a unit is near the visible part of the map (3D). */
  private onScreen(d: DrawnUnit): boolean {
    const [sx, sy] = this.worldToScreen(d.x, d.y, d.air ? AIR_HEIGHT : 0);
    if (this.v.z > 1) return false; // behind the camera
    const { w, h } = this.size();
    const m = 200;
    return sx > -m && sy > -m && sx < w + m && sy < h + m;
  }

  /**
   * Draws a unit with its game model if one is loaded, returning false to
   * fall back to the stand-in. Death models are separate files in the game;
   * while one loads (or when there is none) the unit's own "Death" clip, if
   * any, is used instead.
   */
  private placeModel(type: string, death: boolean, d: DrawnUnit, action: string, time: number, speed: number): boolean {
    const models = this.models!;
    let tpl = death ? models.get(type, true) : models.get(type);
    let isDeathModel = death;
    if (death && !tpl) {
      tpl = models.get(type);
      isDeathModel = false;
      if (tpl && !tpl.clips.some((c) => c.words.includes("death"))) return false;
    }
    if (!tpl) return false;
    let e = this.instances.get(d.id);
    if (!e || e.type !== type || e.death !== isDeathModel) {
      if (e) this.removeInstance(d.id);
      e = { inst: models.instance(tpl, this.colorOf(d.owner)), type, death: isDeathModel };
      this.instances.set(d.id, e);
      this.scene.add(e.inst.root);
    }
    const inst = e.inst;
    inst.setTeam(d.owner === 0 ? NEUTRAL.other : this.colorOf(d.owner));
    inst.lastSeen = this.frame;
    const root = inst.root;
    root.visible = true;
    root.position.set(d.x, d.y, this.heightAt(d.x, d.y) + (d.air ? AIR_HEIGHT : 0));
    // Structures keep the orientation they were modelled in.
    root.rotation.z = d.w ? -Math.PI / 2 : d.facing;
    inst.pose({ action: isDeathModel ? "death" : action, time, speed });
    return true;
  }

  /** Hides models not drawn this frame and frees long-unused ones. */
  private sweepInstances(active: boolean): void {
    for (const [id, e] of this.instances) {
      if (!active || e.inst.lastSeen !== this.frame) e.inst.root.visible = false;
      if (this.frame - e.inst.lastSeen > EVICT_FRAMES) this.removeInstance(id);
    }
  }

  private removeInstance(id: number): void {
    const e = this.instances.get(id);
    if (!e) return;
    this.scene.remove(e.inst.root);
    e.inst.dispose();
    this.instances.delete(id);
  }

  private clearInstances(): void {
    for (const id of [...this.instances.keys()]) this.removeInstance(id);
  }

  /**
   * Creep around Zerg town halls and creep tumors, grown over CREEP_GROWTH
   * after they finish and limited to walkable ground. An estimate: the game
   * spreads creep cell by cell and lets it recede slowly after a source dies.
   */
  private updateCreep(model: Model, loop: number): void {
    const parts: string[] = [];
    const sources: { x: number; y: number; r: number }[] = [];
    for (let i = 0; i < this.drawnCount; i++) {
      const d = this.drawn[i];
      const full = CREEP_RADIUS[model.types[d.type]?.name ?? ""];
      if (!full || d.constructing) continue;
      const u = model.units[d.id];
      const start = u.constructed ? (u.doneLoop ?? loop) : u.bornLoop;
      const r = Math.round(full * Math.min(1, Math.max(0.15, (loop - start) / CREEP_GROWTH)) * 2) / 2;
      sources.push({ x: d.x, y: d.y, r });
      parts.push(`${d.id}:${r}`);
    }
    const key = parts.join(",");
    if (key === this.creepKey) return;
    this.creepKey = key;
    const w = this.worldW, h = this.worldH;
    const data = this.creepData!;
    data.fill(0);
    for (const s of sources) {
      const r2 = s.r * s.r;
      for (let y = Math.max(0, Math.floor(s.y - s.r)); y <= Math.min(h - 1, Math.ceil(s.y + s.r)); y++) {
        for (let x = Math.max(0, Math.floor(s.x - s.r)); x <= Math.min(w - 1, Math.ceil(s.x + s.r)); x++) {
          const dx = x + 0.5 - s.x, dy = y + 0.5 - s.y;
          if (dx * dx + dy * dy > r2) continue;
          if (this.pathable && !this.pathable[y * w + x]) continue;
          data[(y * w + x) * 4 + 1] = 255; // alphaMap reads green
        }
      }
    }
    this.creepTex!.needsUpdate = true;
  }

  /** Units drawn in the last frame (hidden ones excluded). */
  drawnUnits(): DrawnUnit[] {
    return this.drawn.slice(0, this.drawnCount).filter((d) => !d.hidden);
  }

  private place(mesh: THREE.InstancedMesh, i: number, x: number, y: number, z: number, sx: number, sy: number, sz: number): void {
    this.m4.compose(this.v.set(x, y, z), this.q, this.s.set(sx, sy, sz));
    mesh.setMatrixAt(i, this.m4);
  }

  private drawLabels(selected: number | undefined): void {
    const ctx = this.labels.getContext("2d")!;
    const dpr = this.labels.width / Math.max(this.size().w, 1);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, this.size().w, this.size().h);
    const model = this.model;
    if (!model) return;
    const show = this.showLabels && this.view.ppc >= 9;
    ctx.font = "11px system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const { w, h } = this.size();
    for (let i = 0; i < this.drawnCount; i++) {
      const d = this.drawn[i];
      if (d.hidden || (!show && d.id !== selected)) continue;
      if (d.owner === 0 && d.id !== selected) continue; // resources are recognizable by color
      const lift = this.mode === "3d" && d.air ? AIR_HEIGHT : 0;
      const [sx, sy] = this.worldToScreen(d.x, d.y - (d.h ? d.h / 2 : d.r), lift);
      if (sx < -50 || sx > w + 50 || sy < -20 || sy > h + 20) continue;
      const name = model.typeName(d.type);
      ctx.fillStyle = "rgba(0,0,0,0.65)";
      const tw = ctx.measureText(name).width;
      ctx.fillRect(sx - tw / 2 - 2, sy + 1, tw + 4, 13);
      ctx.fillStyle = d.id === selected ? "#ffffff" : "#d8dde3";
      ctx.fillText(name, sx, sy + 2);
    }
  }

  private camera(): THREE.Camera {
    return this.mode === "3d" ? this.persp : this.ortho;
  }

  /** Screen position of a world point (lift: extra height above the ground in 3D). */
  worldToScreen(x: number, y: number, lift = 0): [number, number] {
    const { w, h } = this.size();
    if (this.mode === "2d") {
      return [(x - this.view.cx) * this.view.ppc + w / 2, (this.view.cy - y) * this.view.ppc + h / 2];
    }
    this.v.set(x, y, this.heightAt(x, y) + lift).project(this.persp);
    return [(this.v.x + 1) / 2 * w, (1 - this.v.y) / 2 * h];
  }

  /** World point under a screen position (on the ground in 3D). */
  screenToWorld(px: number, py: number): [number, number] {
    const { w, h } = this.size();
    if (this.mode === "2d") {
      return [(px - w / 2) / this.view.ppc + this.view.cx, this.view.cy - (py - h / 2) / this.view.ppc];
    }
    const ndc = new THREE.Vector2((px / w) * 2 - 1, -(py / h) * 2 + 1);
    this.ray.setFromCamera(ndc, this.persp);
    if (this.terrain3) {
      const hit = this.ray.intersectObject(this.terrain3, false)[0];
      if (hit) return [hit.point.x, hit.point.y];
    }
    // Off the map: intersect the plane at the view target's height.
    const plane = new THREE.Plane(new THREE.Vector3(0, 0, 1), -this.heightAt(this.view.cx, this.view.cy));
    const p = new THREE.Vector3();
    return this.ray.ray.intersectPlane(plane, p) ? [p.x, p.y] : [this.view.cx, this.view.cy];
  }

  /** Unit under a screen point in the last frame; small units win over big ones. */
  pick(px: number, py: number): number | undefined {
    const ppc = this.mode === "2d" ? this.view.ppc : this.pixelsPerCellAt(this.view.cx, this.view.cy);
    const slop = 4 / ppc; // a few pixels of tolerance
    let best: DrawnUnit | undefined;
    const rank = (u: DrawnUnit) => (u.air ? 0 : u.w ? 2 : 1) * 100 + u.r;
    for (let i = 0; i < this.drawnCount; i++) {
      const d = this.drawn[i];
      if (d.hidden) continue;
      // Compare in screen space so flying units (drawn higher in 3D) pick where they appear.
      const [sx, sy] = this.worldToScreen(d.x, d.y, this.mode === "3d" && d.air ? AIR_HEIGHT : 0);
      const dist = Math.hypot(sx - px, sy - py) / ppc;
      const reach = d.w ? Math.max(d.w, d.h) / 2 : d.r;
      if (dist > reach + slop) continue;
      if (!best || rank(d) < rank(best)) best = d;
    }
    return best?.id;
  }

  private pixelsPerCellAt(x: number, y: number): number {
    const [ax, ay] = this.worldToScreen(x, y);
    const [bx, by] = this.worldToScreen(x + 1, y);
    return Math.max(0.5, Math.hypot(bx - ax, by - ay));
  }

  /** The view's size in CSS pixels (the export size while exporting). */
  size(): { w: number; h: number } {
    return this.fixed ?? { w: this.container.clientWidth, h: this.container.clientHeight };
  }

  /** Renders at w×h (device pixels, 1:1) until endExport, for video frames. */
  beginExport(w: number, h: number): void {
    this.fixed = { w, h };
    this.renderer.setPixelRatio(1);
    this.renderer.setSize(w, h, false);
    this.labels.width = w;
    this.labels.height = h;
    this.updateCamera();
  }

  endExport(): void {
    this.fixed = undefined;
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    this.resize();
  }

  /** The canvases a video frame is made of: the 3D/2D view, then the labels. */
  layers(): HTMLCanvasElement[] {
    return [this.canvas, this.labels];
  }

  resize(): void {
    if (this.fixed) return;
    const { clientWidth: w, clientHeight: h } = this.container;
    if (w === 0 || h === 0) return;
    this.renderer.setSize(w, h, false);
    const dpr = Math.min(window.devicePixelRatio, 2);
    this.labels.width = w * dpr;
    this.labels.height = h * dpr;
    this.updateCamera();
  }


  updateCamera(): void {
    const { w, h } = this.size();
    // 2D: orthographic box around the center.
    const hw = w / 2 / this.view.ppc;
    const hh = h / 2 / this.view.ppc;
    this.ortho.left = -hw;
    this.ortho.right = hw;
    this.ortho.top = hh;
    this.ortho.bottom = -hh;
    this.ortho.position.set(this.view.cx, this.view.cy, 500);
    this.ortho.updateProjectionMatrix();
    // 3D: orbit the same center; the distance shows about as much ground as 2D.
    const target = new THREE.Vector3(this.view.cx, this.view.cy, this.heightAt(this.view.cx, this.view.cy));
    const dist = (h / this.view.ppc) / 2 / Math.tan(THREE.MathUtils.degToRad(this.persp.fov / 2));
    const p = this.view.pitch; // angle from vertical
    const off = new THREE.Vector3(Math.sin(this.view.yaw) * Math.sin(p), -Math.cos(this.view.yaw) * Math.sin(p), Math.cos(p));
    this.persp.position.copy(target).addScaledVector(off, dist);
    this.persp.aspect = w / h;
    this.persp.far = dist * 4 + 500;
    this.persp.lookAt(target);
    this.persp.updateProjectionMatrix();
    this.onViewChange?.();
  }

  private attachControls(): void {
    const el = this.labels; // top layer receives input
    let drag: { x: number; y: number; cx: number; cy: number; yaw: number; pitch: number; rotate: boolean; moved: boolean } | undefined;
    el.addEventListener("pointerdown", (e) => {
      el.setPointerCapture(e.pointerId);
      drag = {
        x: e.clientX, y: e.clientY, cx: this.view.cx, cy: this.view.cy, yaw: this.view.yaw, pitch: this.view.pitch,
        rotate: this.mode === "3d" && (e.button === 2 || e.shiftKey), moved: false,
      };
    });
    el.addEventListener("pointermove", (e) => {
      if (!drag) return;
      const dx = e.clientX - drag.x;
      const dy = e.clientY - drag.y;
      if (Math.abs(dx) + Math.abs(dy) > 4 && !drag.moved) {
        drag.moved = true;
        this.onUserMove?.();
      }
      if (!drag.moved) return;
      if (drag.rotate) {
        this.view.yaw = drag.yaw - dx * 0.006;
        this.view.pitch = Math.min(1.35, Math.max(0.05, drag.pitch - dy * 0.005));
      } else {
        // Drag the ground: right on screen is (cos yaw, sin yaw) on the map,
        // up on screen is forward, (-sin yaw, cos yaw). In 2D the yaw is 0.
        const k = 1 / (this.mode === "3d" ? this.pixelsPerCellAt(drag.cx, drag.cy) : this.view.ppc);
        const yaw = this.mode === "3d" ? this.view.yaw : 0;
        const c = Math.cos(yaw), sn = Math.sin(yaw);
        this.view.cx = drag.cx - dx * k * c - dy * k * sn;
        this.view.cy = drag.cy - dx * k * sn + dy * k * c;
      }
      this.updateCamera();
    });
    el.addEventListener("pointerup", (e) => {
      if (drag && !drag.moved && e.button === 0) {
        const r = el.getBoundingClientRect();
        this.onClick?.(e.clientX - r.left, e.clientY - r.top);
      }
      drag = undefined;
    });
    el.addEventListener("contextmenu", (e) => e.preventDefault());
    el.addEventListener(
      "wheel",
      (e) => {
        e.preventDefault();
        this.onUserMove?.();
        const r = el.getBoundingClientRect();
        const px = e.clientX - r.left;
        const py = e.clientY - r.top;
        const [wx, wy] = this.screenToWorld(px, py);
        this.view.ppc = Math.min(64, Math.max(0.5, this.view.ppc * Math.exp(-e.deltaY * 0.0015)));
        this.updateCamera();
        // Keep the point under the cursor fixed.
        const [nx, ny] = this.screenToWorld(px, py);
        this.view.cx += wx - nx;
        this.view.cy += wy - ny;
        this.updateCamera();
      },
      { passive: false },
    );
  }
}

/** The instanced meshes for one mode. */
class Meshes {
  constructor(
    readonly footprints: THREE.InstancedMesh,
    readonly ground: THREE.InstancedMesh,
    readonly air: THREE.InstancedMesh,
    readonly deaths: THREE.InstancedMesh,
  ) {}

  private static make(scene: THREE.Scene, geo: THREE.BufferGeometry, n: number, order: number, lit: boolean, opacity = 1, depth = true): THREE.InstancedMesh {
    const opts = { color: 0xffffff, transparent: opacity < 1, opacity, depthTest: depth };
    const mat = lit ? new THREE.MeshLambertMaterial(opts) : new THREE.MeshBasicMaterial(opts);
    const mesh = new THREE.InstancedMesh(geo, mat, n);
    mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    mesh.setColorAt(0, new THREE.Color());
    mesh.count = 0;
    mesh.renderOrder = order;
    mesh.frustumCulled = false;
    scene.add(mesh);
    return mesh;
  }

  /** 2D: flat shapes drawn in order, no depth. */
  static flat(n: number, scene: THREE.Scene): Meshes {
    return new Meshes(
      Meshes.make(scene, new THREE.PlaneGeometry(1, 1), n, 2, false, 1, false),
      Meshes.make(scene, new THREE.CircleGeometry(0.5, 18), n, 3, false, 1, false),
      Meshes.make(scene, new THREE.CircleGeometry(0.5, 18), n, 5, false, 0.82, false),
      Meshes.make(scene, new THREE.RingGeometry(0.35, 0.5, 16), n, 4, false, 0.9, false),
    );
  }

  /** 3D: lit solids, unit-sized (scaled per instance), standing on z = 0. */
  static solid(n: number, scene: THREE.Scene): Meshes {
    const cyl = new THREE.CylinderGeometry(0.5, 0.5, 1, 16).rotateX(Math.PI / 2);
    const disc = new THREE.CylinderGeometry(0.5, 0.35, 1, 16).rotateX(Math.PI / 2);
    return new Meshes(
      Meshes.make(scene, new THREE.BoxGeometry(1, 1, 1), n, 2, true),
      Meshes.make(scene, cyl, n, 3, true),
      Meshes.make(scene, disc, n, 5, true, 0.9),
      Meshes.make(scene, new THREE.RingGeometry(0.35, 0.5, 16), n, 4, false, 0.9),
    );
  }

  setVisible(v: boolean): void {
    for (const m of [this.footprints, this.ground, this.air, this.deaths]) m.visible = v;
  }

  commit(nf: number, ng: number, na: number, nd: number): void {
    for (const [mesh, n] of [[this.footprints, nf], [this.ground, ng], [this.air, na], [this.deaths, nd]] as const) {
      mesh.count = n;
      mesh.instanceMatrix.needsUpdate = true;
      if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
    }
  }

  dispose(scene: THREE.Scene): void {
    for (const m of [this.footprints, this.ground, this.air, this.deaths]) {
      scene.remove(m);
      m.dispose();
    }
  }
}

/** Mean luminance (0..1, linear) of an image, from a small downscaled copy. */
function averageLuminance(img: CanvasImageSource & { width: number; height: number }): number {
  const c = document.createElement("canvas");
  c.width = c.height = 16;
  const ctx = c.getContext("2d");
  if (!ctx || !img.width) return 0.5;
  ctx.drawImage(img, 0, 0, 16, 16);
  const d = ctx.getImageData(0, 0, 16, 16).data;
  let sum = 0;
  for (let i = 0; i < d.length; i += 4) {
    const lin = (v: number) => Math.pow(v / 255, 2.2);
    sum += 0.299 * lin(d[i]) + 0.587 * lin(d[i + 1]) + 0.114 * lin(d[i + 2]);
  }
  return sum / 256;
}
