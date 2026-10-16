// Firmware shell: the Device core on the reference board (Waveshare
// ESP32-S3-Matrix), or any ESP32-S3 build a Hardware Profile describes.
//
// The shell's own logic is portable and tested natively (../shell); this
// file is the hardware glue around it, verified by hand on the board
// (docs/hardware/reference-board.md):
// - USB CDC serial through TinyUSB, with our product string: protocol lines
//   in, replies out.
// - The Hardware Profile from its flash partition, the reference one when it
//   is blank or holds one the firmware can't drive.
// - Preferences and installed Themes and Sprite Packs in LittleFS.
// - The LEDs through FastLED, the Device core's frame laid on the wiring
//   under the current cap.
// - The BOOT button and the QMI8658's double-tap Acknowledge; the QMI8658's
//   orientation turns the picture under auto rotation. Each only when the
//   Hardware Profile has it.
#include <Arduino.h>
#include <FastLED.h>
#include <LittleFS.h>
#include <USB.h>
#include <Wire.h>
#include <esp_partition.h>

#include <memory>
#include <vector>

#include "button.h"
#include "device_core.h"
#include "flash_store.h"
#include "hardware_profile.h"
#include "imu_gestures.h"
#include "led_wiring.h"
#include "line_reader.h"
#include "littlefs_file_system.h"

