// Device core: protocol messages and clock ticks in, pixel buffer out.
//
// Portable C++17 with no Arduino or FastLED dependency, so the same code runs
// in the firmware shell, the terminal Virtual Device and (later) the browser.
#pragma once

#include <cstdint>
#include <map>
#include <string>
#include <string_view>
#include <vector>

namespace device {

enum class SessionState { Idle, Working, AwaitingInput, Done, Error };

struct Rgb {
  uint8_t r = 0, g = 0, b = 0;

  friend bool operator==(Rgb lhs, Rgb rhs) { return lhs.r == rhs.r && lhs.g == rhs.g && lhs.b == rhs.b; }
  friend bool operator!=(Rgb lhs, Rgb rhs) { return !(lhs == rhs); }
};

// Describes a physical build. Flashed with the firmware; reported in the
// handshake reply.
struct HardwareProfile {
  uint8_t width = 8;
  uint8_t height = 8;
  int dataPin = 0;
  bool serpentine = false;
  std::string colorOrder = "GRB";
  uint8_t brightnessCap = 255;
  uint16_t currentCapMa = 500;
  bool button = false;
  bool imu = false;
};

// Injectable time source. The firmware shell wraps millis(); tests use a fake.
class Clock {
 public:
  virtual ~Clock() = default;
  virtual uint32_t millis() const = 0;
};

// Logical pixels, row-major from the top-left. Mapping to the physical wiring
// (serpentine, color order) is the firmware shell's job.
class PixelBuffer {
 public:
  PixelBuffer(uint8_t width, uint8_t height) : width_(width), height_(height), pixels_(width * height) {}

  uint8_t width() const { return width_; }
  uint8_t height() const { return height_; }
  Rgb at(uint8_t x, uint8_t y) const { return pixels_[y * width_ + x]; }
  void fill(Rgb color) { pixels_.assign(pixels_.size(), color); }
  void set(uint8_t x, uint8_t y, Rgb color) { pixels_[y * width_ + x] = color; }

  friend bool operator==(const PixelBuffer& lhs, const PixelBuffer& rhs) {
    return lhs.width_ == rhs.width_ && lhs.height_ == rhs.height_ && lhs.pixels_ == rhs.pixels_;
  }
  friend bool operator!=(const PixelBuffer& lhs, const PixelBuffer& rhs) { return !(lhs == rhs); }

 private:
  uint8_t width_;
  uint8_t height_;
  std::vector<Rgb> pixels_;
};

// How Awaiting Input escalates the longer it goes unacknowledged. Defaults in
// code for now; they become Preferences (#11).
struct Escalation {
  // A gentle pulse until gentleMs, then faster and brighter until fasterMs,
  // then a Takeover until takeoverMs, then a steady glow.
  uint32_t gentleMs = 30 * 1000;
  uint32_t fasterMs = 2 * 60 * 1000;
  uint32_t takeoverMs = 10 * 60 * 1000;
  // How long each waiting session's Project Color holds when several
  // sessions are in Takeover.
  uint32_t takeoverCycleMs = 1000;
};

class DeviceCore {
 public:
  static constexpr int kProtocolVersion = 0;
  // How long the Host may stay silent before the Device shows Disconnected.
  // Becomes a Preference later.
  static constexpr uint32_t kDefaultDisconnectedTimeoutMs = 5000;

  DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock,
             Escalation escalation = {});

  // Feeds one protocol line (without the trailing newline). Returns the reply
  // line to send back, or an empty string when there is none.
  std::string receive(std::string_view line);

  // The human Acknowledges on the Device (button press, IMU double-tap, a key
  // on the Virtual Device): every Done session is cleared and the Escalation
  // of every Awaiting Input session starts over. Returns the line to send to
  // the Host.
  std::string acknowledge();

  // Renders the frame for the current clock time.
  const PixelBuffer& render();

 private:
  SessionState aggregateState() const;
  std::string helloReply() const;

  HardwareProfile profile_;
  std::string firmwareVersion_;
  bool disconnected();
  void hostMessage();
  void renderDisconnected();

  const Clock& clock_;
  Escalation escalation_;
  PixelBuffer frame_;
  struct Session {
    SessionState state = SessionState::Idle;
    Rgb projectColor;
    // Clock time Escalation counts from: when the session entered Awaiting
    // Input or was last Acknowledged.
    uint32_t escalationStart = 0;
    // Latched once Escalation settles into the steady glow, so waiting longer
    // than the millis() wrap (~49.7 days) never starts it over.
    bool settled = false;
  };
  void renderAwaitingInput();
  // Acknowledges the session at clock time `at`.
  void acknowledge(Session& session, uint32_t at);
  void heardHostTime(int64_t hostTime);
  uint32_t clockTimeOf(int64_t hostTime) const;

  std::map<std::string, Session> sessions_;
  // The latest Host time heard (hello, heartbeat) and the clock time it was
  // heard at, to relate Host times to the clock.
  bool hostTimeKnown_ = false;
  int64_t hostTime_ = 0;
  uint32_t hostTimeHeardAt_ = 0;
  // Clock time of the last message from the Host (boot until the first).
  uint32_t lastHostMessage_;
  // Latched once the timeout passes, until the next Host message, so a
  // silence longer than the millis() wrap (~49.7 days) never ends it.
  bool disconnected_ = false;
};

}  // namespace device
