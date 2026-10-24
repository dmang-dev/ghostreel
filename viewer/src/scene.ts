// SPDX-License-Identifier: Apache-2.0
//
// The game view in three.js, in two modes:
//
//   2D: top-down, orthographic camera, units as flat circles and footprints.
//   3D: perspective camera over a heightfield built from the map's terrain
//       heights, units as stand-in solids (cylinders, footprint boxes; air
//       units float), until real models come from the asset extractor.
//
// World units are map cells, x right, y up, z up (heights), so timeline
// coordinates go straight into the scene. Units are instanced meshes: a
// handful of draw calls per frame whatever the unit count.
//
// Fog of war: with fogPlayer set, each frame computes that player's vision
// from its units' sight radii, darkens what it can't see and hides enemy
// units there, like the in-game player view.

import * as THREE from "three";
import { Model, UnitClass, isAlive, positionAt, type UnitState } from "./model";

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
  private terrainTex?: THREE.CanvasTexture;
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
  /** A click (not a drag) at a point in the canvas, in CSS pixels. */
  onClick?: (px: number, py: number) => void;

  private drawn: DrawnUnit[] = [];
  private drawnCount = 0;
  private readonly tmp: UnitState = { id: 0, type: 0, owner: 0, x: 0, y: 0, facing: 0, constructing: false };
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

  setWorld(model: Model, width: number, height: number, terrain: HTMLCanvasElement, heights?: ArrayLike<number> | null): void {
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
    }));

    this.fogData = new Uint8Array(width * height * 4);
    this.fogTex?.dispose();
    this.fogTex = new THREE.DataTexture(this.fogData, width, height, THREE.RGBAFormat);
    this.fogTex.magFilter = this.fogTex.minFilter = THREE.LinearFilter;
    this.fogTex.needsUpdate = true;
    this.setTerrain(terrain);
    this.applyMode();
  }

  setTerrain(canvas: HTMLCanvasElement): void {
    for (const m of [this.terrain2, this.terrain3, this.fogMesh2, this.fogMesh3]) {
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
    this.scene.add(this.terrain2, this.terrain3, this.fogMesh2, this.fogMesh3);
    this.applyMode();
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
    this.meshes2?.setVisible(!three);
    this.meshes3?.setVisible(three);
    this.selection2.visible = this.selection3.visible = false;
    this.updateFogVisibility();
  }

  private updateFogVisibility(): void {
    const on = this.fogPlayer !== 0;
    if (this.fogMesh2) this.fogMesh2.visible = on && this.mode === "2d";
    if (this.fogMesh3) this.fogMesh3.visible = on && this.mode === "3d";
  }

  setFogPlayer(p: number): void {
    this.fogPlayer = p;
    this.updateFogVisibility();
  }

  /** Fits a rectangle (in cells) to the viewport. */
  fit(left: number, bottom: number, right: number, top: number): void {
    const { clientWidth: w, clientHeight: h } = this.container;
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
      if (t.air || !t.footprintW) {
        dr.r = Math.max(t.radius, 0.25);
        dr.w = dr.h = 0;
      } else {
        dr.w = t.footprintW; dr.h = t.footprintH; dr.r = Math.max(t.footprintW, t.footprintH) / 2;
      }
    }
    this.drawnCount = nDrawn;

    // Pass 2: fog of war from the chosen player's units.
    const visible = this.fogPlayer ? this.computeFog(model) : undefined;

    // Pass 3: instances.
    let nf = 0, ng = 0, na = 0, nd = 0;
    let selDrawn: DrawnUnit | undefined;
    const three = this.mode === "3d";
    for (let i = 0; i < nDrawn; i++) {
      const dr = this.drawn[i];
      const t = model.types[dr.type];
      if (visible && dr.owner !== this.fogPlayer && dr.owner !== 0 && !visible(dr.x, dr.y)) {
        dr.hidden = true;
        continue;
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
      if (dr.id === selected) selDrawn = dr;
    }
    // Death markers.
    for (const u of model.units) {
      if (u.diedLoop === undefined || loop < u.diedLoop || loop - u.diedLoop >= DEATH_WINDOW || u.owner === 0) continue;
      const t = model.types[u.type];
      if (!t || t.class === UnitClass.HIDDEN || t.footprintW) continue;
      positionAt(model.tracks[u.id], 0, u.diedLoop, s);
      if (visible && u.owner !== this.fogPlayer && !visible(s.x, s.y)) continue;
      const fade = 1 - (loop - u.diedLoop) / DEATH_WINDOW;
      const d = 1 + 1.6 * (1 - fade);
      this.place(ms.deaths, nd, s.x, s.y, three ? this.heightAt(s.x, s.y) + 0.15 : 0, d, d, 1);
      this.c.setRGB(0.95 * fade + 0.05, 0.25 * fade, 0.2 * fade);
      ms.deaths.setColorAt(nd++, this.c);
    }
    ms.commit(nf, ng, na, nd);

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
    const dpr = this.labels.width / Math.max(this.labels.clientWidth, 1);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, this.labels.clientWidth, this.labels.clientHeight);
    const model = this.model;
    if (!model) return;
    const show = this.showLabels && this.view.ppc >= 9;
    ctx.font = "11px system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const w = this.labels.clientWidth;
    const h = this.labels.clientHeight;
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
    const { clientWidth: w, clientHeight: h } = this.container;
    if (this.mode === "2d") {
      return [(x - this.view.cx) * this.view.ppc + w / 2, (this.view.cy - y) * this.view.ppc + h / 2];
    }
    this.v.set(x, y, this.heightAt(x, y) + lift).project(this.persp);
    return [(this.v.x + 1) / 2 * w, (1 - this.v.y) / 2 * h];
  }

  /** World point under a screen position (on the ground in 3D). */
  screenToWorld(px: number, py: number): [number, number] {
    const { clientWidth: w, clientHeight: h } = this.container;
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

  private resize(): void {
    const { clientWidth: w, clientHeight: h } = this.container;
    if (w === 0 || h === 0) return;
    this.renderer.setSize(w, h, false);
    const dpr = Math.min(window.devicePixelRatio, 2);
    this.labels.width = w * dpr;
    this.labels.height = h * dpr;
    this.updateCamera();
  }

  updateCamera(): void {
    const { clientWidth: w, clientHeight: h } = this.container;
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
      if (Math.abs(dx) + Math.abs(dy) > 4) drag.moved = true;
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
