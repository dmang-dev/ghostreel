// SPDX-License-Identifier: Apache-2.0
//
// The ground in the game's own look: a finer, smoothed heightfield from the
// map's corner heights, and the map's terrain texture layers blended on the
// GPU by its painted masks (texture splatting), so the ground is as sharp
// as the textures themselves however close the camera gets. Steep faces
// (cliffs, ramp sides) take the textures projected from the side, so they
// don't smear into streaks.

import * as THREE from "three";

/** Heightfield vertices per cell along each axis. */
export const SUB = 2;

/**
 * Corner heights ((w+1)*(h+1), row 0 at y = 0) resampled at SUB vertices
 * per cell and smoothed, so cliffs and ramps become slopes with rounded
 * edges instead of steps along the cell grid. Returns (w*SUB+1)*(h*SUB+1)
 * heights, row 0 at y = 0.
 */
export function fineHeights(corners: ArrayLike<number>, w: number, h: number): Float32Array {
  const fw = w * SUB + 1;
  const fh = h * SUB + 1;
  const a = new Float32Array(fw * fh);
  const at = (x: number, y: number) => corners[y * (w + 1) + x];
  for (let j = 0; j < fh; j++) {
    const y = j / SUB;
    const y0 = Math.min(Math.floor(y), h - 1);
    const fy = y - y0;
    for (let i = 0; i < fw; i++) {
      const x = i / SUB;
      const x0 = Math.min(Math.floor(x), w - 1);
      const fx = x - x0;
      a[j * fw + i] = (at(x0, y0) * (1 - fx) + at(x0 + 1, y0) * fx) * (1 - fy) + (at(x0, y0 + 1) * (1 - fx) + at(x0 + 1, y0 + 1) * fx) * fy;
    }
  }
  // A [1 2 1] blur each way: rounds edges by about half a cell and keeps
  // cliffs steep.
  const b = new Float32Array(fw * fh);
  for (let j = 0; j < fh; j++) {
    const r = j * fw;
    for (let i = 0; i < fw; i++) b[r + i] = (a[r + Math.max(i - 1, 0)] + 2 * a[r + i] + a[r + Math.min(i + 1, fw - 1)]) / 4;
  }
  for (let j = 0; j < fh; j++) {
    const up = Math.min(j + 1, fh - 1) * fw;
    const down = Math.max(j - 1, 0) * fw;
    for (let i = 0; i < fw; i++) a[j * fw + i] = (b[down + i] + 2 * b[j * fw + i] + b[up + i]) / 4;
  }
  return a;
}

/**
 * Makes the ground step rather than slope in cliff cells (2×2 cells, by
 * index) drawn with the game's cliff pieces: each vertex takes its nearest
 * corner's height, the lowest of the nearest on ties, so the ground stays
 * behind the walls, which stand mid-cell. (Ties to the highest would close
 * the groove left behind a wall's top, but raise the ground in front of
 * the wall instead, which hides it.) fine is fineHeights' output; mask, if
 * given, is set to 1 at the vertices changed.
 */
export function stepCliffs(
  fine: Float32Array, corners: ArrayLike<number>, w: number, h: number,
  cells: Iterable<readonly [number, number]>, mask?: Float32Array,
): void {
  const fw = w * SUB + 1;
  const n = 2 * SUB; // fine steps across a cliff cell
  const at = (x: number, y: number) => corners[y * (w + 1) + x];
  for (const [ci, cj] of cells) {
    if (ci < 0 || cj < 0 || 2 * ci + 2 > w || 2 * cj + 2 > h) continue;
    const x0 = 2 * ci;
    const y0 = 2 * cj;
    const c = [[0, 0, at(x0, y0)], [1, 0, at(x0 + 2, y0)], [1, 1, at(x0 + 2, y0 + 2)], [0, 1, at(x0, y0 + 2)]];
    for (let j = 0; j <= n; j++) {
      for (let i = 0; i <= n; i++) {
        let best = Infinity;
        let height = 0;
        for (const [cx, cy, ch] of c) {
          const d = Math.hypot(i / n - cx, j / n - cy);
          if (d < best - 1e-9 || (Math.abs(d - best) <= 1e-9 && ch < height)) {
            best = Math.min(best, d);
            height = ch;
          }
        }
        fine[(cj * n + j) * fw + ci * n + i] = height;
        if (mask) mask[(cj * n + j) * fw + ci * n + i] = 1;
      }
    }
  }
}

