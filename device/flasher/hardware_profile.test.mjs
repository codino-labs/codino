// The web flasher's Hardware Profile encoder against the golden files the
// C++ decoder's tests also read (device/test/shell_test.cpp, "flasher
// images"), so the browser and the firmware can't drift apart. Run with
// `node --test device/flasher/hardware_profile.test.mjs`; `make -C device test` runs it when Node is
// installed.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  PARTITION_SIZE,
  PROFILE_OFFSET,
  drivablePins,
  presets,
  profileImage,
  profileLine,
  profileProblems,
} from "./hardware_profile.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const golden = path.join(here, "../test/golden");
const goldenLines = new Map(
  fs.readFileSync(path.join(golden, "hardware-profiles.tsv"), "utf8").trimEnd().split("\n").map((row) => row.split("\t")),
);

// A Custom profile at the edges of every range, as hardware-profiles.tsv
// stores it under custom-example.
const customExample = {
  width: 32, height: 8, data_pin: 48, serpentine: true, color_order: "BRG",
  brightness_cap: 255, current_cap_ma: 65535, button: true, imu: false,
};

test("every preset is stored as its golden line", () => {
  assert.deepEqual(presets.map((p) => p.id), [...goldenLines.keys()].filter((id) => id !== "custom-example"));
  for (const preset of presets) assert.equal(profileLine(preset.profile), goldenLines.get(preset.id), preset.id);
});

test("a Custom profile is stored as its golden line", () => {
  assert.equal(profileLine(customExample), goldenLines.get("custom-example"));
});

test("the preselected preset is the reference board, as its whole partition image", () => {
  assert.equal(presets[0].id, "esp32-s3-matrix");
  assert.equal(presets[0].preselected, true);
  assert.equal(presets.filter((p) => p.preselected).length, 1);
  const image = profileImage(presets[0].profile);
  assert.equal(image.length, PARTITION_SIZE);
  assert.deepEqual(Buffer.from(image), fs.readFileSync(path.join(golden, "hwprofile-esp32-s3-matrix.bin")));
});

test("the image goes where the firmware's partition table puts hwprofile", () => {
  const table = fs.readFileSync(path.join(here, "../firmware/partitions.csv"), "utf8");
  const row = table.split("\n").find((line) => line.startsWith("hwprofile"));
  const [, , , offset, size] = row.split(",").map((field) => field.trim());
  assert.equal(Number(offset), PROFILE_OFFSET);
  assert.equal(Number(size), PARTITION_SIZE);
});

test("the pins offered are the ones the firmware can drive", () => {
  const main = fs.readFileSync(path.join(here, "../firmware/src/main.cpp"), "utf8");
  const macro = main.match(/#define DATA_PINS\(X\)([\s\S]*?)\n\n/)[1];
  const firmwarePins = [...macro.matchAll(/X\((\d+)\)/g)].map((m) => Number(m[1]));
  assert.deepEqual(drivablePins, firmwarePins);
});

test("the profiles the Device refuses are refused here too", () => {
  const rows = fs.readFileSync(path.join(golden, "hardware-profiles-refused.tsv"), "utf8").trimEnd().split("\n");
  assert.ok(rows.length >= 5);
  for (const row of rows) {
    const [why, line] = row.split("\t");
    assert.notDeepEqual(profileProblems(JSON.parse(line)), [], why);
  }
});

test("a profile the Device would refuse is refused before it is flashed", () => {
  const good = presets[0].profile;
  const refused = [
    { width: 0 }, { width: 256 }, { height: 0 }, { width: 8.5 }, { width: "8" },
    { width: 64, height: 64 }, // a Raw Mode frame wouldn't fit the line buffer
    { data_pin: 0 }, { data_pin: 19 }, { data_pin: 46 }, { data_pin: "14" },
    { serpentine: 1 }, { button: "no" }, { imu: null },
    { color_order: "RGBW" }, { color_order: "RRB" }, { color_order: "rgb" },
    { brightness_cap: 0 }, { brightness_cap: 256 },
    { current_cap_ma: 0 }, { current_cap_ma: 65536 },
  ];
  for (const change of refused) {
    const profile = { ...good, ...change };
    assert.notDeepEqual(profileProblems(profile), [], JSON.stringify(change));
    assert.throws(() => profileImage(profile), JSON.stringify(change));
  }
  for (const preset of presets) assert.deepEqual(profileProblems(preset.profile), [], preset.id);
  assert.deepEqual(profileProblems(customExample), []);
});

