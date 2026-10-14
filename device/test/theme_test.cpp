// Themes at the Device core's seam: install messages, Preferences pushes,
// session messages and clock ticks in, pixel buffer (and install replies)
// out. Semantic checks; the golden frames of the built-in Themes are in
// theme_snapshot_test.cpp.
#include <doctest.h>

#include <ArduinoJson.h>

#include <algorithm>
#include <fstream>
#include <map>
#include <string>
#include <vector>

#include "device_core.h"
#include "test_support.h"

using namespace testsupport;

namespace {

constexpr uint32_t kSecond = 1000;
constexpr uint32_t kMinute = 60 * kSecond;

device::Preferences themed(const std::string& theme) {
  device::Preferences p;
  p.theme = theme;
  return p;
}

// The firmware shell's flash, in memory, counting writes.
class FakeStore : public device::ThemeStore {
 public:
  std::vector<std::string> names(device::InstallKind kind) override {
    std::vector<std::string> out;
    for (const auto& [key, line] : lines)
      if (key.first == kind) out.push_back(key.second);
    return out;
  }
  std::string load(device::InstallKind kind, std::string_view name) override {
    auto it = lines.find({kind, std::string(name)});
    return it == lines.end() ? std::string() : it->second;
  }
  bool save(device::InstallKind kind, std::string_view name, std::string_view line) override {
    lines[{kind, std::string(name)}] = std::string(line);
    ++writes;
    return true;
  }
  bool erase(device::InstallKind kind, std::string_view name) override {
    ++writes;
    return lines.erase({kind, std::string(name)}) == 1;
  }
  std::map<std::pair<device::InstallKind, std::string>, std::string> lines;
  int writes = 0;
};

bool lit(device::Rgb c) { return c != device::Rgb{}; }

int countLit(const device::PixelBuffer& frame) {
  int n = 0;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) n += lit(frame.at(x, y));
  return n;
}

// A pose: some LEDs lit and some off, not a fill.
bool isPose(const device::PixelBuffer& frame) {
  const int n = countLit(frame);
  return n > 0 && n < frame.width() * frame.height();
}

int brightestChannel(const device::PixelBuffer& frame) {
  int brightest = 0;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) {
      const device::Rgb c = frame.at(x, y);
      brightest = std::max({brightest, int{c.r}, int{c.g}, int{c.b}});
    }
  return brightest;
}

// The frames a lone session in `state` shows over `ms`, one every `step` ms,
// with the Host's heartbeat every second.
std::vector<device::PixelBuffer> watchLone(const std::string& theme, const std::string& state, uint32_t ms,
                                           uint32_t step = 50, device::HardwareProfile profile = profile8x8()) {
  FakeClock clock;
  device::DeviceCore core(profile, "0.1.0", clock, themed(theme));
  core.receive(upsert("lone", state));
  std::vector<device::PixelBuffer> frames;
  for (uint32_t t = 0; t < ms; t += step, clock.advance(step)) {
    if (t % kSecond == 0) core.receive(heartbeat(clock));
    frames.push_back(core.render());
  }
  return frames;
}

// An `install` line for a Theme whose every Session State is `state`.
std::string themeWithEveryState(const std::string& name, const std::string& state) {
  std::string states;
  for (const char* s : kStatesByUrgency) states += std::string(states.empty() ? "" : ",") + "\"" + s + "\":" + state;
  return R"({"type":"install","kind":"theme","name":")" + name + R"(","states":{)" + states + "}}";
}

// The frames a lone Working session shows under a Theme whose every state is
// `effect` in pure white, one every 20 ms over `ms`.
std::vector<device::PixelBuffer> effectFrames(const std::string& effect, uint32_t ms) {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  REQUIRE(core.receive(themeWithEveryState("fx", R"({"palette":["#ffffff"],"effect":)" + effect + "}"))
              .find(R"("ok":true)") != std::string::npos);
  core.receive(preferences(R"("theme":"fx")"));
  core.receive(upsert("s", "working"));
  std::vector<device::PixelBuffer> frames;
  for (uint32_t t = 0; t < ms; t += 20, clock.advance(20)) {
    if (t % kSecond == 0) core.receive(heartbeat(clock));
    frames.push_back(core.render());
  }
  return frames;
}

JsonDocument parsed(const std::string& line) {
  JsonDocument doc;
  REQUIRE_FALSE(deserializeJson(doc, line));
  return doc;
}

}  // namespace

// --- The default Character Theme -------------------------------------------

TEST_CASE("by default a lone session shows the mascot: one pose per Session State") {
  std::vector<std::vector<device::PixelBuffer>> poses;
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    const auto frames = watchLone(device::kDefaultTheme, state, 3 * kSecond);
    for (const auto& f : frames) CHECK(isPose(f));
    poses.push_back(frames);
  }
  // No two Session States share a pose at any moment.
  for (size_t a = 0; a < poses.size(); ++a)
    for (size_t b = a + 1; b < poses.size(); ++b)
      for (size_t i = 0; i < poses[a].size(); ++i) CHECK(poses[a][i] != poses[b][i]);
}

