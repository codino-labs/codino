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

// The frame a lone Agent Session in `state` produces.
device::PixelBuffer loneSessionFrame(const std::string& state) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("lone", state));
  return core.render();
}

bool isDark(const device::PixelBuffer& frame) {
  return frame == device::PixelBuffer(frame.width(), frame.height());
}

// A fresh 8x8 Device core with a fake clock, for TEST_CASE_FIXTURE.
struct Device8x8 {
  FakeClock clock;
  device::DeviceCore core{profile8x8(), "0.1.0", clock};
};

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "with no Agent Sessions the whole matrix is dark") {

  const auto& frame = core.render();

  CHECK(frame.width() == 8);
  CHECK(frame.height() == 8);
  CHECK(isDark(frame));
}

TEST_CASE_FIXTURE(Device8x8, "a session upsert lights the whole matrix in one color") {

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

        CHECK(core.render() == loneSessionFrame(more));
      }
    }
  }
}

TEST_CASE_FIXTURE(Device8x8, "an upsert for a known session replaces its Session State") {
  core.receive(upsert("s1", "awaiting_input"));
  core.receive(upsert("s1", "working"));

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "a session remove drops the session from the Aggregate State") {
  core.receive(fixture("session-upsert.jsonl"));

  CHECK(core.receive(fixture("session-remove.jsonl")).empty());

  CHECK(isDark(core.render()));
}

TEST_CASE_FIXTURE(Device8x8, "removing one of two sessions leaves the other's state") {
  core.receive(upsert("s1", "error"));
  core.receive(upsert("s2", "done"));

  core.receive(R"({"type":"session_remove","id":"s1"})");

  CHECK(core.render() == loneSessionFrame("done"));
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

TEST_CASE_FIXTURE(Device8x8, "a heartbeat is accepted without a reply and without changing the matrix") {
  core.receive(upsert("s1", "working"));

  CHECK(core.receive(fixture("heartbeat.jsonl")).empty());

  CHECK(core.render() == loneSessionFrame("working"));
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

    CHECK(core.render() == loneSessionFrame("working"));
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

TEST_CASE_FIXTURE(Device8x8, "a line ending in CRLF is accepted") {

  core.receive(upsert("s1", "error") + "\r");

  CHECK(core.render() == loneSessionFrame("error"));
}

TEST_CASE_FIXTURE(Device8x8, "the solid Aggregate State color holds steady while the Host is alive") {
  core.receive(fixture("session-upsert.jsonl"));
  const device::PixelBuffer before = core.render();

  for (int i = 1; i <= 40; ++i) {
    clock.advance(250);
    if (i % 4 == 0) core.receive(fixture("heartbeat.jsonl"));
    CHECK(core.render() == before);
  }
}

TEST_CASE_FIXTURE(Device8x8, "a hello from any protocol version is answered with the Device's version") {
  for (const std::string hello : {R"({"type":"hello"})", R"({"type":"hello","protocol":7})"}) {
    CAPTURE(hello);
    JsonDocument reply;
    REQUIRE_FALSE(deserializeJson(reply, core.receive(hello)));
    CHECK(reply["type"] == "hello_reply");
    CHECK(reply["protocol"] == 0);
  }
}

// --- Disconnected -----------------------------------------------------------

namespace {

constexpr uint32_t kTimeout = device::DeviceCore::kDefaultDisconnectedTimeoutMs;

// True when at least one pixel is lit and at least one is off: a pattern, not
// a fill, so it can't be mistaken for any Session State's solid color.
bool isSparse(const device::PixelBuffer& frame) {
  bool lit = false, off = false;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) (frame.at(x, y) == device::Rgb{} ? off : lit) = true;
  return lit && off;
}

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "with no heartbeat for the Disconnected timeout the matrix shows Disconnected") {
  core.receive(upsert("s1", "working"));

  clock.advance(kTimeout);
  const auto& frame = core.render();

  CHECK(frame != loneSessionFrame("working"));
  CHECK(isSparse(frame));
}

TEST_CASE_FIXTURE(Device8x8, "heartbeats keep the Device connected well past the Disconnected timeout") {
  core.receive(upsert("s1", "working"));

  for (int i = 0; i < 10; ++i) {
    clock.advance(kTimeout - 1);
    core.receive(fixture("heartbeat.jsonl"));
  }

  CHECK(core.render() == loneSessionFrame("working"));
}

namespace {

// The frame after the Host went silent with the given sessions on the Device.
device::PixelBuffer disconnectedFrame(const std::vector<std::string>& states) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  for (size_t i = 0; i < states.size(); ++i) core.receive(upsert("s" + std::to_string(i), states[i]));
  clock.advance(kTimeout);
  return core.render();
}

// True when no channel of any pixel is above 1/8 of full brightness.
bool isDim(const device::PixelBuffer& frame) {
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) {
      const device::Rgb c = frame.at(x, y);
      if (c.r > 32 || c.g > 32 || c.b > 32) return false;
    }
  return true;
}

}  // namespace

