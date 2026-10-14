// Device core tests at its seam: protocol messages and clock ticks in, pixel
// buffer out. Assertions are semantic ("the whole matrix is one color"), not
// golden frames (those are for the Theme snapshots, theme_snapshot_test.cpp).
//
// These tests render with the abstract Theme ("minimal"), whose effects fill
// the matrix or a Tile, so a fill reads as a Session State; the Character
// Theme is covered in theme_test.cpp.
#include <doctest.h>

#include <ArduinoJson.h>

#include <algorithm>
#include <cstdlib>
#include <fstream>
#include <functional>
#include <string>
#include <vector>

#include "device_core.h"
#include "test_support.h"

using namespace testsupport;

namespace {

// Preferences with the abstract Theme, which the tests in this file render by.
device::Preferences abstractTheme() {
  device::Preferences p;
  p.theme = device::kAbstractTheme;
  return p;
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

// The frame a lone Agent Session in `state` produces.
device::PixelBuffer loneSessionFrame(const std::string& state) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsert("lone", state));
  return core.render();
}

// A fresh 8x8 Device core with a fake clock, for TEST_CASE_FIXTURE.
struct Device8x8 {
  FakeClock clock;
  device::DeviceCore core{profile8x8(), "0.1.0", clock, abstractTheme()};
};

// Preferences with the Aggregate Layout: the whole matrix shows the Aggregate
// State however many sessions there are.
device::Preferences aggregateLayout() {
  device::Preferences p = abstractTheme();
  p.layout = device::Layout::Aggregate;
  return p;
}

// A fresh 8x8 Device core in the Aggregate Layout.
struct AggregateDevice8x8 {
  FakeClock clock;
  device::DeviceCore core{profile8x8(), "0.1.0", clock, aggregateLayout()};
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

TEST_CASE("every Session State lights the whole matrix in its own color") {
  std::vector<device::Rgb> seen;
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(upsert("s1", state));

    const auto& frame = core.render();

    for (uint8_t y = 0; y < 8; ++y)
      for (uint8_t x = 0; x < 8; ++x) REQUIRE(frame.at(x, y) != device::Rgb{});
    for (device::Rgb other : seen) CHECK(frame.at(0, 0) != other);
    seen.push_back(frame.at(0, 0));
  }
}

TEST_CASE("in the Aggregate Layout the matrix shows the most urgent Session State") {
  for (size_t urgent = 0; urgent < 5; ++urgent) {
    for (size_t calmer = urgent + 1; calmer < 5; ++calmer) {
      const std::string more = kStatesByUrgency[urgent];
      const std::string less = kStatesByUrgency[calmer];
      CAPTURE(more);
      CAPTURE(less);
      // Both arrival orders, and ids that sort either way.
      for (const auto& [first, second] : {std::pair{"a", "b"}, std::pair{"b", "a"}}) {
        FakeClock clock;
        device::DeviceCore core(profile8x8(), "0.1.0", clock, aggregateLayout());
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
  device::DeviceCore core(reference, "0.1.0", clock, abstractTheme());

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
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
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
  device::DeviceCore core(panel16, "0.1.0", clock, abstractTheme());
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

TEST_CASE("the abstract Theme's Idle and Done hold steady while the Host is alive") {
  for (const char* state : {"done", "idle"}) {
    CAPTURE(state);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(upsert("s1", state));
    const device::PixelBuffer before = core.render();

    for (int i = 1; i <= 40; ++i) {
      clock.advance(250);
      if (i % 4 == 0) core.receive(fixture("heartbeat.jsonl"));
      CHECK(core.render() == before);
    }
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

constexpr uint32_t kTimeout = device::kDefaultDisconnectedTimeoutMs;

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
  core.receive(upsert("s1", "done"));

  for (int i = 0; i < 10; ++i) {
    clock.advance(kTimeout - 1);
    core.receive(fixture("heartbeat.jsonl"));
  }

  CHECK(core.render() == loneSessionFrame("done"));
}

namespace {

// The frame after the Host went silent with the given sessions on the Device.
device::PixelBuffer disconnectedFrame(const std::vector<std::string>& states) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
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
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());

  clock.advance(kTimeout - 1);
  CHECK(isDark(core.render()));
  clock.advance(1);
  CHECK(core.render() == disconnectedFrame({}));
}

TEST_CASE("the next heartbeat after Disconnected brings back the sessions") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsert("s1", "done"));
  clock.advance(3 * kTimeout);
  REQUIRE(core.render() == disconnectedFrame({}));

  core.receive(fixture("heartbeat.jsonl"));

  CHECK(core.render() == loneSessionFrame("done"));
}

TEST_CASE("lines the Device cannot use don't count as hearing from the Host") {
  for (const std::string line : {"", "not json", R"({"type":"from_the_future"})",
                                 R"({"type":"session_upsert","id":"s2","state":"sleeping"})",
                                 R"({"type":"session_remove"})", R"({"type":"acknowledge","id":42})"}) {
    CAPTURE(line);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
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
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsert("s1", "done"));

  clock.advance(2000);  // wraps
  CHECK(core.render() == loneSessionFrame("done"));
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

// --- Acknowledge ------------------------------------------------------------

TEST_CASE_FIXTURE(AggregateDevice8x8, "an Acknowledge on the Device tells the Host and clears every Done session") {
  core.receive(upsert("s1", "done"));
  core.receive(upsert("s2", "done"));
  core.receive(upsert("s3", "working"));

  const std::string toHost = core.acknowledge();

  JsonDocument actual, expected;
  REQUIRE_FALSE(deserializeJson(actual, toHost));
  REQUIRE_FALSE(deserializeJson(expected, fixture("device-acknowledge.jsonl")));
  CHECK_MESSAGE(actual == expected, "sent: " << toHost);
  CHECK(core.render() == loneSessionFrame("working"));
}

// --- Escalation -------------------------------------------------------------

namespace {

constexpr uint32_t kSecond = 1000;
constexpr uint32_t kMinute = 60 * kSecond;

// The brightest channel of a solid frame: how bright the matrix is.
int brightness(const device::PixelBuffer& frame) {
  const device::Rgb c = frame.at(0, 0);
  return std::max({c.r, c.g, c.b});
}

// What the matrix did while the clock ran for `ms`, sampled every 20 ms, with
// the Host's heartbeat every second.
struct Watched {
  bool alwaysSolidLit = true;
  int dimmest = 255, brightest = 0;
  // Times the brightness turned from rising to falling: pulses seen.
  int peaks = 0;
  std::vector<device::Rgb> colors;  // each distinct color, in order of appearance
};

Watched watch(device::DeviceCore& core, FakeClock& clock, uint32_t ms) {
  Watched w;
  int previous = -1;
  bool rising = false;
  for (uint32_t t = 0; t < ms; t += 20, clock.advance(20)) {
    if (t % kSecond == 0) core.receive(heartbeat(clock));
    const device::PixelBuffer& frame = core.render();
    w.alwaysSolidLit = w.alwaysSolidLit && isSolidLit(frame);
    const int level = brightness(frame);
    w.dimmest = std::min(w.dimmest, level);
    w.brightest = std::max(w.brightest, level);
    if (previous >= 0 && level < previous && rising) ++w.peaks;
    if (previous >= 0 && level != previous) rising = level > previous;
    previous = level;
    if (std::find(w.colors.begin(), w.colors.end(), frame.at(0, 0)) == w.colors.end())
      w.colors.push_back(frame.at(0, 0));
  }
  return w;
}

// True when `color` and `base` are the same hue at any brightness (so the
// same Session State), allowing for each channel being rounded down by 1.
bool sameHue(device::Rgb color, device::Rgb base) {
  const int k = std::max({base.r, base.g, base.b});
  const int level = std::max({color.r, color.g, color.b});
  auto near = [&](int c, int b) { return std::abs(c * k - b * level) <= k + level; };
  return near(color.r, base.r) && near(color.g, base.g) && near(color.b, base.b);
}

// The steady Awaiting Input color of v0, the reference hue for its pulses.
device::Rgb awaitingInputHue() {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsert("s", "awaiting_input"));
  return core.render().at(0, 0);
}

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "Awaiting Input starts as a gentle pulse of the whole matrix") {
  core.receive(upsert("s1", "awaiting_input"));

  const Watched w = watch(core, clock, 29 * kSecond);

  CHECK(w.alwaysSolidLit);
  CHECK(w.peaks >= 5);
  CHECK(w.peaks <= 30);  // gentle: about one pulse a second or slower
  CHECK(w.brightest - w.dimmest >= 40);
  for (device::Rgb c : w.colors) CHECK(sameHue(c, awaitingInputHue()));
}

TEST_CASE("after 30 s ignored Awaiting Input pulses faster and brighter") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsert("s1", "awaiting_input"));
  const Watched gentle = watch(core, clock, 10 * kSecond);
  clock.advance(20 * kSecond);
  core.receive(heartbeat(clock));

  const Watched faster = watch(core, clock, 10 * kSecond);

  CHECK(faster.alwaysSolidLit);
  CHECK(faster.peaks > gentle.peaks);
  CHECK(faster.brightest > gentle.brightest);
  CHECK(faster.dimmest >= gentle.dimmest);
  for (device::Rgb c : faster.colors) CHECK(sameHue(c, awaitingInputHue()));
  // Still escalating, not yet a Takeover: it holds until 2 min.
  clock.advance(2 * kMinute - 40 * kSecond - 1);
  core.receive(heartbeat(clock));
  CHECK(sameHue(core.render().at(0, 0), awaitingInputHue()));
}

