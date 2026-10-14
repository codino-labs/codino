// The web UI's page. The live preview is the Device core built for the
// browser (device.wasm), fed the protocol stream the Host Daemon sends the
// Device (api/stream, Server-Sent Events). Preferences and preview controls
// are JSON POSTs; a heartbeat keeps the Host Daemon's web UI open.
"use strict";

const $ = (sel) => document.querySelector(sel);
const encoder = new TextEncoder();
const decoder = new TextDecoder();

// --- The browser Virtual Device ---------------------------------------------

const device = {
  wasm: null,
  profile: null, // the Hardware Profile of the Device being previewed
  themes: [], // the Themes the preview has, from its hello_reply

  async load() {
    const bytes = await (await fetch("device.wasm")).arrayBuffer();
    const module = await WebAssembly.compile(bytes);
    // The Device core does no I/O: its few WASI imports are never called.
    const imports = {};
    for (const i of WebAssembly.Module.imports(module)) {
      (imports[i.module] ??= {})[i.name] = () => 52; // ENOSYS
    }
    const instance = await WebAssembly.instantiate(module, imports);
    this.wasm = instance.exports;
    this.wasm._initialize();
  },

  start(profile) {
    this.profile = profile;
    this.wasm.vd_start(profile.width, profile.height, profile.brightness_cap);
    const canvas = $("#matrix");
    canvas.style.aspectRatio = `${profile.width} / ${profile.height}`;
    const cap = profile.brightness_cap;
    $("#device").textContent =
      `${profile.width}×${profile.height} matrix` + (cap < 255 ? `, brightness cap ${cap}/255 (shown at full scale)` : "");
  },

  // receive feeds one protocol line (a string, or the bytes of a frame).
  receive(line) {
    if (!this.profile) return;
    // The clock moves with every line too, not only when drawing: a hidden
    // tab draws nothing, and heartbeats must not arrive at a stale time.
    this.wasm.vd_set_millis(Math.floor(performance.now()) >>> 0);
    const bytes = typeof line === "string" ? encoder.encode(line) : line;
    const at = this.wasm.vd_line(bytes.length);
    new Uint8Array(this.wasm.memory.buffer, at, bytes.length).set(bytes);
    const size = this.wasm.vd_receive(bytes.length);
    if (size > 0) {
      const reply = decoder.decode(new Uint8Array(this.wasm.memory.buffer, this.wasm.vd_reply(), size));
      try {
        const message = JSON.parse(reply);
        if (message.type === "hello_reply") this.themes = message.themes || [];
      } catch { /* not JSON: nothing to learn */ }
    }
  },

  render(now) {
    if (!this.profile) return null;
    this.wasm.vd_set_millis(Math.floor(now) >>> 0);
    const at = this.wasm.vd_render();
    const { width, height } = this.profile;
    return new Uint8Array(this.wasm.memory.buffer, at, width * height * 3);
  },
};

// --- Drawing the matrix -----------------------------------------------------

// LEDs glow visibly at low values; a screen pixel does not. Show the frame
// relative to the brightness cap, lifted by a square root as the terminal
// Virtual Device does.
function lift(channel, cap) {
  return Math.round(Math.sqrt(Math.min(1, channel / Math.max(cap, 1))) * 255);
}

function draw(now) {
  const pixels = device.render(now);
  const canvas = $("#matrix");
  if (pixels) {
    const { width, height, brightness_cap: cap } = device.profile;
    const ratio = window.devicePixelRatio || 1;
    const cssWidth = canvas.clientWidth || 320;
    const pitch = (cssWidth * ratio) / width;
    canvas.width = Math.round(pitch * width);
    canvas.height = Math.round(pitch * height);
    const g = canvas.getContext("2d");
    g.fillStyle = "#0d0d0c";
    g.fillRect(0, 0, canvas.width, canvas.height);
    const radius = pitch * 0.34;
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const i = (y * width + x) * 3;
        const cx = (x + 0.5) * pitch, cy = (y + 0.5) * pitch;
        const r = lift(pixels[i], cap), gr = lift(pixels[i + 1], cap), b = lift(pixels[i + 2], cap);
        if (r + gr + b === 0) {
          g.fillStyle = "#1d1c1a";
          g.beginPath();
          g.arc(cx, cy, radius, 0, Math.PI * 2);
          g.fill();
          continue;
        }
        const glow = g.createRadialGradient(cx, cy, 0, cx, cy, pitch * 0.62);
        glow.addColorStop(0, `rgba(${r},${gr},${b},0.55)`);
        glow.addColorStop(1, `rgba(${r},${gr},${b},0)`);
        g.fillStyle = glow;
        g.fillRect(cx - pitch, cy - pitch, pitch * 2, pitch * 2);
        g.fillStyle = `rgb(${r},${gr},${b})`;
        g.beginPath();
        g.arc(cx, cy, radius, 0, Math.PI * 2);
        g.fill();
      }
    }
  }
  requestAnimationFrame(draw);
}