namespace {

class ArduinoClock : public device::Clock {
 public:
  uint32_t millis() const override { return ::millis(); }
};

// Of the data partition holding the Hardware Profile (partitions.csv).
constexpr esp_partition_subtype_t kHardwareProfileSubtype = static_cast<esp_partition_subtype_t>(0x40);
constexpr const char* kHardwareProfileLabel = "hwprofile";
constexpr const char* kLittleFsLabel = "littlefs";

// How often the Device renders without input, so time-driven frames
// (Escalation, Disconnected, animations) show.
constexpr uint32_t kFrameIntervalMs = 10;

USBCDC usbSerial;
ArduinoClock deviceClock;
device::LittleFsFileSystem files;
std::unique_ptr<device::FilePreferencesCache> preferencesCache;
std::unique_ptr<device::FileThemeStore> themeStore;
std::unique_ptr<device::DeviceCore> core;
std::unique_ptr<device::LedWiring> wiring;
device::LineReader lineReader;
std::vector<CRGB> leds;
uint32_t lastRender = 0;

// The BOOT button: GPIO 0, low while pressed (a strapping pin, read only
// once the firmware runs).
constexpr int kButtonPin = 0;
bool hasButton = false;
device::Button button;

// The QMI8658 on I2C (SDA 11, SCL 12), at whichever of its two addresses
// answers: the accelerometer alone, at 224 Hz and +-8 g (a tap can pass 4 g),
// read every kImuSampleMs.
constexpr int kImuSda = 11, kImuScl = 12;
constexpr uint8_t kImuAddresses[] = {0x6B, 0x6A};
constexpr uint8_t kImuWhoAmI = 0x00, kImuCtrl1 = 0x02, kImuCtrl2 = 0x03, kImuCtrl7 = 0x08, kImuAxL = 0x35,
                  kImuReset = 0x60;
constexpr uint8_t kImuChipId = 0x05;
constexpr int kImuLsbPerG = 4096;  // at +-8 g
constexpr uint32_t kImuSampleMs = 5;
uint8_t imuAddress = 0;  // 0: no IMU answered
uint32_t lastImuSample = 0;
device::TapDetector taps;
device::OrientationSensor orientation;

// FastLED takes the data pin as a template argument: one case per GPIO of the
// ESP32-S3 a build may wire its LEDs to. Left out: 0 (the BOOT button), 19
// and 20 (USB), 22-32 (none, or the flash and PSRAM), 43 and 44 (UART0, which
// prints the boot log), 45 and 46 (strapping). Channels go out as FastLED's
// RGB; LedWiring has already put them in the build's color order.
#define DATA_PINS(X)                                                                                              \
  X(1) X(2) X(3) X(4) X(5) X(6) X(7) X(8) X(9) X(10) X(11) X(12) X(13) X(14) X(15) X(16) X(17) X(18) X(21) X(33) \
  X(34) X(35) X(36) X(37) X(38) X(39) X(40) X(41) X(42) X(47) X(48)

bool canDrive(int pin) {
  switch (pin) {
#define CASE(p) case p:
    DATA_PINS(CASE)
#undef CASE
    return true;
    default:
      return false;
  }
}

void addLeds(int pin, CRGB* data, int count) {
  switch (pin) {
#define CASE(p)                                         \
  case p:                                               \
    FastLED.addLeds<WS2812B, p, RGB>(data, count);      \
    break;
    DATA_PINS(CASE)
#undef CASE
    default:
      break;
  }
}

// The Hardware Profile stored on the Device; the reference one when the
// partition is blank (stored there, so it reads as chosen next time) or
// holds one the firmware can't drive (left as it is, for whoever wrote it).
device::HardwareProfile loadHardwareProfile() {
  const device::HardwareProfile reference = device::referenceHardwareProfile();
  const esp_partition_t* partition =
      esp_partition_find_first(ESP_PARTITION_TYPE_DATA, kHardwareProfileSubtype, kHardwareProfileLabel);
  if (partition == nullptr) return reference;
  std::string stored(partition->size, '\xFF');
  if (esp_partition_read(partition, 0, &stored[0], stored.size()) != ESP_OK) return reference;

  device::HardwareProfile profile;
  if (device::parseHardwareProfile(stored, profile) && canDrive(profile.dataPin)) return profile;
  if (stored.find_first_not_of('\xFF') == std::string::npos) {
    const std::string line = device::hardwareProfileLine(reference) + "\n";
    if (esp_partition_erase_range(partition, 0, partition->size) == ESP_OK) {
      esp_partition_write(partition, 0, line.data(), line.size());
    }
  }
  return reference;
}

void send(const std::string& line) {
  if (line.empty()) return;
  usbSerial.write(reinterpret_cast<const uint8_t*>(line.data()), line.size());
  usbSerial.write('\n');
}

bool imuWrite(uint8_t reg, uint8_t value) {
  Wire.beginTransmission(imuAddress);
  Wire.write(reg);
  Wire.write(value);
  return Wire.endTransmission() == 0;
}

bool imuRead(uint8_t reg, uint8_t* out, size_t count) {
  Wire.beginTransmission(imuAddress);
  Wire.write(reg);
  if (Wire.endTransmission(false) != 0) return false;
  if (Wire.requestFrom(imuAddress, static_cast<uint8_t>(count)) != count) return false;
  for (size_t i = 0; i < count; ++i) out[i] = Wire.read();
  return true;
}

// Finds the QMI8658 and starts its accelerometer; imuAddress stays 0 when
// none answers, and the Device goes without double-tap and auto-rotation.
void startImu() {
  Wire.begin(kImuSda, kImuScl, 400000);
  for (uint8_t address : kImuAddresses) {
    imuAddress = address;
    uint8_t id = 0;
    if (imuRead(kImuWhoAmI, &id, 1) && id == kImuChipId) break;
    imuAddress = 0;
  }
  if (imuAddress == 0) return;
  imuWrite(kImuReset, 0xB0);
  delay(20);
  if (!imuWrite(kImuCtrl1, 0x40) ||                // address auto-increment, little endian
      !imuWrite(kImuCtrl2, (0x02 << 4) | 0x05) ||  // +-8 g, 224 Hz
      !imuWrite(kImuCtrl7, 0x01)) {                // the accelerometer only
    imuAddress = 0;
  }
}

// One accelerometer sample through the gestures. The QMI8658 lies turned on
// the reference board: the matrix's right is its +y and down its -x
// (measured on the board, docs/hardware/reference-board.md, step 15).
void sampleImu(uint32_t now) {
  uint8_t raw[6];
  if (!imuRead(kImuAxL, raw, sizeof raw)) return;
  auto mg = [&](int i) { return static_cast<int16_t>(raw[i] | raw[i + 1] << 8) * 1000 / kImuLsbPerG; };
  const int ax = mg(0), ay = mg(2), az = mg(4);
  if (taps.update(ax, ay, az, now)) send(core->acknowledge());
  if (orientation.update(ay, -ax, now)) send(core->orient(orientation.rotation()));
}

void show(const device::PixelBuffer& frame) {
  wiring->write(frame, reinterpret_cast<uint8_t*>(leds.data()));
  FastLED.show();
}

}  // namespace

