// The web flasher: the Hardware Profile the human chooses, as the partition
// image hardware_profile.mjs builds, flashed with the firmware by ESP Web
// Tools through a manifest made here. Every URL in it is absolute, as ESP
// Web Tools wants for a manifest from a blob: URL.
import { PROFILE_OFFSET, drivablePins, presets, profileImage, profileLine, profileProblems } from "./hardware_profile.mjs";

const $ = (selector) => document.querySelector(selector);
const CUSTOM = "custom";

// What `make -C device flasher` put next to the page.
let build = { firmware: "unknown", protocol: "?" };
let urls = [];

function chosenProfile() {
  const id = document.querySelector("input[name=preset]:checked").value;
  if (id !== CUSTOM) return presets.find((p) => p.id === id).profile;
  const form = $("#custom");
  const number = (name) => Number(form.elements[name].value);
  return {
    width: number("width"), height: number("height"), data_pin: number("data_pin"),
    serpentine: form.elements.serpentine.checked, color_order: form.elements.color_order.value,
    brightness_cap: number("brightness_cap"), current_cap_ma: number("current_cap_ma"),
    button: form.elements.button.checked, imu: form.elements.imu.checked,
  };
}

// Fills the Custom form with a profile, to start from.
function fillCustom(profile) {
  const form = $("#custom");
  for (const [field, value] of Object.entries(profile)) {
    const el = form.elements[field];
    if (el.type === "checkbox") el.checked = value;
    else el.value = String(value);
  }
}

function update() {
  const custom = document.querySelector("input[name=preset]:checked").value === CUSTOM;
  $("#custom").hidden = !custom;
  const profile = chosenProfile();
  const problems = profileProblems(profile);
  $("#problems").replaceChildren(...problems.map((p) => Object.assign(document.createElement("li"), { textContent: p })));
  $("#problems").hidden = problems.length === 0;
  $("#line").textContent = profileLine(profile);
  $("#activate").disabled = problems.length > 0;
  for (const url of urls) URL.revokeObjectURL(url);
  urls = [];
  if (problems.length > 0) {
    $("#install").manifest = undefined;
    return;
  }
  const blobURL = (data, type) => {
    const url = URL.createObjectURL(new Blob([data], { type }));
    urls.push(url);
    return url;
  };
  const manifest = {
    name: "Codino",
    version: build.firmware,
    // No Improv Wi-Fi: Codino has no network.
    new_install_improv_wait_time: 0,
    new_install_prompt_erase: true,
    builds: [{
      chipFamily: "ESP32-S3",
      parts: [
        // bootloader, partition table and app in one image (device/Makefile)
        { path: new URL("firmware/firmware-merged.bin", location.href).href, offset: 0 },
        { path: blobURL(profileImage(profile), "application/octet-stream"), offset: PROFILE_OFFSET },
      ],
    }],
  };
  $("#install").manifest = blobURL(JSON.stringify(manifest), "application/json");
}

async function start() {
  if (!("serial" in navigator)) $("#browser").hidden = false;
  try {
    build = await (await fetch("build-info.json")).json();
    $("#build").textContent = `Firmware ${build.firmware}, protocol ${build.protocol}`;
  } catch {
    $("#build").textContent = "No firmware build next to this page: serve the output of `make -C device flasher`.";
  }

  const fieldset = $("#presets");
  const option = (id, label, checked) => {
    const el = document.createElement("label");
    el.className = "check";
    el.innerHTML = `<input type="radio" name="preset">`;
    Object.assign(el.firstChild, { value: id, checked });
    el.append(` ${label}`);
    fieldset.append(el);
  };
  for (const p of presets) option(p.id, p.label, !!p.preselected);
  option(CUSTOM, "Custom…", false);
  $("#custom").elements.data_pin.append(...drivablePins.map((pin) => new Option(String(pin), String(pin))));
  fillCustom(presets.find((p) => p.preselected).profile);

  let lastPreset = presets.find((p) => p.preselected).id;
  fieldset.addEventListener("change", () => {
    const id = document.querySelector("input[name=preset]:checked").value;
    // Custom starts from the preset chosen before it.
    if (id === CUSTOM) fillCustom(presets.find((p) => p.id === lastPreset).profile);
    else lastPreset = id;
    update();
  });
  $("#custom").addEventListener("input", update);
  update();
}

start();