TEST_CASE("the mascot is alive: every pose animates") {
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    const auto frames = watchLone(device::kDefaultTheme, state, 4 * kSecond);
    CHECK(std::any_of(frames.begin(), frames.end(), [&](const auto& f) { return f != frames[0]; }));
  }
}

TEST_CASE("Idle is the calmest pose: dim") {
  auto brightest = [](const std::vector<device::PixelBuffer>& frames) {
    int b = 0;
    for (const auto& f : frames) b = std::max(b, brightestChannel(f));
    return b;
  };
  const int idle = brightest(watchLone(device::kDefaultTheme, "idle", 3 * kSecond));
  for (const char* state : {"working", "awaiting_input", "done", "error"}) {
    CAPTURE(state);
    CHECK(idle * 2 < brightest(watchLone(device::kDefaultTheme, state, 3 * kSecond)));
  }
}

TEST_CASE("on a 16x16 matrix the mascot's 8x8 pose is scaled up twice") {
  device::HardwareProfile panel16;
  panel16.width = 16;
  panel16.height = 16;
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    const auto big = watchLone(device::kDefaultTheme, state, 2 * kSecond, 100, panel16);
    const auto small = watchLone(device::kDefaultTheme, state, 2 * kSecond, 100);
    for (size_t i = 0; i < big.size(); ++i)
      for (uint8_t y = 0; y < 16; ++y)
        for (uint8_t x = 0; x < 16; ++x) REQUIRE(big[i].at(x, y) == small[i].at(x / 2, y / 2));
  }
}

TEST_CASE("on a matrix that isn't a multiple of the pose, the pose is centered") {
  device::HardwareProfile wide;
  wide.width = 12;
  wide.height = 8;
  const auto frames = watchLone(device::kDefaultTheme, "done", kSecond, 100, wide);
  const auto small = watchLone(device::kDefaultTheme, "done", kSecond, 100);
  for (size_t i = 0; i < frames.size(); ++i)
    for (uint8_t y = 0; y < 8; ++y) {
      for (uint8_t x = 0; x < 2; ++x) CHECK_FALSE(lit(frames[i].at(x, y)));
      for (uint8_t x = 0; x < 8; ++x) CHECK(frames[i].at(x + 2, y) == small[i].at(x, y));
      for (uint8_t x = 10; x < 12; ++x) CHECK_FALSE(lit(frames[i].at(x, y)));
    }
}

TEST_CASE("Tiles under a Character Theme fall back to the abstract Theme's effects") {
  auto tiles = [](const std::string& theme) {
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, themed(theme));
    int i = 0;
    for (const char* state : kStatesByUrgency) core.receive(upsertTile(state, state, "#3a86ff", i++));
    std::vector<device::PixelBuffer> frames;
    for (uint32_t t = 0; t < 3 * kSecond; t += 50, clock.advance(50)) {
      if (t % kSecond == 0) core.receive(heartbeat(clock));
      frames.push_back(core.render());
    }
    return frames;
  };

  CHECK(tiles(device::kDefaultTheme) == tiles(device::kAbstractTheme));
}

TEST_CASE("the Overflow Tile under a Character Theme is lit in the Session State's own color") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  for (int i = 0; i < 7; ++i) core.receive(upsertTile("s" + std::to_string(i), "done", "#3a86ff", i));
  core.receive(upsertTile("s7", "done", "#2ad14f", 7));
  core.receive(upsertTile("s8", "done", "#ff3da5", 8));

  const device::PixelBuffer frame = core.render();

  // Done's own color in the default Theme: its palette's first color.
  for (uint8_t y = 0; y < 8; ++y) CHECK(frame.at(7, y) == device::Rgb{0x00, 0xc8, 0x3c});
}

