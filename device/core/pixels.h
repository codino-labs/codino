// Pixels and Session States: what the Device core and its Themes draw with.
#pragma once

#include <cstdint>
#include <vector>

namespace device {

enum class SessionState { Idle, Working, AwaitingInput, Done, Error };

struct Rgb {
  uint8_t r = 0, g = 0, b = 0;

  friend bool operator==(Rgb lhs, Rgb rhs) { return lhs.r == rhs.r && lhs.g == rhs.g && lhs.b == rhs.b; }
  friend bool operator!=(Rgb lhs, Rgb rhs) { return !(lhs == rhs); }
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

}  // namespace device