// --- Talking to the Host Daemon ---------------------------------------------

let closed = false;

function showClosed() {
  if (closed) return;
  closed = true;
  $("#closed").hidden = false;
}

async function post(path, body) {
  let resp;
  try {
    resp = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch {
    showClosed();
    throw new Error("the web UI has closed");
  }
  if (resp.status === 403) {
    showClosed();
    throw new Error("the web UI has closed");
  }
  const reply = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(reply.error || resp.statusText);
  return reply;
}

function base64Bytes(text) {
  const raw = atob(text);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

function stream() {
  const events = new EventSource("api/stream");
  events.addEventListener("device", (e) => device.start(JSON.parse(e.data)));
  events.addEventListener("line", (e) => device.receive(e.data));
  events.addEventListener("frame", (e) => device.receive(base64Bytes(e.data)));
  events.addEventListener("open", () => refresh());
  events.addEventListener("error", () => {
    // EventSource reconnects by itself; a closed UI refuses it for good.
    if (events.readyState === EventSource.CLOSED) showClosed();
  });
}

// --- Preferences and state --------------------------------------------------

let current = { preferences: [], status: {} };
const pending = new Set(); // Preferences being saved: not refreshed meanwhile

function preference(key) {
  const entry = current.preferences.find((p) => p.Key === key);
  return entry ? entry.Value : "";
}

// Go-style durations as the config file shows them ("1m30s", "500ms").
function durationMs(text) {
  const units = { h: 3600000, m: 60000, s: 1000, ms: 1 };
  let total = 0;
  for (const [, n, unit] of text.matchAll(/(\d+(?:\.\d+)?)(ms|h|m|s)/g)) total += Number(n) * units[unit];
  return total;
}

function setField(name, value) {
  const form = $("#form");
  const field = form.elements[name];
  if (!field || pending.has(name)) return;
  if (field instanceof RadioNodeList) {
    for (const radio of field) radio.checked = radio.value === value;
    return;
  }
  if (document.activeElement === field) return;
  if (field.type === "checkbox") field.checked = value === "true";
  else field.value = value;
  if (field.type === "range") field.parentElement.querySelector("output").value = value;
}

function render() {
  const { status } = current;
  const d = status.device || {};
  const link = $("#link");
  link.classList.toggle("ok", !!d.claimed);
  link.textContent = d.claimed ? `Device: ${d.name}` : d.problem ? `No Device: ${d.problem}` : "No Device connected: preview only";
  if (!d.claimed && d.update_url) {
    // A firmware too old for this Host: the problem ends with the web
    // flasher's URL, shown as a link.
    link.textContent = `No Device: ${d.problem.replace(d.update_url, "")}`;
    const a = document.createElement("a");
    a.href = d.update_url;
    a.target = "_blank";
    a.rel = "noopener";
    a.textContent = d.update_url;
    link.append(a);
  }

  const n = (status.sessions || []).length;
  $("#sessions").textContent = n === 0 ? "No Agent Sessions." :
    `${n} Agent Session${n === 1 ? "" : "s"}, Aggregate State ${status.aggregate_state.replace("_", " ")}.`;

  const problem = current.preferences_problem || status.preferences_problem;
  $("#problem").hidden = !problem;
  $("#problem").textContent = problem ? `The config file is ignored: ${problem}` : "";
  if (current.preferences_file) $("#config").textContent = current.preferences_file;

  for (const key of ["brightness", "rotation", "quiet_mode", "done_decay", "disconnected_timeout",
    "escalation.faster", "escalation.takeover", "escalation.steady", "layout"]) {
    setField(key, preference(key));
  }
  renderThemes(d);
  renderPins();
  renderPreview(status.preview || "");
}

function renderThemes(d) {
  const select = $("#form").elements.theme;
  const theme = preference("theme");
  const onDevice = d.claimed ? d.themes || [] : [];
  const names = [...new Set([...device.themes, ...onDevice, theme].filter(Boolean))];
  if (!pending.has("theme") && document.activeElement !== select) {
    select.replaceChildren(...names.map((name) => {
      const option = new Option(name, name, false, name === theme);
      const notes = [];
      if (d.claimed && !onDevice.includes(name)) notes.push("not on the Device");
      if (device.themes.length && !device.themes.includes(name)) notes.push("not in the preview");
      if (notes.length) option.textContent += ` (${notes.join(", ")})`;
      return option;
    }));
  }
  const note = $("#theme-note");
  let text = "";
  if (d.claimed && theme && !onDevice.includes(theme)) {
    text = `The Device doesn't have ${theme}: it shows "default". Install it with codino theme install FILE.`;
  } else if (device.themes.length && theme && !device.themes.includes(theme)) {
    text = `The preview has no copy of ${theme} (it was installed elsewhere): it shows "default" here.`;
  }
  note.hidden = !text;
  note.textContent = text;
}

function renderPins() {
  const pins = current.preferences.filter((p) => p.Key.startsWith("project_colors."));
  $("#pins").hidden = pins.length === 0;
  $("#pin-list").replaceChildren(...pins.map((p) => {
    const li = document.createElement("li");
    const swatch = document.createElement("span");
    swatch.className = "swatch";
    swatch.style.background = p.Value;
    const path = document.createElement("code");
    path.textContent = p.Key.slice("project_colors.".length);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = "Unpin";
    remove.addEventListener("click", () => save(p.Key, null));
    li.append(swatch, path, remove);
    return li;
  }));
}

async function refresh() {
  try {
    const resp = await fetch("api/state");
    if (resp.status === 403) return showClosed();
    if (!resp.ok) return;
    current = await resp.json();
    render();
  } catch {
    // The next refresh, or the stream, tells whether the UI closed.
  }
}

// save sets a Preference, or unsets it (value null: back to its default).
async function save(key, value) {
  const status = $("#saved");
  const field = $("#form").elements[key];
  pending.add(key);
  try {
    await post("api/preferences", value === null ? { key, unset: true } : { key, value });
    if (field && field.setAttribute) field.removeAttribute("aria-invalid");
    status.textContent = value === null ? `${key} back to its default.` : `${key} = ${value}`;
    status.className = "status";
  } catch (err) {
    if (field && field.setAttribute) field.setAttribute("aria-invalid", "true");
    status.textContent = err.message;
    status.className = "error";
  } finally {
    pending.delete(key);
    refresh();
  }
}

function wireForm() {
  const form = $("#form");
  form.addEventListener("submit", (e) => e.preventDefault());
  form.addEventListener("input", (e) => {
    if (e.target.type === "range") e.target.parentElement.querySelector("output").value = e.target.value;
  });
  form.addEventListener("change", (e) => {
    const field = e.target;
    if (!field.name) return;
    if (field.type === "checkbox") return save(field.name, String(field.checked));
    const value = field.value.trim();
    // An emptied field returns the Preference to its default.
    save(field.name, value === "" ? null : value);
  });
}

// --- Preview controls -------------------------------------------------------

let picked = null; // the preview button pressed last

function renderPreview(state) {
  const buttons = [...document.querySelectorAll("#states button")];
  let pressed = buttons.find((b) => b === picked && b.dataset.state === state);
  pressed ??= buttons.find((b) => b.dataset.state === state && !b.dataset.step);
  for (const b of buttons) b.setAttribute("aria-pressed", String(b === pressed));
}

function wirePreview() {
  for (const button of document.querySelectorAll("#states button")) {
    button.addEventListener("click", async () => {
      const state = button.dataset.state;
      const step = button.dataset.step;
      const elapsed = step ? durationMs(preference(`escalation.${step}`)) : 0;
      picked = button;
      renderPreview(state);
      try {
        await post("api/preview", { state, elapsed_ms: elapsed });
      } catch (err) {
        $("#saved").textContent = err.message;
        $("#saved").className = "error";
      }
      refresh();
    });
  }
}

// --- Start ------------------------------------------------------------------

async function main() {
  wireForm();
  wirePreview();
  await device.load();
  stream();
  requestAnimationFrame(draw);
  refresh();
  setInterval(() => { if (!closed) refresh(); }, 2000);
  // The Host Daemon keeps the web UI open while the page is here.
  const beat = () => { if (!closed) post("api/heartbeat", {}).catch(() => {}); };
  beat();
  setInterval(beat, 5000);
}

main();