/** The map's texture layers and the masks that blend them. */
export interface GroundLayers {
  /** Layers 0-3 and 4-7 as RGBA weights, row 0 at y = 0. */
  masks: [THREE.DataTexture, THREE.DataTexture];
  /** One texture per layer; undefined where the game data lacks it. */
  textures: (THREE.Texture | undefined)[];
  /** Repeats across the map (x, y), then offsets in cells. */
  tiling: number[];
}

/** Mask textures from the API's planes (see /api/r/{id}/masks). */
export function maskTextures(planes: Uint8Array, w: number, h: number): [THREE.DataTexture, THREE.DataTexture] {
  const per = w * h * 4;
  const make = (i: number) => {
    const t = new THREE.DataTexture(planes.subarray(i * per, (i + 1) * per), w, h, THREE.RGBAFormat);
    t.magFilter = t.minFilter = THREE.LinearFilter;
    t.needsUpdate = true;
    return t;
  };
  return [make(0), make(1)];
}

export function disposeLayers(l: GroundLayers): void {
  for (const t of [...l.masks, ...l.textures]) t?.dispose();
}

const blank = new THREE.DataTexture(new Uint8Array([0, 0, 0, 255]), 1, 1);
blank.needsUpdate = true;

/**
 * Fine grain laid over the textures (value noise in two octaves, tiling,
 * mean 0.5), so low-detail textures (installs that ship only the small
 * copies) don't read as blur up close.
 */
export const grain: THREE.DataTexture = (() => {
  const n = 128;
  const rnd = (seed: number) => {
    const v = new Float32Array(seed * seed);
    let x = 0x9e3779b9 ^ seed;
    for (let i = 0; i < v.length; i++) {
      x ^= x << 13; x ^= x >>> 17; x ^= x << 5;
      v[i] = ((x >>> 0) % 1000) / 1000;
    }
    return v;
  };
  const octave = (cells: number) => {
    const g = rnd(cells);
    const at = (i: number, j: number) => g[(j % cells) * cells + (i % cells)];
    const smooth = (t: number) => t * t * (3 - 2 * t);
    return (x: number, y: number) => {
      const fx = (x / n) * cells, fy = (y / n) * cells;
      const i = Math.floor(fx), j = Math.floor(fy);
      const u = smooth(fx - i), v = smooth(fy - j);
      return (at(i, j) * (1 - u) + at(i + 1, j) * u) * (1 - v) + (at(i, j + 1) * (1 - u) + at(i + 1, j + 1) * u) * v;
    };
  };
  const a = octave(16), b = octave(64);
  const data = new Uint8Array(n * n * 4);
  for (let y = 0; y < n; y++)
    for (let x = 0; x < n; x++) {
      const v = Math.round((0.45 * a(x, y) + 0.55 * b(x, y)) * 255);
      const o = (y * n + x) * 4;
      data[o] = data[o + 1] = data[o + 2] = v;
      data[o + 3] = 255;
    }
  const t = new THREE.DataTexture(data, n, n, THREE.RGBAFormat);
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  t.magFilter = THREE.LinearFilter;
  t.minFilter = THREE.LinearMipmapLinearFilter;
  t.generateMipmaps = true;
  t.needsUpdate = true;
  return t;
})();
const GRAIN_CELLS = 3; // cells per repeat of the grain
const GRAIN = 0.22; // grain strength: brightness varies by about this much
// Height blending: how much a layer's surface height counts against its
// painted weight (both 0-1), and how close to the top a layer must be to
// show at all (smaller is crisper).
const HEIGHT_WEIGHT = 0.5;
const BLEND_DEPTH = 0.3;
const MASK_WOBBLE = 1.6; // how far mask reads wander, in mask texels (peak to peak)
const MASK_WOBBLE_CELLS = 6; // cells per repeat of the wobble's noise

/**
 * How much grain the layers need: all of it for the 64-pixel copies, none
 * for textures 256 pixels or more across (a full install's), which have
 * their own detail.
 */
export function grainFor(textures: (THREE.Texture | undefined)[]): number {
  let size = Infinity;
  for (const t of textures) {
    const w = (t?.image as { width?: number } | undefined)?.width;
    if (w) size = Math.min(size, w);
  }
  return size === Infinity ? 0 : Math.min(1, Math.max(0, (256 - size) / 192));
}

/**
 * Patches a terrain material's shaders (in onBeforeCompile) to paint the
 * blended layers in place of its map. The material needs a map, for the
 * map's UVs (0-1 across the world). sides: project textures sideways on
 * steep faces (the 3D heightfield).
 */
