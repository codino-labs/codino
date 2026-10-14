// Helpers shared by the Device core tests: a fake clock, protocol fixtures
// and the session messages the tests send.
#pragma once

#include <doctest.h>

#include <cstdint>
#include <fstream>
#include <string>

#include "device_core.h"

namespace testsupport {

class FakeClock : public device::Clock {
 public:
  uint32_t millis() const override { return now; }
  void advance(uint32_t ms) { now += ms; }
  uint32_t now = 1000;
};

// The Host clock of the tests: it reads kStateEntered when a FakeClock starts
// (at 1000 ms), so a session upserted then has just entered its state.
constexpr int64_t kStateEntered = 1760432395000;
inline int64_t hostTime(const FakeClock& clock) { return kStateEntered + clock.now - 1000; }

inline std::string heartbeat(const FakeClock& clock) {
  return R"({"type":"heartbeat","host_time":)" + std::to_string(hostTime(clock)) + "}";
}

inline device::HardwareProfile profile8x8() {
  device::HardwareProfile p;
  p.width = 8;
  p.height = 8;
  return p;
}

// Reads the single message of a protocol fixture, without its newline.
inline std::string fixture(const std::string& name) {
  std::ifstream in(std::string(PROTOCOL_FIXTURES_DIR) + "/" + name, std::ios::binary);
  REQUIRE_MESSAGE(in.good(), "missing fixture " << name);
  std::string line;
  std::getline(in, line);
  return line;
}

inline std::string upsertTile(const std::string& id, const std::string& state, const std::string& projectColor,
                              int tile, int64_t stateEntered = kStateEntered) {
  return R"({"type":"session_upsert","id":")" + id + R"(","state":")" + state + R"(","project_color":")" +
         projectColor + R"(","state_entered":)" + std::to_string(stateEntered) + R"(,"tile":)" +
         std::to_string(tile) + "}";
}

inline std::string upsert(const std::string& id, const std::string& state,
                          const std::string& projectColor = "#3a86ff", int64_t stateEntered = kStateEntered) {
  return upsertTile(id, state, projectColor, 0, stateEntered);
}

inline std::string preferences(const std::string& fields) {
  return R"({"type":"preferences")" + (fields.empty() ? "" : "," + fields) + "}";
}

inline bool isDark(const device::PixelBuffer& frame) {
  return frame == device::PixelBuffer(frame.width(), frame.height());
}

// The wire names of the Session States, most urgent first.
inline const char* const kStatesByUrgency[] = {"awaiting_input", "error", "done", "working", "idle"};

}  // namespace testsupport
