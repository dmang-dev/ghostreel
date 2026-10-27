// SPDX-License-Identifier: Apache-2.0
//
// Updates in the desktop app: ask once whether to look for new releases on
// GitHub when the app starts (ghostreel makes no network requests unless
// asked), show a banner when one is out, and install it on request. The Go
// side does the checking, verifying and installing (desktop/update.go);
// this is only the UI. Settings gets a switch and a "Check now" button.

import * as api from "./api";

const stage = () => document.getElementById("stage")!;

function openURL(url: string): void {
  const rt = (window as unknown as { runtime?: { BrowserOpenURL?: (u: string) => void } }).runtime;
  if (rt?.BrowserOpenURL) rt.BrowserOpenURL(url);
  else window.open(url, "_blank", "noopener");
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, props: Record<string, unknown> = {}, ...kids: (Node | string)[]): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  Object.assign(e, props);
  e.append(...kids);
  return e;
}

/** The banner at the top of the view; one at a time. */
function banner(...kids: (Node | string)[]): HTMLElement {
  document.getElementById("update-banner")?.remove();
  const b = el("div", { id: "update-banner", className: "banner" }, ...kids);
  stage().append(b);
  return b;
}

const close = () => document.getElementById("update-banner")?.remove();

async function saveCheck(on: boolean): Promise<void> {
  const s = await api.getSettings();
  await api.saveSettings({ ...s, checkUpdates: on });
}

/** Follows a check, a download or an install, and shows where it stands. */
async function follow(): Promise<void> {
  for (let i = 0; i < 1200; i++) {
    const st = await api.getUpdate().catch(() => null);
    if (!st) return;
    if (st.state === "checking") {
      await new Promise((r) => setTimeout(r, 1000));
      continue;
    }
    if (st.state === "downloading") {
      banner(`Downloading ghostreel ${st.latest}… ${Math.round((st.progress ?? 0) * 100)}%`);
    } else if (st.state === "installing") {
      banner(`Installing ghostreel ${st.latest}. It will restart in a moment; approve the installer if Windows asks.`);
    } else if (st.state === "error") {
      banner(`The update didn't work: ${st.error}`, el("button", { textContent: "Dismiss", onclick: close }));
      return;
    } else {
      if (st.available) offer(st);
      return;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
}

function offer(st: api.UpdateStatus): void {
  const notes = st.notesUrl;
  banner(
    el("b", { textContent: `ghostreel ${st.latest} is out` }),
    ` (you have ${st.current}). `,
    st.canApply
      ? el("button", {
          textContent: "Update and restart",
          onclick: () => {
            api.applyUpdate().then(follow, (e) => banner(`The update didn't work: ${(e as Error).message}`, el("button", { textContent: "Dismiss", onclick: close })));
          },
        })
      : el("button", { textContent: "Download", onclick: () => notes && openURL(notes) }),
    " ",
    notes ? el("button", { textContent: "What's new", onclick: () => openURL(notes) }) : "",
    " ",
    el("button", { textContent: "Later", onclick: close }),
  );
}

/** Starts the update UI (desktop app only). */
export async function setupUpdates(): Promise<void> {
  const s = await api.getSettings().catch(() => null);
  if (!s) return;
  addToSettings();
  if (s.checkUpdates === undefined) {
    // Ask once.
    banner(
      "Look for new versions of ghostreel on GitHub when it starts? ",
      el("button", {
        textContent: "Yes",
        onclick: async () => {
          close();
          await saveCheck(true);
          await api.checkUpdate().catch(() => null);
          void follow();
        },
      }),
      " ",
      el("button", { textContent: "No", onclick: () => { close(); void saveCheck(false); } }),
    );
  } else if (s.checkUpdates) {
    void follow(); // the app checks on its own at start
  }
}

/** Settings: the switch, the versions, and "Check now". */
function addToSettings(): void {
  const panel = document.getElementById("settings");
  const note = document.getElementById("settings-note");
  if (!panel || !note || document.getElementById("set-updates")) return;
  const box = el("input", { type: "checkbox", id: "set-updates" });
  const status = el("p", { className: "note", id: "set-updates-status" });
  const show = (st: api.UpdateStatus | null) => {
    if (!st) return;
    let text = `This is ghostreel ${st.current}.`;
    if (st.state === "checking") text += " Checking…";
    else if (st.state === "error") text += ` The last check failed: ${st.error}`;
    else if (st.available) text += ` ${st.latest} is out.`;
    else if (st.latest) text += " It's the latest.";
    status.textContent = text;
  };
  const check = el("button", {
    textContent: "Check now",
    onclick: async () => {
      status.textContent = "Checking…";
      const st = await api.checkUpdate().catch((e) => {
        status.textContent = (e as Error).message;
        return null;
      });
      show(st);
      if (st?.available) offer(st);
    },
  });
  box.onchange = () => void saveCheck(box.checked);
  panel.insertBefore(el("label", {}, box, " Look for new versions when ghostreel starts"), note);
  panel.insertBefore(el("div", { className: "row" }, status, check), note);
  // Fill in when the panel opens.
  document.getElementById("settings-btn")?.addEventListener("click", async () => {
    const s = await api.getSettings().catch(() => null);
    box.checked = !!s?.checkUpdates;
    show(await api.getUpdate().catch(() => null));
  });
}