namespace {

// Moves the clock on by `ms`, the Host's heartbeat keeping the link alive.
void advanceWithHeartbeats(device::DeviceCore& core, FakeClock& clock, uint32_t ms) {
  for (; ms >= kSecond; ms -= kSecond) {
    clock.advance(kSecond);
    core.receive(heartbeat(clock));
  }
  clock.advance(ms);
}

constexpr device::Rgb kGreen{0x2a, 0xd1, 0x4f};
constexpr device::Rgb kPink{0xff, 0x3d, 0xa5};

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "after 2 min ignored the whole matrix is taken over in the Project Color of the session Awaiting Input") {
  core.receive(upsert("asking", "awaiting_input", "#2ad14f"));
  core.receive(upsert("busy", "working", "#ff3da5"));
  core.receive(upsert("broken", "error", "#ff3da5"));

  advanceWithHeartbeats(core, clock, 2 * kMinute);
  const Watched w = watch(core, clock, 5 * kSecond);

  CHECK(w.alwaysSolidLit);
  REQUIRE(w.colors.size() == 1);
  CHECK(w.colors[0] == kGreen);
}

TEST_CASE_FIXTURE(Device8x8, "with several sessions in Takeover the matrix cycles between their Project Colors about once a second") {
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  core.receive(upsert("b", "awaiting_input", "#ff3da5"));
  advanceWithHeartbeats(core, clock, 3 * kMinute);

  // How long each color holds, in 20 ms samples.
  std::vector<std::pair<device::Rgb, int>> runs;
  for (int i = 0; i < 400; ++i, clock.advance(20)) {
    if (i % 50 == 0) core.receive(heartbeat(clock));
    const device::PixelBuffer& frame = core.render();
    REQUIRE(isSolidLit(frame));
    if (runs.empty() || runs.back().first != frame.at(0, 0)) runs.push_back({frame.at(0, 0), 0});
    ++runs.back().second;
  }

  REQUIRE(runs.size() >= 6);
  for (size_t i = 0; i < runs.size(); ++i) {
    CHECK((runs[i].first == kGreen || runs[i].first == kPink));
    if (i > 0) CHECK(runs[i].first != runs[i - 1].first);
    // Whole runs (not the first or last, cut by the window) last about 1 s.
    if (i > 0 && i + 1 < runs.size()) CHECK(runs[i].second * 20 >= 700);
    if (i > 0 && i + 1 < runs.size()) CHECK(runs[i].second * 20 <= 1500);
  }
}

TEST_CASE_FIXTURE(Device8x8, "after 10 min ignored Awaiting Input settles into a steady glow") {
  core.receive(upsert("asking", "awaiting_input", "#2ad14f"));
  advanceWithHeartbeats(core, clock, 10 * kMinute);

  const Watched w = watch(core, clock, 5 * kSecond);

  CHECK(w.alwaysSolidLit);
  REQUIRE(w.colors.size() == 1);
  CHECK(sameHue(w.colors[0], awaitingInputHue()));
}

TEST_CASE_FIXTURE(AggregateDevice8x8, "the Aggregate Layout shows the most insistent Escalation step of the sessions Awaiting Input") {
  core.receive(upsert("old", "awaiting_input", "#2ad14f"));
  advanceWithHeartbeats(core, clock, 11 * kMinute);  // steady glow
  core.receive(upsert("older", "awaiting_input", "#ff3da5", hostTime(clock)));
  advanceWithHeartbeats(core, clock, 45 * kSecond);  // faster
  core.receive(upsert("new", "awaiting_input", "#ff3da5", hostTime(clock)));  // gentle

  const Watched mixed = watch(core, clock, 10 * kSecond);
  FakeClock aloneClock;
  device::DeviceCore alone(profile8x8(), "0.1.0", aloneClock, abstractTheme());
  alone.receive(upsert("s", "awaiting_input"));
  advanceWithHeartbeats(alone, aloneClock, 45 * kSecond);
  const Watched faster = watch(alone, aloneClock, 10 * kSecond);

  CHECK(mixed.peaks == faster.peaks);
  CHECK(mixed.brightest == faster.brightest);
  CHECK(mixed.dimmest == faster.dimmest);

  advanceWithHeartbeats(core, clock, 80 * kSecond);  // "older" waited 2 min 15 s
  CHECK(core.render().at(0, 0) == kPink);
}

// --- Acknowledge and Escalation ---------------------------------------------

namespace {

// The frame `ms` after a lone session started Awaiting Input on a fresh
// Device: Escalation from the start.
device::PixelBuffer awaitingInputFor(uint32_t ms) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsert("s", "awaiting_input"));
  advanceWithHeartbeats(core, clock, ms);
  return core.render();
}

}  // namespace

TEST_CASE_FIXTURE(AggregateDevice8x8, "an Acknowledge on the Device restarts Escalation but never hides Awaiting Input") {
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  core.receive(upsert("b", "awaiting_input", "#ff3da5"));
  core.receive(upsert("c", "done"));
  advanceWithHeartbeats(core, clock, 3 * kMinute);  // Takeover

  core.acknowledge();

  CHECK(core.render() == awaitingInputFor(0));
  advanceWithHeartbeats(core, clock, 700);
  CHECK(core.render() == awaitingInputFor(700));
  advanceWithHeartbeats(core, clock, 40 * kSecond);
  CHECK(core.render() == awaitingInputFor(40 * kSecond + 700));
  advanceWithHeartbeats(core, clock, 2 * kMinute);
  CHECK(isSolidLit(core.render()));
  CHECK((core.render().at(0, 0) == kGreen || core.render().at(0, 0) == kPink));  // Takeover again
}

namespace {

// The session id in protocol/fixtures/session-upsert.jsonl and acknowledge.jsonl.
const std::string kFixtureSession = "2f0c6a1e-5b7d-4c1a-9e8f-3d2b1a0c9e7f";
// The Host times in hello.jsonl and acknowledge.jsonl.
constexpr int64_t kHelloTime = 1760432400000;
constexpr int64_t kAcknowledgeTime = 1760432410000;

std::string heartbeatAt(int64_t hostTime) {
  return R"({"type":"heartbeat","host_time":)" + std::to_string(hostTime) + "}";
}

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "an Acknowledge from the Host restarts Escalation of that session only") {
  core.receive(fixture("hello.jsonl"));
  core.receive(upsert(kFixtureSession, "awaiting_input", "#2ad14f", kHelloTime - 3 * kMinute));
  core.receive(upsert("other", "awaiting_input", "#ff3da5", kHelloTime - 3 * kMinute));
  clock.advance(10 * kSecond);
  core.receive(heartbeatAt(kAcknowledgeTime));

  CHECK(core.receive(fixture("acknowledge.jsonl")).empty());

  // Only "other" is still in Takeover: the matrix holds its color.
  const Watched w = watch(core, clock, 3 * kSecond);
  REQUIRE(w.colors.size() == 1);
  CHECK(w.colors[0] == kPink);
}

TEST_CASE_FIXTURE(AggregateDevice8x8, "an Acknowledge from the Host without a session id is for every session") {
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  core.receive(upsert("b", "done"));
  advanceWithHeartbeats(core, clock, 3 * kMinute);

  core.receive(R"({"type":"acknowledge"})");

  CHECK(core.render() == awaitingInputFor(0));
}

