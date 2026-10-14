#include "device_core.h"

#include <ArduinoJson.h>

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

}  // namespace

DeviceCore::DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock)
    : profile_(std::move(profile)),
      firmwareVersion_(std::move(firmwareVersion)),
      clock_(clock),
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
    hostMessage();
    return helloReply();
  } else if (type == "session_upsert") {
    std::string_view id = message["id"] | "";
    const SessionStateInfo* state = findByWireName(message["state"] | "");
    if (id.empty() || state == nullptr) return {};
    sessions_[std::string(id)] = state->state;
  } else if (type == "session_remove") {
    if (!message["id"].is<const char*>()) return {};
    sessions_.erase(std::string(message["id"].as<const char*>()));
  } else if (type != "heartbeat") {
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

SessionState DeviceCore::aggregateState() const {
  SessionState aggregate = SessionState::Idle;
  for (const auto& [id, state] : sessions_) {
    if (infoFor(state).urgency > infoFor(aggregate).urgency) aggregate = state;
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

const PixelBuffer& DeviceCore::render() {
  if (disconnected()) {
    renderDisconnected();
  } else if (sessions_.empty()) {
    frame_.fill(Rgb{});
  } else {
    frame_.fill(infoFor(aggregateState()).color);
  }
  return frame_;
}

}  // namespace device
