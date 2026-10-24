// SPDX-License-Identifier: Apache-2.0
//
// Reports from the core's analysis plugins (internal/analysis: build orders,
// battles, and any plugin added later). They share the events panel as
// extra tabs; every row and marker is tied to a loop, and selecting it seeks
// there. Nothing here knows a particular plugin.

export interface Marker {
  loop: number;
  end?: number;
  player?: number;
  x?: number;
  y?: number;
  r?: number;
  label: string;
  kind?: string;
  /** Hotspots: what happened, how much it matters (0-1), "major" or "minor". */
  category?: string;
  score?: number;
  tier?: string;
}

export interface Table {
  title: string;
  player?: number;
  columns: string[];
  rows: { loop: number; cells: string[] }[];
}

export interface Series {
  title: string;
  player?: number;
  unit?: string;
  points: { loop: number; value: number }[];
}

export interface Report {
  plugin: string;
  title: string;
  markers?: Marker[];
  tables?: Table[];
  series?: Series[];
  error?: string;
}

export class ReportsView {
  reports: Report[] = [];
  /** -1 is the events feed; otherwise an index into reports. */
  current = -1;
  private table = 0;
  private rowEls: { loop: number; el: HTMLElement }[] = [];
  private chart?: { canvas: HTMLCanvasElement; series: Series[] };
  private loop = 0;
  /** Game length in loops, for chart scales. */
  loops = 1;

  constructor(
    private readonly tabs: HTMLElement,
    private readonly body: HTMLElement,
    private readonly feed: HTMLElement,
    private readonly seek: (loop: number) => void,
    private readonly color: (player: number) => string,
  ) {}

  set(reports: Report[]): void {
    this.reports = reports.filter(
      (r) => r.error || r.tables?.some((t) => t.rows.length) || r.markers?.length || r.series?.some((s) => s.points.length),
    );
    for (const r of reports) if (r.error) console.warn(`analysis plugin ${r.plugin}: ${r.error}`);
    if (this.current >= this.reports.length) this.current = -1;
    this.table = 0;
    this.render();
  }

  /** Shows the next tab (events, then each report). */
  cycle(): void {
    this.current = this.current + 1 >= this.reports.length ? -1 : this.current + 1;
    this.table = 0;
    this.render();
  }

  /** Every marker of every report. */
  markers(): Marker[] {
    return this.reports.flatMap((r) => r.markers ?? []);
  }

  private render(): void {
    this.tabs.replaceChildren(
      this.tab("Events", -1),
      ...this.reports.map((r, i) => this.tab(r.title, i)),
    );
    const report = this.reports[this.current];
    this.feed.hidden = !!report;
    this.body.hidden = !report;
    this.rowEls = [];
    this.chart = undefined;
    if (!report) return;
    this.body.replaceChildren();
    if (report.error) {
      const p = document.createElement("p");
      p.className = "report-error";
      p.textContent = `This plugin failed: ${report.error}`;
      this.body.append(p);
      return;
    }
    if (report.series?.length) {
      this.renderChart(report.series);
      return;
    }
    const tables = report.tables ?? [];
    if (tables.length > 1) {
      // One table per player (build orders): pick one.
      const pick = document.createElement("div");
      pick.className = "report-pick";
      tables.forEach((t, i) => {
        const b = document.createElement("button");
        b.textContent = t.title;
        b.className = i === this.table ? "on" : "";
        if (t.player) b.style.borderColor = this.color(t.player);
        b.onclick = () => {
          this.table = i;
          this.render();
        };
        pick.append(b);
      });
      this.body.append(pick);
    }
    const t = tables[this.table];
    if (!t) return;
    const table = document.createElement("table");
    const head = table.insertRow();
    for (const c of t.columns) {
      const th = document.createElement("th");
      th.textContent = c;
      head.append(th);
    }
    for (const row of t.rows) {
      const tr = table.insertRow();
      for (const c of row.cells) tr.insertCell().textContent = c;
      tr.onclick = () => this.seek(row.loop);
      this.rowEls.push({ loop: row.loop, el: tr });
    }
    if (!t.rows.length) {
      const p = document.createElement("p");
      p.className = "report-empty";
      p.textContent = "Nothing found.";
      this.body.append(p);
    }
    this.body.append(table);
  }