TEST_CASE("Escalation goes on with the Character Theme: the pose pulses, then a Takeover, then a steady glow") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("a", "awaiting_input", "#2ad14f"));
  int dimmest = 255, brightest = 0;
  for (uint32_t t = 0; t < 20 * kSecond; t += 20, clock.advance(20)) {
    if (t % kSecond == 0) core.receive(heartbeat(clock));
    const auto& frame = core.render();
    CHECK(isPose(frame));
    dimmest = std::min(dimmest, brightestChannel(frame));
    brightest = std::max(brightest, brightestChannel(frame));
  }
  CHECK(brightest - dimmest >= 40);  // the gentle pulse dims the whole pose

  for (uint32_t t = 0; t < 2 * kMinute; t += kSecond, clock.advance(kSecond)) core.receive(heartbeat(clock));
  const device::PixelBuffer takeover = core.render();
  for (uint8_t y = 0; y < 8; ++y)
    for (uint8_t x = 0; x < 8; ++x) CHECK(takeover.at(x, y) == device::Rgb{0x2a, 0xd1, 0x4f});

  for (uint32_t t = 0; t < 10 * kMinute; t += kSecond, clock.advance(kSecond)) core.receive(heartbeat(clock));
  CHECK(isPose(core.render()));  // settled: the pose again, steady in brightness
}

TEST_CASE("Quiet Mode hides the mascot except Awaiting Input") {
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    FakeClock clock;
    device::Preferences quiet;
    quiet.quietMode = true;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, quiet);
    core.receive(upsert("a", state));
    CHECK(isDark(core.render()) == (std::string(state) != "awaiting_input"));
  }
}

// --- Selecting a Theme ------------------------------------------------------

TEST_CASE("the theme Preference selects between the Themes the Device has") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("a", "done"));
  REQUIRE(isPose(core.render()));

  core.receive(preferences(R"("theme":"minimal")"));
  CHECK(countLit(core.render()) == 64);

  core.receive(preferences(R"("theme":"default")"));
  CHECK(isPose(core.render()));
}

TEST_CASE("a Theme the Device doesn't have shows as the default Theme") {
  FakeClock clock, defaultClock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, themed("not-installed"));
  device::DeviceCore reference(profile8x8(), "0.1.0", defaultClock);
  for (const char* state : kStatesByUrgency) {
    core.receive(upsert("a", state));
    reference.receive(upsert("a", state));
    CHECK(core.render() == reference.render());
  }
  CHECK(core.preferences().theme == "not-installed");  // kept, for when it is installed
}

// --- Installing Themes and Sprite Packs --------------------------------------

TEST_CASE("the install fixtures are accepted and the Theme renders once selected") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  CHECK(parsed(core.receive(fixture("install-sprite-pack.jsonl")))["ok"] == true);

  const std::string reply = core.receive(fixture("install-theme.jsonl"));

  CHECK_MESSAGE(parsed(reply) == parsed(fixture("install-reply.jsonl")), "reply: " << reply);
  core.receive(preferences(R"("theme":"ember")"));
  core.receive(upsert("a", "awaiting_input"));
  CHECK(core.render().at(0, 0) != device::Rgb{});
  CHECK(countLit(core.render()) == 64);
  // Done is a pose of the "dots" pack, its 4x4 frames scaled up twice.
  core.receive(upsert("a", "done"));
  const device::PixelBuffer done = core.render();
  CHECK(done.at(0, 0) == device::Rgb{0xff, 0xff, 0xff});
  CHECK(done.at(1, 1) == device::Rgb{0xff, 0xff, 0xff});
  CHECK_FALSE(lit(done.at(2, 0)));
  clock.advance(500);
  CHECK_FALSE(lit(core.render().at(0, 0)));
  CHECK(core.render().at(2, 2) == device::Rgb{0xff, 0xff, 0xff});
}

TEST_CASE("the hello reply lists the Themes and Sprite Packs the Device has") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(fixture("install-sprite-pack.jsonl"));
  core.receive(fixture("install-theme.jsonl"));

  const JsonDocument reply = parsed(core.receive(fixture("hello.jsonl")));

  CHECK(reply["themes"] == parsed(R"(["default","minimal","ember"])").as<JsonVariantConst>());
  CHECK(reply["sprite_packs"] == parsed(R"(["nino","dots"])").as<JsonVariantConst>());
}

