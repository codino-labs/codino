// Checks the browser Virtual Device just built against the shared protocol
// fixtures: `make -C device web` runs it when Node is installed.
//
// Usage: node check.mjs DEVICE_WASM FIXTURES_DIR
import fs from "fs";
import path from "path";

const [wasmPath, fixtures] = process.argv.slice(2);
const module = new WebAssembly.Module(fs.readFileSync(wasmPath));
const imports = {};
for (const i of WebAssembly.Module.imports(module)) (imports[i.module] ??= {})[i.name] = () => 52;
const vd = new WebAssembly.Instance(module, imports).exports;
vd._initialize();

const fixture = (name) => fs.readFileSync(path.join(fixtures, name), "utf8").trimEnd();
const receive = (line) => {
  const bytes = new TextEncoder().encode(line);
  new Uint8Array(vd.memory.buffer, vd.vd_line(bytes.length), bytes.length).set(bytes);
  const size = vd.vd_receive(bytes.length);
  return new TextDecoder().decode(new Uint8Array(vd.memory.buffer, vd.vd_reply(), size));
};
const frame = () => Array.from(new Uint8Array(vd.memory.buffer, vd.vd_render(), 8 * 8 * 3));
const fail = (why) => { console.error(`browser Virtual Device: ${why}`); process.exit(1); };

vd.vd_start(8, 8, 255);
vd.vd_set_millis(1000);
const reply = JSON.parse(receive(fixture("hello.jsonl")) || "{}");
if (reply.type !== "hello_reply" || !reply.themes?.includes("default")) fail(`hello answered ${JSON.stringify(reply)}`);
if (frame().some((c) => c !== 0)) fail("lit with no session");
receive(fixture("preview-enter.jsonl")); // a Takeover in Awaiting Input's own color
const pixels = frame();
for (let i = 0; i < pixels.length; i += 3) {
  if (pixels[i] !== pixels[0] || pixels[i + 1] !== pixels[1] || pixels[i + 2] !== pixels[2] || pixels[0] === 0)
    fail(`preview-enter.jsonl: not one lit color: ${pixels.slice(0, 12)}`);
}
receive(fixture("preview-exit.jsonl"));
if (frame().some((c) => c !== 0)) fail("preview-exit.jsonl: still lit");
receive(fixture("session-upsert.jsonl"));
if (!frame().some((c) => c !== 0)) fail("session-upsert.jsonl: dark");
receive(fixture("goodbye.jsonl"));
vd.vd_set_millis(1000 + 60000); // a minute of silence: dark, not Disconnected
if (frame().some((c) => c !== 0)) fail("goodbye.jsonl: still lit");
console.log("browser Virtual Device: OK against the protocol fixtures");