TEST_CASE_FIXTURE(Device8x8, "Escalation counts from when the session entered Awaiting Input on the Host") {
  // A Device claimed again (replug) hears of a session waiting for 3 min.
  core.receive(fixture("hello.jsonl"));
  core.receive(upsert("a", "awaiting_input", "#2ad14f", kHelloTime - 3 * kMinute));

  CHECK(core.render().at(0, 0) == kGreen);
}

TEST_CASE_FIXTURE(Device8x8, "an Acknowledge the Host replays after a hello restarts Escalation from its Host time") {
  core.receive(fixture("hello.jsonl"));
  core.receive(upsert("a", "awaiting_input", "#2ad14f", kHelloTime - 5 * kMinute));
  core.receive(R"({"type":"acknowledge","id":"a","host_time":)" + std::to_string(kHelloTime - 10500) + "}");

  CHECK(core.render() == awaitingInputFor(10500));
}

TEST_CASE_FIXTURE(Device8x8, "an upsert that keeps the Session State keeps the Acknowledge") {
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  advanceWithHeartbeats(core, clock, 3 * kMinute);
  core.acknowledge();

  core.receive(upsert("a", "awaiting_input", "#ff3da5"));  // the Project Color changed

  CHECK(core.render() == awaitingInputFor(0));
}

TEST_CASE_FIXTURE(Device8x8, "a session that waits again escalates from the new state_entered") {
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  advanceWithHeartbeats(core, clock, 3 * kMinute);
  core.receive(upsert("a", "working", "#2ad14f", hostTime(clock)));

  core.receive(upsert("a", "awaiting_input", "#2ad14f", hostTime(clock)));

  CHECK(core.render() == awaitingInputFor(0));
}

TEST_CASE("the Escalation thresholds are parameters") {
  FakeClock clock;
  device::Preferences quick = abstractTheme();
  quick.escalation.fasterFromMs = 1 * kSecond;
  quick.escalation.takeoverFromMs = 2 * kSecond;
  quick.escalation.steadyFromMs = 3 * kSecond;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, quick);
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));

  advanceWithHeartbeats(core, clock, 2 * kSecond);
  CHECK(core.render().at(0, 0) == kGreen);
  advanceWithHeartbeats(core, clock, 1 * kSecond);
  CHECK(sameHue(core.render().at(0, 0), awaitingInputHue()));
}

TEST_CASE_FIXTURE(Device8x8, "the steady glow holds past the millisecond clock wrapping around") {
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  advanceWithHeartbeats(core, clock, 11 * kMinute);
  const device::PixelBuffer glow = core.render();

  clock.advance(UINT32_MAX - 11 * kMinute + 2 * kSecond);  // 2^32 + 1 s after it started waiting
  core.receive(heartbeat(clock));

  CHECK(core.render() == glow);
}

TEST_CASE("acknowledge messages the Device cannot use are ignored as a whole") {
  for (const std::string line : {R"({"type":"acknowledge","id":42})", R"({"type":"acknowledge","id":""})"}) {
    CAPTURE(line);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(upsert("s1", "done"));

    core.receive(line);

    CHECK(core.render() == loneSessionFrame("done"));
  }
}

// --- Layout -----------------------------------------------------------------

namespace {

// A rectangle of the matrix: columns [x0, x1), rows [y0, y1).
struct Region {
  int x0, y0, x1, y1;
};

// True when every pixel of `region` is `color`.
bool isFilled(const device::PixelBuffer& frame, Region region, device::Rgb color) {
  for (int y = region.y0; y < region.y1; ++y)
    for (int x = region.x0; x < region.x1; ++x)
      if (frame.at(static_cast<uint8_t>(x), static_cast<uint8_t>(y)) != color) return false;
  return true;
}

bool isDark(const device::PixelBuffer& frame, Region region) { return isFilled(frame, region, device::Rgb{}); }
using testsupport::isDark;

// True when no pixel of `region` is off and every one is `hue` at some
// brightness: the region shows that color, whatever its animation.
bool isLitIn(const device::PixelBuffer& frame, Region region, device::Rgb hue);

constexpr device::Rgb kBlue{0x3a, 0x86, 0xff};
constexpr device::Rgb kYellow{0xe6, 0xe6, 0x00};
constexpr device::Rgb kCyan{0x00, 0xd0, 0xd0};

// The 8x8 halves, quadrants and columns.
constexpr Region kLeft{0, 0, 4, 8}, kRight{4, 0, 8, 8};
constexpr Region kTopLeft{0, 0, 4, 4}, kTopRight{4, 0, 8, 4}, kBottomLeft{0, 4, 4, 8}, kBottomRight{4, 4, 8, 8};
Region column(int i) { return {i, 0, i + 1, 8}; }

bool isLitIn(const device::PixelBuffer& frame, Region region, device::Rgb hue) {
  for (int y = region.y0; y < region.y1; ++y)
    for (int x = region.x0; x < region.x1; ++x) {
      const device::Rgb c = frame.at(static_cast<uint8_t>(x), static_cast<uint8_t>(y));
      if (c == device::Rgb{} || !sameHue(c, hue)) return false;
    }
  return true;
}

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "with two sessions the matrix splits into halves, each in its session's Project Color") {
  core.receive(upsertTile("b", "done", "#2ad14f", 1));
  core.receive(upsertTile("a", "done", "#ff3da5", 0));

  const auto& frame = core.render();

  CHECK(isFilled(frame, kLeft, kPink));
  CHECK(isFilled(frame, kRight, kGreen));
}

TEST_CASE_FIXTURE(Device8x8, "with three or four sessions the matrix splits into quadrants, each session in its Tile") {
  core.receive(upsertTile("a", "done", "#2ad14f", 0));
  core.receive(upsertTile("c", "done", "#ff3da5", 2));
  core.receive(upsertTile("d", "done", "#3a86ff", 3));

  CHECK(isFilled(core.render(), kTopLeft, kGreen));
  CHECK(isDark(core.render(), kTopRight));  // slot 1 is free
  CHECK(isFilled(core.render(), kBottomLeft, kPink));
  CHECK(isFilled(core.render(), kBottomRight, kBlue));

  core.receive(upsertTile("e", "done", "#e6e600", 1));

  CHECK(isFilled(core.render(), kTopRight, kYellow));
}

TEST_CASE_FIXTURE(Device8x8, "with five to eight sessions each one gets a column") {
  const char* const colors[] = {"#2ad14f", "#ff3da5", "#3a86ff", "#e6e600", "#00d0d0"};
  const device::Rgb rgb[] = {kGreen, kPink, kBlue, kYellow, kCyan};
  for (int i = 0; i < 5; ++i) core.receive(upsertTile("s" + std::to_string(i), "done", colors[i], i));

  const auto& frame = core.render();

  for (int i = 0; i < 5; ++i) {
    CAPTURE(i);
    CHECK(isFilled(frame, column(i), rgb[i]));
  }
  for (int i = 5; i < 8; ++i) CHECK(isDark(frame, column(i)));
}

TEST_CASE_FIXTURE(Device8x8, "with nine or more sessions the last column is the Overflow Tile with the Aggregate State of the rest") {
  for (int i = 0; i < 7; ++i) core.receive(upsertTile("s" + std::to_string(i), "done", "#3a86ff", i));
  core.receive(upsertTile("s7", "working", "#2ad14f", 7));
  core.receive(upsertTile("s8", "error", "#ff3da5", 8));

  const auto& frame = core.render();

  for (int i = 0; i < 7; ++i) CHECK(isFilled(frame, column(i), kBlue));
  // Error wins the rest's Aggregate State; the Overflow Tile shows it in the
  // Session State's own color, as it stands for several projects.
  CHECK(isLitIn(frame, column(7), loneSessionFrame("error").at(0, 0)));
  CHECK_FALSE(sameHue(frame.at(7, 0), kGreen));
  CHECK_FALSE(sameHue(frame.at(7, 0), kPink));
}

TEST_CASE("the Overflow Tile animates like a Tile of its Aggregate State") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  for (int i = 0; i < 7; ++i) core.receive(upsertTile("s" + std::to_string(i), "done", "#3a86ff", i));
  core.receive(upsertTile("s7", "working", "#2ad14f", 7));
  core.receive(upsertTile("s8", "error", "#ff3da5", 8));
  // The same Session States, each in its own Tile, the Error one in the
  // color of Error.
  FakeClock otherClock;
  device::DeviceCore other(profile8x8(), "0.1.0", otherClock, abstractTheme());
  for (int i = 0; i < 7; ++i) other.receive(upsertTile("s" + std::to_string(i), "done", "#3a86ff", i));
  other.receive(upsertTile("s7", "error", "#ff0000", 7));

  for (int t = 0; t < 3000; t += 100) {
    CAPTURE(t);
    const device::PixelBuffer overflow = core.render();
    const device::PixelBuffer tile = other.render();
    for (uint8_t y = 0; y < 8; ++y) CHECK(overflow.at(7, y) == tile.at(7, y));
    clock.advance(100);
    otherClock.advance(100);
  }
}

