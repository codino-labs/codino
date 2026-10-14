// Device core tests at its seam: protocol messages and clock ticks in, pixel
// buffer out. Assertions are semantic ("the whole matrix is one color"), not
// golden frames.
#include <doctest.h>

#include <ArduinoJson.h>

#include <fstream>
#include <string>
#include <vector>

#include "device_core.h"

namespace {

class FakeClock : public device::Clock {
 public:
  uint32_t millis() const override { return now; }
  void advance(uint32_t ms) { now += ms; }
  uint32_t now = 1000;
};

device::HardwareProfile profile8x8() {
  device::HardwareProfile p;
  p.width = 8;
  p.height = 8;
  return p;
}

// Reads the single message of a protocol fixture, without its newline.
std::string fixture(const std::string& name) {
  std::ifstream in(std::string(PROTOCOL_FIXTURES_DIR) + "/" + name);
  REQUIRE_MESSAGE(in.good(), "missing fixture " << name);
  std::string line;
  std::getline(in, line);
  return line;
}

// True when every pixel has the same color and that color is not off.
bool isSolidLit(const device::PixelBuffer& frame) {
  const device::Rgb first = frame.at(0, 0);
  if (first == device::Rgb{}) return false;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x)
      if (frame.at(x, y) != first) return false;
  return true;
}

std::string upsert(const std::string& id, const std::string& state) {
  return R"({"type":"session_upsert","id":")" + id + R"(","state":")" + state +
         R"(","project_color":"#3a86ff","state_entered":1760432395000,"tile":0})";
}

// The wire names of the Session States, most urgent first.
const char* const kStatesByUrgency[] = {"awaiting_input", "error", "done", "working", "idle"};

std::vector<device::Rgb> pixelsOf(const device::PixelBuffer& frame) {
  std::vector<device::Rgb> pixels;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) pixels.push_back(frame.at(x, y));
  return pixels;
}

// The frame a lone Agent Session in `state` produces.
std::vector<device::Rgb> loneSessionFrame(const std::string& state) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("lone", state));
  return pixelsOf(core.render());
}

bool isDark(const device::PixelBuffer& frame) {
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x)
      if (frame.at(x, y) != device::Rgb{}) return false;
  return true;
}

}  // namespace

TEST_CASE("with no Agent Sessions the whole matrix is dark") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);

  const auto& frame = core.render();

  CHECK(frame.width() == 8);
  CHECK(frame.height() == 8);
  CHECK(isDark(frame));
}

TEST_CASE("a session upsert lights the whole matrix in one color") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);

  CHECK(core.receive(fixture("session-upsert.jsonl")).empty());

  CHECK(isSolidLit(core.render()));
}

TEST_CASE("every Session State fills the matrix with its own color") {
  std::vector<device::Rgb> seen;
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock);
    core.receive(upsert("s1", state));

    const auto& frame = core.render();

    REQUIRE(isSolidLit(frame));
    for (device::Rgb other : seen) CHECK(frame.at(0, 0) != other);
    seen.push_back(frame.at(0, 0));
  }
}

TEST_CASE("the Aggregate State is the most urgent Session State") {
  for (size_t urgent = 0; urgent < 5; ++urgent) {
    for (size_t calmer = urgent + 1; calmer < 5; ++calmer) {
      const std::string more = kStatesByUrgency[urgent];
      const std::string less = kStatesByUrgency[calmer];
      CAPTURE(more);
      CAPTURE(less);
      // Both arrival orders, and ids that sort either way.
      for (const auto& [first, second] : {std::pair{"a", "b"}, std::pair{"b", "a"}}) {
        FakeClock clock;
        device::DeviceCore core(profile8x8(), "0.1.0", clock);
        core.receive(upsert(first, less));
        core.receive(upsert(second, more));

        CHECK(pixelsOf(core.render()) == loneSessionFrame(more));
      }
    }
  }
}

