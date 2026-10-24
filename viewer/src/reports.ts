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
}

export interface Table {
  title: string;
  player?: number;
  columns: string[];
  rows: { loop: number; cells: string[] }[];
}

export interface Report {
  plugin: string;
  title: string;
  markers?: Marker[];
  tables?: Table[];
  error?: string;
}

export class ReportsView {
  reports: Report[] = [];
  /** -1 is the events feed; otherwise an index into reports. */
  current = -1;
  private table = 0;
  private rowEls: { loop: number; el: HTMLElement }[] = [];

  constructor(
    private readonly tabs: HTMLElement,
    private readonly body: HTMLElement,
    private readonly feed: HTMLElement,
    private readonly seek: (loop: number) => void,
    private readonly color: (player: number) => string,
  ) {}

  set(reports: Report[]): void {
    this.reports = reports.filter((r) => r.error || r.tables?.some((t) => t.rows.length) || r.markers?.length);
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
    if (!report) return;
    this.body.replaceChildren();
    if (report.error) {
      const p = document.createElement("p");
      p.className = "report-error";
      p.textContent = `This plugin failed: ${report.error}`;
      this.body.append(p);
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
    for (const { loop: l, el } of this.rowEls) el.classList.toggle("past", l <= loop);
  }
}