export function splat(shader: THREE.WebGLProgramParametersWithUniforms, l: GroundLayers, worldW: number, worldH: number, sides: boolean): void {
  const have = l.textures.map((t) => (t ? 1 : 0));
  while (have.length < 8) have.push(0);
  shader.uniforms.gMask0 = { value: l.masks[0] };
  shader.uniforms.gMask1 = { value: l.masks[1] };
  shader.uniforms.gMaskSize = { value: new THREE.Vector2(l.masks[0].image.width, l.masks[0].image.height) };
  shader.uniforms.gLayer = { value: Array.from({ length: 8 }, (_, i) => l.textures[i] ?? blank) };
  shader.uniforms.gHave0 = { value: new THREE.Vector4(...have.slice(0, 4)) };
  shader.uniforms.gHave1 = { value: new THREE.Vector4(...have.slice(4, 8)) };
  shader.uniforms.gTiling = { value: new THREE.Vector4(...[0, 1, 2, 3].map((i) => l.tiling[i] ?? 0)) };
  shader.uniforms.gWorld = { value: new THREE.Vector2(worldW, worldH) };
  shader.uniforms.gGrain = { value: grain };
  shader.uniforms.gGrainAmount = { value: GRAIN * grainFor(l.textures) };
  if (sides) {
    shader.vertexShader = shader.vertexShader
      .replace("#include <common>", "#include <common>\nvarying vec3 vGPos;\nvarying vec3 vGNormal;\nattribute float cliffMask;\nvarying float vGCliff;")
      .replace("#include <begin_vertex>", "#include <begin_vertex>\nvGPos = position;\nvGNormal = normal;\nvGCliff = cliffMask;");
  }
  shader.fragmentShader = shader.fragmentShader
    .replace(
      "#include <common>",
      `#include <common>
uniform sampler2D gMask0;
uniform sampler2D gMask1;
uniform vec2 gMaskSize;
uniform sampler2D gLayer[8];
uniform vec4 gHave0;
uniform vec4 gHave1;
uniform vec4 gTiling;
uniform vec2 gWorld;
uniform sampler2D gGrain;
uniform float gGrainAmount;
// A mask at uv through a cubic B-spline, from four bilinear reads: edges
// between layers come out curved instead of stepping along the texel grid.
vec4 gCubic( sampler2D t, vec2 uv ) {
  vec2 st = uv * gMaskSize - 0.5;
  vec2 i = floor( st );
  vec2 f = st - i;
  vec2 f2 = f * f, f3 = f2 * f;
  vec2 w0 = ( -f3 + 3.0 * f2 - 3.0 * f + 1.0 ) / 6.0;
  vec2 w1 = ( 3.0 * f3 - 6.0 * f2 + 4.0 ) / 6.0;
  vec2 w2 = ( -3.0 * f3 + 3.0 * f2 + 3.0 * f + 1.0 ) / 6.0;
  vec2 w3 = f3 / 6.0;
  vec2 g0 = w0 + w1, g1 = w2 + w3;
  vec2 p0 = ( i - 0.5 + w1 / g0 ) / gMaskSize;
  vec2 p1 = ( i + 1.5 + w3 / g1 ) / gMaskSize;
  return g0.y * ( g0.x * texture2D( t, vec2( p0.x, p0.y ) ) + g1.x * texture2D( t, vec2( p1.x, p0.y ) ) )
    + g1.y * ( g0.x * texture2D( t, vec2( p0.x, p1.y ) ) + g1.x * texture2D( t, vec2( p1.x, p1.y ) ) );
}
${sides ? "#define G_SIDES\nvarying vec3 vGPos;\nvarying vec3 vGNormal;\nvarying float vGCliff;" : ""}
// The blend at uv, by height: each layer's texture carries its surface
// height in alpha (128-255), and where layers overlap the one standing
// highest for its weight wins, within a small depth. So edges follow the
// textures (grass tufts over paving) instead of fading. Most points have
// one to three layers, so the others are skipped; the UV derivatives come
// from the caller, outside any branch, so mipmapping stays right.
vec3 gSplat( vec2 uv, vec2 dx, vec2 dy, vec4 w0, vec4 w1 ) {
  float w[8] = float[8]( w0.x, w0.y, w0.z, w0.w, w1.x, w1.y, w1.z, w1.w );
  vec4 s[8];
  for ( int i = 0; i < 8; i++ ) s[i] = vec4( 0.0 );
  if ( w[0] > 0.0 ) s[0] = textureGrad( gLayer[0], uv, dx, dy );
  if ( w[1] > 0.0 ) s[1] = textureGrad( gLayer[1], uv, dx, dy );
  if ( w[2] > 0.0 ) s[2] = textureGrad( gLayer[2], uv, dx, dy );
  if ( w[3] > 0.0 ) s[3] = textureGrad( gLayer[3], uv, dx, dy );
  if ( w[4] > 0.0 ) s[4] = textureGrad( gLayer[4], uv, dx, dy );
  if ( w[5] > 0.0 ) s[5] = textureGrad( gLayer[5], uv, dx, dy );
  if ( w[6] > 0.0 ) s[6] = textureGrad( gLayer[6], uv, dx, dy );
  if ( w[7] > 0.0 ) s[7] = textureGrad( gLayer[7], uv, dx, dy );
  float score[8];
  float top = -1.0;
  for ( int i = 0; i < 8; i++ ) {
    score[i] = w[i] + ${HEIGHT_WEIGHT.toFixed(2)} * clamp( s[i].a * 2.0 - 1.0, 0.0, 1.0 );
    if ( w[i] > 0.0 ) top = max( top, score[i] );
  }
  vec3 c = vec3( 0.0 );
  float total = 0.0;
  for ( int i = 0; i < 8; i++ ) {
    float b = w[i] > 0.0 ? max( score[i] - ( top - ${BLEND_DEPTH.toFixed(2)} ), 0.0 ) : 0.0;
    c += s[i].rgb * b;
    total += b;
  }
  return c / max( total, 1e-5 );
}`,
    )
    .replace(
      "#include <map_fragment>",
      `#include <map_fragment>
  // Layer weights here, without the layers the game data lacks.
  // Read a little off the exact point, by noise of about a texel, so what
  // is left of the grid wobbles like a painted edge.
  vec2 gN = vMapUv * gWorld / ${MASK_WOBBLE_CELLS.toFixed(1)};
  vec2 gWobble = vec2( texture2D( gGrain, gN ).r, texture2D( gGrain, gN.yx + vec2( 0.37, 0.71 ) ).r ) - 0.5;
  vec2 gMaskUv = vMapUv + gWobble * ${MASK_WOBBLE.toFixed(2)} / gMaskSize;
  vec4 gw0 = gCubic( gMask0, gMaskUv ) * gHave0;
  vec4 gw1 = gCubic( gMask1, gMaskUv ) * gHave1;
  float gSum = dot( gw0, vec4( 1.0 ) ) + dot( gw1, vec4( 1.0 ) );
  if ( gSum < 0.004 ) { gw0 = gHave0; gw1 = gHave1; gSum = dot( gw0, vec4( 1.0 ) ) + dot( gw1, vec4( 1.0 ) ); }
  gw0 /= gSum;
  gw1 /= gSum;
  vec2 gRate = gTiling.xy / gWorld; // texture repeats per cell
  vec2 gUv = ( vMapUv * gWorld + gTiling.zw ) * gRate;
  vec3 gColor = gSplat( gUv, dFdx( gUv ), dFdy( gUv ), gw0, gw1 );
#ifdef G_SIDES
  // Steep faces: the same blend projected along x and y, darker and
  // greyer, closer to the game's rock cliffs than grass or paving would be.
  vec3 gn = normalize( vGNormal );
  // Not where a cliff piece stands: its walls cover the steps.
  float gSteep = smoothstep( 0.92, 0.62, gn.z ) * ( 1.0 - vGCliff );
  vec2 gSideW = abs( gn.xy ) / max( abs( gn.x ) + abs( gn.y ), 1e-4 );
  float gR = 0.5 * ( gRate.x + gRate.y );
  vec2 gUx = vGPos.yz * gR, gUy = vGPos.xz * gR;
  vec2 gUxdx = dFdx( gUx ), gUxdy = dFdy( gUx ), gUydx = dFdx( gUy ), gUydy = dFdy( gUy );
  if ( gSteep > 0.0 ) {
    vec3 gSide = vec3( 0.0 );
    if ( gSideW.x > 0.0 ) gSide += gSplat( gUx, gUxdx, gUxdy, gw0, gw1 ) * gSideW.x;
    if ( gSideW.y > 0.0 ) gSide += gSplat( gUy, gUydx, gUydy, gw0, gw1 ) * gSideW.y;
    gSide = mix( vec3( dot( gSide, vec3( 0.299, 0.587, 0.114 ) ) ), gSide, 0.45 ) * 0.72;
    gColor = mix( gColor, gSide, gSteep );
  }
#endif
  // Grain, fading out with distance as the mipmaps flatten it.
  float gG = texture2D( gGrain, vMapUv * gWorld / ${GRAIN_CELLS.toFixed(1)} ).r - 0.5;
  diffuseColor.rgb = gColor * ( 1.0 + gGrainAmount * 2.0 * gG );`,
    );
}