  private tab(title: string, i: number): HTMLElement {
    const b = document.createElement("button");
    b.textContent = title;
    b.className = i === this.current ? "on" : "";
    b.onclick = () => {
      this.current = i;
      this.table = 0;
      this.render();
    };
    return b;
  }

  /** Marks the rows already reached at loop. */
  update(loop: number): void {
    this.loop = loop;
    for (const { loop: l, el } of this.rowEls) el.classList.toggle("past", l <= loop);
    if (this.chart) this.drawChart();
  }

  /** Series with one title share a chart; buttons pick the title. */
  private renderChart(series: Series[]): void {
    const titles = [...new Set(series.map((s) => s.title))];
    const pick = document.createElement("div");
    pick.className = "report-pick";
    titles.forEach((title, i) => {
      const b = document.createElement("button");
      b.textContent = title;
      b.className = i === this.table ? "on" : "";
      b.onclick = () => {
        this.table = i;
        this.render();
      };
      pick.append(b);
    });
    const canvas = document.createElement("canvas");
    canvas.className = "report-chart";
    canvas.onclick = (e) => {
      const r = canvas.getBoundingClientRect();
      this.seek(Math.round(((e.clientX - r.left) / r.width) * this.loops));
    };
    const legend = document.createElement("div");
    legend.className = "report-legend";
    this.body.append(pick, canvas, legend);
    this.chart = { canvas, series: series.filter((s) => s.title === titles[this.table]) };
    this.drawChart();
  }

  private drawChart(): void {
    const { canvas, series } = this.chart!;
    const dpr = Math.min(window.devicePixelRatio, 2);
    const w = canvas.clientWidth || 260;
    const h = canvas.clientHeight || 140;
    canvas.width = w * dpr;
    canvas.height = h * dpr;
    const ctx = canvas.getContext("2d")!;
    ctx.scale(dpr, dpr);
    let max = 1;
    for (const s of series) for (const p of s.points) max = Math.max(max, p.value);
    const pad = 4;
    const x = (loop: number) => (loop / this.loops) * w;
    const y = (v: number) => h - pad - (v / max) * (h - 2 * pad);
    ctx.strokeStyle = "#ffffff18";
    ctx.lineWidth = 1;
    for (const f of [0.25, 0.5, 0.75]) {
      ctx.beginPath();
      ctx.moveTo(0, y(max * f) + 0.5);
      ctx.lineTo(w, y(max * f) + 0.5);
      ctx.stroke();
    }
    const now: string[] = [];
    for (const s of series) {
      ctx.strokeStyle = this.color(s.player ?? 0);
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      s.points.forEach((p, i) => (i ? ctx.lineTo(x(p.loop), y(p.value)) : ctx.moveTo(x(p.loop), y(p.value))));
      ctx.stroke();
      let v: number | undefined;
      for (const p of s.points) if (p.loop <= this.loop) v = p.value;
      if (v !== undefined) now.push(`<span style="color:${this.color(s.player ?? 0)}">${Math.round(v)}${s.unit ?? ""}</span>`);
    }
    ctx.strokeStyle = "#ffffffaa";
    ctx.beginPath();
    ctx.moveTo(Math.round(x(this.loop)) + 0.5, 0);
    ctx.lineTo(Math.round(x(this.loop)) + 0.5, h);
    ctx.stroke();
    const legend = canvas.nextElementSibling as HTMLElement | null;
    if (legend) legend.innerHTML = `max ${Math.round(max)}${series[0]?.unit ?? ""} · now ${now.join(" · ")}`;
  }
}
