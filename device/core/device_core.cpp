#include "device_core.h"

#include <ArduinoJson.h>

namespace device {

namespace {

bool parseSessionState(std::string_view text, SessionState& out) {
  if (text == "idle") out = SessionState::Idle;
  else if (text == "working") out = SessionState::Working;
  else if (text == "awaiting_input") out = SessionState::AwaitingInput;
  else if (text == "done") out = SessionState::Done;
  else if (text == "error") out = SessionState::Error;
  else return false;
  return true;
}

// Higher is more urgent: Awaiting Input > Error > Done > Working > Idle.
int urgency(SessionState state) {
  switch (state) {
    case SessionState::Idle: return 0;
    case SessionState::Working: return 1;
    case SessionState::Done: return 2;
    case SessionState::Error: return 3;
    case SessionState::AwaitingInput: return 4;
  }
  return 0;
}

Rgb colorFor(SessionState state) {
  switch (state) {
    case SessionState::Idle: return {16, 16, 24};
    case SessionState::Working: return {0, 90, 255};
    case SessionState::AwaitingInput: return {255, 150, 0};
    case SessionState::Done: return {0, 200, 60};
    case SessionState::Error: return {255, 0, 0};
  }
  return {};
}

}  // namespace

DeviceCore::DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock)
    : profile_(std::move(profile)),
      firmwareVersion_(std::move(firmwareVersion)),
      clock_(clock),
      frame_(profile_.width, profile_.height) {}

std::string DeviceCore::receive(std::string_view line) {
  JsonDocument message;
  if (deserializeJson(message, line.data(), line.size())) return {};

  std::string_view type = message["type"] | "";
  if (type == "hello") {
    return helloReply();
  } else if (type == "session_upsert") {
    std::string_view id = message["id"] | "";
    SessionState state;
    if (id.empty() || !parseSessionState(message["state"] | "", state)) return {};
    sessions_[std::string(id)] = state;
  } else if (type == "session_remove") {
    sessions_.erase(std::string(message["id"] | ""));
  }
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
    if (urgency(state) > urgency(aggregate)) aggregate = state;
  }
  return aggregate;
}

const PixelBuffer& DeviceCore::render() {
  if (sessions_.empty()) {
    frame_.fill(Rgb{});
  } else {
    frame_.fill(colorFor(aggregateState()));
  }
  return frame_;
}

}  // namespace device
