#include "device_core.h"

#include <ArduinoJson.h>

#include <algorithm>

namespace device {

namespace {

// Everything the core knows about each Session State, in one place.
// `urgency`: higher wins the Aggregate State (Awaiting Input > Error > Done >
// Working > Idle). `color`: the v0 solid color for the whole matrix.
struct SessionStateInfo {
  SessionState state;
  const char* wireName;
  int urgency;
  Rgb color;
};

constexpr SessionStateInfo kSessionStates[] = {
    {SessionState::Idle, "idle", 0, {16, 16, 24}},
    {SessionState::Working, "working", 1, {0, 90, 255}},
    {SessionState::Done, "done", 2, {0, 200, 60}},
    {SessionState::Error, "error", 3, {255, 0, 0}},
    {SessionState::AwaitingInput, "awaiting_input", 4, {255, 150, 0}},
};

const SessionStateInfo* findByWireName(std::string_view name) {
  for (const auto& info : kSessionStates)
    if (name == info.wireName) return &info;
  return nullptr;
}

const SessionStateInfo& infoFor(SessionState state) {
  for (const auto& info : kSessionStates)
    if (info.state == state) return info;
  return kSessionStates[0];
}

// Parses a Project Color, "#rrggbb". False when it isn't one.
bool parseColor(std::string_view text, Rgb& color) {
  if (text.size() != 7 || text[0] != '#') return false;
  int channels[3];
  for (int i = 0; i < 3; ++i) {
    int value = 0;
    for (char c : text.substr(1 + 2 * i, 2)) {
      int digit;
      if (c >= '0' && c <= '9') digit = c - '0';
      else if (c >= 'a' && c <= 'f') digit = c - 'a' + 10;
      else if (c >= 'A' && c <= 'F') digit = c - 'A' + 10;
      else return false;
      value = value * 16 + digit;
    }
    channels[i] = value;
  }
  color = {static_cast<uint8_t>(channels[0]), static_cast<uint8_t>(channels[1]), static_cast<uint8_t>(channels[2])};
  return true;
}

// `color` at `level` of its full brightness (255 = unchanged).
Rgb scaled(Rgb color, int level) {
  auto channel = [level](uint8_t c) { return static_cast<uint8_t>(c * level / 255); };
  return {channel(color.r), channel(color.g), channel(color.b)};
}

// A pulse between brightness `low` and `high` (of 255) with the given period:
// a triangle wave starting at `low`, `elapsed` ms in.
struct Pulse {
  uint32_t periodMs;
  int low, high;

  int level(uint32_t elapsed) const {
    const uint32_t half = periodMs / 2;
    const uint32_t phase = elapsed % periodMs;
    const uint32_t up = phase < half ? phase : periodMs - phase;
    return low + static_cast<int>((high - low) * up / half);
  }
};

constexpr Pulse kGentlePulse{2000, 90, 170};
constexpr Pulse kFasterPulse{700, 120, 255};
// The steady glow's brightness: Escalation settles, so lunch isn't a strobe.
constexpr int kSteadyGlow = 170;

}  // namespace

DeviceCore::DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock,
                       Escalation escalation)
    : profile_(std::move(profile)),
      firmwareVersion_(std::move(firmwareVersion)),
      clock_(clock),
      escalation_(escalation),
      frame_(profile_.width, profile_.height),
      lastHostMessage_(clock.millis()) {}

std::string DeviceCore::receive(std::string_view line) {
  JsonDocument message;
  if (deserializeJson(message, line.data(), line.size())) return {};

  // Every message the Device can use proves the Host is alive.
  std::string_view type = message["type"] | "";
  if (type == "hello") {
    // A new link: the Host follows with every session it knows, so sessions
    // from an earlier Host Daemon must not linger.
    sessions_.clear();
    if (message["host_time"].is<int64_t>()) heardHostTime(message["host_time"]);
    hostMessage();
    return helloReply();
  } else if (type == "session_upsert") {
    std::string_view id = message["id"] | "";
    const SessionStateInfo* state = findByWireName(message["state"] | "");
    if (id.empty() || state == nullptr) return {};
    auto [it, created] = sessions_.try_emplace(std::string(id));
    Session& session = it->second;
    if (created || session.state != state->state) {
      // A Device claimed again learns how long a session has been waiting.
      session.escalationStart = clockTimeOf(message["state_entered"] | int64_t{0});
      session.settled = false;
    }
    session.state = state->state;
    // Without a valid Project Color, a Takeover shows the Session State's.
    if (!parseColor(message["project_color"] | "", session.projectColor)) session.projectColor = state->color;
  } else if (type == "session_remove") {
    if (!message["id"].is<const char*>()) return {};
    sessions_.erase(std::string(message["id"].as<const char*>()));
  } else if (type == "acknowledge") {
    const uint32_t at = clockTimeOf(message["host_time"] | int64_t{0});
    if (message["id"].isNull()) {
      for (auto& [id, session] : sessions_) acknowledge(session, at);
    } else {
      std::string_view id = message["id"] | "";
      if (id.empty()) return {};
      auto it = sessions_.find(std::string(id));
      if (it != sessions_.end()) acknowledge(it->second, at);
    }
  } else if (type == "heartbeat") {
    if (message["host_time"].is<int64_t>()) heardHostTime(message["host_time"]);
  } else {
    return {};
  }
  hostMessage();
  return {};
}

