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

class DeviceCore {
 public:
  static constexpr int kProtocolVersion = 0;
  // How long the Host may stay silent before the Device shows Disconnected.
  // Becomes a Preference later.
  static constexpr uint32_t kDefaultDisconnectedTimeoutMs = 5000;

  DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock);

  // Feeds one protocol line (without the trailing newline). Returns the reply
  // line to send back, or an empty string when there is none.
  std::string receive(std::string_view line);

  // Renders the frame for the current clock time.
  const PixelBuffer& render();

 private:
  SessionState aggregateState() const;
  std::string helloReply() const;

  HardwareProfile profile_;
  std::string firmwareVersion_;
  bool disconnected() const;
  void renderDisconnected();

  const Clock& clock_;
  PixelBuffer frame_;
  std::map<std::string, SessionState> sessions_;
  // Clock time of the last message from the Host (boot until the first).
  uint32_t lastHostMessage_;
};

}  // namespace device