TEST_CASE_FIXTURE(Device8x8, "a session keeps its Tile while others come and go") {
  core.receive(upsertTile("a", "done", "#2ad14f", 0));
  core.receive(upsertTile("b", "done", "#ff3da5", 1));
  core.receive(upsertTile("c", "done", "#3a86ff", 2));
  core.receive(upsertTile("d", "done", "#e6e600", 3));

  core.receive(R"({"type":"session_remove","id":"a"})");

  CHECK(isDark(core.render(), kTopLeft));
  CHECK(isFilled(core.render(), kTopRight, kPink));
  CHECK(isFilled(core.render(), kBottomLeft, kBlue));
  CHECK(isFilled(core.render(), kBottomRight, kYellow));
}

TEST_CASE_FIXTURE(Device8x8, "sessions whose Tiles don't fit the Layout take the free ones in order") {
  // Between a session_remove and the upserts of the sessions the Host moved.
  core.receive(upsertTile("x", "done", "#2ad14f", 3));
  core.receive(upsertTile("y", "done", "#ff3da5", 5));

  CHECK(isFilled(core.render(), kLeft, kGreen));
  CHECK(isFilled(core.render(), kRight, kPink));
}

TEST_CASE_FIXTURE(Device8x8, "a session upsert without a Tile takes the first free one") {
  core.receive(upsertTile("a", "done", "#2ad14f", 1));
  core.receive(R"({"type":"session_upsert","id":"b","state":"done","project_color":"#ff3da5"})");

  CHECK(isFilled(core.render(), kLeft, kPink));
  CHECK(isFilled(core.render(), kRight, kGreen));
}

TEST_CASE_FIXTURE(Device8x8, "a lone session has the whole matrix and shows the Aggregate State") {
  core.receive(upsertTile("a", "working", "#2ad14f", 3));

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE("the Layout scales to the Hardware Profile's matrix size") {
  device::HardwareProfile panel16;
  panel16.width = 16;
  panel16.height = 16;
  FakeClock clock;
  device::DeviceCore core(panel16, "0.1.0", clock, abstractTheme());
  for (int i = 0; i < 4; ++i)
    core.receive(upsertTile("s" + std::to_string(i), "done", i == 1 ? "#ff3da5" : "#3a86ff", i));

  const auto& frame = core.render();

  CHECK(isFilled(frame, {8, 0, 16, 8}, kPink));  // top-right quadrant
  CHECK(isFilled(frame, {0, 0, 8, 8}, kBlue));
  CHECK(isFilled(frame, {0, 8, 16, 16}, kBlue));

  for (int i = 4; i < 6; ++i) core.receive(upsertTile("s" + std::to_string(i), "done", "#3a86ff", i));
  CHECK(isFilled(core.render(), {2, 0, 4, 16}, kPink));  // the second column, two pixels wide
  CHECK(isDark(core.render(), {12, 0, 16, 16}));
}

namespace {

// What a Tile in the top-left quadrant looks like over three seconds, one
// frame every 50 ms, with three other Done sessions.
std::vector<device::PixelBuffer> tileFrames(const std::string& state) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(upsertTile("a", state, "#2ad14f", 0));
  for (int i = 1; i < 4; ++i) core.receive(upsertTile("s" + std::to_string(i), "done", "#ff3da5", i));
  std::vector<device::PixelBuffer> frames;
  for (int t = 0; t < 3000; t += 50, clock.advance(50)) {
    if (t % 1000 == 0) core.receive(heartbeat(clock));
    device::PixelBuffer frame(4, 4);
    const device::PixelBuffer& whole = core.render();
    for (uint8_t y = 0; y < 4; ++y)
      for (uint8_t x = 0; x < 4; ++x) frame.set(x, y, whole.at(x, y));
    frames.push_back(frame);
  }
  return frames;
}

bool allFrames(const std::vector<device::PixelBuffer>& frames, bool (*test)(const device::PixelBuffer&)) {
  return std::all_of(frames.begin(), frames.end(), test);
}

bool anyFrame(const std::vector<device::PixelBuffer>& frames, bool (*test)(const device::PixelBuffer&)) {
  return std::any_of(frames.begin(), frames.end(), test);
}

bool isGreenEverywhere(const device::PixelBuffer& f) { return isLitIn(f, {0, 0, 4, 4}, kGreen); }
bool isSolid(const device::PixelBuffer& f) { return isSolidLit(f); }
bool isOff(const device::PixelBuffer& f) { return isDark(f); }

}  // namespace

TEST_CASE("a Tile animates its Session State in its Project Color") {
  const auto idle = tileFrames("idle");
  const auto working = tileFrames("working");
  const auto done = tileFrames("done");
  const auto error = tileFrames("error");
  const auto awaiting = tileFrames("awaiting_input");

  // Idle: calm, a dim steady glow.
  CHECK(allFrames(idle, isGreenEverywhere));
  CHECK(std::all_of(idle.begin(), idle.end(), [&](const auto& f) { return f == idle[0]; }));
  CHECK(isDim(idle[0]));
  // Done: steady, fully lit.
  CHECK(std::all_of(done.begin(), done.end(), [](const auto& f) { return isFilled(f, {0, 0, 4, 4}, kGreen); }));
  // Working: something moves through the Tile.
  CHECK(allFrames(working, isGreenEverywhere));
  CHECK_FALSE(anyFrame(working, isSolid));
  // Error: blinks.
  CHECK(anyFrame(error, isOff));
  CHECK(std::any_of(error.begin(), error.end(), [](const auto& f) { return isFilled(f, {0, 0, 4, 4}, kGreen); }));
  // Awaiting Input: the whole Tile pulses.
  CHECK(allFrames(awaiting, isGreenEverywhere));
  CHECK(allFrames(awaiting, isSolid));
  const auto brightest = std::max_element(awaiting.begin(), awaiting.end(), [](const auto& l, const auto& r) {
    return brightness(l) < brightness(r);
  });
  const auto dimmest = std::min_element(awaiting.begin(), awaiting.end(), [](const auto& l, const auto& r) {
    return brightness(l) < brightness(r);
  });
  CHECK(brightness(*brightest) - brightness(*dimmest) >= 40);
}

TEST_CASE_FIXTURE(Device8x8, "a Tile of a session Awaiting Input escalates: it pulses faster after 30 s") {
  core.receive(upsertTile("a", "awaiting_input", "#2ad14f", 0));
  core.receive(upsertTile("b", "working", "#ff3da5", 1));
  auto peaksIn = [&](uint32_t ms) {
    int peaks = 0, previous = -1;
    bool rising = false;
    for (uint32_t t = 0; t < ms; t += 20, clock.advance(20)) {
      if (t % kSecond == 0) core.receive(heartbeat(clock));
      const int level = std::max({core.render().at(0, 0).r, core.render().at(0, 0).g, core.render().at(0, 0).b});
      if (previous >= 0 && level < previous && rising) ++peaks;
      if (previous >= 0 && level != previous) rising = level > previous;
      previous = level;
    }
    return peaks;
  };
  const int gentle = peaksIn(10 * kSecond);
  advanceWithHeartbeats(core, clock, 25 * kSecond);

  CHECK(peaksIn(10 * kSecond) > gentle);
  CHECK(sameHue(core.render().at(0, 0), kGreen));
}

TEST_CASE_FIXTURE(Device8x8, "a Takeover takes the whole matrix in the adaptive Layout") {
  core.receive(upsertTile("a", "awaiting_input", "#2ad14f", 0));
  core.receive(upsertTile("b", "working", "#ff3da5", 1));
  core.receive(upsertTile("c", "done", "#3a86ff", 2));

  advanceWithHeartbeats(core, clock, 2 * kMinute);

  CHECK(isFilled(core.render(), {0, 0, 8, 8}, kGreen));
}

TEST_CASE("a session in the Overflow Tile can take the whole matrix over too") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  for (int i = 0; i < 8; ++i) core.receive(upsertTile("s" + std::to_string(i), "working", "#3a86ff", i));
  core.receive(upsertTile("s8", "awaiting_input", "#2ad14f", 8));

  advanceWithHeartbeats(core, clock, 2 * kMinute);

  CHECK(isFilled(core.render(), {0, 0, 8, 8}, kGreen));
}

