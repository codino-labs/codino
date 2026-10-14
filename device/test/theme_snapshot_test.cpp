// Theme snapshot tests: the one place golden frames are allowed (spec,
// Testing Decisions). For each built-in Theme and Session State they record
// what the whole matrix (8x8 and 16x16) and a Tile show over a few seconds, so
// any change to how a Theme looks is seen and reviewed.
//
// The golden files are device/test/golden/<theme>.txt. When a change is meant
// to change how a Theme looks, regenerate them deliberately and review the
// diff:
//
//   make -C device update-goldens
#include <doctest.h>

#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <sstream>
#include <string>

#include "device_core.h"
#include "test_support.h"

using namespace testsupport;

namespace {

// When each frame is taken, in ms after the session is upserted.
constexpr uint32_t kTimes[] = {0, 150, 300, 450, 600, 900, 1200, 1500, 2000, 3000};

std::string hex(device::Rgb c) {
  if (c == device::Rgb{}) return "......";
  char text[7];
  std::snprintf(text, sizeof text, "%02x%02x%02x", c.r, c.g, c.b);
  return text;
}

void print(std::ostream& out, const device::PixelBuffer& frame) {
  for (uint8_t y = 0; y < frame.height(); ++y) {
    for (uint8_t x = 0; x < frame.width(); ++x) out << (x == 0 ? "" : " ") << hex(frame.at(x, y));
    out << '\n';
  }
}

// One shot: the frames of `state` over kTimes. With `tiles`, the session
// takes the top-left of four quadrants, the other three Idle.
void snapshot(std::ostream& out, const std::string& theme, const std::string& state, uint8_t size, bool tiles) {
  device::HardwareProfile profile;
  profile.width = size;
  profile.height = size;
  device::Preferences preferences;
  preferences.theme = theme;
  FakeClock clock;
  device::DeviceCore core(profile, "0.1.0", clock, preferences);
  core.receive(upsertTile("s", state, "#3a86ff", 0));
  if (tiles)
    for (int i = 1; i < 4; ++i) core.receive(upsertTile("idle" + std::to_string(i), "idle", "#ff3da5", i));
  uint32_t elapsed = 0;
  for (uint32_t t : kTimes) {
    clock.advance(t - elapsed);
    elapsed = t;
    core.receive(heartbeat(clock));
    out << "== " << state << ", " << (tiles ? "Tile" : "whole matrix") << ", " << int{size} << "x" << int{size}
        << ", t=" << t << "ms\n";
    print(out, core.render());
  }
}

std::string snapshots(const std::string& theme) {
  std::ostringstream out;
  out << "# Golden frames of the built-in Theme \"" << theme << "\" (device/test/theme_snapshot_test.cpp).\n"
      << "# Each pixel is rrggbb, or ...... when unlit. Regenerate deliberately: make -C device update-goldens\n";
  for (const char* state : kStatesByUrgency) {
    snapshot(out, theme, state, 8, false);
    snapshot(out, theme, state, 16, false);
    snapshot(out, theme, state, 8, true);
  }
  return out.str();
}

void checkGolden(const std::string& theme) {
  const std::string path = std::string(GOLDEN_DIR) + "/" + theme + ".txt";
  const std::string actual = snapshots(theme);
  const char* update = std::getenv("UPDATE_GOLDENS");
  if (update != nullptr && std::string(update) == "1") {
    std::ofstream(path, std::ios::binary | std::ios::trunc) << actual;
    MESSAGE("updated " << path);
    return;
  }
  std::ifstream in(path, std::ios::binary);
  REQUIRE_MESSAGE(in.good(), "missing " << path << ": make -C device update-goldens");
  std::stringstream expected;
  expected << in.rdbuf();
  if (expected.str() == actual) return;
  // Point at the first frame that differs.
  std::istringstream want(expected.str()), got(actual);
  std::string wantLine, gotLine, heading;
  for (int line = 1;; ++line) {
    const bool more = static_cast<bool>(std::getline(want, wantLine));
    if (!std::getline(got, gotLine) && !more) break;
    if (wantLine.rfind("==", 0) == 0) heading = wantLine;
    if (wantLine != gotLine) {
      FAIL_CHECK(path << ":" << line << " differs (" << heading << ")\n want: " << wantLine << "\n  got: " << gotLine
                      << "\nIf the change is meant, run: make -C device update-goldens");
      return;
    }
    wantLine.clear();
    gotLine.clear();
  }
}

}  // namespace

TEST_CASE("snapshot: the default Character Theme (Nino), every Session State, whole matrix and Tile") {
  checkGolden(device::kDefaultTheme);
}

TEST_CASE("snapshot: the abstract minimal Theme, every Session State, whole matrix and Tile") {
  checkGolden(device::kAbstractTheme);
}
