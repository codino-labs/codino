// The firmware shell's portable parts, tested natively: line framing of the
// serial link, the LED wiring, the stored Hardware Profile and the flash
// stores of Preferences and Themes. The hardware glue around them
// (device/firmware) is verified by hand on the reference board.
#include <doctest.h>

#include <algorithm>
#include <fstream>
#include <iterator>
#include <map>
#include <string>
#include <vector>

#include <ArduinoJson.h>

#include "flash_store.h"
#include "hardware_profile.h"
#include "led_wiring.h"
#include "line_reader.h"
#include "test_support.h"

namespace {

// Feeds `chunks` one read at a time and returns the lines that came out.
std::vector<std::string> linesFrom(device::LineReader& reader, const std::vector<std::string>& chunks) {
  std::vector<std::string> lines;
  for (const std::string& chunk : chunks) {
    reader.feed(chunk.data(), chunk.size(), [&](std::string_view line) { lines.emplace_back(line); });
  }
  return lines;
}

// A 3x2 frame whose every channel says where its pixel is: R = 10x + 1,
// G = 10y + 2, B = 3.
device::PixelBuffer whereAmIFrame() {
  device::PixelBuffer frame(3, 2);
  for (uint8_t y = 0; y < 2; ++y) {
    for (uint8_t x = 0; x < 3; ++x) frame.set(x, y, {uint8_t(10 * x + 1), uint8_t(10 * y + 2), 3});
  }
  return frame;
}

device::HardwareProfile wiring3x2(bool serpentine, const char* colorOrder) {
  device::HardwareProfile profile;
  profile.width = 3;
  profile.height = 2;
  profile.serpentine = serpentine;
  profile.colorOrder = colorOrder;
  return profile;
}

// The bytes the data line sends for `frame`.
std::vector<uint8_t> sent(const device::HardwareProfile& profile, const device::PixelBuffer& frame) {
  const device::LedWiring wiring(profile);
  std::vector<uint8_t> bytes(3 * wiring.count());
  wiring.write(frame, bytes.data());
  return bytes;
}

}  // namespace

// --- Line framing