TEST_CASE("the Aggregate Layout shows the Aggregate State on the whole matrix") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, aggregateLayout());
  core.receive(upsertTile("a", "done", "#2ad14f", 0));
  core.receive(upsertTile("b", "error", "#ff3da5", 1));

  CHECK(core.render() == loneSessionFrame("error"));
}

// Raw Mode (#16): the Host streams pixels in binary frames; Awaiting Input
// always pre-empts them (ADR-0001).
namespace {

// The pixel protocol/fixtures/raw-frame.bin sets at (x, y).
device::Rgb rawFixturePixel(int x, int y) {
  return {static_cast<uint8_t>(x * 32), static_cast<uint8_t>(y * 32), 0x80};
}

bool showsRawFixture(const device::PixelBuffer& frame) {
  for (uint8_t y = 0; y < 8; ++y)
    for (uint8_t x = 0; x < 8; ++x)
      if (frame.at(x, y) != rawFixturePixel(x, y)) return false;
  return true;
}

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "in Raw Mode the matrix shows the pixels of the Host's frame") {
  core.receive(upsert("s1", "working"));

  CHECK(core.receive(fixture("raw-mode-enter.jsonl")).empty());
  CHECK(core.receive(fixture("raw-frame.bin")).empty());

  CHECK(showsRawFixture(core.render()));
}

TEST_CASE_FIXTURE(AggregateDevice8x8, "any Awaiting Input pre-empts Raw Mode at once, and the Host's pixels come back after") {
  core.receive(upsert("s1", "working"));
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));

  core.receive(upsert("s2", "awaiting_input"));
  CHECK(core.render() == awaitingInputFor(0));

  core.receive(upsert("s2", "working"));
  CHECK(showsRawFixture(core.render()));
}

TEST_CASE_FIXTURE(Device8x8, "Escalation goes on under Raw Mode: after 2 min ignored a Takeover fills the matrix in the Project Color") {
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(upsert("asking", "awaiting_input", "#2ad14f"));
  core.receive(upsert("busy", "working", "#ff3da5"));

  for (int second = 0; second < 2 * 60; ++second) {
    core.receive(fixture("raw-frame.bin"));
    advanceWithHeartbeats(core, clock, kSecond);
  }

  CHECK(isFilled(core.render(), {0, 0, 8, 8}, kGreen));
}

TEST_CASE_FIXTURE(Device8x8, "raw_mode_exit returns the matrix to the Device's own rendering") {
  core.receive(upsert("s1", "working"));
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));

  CHECK(core.receive(fixture("raw-mode-exit.jsonl")).empty());

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "a Host gone silent ends Raw Mode: Disconnected, then the Device's own rendering") {
  core.receive(upsert("s1", "working"));
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));

  clock.advance(kTimeout);
  CHECK(core.render() == disconnectedFrame({"working"}));

  core.receive(heartbeat(clock));
  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "Raw Mode frames keep the Device connected") {
  core.receive(fixture("raw-mode-enter.jsonl"));
  for (int i = 0; i < 10; ++i) {
    clock.advance(kSecond);
    core.receive(fixture("raw-frame.bin"));
  }

  CHECK(showsRawFixture(core.render()));
}

TEST_CASE_FIXTURE(Device8x8, "a hello ends Raw Mode: a new Host Daemon starts from the Device's own rendering") {
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));

  core.receive(fixture("hello.jsonl"));
  core.receive(upsert("s1", "working"));

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "Raw Mode shows a dark matrix until the first frame") {
  core.receive(upsert("s1", "working"));

  core.receive(fixture("raw-mode-enter.jsonl"));

  CHECK(isDark(core.render()));
}

TEST_CASE("frames the Device cannot use are ignored, and don't count as hearing from the Host") {
  const std::string frame = fixture("raw-frame.bin");
  struct Unusable {
    const char* why;
    bool inRawMode;
    std::string frame;
  };
  const Unusable unusable[] = {
      {"outside Raw Mode", false, frame},
      {"one pixel short", true, frame.substr(0, frame.size() - 3)},
      {"one byte too long", true, frame + '\x01'},
      {"for a 16x16 matrix", true, frame + std::string(16 * 16 * 3 - 8 * 8 * 3, '\x01')},
  };
  for (const auto& [why, inRawMode, line] : unusable) {
    CAPTURE(why);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(upsert("s1", "working"));
    if (inRawMode) core.receive(fixture("raw-mode-enter.jsonl"));
    clock.advance(kTimeout - 1);

    core.receive(line);

    CHECK_FALSE(showsRawFixture(core.render()));
    clock.advance(1);
    CHECK(core.render() == disconnectedFrame({"working"}));
  }
}

TEST_CASE("a frame has one pixel per LED of the Hardware Profile's matrix") {
  FakeClock clock;
  device::HardwareProfile profile;
  profile.width = 16;
  profile.height = 16;
  device::DeviceCore core(profile, "0.1.0", clock, abstractTheme());
  std::string frame(1, device::DeviceCore::kRawFrameStart);
  for (int i = 0; i < 16 * 16; ++i) frame += i == 16 * 16 - 1 ? std::string("\x01\x02\x03", 3) : std::string(3, '\x00');
  core.receive(fixture("raw-mode-enter.jsonl"));

  core.receive(frame);

  const auto& shown = core.render();
  CHECK(shown.at(15, 15) == device::Rgb{1, 2, 3});
  CHECK(shown.at(14, 15) == device::Rgb{});
}

// --- Preferences (#11) ------------------------------------------------------
// The Host pushes the Preferences the Device renders by; the Device caches them
// and boots with them. Brightness always stays under the Hardware Profile cap.

namespace {

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "a Preferences push changes the Layout at runtime") {
  core.receive(upsertTile("a", "done", "#ff3da5", 0));
  core.receive(upsertTile("b", "error", "#2ad14f", 1));
  REQUIRE(isFilled(core.render(), kLeft, kPink));

  CHECK(core.receive(preferences(R"("layout":"aggregate")")).empty());

  CHECK(core.render() == loneSessionFrame("error"));

  core.receive(preferences(R"("layout":"adaptive")"));

  CHECK(isFilled(core.render(), kLeft, kPink));
}

TEST_CASE_FIXTURE(Device8x8, "the preferences fixture is accepted and counts as hearing from the Host") {
  core.receive(upsertTile("a", "done", "#ff3da5", 0));
  core.receive(upsertTile("b", "idle", "#2ad14f", 1));
  clock.advance(kTimeout - 1);

  CHECK(core.receive(fixture("preferences.jsonl")).empty());

  // Aggregate Layout, turned upside down, at half the cap, with a
  // Disconnected timeout of 10 s.
  const auto& prefs = core.preferences();
  CHECK(prefs.theme == "minimal");
  CHECK(prefs.brightness == 50);
  CHECK(prefs.layout == device::Layout::Aggregate);
  CHECK(prefs.rotation == 180);
  CHECK_FALSE(prefs.quietMode);
  CHECK(prefs.disconnectedTimeoutMs == 10 * kSecond);
  CHECK(prefs.escalation.fasterFromMs == 20 * kSecond);
  CHECK(prefs.escalation.takeoverFromMs == 1 * kMinute);
  CHECK(prefs.escalation.steadyFromMs == 5 * kMinute);
  clock.advance(kTimeout);
  CHECK(isSolidLit(core.render()));
}

TEST_CASE_FIXTURE(Device8x8, "a Preferences push sets how long the Host may stay silent before Disconnected") {
  core.receive(upsert("s1", "done"));
  core.receive(preferences(R"("disconnected_timeout_ms":10000)"));

  clock.advance(10 * kSecond - 1);
  CHECK(core.render() == loneSessionFrame("done"));
  clock.advance(1);
  CHECK(core.render() == disconnectedFrame({"done"}));
}

TEST_CASE_FIXTURE(Device8x8, "a Preferences push sets the Escalation thresholds") {
  core.receive(preferences(R"("escalation_faster_ms":1000,"escalation_takeover_ms":2000,"escalation_steady_ms":3000)"));
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));

  advanceWithHeartbeats(core, clock, 2 * kSecond);
  CHECK(core.render().at(0, 0) == kGreen);
  advanceWithHeartbeats(core, clock, 1 * kSecond);
  CHECK(sameHue(core.render().at(0, 0), awaitingInputHue()));
}

