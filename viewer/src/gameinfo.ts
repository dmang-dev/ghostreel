// SPDX-License-Identifier: Apache-2.0
//
// The game info panel: the map (title, description, screenshot), when and
// where the game was played, and each player's details from the replay.

import type { MapData } from "./api";
import { formatClock, type Timeline } from "./model";

const esc = (s: string) =>
  s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

const row = (k: string, v: string) => (v ? `<div class="row"><span>${k}</span><span>${v}</span></div>` : "");

/** The panel's HTML. image is an object URL for the map's picture, if any. */
export function gameInfoHTML(tl: Timeline, map: MapData | null, image: string | null, color: (p: number) => string): string {
  const m = tl.meta!;
  const when = m.startTime ? new Date(Number(m.startTime) * 1000) : undefined;
  const mode = m.ladder ? (m.competitive ? "Ladder, ranked" : "Ladder, unranked") : m.region ? "Custom game on Battle.net" : "Local game";
  const title = map?.title || m.mapTitle;
  let html = `<h3>${esc(title)}</h3>`;
  if (image) html += `<img class="map-image" src="${image}" alt="">`;
  if (map?.description) html += `<p class="note">${esc(map.description)}</p>`;
  const kind = map?.players && /^\d+$/.test(map.players) ? `${map.players} players` : map?.players;
  html += row("Map", esc([kind, m.mapAuthor ? `by ${m.mapAuthor}` : ""].filter(Boolean).join(" · ")));
  html += row("Played", when ? esc(when.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })) : "");
  html += row("Mode", esc(mode + (m.region ? ` · ${m.region}` : "")));
  html += row("Length", formatClock(m.loops));
  html += row("Version", esc(m.version));
  if (m.observers.length) html += row("Observers", esc(m.observers.join(", ")));

  html += `<h4>Players</h4>`;
  for (const p of m.players) {
    const name = (p.clan ? `[${p.clan}] ` : "") + p.name;
    const race = p.selectedRace && p.selectedRace !== p.race ? `${p.race} (picked ${p.selectedRace})` : p.race;
    const bits = [
      race,
      p.result && p.result !== "Unknown" && p.result !== "Undecided" ? p.result : "",
      p.apm ? `${p.apm} APM` : "",
      p.mmr ? `${p.mmr} MMR` : "",
      p.handicap && p.handicap !== 100 ? `${p.handicap}% handicap` : "",
      p.difficulty ? `Computer: ${p.difficulty}${p.aiBuild ? `, ${p.aiBuild}` : ""}` : "",
    ].filter(Boolean);
    html += `<div class="player-info"><b style="color:${color(p.id)}">${esc(name)}</b><span>${esc(bits.join(" · "))}</span>` +
      (p.toon ? `<span class="muted">Battle.net profile ${esc(p.toon)}</span>` : "") + `</div>`;
  }
  return html;
}
