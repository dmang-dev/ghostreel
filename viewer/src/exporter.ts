// SPDX-License-Identifier: Apache-2.0
//
// Video export: the whole game, or a highlight reel of the top hotspots, as
// an MP4 file, made in the page. Frames are rendered one by one at a fixed
// size by stepping the replay clock (so the export runs as fast as the
// machine can render, and every run gives the same video), encoded with
// WebCodecs (H.264, else VP9 or AV1) and packed by mp4-muxer. No server work
// and no FFmpeg.

import { ArrayBufferTarget, Muxer } from "mp4-muxer";
import { selectReel, type Director, type Shot } from "./director";
import { formatClock, LOOPS_PER_CLOCK_SECOND, type Model } from "./model";
import type { Marker } from "./reports";
import type { Scene } from "./scene";

export interface ExportOptions {
  range: "game" | "reel";
  reelCount: number;
  /** Game speed: 1 is real time at Faster. */
  speed: number;
  width: number;
  height: number;
  fps: number;
  /** Let the director move the camera; otherwise the current view stays. */
  director: boolean;
}

export interface ExportContext {
  scene: Scene;
  model: Model;
  director?: Director;
  hotspots: Marker[];
  playerColor(id: number): string;
  /** The minimap, drawn for this loop. */
  minimap(loop: number): HTMLCanvasElement | undefined;
}

/** Whether this browser can encode video (WebCodecs). */
export function canExport(): boolean {
  return typeof VideoEncoder !== "undefined" && typeof VideoFrame !== "undefined";
}

interface Segment {
  from: number;
  to: number;
  title?: string;
  hotspot?: Marker;
}

const CARD_SECONDS = 1.2;

/** The parts of the game to film, in order. */
export function plan(model: Model, hotspots: Marker[], opts: ExportOptions): Segment[] {
  if (opts.range === "game") return [{ from: 0, to: model.loops }];
  const lead = 6 * LOOPS_PER_CLOCK_SECOND;
  const tail = 3 * LOOPS_PER_CLOCK_SECOND;
  return selectReel(hotspots, opts.reelCount).map((h) => ({
    from: Math.max(0, h.loop - lead),
    to: Math.min(model.loops, (h.end ?? h.loop) + tail),
    title: `${formatClock(h.loop)}  ${h.label.split(":")[0]}`,
    hotspot: h,
  }));
}

/** Frames the export will have (for progress). */
export function frameCount(segs: Segment[], opts: ExportOptions): number {
  const perFrame = (LOOPS_PER_CLOCK_SECOND * opts.speed) / opts.fps;
  let n = 0;
  for (const s of segs) n += Math.ceil((s.to - s.from) / perFrame) + (s.title ? Math.round(CARD_SECONDS * opts.fps) : 0);
  return n;
}

async function pickCodec(w: number, h: number, fps: number): Promise<{ config: VideoEncoderConfig; mux: "avc" | "vp9" | "av1" }> {
  const bitrate = Math.round(w * h * fps * 0.15);
  const candidates: [string, "avc" | "vp9" | "av1"][] = [
    ["avc1.640028", "avc"],
    ["avc1.4d0028", "avc"],
    ["vp09.00.40.08", "vp9"],
    ["av01.0.08M.08", "av1"],
  ];
  for (const [codec, mux] of candidates) {
    const config: VideoEncoderConfig = { codec, width: w, height: h, bitrate, framerate: fps };
    if (codec.startsWith("avc")) (config as VideoEncoderConfig & { avc?: { format: string } }).avc = { format: "avc" };
    try {
      if ((await VideoEncoder.isConfigSupported(config)).supported) return { config, mux };
    } catch {
      // try the next one
    }
  }
  throw new Error("This browser can't encode H.264, VP9 or AV1 video.");
}

/**
 * Renders and encodes the video; resolves to the MP4. onProgress gets 0-1.
 * The caller pauses playback first and restores the view afterwards.
 */