TEST_CASE("installed Themes and Sprite Packs live in the store and survive a reboot") {
  FakeStore store;
  {
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, {}, nullptr, &store);
    core.receive(fixture("install-sprite-pack.jsonl"));
    core.receive(fixture("install-theme.jsonl"));
  }
  CHECK(store.writes == 2);

  FakeClock clock;
  device::DeviceCore rebooted(profile8x8(), "0.1.0", clock, themed("ember"), nullptr, &store);
  rebooted.receive(upsert("a", "done"));

  CHECK(rebooted.render().at(0, 0) == device::Rgb{0xff, 0xff, 0xff});
  CHECK(parsed(rebooted.receive(fixture("hello.jsonl")))["themes"][2] == "ember");
}

TEST_CASE("a Sprite Pack installed after its Theme brings the Theme's poses") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, themed("ember"));
  core.receive(fixture("install-theme.jsonl"));
  core.receive(upsert("a", "done"));
  // Without the pack the pose can't show: the abstract Done in the Theme's color.
  CHECK(countLit(core.render()) == 64);
  CHECK(core.render().at(0, 0) == device::Rgb{0x40, 0xff, 0x40});

  core.receive(fixture("install-sprite-pack.jsonl"));

  CHECK(isPose(core.render()));
}

TEST_CASE("installing again under the same name replaces the Theme") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, themed("mono"));
  core.receive(upsert("a", "error"));
  core.receive(themeWithEveryState("mono", R"({"palette":["#ff0000"],"effect":{"name":"steady"}})"));
  CHECK(core.render().at(0, 0) == device::Rgb{0xff, 0, 0});

  core.receive(themeWithEveryState("mono", R"({"palette":["#0000ff"],"effect":{"name":"steady"}})"));

  CHECK(core.render().at(0, 0) == device::Rgb{0, 0, 0xff});
}

TEST_CASE("an install the Device can't use is refused with the reason, and nothing changes") {
  const std::string effect = R"({"palette":["#ffffff"],"effect":{"name":"steady"}})";
  const struct {
    const char* why;
    std::string line;
    const char* error;
  } refused[] = {
      {"unknown kind", R"({"type":"install","kind":"font","name":"x"})", "kind"},
      {"bad name", themeWithEveryState("Bad Name", effect), "name"},
      {"a built-in name", themeWithEveryState("minimal", effect), "built in"},
      {"a state missing", R"({"type":"install","kind":"theme","name":"x","states":{"idle":)" + effect + "}}",
       "states.working"},
      {"unknown effect", themeWithEveryState("x", R"({"palette":["#ffffff"],"effect":{"name":"sparkle"}})"),
       "effect"},
      {"bad color", themeWithEveryState("x", R"({"palette":["red"],"effect":{"name":"steady"}})"), "palette"},
      {"no palette", themeWithEveryState("x", R"({"palette":[],"effect":{"name":"steady"}})"), "palette"},
      {"effect and sprite", themeWithEveryState("x", R"({"palette":["#ffffff"],"effect":{"name":"steady"},)"
                                                     R"("sprite":{"pack":"p","frames":["a"]}})"),
       "either"},
      {"level out of range", themeWithEveryState("x", R"({"palette":["#ffffff"],"effect":{"name":"steady","level":300}})"),
       "level"},
      {"level and high", themeWithEveryState("x", R"({"palette":["#ffffff"],"effect":{"name":"blink","level":9,"high":9}})"),
       "not both"},
      {"sprite frame too fast",
       themeWithEveryState("x", R"({"palette":["#ffffff"],"sprite":{"pack":"p","frames":["a"],"frame_ms":5}})"),
       "frame_ms"},
      {"pack too big", R"({"type":"install","kind":"sprite_pack","name":"p","width":17,"height":8,"frames":{"a":[]}})",
       "width"},
      {"row too short", R"({"type":"install","kind":"sprite_pack","name":"p","width":2,"height":1,"frames":{"a":["1"]}})",
       "characters"},
      {"too few rows", R"({"type":"install","kind":"sprite_pack","name":"p","width":1,"height":2,"frames":{"a":["1"]}})",
       "rows"},
      {"bad pixel", R"({"type":"install","kind":"sprite_pack","name":"p","width":1,"height":1,"frames":{"a":["g"]}})",
       "pixels"},
      {"no frames", R"({"type":"install","kind":"sprite_pack","name":"p","width":1,"height":1,"frames":{}})", "frames"},
  };
  for (const auto& r : refused) {
    CAPTURE(r.why);
    FakeStore store;
    FakeClock clock;
    device::DeviceCore core(profile8x8(), "0.1.0", clock, {}, nullptr, &store);
    core.receive(upsert("a", "done"));
    const device::PixelBuffer before = core.render();

    const JsonDocument reply = parsed(core.receive(r.line));

    CHECK(reply["type"] == "install_reply");
    CHECK(reply["ok"] == false);
    CHECK_MESSAGE(std::string(reply["error"] | "").find(r.error) != std::string::npos, "error: " << reply["error"]);
    CHECK(store.writes == 0);
    CHECK(core.render() == before);
  }
}

TEST_CASE("a Device holds at most 8 installed Themes, and as many Sprite Packs") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  const std::string effect = R"({"palette":["#ffffff"],"effect":{"name":"steady"}})";
  for (int i = 0; i < 8; ++i)
    REQUIRE(parsed(core.receive(themeWithEveryState("t" + std::to_string(i), effect)))["ok"] == true);

  const JsonDocument full = parsed(core.receive(themeWithEveryState("t8", effect)));

  CHECK(full["ok"] == false);
  CHECK(std::string(full["error"] | "").find("uninstall") != std::string::npos);
  // Replacing one that is installed still works.
  CHECK(parsed(core.receive(themeWithEveryState("t3", effect)))["ok"] == true);
}

TEST_CASE("uninstall removes an installed Theme; selected, the Device shows the default Theme instead") {
  FakeStore store;
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, themed("ember"), nullptr, &store);
  core.receive(fixture("install-sprite-pack.jsonl"));
  core.receive(fixture("install-theme.jsonl"));
  core.receive(upsert("a", "awaiting_input"));
  REQUIRE(countLit(core.render()) == 64);

  const JsonDocument reply = parsed(core.receive(fixture("uninstall.jsonl")));

  CHECK(reply["ok"] == true);
  CHECK(reply["themes"] == parsed(R"(["default","minimal"])").as<JsonVariantConst>());
  CHECK(store.load(device::InstallKind::Theme, "ember").empty());
  CHECK(isPose(core.render()));
  // Neither built-ins nor what isn't there can be uninstalled.
  CHECK(parsed(core.receive(R"({"type":"uninstall","kind":"theme","name":"minimal"})"))["ok"] == false);
  CHECK(parsed(core.receive(fixture("uninstall.jsonl")))["ok"] == false);
}

TEST_CASE("an install counts as hearing from the Host") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);
  core.receive(upsert("a", "done"));
  const device::PixelBuffer connected = core.render();
  clock.advance(device::kDefaultDisconnectedTimeoutMs - 1);

  core.receive(fixture("install-sprite-pack.jsonl"));
  clock.advance(2);

  CHECK(isPose(core.render()));
}

TEST_CASE("a line that doesn't fit a Device's line buffer, newline included, is ignored") {
  // The sprite pack fixture padded to `length` bytes without its newline.
  auto padded = [](size_t length) {
    std::string line = fixture("install-sprite-pack.jsonl");
    const std::string field = R"(,"padding":"")";
    line.insert(line.size() - 1, R"(,"padding":")" + std::string(length - line.size() - field.size(), 'x') + "\"");
    REQUIRE(line.size() == length);
    return line;
  };
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock);

  CHECK(core.receive(padded(device::DeviceCore::kMaxLineLength)).empty());
  CHECK_FALSE(core.receive(padded(device::DeviceCore::kMaxLineLength - 1)).empty());
}

// --- Procedural effects -------------------------------------------------------

TEST_CASE("steady holds one level") {
  const auto frames = effectFrames(R"({"name":"steady","level":128})", 2 * kSecond);
  for (const auto& f : frames) CHECK(f.at(3, 3) == device::Rgb{128, 128, 128});
  for (const auto& f : frames) CHECK(countLit(f) == 64);
}

TEST_CASE("pulse and breathe rise and fall between low and high, once a period") {
  for (const char* name : {"pulse", "breathe"}) {
    CAPTURE(name);
    const auto frames =
        effectFrames(std::string(R"({"name":")") + name + R"(","period_ms":1000,"low":40,"high":200})", 3 * kSecond);
    int dimmest = 255, brightest = 0, peaks = 0, previous = -1;
    bool rising = false;
    for (const auto& f : frames) {
      CHECK(countLit(f) == 64);
      const int level = f.at(0, 0).r;
      dimmest = std::min(dimmest, level);
      brightest = std::max(brightest, level);
      if (previous >= 0 && level < previous && rising) ++peaks;
      if (previous >= 0 && level != previous) rising = level > previous;
      previous = level;
    }
    CHECK(dimmest <= 45);
    CHECK(dimmest >= 40);
    CHECK(brightest >= 195);
    CHECK(brightest <= 200);
    CHECK(peaks == 3);
  }
}

TEST_CASE("breathe lingers at its ends longer than pulse") {
  auto nearEnds = [](const std::vector<device::PixelBuffer>& frames) {
    return std::count_if(frames.begin(), frames.end(), [](const auto& f) {
      return f.at(0, 0).r < 60 || f.at(0, 0).r > 180;
    });
  };
  CHECK(nearEnds(effectFrames(R"({"name":"breathe","period_ms":1000,"low":40,"high":200})", 2 * kSecond)) >
        nearEnds(effectFrames(R"({"name":"pulse","period_ms":1000,"low":40,"high":200})", 2 * kSecond)));
}

TEST_CASE("blink is on for half its period, then off") {
  const auto frames = effectFrames(R"({"name":"blink","period_ms":400})", 2 * kSecond);
  // One frame every 20 ms: runs of 10 frames all on, then 10 all off.
  std::vector<int> runs{1};
  for (size_t i = 1; i < frames.size(); ++i) {
    REQUIRE((countLit(frames[i]) == 0 || countLit(frames[i]) == 64));
    if (countLit(frames[i]) == countLit(frames[i - 1])) ++runs.back();
    else runs.push_back(1);
  }
  REQUIRE(runs.size() >= 4);
  for (size_t i = 1; i + 1 < runs.size(); ++i) CHECK(runs[i] == 10);
}

TEST_CASE("scroll moves a bright line across a dimmer matrix, in its direction") {
  const struct {
    const char* direction;
    int dx, dy;
  } directions[] = {{"down", 0, 1}, {"up", 0, -1}, {"right", 1, 0}, {"left", -1, 0}};
  for (const auto& d : directions) {
    CAPTURE(d.direction);
    const auto frames = effectFrames(std::string(R"({"name":"scroll","period_ms":100,"low":50,"high":250,"direction":")") +
                                         d.direction + "\"}",
                                     kSecond);
    // Where the bright line is in each frame: a row or a column of 250s.
    auto line = [&](const device::PixelBuffer& f) {
      for (int i = 0; i < 8; ++i) {
        const auto x = static_cast<uint8_t>(d.dx != 0 ? i : 0), y = static_cast<uint8_t>(d.dy != 0 ? i : 0);
        if (f.at(x, y).r == 250) return i;
      }
      return -1;
    };
    for (const auto& f : frames) CHECK(countLit(f) == 64);
    // Every 100 ms (5 frames) the line moves one step in the direction.
    for (size_t i = 5; i < frames.size(); i += 5) {
      const int step = d.dx + d.dy;
      CHECK((line(frames[i]) - line(frames[i - 5]) + 8) % 8 == (step + 8) % 8);
    }
  }
}

// --- The example files in themes/examples ------------------------------------

namespace {

// A Theme or Sprite Pack file as `codino theme install` sends it: one line,
// with the type added.
std::string installLine(const std::string& file) {
  std::ifstream in(std::string(THEME_EXAMPLES_DIR) + "/" + file, std::ios::binary);
  REQUIRE_MESSAGE(in.good(), "missing themes/examples/" << file);
  JsonDocument doc;
  REQUIRE_FALSE(deserializeJson(doc, in));
  doc["type"] = "install";
  std::string line;
  serializeJson(doc, line);
  return line;
}

}  // namespace

TEST_CASE("the example Theme and Sprite Pack files install, and the Character Theme shows its poses") {
  FakeClock clock;
  device::DeviceCore core(profile8x8(), "0.1.0", clock, themed("my-character"));
  for (const char* file : {"my-pack.json", "my-character.json", "neon.json"}) {
    CAPTURE(file);
    const std::string line = installLine(file);
    CHECK(line.size() < device::DeviceCore::kMaxLineLength);
    const JsonDocument reply = parsed(core.receive(line));
    CHECK_MESSAGE(reply["ok"] == true, "error: " << reply["error"]);
  }
  for (const char* state : kStatesByUrgency) {
    CAPTURE(state);
    core.receive(upsert("a", state));
    CHECK(isPose(core.render()));
  }
}
