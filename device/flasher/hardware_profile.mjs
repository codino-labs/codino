// The Hardware Profile as the web flasher writes it into the Device's
// `hwprofile` partition: one line of JSON with the fields of the
// `hardware_profile` object of a hello_reply (protocol/README.md), in the
// order the firmware writes them (device::hardwareProfileLine), then `\n`,
// then erased flash (0xFF) to the end of the 4 KB partition. The firmware
// reads it at boot with device::parseHardwareProfile; the validation below
// is the same, so the flasher refuses a profile the Device would ignore.
//
// Shared golden files keep this and the C++ side in step:
// device/test/golden/hardware-profiles.tsv and hwprofile-esp32-s3-matrix.bin,
// checked by hardware_profile.test.mjs and device/test/shell_test.cpp.

// Where the partition is and how big (device/firmware/partitions.csv).
export const PROFILE_OFFSET = 0x210000;
export const PARTITION_SIZE = 0x1000;

// The longest protocol line a Device takes, newline included
// (DeviceCore::kMaxLineLength): a Raw Mode frame of the matrix must fit.
const MAX_LINE_LENGTH = 8192;

// The GPIOs the firmware can drive LEDs from (DATA_PINS in
// device/firmware/src/main.cpp): not 0 (BOOT), 19 and 20 (USB), 22-32
// (absent, or the flash and PSRAM), 43 and 44 (UART0), 45 and 46 (strapping).
export const drivablePins = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 21, 33, 34, 35, 36, 37, 38,
  39, 40, 41, 42, 47, 48];

// The presets the flasher offers, the reference board first and
// preselected. The power budget behind the caps is in
// docs/hardware/reference-board.md ("Hardware Profile presets").
export const presets = [
  {
    id: "esp32-s3-matrix",
    label: "ESP32-S3-Matrix, built-in 8×8",
    preselected: true,
    profile: {
      width: 8, height: 8, data_pin: 14, serpentine: false, color_order: "RGB",
      brightness_cap: 48, current_cap_ma: 300, button: false, imu: true,
    },
  },
  {
    id: "esp32-s3-matrix-8x8-panel",
    label: "ESP32-S3-Matrix + external 8×8 panel on GPIO 1",
    profile: {
      width: 8, height: 8, data_pin: 1, serpentine: false, color_order: "GRB",
      brightness_cap: 64, current_cap_ma: 330, button: false, imu: true,
    },
  },
  {
    id: "esp32-s3-matrix-16x16-panel",
    label: "ESP32-S3-Matrix + external 16×16 panel on GPIO 1",
    profile: {
      width: 16, height: 16, data_pin: 1, serpentine: true, color_order: "GRB",
      brightness_cap: 32, current_cap_ma: 330, button: false, imu: true,
    },
  },
];

// The fields in the order the firmware writes them.
const FIELDS = ["width", "height", "data_pin", "serpentine", "color_order", "brightness_cap", "current_cap_ma", "button",
  "imu"];

// Why the Device would refuse `profile` (empty when it wouldn't), in words
// for the page.
export function profileProblems(profile) {
  const problems = [];
  const integer = (field, min, max) => {
    const v = profile[field];
    if (!Number.isInteger(v) || v < min || v > max) problems.push(`${field} must be a whole number from ${min} to ${max}`);
  };
  integer("width", 1, 255);
  integer("height", 1, 255);
  integer("brightness_cap", 1, 255);
  integer("current_cap_ma", 1, 65535);
  if (!drivablePins.includes(profile.data_pin)) problems.push(`data_pin must be one of ${drivablePins.join(", ")}`);
  for (const field of ["serpentine", "button", "imu"]) {
    if (typeof profile[field] !== "boolean") problems.push(`${field} must be true or false`);
  }
  const order = profile.color_order;
  if (typeof order !== "string" || order.length !== 3 || [..."RGB"].some((c) => !order.includes(c))) {
    problems.push("color_order must be R, G and B, each once, e.g. GRB");
  }
  if (Number.isInteger(profile.width) && Number.isInteger(profile.height) &&
      2 + 3 * profile.width * profile.height > MAX_LINE_LENGTH) {
    problems.push(`a ${profile.width}×${profile.height} matrix is too big: width × height must be at most 2730`);
  }
  return problems;
}

// The line the profile is stored as, without its newline.
export function profileLine(profile) {
  const ordered = {};
  for (const field of FIELDS) ordered[field] = profile[field];
  return JSON.stringify(ordered);
}

// The whole partition image for the profile. Throws when the Device would
// refuse it.
export function profileImage(profile) {
  const problems = profileProblems(profile);
  if (problems.length > 0) throw new Error(problems.join("; "));
  const line = new TextEncoder().encode(profileLine(profile) + "\n");
  const image = new Uint8Array(PARTITION_SIZE).fill(0xff);
  image.set(line);
  return image;
}