TEST_CASE_FIXTURE(AggregateDevice8x8, "a field missing from a Preferences push takes the Device's default") {
  core.receive(preferences(R"("brightness":50,"rotation":90)"));

  core.receive(preferences(R"("quiet_mode":true)"));

  const auto& prefs = core.preferences();
  CHECK(prefs.brightness == 100);
  CHECK(prefs.rotation == 0);
  CHECK(prefs.quietMode);
  CHECK(prefs.layout == device::Layout::Aggregate);  // the default this Device was built with
}

TEST_CASE("a Preferences push with any invalid field is ignored as a whole") {
  const char* const invalid[] = {
      R"("brightness":0)",
      R"("brightness":101)",
      R"("brightness":"50")",
      R"("brightness":50.5)",
      R"("layout":"tiles")",
      R"("layout":1)",
      R"("rotation":45)",
      R"("rotation":-90)",
      R"("quiet_mode":"yes")",
      R"("theme":"")",
      R"("theme":7)",
      R"("theme":"a-theme-name-longer-than-thirty-two")",
      R"("disconnected_timeout_ms":999)",
      R"("disconnected_timeout_ms":-1)",
      R"("escalation_faster_ms":200000)",  // after the Takeover
      R"("escalation_takeover_ms":1000000)",  // after the steady glow
  };
  for (const char* field : invalid) {
    CAPTURE(field);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(upsert("s1", "working"));
    clock.advance(kTimeout - 1);

    core.receive(preferences(std::string(R"("layout":"aggregate","quiet_mode":true,)") + field));

    CHECK(core.preferences().layout == device::Layout::Adaptive);
    CHECK_FALSE(core.preferences().quietMode);
    clock.advance(1);  // an unusable message doesn't count as hearing from the Host
    CHECK(core.render() == disconnectedFrame({"working"}));
  }
}

TEST_CASE_FIXTURE(Device8x8, "a hello keeps the Preferences") {
  core.receive(preferences(R"("layout":"aggregate")"));

  core.receive(fixture("hello.jsonl"));

  CHECK(core.preferences().layout == device::Layout::Aggregate);
}

// --- Brightness cap ---------------------------------------------------------

namespace {

// The brightest channel anywhere in the frame.
int brightestChannel(const device::PixelBuffer& frame) {
  int brightest = 0;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) {
      const device::Rgb c = frame.at(x, y);
      brightest = std::max({brightest, int{c.r}, int{c.g}, int{c.b}});
    }
  return brightest;
}

device::HardwareProfile cappedProfile(uint8_t cap) {
  device::HardwareProfile p = profile8x8();
  p.brightnessCap = cap;
  return p;
}

// The brightest any LED gets while `script` drives a Device with this cap
// and these Preferences, over `ms` of frames with the Host's heartbeat (or
// one frame and no heartbeat for `ms` 0).
template <typename Script>
int brightestUnder(uint8_t cap, const std::string& prefs, uint32_t ms, Script script) {
  FakeClock clock;
  device::DeviceCore core(cappedProfile(cap), "0.1.0", clock, abstractTheme());
  core.receive(preferences(prefs));
  script(core, clock);
  int brightest = brightestChannel(core.render());
  for (uint32_t t = 0; t < ms; t += 20, clock.advance(20)) {
    if (t % kSecond == 0) core.receive(heartbeat(clock));
    brightest = std::max(brightest, brightestChannel(core.render()));
  }
  return brightest;
}

}  // namespace

TEST_CASE("every LED stays under the Hardware Profile's brightness cap, whatever is shown") {
  // A bright Project Color and a Raw Mode frame at full white.
  std::string white(1, device::DeviceCore::kRawFrameStart);
  white += std::string(8 * 8 * 3, '\xFF');
  const struct {
    const char* name;
    std::function<void(device::DeviceCore&, FakeClock&)> script;
    uint32_t watchMs;
  } scenes[] = {
      {"every Session State", [](auto& core, auto&) {
         int i = 0;
         for (const char* s : kStatesByUrgency) core.receive(upsertTile(s, s, "#ffffff", i++));
       }, 2 * kSecond},
      {"the faster, brighter pulse", [](auto& core, auto& clock) {
         core.receive(upsert("a", "awaiting_input", "#ffffff"));
         advanceWithHeartbeats(core, clock, 40 * kSecond);
       }, 2 * kSecond},
      {"a Takeover", [](auto& core, auto& clock) {
         core.receive(upsert("a", "awaiting_input", "#ffffff"));
         advanceWithHeartbeats(core, clock, 3 * kMinute);
       }, 2 * kSecond},
      {"Disconnected", [](auto&, auto& clock) { clock.advance(kTimeout); }, 0},
      {"a Raw Mode frame", [&white](auto& core, auto&) {
         core.receive(fixture("raw-mode-enter.jsonl"));
         core.receive(white);
       }, 2 * kSecond},
  };
  for (const auto& scene : scenes) {
    CAPTURE(scene.name);
    for (uint8_t cap : {uint8_t{48}, uint8_t{128}}) {
      CAPTURE(int{cap});
      const int full = brightestUnder(cap, R"("brightness":100)", scene.watchMs, scene.script);
      CHECK(full <= cap);
      CHECK(full > 0);
      // The brightness Preference scales under the cap.
      const int half = brightestUnder(cap, R"("brightness":50)", scene.watchMs, scene.script);
      CHECK(half <= cap / 2 + 1);
      CHECK(half < full);
    }
  }
}

TEST_CASE("a Raw Mode frame at full white is shown at the cap") {
  std::string white(1, device::DeviceCore::kRawFrameStart);
  white += std::string(8 * 8 * 3, '\xFF');
  FakeClock clock;
  device::DeviceCore core(cappedProfile(48), "0.1.0", clock, abstractTheme());
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(white);

  const auto& frame = core.render();

  CHECK(isSolidLit(frame));
  CHECK(frame.at(0, 0) == device::Rgb{48, 48, 48});
}

TEST_CASE("on a dim Device a dim pattern stays lit") {
  FakeClock clock;
  device::DeviceCore core(cappedProfile(8), "0.1.0", clock, abstractTheme());
  core.receive(preferences(R"("brightness":1)"));

  clock.advance(kTimeout);

  CHECK(isSparse(core.render()));  // Disconnected still shows, never a dark matrix
}

// --- Rotation ---------------------------------------------------------------

TEST_CASE("the rotation Preference turns the picture clockwise on the matrix") {
  const struct {
    int degrees;
    Region left;  // where the left half of the picture goes
  } turns[] = {{0, kLeft}, {90, {0, 0, 8, 4}}, {180, kRight}, {270, {0, 4, 8, 8}}};
  for (const auto& turn : turns) {
    CAPTURE(turn.degrees);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(preferences(R"("rotation":)" + std::to_string(turn.degrees)));
    core.receive(upsertTile("a", "done", "#ff3da5", 0));
    core.receive(upsertTile("b", "done", "#2ad14f", 1));

    const auto& frame = core.render();

    CHECK(isFilled(frame, turn.left, kPink));
  }
}

TEST_CASE("turned sideways, a matrix that isn't square lays the picture out in the turned shape") {
  FakeClock clock;
  device::HardwareProfile wide = profile8x8();
  wide.width = 8;
  wide.height = 4;
  device::DeviceCore core(wide, "0.1.0", clock, abstractTheme());
  core.receive(preferences(R"("rotation":90)"));
  core.receive(upsertTile("a", "done", "#ff3da5", 0));
  core.receive(upsertTile("b", "done", "#2ad14f", 1));

  const auto& frame = core.render();

  // The picture is 4 wide and 8 tall: its left half (2 x 8) turns into the
  // top half of the 8 x 4 matrix.
  CHECK(frame.width() == 8);
  CHECK(frame.height() == 4);
  CHECK(isFilled(frame, {0, 0, 8, 2}, kPink));
  CHECK(isFilled(frame, {0, 2, 8, 4}, kGreen));
}

TEST_CASE_FIXTURE(Device8x8, "a Raw Mode frame turns with the rotation too") {
  core.receive(preferences(R"("rotation":180)"));
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));

  const auto& frame = core.render();

  CHECK(frame.at(7, 7) == rawFixturePixel(0, 0));
  CHECK(frame.at(0, 0) == rawFixturePixel(7, 7));
  CHECK(frame.at(5, 6) == rawFixturePixel(2, 1));
}

// --- Quiet Mode -------------------------------------------------------------

