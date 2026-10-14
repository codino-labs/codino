#include "led_wiring.h"

namespace device {

LedWiring::LedWiring(const HardwareProfile& profile) : width_(profile.width), height_(profile.height), serpentine_(profile.serpentine) {
  uint8_t channels[3];
  bool seen[3] = {false, false, false};
  if (profile.colorOrder.size() != 3) return;
  for (size_t i = 0; i < 3; ++i) {
    const char letter = profile.colorOrder[i];
    const int channel = letter == 'R' ? 0 : letter == 'G' ? 1 : letter == 'B' ? 2 : -1;
    if (channel < 0 || seen[channel]) return;
    seen[channel] = true;
    channels[i] = static_cast<uint8_t>(channel);
  }
  for (size_t i = 0; i < 3; ++i) channels_[i] = channels[i];
}

void LedWiring::write(const PixelBuffer& shown, uint8_t* out) const {
  for (uint8_t y = 0; y < height_; ++y) {
    // Serpentine (zig-zag) wiring turns back at the end of every row.
    const bool reversed = serpentine_ && y % 2 == 1;
    for (uint8_t i = 0; i < width_; ++i) {
      const Rgb pixel = shown.at(reversed ? width_ - 1 - i : i, y);
      const uint8_t rgb[3] = {pixel.r, pixel.g, pixel.b};
      for (uint8_t channel : channels_) *out++ = rgb[channel];
    }
  }
}

}  // namespace device