std::string DeviceCore::helloReply() const {
  JsonDocument reply;
  reply["type"] = "hello_reply";
  reply["protocol"] = kProtocolVersion;
  reply["firmware"] = firmwareVersion_;
  JsonObject hw = reply["hardware_profile"].to<JsonObject>();
  hw["width"] = profile_.width;
  hw["height"] = profile_.height;
  hw["data_pin"] = profile_.dataPin;
  hw["serpentine"] = profile_.serpentine;
  hw["color_order"] = profile_.colorOrder;
  hw["brightness_cap"] = profile_.brightnessCap;
  hw["current_cap_ma"] = profile_.currentCapMa;
  hw["button"] = profile_.button;
  hw["imu"] = profile_.imu;
  std::string line;
  serializeJson(reply, line);
  return line;
}

// Acknowledge clears Done and restarts Escalation; Awaiting Input stays. An
// Acknowledge older than the Escalation start changes nothing about it.
void DeviceCore::acknowledge(Session& session, uint32_t at) {
  if (session.state == SessionState::Done) session.state = SessionState::Idle;
  const uint32_t now = clock_.millis();
  if (session.settled || now - at < now - session.escalationStart) {
    session.escalationStart = at;
    session.settled = false;
  }
}

void DeviceCore::heardHostTime(int64_t hostTime) {
  hostTimeKnown_ = true;
  hostTime_ = hostTime;
  hostTimeHeardAt_ = clock_.millis();
}

// The clock time a Host time corresponds to; now when the Host time is unknown
// (0) or no Host time has been heard. Times in the Host's future count as now,
// and the distant past as a day ago, past every Escalation step.
uint32_t DeviceCore::clockTimeOf(int64_t hostTime) const {
  constexpr int64_t kLongAgo = 24LL * 60 * 60 * 1000;
  const uint32_t now = clock_.millis();
  if (!hostTimeKnown_ || hostTime <= 0) return now;
  const int64_t hostNow = hostTime_ + static_cast<uint32_t>(now - hostTimeHeardAt_);
  const int64_t age = std::clamp<int64_t>(hostNow - hostTime, 0, kLongAgo);
  return now - static_cast<uint32_t>(age);
}

std::string DeviceCore::acknowledge() {
  for (auto& [id, session] : sessions_) acknowledge(session, clock_.millis());
  return R"({"type":"acknowledge"})";
}

SessionState DeviceCore::aggregateState() const {
  SessionState aggregate = SessionState::Idle;
  for (const auto& [id, session] : sessions_) {
    if (infoFor(session.state).urgency > infoFor(aggregate).urgency) aggregate = session.state;
  }
  return aggregate;
}

// Unsigned subtraction keeps working when millis() wraps (every ~49.7 days).
bool DeviceCore::disconnected() {
  if (clock_.millis() - lastHostMessage_ >= kDefaultDisconnectedTimeoutMs) disconnected_ = true;
  return disconnected_;
}

void DeviceCore::hostMessage() {
  lastHostMessage_ = clock_.millis();
  disconnected_ = false;
}

// Disconnected is not a Session State: a dim slash across an otherwise dark
// matrix, so it never reads as Idle (a full, dim fill) or as any other state.
void DeviceCore::renderDisconnected() {
  constexpr Rgb kDisconnected{20, 0, 28};
  frame_.fill(Rgb{});
  const int w = profile_.width, h = profile_.height;
  for (int y = 0; y < h; ++y) {
    const int x = (w - 1) - (y * w) / h;
    frame_.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), kDisconnected);
  }
}

// Awaiting Input escalates per session; the matrix shows the most insistent
// step any waiting session has reached: Takeover, then the faster pulse, then
// the gentle pulse, then the steady glow.
void DeviceCore::renderAwaitingInput() {
  const uint32_t now = clock_.millis();
  std::vector<Rgb> takeover;  // the Project Colors in Takeover, in session id order
  bool faster = false;
  bool gentle = false;
  uint32_t pulseElapsed = 0;  // of the session whose pulse is shown
  for (auto& [id, session] : sessions_) {
    if (session.state != SessionState::AwaitingInput) continue;
    const uint32_t waited = now - session.escalationStart;
    if (session.settled || waited >= escalation_.takeoverMs) {
      session.settled = true;  // steady glow
      continue;
    }
    if (waited >= escalation_.fasterMs) {
      takeover.push_back(session.projectColor);
    } else if (waited >= escalation_.gentleMs) {
      if (!faster) pulseElapsed = waited;
      faster = true;
    } else if (!faster && !gentle) {
      pulseElapsed = waited;
      gentle = true;
    }
  }
  const Rgb color = infoFor(SessionState::AwaitingInput).color;
  if (!takeover.empty()) {
    const uint32_t cycle = std::max<uint32_t>(escalation_.takeoverCycleMs, 1);
    frame_.fill(takeover[(now / cycle) % takeover.size()]);
  } else if (faster) {
    frame_.fill(scaled(color, kFasterPulse.level(pulseElapsed)));
  } else if (gentle) {
    frame_.fill(scaled(color, kGentlePulse.level(pulseElapsed)));
  } else {
    frame_.fill(scaled(color, kSteadyGlow));
  }
}

const PixelBuffer& DeviceCore::render() {
  if (disconnected()) {
    renderDisconnected();
  } else if (sessions_.empty()) {
    frame_.fill(Rgb{});
  } else if (aggregateState() == SessionState::AwaitingInput) {
    renderAwaitingInput();
  } else {
    frame_.fill(infoFor(aggregateState()).color);
  }
  return frame_;
}

}  // namespace device