TEST_CASE("Quiet Mode shows nothing but Awaiting Input") {
  for (const char* state : {"idle", "working", "done", "error"}) {
    CAPTURE(state);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(preferences(R"("quiet_mode":true)"));

    core.receive(upsert("s1", state));

    CHECK(isDark(core.render()));
  }
}

TEST_CASE_FIXTURE(Device8x8, "in Quiet Mode Awaiting Input shows as it always does") {
  core.receive(preferences(R"("quiet_mode":true)"));

  core.receive(upsert("s1", "awaiting_input"));

  CHECK(core.render() == loneSessionFrame("awaiting_input"));
}

TEST_CASE_FIXTURE(Device8x8, "in Quiet Mode only the Tiles of sessions Awaiting Input are lit") {
  core.receive(preferences(R"("quiet_mode":true)"));
  core.receive(upsertTile("a", "error", "#ff3da5", 0));
  core.receive(upsertTile("b", "awaiting_input", "#2ad14f", 1));
  core.receive(upsertTile("c", "working", "#3a86ff", 2));

  const auto& frame = core.render();

  CHECK(isDark(frame, kTopLeft));
  CHECK(isLitIn(frame, kTopRight, kGreen));
  CHECK(isDark(frame, kBottomLeft));
  CHECK(isDark(frame, kBottomRight));
}

TEST_CASE_FIXTURE(AggregateDevice8x8, "Quiet Mode never suppresses Escalation: it escalates to a Takeover") {
  core.receive(preferences(R"("quiet_mode":true)"));
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  core.receive(upsert("b", "error", "#ff3da5"));
  const Watched gentle = watch(core, clock, 10 * kSecond);
  advanceWithHeartbeats(core, clock, 20 * kSecond);

  const Watched faster = watch(core, clock, 10 * kSecond);

  CHECK(faster.peaks > gentle.peaks);
  advanceWithHeartbeats(core, clock, 2 * kMinute);
  CHECK(isSolidLit(core.render()));
  CHECK(core.render().at(0, 0) == kGreen);
}

TEST_CASE_FIXTURE(Device8x8, "Quiet Mode still shows Disconnected") {
  core.receive(preferences(R"("quiet_mode":true)"));
  core.receive(upsert("s1", "working"));

  clock.advance(kTimeout);

  CHECK(isSparse(core.render()));
}

// --- Cached Preferences -----------------------------------------------------

namespace {

// The firmware shell's flash, in memory.
class FakeCache : public device::PreferencesCache {
 public:
  std::string load() override { return saved; }
  void save(std::string_view line) override {
    saved = std::string(line);
    ++saves;
  }
  std::string saved;
  int saves = 0;
};

}  // namespace

TEST_CASE("the Device caches the Preferences a push changes, and only then") {
  FakeClock clock;
  FakeCache cache;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme(), &cache);

  core.receive(fixture("preferences.jsonl"));
  core.receive(fixture("preferences.jsonl"));

  CHECK(cache.saves == 1);
  JsonDocument saved, pushed;
  REQUIRE_FALSE(deserializeJson(saved, cache.saved));
  REQUIRE_FALSE(deserializeJson(pushed, fixture("preferences.jsonl")));
  CHECK_MESSAGE(saved == pushed, "saved: " << cache.saved);
  core.receive(preferences(R"("theme":"default")"));  // nothing else: the defaults again
  CHECK(cache.saves == 2);
}

TEST_CASE("a Device boots with its cached Preferences before any Host connects") {
  FakeClock clock;
  FakeCache cache;
  cache.saved = preferences(R"("brightness":50,"rotation":90,"disconnected_timeout_ms":10000,"layout":"aggregate")");

  device::DeviceCore core(cappedProfile(200), "0.1.0", clock, abstractTheme(), &cache);

  CHECK(core.preferences().layout == device::Layout::Aggregate);
  clock.advance(kTimeout);
  CHECK(isDark(core.render()));  // not Disconnected yet: the cached timeout is 10 s
  clock.advance(10 * kSecond - kTimeout);
  const device::PixelBuffer turned = core.render();
  CHECK(isSparse(turned));
  CHECK(brightestChannel(turned) <= 100);

  // The same Disconnected, upright, turns into it clockwise.
  FakeClock otherClock;
  device::DeviceCore unturned(cappedProfile(200), "0.1.0", otherClock, abstractTheme());
  unturned.receive(preferences(R"("brightness":50)"));
  otherClock.advance(kTimeout);
  const device::PixelBuffer upright = unturned.render();
  CHECK(upright != turned);
  for (uint8_t y = 0; y < 8; ++y)
    for (uint8_t x = 0; x < 8; ++x) CHECK(turned.at(static_cast<uint8_t>(7 - y), x) == upright.at(x, y));
}

TEST_CASE("a cache without valid Preferences boots with the defaults") {
  for (const char* line : {"", "not json", R"({"type":"preferences","brightness":0})"}) {
    CAPTURE(line);
    FakeClock clock;
    FakeCache cache;
    cache.saved = line;
    device::Preferences defaults = aggregateLayout();

    device::DeviceCore core(profile8x8(), "0.1.0", clock, defaults, &cache);

    CHECK(core.preferences().layout == device::Layout::Aggregate);
    CHECK(core.preferences().brightness == 100);
    CHECK(cache.saves == 0);
  }
}

// --- Preview (the web UI's preview-state override) --------------------------

namespace {

std::string previewEnter(const std::string& state, uint32_t elapsedMs = 0) {
  return R"({"type":"preview_enter","state":")" + state + R"(","elapsed_ms":)" + std::to_string(elapsedMs) + "}";
}

constexpr device::Rgb kAwaitingInputOrange{0xff, 0x96, 0x00};

}  // namespace

TEST_CASE("a preview shows the matrix of a lone session in that Session State, whatever sessions the Device holds") {
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
    core.receive(upsertTile("a", "error", "#2ad14f", 0));
    core.receive(upsertTile("b", "done", "#ff3da5", 1));

    CHECK(core.receive(previewEnter(state)).empty());

    CHECK(core.render() == loneSessionFrame(state));
  }
}

TEST_CASE_FIXTURE(Device8x8, "preview_exit brings back the Device's sessions") {
  core.receive(upsert("s1", "working"));
  core.receive(previewEnter("error"));

  CHECK(core.receive(fixture("preview-exit.jsonl")).empty());

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "a preview of Awaiting Input shows the Escalation step its elapsed time has reached") {
  core.receive(previewEnter("awaiting_input", 40 * kSecond));
  CHECK(core.render() == awaitingInputFor(40 * kSecond));

  advanceWithHeartbeats(core, clock, 700);
  CHECK(core.render() == awaitingInputFor(40 * kSecond + 700));
}

TEST_CASE_FIXTURE(Device8x8, "the preview fixture shows a Takeover in the Session State's own color") {
  core.receive(upsert("s1", "idle"));

  core.receive(fixture("preview-enter.jsonl"));

  CHECK(isFilled(core.render(), {0, 0, 8, 8}, kAwaitingInputOrange));
}

TEST_CASE_FIXTURE(Device8x8, "a preview shows over Raw Mode, and the Host's pixels come back after") {
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));

  core.receive(previewEnter("done"));
  CHECK(core.render() == loneSessionFrame("done"));

  core.receive(fixture("preview-exit.jsonl"));
  CHECK(showsRawFixture(core.render()));
}