TEST_CASE("an upsert for a known session replaces its Session State") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("s1", "awaiting_input"));
  core.receive(upsert("s1", "working"));

  CHECK(pixelsOf(core.render()) == loneSessionFrame("working"));
}

TEST_CASE("a session remove drops the session from the Aggregate State") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(fixture("session-upsert.jsonl"));

  CHECK(core.receive(fixture("session-remove.jsonl")).empty());

  CHECK(isDark(core.render()));
}

TEST_CASE("removing one of two sessions leaves the other's state") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("s1", "error"));
  core.receive(upsert("s2", "done"));

  core.receive(R"({"type":"session_remove","id":"s1"})");

  CHECK(pixelsOf(core.render()) == loneSessionFrame("done"));
}

TEST_CASE("hello gets a reply with protocol version, firmware version and Hardware Profile") {
  // The reference build the hello-reply fixture describes.
  device::HardwareProfile reference;
  reference.width = 8;
  reference.height = 8;
  reference.dataPin = 14;
  reference.serpentine = false;
  reference.colorOrder = "RGB";
  reference.brightnessCap = 48;
  reference.currentCapMa = 300;
  reference.button = false;
  reference.imu = true;
  FakeClock clock;
  device::DeviceCore core(reference, "0.1.0", clock);

  const std::string reply = core.receive(fixture("hello.jsonl"));

  JsonDocument actual, expected;
  REQUIRE_FALSE(deserializeJson(actual, reply));
  REQUIRE_FALSE(deserializeJson(expected, fixture("hello-reply.jsonl")));
  CHECK_MESSAGE(actual == expected, "reply: " << reply);
}

TEST_CASE("a heartbeat is accepted without a reply and without changing the matrix") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("s1", "working"));

  CHECK(core.receive(fixture("heartbeat.jsonl")).empty());

  CHECK(pixelsOf(core.render()) == loneSessionFrame("working"));
}

TEST_CASE("messages the Device cannot use are ignored as a whole") {
  const char* const ignored[] = {
      "",
      "not json",
      R"({"type":"session_upsert","id":"s2","state":"awaiting)",  // truncated line
      R"(["session_upsert"])",
      R"({"id":"s2","state":"awaiting_input"})",
      R"({"type":"from_the_future","id":"s2","state":"awaiting_input"})",
      R"({"type":"session_upsert","id":"s2","state":"sleeping"})",
      R"({"type":"session_upsert","id":"s2"})",
      R"({"type":"session_upsert","state":"awaiting_input"})",
      R"({"type":"session_upsert","id":"","state":"awaiting_input"})",
      R"({"type":"session_upsert","id":42,"state":"awaiting_input"})",
      R"({"type":"session_remove"})",
  };
  for (const std::string line : ignored) {
    CAPTURE(line);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock);
    core.receive(upsert("s1", "working"));

    CHECK(core.receive(line).empty());

    CHECK(pixelsOf(core.render()) == loneSessionFrame("working"));
  }
}

TEST_CASE("the Hardware Profile sets the matrix size") {
  device::HardwareProfile panel16;
  panel16.width = 16;
  panel16.height = 16;
  FakeClock clock;
  device::DeviceCore core(panel16, "0.1.0", clock);
  core.receive(fixture("session-upsert.jsonl"));

  const auto& frame = core.render();

  CHECK(frame.width() == 16);
  CHECK(frame.height() == 16);
  CHECK(isSolidLit(frame));
}

TEST_CASE("a line ending in CRLF is accepted") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);

  core.receive(upsert("s1", "error") + "\r");

  CHECK(pixelsOf(core.render()) == loneSessionFrame("error"));
}

TEST_CASE("the solid Aggregate State color holds steady as time passes") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(fixture("session-upsert.jsonl"));
  const auto before = pixelsOf(core.render());

  for (int i = 0; i < 20; ++i) {
    clock.advance(250);
    CHECK(pixelsOf(core.render()) == before);
  }
}