export async function exportVideo(
  ctx: ExportContext,
  opts: ExportOptions,
  onProgress: (fraction: number) => void,
  signal: AbortSignal,
): Promise<Blob> {
  if (!canExport()) throw new Error("This browser can't encode video (no WebCodecs). Use Chrome, Edge, or the Windows or macOS app.");
  const { width: w, height: h, fps } = opts;
  const segs = plan(ctx.model, ctx.hotspots, opts);
  if (!segs.length) throw new Error("No hotspots to make a reel from.");
  const total = frameCount(segs, opts);
  const { config, mux } = await pickCodec(w, h, fps);

  const target = new ArrayBufferTarget();
  const muxer = new Muxer({ target, video: { codec: mux, width: w, height: h, frameRate: fps }, fastStart: "in-memory" });
  let failure: Error | undefined;
  const encoder = new VideoEncoder({
    output: (chunk, meta) => muxer.addVideoChunk(chunk, meta),
    error: (e) => (failure = e instanceof Error ? e : new Error(String(e))),
  });
  encoder.configure(config);

  const frame = document.createElement("canvas");
  frame.width = w;
  frame.height = h;
  const g = frame.getContext("2d")!;
  const scene = ctx.scene;
  scene.beginExport(w, h);
  const perFrame = (LOOPS_PER_CLOCK_SECOND * opts.speed) / fps;
  let n = 0;

  const emit = async () => {
    const vf = new VideoFrame(frame, { timestamp: Math.round((n * 1e6) / fps), duration: Math.round(1e6 / fps) });
    encoder.encode(vf, { keyFrame: n % (fps * 2) === 0 });
    vf.close();
    n++;
    while (encoder.encodeQueueSize > 4) await new Promise((r) => setTimeout(r, 1));
    if (n % 10 === 0) {
      onProgress(n / total);
      await new Promise((r) => setTimeout(r, 0)); // let the page breathe
    }
    if (failure) throw failure;
    if (signal.aborted) throw new DOMException("Export cancelled", "AbortError");
  };

  try {
    for (const seg of segs) {
      if (seg.title) {
        for (let i = 0; i < Math.round(CARD_SECONDS * fps); i++) {
          titleCard(g, w, h, seg, ctx);
          await emit();
        }
      }
      ctx.director?.reset();
      for (let loop = seg.from; loop <= seg.to; loop += perFrame) {
        let shot: Shot | undefined;
        if (opts.director && ctx.director) {
          shot = ctx.director.shotAt(loop);
          scene.view.cx = shot.cx;
          scene.view.cy = shot.cy;
          scene.view.ppc = h / shot.span;
          scene.updateCamera();
        }
        scene.render(loop, undefined);
        g.drawImage(scene.layers()[0], 0, 0, w, h);
        g.drawImage(scene.layers()[1], 0, 0, w, h);
        hud(g, w, h, loop, ctx, shot?.hotspot ?? seg.hotspot);
        await emit();
      }
    }
    await encoder.flush();
    muxer.finalize();
    onProgress(1);
    return new Blob([target.buffer], { type: "video/mp4" });
  } finally {
    if (encoder.state !== "closed") encoder.close();
    scene.endExport();
  }
}