TEST_CASE("line reader: lines come out whole however the reads split them") {
  device::LineReader reader;
  const auto lines = linesFrom(reader, {R"({"type":"hel)", R"(lo"}
{"type":"heart)", "beat\"}\n{\"ty"});
  CHECK(lines == std::vector<std::string>{R"({"type":"hello"})", R"({"type":"heartbeat"})"});
}

TEST_CASE("line reader: a Raw Mode frame split across reads comes out byte for byte") {
  // The frame of protocol/fixtures/raw-frame.bin, its pixels \r, NUL and 0xFF
  // included: nothing but the newline is taken out.
  const std::string frame = testsupport::fixture("raw-frame.bin");
  REQUIRE(frame.size() == 1 + 3 * 8 * 8);
  std::string withAwkwardBytes = frame;
  withAwkwardBytes[1] = '\r';
  withAwkwardBytes[2] = '\0';
  withAwkwardBytes[3] = '\xFF';
  withAwkwardBytes[withAwkwardBytes.size() - 1] = '\r';
  const std::string stream = withAwkwardBytes + "\n" + R"({"type":"raw_mode_exit"})" + "\n";

  for (size_t split : {size_t{1}, size_t{7}, size_t{64}, stream.size() - 1}) {
    device::LineReader reader;
    const auto lines = linesFrom(reader, {stream.substr(0, split), stream.substr(split)});
    CHECK(lines == std::vector<std::string>{withAwkwardBytes, R"({"type":"raw_mode_exit"})"});
  }
}

TEST_CASE("line reader: a line longer than the Device's line buffer is dropped up to its newline") {
  // The buffer holds kMaxLineLength bytes, newline included.
  const std::string longest(device::DeviceCore::kMaxLineLength - 1, 'a');
  const std::string tooLong(device::DeviceCore::kMaxLineLength, 'b');
  device::LineReader reader;
  const auto lines = linesFrom(reader, {longest + "\n", tooLong.substr(0, 5000), tooLong.substr(5000) + "tail\n",
                                        R"({"type":"heartbeat"})" "\n"});
  CHECK(lines == std::vector<std::string>{longest, R"({"type":"heartbeat"})"});
}

// --- LED wiring

TEST_CASE("LED wiring: rows run left to right from the top-left LED, in RGB") {
  CHECK(sent(wiring3x2(false, "RGB"), whereAmIFrame()) == std::vector<uint8_t>{
                                                             1, 2, 3, 11, 2, 3, 21, 2, 3,     // row 0
                                                             1, 12, 3, 11, 12, 3, 21, 12, 3,  // row 1
                                                         });
}

TEST_CASE("LED wiring: serpentine wiring runs every other row right to left") {
  CHECK(sent(wiring3x2(true, "RGB"), whereAmIFrame()) == std::vector<uint8_t>{
                                                            1, 2, 3, 11, 2, 3, 21, 2, 3,     // row 0, left to right
                                                            21, 12, 3, 11, 12, 3, 1, 12, 3,  // row 1, right to left
                                                        });
}

TEST_CASE("LED wiring: each LED takes its channels in the build's color order") {
  device::PixelBuffer frame(3, 2);
  frame.set(0, 0, {1, 2, 3});
  const auto firstLed = [&](const char* order) {
    const auto bytes = sent(wiring3x2(false, order), frame);
    return std::vector<uint8_t>(bytes.begin(), bytes.begin() + 3);
  };
  CHECK(firstLed("RGB") == std::vector<uint8_t>{1, 2, 3});
  CHECK(firstLed("GRB") == std::vector<uint8_t>{2, 1, 3});
  CHECK(firstLed("BGR") == std::vector<uint8_t>{3, 2, 1});
  CHECK(firstLed("RBG") == std::vector<uint8_t>{1, 3, 2});
  CHECK(firstLed("GBR") == std::vector<uint8_t>{2, 3, 1});
  CHECK(firstLed("BRG") == std::vector<uint8_t>{3, 1, 2});
}

TEST_CASE("LED wiring: a Device turned by the rotation Preference sends the turned picture down the wiring") {
  // The core turns the picture (rotation 180 here), the wiring lays it on
  // the LEDs: the first LED shows the picture's bottom-right pixel, and on a
  // serpentine build the second row starts from its right end, which turned
  // by 180 is the picture's left end of its second-to-last row.
  device::HardwareProfile profile = testsupport::profile8x8();
  profile.serpentine = true;
  profile.colorOrder = "RGB";
  profile.currentCapMa = 65535;  // no current limit: the frame goes out as the core renders it
  testsupport::FakeClock clock;
  device::DeviceCore core(profile, "0.1.0", clock);
  core.receive(testsupport::preferences(R"("rotation":180)"));
  core.receive(testsupport::fixture("raw-mode-enter.jsonl"));
  core.receive(testsupport::fixture("raw-frame.bin"));  // (x, y) in R = 32x, G = 32y, B = 128

  const auto bytes = sent(profile, core.render());
  CHECK(std::vector<uint8_t>(bytes.begin(), bytes.begin() + 3) == std::vector<uint8_t>{224, 224, 128});  // (7, 7)
  CHECK(std::vector<uint8_t>(bytes.begin() + 3 * 8, bytes.begin() + 3 * 9) ==
        std::vector<uint8_t>{0, 192, 128});  // (0, 6)
}

// --- Current limit
//
// The data line never carries a frame the power model says draws more than
// the Hardware Profile's current_cap_ma: FastLED's model of a 5050 WS2812B,
// 16, 11 and 15 mA for red, green and blue at 255 and 1 mA per LED while dark.

namespace {

device::HardwareProfile panel16x16(uint16_t currentCapMa) {
  device::HardwareProfile profile;
  profile.width = 16;
  profile.height = 16;
  profile.colorOrder = "RGB";
  profile.currentCapMa = currentCapMa;
  return profile;
}

// The model's draw of the LEDs, in mA, for the bytes of an RGB data line.
double drawMa(const std::vector<uint8_t>& rgb) {
  double ma = 0;
  for (size_t i = 0; i < rgb.size(); i += 3) ma += 1 + (rgb[i] * 16 + rgb[i + 1] * 11 + rgb[i + 2] * 15) / 255.0;
  return ma;
}

}  // namespace

TEST_CASE("current limit: a frame inside the current cap goes out as it is") {
  device::PixelBuffer frame(16, 16);
  frame.set(3, 4, {255, 255, 255});  // one white LED: 42 mA, plus 256 dark
  const auto bytes = sent(panel16x16(330), frame);
  CHECK(drawMa(bytes) == doctest::Approx(298));
  CHECK(std::vector<uint8_t>(bytes.begin() + 3 * (4 * 16 + 3), bytes.begin() + 3 * (4 * 16 + 4)) ==
        std::vector<uint8_t>{255, 255, 255});
}

TEST_CASE("current limit: a frame over the current cap is dimmed until it fits, its colors kept") {
  device::PixelBuffer frame(16, 16);
  frame.fill({128, 64, 0});  // an orange Takeover: 946 mA by the model
  const auto bytes = sent(panel16x16(330), frame);
  CHECK(drawMa(bytes) <= 330);
  CHECK(drawMa(bytes) >= 290);  // not dimmer than it has to be
  for (size_t i = 0; i < bytes.size(); i += 3) {
    CAPTURE(i);
    CHECK(std::vector<uint8_t>(bytes.begin() + i, bytes.begin() + i + 3) ==
          std::vector<uint8_t>(bytes.begin(), bytes.begin() + 3));  // still one color
    CHECK(bytes[i] > bytes[i + 1]);                                 // still more red than green
    CHECK(bytes[i + 2] == 0);
  }
}

TEST_CASE("current limit: a dim pixel stays lit while the limit dims a bright frame") {
  device::PixelBuffer frame(16, 16);
  frame.fill({255, 255, 255});
  frame.set(0, 0, {0, 0, 3});
  const auto bytes = sent(panel16x16(330), frame);
  CHECK(drawMa(bytes) <= 330);
  CHECK(std::vector<uint8_t>(bytes.begin(), bytes.begin() + 3) == std::vector<uint8_t>{0, 0, 1});
}

TEST_CASE("current limit: the reference board's Takeover at its brightness cap is not dimmed") {
  // A whole-matrix Takeover in red, as the core caps it (48 of 255): 257 mA
  // by the model, inside the reference build's 300.
  device::PixelBuffer frame(8, 8);
  frame.fill({48, 0, 0});
  const auto bytes = sent(device::referenceHardwareProfile(), frame);
  for (size_t i = 0; i < bytes.size(); i += 3) CHECK(bytes[i] == 48);
}

TEST_CASE("current limit: a cap the dark LEDs alone exceed leaves the matrix dark") {
  device::PixelBuffer frame(16, 16);
  frame.fill({255, 0, 0});
  const auto bytes = sent(panel16x16(200), frame);  // 256 LEDs draw 256 mA dark
  CHECK(std::all_of(bytes.begin(), bytes.end(), [](uint8_t b) { return b == 0; }));
}

// --- Stored Hardware Profile

namespace {

// The hardware_profile object of a Device's hello_reply, as JSON.
std::string reportedProfile(const device::HardwareProfile& profile) {
  testsupport::FakeClock clock;
  device::DeviceCore core(profile, "0.1.0", clock);
  JsonDocument reply;
  REQUIRE_FALSE(deserializeJson(reply, core.receive(testsupport::fixture("hello.jsonl"))));
  std::string out;
  serializeJson(reply["hardware_profile"], out);
  return out;
}

std::string fixtureProfile() {
  JsonDocument reply;
  REQUIRE_FALSE(deserializeJson(reply, testsupport::fixture("hello-reply.jsonl")));
  std::string out;
  serializeJson(reply["hardware_profile"], out);
  return out;
}

}  // namespace

TEST_CASE("stored Hardware Profile: the reference build's is the one hello-reply.jsonl describes") {
  CHECK(reportedProfile(device::referenceHardwareProfile()) == fixtureProfile());
}

TEST_CASE("stored Hardware Profile: what the Device stores reads back as the same profile") {
  device::HardwareProfile custom;
  custom.width = 16;
  custom.height = 16;
  custom.dataPin = 4;
  custom.serpentine = true;
  custom.colorOrder = "GRB";
  custom.brightnessCap = 40;
  custom.currentCapMa = 450;
  custom.button = true;
  custom.imu = false;
  // A flash partition holds the line, then erased bytes.
  const std::string stored = device::hardwareProfileLine(custom) + "\n" + std::string(64, '\xFF');

  device::HardwareProfile read;
  REQUIRE(device::parseHardwareProfile(stored, read));
  CHECK(reportedProfile(read) == reportedProfile(custom));
}

TEST_CASE("stored Hardware Profile: one the Device can't drive is refused and changes nothing") {
  const std::string good = device::hardwareProfileLine(device::referenceHardwareProfile());
  // `good` with one field replaced (or, with an empty value, removed).
  const auto with = [&](const std::string& field, const std::string& value) {
    JsonDocument doc;
    REQUIRE_FALSE(deserializeJson(doc, good));
    if (value.empty()) {
      doc.remove(field);
    } else {
      JsonDocument v;
      REQUIRE_FALSE(deserializeJson(v, value));
      doc[field] = v;
    }
    std::string out;
    serializeJson(doc, out);
    return out;
  };
  const std::string refused[] = {
      std::string(4096, '\xFF'),  // erased flash: nothing stored
      "",
      "[]",
      good.substr(0, good.size() - 1),  // cut short
      with("width", ""), with("height", ""), with("data_pin", ""), with("serpentine", ""),
      with("color_order", ""), with("brightness_cap", ""), with("current_cap_ma", ""), with("button", ""),
      with("imu", ""),
      with("width", "0"), with("height", "0"), with("width", "256"), with("width", "8.5"),
      with("width", "\"8\""),
      // A Raw Mode frame of 64x64 wouldn't fit the line buffer.
      R"({"width":64,"height":64,"data_pin":14,"serpentine":false,"color_order":"RGB","brightness_cap":48,)"
      R"("current_cap_ma":300,"button":false,"imu":true})",
      with("data_pin", "-1"), with("data_pin", "\"14\""),
      with("serpentine", "1"), with("button", "\"no\""), with("imu", "null"),
      with("color_order", "\"RGBW\""), with("color_order", "\"RRB\""), with("color_order", "\"rgb\""),
      with("color_order", "3"),
      with("brightness_cap", "0"), with("brightness_cap", "256"),
      with("current_cap_ma", "0"), with("current_cap_ma", "65536"),
  };
  for (const std::string& stored : refused) {
    CAPTURE(stored.substr(0, 200));
    device::HardwareProfile profile = device::referenceHardwareProfile();
    profile.dataPin = 7;
    CHECK_FALSE(device::parseHardwareProfile(stored, profile));
    CHECK(profile.dataPin == 7);
  }
}

// --- Flash stores: Preferences and Themes across reboots

namespace {

// A filesystem in RAM: what the Device's flash holds, kept across a reboot
// (a new DeviceCore over the same InMemoryFileSystem).
class InMemoryFileSystem : public device::FileSystem {
 public:
  bool read(const std::string& path, std::string& content) override {
    auto it = files.find(path);
    if (it == files.end()) return false;
    content = it->second;
    return true;
  }
  bool write(const std::string& path, std::string_view content) override {
    if (full) return false;
    files[path] = std::string(content);
    return true;
  }
  bool remove(const std::string& path) override { return files.erase(path) == 1; }
  std::vector<std::string> list(const std::string& dir) override {
    std::vector<std::string> names;
    const std::string prefix = dir + "/";
    for (const auto& [path, content] : files) {
      if (path.compare(0, prefix.size(), prefix) == 0 && path.find('/', prefix.size()) == std::string::npos) {
        names.push_back(path.substr(prefix.size()));
      }
    }
    return names;
  }

  std::map<std::string, std::string> files;
  // Every write fails, as on a full flash.
  bool full = false;
};

// A Device booted on `flash`, as the firmware boots: its Preferences cache
// and Theme store in files.
struct BootedDevice {
  explicit BootedDevice(InMemoryFileSystem& flash)
      : cache(flash, "/preferences.jsonl"), store(flash, "/installed"),
        core(testsupport::profile8x8(), "0.1.0", clock, {}, &cache, &store) {}

  testsupport::FakeClock clock;
  device::FilePreferencesCache cache;
  device::FileThemeStore store;
  device::DeviceCore core;
};

JsonDocument parsedLine(const std::string& line) {
  JsonDocument doc;
  REQUIRE_FALSE(deserializeJson(doc, line));
  return doc;
}

}  // namespace

TEST_CASE("flash stores: installed Themes and Sprite Packs are still there after a reboot") {
  InMemoryFileSystem flash;
  {
    BootedDevice device(flash);
    device.core.receive(testsupport::fixture("install-sprite-pack.jsonl"));
    CHECK(parsedLine(device.core.receive(testsupport::fixture("install-theme.jsonl")))["ok"] == true);
  }
  BootedDevice rebooted(flash);
  const JsonDocument reply = parsedLine(rebooted.core.receive(testsupport::fixture("hello.jsonl")));
  CHECK(reply["themes"] == parsedLine(R"(["default","minimal","ember"])").as<JsonVariantConst>());
  CHECK(reply["sprite_packs"] == parsedLine(R"(["nino","dots"])").as<JsonVariantConst>());
}

TEST_CASE("flash stores: the Device boots with the Preferences it was pushed last") {
  InMemoryFileSystem flash;
  {
    BootedDevice device(flash);
    device.core.receive(testsupport::fixture("preferences.jsonl"));  // brightness 50, turned 180
  }
  BootedDevice rebooted(flash);
  CHECK(rebooted.core.preferences().brightness == 50);
  CHECK(rebooted.core.preferences().rotation == 180);
}

TEST_CASE("flash stores: an uninstalled Theme stays gone after a reboot") {
  InMemoryFileSystem flash;
  {
    BootedDevice device(flash);
    device.core.receive(testsupport::fixture("install-sprite-pack.jsonl"));
    device.core.receive(testsupport::fixture("install-theme.jsonl"));
    CHECK(parsedLine(device.core.receive(testsupport::fixture("uninstall.jsonl")))["ok"] == true);
  }
  BootedDevice rebooted(flash);
  const JsonDocument reply = parsedLine(rebooted.core.receive(testsupport::fixture("hello.jsonl")));
  CHECK(reply["themes"] == parsedLine(R"(["default","minimal"])").as<JsonVariantConst>());
}

TEST_CASE("flash stores: only installed files count, not what else a directory holds") {
  InMemoryFileSystem flash;
  {
    BootedDevice device(flash);
    device.core.receive(testsupport::fixture("install-sprite-pack.jsonl"));
  }
  // What a write cut short by a power cut may leave, and a stray file.
  flash.files["/installed/sprite_pack/dots.jsonl.tmp"] = "{\"type\":\"install\"";
  flash.files["/installed/theme/notes.txt"] = "hello";
  BootedDevice rebooted(flash);
  const JsonDocument reply = parsedLine(rebooted.core.receive(testsupport::fixture("hello.jsonl")));
  CHECK(reply["themes"] == parsedLine(R"(["default","minimal"])").as<JsonVariantConst>());
  CHECK(reply["sprite_packs"] == parsedLine(R"(["nino","dots"])").as<JsonVariantConst>());
}

TEST_CASE("flash stores: an install the flash can't hold is refused") {
  InMemoryFileSystem flash;
  flash.full = true;
  BootedDevice device(flash);
  CHECK(parsedLine(device.core.receive(testsupport::fixture("install-sprite-pack.jsonl")))["ok"] == false);
}

// --- The web flasher's Hardware Profile images
//
// The web flasher (device/flasher) writes the chosen Hardware Profile into
// the hwprofile partition. Its encoder (hardware_profile.mjs, tested with
// Node against the same files) and this decoder share golden files, so the
// two can't drift: hardware-profiles.tsv holds the line each preset (and a
// Custom example) is stored as, hwprofile-esp32-s3-matrix.bin the whole 4 KB
// partition image of the preselected one, hardware-profiles-refused.tsv
// profiles both must refuse.

namespace {

// id → stored line, from test/golden/hardware-profiles.tsv.
std::vector<std::pair<std::string, std::string>> goldenProfiles() {
  std::ifstream in(std::string(GOLDEN_DIR) + "/hardware-profiles.tsv");
  REQUIRE(in.good());
  std::vector<std::pair<std::string, std::string>> out;
  for (std::string row; std::getline(in, row);) {
    const size_t tab = row.find('\t');
    REQUIRE(tab != std::string::npos);
    out.emplace_back(row.substr(0, tab), row.substr(tab + 1));
  }
  return out;
}

}  // namespace

TEST_CASE("flasher images: every preset's line is one the Device reads and would store the same way") {
  const auto golden = goldenProfiles();
  REQUIRE(golden.size() >= 4);
  for (const auto& [id, line] : golden) {
    CAPTURE(id);
    device::HardwareProfile profile;
    REQUIRE(device::parseHardwareProfile(line, profile));
    CHECK(device::hardwareProfileLine(profile) == line);
  }
}

TEST_CASE("flasher images: the profiles the flasher refuses are the ones the Device refuses") {
  // hardware-profiles-refused.tsv: why, then the line. Whether the firmware
  // can drive data_pin is the firmware's (canDrive in firmware/src/main.cpp),
  // checked against the flasher's pin list by the Node test.
  std::ifstream in(std::string(GOLDEN_DIR) + "/hardware-profiles-refused.tsv");
  REQUIRE(in.good());
  int rows = 0;
  for (std::string row; std::getline(in, row); ++rows) {
    CAPTURE(row);
    device::HardwareProfile profile;
    CHECK_FALSE(device::parseHardwareProfile(row.substr(row.find('\t') + 1), profile));
  }
  CHECK(rows >= 5);
}

TEST_CASE("flasher images: the preselected preset is the reference build, as its whole partition image") {
  std::ifstream in(std::string(GOLDEN_DIR) + "/hwprofile-esp32-s3-matrix.bin", std::ios::binary);
  const std::string image((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
  REQUIRE(image.size() == 4096);  // the hwprofile partition (firmware/partitions.csv)
  CHECK(goldenProfiles().front() ==
        std::make_pair(std::string("esp32-s3-matrix"), device::hardwareProfileLine(device::referenceHardwareProfile())));

  device::HardwareProfile profile;
  REQUIRE(device::parseHardwareProfile(image, profile));
  CHECK(reportedProfile(profile) == reportedProfile(device::referenceHardwareProfile()));
  // What the firmware writes into a blank partition: the line, its newline,
  // then erased flash.
  const std::string written = device::hardwareProfileLine(device::referenceHardwareProfile()) + "\n";
  CHECK(image == written + std::string(4096 - written.size(), '\xFF'));
}