TEST_CASE("Disconnected is dim and looks like no Session State, Idle included") {
  const device::PixelBuffer disconnected = disconnectedFrame({});

  CHECK(isDim(disconnected));
  CHECK_FALSE(isDark(disconnected));
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    CHECK(disconnected != loneSessionFrame(state));
  }
}

TEST_CASE("Disconnected hides the sessions: the Device doesn't know their state any more") {
  const device::PixelBuffer none = disconnectedFrame({});

  CHECK(disconnectedFrame({"idle"}) == none);
  CHECK(disconnectedFrame({"awaiting_input", "working"}) == none);
}

TEST_CASE("a Device that never heard from the Host shows Disconnected after the timeout") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);

  clock.advance(kTimeout - 1);
  CHECK(isDark(core.render()));
  clock.advance(1);
  CHECK(core.render() == disconnectedFrame({}));
}

TEST_CASE("the next heartbeat after Disconnected brings back the sessions") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("s1", "done"));
  clock.advance(3 * kTimeout);
  REQUIRE(core.render() == disconnectedFrame({}));

  core.receive(fixture("heartbeat.jsonl"));

  CHECK(core.render() == loneSessionFrame("done"));
}

TEST_CASE("lines the Device cannot use don't count as hearing from the Host") {
  for (const std::string line : {"", "not json", R"({"type":"from_the_future"})",
                                 R"({"type":"session_upsert","id":"s2","state":"sleeping"})",
                                 R"({"type":"session_remove"})"}) {
    CAPTURE(line);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock);
    core.receive(upsert("s1", "working"));
    clock.advance(kTimeout - 1);

    core.receive(line);
    clock.advance(1);

    CHECK(core.render() == disconnectedFrame({}));
  }
}

TEST_CASE("Disconnected survives the millisecond clock wrapping around") {
  FakeClock clock;
  clock.now = UINT32_MAX - 1000;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("s1", "working"));

  clock.advance(2000);  // wraps
  CHECK(core.render() == loneSessionFrame("working"));
  clock.advance(kTimeout);
  CHECK(core.render() == disconnectedFrame({}));
}

TEST_CASE_FIXTURE(Device8x8, "a hello forgets the sessions of the previous Host Daemon") {
  core.receive(upsert("old", "awaiting_input"));
  clock.advance(3 * kTimeout);  // the Host Daemon exited

  core.receive(fixture("hello.jsonl"));  // a new one starts
  core.receive(upsert("new", "working"));

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "Disconnected holds however long the silence, even past a clock wrap") {
  core.receive(upsert("s1", "working"));
  clock.advance(kTimeout);
  REQUIRE(core.render() == disconnectedFrame({}));

  clock.advance(UINT32_MAX - kTimeout + 2);  // 2^32 + 1 ms after the last message: millis() is just past it

  CHECK(core.render() == disconnectedFrame({}));
}