void setup() {
  device::HardwareProfile profile = loadHardwareProfile();

  // The LEDs first, dark, so whatever the matrix held at reset goes away.
  wiring = std::make_unique<device::LedWiring>(profile);
  leds.assign(wiring->count(), CRGB::Black);
  addLeds(profile.dataPin, leds.data(), static_cast<int>(leds.size()));
  // The core keeps every channel under the brightness cap, and LedWiring
  // dims a frame whose estimated draw exceeds the profile's current_cap_ma:
  // FastLED's own brightness and power limiter stay out of it.
  FastLED.setBrightness(255);
  FastLED.setDither(DISABLE_DITHER);
  FastLED.show();

  // Formats the partition when it holds no LittleFS yet (the first boot).
  // Without one the Device still works, keeping Preferences and Themes in
  // RAM until it reboots.
  if (LittleFS.begin(true, "/littlefs", 4, kLittleFsLabel)) {
    preferencesCache = std::make_unique<device::FilePreferencesCache>(files, "/preferences.jsonl");
    themeStore = std::make_unique<device::FileThemeStore>(files, "/installed");
  }
  if (profile.button) {
    pinMode(kButtonPin, INPUT_PULLUP);
    hasButton = true;
  }
  // An IMU that doesn't answer is none: the hello_reply says so, and auto
  // rotation stays unturned.
  if (profile.imu) startImu();
  profile.imu = imuAddress != 0;
  core = std::make_unique<device::DeviceCore>(profile, FIRMWARE_VERSION, deviceClock, device::Preferences{},
                                              preferencesCache.get(), themeStore.get());

  // A whole install line fits in the receive queue while the loop is busy.
  usbSerial.setRxBufferSize(2 * device::DeviceCore::kMaxLineLength);
  // No reboot into the ROM bootloader on a DTR/RTS sequence or 1200 baud: a
  // Host opening and closing the port must never strand the Device in
  // download mode. To flash it again, hold BOOT and press RESET.
  usbSerial.enableReboot(false);
  usbSerial.begin();
  // The board variant's USB_PID is 0x1001, the ROM's USB-Serial/JTAG in
  // download mode: same VID, PID and serial number (the MAC) with other
  // descriptors, which Windows caches per VID/PID/serial. The Arduino core's
  // TinyUSB default (0x0002) tells the running Device apart.
  USB.PID(0x0002);
  USB.begin();
}

void loop() {
  uint8_t chunk[256];
  while (int available = usbSerial.available()) {
    const size_t got = usbSerial.read(chunk, available < static_cast<int>(sizeof chunk) ? available : sizeof chunk);
    if (got == 0) break;
    lineReader.feed(reinterpret_cast<const char*>(chunk), got,
                    [](std::string_view line) { send(core->receive(line)); });
  }

  const uint32_t now = millis();
  if (hasButton && button.update(digitalRead(kButtonPin) == LOW, now)) send(core->acknowledge());
  if (imuAddress != 0 && now - lastImuSample >= kImuSampleMs) {
    lastImuSample = now;
    sampleImu(now);
  }
  if (now - lastRender >= kFrameIntervalMs) {
    lastRender = now;
    static device::PixelBuffer shown(0, 0);
    const device::PixelBuffer& frame = core->render();
    if (frame != shown) {
      shown = frame;
      show(frame);
    }
  }
  delay(1);
}
