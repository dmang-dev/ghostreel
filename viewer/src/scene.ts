// SPDX-License-Identifier: Apache-2.0
//
// Top-down 2D view in three.js with an orthographic camera. World units are
// map cells, x right, y up (the game's convention), so timeline coordinates
// go straight into the scene. Units are drawn with instanced meshes, a
// handful of draw calls per frame whatever the unit count.

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
}

const NEUTRAL = {
  mineral: new THREE.Color("#b5ecff"),
  gas: new THREE.Color("#42d392"),
  rock: new THREE.Color("#8d8d84"),
  other: new THREE.Color("#b0b0b0"),
};
const DEATH_WINDOW = 112; // loops a death marker stays visible (5 s on the clock)

export class Scene {
  readonly canvas: HTMLCanvasElement;
  private readonly labels: HTMLCanvasElement;
  private readonly renderer: THREE.WebGLRenderer;
  private readonly scene = new THREE.Scene();
  private readonly camera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0.1, 100);
  private terrain?: THREE.Mesh;
  private terrainTex?: THREE.CanvasTexture;

  private model?: Model;
  private worldW = 1;
  private worldH = 1;
  private playerColors = new Map<number, THREE.Color>();

  private footprints?: THREE.InstancedMesh;
  private ground?: THREE.InstancedMesh;
  private air?: THREE.InstancedMesh;
  private deaths?: THREE.InstancedMesh;
  private readonly selection: THREE.Mesh;

  /** Camera: center in cells and zoom in pixels per cell. */
  view = { cx: 0, cy: 0, ppc: 4 };
  drawn: DrawnUnit[] = [];
  private drawnCount = 0;
  showLabels = true;
  onViewChange?: () => void;
  /** A click (not a drag) at a point in the canvas, in CSS pixels. */
  onClick?: (px: number, py: number) => void;

  private readonly tmp: UnitState = { id: 0, type: 0, owner: 0, x: 0, y: 0, facing: 0, constructing: false };
  private readonly m4 = new THREE.Matrix4();
  private readonly q = new THREE.Quaternion();
  private readonly v = new THREE.Vector3();
  private readonly s = new THREE.Vector3();
  private readonly c = new THREE.Color();

  constructor(private readonly container: HTMLElement) {
    this.renderer = new THREE.WebGLRenderer({ antialias: true });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    this.renderer.setClearColor(0x0b0d10);
    this.canvas = this.renderer.domElement;
    this.canvas.className = "gl";
    this.labels = document.createElement("canvas");
    this.labels.className = "labels";
    container.append(this.canvas, this.labels);

    const ringMat = new THREE.MeshBasicMaterial({ color: 0xffffff, depthTest: false, transparent: true });
    this.selection = new THREE.Mesh(new THREE.RingGeometry(0.62, 0.78, 40), ringMat);
    this.selection.renderOrder = 10;
    this.selection.visible = false;
    this.scene.add(this.selection);

    new ResizeObserver(() => this.resize()).observe(container);
    this.attachControls();
    this.resize();
  }

  setWorld(model: Model, width: number, height: number, terrain: HTMLCanvasElement): void {
    this.model = model;
    this.worldW = width;
    this.worldH = height;
    this.playerColors.clear();
    for (const p of model.tl.meta?.players ?? []) this.playerColors.set(p.id, new THREE.Color(p.color));

    for (const m of [this.footprints, this.ground, this.air, this.deaths]) {
      if (m) {
        this.scene.remove(m);
        m.dispose();
      }
    }
    const n = Math.max(model.units.length, 1);
    const mk = (geo: THREE.BufferGeometry, order: number, opacity = 1) => {
      const mat = new THREE.MeshBasicMaterial({ color: 0xffffff, depthTest: false, transparent: opacity < 1, opacity });
      const mesh = new THREE.InstancedMesh(geo, mat, n);
      mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
      mesh.setColorAt(0, new THREE.Color());
      mesh.count = 0;
      mesh.renderOrder = order;
      mesh.frustumCulled = false;
      this.scene.add(mesh);
      return mesh;
    };
    this.footprints = mk(new THREE.PlaneGeometry(1, 1), 2);
    this.ground = mk(new THREE.CircleGeometry(0.5, 18), 3);
    this.air = mk(new THREE.CircleGeometry(0.5, 18), 5, 0.82);
    this.deaths = mk(new THREE.RingGeometry(0.35, 0.5, 16), 4, 0.9);
    this.drawn = Array.from({ length: n }, () => ({ id: 0, x: 0, y: 0, r: 0, w: 0, h: 0, owner: 0, type: 0, air: false, constructing: false }));
    this.setTerrain(terrain);
  }

  setTerrain(canvas: HTMLCanvasElement): void {
    if (this.terrain) {
      this.scene.remove(this.terrain);
      this.terrain.geometry.dispose();
      (this.terrain.material as THREE.Material).dispose();
      this.terrainTex?.dispose();
    }
    this.terrainTex = new THREE.CanvasTexture(canvas);
    this.terrainTex.colorSpace = THREE.SRGBColorSpace;
    this.terrainTex.anisotropy = 4;
    const mat = new THREE.MeshBasicMaterial({ map: this.terrainTex, depthTest: false });
    this.terrain = new THREE.Mesh(new THREE.PlaneGeometry(this.worldW, this.worldH), mat);
    this.terrain.position.set(this.worldW / 2, this.worldH / 2, 0);
    this.terrain.renderOrder = 0;
    this.scene.add(this.terrain);
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
    if (!model || !this.footprints || !this.ground || !this.air || !this.deaths) {
      this.renderer.render(this.scene, this.camera);
      return;
    }
    let nf = 0, ng = 0, na = 0, nd = 0, nDrawn = 0;
    const s = this.tmp;
    let selDrawn: DrawnUnit | undefined;
    for (const u of model.units) {
      if (!isAlive(u, loop)) {
        if (u.diedLoop !== undefined && loop >= u.diedLoop && loop - u.diedLoop < DEATH_WINDOW && u.owner !== 0) {
          const t = model.types[u.type];
          if (t && t.class !== UnitClass.HIDDEN && !t.footprintW) {
            positionAt(model.tracks[u.id], 0, u.diedLoop, s);
            const fade = 1 - (loop - u.diedLoop) / DEATH_WINDOW;
            const d = 1 + 1.6 * (1 - fade);
            this.place(this.deaths, nd, s.x, s.y, d, d);
            this.c.setRGB(0.95 * fade + 0.05, 0.25 * fade, 0.2 * fade);
            this.deaths.setColorAt(nd++, this.c);
          }
        }
        continue;
      }
      model.evalUnit(u, loop, s);
      const t = model.types[s.type];
      if (!t || t.class === UnitClass.HIDDEN) continue;

      const neutral = s.owner === 0;
      this.c.copy(neutral ? this.neutralColor(t.name) : this.colorOf(s.owner));
      if (s.constructing) this.c.multiplyScalar(0.45);

      const dr = this.drawn[nDrawn++];
      dr.id = s.id; dr.x = s.x; dr.y = s.y; dr.owner = s.owner; dr.type = s.type;
      dr.air = t.air; dr.constructing = s.constructing;
      if (t.air || !t.footprintW) {
        const r = Math.max(t.radius, 0.25);
        dr.r = r; dr.w = dr.h = 0;
        const mesh = t.air ? this.air : this.ground;
        const i = t.air ? na++ : ng++;
        this.place(mesh, i, s.x, s.y, r * 2, r * 2);
        mesh.setColorAt(i, this.c);
      } else {
        // Structures: footprint rectangle, slightly inset so neighbors read apart.
        dr.w = t.footprintW; dr.h = t.footprintH; dr.r = Math.max(t.footprintW, t.footprintH) / 2;
        this.place(this.footprints, nf, s.x, s.y, t.footprintW - 0.15, t.footprintH - 0.15);
        this.footprints.setColorAt(nf++, this.c);
      }
      if (s.id === selected) selDrawn = dr;
    }
    this.drawnCount = nDrawn;
    for (const [mesh, n] of [[this.footprints, nf], [this.ground, ng], [this.air, na], [this.deaths, nd]] as const) {
      mesh.count = n;
      mesh.instanceMatrix.needsUpdate = true;
      if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
    }
    if (selDrawn) {
      const d = Math.max(selDrawn.r * 2, 1);
      this.selection.position.set(selDrawn.x, selDrawn.y, 0);
      this.selection.scale.set(d, d, 1);
      this.selection.visible = true;
    } else {
      this.selection.visible = false;
    }
    this.renderer.render(this.scene, this.camera);
    this.drawLabels(selected);
  }

  /** Units drawn in the last frame. */
  drawnUnits(): DrawnUnit[] {
    return this.drawn.slice(0, this.drawnCount);
  }

  private place(mesh: THREE.InstancedMesh, i: number, x: number, y: number, sx: number, sy: number): void {
    this.m4.compose(this.v.set(x, y, 0), this.q, this.s.set(sx, sy, 1));
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
      if (!show && d.id !== selected) continue;
      if (d.owner === 0 && d.id !== selected) continue; // resources are recognizable by color
      const [sx, sy] = this.worldToScreen(d.x, d.y - (d.h ? d.h / 2 : d.r));
      if (sx < -50 || sx > w + 50 || sy < -20 || sy > h + 20) continue;
      const name = model.typeName(d.type);
      ctx.fillStyle = "rgba(0,0,0,0.65)";
      const tw = ctx.measureText(name).width;
      ctx.fillRect(sx - tw / 2 - 2, sy + 1, tw + 4, 13);
      ctx.fillStyle = d.id === selected ? "#ffffff" : "#d8dde3";
      ctx.fillText(name, sx, sy + 2);
    }
  }

  worldToScreen(x: number, y: number): [number, number] {
    const { clientWidth: w, clientHeight: h } = this.container;
    return [(x - this.view.cx) * this.view.ppc + w / 2, (this.view.cy - y) * this.view.ppc + h / 2];
  }

  screenToWorld(px: number, py: number): [number, number] {
    const { clientWidth: w, clientHeight: h } = this.container;
    return [(px - w / 2) / this.view.ppc + this.view.cx, this.view.cy - (py - h / 2) / this.view.ppc];
  }

  /** Unit under a screen point in the last frame; small units win over big ones. */
  pick(px: number, py: number): number | undefined {
    const [x, y] = this.screenToWorld(px, py);
    const slop = 4 / this.view.ppc; // a few pixels of tolerance
    let best: DrawnUnit | undefined;
    for (let i = 0; i < this.drawnCount; i++) {
      const d = this.drawn[i];
      const hit = d.w
        ? Math.abs(x - d.x) <= d.w / 2 + slop && Math.abs(y - d.y) <= d.h / 2 + slop
        : Math.hypot(x - d.x, y - d.y) <= d.r + slop;
      if (!hit) continue;
      // Prefer air over ground over structures, then the smaller one.
      const rank = (u: DrawnUnit) => (u.air ? 0 : u.w ? 2 : 1) * 100 + u.r;
      if (!best || rank(d) < rank(best)) best = d;
    }
    return best?.id;
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
    const hw = w / 2 / this.view.ppc;
    const hh = h / 2 / this.view.ppc;
    this.camera.left = -hw;
    this.camera.right = hw;
    this.camera.top = hh;
    this.camera.bottom = -hh;
    this.camera.position.set(this.view.cx, this.view.cy, 10);
    this.camera.updateProjectionMatrix();
    this.onViewChange?.();
  }

  private attachControls(): void {
    const el = this.labels; // top layer receives input
    let drag: { x: number; y: number; cx: number; cy: number; moved: boolean } | undefined;
    el.addEventListener("pointerdown", (e) => {
      el.setPointerCapture(e.pointerId);
      drag = { x: e.clientX, y: e.clientY, cx: this.view.cx, cy: this.view.cy, moved: false };
    });
    el.addEventListener("pointermove", (e) => {
      if (!drag) return;
      const dx = e.clientX - drag.x;
      const dy = e.clientY - drag.y;
      if (Math.abs(dx) + Math.abs(dy) > 4) drag.moved = true;
      if (drag.moved) {
        this.view.cx = drag.cx - dx / this.view.ppc;
        this.view.cy = drag.cy + dy / this.view.ppc;
        this.updateCamera();
      }
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
        const ppc = Math.min(64, Math.max(0.5, this.view.ppc * Math.exp(-e.deltaY * 0.0015)));
        this.view.ppc = ppc;
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