/** The overlay: players across the top, clock, hotspot caption, minimap. */
function hud(g: CanvasRenderingContext2D, w: number, h: number, loop: number, ctx: ExportContext, hotspot?: Marker): void {
  const s = h / 720; // scale with resolution
  const font = (px: number, weight = 600) => `${weight} ${Math.round(px * s)}px system-ui, sans-serif`;
  const players = ctx.model.tl.meta?.players ?? [];
  // Player strip.
  let x = 16 * s;
  for (const p of players) {
    const st = ctx.model.statsAt(p.id, loop);
    const text = st
      ? `${p.name}  ${Math.round(st.supplyUsed)}/${Math.round(st.supplyMade)}  ·  ${st.mineralRate + st.vespeneRate}/min  ·  army ${st.armyMinerals + st.armyVespene}`
      : p.name;
    g.font = font(15);
    const tw = g.measureText(text).width;
    g.fillStyle = "rgba(10,13,17,0.78)";
    g.fillRect(x, 14 * s, tw + 28 * s, 30 * s);
    g.fillStyle = ctx.playerColor(p.id);
    g.fillRect(x, 14 * s, 5 * s, 30 * s);
    g.fillStyle = "#e8edf2";
    g.textBaseline = "middle";
    g.fillText(text, x + 16 * s, 29 * s);
    x += tw + 40 * s;
  }
  // Clock.
  const clock = formatClock(loop);
  g.font = font(20, 700);
  const cw = g.measureText(clock).width;
  g.fillStyle = "rgba(10,13,17,0.78)";
  g.fillRect(w - cw - 40 * s, 12 * s, cw + 24 * s, 34 * s);
  g.fillStyle = "#e8edf2";
  g.fillText(clock, w - cw - 28 * s, 29 * s);
  // Hotspot caption, lower third.
  if (hotspot) {
    const [what, ...rest] = hotspot.label.split(": ");
    const detail = rest.join(": ");
    g.font = font(22, 700);
    const ww = g.measureText(what).width;
    g.font = font(16, 500);
    const dw = g.measureText(detail).width;
    const bw = Math.max(ww, dw) + 48 * s;
    const by = h - 118 * s;
    g.fillStyle = "rgba(10,13,17,0.82)";
    g.fillRect(w / 2 - bw / 2, by, bw, 74 * s);
    g.fillStyle = hotspot.tier === "major" ? "#ffd666" : "#8bc4ff";
    g.fillRect(w / 2 - bw / 2, by, bw, 4 * s);
    g.textAlign = "center";
    g.fillStyle = "#ffffff";
    g.font = font(22, 700);
    g.fillText(what, w / 2, by + 26 * s);
    g.fillStyle = "#c9d1d9";
    g.font = font(16, 500);
    g.fillText(detail, w / 2, by + 54 * s);
    g.textAlign = "left";
  }
  // Minimap, bottom left.
  const mm = ctx.minimap(loop);
  if (mm && mm.width > 0) {
    const mh = 170 * s;
    const mw = (mm.width / mm.height) * mh;
    g.fillStyle = "rgba(10,13,17,0.8)";
    g.fillRect(12 * s, h - mh - 22 * s, mw + 8 * s, mh + 8 * s);
    g.drawImage(mm, 16 * s, h - mh - 18 * s, mw, mh);
  }
  // Credit.
  g.font = font(12, 500);
  g.fillStyle = "rgba(232,237,242,0.55)";
  g.textAlign = "right";
  g.fillText("ghostreel", w - 16 * s, h - 14 * s);
  g.textAlign = "left";
}

/** A title card before each reel clip. */
function titleCard(g: CanvasRenderingContext2D, w: number, h: number, seg: Segment, ctx: ExportContext): void {
  const s = h / 720;
  g.fillStyle = "#0b0d10";
  g.fillRect(0, 0, w, h);
  const hsp = seg.hotspot;
  g.textAlign = "center";
  g.textBaseline = "middle";
  if (hsp?.tier) {
    g.fillStyle = hsp.tier === "major" ? "#ffd666" : "#8bc4ff";
    g.font = `700 ${Math.round(16 * s)}px system-ui, sans-serif`;
    g.fillText(hsp.tier.toUpperCase(), w / 2, h / 2 - 46 * s);
  }
  g.fillStyle = "#ffffff";
  g.font = `700 ${Math.round(40 * s)}px system-ui, sans-serif`;
  g.fillText(seg.title ?? "", w / 2, h / 2);
  const players = (ctx.model.tl.meta?.players ?? []).map((p) => p.name).join("  vs  ");
  g.fillStyle = "#8a94a0";
  g.font = `500 ${Math.round(18 * s)}px system-ui, sans-serif`;
  g.fillText(players, w / 2, h / 2 + 44 * s);
  g.textAlign = "left";
  g.textBaseline = "alphabetic";
}
