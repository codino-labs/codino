#include "led_wiring.h"

#include "hardware_profile.h"

namespace device {

LedWiring::LedWiring(const HardwareProfile& profile) : width_(profile.width), height_(profile.height), serpentine_(profile.serpentine) {
  if (!validColorOrder(profile.colorOrder)) return;
  for (size_t i = 0; i < 3; ++i) {
    const char letter = profile.colorOrder[i];
    channels_[i] = letter == 'R' ? 0 : letter == 'G' ? 1 : 2;
  }
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