TEST_CASE_FIXTURE(Device8x8, "a Host gone silent ends the preview: Disconnected, then the Device's own rendering") {
  core.receive(upsert("s1", "working"));
  core.receive(previewEnter("error"));

  clock.advance(kTimeout);
  CHECK(core.render() == disconnectedFrame({"working"}));

  core.receive(heartbeat(clock));
  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "a hello ends the preview") {
  core.receive(previewEnter("error"));

  core.receive(fixture("hello.jsonl"));
  core.receive(upsert("s1", "working"));

  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE_FIXTURE(Device8x8, "preview messages keep the Device connected") {
  for (int i = 0; i < 10; ++i) {
    clock.advance(kSecond);
    core.receive(previewEnter("done"));
  }

  CHECK(core.render() == loneSessionFrame("done"));
}

TEST_CASE_FIXTURE(Device8x8, "a preview the Device cannot use is ignored") {
  core.receive(upsert("s1", "working"));
  for (const char* line : {R"({"type":"preview_enter"})", R"({"type":"preview_enter","state":"asleep"})",
                           R"({"type":"preview_enter","state":"done","elapsed_ms":-1})",
                           R"({"type":"preview_enter","state":"done","elapsed_ms":"soon"})"}) {
    CAPTURE(line);
    CHECK(core.receive(line).empty());
    CHECK(core.render() == loneSessionFrame("working"));
  }
}

TEST_CASE_FIXTURE(Device8x8, "a real session Awaiting Input pre-empts the preview, and the preview shows again after") {
  core.receive(upsert("s1", "working"));
  core.receive(previewEnter("done"));

  core.receive(upsert("s1", "awaiting_input"));
  CHECK(core.render() == awaitingInputFor(0));

  advanceWithHeartbeats(core, clock, 40 * kSecond);
  CHECK(core.render() == awaitingInputFor(40 * kSecond));

  core.receive(upsert("s1", "working"));
  CHECK(core.render() == loneSessionFrame("done"));
}

TEST_CASE_FIXTURE(Device8x8, "a preview entered while a session is Awaiting Input waits for it") {
  core.receive(upsert("s1", "awaiting_input"));

  core.receive(fixture("preview-enter.jsonl"));
  CHECK(core.render() == awaitingInputFor(0));

  core.receive(R"({"type":"session_remove","id":"s1"})");
  CHECK(isFilled(core.render(), {0, 0, 8, 8}, kAwaitingInputOrange));
}

// --- Goodbye (a Host Daemon exiting cleanly) --------------------------------

TEST_CASE_FIXTURE(Device8x8, "a goodbye darkens the matrix, and no Disconnected follows the Host's silence") {
  core.receive(upsert("s1", "working"));

  CHECK(core.receive(fixture("goodbye.jsonl")).empty());
  CHECK(core.render() == device::PixelBuffer(8, 8));

  for (int i = 0; i < 10; ++i) {
    clock.advance(kTimeout);
    CHECK(core.render() == device::PixelBuffer(8, 8));
  }
}

TEST_CASE_FIXTURE(Device8x8, "a goodbye ends Raw Mode and the preview") {
  core.receive(fixture("raw-mode-enter.jsonl"));
  core.receive(fixture("raw-frame.bin"));
  core.receive(previewEnter("done"));

  core.receive(fixture("goodbye.jsonl"));

  CHECK(core.render() == device::PixelBuffer(8, 8));
}

TEST_CASE_FIXTURE(Device8x8, "a hello after a goodbye resumes as usual, Disconnected included") {
  core.receive(upsert("s1", "error"));
  core.receive(fixture("goodbye.jsonl"));
  clock.advance(kTimeout);

  CHECK_FALSE(core.receive(fixture("hello.jsonl")).empty());
  core.receive(upsert("s2", "working"));
  CHECK(core.render() == loneSessionFrame("working"));

  clock.advance(kTimeout);
  CHECK(core.render() == disconnectedFrame({"working"}));
}

// --- Update me --------------------------------------------------------------
//
// A Host that speaks another protocol version refuses to drive the Device.
// Its hello says which version it speaks, which every version of the
// protocol keeps (protocol/README.md), so the Device shows "update me" (an
// arrow pointing up) instead of Disconnected, until a Host it can talk to
// says hello.

namespace {

// Lit pixels per row of the frame.
std::vector<int> litPerRow(const device::PixelBuffer& frame) {
  std::vector<int> lit(frame.height());
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) lit[y] += frame.at(x, y) != device::Rgb{};
  return lit;
}

// True when the frame is an arrow pointing up, in one color: lit at the
// middle of the top and bottom rows, mirror-symmetric, its widest row (the
// head) in the upper half and the bottom row (the shaft) narrower.
bool isUpArrow(const device::PixelBuffer& frame) {
  const int w = frame.width(), h = frame.height();
  device::Rgb color{};
  for (int y = 0; y < h; ++y)
    for (int x = 0; x < w; ++x) {
      const device::Rgb c = frame.at(x, y);
      if (c != frame.at(w - 1 - x, y)) return false;
      if (c == device::Rgb{}) continue;
      if (color == device::Rgb{}) color = c;
      if (c != color) return false;
    }
  const auto lit = litPerRow(frame);
  const int widest = static_cast<int>(std::max_element(lit.begin(), lit.end()) - lit.begin());
  return frame.at(w / 2, 0) != device::Rgb{} && frame.at(w / 2, h - 1) != device::Rgb{} && widest < h / 2 &&
         lit[h - 1] < lit[widest] && frame.at(0, h - 1) == device::Rgb{};
}

device::PixelBuffer updateMeFrame() {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(fixture("hello-newer-protocol.jsonl"));
  return core.render();
}

}  // namespace

TEST_CASE_FIXTURE(Device8x8, "a hello in a newer protocol version is answered as any hello, and the matrix asks for an update") {
  core.receive(upsert("s1", "awaiting_input"));

  // The reply tells the Host which version the Device speaks: the same
  // hello_reply as to a Host of its own version.
  CHECK(core.receive(fixture("hello-newer-protocol.jsonl")) == core.receive(fixture("hello.jsonl")));
  core.receive(fixture("hello-newer-protocol.jsonl"));

  const auto frame = core.render();
  CHECK(isUpArrow(frame));
  CHECK(frame != disconnectedFrame({}));
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    CHECK(frame != loneSessionFrame(state));
  }
}

TEST_CASE("update me is an arrow on any matrix size") {
  for (const auto& [w, h] : {std::pair{8, 8}, std::pair{16, 16}, std::pair{32, 8}, std::pair{5, 7}}) {
    CAPTURE(w);
    CAPTURE(h);
    device::HardwareProfile profile = profile8x8();
    profile.width = static_cast<uint8_t>(w);
    profile.height = static_cast<uint8_t>(h);
    FakeClock clock;
    device::DeviceCore core(profile, "0.1.0", clock, abstractTheme());
    core.receive(fixture("hello-newer-protocol.jsonl"));
    CHECK(isUpArrow(core.render()));
  }
}

TEST_CASE("a hello in an older protocol version asks for an update too") {
  // The Host refuses to drive a Device of another version either way; it
  // tells the human what to update (codino status).
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, abstractTheme());
  core.receive(R"({"type":"hello","protocol":-1,"host_time":1760432400000})");
  CHECK(core.render() == updateMeFrame());
}

TEST_CASE_FIXTURE(Device8x8, "update me holds through the Host's silence: it is no Disconnected") {
  core.receive(fixture("hello-newer-protocol.jsonl"));
  for (int i = 0; i < 10; ++i) {
    clock.advance(kTimeout);
    CHECK(core.render() == updateMeFrame());
  }
}

TEST_CASE_FIXTURE(Device8x8, "while it asks for an update the Device ignores everything but hello") {
  core.receive(fixture("hello-newer-protocol.jsonl"));
  for (const std::string& line : {upsert("s1", "awaiting_input"), fixture("raw-mode-enter.jsonl"), fixture("raw-frame.bin"),
                                  fixture("preview-enter.jsonl"), fixture("heartbeat.jsonl"), fixture("goodbye.jsonl")}) {
    CAPTURE(line.substr(0, 40));
    CHECK(core.receive(line).empty());
    CHECK(core.render() == updateMeFrame());
  }
}

TEST_CASE_FIXTURE(Device8x8, "a hello in the Device's own version ends update me") {
  core.receive(fixture("hello-newer-protocol.jsonl"));

  core.receive(fixture("hello.jsonl"));
  CHECK(isDark(core.render()));
  core.receive(upsert("s1", "working"));
  CHECK(core.render() == loneSessionFrame("working"));
}

TEST_CASE("update me stays under the brightness cap, turned by the rotation, and shows in Quiet Mode") {
  FakeClock clock;
  device::DeviceCore core(cappedProfile(40), "0.1.0", clock, abstractTheme());
  core.receive(preferences(R"("theme":"minimal","quiet_mode":true,"rotation":180)"));
  core.receive(fixture("hello-newer-protocol.jsonl"));

  const auto frame = core.render();
  CHECK(brightestChannel(frame) <= 40);
  CHECK_FALSE(isDark(frame));
  CHECK_FALSE(isUpArrow(frame));  // upside down: the Device is mounted the other way round
  core.receive(fixture("hello.jsonl"));
  core.receive(preferences(R"("theme":"minimal","quiet_mode":true)"));
  core.receive(fixture("hello-newer-protocol.jsonl"));
  CHECK(isUpArrow(core.render()));
}

TEST_CASE_FIXTURE(Device8x8, "while it asks for an update a press on the Device tells the Host nothing") {
  core.receive(fixture("hello-newer-protocol.jsonl"));
  CHECK(core.acknowledge().empty());
  CHECK(core.render() == updateMeFrame());
}
